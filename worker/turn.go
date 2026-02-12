//go:build js && wasm

package main

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"net"
	"strconv"
	"strings"
	"syscall/js"
	"time"

	"github.com/kuuji/riftgate/worker/stun"
)

// TURN server constants.
const (
	turnRealm            = "riftgate"
	defaultAllocLifetime = 600  // 10 minutes
	maxAllocLifetime     = 3600 // 1 hour

	// Virtual relay addresses are assigned from this range.
	// These are synthetic — they never touch a real network. They exist only
	// so ICE candidates have an address to reference and so TURN clients
	// can Send/ChannelData to each other via these virtual addresses.
	relayBaseIP   = "10.255.0.0"
	relayBasePort = 50000
)

// allocation represents a single TURN allocation (one per TURN WebSocket connection).
type allocation struct {
	wsId      int
	username  string
	nonce     string
	authKey   []byte // MD5(username:realm:password)
	relayAddr stun.XORAddress
	createdAt time.Time
	lifetime  int // seconds

	// Permissions: set of allowed peer IPs (without port).
	permissions map[string]bool

	// Channel bindings: channel number <-> peer address.
	channels      map[uint16]string // channel# -> "ip:port"
	channelByAddr map[string]uint16 // "ip:port" -> channel#
}

// turnServer is the in-memory state for all TURN allocations.
var turnAllocations = make(map[int]*allocation) // wsId -> allocation

// relayByAddr maps virtual relay addresses to allocation wsIds, enabling
// forwarding from one peer's allocation to another.
var relayByAddr = make(map[string]int) // "ip:port" -> wsId

// nextRelayHost tracks the next virtual relay IP host octet to assign.
var nextRelayHost = 1

// sendBinaryFn is the JS function for sending binary data to a WebSocket.
// Set during init: jsSendBinary(wsId, Uint8Array).
var sendBinaryFn js.Value

// turnSecretFn is the JS function that returns the TURN_SECRET env var.
var turnSecretFn js.Value

// generateNonce creates a simple time-based nonce.
func generateNonce() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// getTURNSecret returns the TURN shared secret from the environment.
func getTURNSecret() string {
	return turnSecretFn.Invoke().String()
}

// validateTURNCredentials validates TURN REST API credentials.
// username format: "<unix_expiry>:<peerID>"
// password: base64(HMAC-SHA1(secret, username))
func validateTURNCredentials(username, password string) error {
	secret := getTURNSecret()
	if secret == "" {
		return fmt.Errorf("TURN_SECRET not configured")
	}

	parts := strings.SplitN(username, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid username format")
	}

	expiry, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return fmt.Errorf("invalid expiry in username")
	}

	if time.Now().Unix() > expiry {
		return fmt.Errorf("credentials expired")
	}

	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write([]byte(username))
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(password), []byte(expected)) {
		return fmt.Errorf("invalid password")
	}

	return nil
}

// deriveAuthKey computes MD5(username:realm:password) for MESSAGE-INTEGRITY.
func deriveAuthKey(username, realm, password string) []byte {
	h := md5.New()
	h.Write([]byte(username + ":" + realm + ":" + password))
	return h.Sum(nil)
}

// recomputePassword recomputes the TURN REST API password from the secret and username.
func recomputePassword(username string) string {
	secret := getTURNSecret()
	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write([]byte(username))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// assignRelayAddr assigns the next virtual relay address.
func assignRelayAddr() stun.XORAddress {
	base := net.ParseIP(relayBaseIP).To4()
	ip := make(net.IP, 4)
	copy(ip, base)
	ip[3] = byte(nextRelayHost)
	nextRelayHost++
	return stun.XORAddress{IP: ip, Port: relayBasePort}
}

// addrKey converts an XORAddress to a string key for map lookups.
func addrKey(addr stun.XORAddress) string {
	return fmt.Sprintf("%s:%d", addr.IP.String(), addr.Port)
}

// sendBinary sends raw bytes to a WebSocket via the JS bridge.
func sendBinary(wsId int, data []byte) {
	// Convert Go []byte to JS Uint8Array.
	jsArray := js.Global().Get("Uint8Array").New(len(data))
	js.CopyBytesToJS(jsArray, data)
	sendBinaryFn.Invoke(wsId, jsArray)
}

// jsOnTURNMessage handles binary STUN/TURN messages from a TURN WebSocket.
// Called from JS: goOnTURNMessage(wsId, Uint8Array)
func jsOnTURNMessage(_ js.Value, args []js.Value) any {
	wsId := args[0].Int()
	jsData := args[1]
	data := make([]byte, jsData.Get("length").Int())
	js.CopyBytesToGo(data, jsData)

	handleTURNMessage(wsId, data)
	return nil
}

// jsOnTURNClose handles a TURN WebSocket disconnecting.
func jsOnTURNClose(_ js.Value, args []js.Value) any {
	wsId := args[0].Int()
	removeTURNAllocation(wsId)
	return nil
}

// handleTURNMessage dispatches a STUN or ChannelData message.
func handleTURNMessage(wsId int, data []byte) {
	if stun.IsChannelData(data) {
		handleChannelData(wsId, data)
		return
	}

	if !stun.IsSTUN(data) {
		return // Unknown frame type — discard.
	}

	msg, err := stun.Parse(data)
	if err != nil {
		return
	}

	switch msg.Method {
	case stun.MethodBinding:
		handleBinding(wsId, &msg)
	case stun.MethodAllocate:
		handleAllocate(wsId, &msg, data)
	case stun.MethodRefresh:
		handleRefresh(wsId, &msg, data)
	case stun.MethodCreatePermission:
		handleCreatePermission(wsId, &msg, data)
	case stun.MethodChannelBind:
		handleChannelBind(wsId, &msg, data)
	case stun.MethodSend:
		handleSend(wsId, &msg)
	}
}

// handleBinding responds to a STUN Binding request with a reflexive address.
func handleBinding(wsId int, msg *stun.Message) {
	// Return a synthetic reflexive address — the client's actual IP is not
	// meaningful in our virtual relay, but pion expects a response.
	resp := stun.NewResponse(msg, stun.ClassSuccessResponse).
		AddXORAddress(stun.AttrXORMappedAddress, stun.XORAddress{
			IP:   net.IPv4(127, 0, 0, 1),
			Port: 1234,
		}).
		Build(nil)
	sendBinary(wsId, resp)
}

// handleAllocate processes an Allocate request (two-phase auth dance).
func handleAllocate(wsId int, msg *stun.Message, rawData []byte) {
	alloc, exists := turnAllocations[wsId]

	// Check if this is the unauthenticated first request.
	username := msg.GetUsername()
	if username == "" {
		// Phase 1: Reject with 401 + nonce + realm.
		nonce := generateNonce()

		// Store the nonce for this connection for later validation.
		if !exists {
			turnAllocations[wsId] = &allocation{
				wsId:  wsId,
				nonce: nonce,
			}
		} else {
			alloc.nonce = nonce
		}

		resp := stun.NewResponse(msg, stun.ClassErrorResponse).
			AddErrorCode(401, "Unauthorized").
			AddRealm(turnRealm).
			AddNonce(nonce).
			Build(nil)
		sendBinary(wsId, resp)
		return
	}

	// Phase 2: Authenticated request.
	if !exists {
		// No prior nonce exchange — reject.
		nonce := generateNonce()
		turnAllocations[wsId] = &allocation{wsId: wsId, nonce: nonce}
		resp := stun.NewResponse(msg, stun.ClassErrorResponse).
			AddErrorCode(401, "Unauthorized").
			AddRealm(turnRealm).
			AddNonce(nonce).
			Build(nil)
		sendBinary(wsId, resp)
		return
	}

	// Validate credentials.
	password := recomputePassword(username)
	if err := validateTURNCredentials(username, password); err != nil {
		// Invalid credentials.
		nonce := generateNonce()
		alloc.nonce = nonce
		resp := stun.NewResponse(msg, stun.ClassErrorResponse).
			AddErrorCode(401, "Unauthorized").
			AddRealm(turnRealm).
			AddNonce(nonce).
			Build(nil)
		sendBinary(wsId, resp)
		return
	}

	// Verify MESSAGE-INTEGRITY.
	authKey := deriveAuthKey(username, turnRealm, password)
	if err := stun.CheckIntegrity(rawData, authKey); err != nil {
		nonce := generateNonce()
		alloc.nonce = nonce
		resp := stun.NewResponse(msg, stun.ClassErrorResponse).
			AddErrorCode(401, "Unauthorized").
			AddRealm(turnRealm).
			AddNonce(nonce).
			Build(nil)
		sendBinary(wsId, resp)
		return
	}

	// Check if already allocated.
	if alloc.relayAddr.IP != nil {
		// Already allocated — reject with 437 (Allocation Mismatch).
		resp := stun.NewResponse(msg, stun.ClassErrorResponse).
			AddErrorCode(437, "Allocation Mismatch").
			Build(authKey)
		sendBinary(wsId, resp)
		return
	}

	// Create allocation.
	relayAddr := assignRelayAddr()
	lifetime := defaultAllocLifetime

	alloc.username = username
	alloc.authKey = authKey
	alloc.relayAddr = relayAddr
	alloc.createdAt = time.Now()
	alloc.lifetime = lifetime
	alloc.permissions = make(map[string]bool)
	alloc.channels = make(map[uint16]string)
	alloc.channelByAddr = make(map[string]uint16)

	relayByAddr[addrKey(relayAddr)] = wsId

	resp := stun.NewResponse(msg, stun.ClassSuccessResponse).
		AddXORAddress(stun.AttrXORRelayedAddress, relayAddr).
		AddXORAddress(stun.AttrXORMappedAddress, stun.XORAddress{
			IP:   net.IPv4(127, 0, 0, 1),
			Port: 1234,
		}).
		AddLifetime(uint32(lifetime)).
		Build(authKey)
	sendBinary(wsId, resp)
}

// handleRefresh processes a Refresh request (update or remove allocation).
func handleRefresh(wsId int, msg *stun.Message, rawData []byte) {
	alloc, exists := turnAllocations[wsId]
	if !exists || alloc.authKey == nil {
		resp := stun.NewResponse(msg, stun.ClassErrorResponse).
			AddErrorCode(437, "Allocation Mismatch").
			Build(nil)
		sendBinary(wsId, resp)
		return
	}

	// Verify MESSAGE-INTEGRITY.
	if err := stun.CheckIntegrity(rawData, alloc.authKey); err != nil {
		// Stale nonce — send 438.
		nonce := generateNonce()
		alloc.nonce = nonce
		resp := stun.NewResponse(msg, stun.ClassErrorResponse).
			AddErrorCode(438, "Stale Nonce").
			AddRealm(turnRealm).
			AddNonce(nonce).
			Build(nil)
		sendBinary(wsId, resp)
		return
	}

	requestedLifetime := msg.GetLifetime()

	if requestedLifetime == 0 {
		// Deallocate.
		resp := stun.NewResponse(msg, stun.ClassSuccessResponse).
			AddLifetime(0).
			Build(alloc.authKey)
		sendBinary(wsId, resp)
		removeTURNAllocation(wsId)
		return
	}

	// Cap lifetime.
	lifetime := int(requestedLifetime)
	if lifetime > maxAllocLifetime {
		lifetime = maxAllocLifetime
	}
	alloc.lifetime = lifetime

	resp := stun.NewResponse(msg, stun.ClassSuccessResponse).
		AddLifetime(uint32(lifetime)).
		Build(alloc.authKey)
	sendBinary(wsId, resp)
}

// handleCreatePermission installs permissions for peer IPs.
func handleCreatePermission(wsId int, msg *stun.Message, rawData []byte) {
	alloc, exists := turnAllocations[wsId]
	if !exists || alloc.authKey == nil {
		resp := stun.NewResponse(msg, stun.ClassErrorResponse).
			AddErrorCode(437, "Allocation Mismatch").
			Build(nil)
		sendBinary(wsId, resp)
		return
	}

	if err := stun.CheckIntegrity(rawData, alloc.authKey); err != nil {
		nonce := generateNonce()
		alloc.nonce = nonce
		resp := stun.NewResponse(msg, stun.ClassErrorResponse).
			AddErrorCode(438, "Stale Nonce").
			AddRealm(turnRealm).
			AddNonce(nonce).
			Build(nil)
		sendBinary(wsId, resp)
		return
	}

	// Extract all XOR-PEER-ADDRESS attributes.
	addrs := msg.GetXORPeerAddresses()
	for _, addr := range addrs {
		alloc.permissions[addr.IP.String()] = true
	}

	resp := stun.NewResponse(msg, stun.ClassSuccessResponse).
		Build(alloc.authKey)
	sendBinary(wsId, resp)
}

// handleChannelBind binds a channel number to a peer address.
func handleChannelBind(wsId int, msg *stun.Message, rawData []byte) {
	alloc, exists := turnAllocations[wsId]
	if !exists || alloc.authKey == nil {
		resp := stun.NewResponse(msg, stun.ClassErrorResponse).
			AddErrorCode(437, "Allocation Mismatch").
			Build(nil)
		sendBinary(wsId, resp)
		return
	}

	if err := stun.CheckIntegrity(rawData, alloc.authKey); err != nil {
		nonce := generateNonce()
		alloc.nonce = nonce
		resp := stun.NewResponse(msg, stun.ClassErrorResponse).
			AddErrorCode(438, "Stale Nonce").
			AddRealm(turnRealm).
			AddNonce(nonce).
			Build(nil)
		sendBinary(wsId, resp)
		return
	}

	channelNumber := msg.GetChannelNumber()
	if channelNumber < 0x4000 || channelNumber > 0x7FFF {
		resp := stun.NewResponse(msg, stun.ClassErrorResponse).
			AddErrorCode(400, "Bad Request").
			Build(alloc.authKey)
		sendBinary(wsId, resp)
		return
	}

	peerAddr, ok := msg.GetXORPeerAddress()
	if !ok {
		resp := stun.NewResponse(msg, stun.ClassErrorResponse).
			AddErrorCode(400, "Bad Request").
			Build(alloc.authKey)
		sendBinary(wsId, resp)
		return
	}

	peerKey := addrKey(peerAddr)

	// If this channel is already bound to a different address, reject.
	if existingAddr, bound := alloc.channels[channelNumber]; bound && existingAddr != peerKey {
		resp := stun.NewResponse(msg, stun.ClassErrorResponse).
			AddErrorCode(400, "Bad Request").
			Build(alloc.authKey)
		sendBinary(wsId, resp)
		return
	}

	alloc.channels[channelNumber] = peerKey
	alloc.channelByAddr[peerKey] = channelNumber

	// Also install a permission for this peer.
	alloc.permissions[peerAddr.IP.String()] = true

	resp := stun.NewResponse(msg, stun.ClassSuccessResponse).
		Build(alloc.authKey)
	sendBinary(wsId, resp)
}

// handleSend processes a Send indication — relay data to the target peer.
func handleSend(wsId int, msg *stun.Message) {
	alloc, exists := turnAllocations[wsId]
	if !exists || alloc.authKey == nil {
		return // Indications get no response.
	}

	peerAddr, ok := msg.GetXORPeerAddress()
	if !ok {
		return
	}

	data := msg.GetData()
	if data == nil {
		return
	}

	// Check permission.
	if !alloc.permissions[peerAddr.IP.String()] {
		return
	}

	// Find the target peer's allocation by their relay address.
	targetKey := addrKey(peerAddr)
	targetWsId, ok := relayByAddr[targetKey]
	if !ok {
		return
	}

	targetAlloc, ok := turnAllocations[targetWsId]
	if !ok {
		return
	}

	// Check if target has a channel binding for our relay address.
	ourKey := addrKey(alloc.relayAddr)
	if channelNum, bound := targetAlloc.channelByAddr[ourKey]; bound {
		// Send as ChannelData.
		frame := stun.BuildChannelData(channelNum, data)
		sendBinary(targetWsId, frame)
		return
	}

	// Send as Data indication.
	ind := stun.NewBuilder(stun.MethodData, stun.ClassIndication, msg.TransactionID).
		AddXORAddress(stun.AttrXORPeerAddress, alloc.relayAddr).
		AddData(data).
		BuildNoFingerprint(nil)
	sendBinary(targetWsId, ind)
}

// handleChannelData processes a ChannelData frame — fast-path relay.
func handleChannelData(wsId int, data []byte) {
	alloc, exists := turnAllocations[wsId]
	if !exists || alloc.authKey == nil {
		return
	}

	cd, err := stun.ParseChannelData(data)
	if err != nil {
		return
	}

	// Look up the peer address bound to this channel number.
	peerKey, ok := alloc.channels[cd.ChannelNumber]
	if !ok {
		return
	}

	// Find the target peer's allocation.
	targetWsId, ok := relayByAddr[peerKey]
	if !ok {
		return
	}

	targetAlloc, ok := turnAllocations[targetWsId]
	if !ok {
		return
	}

	// Forward as ChannelData using the target's channel binding for our address.
	ourKey := addrKey(alloc.relayAddr)
	if channelNum, bound := targetAlloc.channelByAddr[ourKey]; bound {
		frame := stun.BuildChannelData(channelNum, cd.Data)
		sendBinary(targetWsId, frame)
		return
	}

	// Fallback: send as Data indication.
	var txID [12]byte // Zero txID for indications is fine.
	ind := stun.NewBuilder(stun.MethodData, stun.ClassIndication, txID).
		AddXORAddress(stun.AttrXORPeerAddress, alloc.relayAddr).
		AddData(cd.Data).
		BuildNoFingerprint(nil)
	sendBinary(targetWsId, ind)
}

// removeTURNAllocation cleans up a TURN allocation.
func removeTURNAllocation(wsId int) {
	alloc, exists := turnAllocations[wsId]
	if !exists {
		return
	}

	if alloc.relayAddr.IP != nil {
		delete(relayByAddr, addrKey(alloc.relayAddr))
	}
	delete(turnAllocations, wsId)
}

// Package stun provides a minimal STUN/TURN message parser and builder for use in
// a TinyGo/Wasm TURN server. It implements only the subset of RFC 5389 (STUN) and
// RFC 5766 (TURN) needed to serve as a TURN relay for pion/ice clients.
//
// This package has zero external dependencies and is TinyGo-compatible.
package stun

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"net"
)

// STUN message header constants.
const (
	HeaderSize  = 20
	MagicCookie = 0x2112A442

	// Fingerprint XOR constant per RFC 5389.
	fingerprintXOR = 0x5354554E
)

// STUN message methods.
const (
	MethodBinding          = 0x001
	MethodAllocate         = 0x003
	MethodRefresh          = 0x004
	MethodSend             = 0x006
	MethodData             = 0x007
	MethodCreatePermission = 0x008
	MethodChannelBind      = 0x009
)

// STUN message classes.
const (
	ClassRequest         = 0x00
	ClassIndication      = 0x01
	ClassSuccessResponse = 0x02
	ClassErrorResponse   = 0x03
)

// STUN attribute types.
const (
	AttrMappedAddress      = 0x0001
	AttrUsername           = 0x0006
	AttrMessageIntegrity   = 0x0008
	AttrErrorCode          = 0x0009
	AttrChannelNumber      = 0x000C
	AttrLifetime           = 0x000D
	AttrXORPeerAddress     = 0x0012
	AttrData               = 0x0013
	AttrRealm              = 0x0014
	AttrNonce              = 0x0015
	AttrXORRelayedAddress  = 0x0016
	AttrRequestedTransport = 0x0019
	AttrXORMappedAddress   = 0x0020
	AttrFingerprint        = 0x8028
	AttrSoftware           = 0x8022
)

// Address families.
const (
	FamilyIPv4 = 0x01
	FamilyIPv6 = 0x02
)

// MessageType encodes a STUN method and class into the 16-bit type field.
// The encoding is non-trivial: bits are interleaved per RFC 5389 Section 6.
//
//	Bits: M11 M10 M9 M8 M7 C1 M6 M5 M4 C0 M3 M2 M1 M0
func MessageType(method, class int) uint16 {
	// method bits: M0-M3 in bits 0-3, M4-M6 in bits 5-7, M7-M11 in bits 9-13
	// class bits: C0 in bit 4, C1 in bit 8
	m := uint16(method)
	c := uint16(class)
	return (m & 0x0F) | ((c & 0x01) << 4) | ((m & 0x70) << 1) | ((c & 0x02) << 7) | ((m & 0xF80) << 2)
}

// ParseType extracts the method and class from a STUN message type.
func ParseType(t uint16) (method, class int) {
	method = int((t & 0x0F) | ((t >> 1) & 0x70) | ((t >> 2) & 0xF80))
	class = int(((t >> 4) & 0x01) | ((t >> 7) & 0x02))
	return method, class
}

// Message represents a parsed STUN message.
type Message struct {
	Method        int
	Class         int
	TransactionID [12]byte
	Attributes    []Attribute
}

// Attribute is a STUN attribute (type-length-value).
type Attribute struct {
	Type  uint16
	Value []byte
}

// IsChannelData returns true if the raw data starts with a ChannelData header
// (first two bytes are in the range 0x4000-0x7FFF).
func IsChannelData(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	ch := binary.BigEndian.Uint16(data[0:2])
	return ch >= 0x4000 && ch <= 0x7FFF
}

// IsSTUN returns true if the data looks like a STUN message
// (first two bits are 0, and bytes 4-7 are the magic cookie).
func IsSTUN(data []byte) bool {
	if len(data) < HeaderSize {
		return false
	}
	// First two bits must be 0.
	if data[0]&0xC0 != 0 {
		return false
	}
	cookie := binary.BigEndian.Uint32(data[4:8])
	return cookie == MagicCookie
}

// ChannelData represents a parsed ChannelData frame.
type ChannelData struct {
	ChannelNumber uint16
	Data          []byte
}

// ParseChannelData parses a ChannelData frame from raw bytes.
func ParseChannelData(data []byte) (ChannelData, error) {
	if len(data) < 4 {
		return ChannelData{}, fmt.Errorf("channel data too short: %d bytes", len(data))
	}
	ch := binary.BigEndian.Uint16(data[0:2])
	length := binary.BigEndian.Uint16(data[2:4])
	if int(length) > len(data)-4 {
		return ChannelData{}, fmt.Errorf("channel data length %d exceeds available %d", length, len(data)-4)
	}
	return ChannelData{
		ChannelNumber: ch,
		Data:          data[4 : 4+length],
	}, nil
}

// BuildChannelData constructs a ChannelData frame.
func BuildChannelData(channelNumber uint16, payload []byte) []byte {
	// 4-byte header + payload, padded to 4-byte boundary.
	padded := (len(payload) + 3) & ^3
	buf := make([]byte, 4+padded)
	binary.BigEndian.PutUint16(buf[0:2], channelNumber)
	binary.BigEndian.PutUint16(buf[2:4], uint16(len(payload)))
	copy(buf[4:], payload)
	return buf
}

// Parse parses a STUN message from raw bytes. It does not validate
// MESSAGE-INTEGRITY or FINGERPRINT — use CheckIntegrity and CheckFingerprint
// for that.
func Parse(data []byte) (Message, error) {
	if len(data) < HeaderSize {
		return Message{}, fmt.Errorf("message too short: %d bytes", len(data))
	}

	msgType := binary.BigEndian.Uint16(data[0:2])
	msgLen := binary.BigEndian.Uint16(data[2:4])
	cookie := binary.BigEndian.Uint32(data[4:8])

	if cookie != MagicCookie {
		return Message{}, fmt.Errorf("bad magic cookie: %#x", cookie)
	}
	if int(msgLen)+HeaderSize > len(data) {
		return Message{}, fmt.Errorf("message length %d exceeds available %d", msgLen, len(data)-HeaderSize)
	}

	method, class := ParseType(msgType)

	var txID [12]byte
	copy(txID[:], data[8:20])

	msg := Message{
		Method:        method,
		Class:         class,
		TransactionID: txID,
	}

	// Parse attributes.
	offset := HeaderSize
	end := HeaderSize + int(msgLen)
	for offset+4 <= end {
		attrType := binary.BigEndian.Uint16(data[offset : offset+2])
		attrLen := binary.BigEndian.Uint16(data[offset+2 : offset+4])
		if offset+4+int(attrLen) > end {
			return Message{}, fmt.Errorf("attribute %#x length %d exceeds message", attrType, attrLen)
		}
		value := make([]byte, attrLen)
		copy(value, data[offset+4:offset+4+int(attrLen)])
		msg.Attributes = append(msg.Attributes, Attribute{Type: attrType, Value: value})
		// Advance past attribute with padding to 4-byte boundary.
		offset += 4 + ((int(attrLen) + 3) & ^3)
	}

	return msg, nil
}

// GetAttr returns the first attribute with the given type, or nil if not found.
func (m *Message) GetAttr(attrType uint16) []byte {
	for _, a := range m.Attributes {
		if a.Type == attrType {
			return a.Value
		}
	}
	return nil
}

// GetAttrs returns all attributes with the given type.
func (m *Message) GetAttrs(attrType uint16) [][]byte {
	var result [][]byte
	for _, a := range m.Attributes {
		if a.Type == attrType {
			result = append(result, a.Value)
		}
	}
	return result
}

// GetUsername returns the USERNAME attribute as a string.
func (m *Message) GetUsername() string {
	v := m.GetAttr(AttrUsername)
	if v == nil {
		return ""
	}
	return string(v)
}

// GetRealm returns the REALM attribute as a string.
func (m *Message) GetRealm() string {
	v := m.GetAttr(AttrRealm)
	if v == nil {
		return ""
	}
	return string(v)
}

// GetNonce returns the NONCE attribute as a string.
func (m *Message) GetNonce() string {
	v := m.GetAttr(AttrNonce)
	if v == nil {
		return ""
	}
	return string(v)
}

// GetLifetime returns the LIFETIME attribute in seconds, or 0 if not present.
func (m *Message) GetLifetime() uint32 {
	v := m.GetAttr(AttrLifetime)
	if v == nil || len(v) < 4 {
		return 0
	}
	return binary.BigEndian.Uint32(v)
}

// GetRequestedTransport returns the requested transport protocol number, or 0 if not present.
func (m *Message) GetRequestedTransport() byte {
	v := m.GetAttr(AttrRequestedTransport)
	if v == nil || len(v) < 1 {
		return 0
	}
	return v[0]
}

// GetChannelNumber returns the CHANNEL-NUMBER attribute, or 0 if not present.
func (m *Message) GetChannelNumber() uint16 {
	v := m.GetAttr(AttrChannelNumber)
	if v == nil || len(v) < 2 {
		return 0
	}
	return binary.BigEndian.Uint16(v)
}

// GetData returns the DATA attribute.
func (m *Message) GetData() []byte {
	return m.GetAttr(AttrData)
}

// XORAddress represents a decoded XOR-MAPPED-ADDRESS or similar.
type XORAddress struct {
	IP   net.IP
	Port int
}

// GetXORPeerAddress decodes the first XOR-PEER-ADDRESS attribute.
func (m *Message) GetXORPeerAddress() (XORAddress, bool) {
	v := m.GetAttr(AttrXORPeerAddress)
	if v == nil {
		return XORAddress{}, false
	}
	return decodeXORAddress(v, m.TransactionID), true
}

// GetXORPeerAddresses decodes all XOR-PEER-ADDRESS attributes.
func (m *Message) GetXORPeerAddresses() []XORAddress {
	vals := m.GetAttrs(AttrXORPeerAddress)
	addrs := make([]XORAddress, 0, len(vals))
	for _, v := range vals {
		addrs = append(addrs, decodeXORAddress(v, m.TransactionID))
	}
	return addrs
}

// decodeXORAddress decodes an XOR-MAPPED-ADDRESS family attribute value.
// Format: 1 reserved byte, 1 family byte, 2 XOR'd port bytes, 4 or 16 XOR'd IP bytes.
func decodeXORAddress(value []byte, txID [12]byte) XORAddress {
	if len(value) < 4 {
		return XORAddress{}
	}
	family := value[1]
	xorPort := binary.BigEndian.Uint16(value[2:4])
	port := int(xorPort ^ uint16(MagicCookie>>16))

	var ip net.IP
	switch family {
	case FamilyIPv4:
		if len(value) < 8 {
			return XORAddress{}
		}
		ip = make(net.IP, 4)
		cookieBytes := [4]byte{}
		binary.BigEndian.PutUint32(cookieBytes[:], MagicCookie)
		for i := 0; i < 4; i++ {
			ip[i] = value[4+i] ^ cookieBytes[i]
		}
	case FamilyIPv6:
		if len(value) < 20 {
			return XORAddress{}
		}
		ip = make(net.IP, 16)
		// XOR with magic cookie (4 bytes) + transaction ID (12 bytes) = 16 bytes.
		cookieBytes := [4]byte{}
		binary.BigEndian.PutUint32(cookieBytes[:], MagicCookie)
		for i := 0; i < 4; i++ {
			ip[i] = value[4+i] ^ cookieBytes[i]
		}
		for i := 0; i < 12; i++ {
			ip[4+i] = value[8+i] ^ txID[i]
		}
	}

	return XORAddress{IP: ip, Port: port}
}

// Builder constructs a STUN message.
type Builder struct {
	method int
	class  int
	txID   [12]byte
	attrs  []byte
}

// NewBuilder creates a Builder for a STUN message with the given method, class,
// and transaction ID.
func NewBuilder(method, class int, txID [12]byte) *Builder {
	return &Builder{method: method, class: class, txID: txID}
}

// NewResponse creates a Builder for a response to the given request message.
func NewResponse(req *Message, class int) *Builder {
	return NewBuilder(req.Method, class, req.TransactionID)
}

// AddRaw adds a raw attribute with the given type and value.
func (b *Builder) AddRaw(attrType uint16, value []byte) *Builder {
	var hdr [4]byte
	binary.BigEndian.PutUint16(hdr[0:2], attrType)
	binary.BigEndian.PutUint16(hdr[2:4], uint16(len(value)))
	b.attrs = append(b.attrs, hdr[:]...)
	b.attrs = append(b.attrs, value...)
	// Pad to 4-byte boundary.
	if pad := (4 - len(value)%4) % 4; pad > 0 {
		b.attrs = append(b.attrs, make([]byte, pad)...)
	}
	return b
}

// AddString adds a string attribute.
func (b *Builder) AddString(attrType uint16, s string) *Builder {
	return b.AddRaw(attrType, []byte(s))
}

// AddUsername adds a USERNAME attribute.
func (b *Builder) AddUsername(username string) *Builder {
	return b.AddString(AttrUsername, username)
}

// AddRealm adds a REALM attribute.
func (b *Builder) AddRealm(realm string) *Builder {
	return b.AddString(AttrRealm, realm)
}

// AddNonce adds a NONCE attribute.
func (b *Builder) AddNonce(nonce string) *Builder {
	return b.AddString(AttrNonce, nonce)
}

// AddLifetime adds a LIFETIME attribute (seconds).
func (b *Builder) AddLifetime(seconds uint32) *Builder {
	var v [4]byte
	binary.BigEndian.PutUint32(v[:], seconds)
	return b.AddRaw(AttrLifetime, v[:])
}

// AddErrorCode adds an ERROR-CODE attribute.
func (b *Builder) AddErrorCode(code int, reason string) *Builder {
	classDigit := byte(code / 100)
	numberDigit := byte(code % 100)
	value := make([]byte, 4+len(reason))
	value[2] = classDigit
	value[3] = numberDigit
	copy(value[4:], reason)
	return b.AddRaw(AttrErrorCode, value)
}

// AddXORAddress adds an XOR-encoded address attribute (used for XOR-MAPPED-ADDRESS,
// XOR-RELAYED-ADDRESS, XOR-PEER-ADDRESS).
func (b *Builder) AddXORAddress(attrType uint16, addr XORAddress) *Builder {
	ip4 := addr.IP.To4()
	if ip4 != nil {
		value := make([]byte, 8)
		value[1] = FamilyIPv4
		binary.BigEndian.PutUint16(value[2:4], uint16(addr.Port)^uint16(MagicCookie>>16))
		cookieBytes := [4]byte{}
		binary.BigEndian.PutUint32(cookieBytes[:], MagicCookie)
		for i := 0; i < 4; i++ {
			value[4+i] = ip4[i] ^ cookieBytes[i]
		}
		return b.AddRaw(attrType, value)
	}

	// IPv6
	ip6 := addr.IP.To16()
	if ip6 == nil {
		return b
	}
	value := make([]byte, 20)
	value[1] = FamilyIPv6
	binary.BigEndian.PutUint16(value[2:4], uint16(addr.Port)^uint16(MagicCookie>>16))
	cookieBytes := [4]byte{}
	binary.BigEndian.PutUint32(cookieBytes[:], MagicCookie)
	for i := 0; i < 4; i++ {
		value[4+i] = ip6[i] ^ cookieBytes[i]
	}
	for i := 0; i < 12; i++ {
		value[8+i] = ip6[4+i] ^ b.txID[i]
	}
	return b.AddRaw(attrType, value)
}

// AddData adds a DATA attribute.
func (b *Builder) AddData(data []byte) *Builder {
	return b.AddRaw(AttrData, data)
}

// AddChannelNumber adds a CHANNEL-NUMBER attribute.
func (b *Builder) AddChannelNumber(ch uint16) *Builder {
	var v [4]byte
	binary.BigEndian.PutUint16(v[0:2], ch)
	return b.AddRaw(AttrChannelNumber, v[:])
}

// Build constructs the final STUN message bytes. If authKey is non-nil,
// MESSAGE-INTEGRITY and FINGERPRINT are appended.
func (b *Builder) Build(authKey []byte) []byte {
	attrsLen := len(b.attrs)

	// If adding MESSAGE-INTEGRITY (24 bytes: 4 header + 20 HMAC) + FINGERPRINT (8 bytes: 4 header + 4 CRC32).
	if authKey != nil {
		attrsLen += 24 + 8
	} else {
		attrsLen += 8 // Just FINGERPRINT
	}

	buf := make([]byte, HeaderSize+len(b.attrs))
	binary.BigEndian.PutUint16(buf[0:2], MessageType(b.method, b.class))
	// Length will be updated below.
	binary.BigEndian.PutUint32(buf[4:8], MagicCookie)
	copy(buf[8:20], b.txID[:])
	copy(buf[20:], b.attrs)

	if authKey != nil {
		// Set length to include everything up through MESSAGE-INTEGRITY (but not FINGERPRINT).
		binary.BigEndian.PutUint16(buf[2:4], uint16(len(b.attrs)+24))
		mac := hmac.New(sha1.New, authKey)
		mac.Write(buf)
		integrity := mac.Sum(nil) // 20 bytes
		var miHeader [4]byte
		binary.BigEndian.PutUint16(miHeader[0:2], AttrMessageIntegrity)
		binary.BigEndian.PutUint16(miHeader[2:4], 20)
		buf = append(buf, miHeader[:]...)
		buf = append(buf, integrity...)
	}

	// FINGERPRINT: set length to include everything through FINGERPRINT.
	binary.BigEndian.PutUint16(buf[2:4], uint16(len(buf)-HeaderSize+8))
	crc := crc32.ChecksumIEEE(buf) ^ fingerprintXOR
	var fpHeader [4]byte
	binary.BigEndian.PutUint16(fpHeader[0:2], AttrFingerprint)
	binary.BigEndian.PutUint16(fpHeader[2:4], 4)
	buf = append(buf, fpHeader[:]...)
	var fpValue [4]byte
	binary.BigEndian.PutUint32(fpValue[:], crc)
	buf = append(buf, fpValue[:]...)

	return buf
}

// BuildNoFingerprint constructs the message without FINGERPRINT. Used for
// indications and cases where FINGERPRINT is not needed.
func (b *Builder) BuildNoFingerprint(authKey []byte) []byte {
	buf := make([]byte, HeaderSize+len(b.attrs))
	binary.BigEndian.PutUint16(buf[0:2], MessageType(b.method, b.class))
	binary.BigEndian.PutUint32(buf[4:8], MagicCookie)
	copy(buf[8:20], b.txID[:])
	copy(buf[20:], b.attrs)

	if authKey != nil {
		binary.BigEndian.PutUint16(buf[2:4], uint16(len(b.attrs)+24))
		mac := hmac.New(sha1.New, authKey)
		mac.Write(buf)
		integrity := mac.Sum(nil)
		var miHeader [4]byte
		binary.BigEndian.PutUint16(miHeader[0:2], AttrMessageIntegrity)
		binary.BigEndian.PutUint16(miHeader[2:4], 20)
		buf = append(buf, miHeader[:]...)
		buf = append(buf, integrity...)
	}

	// Set final length.
	binary.BigEndian.PutUint16(buf[2:4], uint16(len(buf)-HeaderSize))
	return buf
}

// CheckIntegrity validates the MESSAGE-INTEGRITY attribute of a raw STUN message
// against the given auth key. Returns nil if valid, error otherwise.
func CheckIntegrity(data []byte, authKey []byte) error {
	// Find the MESSAGE-INTEGRITY attribute offset.
	if len(data) < HeaderSize {
		return fmt.Errorf("message too short")
	}

	miOffset := -1
	offset := HeaderSize
	msgLen := int(binary.BigEndian.Uint16(data[2:4]))
	end := HeaderSize + msgLen
	if end > len(data) {
		end = len(data)
	}

	for offset+4 <= end {
		attrType := binary.BigEndian.Uint16(data[offset : offset+2])
		attrLen := int(binary.BigEndian.Uint16(data[offset+2 : offset+4]))
		if attrType == AttrMessageIntegrity {
			miOffset = offset
			break
		}
		offset += 4 + ((attrLen + 3) & ^3)
	}

	if miOffset < 0 {
		return fmt.Errorf("no MESSAGE-INTEGRITY attribute")
	}

	if miOffset+4+20 > len(data) {
		return fmt.Errorf("MESSAGE-INTEGRITY attribute truncated")
	}

	// The HMAC covers the STUN message up to and including the MI attribute header,
	// with the Length field adjusted to end at the MI attribute (before FINGERPRINT).
	// Make a copy of the header + everything before MI.
	hashData := make([]byte, miOffset)
	copy(hashData, data[:miOffset])
	// Adjust the STUN message Length field to include up through MESSAGE-INTEGRITY (header + 20 bytes).
	binary.BigEndian.PutUint16(hashData[2:4], uint16(miOffset-HeaderSize+4+20))

	mac := hmac.New(sha1.New, authKey)
	mac.Write(hashData)
	expected := mac.Sum(nil)

	actual := data[miOffset+4 : miOffset+4+20]
	if !hmac.Equal(expected, actual) {
		return fmt.Errorf("MESSAGE-INTEGRITY mismatch")
	}

	return nil
}

// CheckFingerprint validates the FINGERPRINT attribute of a raw STUN message.
func CheckFingerprint(data []byte) error {
	if len(data) < HeaderSize+8 {
		return fmt.Errorf("message too short for fingerprint")
	}

	// FINGERPRINT is always the last attribute.
	fpOffset := len(data) - 8
	attrType := binary.BigEndian.Uint16(data[fpOffset : fpOffset+2])
	if attrType != AttrFingerprint {
		return fmt.Errorf("last attribute is not FINGERPRINT: %#x", attrType)
	}

	expected := crc32.ChecksumIEEE(data[:fpOffset]) ^ fingerprintXOR
	actual := binary.BigEndian.Uint32(data[fpOffset+4 : fpOffset+8])

	if expected != actual {
		return fmt.Errorf("FINGERPRINT mismatch: expected %#x, got %#x", expected, actual)
	}

	return nil
}

// DeriveAuthKey computes the long-term credential key: MD5(username:realm:password).
// This is the same as internal/turn.DeriveAuthKey but duplicated here to avoid
// importing the client module from the worker module.
func DeriveAuthKey(username, realm, password string) []byte {
	h := md5.New() //nolint:gosec
	h.Write([]byte(username + ":" + realm + ":" + password))
	return h.Sum(nil)
}

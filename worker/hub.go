//go:build js && wasm

package main

import (
	"encoding/json"
	"syscall/js"
)

// peer represents a connected WebSocket peer in the signaling hub.
type peer struct {
	wsId      int
	peerID    string
	publicKey string
	address   string
	routes    []string
}

// peers tracks all connected peers by their WebSocket ID.
var peers = make(map[int]*peer)

// peerByID maps peer IDs to WebSocket IDs for fast lookups.
var peerByID = make(map[string]int)

// peerInfo is the JSON representation of a peer in the peers list.
type peerInfo struct {
	PeerID    string   `json:"peerId"`
	PublicKey string   `json:"publicKey"`
	Address   string   `json:"address,omitempty"`
	Routes    []string `json:"routes,omitempty"`
}

// send sends a JSON message to a specific WebSocket by ID.
func send(wsId int, data []byte) {
	sendFn.Invoke(wsId, string(data))
}

// broadcast sends a JSON message to all connected peers except the sender.
func broadcast(senderWsId int, data []byte) {
	msg := string(data)
	for _, p := range peers {
		if p.wsId == senderWsId {
			continue
		}
		sendFn.Invoke(p.wsId, msg)
	}
}

// jsOnRehydrate is called by JS to silently restore a peer after hibernation.
// Unlike jsOnJoin, it does NOT send a peers list or broadcast a join notification.
// Arguments: wsId (int), peerId (string), publicKey (string), address (string), routesJSON (string)
func jsOnRehydrate(_ js.Value, args []js.Value) any {
	wsId := args[0].Int()
	peerID := args[1].String()
	publicKey := args[2].String()
	address := args[3].String()
	routes := parseRoutesJSON(args[4].String())

	peers[wsId] = &peer{
		wsId:      wsId,
		peerID:    peerID,
		publicKey: publicKey,
		address:   address,
		routes:    routes,
	}
	peerByID[peerID] = wsId

	return nil
}

// jsOnJoin is called by JS when a new peer sends a join message.
// Arguments: wsId (int), peerId (string), publicKey (string), address (string), routesJSON (string)
func jsOnJoin(_ js.Value, args []js.Value) any {
	wsId := args[0].Int()
	peerID := args[1].String()
	publicKey := args[2].String()
	address := args[3].String()
	routes := parseRoutesJSON(args[4].String())

	p := &peer{
		wsId:      wsId,
		peerID:    peerID,
		publicKey: publicKey,
		address:   address,
		routes:    routes,
	}

	// Build the current peers list before adding the new peer.
	peerInfos := make([]peerInfo, 0, len(peers))
	for _, existing := range peers {
		peerInfos = append(peerInfos, peerInfo{
			PeerID:    existing.peerID,
			PublicKey: existing.publicKey,
			Address:   existing.address,
			Routes:    existing.routes,
		})
	}

	// Add the new peer.
	peers[wsId] = p
	peerByID[peerID] = wsId

	// Send the current peers list to the new peer.
	peersMsg, _ := json.Marshal(map[string]any{
		"type":  "peers",
		"peers": peerInfos,
	})
	send(wsId, peersMsg)

	// Notify existing peers about the new arrival.
	newPeerMsg, _ := json.Marshal(map[string]any{
		"type": "peers",
		"peers": []peerInfo{{
			PeerID:    peerID,
			PublicKey: publicKey,
			Address:   address,
			Routes:    routes,
		}},
	})
	broadcast(wsId, newPeerMsg)

	return nil
}

// jsOnMessage is called by JS when a peer sends a signaling message.
// Arguments: wsId (int), rawJSON (string)
func jsOnMessage(_ js.Value, args []js.Value) any {
	rawJSON := args[1].String()

	// Parse just the routing envelope to determine the target.
	var env struct {
		Type string `json:"type"`
		To   string `json:"to"`
	}
	if err := json.Unmarshal([]byte(rawJSON), &env); err != nil {
		return nil
	}

	switch env.Type {
	case "offer", "answer", "ice-candidate":
		targetWsId, ok := peerByID[env.To]
		if ok {
			send(targetWsId, []byte(rawJSON))
		}
	}

	return nil
}

// parseRoutesJSON decodes a JSON array of route strings. Returns nil if the
// input is empty or invalid.
func parseRoutesJSON(s string) []string {
	if s == "" || s == "[]" {
		return nil
	}
	var routes []string
	if err := json.Unmarshal([]byte(s), &routes); err != nil {
		return nil
	}
	return routes
}

// jsOnLeave is called by JS when a peer disconnects.
// Arguments: wsId (int)
func jsOnLeave(_ js.Value, args []js.Value) any {
	wsId := args[0].Int()

	p, ok := peers[wsId]
	if !ok {
		return nil
	}

	peerID := p.peerID
	delete(peers, wsId)
	delete(peerByID, peerID)

	// Notify remaining peers about the departure.
	leftMsg, _ := json.Marshal(map[string]any{
		"type":   "peer-left",
		"peerId": peerID,
	})
	broadcast(-1, leftMsg)

	return nil
}

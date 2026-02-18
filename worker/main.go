//go:build js && wasm

// Package main implements the bamgate signaling hub and TURN relay for
// Cloudflare Workers. It is compiled to WebAssembly via TinyGo and bridged
// to the JavaScript Durable Object class via syscall/js callbacks.
package main

import (
	"syscall/js"
)

// sendFn is the JavaScript function used to send JSON messages to WebSockets.
// Set during init by the JS glue layer: sendFn(wsId, jsonString).
var sendFn js.Value

func main() {
	// Register Go callbacks that the JS Durable Object class will call.
	// Signaling callbacks:
	js.Global().Set("goOnRehydrate", js.FuncOf(jsOnRehydrate))
	js.Global().Set("goOnJoin", js.FuncOf(jsOnJoin))
	js.Global().Set("goOnMessage", js.FuncOf(jsOnMessage))
	js.Global().Set("goOnLeave", js.FuncOf(jsOnLeave))

	// TURN relay callbacks:
	js.Global().Set("goOnTURNMessage", js.FuncOf(jsOnTURNMessage))
	js.Global().Set("goOnTURNClose", js.FuncOf(jsOnTURNClose))
	js.Global().Set("goOnTURNRehydrate", js.FuncOf(jsOnTURNRehydrate))

	// Store the JS send functions for Go → JS calls.
	// jsSend(wsId, jsonString) — for signaling (text messages).
	sendFn = js.Global().Get("jsSend")
	// jsSendBinary(wsId, Uint8Array) — for TURN relay (binary messages).
	sendBinaryFn = js.Global().Get("jsSendBinary")
	// jsTURNSecret() — returns the TURN_SECRET env var.
	turnSecretFn = js.Global().Get("jsTURNSecret")
	// jsSaveTURNAllocation(wsId, jsonString) — persists TURN allocation to WS attachment.
	saveTURNAllocFn = js.Global().Get("jsSaveTURNAllocation")

	// Signal to the JS side that Go is ready.
	js.Global().Call("goReady")

	// Block forever — the Wasm module stays alive for the lifetime of the DO.
	select {}
}

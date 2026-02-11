//go:build js && wasm

// Package main implements the riftgate signaling hub for Cloudflare Workers.
// It is compiled to WebAssembly via TinyGo and bridged to the JavaScript
// Durable Object class via syscall/js callbacks.
package main

import (
	"syscall/js"
)

// sendFn is the JavaScript function used to send messages to WebSockets.
// Set during init by the JS glue layer: sendFn(wsId, jsonString).
var sendFn js.Value

func main() {
	// Register Go callbacks that the JS Durable Object class will call.
	js.Global().Set("goOnJoin", js.FuncOf(jsOnJoin))
	js.Global().Set("goOnMessage", js.FuncOf(jsOnMessage))
	js.Global().Set("goOnLeave", js.FuncOf(jsOnLeave))

	// Store the JS send function for Go → JS calls.
	// The JS glue sets globalThis.jsSend before instantiating the Wasm module.
	sendFn = js.Global().Get("jsSend")

	// Signal to the JS side that Go is ready.
	js.Global().Call("goReady")

	// Block forever — the Wasm module stays alive for the lifetime of the DO.
	select {}
}

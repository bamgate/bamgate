package deploy

import "embed"

// assets holds the pre-built worker files that are embedded into the riftgate
// binary at compile time. These are uploaded to Cloudflare Workers during
// `riftgate setup`.
//
//go:embed assets/worker.mjs assets/app.wasm assets/wasm_exec.js
var assets embed.FS

// WorkerModules returns the embedded worker files as deploy-ready modules.
func WorkerModules() ([]WorkerModule, error) {
	workerMJS, err := assets.ReadFile("assets/worker.mjs")
	if err != nil {
		return nil, err
	}

	appWasm, err := assets.ReadFile("assets/app.wasm")
	if err != nil {
		return nil, err
	}

	wasmExecJS, err := assets.ReadFile("assets/wasm_exec.js")
	if err != nil {
		return nil, err
	}

	return []WorkerModule{
		{Name: "worker.mjs", Data: workerMJS, ContentType: "application/javascript+module"},
		{Name: "app.wasm", Data: appWasm, ContentType: "application/wasm"},
		{Name: "wasm_exec.js", Data: wasmExecJS, ContentType: "application/javascript+module"},
	}, nil
}

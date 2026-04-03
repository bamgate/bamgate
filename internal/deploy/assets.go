package deploy

import "embed"

// assets holds the pre-built worker files that are embedded into the bamgate
// binary at compile time. These are uploaded to Cloudflare Workers during
// `bamgate setup`.
//
//go:embed assets/worker.mjs
var assets embed.FS

// WorkerModules returns the embedded worker files as deploy-ready modules.
func WorkerModules() ([]WorkerModule, error) {
	workerMJS, err := assets.ReadFile("assets/worker.mjs")
	if err != nil {
		return nil, err
	}

	return []WorkerModule{
		{Name: "worker.mjs", Data: workerMJS, ContentType: "application/javascript+module"},
	}, nil
}

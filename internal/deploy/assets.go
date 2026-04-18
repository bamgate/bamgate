package deploy

import "embed"

// assets holds the pre-built worker file that is embedded into the bamgate
// binary at compile time. It is uploaded to Cloudflare Workers during
// `bamgate setup`.
//
//go:embed assets/worker.mjs
var assets embed.FS

// WorkerModules returns the embedded worker file as a deploy-ready module.
func WorkerModules() ([]WorkerModule, error) {
	workerMJS, err := assets.ReadFile("assets/worker.mjs")
	if err != nil {
		return nil, err
	}

	return []WorkerModule{
		{Name: "worker.mjs", Data: workerMJS, ContentType: "application/javascript+module"},
	}, nil
}

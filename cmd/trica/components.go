package main

import (
	"github.com/VargaFoundation/trica/internal/server"
	"github.com/VargaFoundation/trica/internal/wiring"
)

// components lists what the server mounts besides health and metrics: the
// REST API, the MCP server, the web UI, the cabling deducer, the mTLS
// transport (when configured) and the device type library (ADR 0025).
func components(cfg *server.Config) []server.Component {
	return wiring.New(cfg, version).Components()
}

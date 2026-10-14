package main

import "github.com/VargaFoundation/trica/internal/server"

// components lists what the server mounts besides health and metrics. The
// API, the MCP server, the transport and the deducer register here as they
// land on main.
func components() []server.Component {
	return nil
}

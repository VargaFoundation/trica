// Package hostagent registers the host agent (trica-agent, internal/agent) as
// a collector module, so that the API and the Collectors pages list it next to
// the collectors that run from a targets file (ADR 0055).
//
// It only describes the agent: it has no factory, since nothing configures
// the agent as a target. It must not import internal/agent, so that the
// server binary does not link the agent.
package hostagent

import (
	"github.com/VargaFoundation/trica/internal/collect"
	"github.com/VargaFoundation/trica/internal/schema"
)

// Kind is the source prefix of the agent ("agent:<hostname>").
const Kind = "agent"

// Descriptor is what the module says about itself.
var Descriptor = collect.Descriptor{
	Kind:       Kind,
	Title:      "Linux host agent",
	Vendors:    []string{"Varga Foundation"},
	Protocol:   "Local probes (/proc, /sys, package databases, netlink), sent to the server over mutual TLS",
	Summary:    schema.Label{EN: "trica-agent, installed on a Linux host: CPUs, disks, network interfaces, services, listeners, packages and virtual machines.", FR: "trica-agent, installé sur un hôte Linux : CPU, disques, interfaces réseau, services, ports en écoute, paquets et machines virtuelles."},
	Deployment: collect.DeploymentAgent,
	Collects:   []string{"cpu", "device", "disk", "interface", "ip_address", "listener", "service", "software", "vm"},
	ReadOnly:   "Reads files and runs a fixed list of read-only commands on its own host; it never writes to the host it inventories.",
	Tested:     []string{"Debian 12", "RHEL 9"},
	Evidence:   collect.EvidenceSynthesised,
	Docs:       "docs/reference/agent.md",
}

func init() { collect.Register(Descriptor, nil) }

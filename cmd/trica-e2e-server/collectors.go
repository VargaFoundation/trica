package main

import (
	"net/http"
	"strings"
)

// The collectors the mock server describes (ADR 0055): a module with two
// instances, the host agent, and a module (unifi) with no instance that the
// UI code has never heard of. Written by hand, in the shape of the API.

func moduleAristaDoc() obj {
	return obj{
		"kind": "arista", "title": "Arista EOS", "vendors": []string{"Arista Networks"}, "protocol": "eAPI: JSON-RPC 2.0 over HTTP(S)",
		"summary":    obj{"en": "Arista switches: interfaces, VLANs, transceivers.", "fr": "Commutateurs Arista : interfaces, VLAN, transceivers."},
		"deployment": "target", "collects": []string{"device", "psu"}, "read_only": "Sends show commands only, through a network-operator account.",
		"address":     "A bare host (https), http://host or https://host[:port].",
		"credentials": obj{"username": obj{"use": "required", "meaning": "Account with the network-operator role"}, "secret": obj{"use": "required", "meaning": "Password of that account"}},
		"options": []obj{
			{"name": "max_edge_macs", "type": "integer", "default": "4", "description": "learned_macs is reported only on ports with at most this many entries."},
			{"name": "token_file", "type": "secret_reference", "description": "A reference to a token."},
		},
		"tested": []string{"EOS 4.14.2F", "EOS 4.20.7M"}, "evidence": "synthesised",
		"docs": "internal/collect/arista/README.md", "docs_url": "https://github.com/VargaFoundation/trica/blob/main/internal/collect/arista/README.md",
		"counts": obj{"instances": 2, "ok": 1, "failing": 1, "pending": 0, "stale": 0},
	}
}

func moduleAgentDoc() obj {
	return obj{
		"kind": "agent", "title": "Linux host agent", "vendors": []string{"Varga Foundation"}, "protocol": "Local probes, sent over mutual TLS",
		"summary":    obj{"en": "trica-agent, installed on a Linux host.", "fr": "trica-agent, installé sur un hôte Linux."},
		"deployment": "agent", "collects": []string{"device"}, "read_only": "Reads files and runs read-only commands on its own host.",
		"options": []obj{}, "tested": []string{"Debian 12"}, "evidence": "synthesised",
		"docs": "docs/reference/agent.md", "docs_url": "https://github.com/VargaFoundation/trica/blob/main/docs/reference/agent.md",
		"counts": obj{"instances": 1, "ok": 1, "failing": 0, "pending": 0, "stale": 0},
	}
}

func moduleUnifiDoc() obj {
	return obj{
		"kind": "unifi", "title": "Ubiquiti UniFi", "vendors": []string{"Ubiquiti"}, "protocol": "UniFi Network API over HTTPS",
		"summary":    obj{"en": "UniFi gateways, switches and access points.", "fr": "Passerelles, commutateurs et points d'accès UniFi."},
		"deployment": "target", "collects": []string{"device"}, "read_only": "GET only.", "address": "https://controller[:port]",
		"credentials": obj{"username": obj{"use": "unused"}, "secret": obj{"use": "required", "meaning": "API key"}},
		"options":     []obj{{"name": "site", "type": "string", "default": "default", "description": "The UniFi site to read."}},
		"tested":      []string{"UniFi Network 9.0"}, "evidence": "recorded",
		"docs": "internal/collect/unifi/README.md", "docs_url": "https://github.com/VargaFoundation/trica/blob/main/internal/collect/unifi/README.md",
		"counts": obj{"instances": 0, "ok": 0, "failing": 0, "pending": 0, "stale": 0},
	}
}

// healthRow is a row of the health board; module names the collector module of its source.
func healthRows() (eos, snmp, sw2, host obj) {
	eos = obj{"source": "arista:eos", "module": "arista", "collector": "arista", "target": "192.0.2.10", "version": "0.3.0", "ok": true, "last_attempt": t1, "last_success": t1, "duration_ms": 840, "items": 124, "warnings": []string{}, "failures": 0}
	snmp = obj{"source": "snmp:core", "collector": "snmp", "target": "198.51.100.1", "ok": false, "last_attempt": "2026-10-12T06:25:00Z", "last_success": "2026-10-11T06:25:00Z", "last_error": "timeout after 30s", "duration_ms": 30000, "items": 0, "warnings": []string{"no ifAlias on 3 interfaces"}, "failures": 4}
	sw2 = obj{"source": "arista:sw-a2", "module": "arista", "collector": "arista", "target": "192.0.2.11", "ok": false, "last_attempt": t1, "last_error": "i/o timeout", "duration_ms": 30000, "items": 0, "warnings": []string{}, "failures": 2}
	host = obj{"source": "agent:host-01", "module": "agent", "collector": "agent", "target": "host-01", "ok": true, "last_attempt": t1, "last_success": t1, "duration_ms": 120, "items": 40, "warnings": []string{}, "failures": 0}
	return
}

func instanceDoc(kind, name, origin, status string, config, health obj) obj {
	i := obj{"source": kind + ":" + name, "kind": kind, "name": name, "origin": origin, "status": status}
	if config != nil {
		i["config"] = config
	}
	if health != nil {
		i["health"] = health
	}
	return i
}

func targetConfigDoc(address string) obj {
	return obj{"address": address, "interval_seconds": 300, "timeout_seconds": 30, "tls": obj{"ca_file": "", "insecure": true, "min_version": ""},
		"options": obj{"max_edge_macs": 4, "token_file": "file:/run/secrets/token"}, "credentials_hidden": true}
}

// collectors answers GET /collectors and what is under it.
func (a *api) collectors(w http.ResponseWriter, path string) {
	eos, snmp, sw2, host := healthRows()
	instances := map[string][]obj{
		"arista": {
			instanceDoc("arista", "sw-a2", "server", "failing", targetConfigDoc("https://192.0.2.11"), sw2),
			instanceDoc("arista", "eos", "server", "ok", targetConfigDoc("https://192.0.2.10"), eos),
		},
		"agent": {instanceDoc("agent", "host-01", "reported", "ok", nil, host)},
		"unifi": {},
	}
	modules := map[string]obj{"arista": moduleAristaDoc(), "agent": moduleAgentDoc(), "unifi": moduleUnifiDoc()}
	rest := strings.Trim(strings.TrimPrefix(path, "/collectors"), "/")
	if rest == "" {
		writeJSON(w, http.StatusOK, obj{"modules": []obj{modules["agent"], modules["arista"], modules["unifi"]}, "unregistered": []obj{snmp}})
		return
	}
	parts := strings.Split(rest, "/")
	m, ok := modules[parts[0]]
	if !ok {
		problem(w, http.StatusNotFound, "not-found", "No such collector module")
		return
	}
	switch {
	case len(parts) == 1:
		writeJSON(w, http.StatusOK, m)
	case len(parts) == 2 && parts[1] == "instances":
		writeJSON(w, http.StatusOK, page(instances[parts[0]]))
	case len(parts) == 3 && parts[1] == "instances":
		for _, i := range instances[parts[0]] {
			if i["name"] == parts[2] {
				writeJSON(w, http.StatusOK, i)
				return
			}
		}
		problem(w, http.StatusNotFound, "not-found", "No such instance")
	default:
		problem(w, http.StatusNotFound, "not-found", "No such endpoint")
	}
}

package graph

import (
	"fmt"

	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/store"
)

// world builds snapshots for tests with the built-in relation types, so that
// a change of impact mode in builtin.yaml shows up here.
type world struct {
	objs []*store.Object
	rels []store.Relation
}

func (w *world) add(id, typ, parent string, attrs map[string]any) *world {
	o := &store.Object{ID: id, Type: typ, Display: id, Status: "active", Attrs: attrs}
	if parent != "" {
		o.ParentID = &parent
	}
	w.objs = append(w.objs, o)
	return w
}

func (w *world) absent(id string) *world {
	for _, o := range w.objs {
		if o.ID == id {
			o.Status = StatusAbsent
		}
	}
	return w
}

func (w *world) rel(typ, from, to string) *world {
	w.rels = append(w.rels, store.Relation{Type: typ, FromID: from, ToID: to})
	return w
}

// device adds a device with the given role and ports named <id>/<port>.
func (w *world) device(id, role string, ports ...string) *world {
	w.add(id, "device", "", map[string]any{"role": role})
	for _, p := range ports {
		w.add(id+"/"+p, "interface", id, nil)
	}
	return w
}

func (w *world) cable(a, b string) *world { return w.rel("cabled_to", a, b) }

func (w *world) snapshot() *Snapshot { return Build(w.objs, w.rels, schema.Builtin()) }

// lab is a small site: a firewall pair, two switches, a dual-homed server, a
// single-homed one, a server nobody cabled, and a power chain with two PDUs.
func lab() *world {
	w := &world{}
	w.device("fw1", "firewall", "e1")
	w.device("sw1", "switch", "e1", "e2", "e3")
	w.device("sw2", "switch", "e1", "e2")
	w.device("srv-dual", "server", "e1", "e2")
	w.device("srv-single", "server", "e1")
	w.device("srv-lonely", "server", "e1")
	w.cable("fw1/e1", "sw1/e1").cable("sw1/e3", "sw2/e1")
	w.cable("sw1/e2", "srv-dual/e1").cable("sw2/e2", "srv-dual/e2")
	w.add("srv-single/e2", "interface", "srv-single", nil)
	w.cable("sw2/e2", "srv-single/e1")

	// Power: srv-dual has one PSU on each PDU.
	w.device("pdu-a", "pdu").add("pdu-a/o1", "power_outlet", "pdu-a", nil)
	w.device("pdu-b", "pdu").add("pdu-b/o1", "power_outlet", "pdu-b", nil)
	w.add("srv-dual/psu1", "psu", "srv-dual", nil).add("srv-dual/psu2", "psu", "srv-dual", nil)
	w.rel("powered_by", "srv-dual/psu1", "pdu-a/o1").rel("powered_by", "srv-dual/psu2", "pdu-b/o1")

	// Compute and applications on srv-single.
	w.add("vm1", "vm", "", nil).rel("hosts", "srv-single", "vm1")
	w.add("db", "component_instance", "", nil).rel("runs_on", "db", "vm1")
	w.add("app", "application", "", nil).rel("depends_on", "app", "db")
	w.add("front", "application", "", nil).rel("calls", "front", "app")
	w.add("report", "application", "", nil).rel("depends_on", "report", "front")
	return w
}

// site builds a synthetic site of racks*perRack servers, each with two PSUs
// on two PDUs and two ports on the rack's two top-of-rack switches, which
// uplink to two core switches behind a firewall pair. Each server hosts a VM.
func site(racks, perRack int) *world {
	w := &world{}
	w.device("fw1", "firewall", "e1", "e2").device("fw2", "firewall", "e1", "e2")
	for _, c := range []string{"core1", "core2"} {
		ports := []string{"fw1", "fw2"}
		for r := range racks {
			ports = append(ports, fmt.Sprintf("r%d-a", r), fmt.Sprintf("r%d-b", r))
		}
		w.device(c, "switch", ports...)
	}
	w.cable("fw1/e1", "core1/fw1").cable("fw1/e2", "core2/fw1").cable("fw2/e1", "core1/fw2").cable("fw2/e2", "core2/fw2")
	for r := range racks {
		rack := fmt.Sprintf("r%d", r)
		w.add(rack, "rack", "", nil)
		for i, tor := range []string{rack + "-tor-a", rack + "-tor-b"} {
			ports := []string{"up1", "up2"}
			for s := range perRack {
				ports = append(ports, fmt.Sprintf("p%d", s))
			}
			w.device(tor, "switch", ports...)
			side := []string{"a", "b"}[i]
			w.cable(tor+"/up1", fmt.Sprintf("core1/%s-%s", rack, side)).cable(tor+"/up2", fmt.Sprintf("core2/%s-%s", rack, side))
		}
		for _, pdu := range []string{rack + "-pdu-a", rack + "-pdu-b"} {
			w.device(pdu, "pdu")
			for s := range perRack {
				w.add(fmt.Sprintf("%s/o%d", pdu, s), "power_outlet", pdu, nil)
			}
		}
		for s := range perRack {
			srv := fmt.Sprintf("%s-s%d", rack, s)
			w.device(srv, "server", "e1", "e2")
			w.cable(srv+"/e1", fmt.Sprintf("%s-tor-a/p%d", rack, s)).cable(srv+"/e2", fmt.Sprintf("%s-tor-b/p%d", rack, s))
			for k, side := range []string{"a", "b"} {
				psu := fmt.Sprintf("%s/psu%d", srv, k+1)
				w.add(psu, "psu", srv, nil).rel("powered_by", psu, fmt.Sprintf("%s-pdu-%s/o%d", rack, side, s))
			}
			w.add(srv+"-vm", "vm", "", nil).rel("hosts", srv, srv+"-vm")
		}
	}
	return w
}

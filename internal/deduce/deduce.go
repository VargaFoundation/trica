// Package deduce turns what the network collectors observe into cables
// (ADR 0010). LLDP adjacencies seen from both ends give a cable of high
// confidence, from one end a cable of medium confidence; a switch forwarding
// table that sees a single known host port on an edge port gives one of low
// confidence. Deduced cables are compared with documented ones, and the
// differences become discrepancies.
//
// The deduction is a pure function of the current inventory (Deduce); Run
// submits its result to the reconciliation engine like any collector.
package deduce

import (
	"fmt"
	"slices"
	"strings"

	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/internal/store"
)

// Evidence and level of a deduced cable, as stored on the cabled_to relation.
const (
	EvidenceLLDP      = "lldp"
	EvidenceLLDPOne   = "lldp_one_sided"
	EvidenceMACTable  = "mac_table"
	LevelHigh         = "high"
	LevelMedium       = "medium"
	LevelLow          = "low"
	relNeighbor       = "neighbor_of"
	relCabled         = "cabled_to"
	relConnects       = "connects"
	typeInterface     = "interface"
	typeCable         = "cable"
	typeDevice        = "device"
	kindPhysical      = "physical"
	kindBMC           = "bmc"
	attrKind          = "kind"
	attrMAC           = "mac"
	attrLearned       = "learned_macs"
	deducedSourceLike = "deduce:"
)

// Discrepancy kinds between documented and deduced cables.
const (
	KindNotObserved = "cable_not_observed"
	KindNotDeclared = "cable_not_declared"
	KindConflict    = "cable_conflict"
)

// Cable is one deduced cable between two ports, A < B.
type Cable struct {
	A, B     string
	Evidence string
	Level    string
	// Reason says, in a sentence, why the deducer believes this cable.
	Reason string
}

// macCand is the host port a forwarding table points at, and why.
type macCand struct {
	host, reason string
}

// sameDevice reports whether the BMC ports are all on the device of port.
func sameDevice(g *graph, port string, bmc []string) bool {
	if len(bmc) == 0 {
		return false
	}
	for _, b := range bmc {
		if g.ports[b].device != g.ports[port].device {
			return false
		}
	}
	return true
}

// Skip is a port the deducer could not decide on, and why.
type Skip struct {
	Port   string
	Reason string
}

// Finding is a difference between documented and deduced cabling.
type Finding struct {
	Kind string
	// Port is the port the finding is about; Other the documented or
	// deduced peer.
	Port, Other string
	Declared    string
	Observed    string
}

// Fingerprint identifies a finding across passes.
func (f Finding) Fingerprint() string {
	return fmt.Sprintf("cable:%s:%s", f.Kind, pairKey(f.Port, f.Other))
}

// Plan is the outcome of one deduction pass.
type Plan struct {
	Cables   []Cable
	Skipped  []Skip
	Findings []Finding
}

// port is an interface of the inventory, with what the deduction needs.
type port struct {
	id, device string
	physical   bool
	// bmc marks the port of a management controller (kind bmc).
	bmc     bool
	mac     string
	learned []string
}

// Deduce computes the cables the current inventory supports. objs and rels
// are the current objects and relations; absent objects are ignored.
func Deduce(objs []*store.Object, rels []store.Relation) *Plan {
	g := newGraph(objs)
	p := &Plan{}
	cabled := map[string]bool{}

	// LLDP. An adjacency seen from both ends is kept when each end has
	// exactly one such adjacency; one seen from one end only is kept when
	// neither end has any other claim. A port with several is ambiguous: a
	// hub, an unmanaged switch, or a stale neighbour entry.
	seen := map[[2]string]bool{}
	for _, r := range rels {
		if r.Type == relNeighbor && g.ports[r.FromID] != nil && g.ports[r.ToID] != nil && r.FromID != r.ToID {
			seen[[2]string{r.FromID, r.ToID}] = true
			g.lldp[r.FromID] = true
		}
	}
	type cand struct {
		a, b   string
		mutual bool
	}
	cands := map[string]*cand{}
	for k := range seen {
		a, b := order(k[0], k[1])
		key := pairKey(a, b)
		if cands[key] == nil {
			cands[key] = &cand{a: a, b: b}
		}
		if seen[[2]string{k[1], k[0]}] {
			cands[key].mutual = true
		}
	}
	keys := sortedKeys(cands)
	mutualPer, anyPer := map[string]int{}, map[string]int{}
	for _, k := range keys {
		c := cands[k]
		anyPer[c.a]++
		anyPer[c.b]++
		if c.mutual {
			mutualPer[c.a]++
			mutualPer[c.b]++
		}
	}
	for _, k := range keys {
		if c := cands[k]; c.mutual && mutualPer[c.a] == 1 && mutualPer[c.b] == 1 {
			p.Cables = append(p.Cables, Cable{A: c.a, B: c.b, Evidence: EvidenceLLDP, Level: LevelHigh,
				Reason: "LLDP: each port sees the other as its only neighbour"})
			cabled[c.a], cabled[c.b] = true, true
		}
	}
	for _, k := range keys {
		c := cands[k]
		if c.mutual || cabled[c.a] || cabled[c.b] {
			continue
		}
		if anyPer[c.a] == 1 && anyPer[c.b] == 1 {
			p.Cables = append(p.Cables, Cable{A: c.a, B: c.b, Evidence: EvidenceLLDPOne, Level: LevelMedium,
				Reason: "LLDP seen from one end only; neither port has another neighbour"})
			cabled[c.a], cabled[c.b] = true, true
		}
	}
	for _, id := range sortedKeys(anyPer) {
		if !cabled[id] {
			p.Skipped = append(p.Skipped, Skip{Port: id, Reason: fmt.Sprintf("%d conflicting LLDP neighbours", anyPer[id])})
		}
	}

	// Forwarding tables, for ports LLDP says nothing about. The MACs learned
	// on an edge port are resolved to the physical ports of other devices
	// that carry them; exactly one such port makes a cable, and that port
	// must not be learned on another edge port.
	byMAC := map[string][]string{}
	for _, id := range sortedKeys(g.ports) {
		if pt := g.ports[id]; pt.mac != "" && pt.device != "" {
			byMAC[pt.mac] = append(byMAC[pt.mac], id)
		}
	}
	macCands := map[string]macCand{}
	learnedOn := map[string][]string{}
	for _, id := range sortedKeys(g.ports) {
		pt := g.ports[id]
		// A port LLDP has an opinion about is not an edge port: either it is
		// cabled already, or its neighbours conflict and were reported.
		if len(pt.learned) == 0 || cabled[id] || anyPer[id] > 0 {
			continue
		}
		if !pt.physical {
			p.Skipped = append(p.Skipped, Skip{Port: id, Reason: "addresses learned on a port that is not physical (an aggregate): the member link is unknown"})
			continue
		}
		hosts := map[string]string{}
		for _, e := range pt.learned {
			m := macOf(e)
			if m == "" {
				continue
			}
			for _, h := range byMAC[m] {
				if hp := g.ports[h]; hp.device != pt.device {
					hosts[h] = m
				}
			}
		}
		// Host ports that take a cable: physical ports, and the dedicated
		// ports of management controllers, which speak no LLDP and are only
		// ever found this way.
		var physical, bmc []string
		for _, h := range sortedKeys(hosts) {
			switch hp := g.ports[h]; {
			case hp.physical:
				physical = append(physical, h)
			case hp.bmc:
				bmc = append(bmc, h)
			}
		}
		var c macCand
		switch {
		case len(physical) == 1 && len(bmc) == 0:
			c = macCand{host: physical[0], reason: fmt.Sprintf("forwarding table: the only known address learned on this edge port, %s, is the MAC of this host port", hosts[physical[0]])}
		case len(physical) == 0 && len(bmc) == 1:
			c = macCand{host: bmc[0], reason: fmt.Sprintf("forwarding table: the only known address learned on this edge port, %s, is the MAC of this management controller port (Redfish), which speaks no LLDP", hosts[bmc[0]])}
		case len(physical) == 1 && sameDevice(g, physical[0], bmc):
			// A management controller sharing a host port (shared LOM) is
			// learned on the same switch port as that host port: the cable
			// goes to the host port.
			c = macCand{host: physical[0], reason: fmt.Sprintf("forwarding table: the addresses learned on this edge port are those of one host port, %s, and of the management controller sharing it (shared LOM)", hosts[physical[0]])}
		case len(physical)+len(bmc) > 1:
			p.Skipped = append(p.Skipped, Skip{Port: id, Reason: fmt.Sprintf("learned addresses match %d host ports (a bond, or several hosts)", len(physical)+len(bmc))})
			continue
		default:
			continue
		}
		macCands[id] = c
		learnedOn[c.host] = append(learnedOn[c.host], id)
	}
	for _, id := range sortedKeys(macCands) {
		c := macCands[id]
		h := c.host
		switch {
		case cabled[h]:
			p.Skipped = append(p.Skipped, Skip{Port: id, Reason: "the host port its addresses belong to is cabled elsewhere by LLDP"})
		case len(learnedOn[h]) > 1:
			p.Skipped = append(p.Skipped, Skip{Port: id, Reason: fmt.Sprintf("the host port is learned on %d edge ports", len(learnedOn[h]))})
		default:
			a, b := order(id, h)
			p.Cables = append(p.Cables, Cable{A: a, B: b, Evidence: EvidenceMACTable, Level: LevelLow, Reason: c.reason})
			cabled[id], cabled[h] = true, true
		}
	}
	slices.SortFunc(p.Cables, func(x, y Cable) int { return strings.Compare(pairKey(x.A, x.B), pairKey(y.A, y.B)) })

	p.Findings = compare(g, rels, p.Cables)
	return p
}

// compare finds the differences between documented cables (cable objects
// with two connects relations to ports, not from a deduce: source) and
// deduced ones. A documented cable is only reported missing when one of its
// ends is on a device the deducer has evidence about: elsewhere absence of
// evidence says nothing. A deduced cable is only reported undocumented on a
// device that has documented cables: a site without cable documentation would
// otherwise turn every cable into a discrepancy.
func compare(g *graph, rels []store.Relation, cables []Cable) []Finding {
	ends := map[string][]string{}
	for _, r := range rels {
		if r.Type == relConnects && g.cables[r.FromID] && g.ports[r.ToID] != nil && !strings.HasPrefix(r.Source, deducedSourceLike) {
			ends[r.FromID] = append(ends[r.FromID], r.ToID)
		}
	}
	declared := map[string]string{}
	documentedDevice := map[string]bool{}
	for _, c := range sortedKeys(ends) {
		e := ends[c]
		if len(e) != 2 || e[0] == e[1] {
			continue
		}
		declared[e[0]], declared[e[1]] = e[1], e[0]
		documentedDevice[g.ports[e[0]].device] = true
		documentedDevice[g.ports[e[1]].device] = true
	}
	deduced := map[string]string{}
	evidenced := map[string]bool{}
	for _, c := range cables {
		deduced[c.A], deduced[c.B] = c.B, c.A
	}
	for _, pt := range g.ports {
		if len(pt.learned) > 0 || g.lldp[pt.id] {
			evidenced[pt.device] = true
		}
	}

	var out []Finding
	for _, a := range sortedKeys(declared) {
		b := declared[a]
		got, ok := deduced[a]
		switch {
		case ok && got == b:
		case ok:
			out = append(out, Finding{Kind: KindConflict, Port: a, Other: b, Declared: b, Observed: got})
		case a < b && (evidenced[g.ports[a].device] || evidenced[g.ports[b].device]):
			if _, other := deduced[b]; !other {
				out = append(out, Finding{Kind: KindNotObserved, Port: a, Other: b, Declared: b})
			}
		}
	}
	for _, c := range cables {
		_, da := declared[c.A]
		_, db := declared[c.B]
		if !da && !db && (documentedDevice[g.ports[c.A].device] || documentedDevice[g.ports[c.B].device]) {
			out = append(out, Finding{Kind: KindNotDeclared, Port: c.A, Other: c.B, Observed: c.B})
		}
	}
	return out
}

type graph struct {
	ports  map[string]*port
	cables map[string]bool
	lldp   map[string]bool
}

func newGraph(objs []*store.Object) *graph {
	byID := map[string]*store.Object{}
	for _, o := range objs {
		if o.Status == "active" {
			byID[o.ID] = o
		}
	}
	g := &graph{ports: map[string]*port{}, cables: map[string]bool{}, lldp: map[string]bool{}}
	for _, o := range byID {
		switch o.Type {
		case typeCable:
			g.cables[o.ID] = true
		case typeInterface:
			pt := &port{id: o.ID, device: deviceOf(byID, o)}
			kind, _ := o.Attrs[attrKind].(string)
			pt.physical = kind == "" || kind == kindPhysical
			pt.bmc = kind == kindBMC
			if m, ok := o.Attrs[attrMAC].(string); ok {
				pt.mac = macOf(m)
			}
			if l, ok := o.Attrs[attrLearned].([]any); ok {
				for _, e := range l {
					if s, ok := e.(string); ok {
						pt.learned = append(pt.learned, s)
					}
				}
			}
			g.ports[o.ID] = pt
		}
	}
	return g
}

// deviceOf walks up the parents to the device that holds an interface; ""
// when it is held by a VM or a node, whose ports are not cabled.
func deviceOf(byID map[string]*store.Object, o *store.Object) string {
	for range 8 {
		if o.ParentID == nil {
			return ""
		}
		p := byID[*o.ParentID]
		if p == nil {
			return ""
		}
		if p.Type == typeDevice {
			return p.ID
		}
		if p.Type != "module" {
			return ""
		}
		o = p
	}
	return ""
}

// macOf returns the canonical address of a learned entry, "vlan/mac" or
// "mac"; "" when it is not an address.
func macOf(entry string) string {
	if i := strings.LastIndexByte(entry, '/'); i >= 0 {
		entry = entry[i+1:]
	}
	m, err := schema.NormalizeMAC(entry)
	if err != nil {
		return ""
	}
	return m
}

func order(a, b string) (string, string) {
	if b < a {
		return b, a
	}
	return a, b
}

func pairKey(a, b string) string {
	a, b = order(a, b)
	return a + "|" + b
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

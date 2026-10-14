// Package reconcile is the single place that turns observations into
// inventory state. Collectors, the agent, the declaration files, the UI and
// the MCP tools all submit sdk.Batch values here; nothing else writes to the
// objects, claims, history and relation tables.
package reconcile

import (
	"path"
	"strings"
	"time"

	"github.com/VargaFoundation/trica/pkg/sdk"
)

// PriorityRule orders sources for the attributes it matches. Attribute is a
// "type.attribute" glob ("device.os_version", "*.owner", "rack.*"). Sources
// is an ordered list of source globs ("user:*", "redfish:*"); earlier wins.
// Example from the specification: the version comes from the equipment, the
// owner comes from manual entry.
type PriorityRule struct {
	Attribute string   `yaml:"attribute" json:"attribute"`
	Sources   []string `yaml:"sources" json:"sources"`
}

// Policy configures the engine.
type Policy struct {
	// Priorities, first matching rule wins. Sources a rule does not list rank
	// after the listed ones, by the default order.
	Priorities []PriorityRule `yaml:"priorities" json:"priorities"`
	// Propose lists source globs whose batches are queued as proposals for
	// human approval instead of being applied ("mcp:*", "deduce:*").
	Propose []string `yaml:"propose" json:"propose"`
	// AbsentAfter is how long an observed object may go unseen before it is
	// marked absent. Zero means 30 minutes.
	AbsentAfter time.Duration `yaml:"absent_after" json:"absent_after"`
}

// DefaultAbsentAfter applies when Policy.AbsentAfter is zero.
const DefaultAbsentAfter = 30 * time.Minute

func (p Policy) absentAfter() time.Duration {
	if p.AbsentAfter <= 0 {
		return DefaultAbsentAfter
	}
	return p.AbsentAfter
}

// Proposes reports whether batches from source go to the proposal queue.
func (p Policy) Proposes(source string) bool {
	for _, g := range p.Propose {
		if globMatch(g, source) {
			return true
		}
	}
	return false
}

func globMatch(pattern, s string) bool {
	ok, err := path.Match(pattern, s)
	if err == nil && ok {
		return true
	}
	// path.Match stops "*" at "/": sources and attributes never contain one,
	// but be lenient with a trailing "*" over anything.
	if strings.HasSuffix(pattern, "*") && strings.HasPrefix(s, strings.TrimSuffix(pattern, "*")) {
		return true
	}
	return false
}

var confidenceRank = map[sdk.Confidence]int{sdk.Observed: 3, sdk.Deduced: 2, sdk.Declared: 1}

// candidate is one claim competing for an attribute's resolved value.
type candidate struct {
	Source     string
	Value      []byte
	Confidence sdk.Confidence
	LastSeen   time.Time
	Author     string
}

// rank returns the position of source in the rule matching typ.attr, or -1.
func (p Policy) rank(typ, attr, source string) int {
	key := typ + "." + attr
	for _, r := range p.Priorities {
		if !globMatch(r.Attribute, key) {
			continue
		}
		for i, g := range r.Sources {
			if globMatch(g, source) {
				return i
			}
		}
		return len(r.Sources)
	}
	return -1
}

// better reports whether a beats b for typ.attr. Default order: the rule's
// source order, then observed > deduced > declared, then the most recently
// confirmed, then the source name for determinism.
func (p Policy) better(typ, attr string, a, b candidate) bool {
	ra, rb := p.rank(typ, attr, a.Source), p.rank(typ, attr, b.Source)
	if ra != rb {
		return ra < rb
	}
	if ca, cb := confidenceRank[a.Confidence], confidenceRank[b.Confidence]; ca != cb {
		return ca > cb
	}
	if !a.LastSeen.Equal(b.LastSeen) {
		return a.LastSeen.After(b.LastSeen)
	}
	return a.Source < b.Source
}

// winner picks the resolved claim among cands (non-empty).
func (p Policy) winner(typ, attr string, cands []candidate) candidate {
	w := cands[0]
	for _, c := range cands[1:] {
		if p.better(typ, attr, c, w) {
			w = c
		}
	}
	return w
}

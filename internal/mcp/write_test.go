package mcp_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/VargaFoundation/trica/internal/reconcile"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// inventory counts the rows the engine writes when it applies a batch: if a
// proposal changes any of them, it was applied, not proposed.
func (e *env) inventory() string {
	e.t.Helper()
	var out []string
	for _, table := range []string{"objects", "claims", "attr_history", "relations", "rel_claims"} {
		var n int
		if err := e.st.Pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%s=%d", table, n))
	}
	return strings.Join(out, " ")
}

// newServer is the proposal of a new server in rack-2 at U10 (the spec's
// question 12), designating the rack by its id as search returned it.
func (e *env) newServer(reason string) m {
	return m{
		"reason": reason,
		"objects": []m{{
			"ref": "srv", "type": "device",
			"identity":   m{"serial": "SRV-13-SN", "name": "srv-13"},
			"attributes": m{"role": "server", "vendor": "Acme", "power_w": 350},
		}},
		"relations": []m{{
			"type":       "mounted_in",
			"from":       m{"ref": "srv"},
			"to":         m{"type": "rack", "identity": m{"id": e.ids["rack-2"]}},
			"attributes": m{"position": 10, "face": "front"},
		}},
	}
}

// refused runs a tool and fails unless it was refused, either by the
// input schema (a protocol error) or by the tool (an error result).
func refused(t *testing.T, cs *gosdk.ClientSession, tool string, args any) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &gosdk.CallToolParams{Name: tool, Arguments: args})
	if err == nil && !res.IsError {
		t.Fatalf("%s accepted %v", tool, args)
	}
}

func TestProposeChange(t *testing.T) {
	e := newEnv(t, proposeMCP, false)
	cs := e.connect(e.as(tokMCP))
	before := e.inventory()

	a := ok(t, cs, "propose_change", e.newServer("Server srv-13 arrives next week, rack 2 at U10"))
	id := num(t, a.Data, "proposal_id")
	if str(t, a.Data, "status") != "proposed" || id == 0 || str(t, a.Data, "source") != "mcp:assistant" {
		t.Fatalf("outcome %v", a.Data)
	}
	if a.Data["preview"] == nil {
		t.Fatal("the answer carries no preview of what the proposal would do")
	}

	t.Run("the proposal is queued, attributed, and says what the caller cannot change", func(t *testing.T) {
		p, err := e.eng.GetProposal(context.Background(), int64(id))
		if err != nil {
			t.Fatal(err)
		}
		if p.Status != "pending" || p.Source != "mcp:assistant" || p.Author != "mcp-assistant" || !strings.Contains(p.Reason, "srv-13") {
			t.Fatalf("proposal %+v", p)
		}
		if p.Batch.Scope != sdk.ScopePartial || p.Batch.Confidence != sdk.Declared || p.Batch.Source != "mcp:assistant" {
			t.Fatalf("batch scope %q confidence %q source %q", p.Batch.Scope, p.Batch.Confidence, p.Batch.Source)
		}
		if !p.Batch.ObservedAt.Equal(e.clk.now()) {
			t.Fatalf("observed at %v, want the server's clock %v", p.Batch.ObservedAt, e.clk.now())
		}
	})
	t.Run("the inventory is untouched: nothing was applied", func(t *testing.T) {
		if after := e.inventory(); after != before {
			t.Fatalf("the inventory changed:\n before %s\n after  %s", before, after)
		}
		s := ok(t, e.connect(e.as(tokReader)), "search", m{"query": "type:device name:srv-13"})
		if num(t, s.Data, "summary", "total") != 0 {
			t.Fatalf("the proposed server is already in the inventory: %v", dig(t, s.Data, "summary"))
		}
	})
	t.Run("the same proposal twice is one proposal", func(t *testing.T) {
		b := ok(t, cs, "propose_change", e.newServer("Server srv-13 arrives next week, rack 2 at U10"))
		if num(t, b.Data, "proposal_id") != id {
			t.Fatalf("a second proposal was queued: %d and %d", id, num(t, b.Data, "proposal_id"))
		}
	})
	t.Run("approving it is a person's act, and then the inventory has the server", func(t *testing.T) {
		if _, err := e.eng.Approve(context.Background(), int64(id), "alice"); err != nil {
			t.Fatal(err)
		}
		s := ok(t, e.connect(e.as(tokReader)), "search", m{"query": "type:device name:srv-13"})
		if num(t, s.Data, "summary", "total") != 1 {
			t.Fatalf("approved but not in the inventory: %v", dig(t, s.Data, "summary"))
		}
	})
}

// A person on a stdio session speaks as mcp:<subject>: the policy and the
// audit trail recognise the origin whoever is behind it.
func TestProposeChangeSource(t *testing.T) {
	e := newEnv(t, proposeMCP, false)
	a := ok(t, e.connect(e.as(tokAdmin)), "propose_change", e.newServer("planned"))
	if str(t, a.Data, "source") != "mcp:alice" {
		t.Fatalf("source %q, want mcp:alice", str(t, a.Data, "source"))
	}
}

// The batch's source, scope and confidence are not the caller's to choose:
// asking for another is refused or ignored, never obeyed.
func TestProposeChangeCannotChooseItsOrigin(t *testing.T) {
	e := newEnv(t, proposeMCP, false)
	cs := e.connect(e.as(tokMCP))
	args := e.newServer("planned")
	args["source"], args["scope"], args["confidence"] = "arista:sw-a1", "full", "observed"
	res, err := cs.CallTool(context.Background(), &gosdk.CallToolParams{Name: "propose_change", Arguments: args})
	if err != nil || res.IsError {
		return // refused: the input has no such field
	}
	proposals, lerr := e.eng.ListProposals(context.Background(), "", 10)
	if lerr != nil || len(proposals) != 1 {
		t.Fatalf("proposals %v, %v", proposals, lerr)
	}
	b := proposals[0].Batch
	if b.Source != "mcp:assistant" || b.Scope != sdk.ScopePartial || b.Confidence != sdk.Declared {
		t.Fatalf("the caller chose the origin: source %q scope %q confidence %q", b.Source, b.Scope, b.Confidence)
	}
}

// Whatever the policy says, MCP queues: a policy that forgot to list mcp:*
// must not turn a proposal into a write.
func TestProposeChangeNeverApplies(t *testing.T) {
	e := newEnv(t, reconcile.Policy{}, false)
	before := e.inventory()
	a := ok(t, e.connect(e.as(tokMCP)), "propose_change", e.newServer("planned"))
	if str(t, a.Data, "status") != "proposed" {
		t.Fatalf("outcome %v", a.Data)
	}
	if after := e.inventory(); after != before {
		t.Fatalf("with a policy that applies everything, the proposal was applied:\n before %s\n after  %s", before, after)
	}
	proposals, err := e.eng.ListProposals(context.Background(), "pending", 10)
	if err != nil || len(proposals) != 1 {
		t.Fatalf("pending proposals %v, %v", proposals, err)
	}
}

func TestProposeChangeRefusals(t *testing.T) {
	e := newEnv(t, proposeMCP, false)
	cs := e.connect(e.as(tokMCP))
	before := e.inventory()

	good := func(mut func(m)) m {
		a := e.newServer("planned")
		if mut != nil {
			mut(a)
		}
		return a
	}
	t.Run("a reason is required, and bounded", func(t *testing.T) {
		denied(t, cs, "propose_change", good(func(a m) { a["reason"] = "" }), "invalid-request")
		denied(t, cs, "propose_change", good(func(a m) { a["reason"] = "   " }), "invalid-request")
		denied(t, cs, "propose_change", good(func(a m) { a["reason"] = strings.Repeat("x", 501) }), "invalid-request")
	})
	t.Run("an empty proposal is refused", func(t *testing.T) {
		denied(t, cs, "propose_change", m{"reason": "nothing"}, "invalid-request")
	})
	t.Run("a null entry is refused", func(t *testing.T) {
		refused(t, cs, "propose_change", m{"reason": "r", "objects": []any{nil}})
		refused(t, cs, "propose_change", m{"reason": "r", "relations": []any{nil}})
	})
	t.Run("a proposal too big is refused", func(t *testing.T) {
		objs := make([]m, 201)
		for i := range objs {
			objs[i] = m{"ref": fmt.Sprintf("o%d", i), "type": "device", "identity": m{"name": fmt.Sprintf("srv-x%d", i)}}
		}
		denied(t, cs, "propose_change", m{"reason": "r", "objects": objs}, "invalid-request")
	})
	t.Run("a secret is not written through MCP", func(t *testing.T) {
		for _, attr := range []string{"snmp_community", "password", "api_token", "private_key"} {
			a := good(func(a m) { a["objects"].([]m)[0]["attributes"] = m{attr: "x"} })
			a2 := call(t, cs, "propose_change", a)
			if !a2.IsError {
				t.Errorf("a proposal setting %s was accepted", attr)
			}
		}
	})
	t.Run("an unknown type is refused", func(t *testing.T) {
		a := good(func(a m) { a["objects"].([]m)[0]["type"] = "no_such_type" })
		if !call(t, cs, "propose_change", a).IsError {
			t.Error("a proposal of an unknown type was accepted")
		}
	})
	if after := e.inventory(); after != before {
		t.Fatalf("a refused proposal changed the inventory:\n before %s\n after  %s", before, after)
	}
	if proposals, err := e.eng.ListProposals(context.Background(), "", 10); err != nil || len(proposals) != 0 {
		t.Fatalf("refused proposals were queued: %v, %v", proposals, err)
	}
}

func TestProposeChangeRights(t *testing.T) {
	e := newEnv(t, proposeMCP, false)

	t.Run("a credential that cannot propose is not offered the tool, and is refused if it tries", func(t *testing.T) {
		cs := e.connect(e.as(tokReader))
		for _, n := range toolNames(t, cs) {
			if n == "propose_change" {
				t.Fatal("offered")
			}
		}
		refused(t, cs, "propose_change", e.newServer("planned"))
	})
	t.Run("proposing does not need to read", func(t *testing.T) {
		// erin may propose and cannot read: she is not told what exists (the id
		// of the rack she designates is not checked against what she may read).
		cs := e.connect(e.as(tokProposeNR))
		a := call(t, cs, "propose_change", m{"reason": "planned", "objects": []m{{"ref": "n", "type": "device", "identity": m{"serial": "SRV-14-SN", "name": "srv-14"}}}})
		if a.IsError {
			t.Fatalf("a proposer was refused: %s", a.Text)
		}
		if str(t, a.Data, "source") != "mcp:erin" {
			t.Fatalf("source %v", a.Data["source"])
		}
		if a.Data["preview"] != nil {
			if p, ok := a.Data["preview"].(map[string]any); ok {
				for k, v := range p {
					if list, ok := v.([]any); ok && len(list) > 0 {
						t.Errorf("a caller who cannot read was shown %s: %v", k, list)
					}
				}
			}
		}
	})
	t.Run("a grant limited to devices proposes devices, not racks", func(t *testing.T) {
		cs := e.connect(e.as(tokDevProp))
		ok(t, cs, "propose_change", m{"reason": "planned", "objects": []m{{"ref": "n", "type": "device", "identity": m{"serial": "SRV-15-SN", "name": "srv-15"}}}})
		denied(t, cs, "propose_change", m{"reason": "planned", "objects": []m{{"ref": "r", "type": "rack", "identity": m{"name": "rack-9"}}}}, "forbidden")
		// A relation whose far end is an object of a type the grant does not cover.
		denied(t, cs, "propose_change", e.newServer("planned"), "forbidden")
	})
}

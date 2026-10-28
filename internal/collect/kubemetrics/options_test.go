package kubemetrics

import (
	"strings"
	"testing"

	"github.com/VargaFoundation/trica/internal/collect"
)

func TestOptionsValidation(t *testing.T) {
	f := newFake(t)
	for _, tc := range []struct{ name, doc, want string }{
		{"no cluster", "environment: dev", "options.cluster is required"},
		{"unknown option", "cluster: dev\ncolour: blue", "colour"},
		{"bad selector", "cluster: dev\nselector: 'job=kube'", "options.selector"},
		{"selector names a metric", `selector: '__name__="up"'`, "__name__"},
		{"selector with a function", `selector: 'job="a" or up'`, "options.selector"},
		{"selector with a bad regexp", `selector: 'job=~"("'`, "options.selector"},
		{"unbalanced braces", `selector: '{job="a"'`, "unbalanced"},
		{"finished_pods is not a boolean", "cluster: dev\nfinished_pods: [1]", "unmarshal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := tc.doc
			if !strings.Contains(doc, "cluster:") {
				doc = "cluster: dev\n" + doc
			}
			tg := withOptions(t, f.Target(tgt), doc)
			if tc.name == "no cluster" {
				tg = withOptions(t, f.Target(tgt), tc.doc)
			}
			_, err := collect.New(tg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %v, want %q", err, tc.want)
			}
		})
	}
	for _, doc := range []string{
		"cluster: dev",
		`selector: 'job="monitoring-kube-state-metrics"'`,
		`selector: '{job="monitoring-kube-state-metrics", namespace!~"kube-.*"}'`,
		"selector: ''",
		"finished_pods: true",
		"environment: production",
	} {
		if !strings.Contains(doc, "cluster:") {
			doc = "cluster: dev\n" + doc
		}
		if _, err := collect.New(withOptions(t, f.Target(tgt), doc)); err != nil {
			t.Errorf("%q refused: %v", doc, err)
		}
	}
}

func TestSelectorCanonicalForm(t *testing.T) {
	for in, want := range map[string]string{
		`job="a"`:                   `job="a"`,
		`{job="a"}`:                 `job="a"`,
		` { job = "a" , x!="b" } `:  `job = "a" , x!="b"`,
		`{}`:                        "",
		``:                          "",
		`path=~"/a,b"`:              `path=~"/a,b"`,
		`msg="say \"hi\", then go"`: `msg="say \"hi\", then go"`,
	} {
		got, _, err := parseMatchers(in)
		if err != nil || got != want {
			t.Errorf("%q -> %q, %v; want %q", in, got, err, want)
		}
	}
	if _, ms, _ := parseMatchers(`a="1",b!="2",c=~"x.*",d!~"y"`); len(ms) != 4 {
		t.Errorf("%d matchers", len(ms))
	}
}

// The selector is ANDed on every query, and it narrows what the server returns.
func TestSelectorNarrows(t *testing.T) {
	f := newFake(t)
	all := runOne(t, f, "")
	f2 := newFake(t)
	b := runOne(t, f2, `selector: 'namespace!="orion"'`)
	if count(b, "pod") >= count(all, "pod") {
		t.Errorf("pods %d vs %d: the selector did not narrow", count(b, "pod"), count(all, "pod"))
	}
	for _, o := range b.Objects {
		if o.Type == "namespace" && o.Identity["name"] == "orion" {
			t.Error("a namespace the selector excludes")
		}
	}
	for _, r := range f2.Requests() {
		if !strings.Contains(r.Params["query"][0], `{namespace!="orion"}`) {
			t.Errorf("query %q lacks the selector", r.Params["query"][0])
		}
	}
}

func TestEnvironmentOption(t *testing.T) {
	f := newFake(t)
	b := runOne(t, f, "")
	if !hasRel(b, "deployed_in", "cluster", "env/dev") {
		t.Error("the environment defaults to the cluster's name")
	}
	b = runOne(t, newFake(t), "environment: production")
	if !hasRel(b, "deployed_in", "cluster", "env/production") {
		t.Error("environment option ignored")
	}
	if o := obj(t, b, "environment", "name", "production"); o.Attributes["kind"] != nil {
		t.Errorf("the collector sets the environment kind: %v", o.Attributes)
	}
}

func TestTargetValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*collect.Target)
		want   string
	}{
		{"no address", func(tg *collect.Target) { tg.Address = "" }, "address is required"},
		{"http", func(tg *collect.Target) { tg.Address = "http://metrics.example.org/inventory" }, "must be https"},
		{"garbage", func(tg *collect.Target) { tg.Address = "https://" }, "invalid address"},
		{"credentials in the address", func(tg *collect.Target) { tg.Address = "https://u:p@metrics.example.org" }, "no credentials"},
		{"a query in the address", func(tg *collect.Target) { tg.Address = "https://metrics.example.org/?x=1" }, "no credentials, query"},
		{"no username", func(tg *collect.Target) { tg.Username = " " }, "username is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tg := withOptions(t, collect.Target{Kind: Kind, Name: tgt, Address: "https://metrics.example.org/inventory", Username: "u", Password: "file:/x"}, "cluster: dev")
			tc.mutate(&tg)
			_, err := collect.New(tg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %v, want %q", err, tc.want)
			}
		})
	}
	// An address with no scheme is https; a trailing slash is dropped.
	for _, a := range []string{"metrics.example.org/inventory", "https://metrics.example.org/inventory/"} {
		tg := withOptions(t, collect.Target{Kind: Kind, Name: tgt, Address: a, Username: "u", Password: "file:/x"}, "cluster: dev")
		c, err := collect.New(tg)
		if err != nil {
			t.Fatal(err)
		}
		if got := c.(*collector).base.String(); got != "https://metrics.example.org/inventory" {
			t.Errorf("%q -> %q", a, got)
		}
	}
}

func TestDescriptor(t *testing.T) {
	d, ok := collect.Lookup(Kind)
	if !ok {
		t.Fatal("not registered")
	}
	if d.Evidence != collect.EvidenceRecorded || d.Docs == "" || d.ReadOnly == "" {
		t.Errorf("descriptor %+v", d)
	}
}

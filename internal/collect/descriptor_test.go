package collect

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/schema"
	"github.com/VargaFoundation/trica/pkg/sdk"
)

// testDescriptor is a complete descriptor of a target module.
func testDescriptor(kind string) Descriptor {
	return Descriptor{
		Kind:       kind,
		Title:      "Test module",
		Vendors:    []string{"Acme"},
		Protocol:   "HTTPS",
		Summary:    schema.Label{EN: "A test module.", FR: "Un module de test."},
		Deployment: DeploymentTarget,
		Collects:   []string{"device"},
		ReadOnly:   "GET only.",
		Address:    "https://host",
		Username:   Credential{Use: UseRequired, Meaning: "account"},
		Secret:     Credential{Use: UseRequired, Meaning: "password"},
		Tested:     []string{"1.0"},
		Evidence:   EvidenceSynthesised,
		Docs:       "internal/collect/README.md",
	}
}

func nopFactory(t Target) (sdk.Collector, error) { return nop{t}, nil }

func TestDescriptorValidates(t *testing.T) {
	if err := testDescriptor("acme").Validate(true); err != nil {
		t.Fatalf("a complete descriptor: %v", err)
	}
	agent := testDescriptor("acme-agent")
	agent.Deployment = DeploymentAgent
	if err := agent.Validate(false); err != nil {
		t.Fatalf("a complete agent descriptor: %v", err)
	}
}

func TestRegisterRefusesAnIncompleteDescriptor(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mutate     func(*Descriptor)
		hasFactory bool
	}{
		{"kind with a capital", func(d *Descriptor) { d.Kind = "Arista" }, true},
		{"kind with a colon", func(d *Descriptor) { d.Kind = "a:b" }, true},
		{"empty kind", func(d *Descriptor) { d.Kind = "" }, true},
		{"no title", func(d *Descriptor) { d.Title = "" }, true},
		{"no protocol", func(d *Descriptor) { d.Protocol = " " }, true},
		{"no read-only statement", func(d *Descriptor) { d.ReadOnly = "" }, true},
		{"no docs", func(d *Descriptor) { d.Docs = "" }, true},
		{"no english summary", func(d *Descriptor) { d.Summary.EN = "" }, true},
		{"no french summary", func(d *Descriptor) { d.Summary.FR = "" }, true},
		{"no vendors", func(d *Descriptor) { d.Vendors = nil }, true},
		{"collects nothing", func(d *Descriptor) { d.Collects = nil }, true},
		{"tested with nothing", func(d *Descriptor) { d.Tested = nil }, true},
		{"unknown evidence", func(d *Descriptor) { d.Evidence = "hoped" }, true},
		{"no evidence", func(d *Descriptor) { d.Evidence = "" }, true},
		{"unknown deployment", func(d *Descriptor) { d.Deployment = "cloud" }, true},
		{"target without a factory", func(*Descriptor) {}, false},
		{"target without an address", func(d *Descriptor) { d.Address = "" }, true},
		{"target without a username use", func(d *Descriptor) { d.Username.Use = "" }, true},
		{"target with an unknown secret use", func(d *Descriptor) { d.Secret.Use = "maybe" }, true},
		{"agent with a factory", func(d *Descriptor) { d.Deployment = DeploymentAgent }, true},
		{"options that are not a struct", func(d *Descriptor) { d.Options = 3 }, true},
		{"an option without doc", func(d *Descriptor) {
			d.Options = struct {
				A string `yaml:"a"`
			}{}
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := testDescriptor("acme")
			tc.mutate(&d)
			if err := d.Validate(tc.hasFactory); err == nil {
				t.Fatalf("accepted %+v", d)
			}
		})
	}

	// Register is the gate: an incomplete descriptor does not get in.
	d := testDescriptor("test-incomplete")
	d.Title = ""
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("Register accepted an incomplete descriptor")
			}
		}()
		Register(d, nopFactory)
	}()
	if _, ok := Lookup("test-incomplete"); ok {
		t.Fatal("a refused descriptor was registered")
	}
}

type sampleOptions struct {
	Name     string            `yaml:"name" doc:"A string." default:"x"`
	On       bool              `yaml:"on" doc:"A boolean."`
	Count    *int              `yaml:"count" doc:"An integer, optional."`
	Wide     int64             `yaml:"wide" doc:"Another integer."`
	Every    time.Duration     `yaml:"every" doc:"A duration."`
	Skip     []string          `yaml:"skip" doc:"A list." enum:"a,b"`
	TokenRef Secret            `yaml:"token_file" doc:"A reference."`
	Names    map[string]string `yaml:"names" doc:"A text map."`
	Vids     map[string]int    `yaml:"vids" doc:"An integer map."`
}

func TestOptionFields(t *testing.T) {
	d := testDescriptor("acme")
	d.Options = sampleOptions{}
	got, err := d.OptionFields()
	if err != nil {
		t.Fatal(err)
	}
	want := []OptionField{
		{Name: "name", Kind: OptionString, Default: "x", Description: "A string."},
		{Name: "on", Kind: OptionBoolean, Description: "A boolean."},
		{Name: "count", Kind: OptionInteger, Description: "An integer, optional."},
		{Name: "wide", Kind: OptionInteger, Description: "Another integer."},
		{Name: "every", Kind: OptionDuration, Description: "A duration."},
		{Name: "skip", Kind: OptionList, Values: []string{"a", "b"}, Description: "A list."},
		{Name: "token_file", Kind: OptionSecretReference, Description: "A reference."},
		{Name: "names", Kind: OptionMap, ValueKind: OptionString, Description: "A text map."},
		{Name: "vids", Kind: OptionMap, ValueKind: OptionInteger, Description: "An integer map."},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	// A pointer to the struct documents the same.
	d.Options = &sampleOptions{}
	if got2, err := d.OptionFields(); err != nil || len(got2) != len(want) {
		t.Fatalf("pointer: %v, %v", got2, err)
	}
	// No options is fine.
	d.Options = nil
	if got, err := d.OptionFields(); err != nil || got != nil {
		t.Fatalf("nil: %v, %v", got, err)
	}

	for name, opts := range map[string]any{
		"no doc tag": struct {
			A string `yaml:"a"`
		}{},
		"no yaml tag": struct {
			A string `doc:"a"`
		}{},
		"skipped yaml": struct {
			A string `yaml:"-" doc:"a"`
		}{},
		"a map with int keys": struct {
			A map[int]string `yaml:"a" doc:"a"`
		}{},
		"a map of lists": struct {
			A map[string][]string `yaml:"a" doc:"a"`
		}{},
		"a map of structs": struct {
			A map[string]struct{ B string } `yaml:"a" doc:"a"`
		}{},
		"a list of ints": struct {
			A []int `yaml:"a" doc:"a"`
		}{},
		"unexported": struct{ a string }{},
	} {
		d.Options = opts
		if _, err := d.OptionFields(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestModulesSortedAndLookup(t *testing.T) {
	Register(testDescriptor("test-zz"), nopFactory)
	Register(testDescriptor("test-aa"), nopFactory)
	var kinds []string
	for _, d := range Modules() {
		kinds = append(kinds, d.Kind)
	}
	if !slices.IsSorted(kinds) {
		t.Fatalf("modules are not sorted: %v", kinds)
	}
	if !slices.Contains(kinds, "test-aa") || !slices.Contains(kinds, "test-zz") {
		t.Fatalf("registered modules are missing: %v", kinds)
	}
	d, ok := Lookup("test-aa")
	if !ok || d.Kind != "test-aa" || d.Title != "Test module" {
		t.Fatalf("Lookup: %+v, %v", d, ok)
	}
	if _, ok := Lookup("nope"); ok {
		t.Fatal("Lookup found a kind nobody registered")
	}
}

func TestNewRefusesAnAgentOnlyKind(t *testing.T) {
	d := testDescriptor("test-hostbound")
	d.Deployment = DeploymentAgent
	Register(d, nil)
	if slices.Contains(Kinds(), "test-hostbound") {
		t.Fatal("an agent module is listed as a kind a target can name")
	}
	if _, ok := Lookup("test-hostbound"); !ok {
		t.Fatal("an agent module is not described")
	}
	_, err := New(Target{Kind: "test-hostbound", Name: "x"})
	if err == nil || !strings.Contains(err.Error(), "installed on the host") {
		t.Fatalf("New: %v", err)
	}
}

package collect

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/VargaFoundation/trica/internal/schema"
)

// Deployment is where a module runs.
type Deployment string

const (
	// DeploymentTarget: a targets-file entry, run by internal/collect/runner
	// (in the server or in `trica collector`).
	DeploymentTarget Deployment = "target"
	// DeploymentAgent: installed on the host it inventories (trica-agent). It
	// has no target and no factory.
	DeploymentAgent Deployment = "agent"
)

// Use says whether a credential is needed.
type Use string

const (
	UseRequired Use = "required"
	UseOptional Use = "optional"
	UseUnused   Use = "unused"
)

// Evidence is what a module's fakes are built from, as the testdata/README.md
// of its package says.
type Evidence string

const (
	EvidenceSynthesised Evidence = "synthesised"
	EvidenceRecorded    Evidence = "recorded"
	// EvidenceField: checked against real equipment in the field.
	EvidenceField Evidence = "field"
)

// Credential says how a module uses one of the target's two credential keys.
type Credential struct {
	Use Use
	// Meaning says what to put there: "API key consumer_key:token_key:token_secret".
	Meaning string
}

// Descriptor is what a module says about itself. It is registered together
// with the module's factory (ADR 0055); the API and the Collectors pages are
// built from it, so a new module appears there without any change to them.
type Descriptor struct {
	// Kind is the source prefix ("arista"): ^[a-z][a-z0-9-]*$.
	Kind string
	// Title is the product name, untranslated ("Arista EOS").
	Title   string
	Vendors []string
	// Protocol: "eAPI: JSON-RPC 2.0 over HTTPS".
	Protocol string
	// Summary is one sentence, in both languages.
	Summary    schema.Label
	Deployment Deployment
	// Collects lists the object types the module emits (schema names). The
	// fakes' runs fail on a type that is not listed.
	Collects []string
	// ReadOnly says how read-only is enforced.
	ReadOnly string
	// Address says what a target's `address` takes (target modules only).
	Address string
	// Username and Secret describe the target's `username` and `password`
	// keys (target modules only). The latter is always a reference.
	Username Credential
	Secret   Credential
	// Options is the zero value of the struct DecodeOptions decodes the
	// target's options into; nil when the module has none. OptionFields
	// documents it from its tags.
	Options any
	// Tested lists the equipment versions the fakes stand for.
	Tested   []string
	Evidence Evidence
	// Docs is the repository-relative path of the module's README.
	Docs string
}

var kindPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func (u Use) valid() bool { return u == UseRequired || u == UseOptional || u == UseUnused }

// Validate reports why d cannot be registered. hasFactory says whether it is
// registered with a factory.
func (d Descriptor) Validate(hasFactory bool) error {
	var errs []error
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	if !kindPattern.MatchString(d.Kind) {
		bad("kind %q must match %s", d.Kind, kindPattern)
	}
	for name, v := range map[string]string{"title": d.Title, "protocol": d.Protocol, "read_only": d.ReadOnly, "docs": d.Docs} {
		if strings.TrimSpace(v) == "" {
			bad("%s is empty", name)
		}
	}
	if strings.TrimSpace(d.Summary.EN) == "" || strings.TrimSpace(d.Summary.FR) == "" {
		bad("summary needs both en and fr")
	}
	for name, v := range map[string][]string{"vendors": d.Vendors, "collects": d.Collects, "tested": d.Tested} {
		if len(v) == 0 {
			bad("%s is empty", name)
		}
	}
	switch d.Evidence {
	case EvidenceSynthesised, EvidenceRecorded, EvidenceField:
	default:
		bad("evidence %q is not synthesised, recorded or field", d.Evidence)
	}
	switch d.Deployment {
	case DeploymentTarget:
		if !hasFactory {
			bad("a target module needs a factory")
		}
		if strings.TrimSpace(d.Address) == "" {
			bad("address is empty")
		}
		if !d.Username.Use.valid() || !d.Secret.Use.valid() {
			bad("username and secret need a use (required, optional or unused)")
		}
	case DeploymentAgent:
		if hasFactory {
			bad("an agent module has no factory")
		}
	default:
		bad("deployment %q is not target or agent", d.Deployment)
	}
	if _, err := d.OptionFields(); err != nil {
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("collector module %q: %w", d.Kind, err)
	}
	return nil
}

// OptionKind is the type of an option, as the API and the UI name it.
type OptionKind string

const (
	OptionString          OptionKind = "string"
	OptionBoolean         OptionKind = "boolean"
	OptionInteger         OptionKind = "integer"
	OptionDuration        OptionKind = "duration"
	OptionList            OptionKind = "list"
	OptionSecretReference OptionKind = "secret_reference"
)

// OptionField documents one key of a target's `options:` block.
type OptionField struct {
	Name        string
	Kind        OptionKind
	Default     string
	Values      []string
	Description string
}

var (
	secretType   = reflect.TypeOf(Secret(""))
	durationType = reflect.TypeOf(time.Duration(0))
)

// OptionFields lists the options the module accepts, reflected from the
// struct DecodeOptions decodes into, so that the documentation cannot drift
// from the code: the yaml tag is the key, `doc` the description (required),
// `default` and `enum` the rest.
func (d Descriptor) OptionFields() ([]OptionField, error) {
	if d.Options == nil {
		return nil, nil
	}
	t := reflect.TypeOf(d.Options)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("options: %s is not a struct", t)
	}
	var out []OptionField
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			return nil, fmt.Errorf("options: field %s is not exported, so it cannot be decoded", f.Name)
		}
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if name == "" || name == "-" {
			return nil, fmt.Errorf("options: field %s has no yaml tag", f.Name)
		}
		doc := strings.TrimSpace(f.Tag.Get("doc"))
		if doc == "" {
			return nil, fmt.Errorf("options: %s has no doc tag", name)
		}
		kind, err := optionKind(f.Type)
		if err != nil {
			return nil, fmt.Errorf("options: %s: %w", name, err)
		}
		of := OptionField{Name: name, Kind: kind, Default: f.Tag.Get("default"), Description: doc}
		if enum := f.Tag.Get("enum"); enum != "" {
			of.Values = strings.Split(enum, ",")
		}
		out = append(out, of)
	}
	return out, nil
}

func optionKind(t reflect.Type) (OptionKind, error) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t {
	case secretType:
		return OptionSecretReference, nil
	case durationType:
		return OptionDuration, nil
	}
	switch t.Kind() {
	case reflect.String:
		return OptionString, nil
	case reflect.Bool:
		return OptionBoolean, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return OptionInteger, nil
	case reflect.Slice:
		if t.Elem().Kind() == reflect.String {
			return OptionList, nil
		}
	}
	return "", fmt.Errorf("type %s has no option kind", t)
}

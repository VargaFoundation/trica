package catalog

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Format and Version identify a catalog document. Another value is refused:
// a file meant for something else must not withdraw the applications.
const (
	Format  = "trica-catalog"
	Version = 1
)

// Document is one catalog file. Several files (a directory, a ConfigMap with
// several keys) are merged into one catalog: a team declared in one file can
// own an application declared in another.
type Document struct {
	Format       string        `yaml:"format"`
	Version      int           `yaml:"version"`
	Teams        []Team        `yaml:"teams"`
	People       []Person      `yaml:"people"`
	Environments []Environment `yaml:"environments"`
	Applications []Application `yaml:"applications"`
}

// Team owns applications.
type Team struct {
	Name  string `yaml:"name"`
	Email string `yaml:"email"`
	Chat  string `yaml:"chat"`
}

// Person owns applications; identified by e-mail.
type Person struct {
	Email string `yaml:"email"`
	Name  string `yaml:"name"`
	Phone string `yaml:"phone"`
}

// Environment declares what an environment is for.
type Environment struct {
	Name string `yaml:"name"`
	Kind string `yaml:"kind"`
}

// Application is one business application.
type Application struct {
	// Name is the slug, the application's identity: the one the kubernetes
	// collector derives (app.kubernetes.io/part-of, the Argo CD project or the
	// trica.io/application annotation), so that both describe one object.
	Name        string `yaml:"name"`
	DisplayName string `yaml:"display_name"`
	Description string `yaml:"description"`
	Domain      string `yaml:"domain"`
	Criticality string `yaml:"criticality"`
	Lifecycle   string `yaml:"lifecycle"`
	Hosting     string `yaml:"hosting"`
	Vendor      string `yaml:"vendor"`
	Repository  string `yaml:"repository"`
	// Owner is a team name or a person's e-mail declared in the catalog.
	Owner string `yaml:"owner"`
	// DependsOn and Calls name other applications, of this catalog or
	// discovered: a hard dependency (its failure takes this one down) and a
	// soft one (its failure degrades this one).
	DependsOn  []string    `yaml:"depends_on"`
	Calls      []string    `yaml:"calls"`
	Components []Component `yaml:"components"`
	Instances  []Instance  `yaml:"instances"`
}

// Component is a deployable part of an application.
type Component struct {
	Name       string `yaml:"name"`
	Kind       string `yaml:"kind"`
	Technology string `yaml:"technology"`
}

// Instance is a component running somewhere no collector derives it from:
// a VM, a server, a host the agent inventories. Instances in Kubernetes are
// discovered and need not be declared.
type Instance struct {
	Component   string `yaml:"component"`
	Environment string `yaml:"environment"`
	// Location tells two instances of a component in one environment apart
	// (the host they run on, a site); default: the name of the first runs_on.
	Location  string   `yaml:"location"`
	Version   string   `yaml:"version"`
	RunsOn    []Host   `yaml:"runs_on"`
	Endpoints []string `yaml:"endpoints"`
}

// Host designates an object of the inventory an instance runs on, by the
// identities its collectors give it.
type Host struct {
	Type   string `yaml:"type"`
	Name   string `yaml:"name"`
	Serial string `yaml:"serial"`
	UUID   string `yaml:"uuid"`
}

// Enumerations, as the schema declares them. A value outside is refused at
// parse time: the engine would drop it with a warning, and a catalog is
// written by people who want to know.
var (
	criticalities  = []string{"low", "medium", "high", "critical"}
	lifecycles     = []string{"planned", "development", "production", "deprecated", "retired"}
	hostings       = []string{"self", "saas"}
	componentKinds = []string{"service", "worker", "database", "cache", "queue", "frontend", "batch", "other"}
	envKinds       = []string{"prod", "staging", "dev", "test", "build", "other"}
	hostTypes      = []string{"device", "vm"}
)

// slugPattern is what an application or component name must be: the form the
// kubernetes collector gives the names it derives, so that a declared
// "Billing" never stands beside a discovered "billing".
var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

var notSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	return strings.Trim(notSlug.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-"), "-")
}

// Parse reads one catalog file. Unknown keys are an error: a misspelt key
// would otherwise be a value silently not declared.
func Parse(r io.Reader) (*Document, error) {
	var d Document
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&d); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("empty document")
		}
		return nil, err
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err == nil {
		return nil, errors.New("more than one YAML document: one catalog per file")
	}
	if d.Format != Format {
		return nil, fmt.Errorf("format is %q, want %q", d.Format, Format)
	}
	if d.Version != Version {
		return nil, fmt.Errorf("version is %d, want %d", d.Version, Version)
	}
	return &d, nil
}

// ParseBytes is Parse on a byte slice.
func ParseBytes(b []byte) (*Document, error) { return Parse(bytes.NewReader(b)) }

// Merge puts several documents into one. Duplicates are found by Validate.
func Merge(docs ...*Document) *Document {
	out := &Document{Format: Format, Version: Version}
	for _, d := range docs {
		out.Teams = append(out.Teams, d.Teams...)
		out.People = append(out.People, d.People...)
		out.Environments = append(out.Environments, d.Environments...)
		out.Applications = append(out.Applications, d.Applications...)
	}
	return out
}

// Validate checks a merged catalog. It returns every problem at once, so that
// one run tells the author everything to fix. A catalog with a problem is not
// applied at all (the run fails and withdraws nothing).
func (d *Document) Validate() error {
	var errs []error
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	enum := func(where, field, v string, values []string) {
		if v != "" && !slices.Contains(values, v) {
			bad("%s: %s %q is not one of %s", where, field, v, strings.Join(values, ", "))
		}
	}

	teams := map[string]bool{}
	for i, t := range d.Teams {
		n := strings.TrimSpace(t.Name)
		switch {
		case n == "":
			bad("teams[%d]: name is required", i)
		case teams[n]:
			bad("team %q is declared twice", n)
		}
		teams[n] = true
	}
	people := map[string]bool{}
	for i, p := range d.People {
		e := strings.ToLower(strings.TrimSpace(p.Email))
		switch {
		case !strings.Contains(e, "@"):
			bad("people[%d]: email %q is not an e-mail address", i, p.Email)
		case people[e]:
			bad("person %q is declared twice", e)
		}
		people[e] = true
	}
	envs := map[string]bool{}
	for i, e := range d.Environments {
		n := strings.TrimSpace(e.Name)
		switch {
		case n == "":
			bad("environments[%d]: name is required", i)
		case envs[n]:
			bad("environment %q is declared twice", n)
		}
		envs[n] = true
		enum("environment "+n, "kind", e.Kind, envKinds)
	}

	apps := map[string]bool{}
	for i := range d.Applications {
		a := &d.Applications[i]
		where := fmt.Sprintf("applications[%d]", i)
		if a.Name != "" {
			where = "application " + a.Name
		}
		switch {
		case a.Name == "":
			bad("%s: name is required", where)
		case !slugPattern.MatchString(a.Name):
			bad("%s: name must be a slug (lower case, digits, single dashes), as discovery names applications: %q", where, slug(a.Name))
		case apps[a.Name]:
			bad("%s: declared twice", where)
		}
		apps[a.Name] = true
		enum(where, "criticality", a.Criticality, criticalities)
		enum(where, "lifecycle", a.Lifecycle, lifecycles)
		enum(where, "hosting", a.Hosting, hostings)
		if o := strings.TrimSpace(a.Owner); o != "" {
			if strings.Contains(o, "@") {
				if !people[strings.ToLower(o)] {
					bad("%s: owner %q is not in people", where, o)
				}
			} else if !teams[o] {
				bad("%s: owner %q is not in teams", where, o)
			}
		}
		if a.Repository != "" && cleanURL(a.Repository) != a.Repository {
			bad("%s: repository must hold no credentials, query or fragment", where)
		}
		for _, dep := range append(append([]string(nil), a.DependsOn...), a.Calls...) {
			switch {
			case !slugPattern.MatchString(dep):
				bad("%s: dependency %q is not an application name (a slug)", where, dep)
			case dep == a.Name:
				bad("%s: depends on itself", where)
			}
		}
		comps := map[string]bool{}
		for j, c := range a.Components {
			cw := fmt.Sprintf("%s: components[%d]", where, j)
			switch {
			case !slugPattern.MatchString(c.Name):
				bad("%s: name %q must be a slug", cw, c.Name)
			case comps[c.Name]:
				bad("%s: component %q declared twice", cw, c.Name)
			}
			comps[c.Name] = true
			enum(cw, "kind", c.Kind, componentKinds)
		}
		if a.Hosting == "saas" && len(a.Instances) > 0 {
			bad("%s: a saas application has no instance (its vendor runs it)", where)
		}
		keys := map[string]bool{}
		for j, in := range a.Instances {
			iw := fmt.Sprintf("%s: instances[%d]", where, j)
			if !comps[in.Component] {
				bad("%s: component %q is not in the application's components", iw, in.Component)
			}
			if strings.TrimSpace(in.Environment) == "" {
				bad("%s: environment is required", iw)
			}
			for k, h := range in.RunsOn {
				hw := fmt.Sprintf("%s: runs_on[%d]", iw, k)
				enum(hw, "type", h.Type, hostTypes)
				switch {
				case h.Type == "":
					bad("%s: type is required (device or vm)", hw)
				case h.Name == "" && h.Serial == "" && h.UUID == "":
					bad("%s: name, serial or uuid is required", hw)
				case h.Serial != "" && h.Type != "device":
					bad("%s: serial designates a device", hw)
				case h.UUID != "" && h.Type != "vm":
					bad("%s: uuid designates a vm", hw)
				}
			}
			loc := in.location()
			if loc == "" {
				bad("%s: location is required when no runs_on has a name", iw)
			}
			if key := instanceKey(a.Name, in.Component, in.Environment, loc); keys[key] {
				bad("%s: two instances with the key %s (set location to tell them apart)", iw, key)
			} else {
				keys[key] = true
			}
			for _, e := range in.Endpoints {
				if _, err := endpointProtocol(e); err != nil {
					bad("%s: endpoint %q: %v", iw, e, err)
				}
			}
		}
	}
	return errors.Join(errs...)
}

// location is the declared location, else the name of the first host with one.
func (in Instance) location() string {
	if l := strings.TrimSpace(in.Location); l != "" {
		return l
	}
	for _, h := range in.RunsOn {
		if n := strings.TrimSpace(h.Name); n != "" {
			return n
		}
	}
	return ""
}

// instanceKey is the component_instance identity, the scheme of the
// kubernetes collector (application/component/environment/location, ADR
// 0069) with a host or a site in place of the namespace.
func instanceKey(app, comp, env, loc string) string {
	return app + "/" + comp + "/" + slug(env) + "/" + slug(loc)
}

// endpointProtocol returns the protocol of an endpoint: the scheme of a URL,
// "tcp" for host:port. Credentials, a query or a fragment are refused.
func endpointProtocol(raw string) (string, error) {
	if !strings.Contains(raw, "://") {
		host, port, ok := strings.Cut(raw, ":")
		if !ok || host == "" || port == "" || strings.ContainsAny(raw, "/@?# ") {
			return "", errors.New("must be a URL (https://host/path) or host:port")
		}
		return "tcp", nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", errors.New("not a URL")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("must hold no credentials, query or fragment")
	}
	return strings.ToLower(u.Scheme), nil
}

// cleanURL is raw without credentials, query and fragment; raw itself when it
// is not a URL with a scheme.
func cleanURL(raw string) string {
	if !strings.Contains(raw, "://") {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	return u.String()
}

package catalog

import (
	"strings"
	"testing"
)

const head = "format: trica-catalog\nversion: 1\n"

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		name, src, want string
	}{
		{"the fixture", string(Fixture("applications.yaml")), ""},
		{"empty", "", "empty document"},
		{"another format", "format: trica-inventory\nversion: 1\n", `format is "trica-inventory"`},
		{"another version", "format: trica-catalog\nversion: 2\n", "version is 2"},
		{"no version", "format: trica-catalog\n", "version is 0"},
		{"unknown key", head + "applications:\n  - name: a\n    critical: high\n", "field critical not found"},
		{"unknown top-level key", head + "apps: []\n", "field apps not found"},
		{"two documents", head + "---\n" + head, "more than one YAML document"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseBytes([]byte(tc.src))
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("error %v, want one containing %q", err, tc.want)
			}
		})
	}
}

// Every rule of Validate, each with the smallest catalog that breaks it. A
// rule removed from Validate turns its case red.
func TestValidate(t *testing.T) {
	ok := head + "teams: [{name: t}]\npeople: [{email: P@example.org}]\n"
	for _, tc := range []struct {
		name, src, want string
	}{
		{"fixture is valid", string(Fixture("applications.yaml")), ""},
		{"owner by e-mail in another case", ok + "applications: [{name: a, owner: p@EXAMPLE.org}]\n", ""},
		{"team without name", head + "teams: [{email: x@example.org}]\n", "teams[0]: name is required"},
		{"team twice", head + "teams: [{name: t}, {name: t}]\n", `team "t" is declared twice`},
		{"person without e-mail", head + "people: [{name: x}]\n", "is not an e-mail address"},
		{"person twice", head + "people: [{email: a@example.org}, {email: A@example.org}]\n", "declared twice"},
		{"environment without name", head + "environments: [{kind: prod}]\n", "environments[0]: name is required"},
		{"environment twice", head + "environments: [{name: p}, {name: p}]\n", `environment "p" is declared twice`},
		{"environment kind", head + "environments: [{name: p, kind: production}]\n", `kind "production" is not one of`},
		{"application without name", head + "applications: [{display_name: X}]\n", "applications[0]: name is required"},
		{"name not a slug", head + "applications: [{name: Billing App}]\n", `must be a slug (lower case, digits, single dashes), as discovery names applications: "billing-app"`},
		{"application twice", head + "applications: [{name: a}, {name: a}]\n", "application a: declared twice"},
		{"criticality", head + "applications: [{name: a, criticality: urgent}]\n", `criticality "urgent"`},
		{"lifecycle", head + "applications: [{name: a, lifecycle: live}]\n", `lifecycle "live"`},
		{"hosting", head + "applications: [{name: a, hosting: cloud}]\n", `hosting "cloud"`},
		{"unknown team", ok + "applications: [{name: a, owner: nobody}]\n", `owner "nobody" is not in teams`},
		{"unknown person", ok + "applications: [{name: a, owner: x@example.org}]\n", `owner "x@example.org" is not in people`},
		{"repository with credentials", head + "applications: [{name: a, repository: 'https://u:tok@git.example.org/a'}]\n", "repository must hold no credentials"},
		{"dependency not a slug", head + "applications: [{name: a, depends_on: [Entra ID]}]\n", `dependency "Entra ID" is not an application name`},
		{"calls itself", head + "applications: [{name: a, calls: [a]}]\n", "depends on itself"},
		{"component not a slug", head + "applications: [{name: a, components: [{name: API}]}]\n", `name "API" must be a slug`},
		{"component twice", head + "applications: [{name: a, components: [{name: c}, {name: c}]}]\n", `component "c" declared twice`},
		{"component kind", head + "applications: [{name: a, components: [{name: c, kind: db}]}]\n", `kind "db"`},
		{"saas with instances", head + "applications: [{name: a, hosting: saas, components: [{name: c}], instances: [{component: c, environment: p, location: x}]}]\n", "a saas application has no instance"},
		{"instance of an unknown component", head + "applications: [{name: a, instances: [{component: c, environment: p, location: x}]}]\n", `component "c" is not in the application's components`},
		{"instance without environment", head + "applications: [{name: a, components: [{name: c}], instances: [{component: c, location: x}]}]\n", "environment is required"},
		{"instance without location", head + "applications: [{name: a, components: [{name: c}], instances: [{component: c, environment: p, runs_on: [{type: device, serial: S}]}]}]\n", "location is required"},
		{"host without type", head + "applications: [{name: a, components: [{name: c}], instances: [{component: c, environment: p, runs_on: [{name: h}]}]}]\n", "type is required"},
		{"host type", head + "applications: [{name: a, components: [{name: c}], instances: [{component: c, environment: p, runs_on: [{type: pod, name: h}]}]}]\n", `type "pod"`},
		{"host without identity", head + "applications: [{name: a, components: [{name: c}], instances: [{component: c, environment: p, location: x, runs_on: [{type: vm}]}]}]\n", "name, serial or uuid is required"},
		{"serial of a vm", head + "applications: [{name: a, components: [{name: c}], instances: [{component: c, environment: p, location: x, runs_on: [{type: vm, serial: S}]}]}]\n", "serial designates a device"},
		{"uuid of a device", head + "applications: [{name: a, components: [{name: c}], instances: [{component: c, environment: p, location: x, runs_on: [{type: device, uuid: U}]}]}]\n", "uuid designates a vm"},
		{"two instances, one key", head + "applications: [{name: a, components: [{name: c}], instances: [{component: c, environment: p, location: X}, {component: c, environment: p, location: x}]}]\n", "two instances with the key a/c/p/x"},
		{"endpoint with credentials", head + "applications: [{name: a, components: [{name: c}], instances: [{component: c, environment: p, location: x, endpoints: ['https://u:p@h.example.org']}]}]\n", "must hold no credentials"},
		{"endpoint neither URL nor host:port", head + "applications: [{name: a, components: [{name: c}], instances: [{component: c, environment: p, location: x, endpoints: [h.example.org]}]}]\n", "must be a URL"},
		{"endpoint URL without host", head + "applications: [{name: a, components: [{name: c}], instances: [{component: c, environment: p, location: x, endpoints: ['https://']}]}]\n", "not a URL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, err := ParseBytes([]byte(tc.src))
			if err != nil {
				t.Fatal(err)
			}
			err = d.Validate()
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("error %v, want one containing %q", err, tc.want)
			}
		})
	}
}

// All problems are reported at once: the author fixes the file in one go.
func TestValidateReportsEverything(t *testing.T) {
	d, err := ParseBytes([]byte(head + "applications: [{name: A}, {name: b, criticality: x}, {name: c, owner: z}]\n"))
	if err != nil {
		t.Fatal(err)
	}
	err = d.Validate()
	if err == nil {
		t.Fatal("no error")
	}
	if n := strings.Count(err.Error(), "\n") + 1; n != 3 {
		t.Errorf("%d problems reported, want 3:\n%v", n, err)
	}
}

func TestEndpointProtocol(t *testing.T) {
	for in, want := range map[string]string{
		"https://app.example.org/x": "https",
		"GRPC://app.example.org":    "grpc",
		"db.example.org:5432":       "tcp",
	} {
		if got, err := endpointProtocol(in); err != nil || got != want {
			t.Errorf("%s: %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"db.example.org", ":5432", "db.example.org:", "u@db.example.org:5432", "db.example.org:5432/x", "https://h.example.org/?a=1", "https://h.example.org/#f"} {
		if _, err := endpointProtocol(in); err == nil {
			t.Errorf("%s accepted", in)
		}
	}
}

func TestInstanceKeyAndLocation(t *testing.T) {
	in := Instance{Component: "db", Environment: "Prod EU", RunsOn: []Host{{Type: "device", Serial: "S"}, {Type: "vm", Name: "DB-01"}}}
	if got := in.location(); got != "DB-01" {
		t.Errorf("location %q, want the first named host", got)
	}
	if got := instanceKey("erp", "db", in.Environment, in.location()); got != "erp/db/prod-eu/db-01" {
		t.Errorf("key %q", got)
	}
	in.Location = " dc1 "
	if got := in.location(); got != "dc1" {
		t.Errorf("location %q, want the declared one", got)
	}
}

func TestCleanURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://u:p@git.example.org/a?x=1#f": "https://git.example.org/a",
		"git@git.example.org:org/repo.git":    "git@git.example.org:org/repo.git",
		"https://git.example.org/%zz":         "",
	} {
		if got := cleanURL(in); got != want {
			t.Errorf("%s: %q, want %q", in, got, want)
		}
	}
}

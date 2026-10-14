package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// probeServices lists systemd services from two sources, merged by unit name:
//
//   - `systemctl list-units --type=service --output=json`, through the Runner:
//     the loaded units and their active state. It is a read-only query of the
//     service manager; reading the unit files alone cannot tell what runs.
//   - the *.wants directories under /etc/systemd/system: the units that are
//     enabled, which no listing of loaded units gives for a stopped service.
//
// Without systemctl (or on a fake root) only the enabled units are reported,
// without a state. A host that does not run systemd is skipped.
func (c *collector) probeServices() error {
	if !exists(c.fs, "/run/systemd/system") {
		return skipf("systemd is not running")
	}
	units := map[string]*Service{}
	get := func(name string) *Service {
		s := units[name]
		if s == nil {
			s = &Service{Name: name}
			units[name] = s
		}
		return s
	}
	for _, name := range c.enabledServices() {
		get(name).Enabled = true
	}

	var failure error
	out, err := c.run.Run(c.ctx, "systemctl", "list-units", "--type=service", "--output=json", "--no-pager")
	switch {
	case errors.Is(err, ErrNotFound):
		c.inv.Skipped = append(c.inv.Skipped, "services: systemctl not available, service states unknown")
	case err != nil && len(out) == 0:
		failure = fmt.Errorf("systemctl list-units: %w", err)
	default:
		var list []struct {
			Unit   string `json:"unit"`
			Load   string `json:"load"`
			Active string `json:"active"`
		}
		if jerr := json.Unmarshal(out, &list); jerr != nil {
			failure = fmt.Errorf("systemctl list-units output: %w", jerr)
			break
		}
		for _, u := range list {
			// not-found units are references to units that do not exist (a
			// dependency, a removed package): not services of this host.
			if !strings.HasSuffix(u.Unit, ".service") || u.Load == "not-found" {
				continue
			}
			get(u.Unit).State = u.Active
		}
	}

	for _, s := range units {
		c.inv.Services = append(c.inv.Services, *s)
	}
	sort.Slice(c.inv.Services, func(i, j int) bool { return c.inv.Services[i].Name < c.inv.Services[j].Name })
	return failure
}

// enabledServices returns the services enabled by a symbolic link in a
// *.wants directory under /etc/systemd/system (what `systemctl enable` creates).
func (c *collector) enabledServices() []string {
	const base = "/etc/systemd/system"
	ents, err := c.fs.ReadDir(base)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if !e.IsDir() || !strings.HasSuffix(e.Name(), ".wants") {
			continue
		}
		links, err := c.fs.ReadDir(base + "/" + e.Name())
		if err != nil {
			continue
		}
		for _, l := range links {
			if strings.HasSuffix(l.Name(), ".service") {
				out = append(out, l.Name())
			}
		}
	}
	return out
}

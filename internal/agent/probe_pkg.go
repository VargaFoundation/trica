package agent

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

const dpkgStatus = "/var/lib/dpkg/status"

// probePackages lists installed packages. Debian-family hosts are read from
// the dpkg status file, a plain file, so no command runs. Red Hat-family hosts
// have no readable equivalent (the rpm database is SQLite or Berkeley DB), so
// `rpm -qa` is asked through the Runner when rpm is installed; without it the
// probe is skipped, with a note.
func (c *collector) probePackages() error {
	r, err := c.fs.Open(dpkgStatus)
	if err == nil {
		defer r.Close()
		pkgs, err := parseDpkgStatus(r)
		if err != nil {
			return fmt.Errorf("dpkg status: %w", err)
		}
		c.inv.Packages = pkgs
		return nil
	}
	if !skippable(err) {
		return err
	}
	out, err := c.run.Run(c.ctx, "rpm", "-qa", "--qf", rpmQueryFormat)
	if errors.Is(err, ErrNotFound) {
		return skipf("no package database: %s absent and rpm not installed", dpkgStatus)
	}
	if err != nil && len(out) == 0 {
		return fmt.Errorf("rpm -qa: %w", err)
	}
	c.inv.Packages = parseRPM(string(out))
	return nil
}

// parseDpkgStatus reads the stanzas of a dpkg status file and keeps the
// packages whose status ends in "installed": removed packages that left
// their configuration behind ("config-files") and half-installed ones are not
// installed software.
func parseDpkgStatus(r io.Reader) ([]Package, error) {
	var out []Package
	var cur Package
	installed := false
	flush := func() {
		if installed && cur.Name != "" && cur.Version != "" {
			out = append(out, cur)
		}
		cur, installed = Package{}, false
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20) // a Description can be long
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			flush()
			continue
		}
		if line[0] == ' ' || line[0] == '\t' { // continuation of a multi-line field
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch k {
		case "Package":
			cur.Name = v
		case "Version":
			cur.Version = v
		case "Architecture":
			cur.Arch = v
		case "Status":
			f := strings.Fields(v)
			installed = len(f) == 3 && f[2] == "installed"
		}
	}
	flush()
	if err := sc.Err(); err != nil {
		return nil, err
	}
	sortPackages(out)
	return out, nil
}

// parseRPM reads `name<TAB>[epoch:]version-release<TAB>arch` lines. The GPG
// keys rpm stores as pseudo-packages are not software.
func parseRPM(s string) []Package {
	var out []Package
	for _, line := range strings.Split(s, "\n") {
		f := strings.Split(strings.TrimSpace(line), "\t")
		if len(f) != 3 || f[0] == "" || f[0] == "gpg-pubkey" {
			continue
		}
		out = append(out, Package{Name: f[0], Version: f[1], Arch: f[2]})
	}
	sortPackages(out)
	return out
}

func sortPackages(p []Package) {
	sort.Slice(p, func(i, j int) bool {
		a, b := p[i], p[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.Version != b.Version {
			return a.Version < b.Version
		}
		return a.Arch < b.Arch
	})
}

package redfish

import (
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/netip"
	"regexp"
	"strings"
	"time"
)

// first returns the first non-empty value, trimmed.
func first(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// healthOf maps a Redfish Health to the schema's enum; "" when the resource
// does not say.
func healthOf(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return ""
	case "ok":
		return "ok"
	case "warning":
		return "warning"
	case "critical":
		return "critical"
	}
	return "unknown"
}

// partHealth is the health of a component: absent when the slot is empty.
func partHealth(s status) string {
	if s.absent() {
		return "absent"
	}
	return healthOf(s.Health)
}

func round(f *float64) *int64 {
	if f == nil {
		return nil
	}
	v := int64(math.Round(*f))
	return &v
}

// mib converts a size in MiB to bytes; 0 and absent give nil.
func mib(n *int64) *int64 {
	if n == nil || *n <= 0 {
		return nil
	}
	v := *n << 20
	return &v
}

// uniqueNames makes the names of sibling objects distinct and non-empty, so
// that two of them never share an identity: a duplicate gets its member id
// (or its position) appended, an empty one is named after its position.
func uniqueNames(names, ids []string) []string {
	count := map[string]int{}
	for _, n := range names {
		count[n]++
	}
	out := make([]string, len(names))
	used := map[string]bool{}
	for i, n := range names {
		switch {
		case n == "":
			n = fmt.Sprintf("item-%d", i+1)
		case count[n] > 1 && ids[i] != "":
			n += " " + ids[i]
		case count[n] > 1:
			n = fmt.Sprintf("%s %d", n, i+1)
		}
		for base, k := n, 2; used[n]; k++ {
			n = fmt.Sprintf("%s #%d", base, k)
		}
		used[n] = true
		out[i] = n
	}
	return out
}

// usableIPv4 reports whether a BMC address is one to record: set, valid, and
// not the 0.0.0.0 placeholder of an unconfigured interface.
func usableIPv4(s string) (string, bool) {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil || !a.Is4() || a.IsUnspecified() {
		return "", false
	}
	return a.String(), true
}

// prefixLength converts a dotted mask to a prefix length.
func prefixLength(mask string) (int, bool) {
	ip := net.ParseIP(strings.TrimSpace(mask)).To4()
	if ip == nil {
		return 0, false
	}
	ones, bits := net.IPMask(ip).Size()
	if bits == 0 {
		return 0, false
	}
	return ones, true
}

// linkState maps the LinkStatus vocabularies (Redfish: LinkUp, LinkDown,
// NoLink; iLO: Up, Down) to the schema's enum.
func linkState(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return ""
	case "up", "linkup":
		return "up"
	case "down", "linkdown", "nolink":
		return "down"
	}
	return "unknown"
}

// mediaOf maps drive technology and protocol to the schema's media enum.
func mediaOf(mediaType, protocol string) string {
	if strings.EqualFold(strings.TrimSpace(protocol), "nvme") {
		return "nvme"
	}
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "hdd":
		return "hdd"
	case "ssd":
		return "ssd"
	}
	return "unknown"
}

// standbyImage says whether a firmware component is a copy kept for recovery
// (HPE's backup or redundant system ROM) rather than the one that runs.
func standbyImage(name string) bool {
	n := strings.ToLower(name)
	return strings.HasPrefix(n, "backup ") || strings.HasPrefix(n, "redundant ")
}

var (
	// HPE writes the build date into the version: "P89 v2.60 (05/21/2018)"
	// for a system ROM, "2.78 Dec 08 2020" for an iLO.
	slashDate = regexp.MustCompile(`\b(\d{2}/\d{2}/\d{4})\b`)
	wordDate  = regexp.MustCompile(`\b((?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) \d{1,2} \d{4})\b`)
	// Before this, a date is a placeholder (Dell reports "00:00:00Z").
	firstRelease = time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC)
)

// releaseDate is the day a firmware was released, as YYYY-MM-DD: the
// inventory's ReleaseDate when it holds a real date, else a date written in
// the version, else "".
func releaseDate(reported, version string) string {
	valid := func(t time.Time, err error) string {
		if err != nil || t.Before(firstRelease) {
			return ""
		}
		return t.Format(time.DateOnly)
	}
	if r := strings.TrimSpace(reported); r != "" {
		if d := valid(time.Parse(time.RFC3339, r)); d != "" {
			return d
		}
		if d := valid(time.Parse(time.DateOnly, r)); d != "" {
			return d
		}
	}
	if m := slashDate.FindStringSubmatch(version); m != nil {
		if d := valid(time.Parse("01/02/2006", m[1])); d != "" {
			return d
		}
	}
	if m := wordDate.FindStringSubmatch(version); m != nil {
		return valid(time.Parse("Jan 2 2006", m[1]))
	}
	return ""
}

// fwLocation reads a firmware component's Location when it is a plain
// string (the iLO 4 inventory); the standard object form is left aside.
func fwLocation(raw json.RawMessage) string {
	var s string
	if len(raw) == 0 || json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return strings.TrimSpace(s)
}

package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type lldpID struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type lldpIface struct {
	Via     string          `json:"via"`
	Chassis json.RawMessage `json:"chassis"`
	Port    struct {
		ID    lldpID `json:"id"`
		Descr string `json:"descr"`
	} `json:"port"`
}

// probeLLDP asks lldpd for its neighbours (`lldpctl -f json`, through the
// Runner). lldpd listens on the wire as root and lldpctl only reads its
// table through a local socket. A host without lldpd is not an error: most
// servers have none.
func (c *collector) probeLLDP() error {
	out, err := c.run.Run(c.ctx, "lldpctl", "-f", "json")
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil && len(out) == 0 {
		return fmt.Errorf("lldpctl: %w", err)
	}
	ns, notes, err := parseLLDP(out)
	if err != nil {
		return fmt.Errorf("lldpctl output: %w", err)
	}
	c.inv.Neighbors = ns
	for _, n := range notes {
		c.inv.Skipped = append(c.inv.Skipped, "lldp: "+n)
	}
	return nil
}

var protoVersion = regexp.MustCompile(`v[0-9]+$`)

// parseLLDP decodes lldpctl's JSON. Two layouts exist in the wild and both are
// accepted: lldpd 1.0.x prints the interfaces as an object keyed by name and
// the chassis as an object keyed by system name, older versions print arrays
// of single-key objects, and a neighbour that sends no system name has an
// unnamed chassis ({"id": {...}, "descr": ...}). Neighbours whose identity is
// unusable come back as notes, not neighbours.
func parseLLDP(data []byte) ([]Neighbor, []string, error) {
	var doc struct {
		LLDP struct {
			Interface json.RawMessage `json:"interface"`
		} `json:"lldp"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, nil, err
	}
	if len(doc.LLDP.Interface) == 0 || string(doc.LLDP.Interface) == "null" {
		return nil, nil, nil // lldpd runs and sees nobody
	}
	ifaces := map[string]lldpIface{}
	if err := json.Unmarshal(doc.LLDP.Interface, &ifaces); err != nil {
		var list []map[string]lldpIface
		if err2 := json.Unmarshal(doc.LLDP.Interface, &list); err2 != nil {
			return nil, nil, fmt.Errorf("interface list: %w", err)
		}
		for _, m := range list {
			for k, v := range m {
				ifaces[k] = v
			}
		}
	}

	names := make([]string, 0, len(ifaces))
	for n := range ifaces {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []Neighbor
	var notes []string
	for _, local := range names {
		i := ifaces[local]
		sys, chassis, err := lldpChassis(i.Chassis)
		if err != nil {
			return nil, nil, fmt.Errorf("chassis on %s: %w", local, err)
		}
		n := Neighbor{
			Local:         local,
			Protocol:      protoVersion.ReplaceAllString(strings.ToLower(i.Via), ""),
			ChassisIDType: strings.ToLower(chassis.Type),
			ChassisID:     strings.ToLower(chassis.Value),
			SysName:       strings.ToLower(sys),
			PortIDType:    strings.ToLower(i.Port.ID.Type),
			PortID:        i.Port.ID.Value,
			PortDescr:     i.Port.Descr,
		}
		if n.Protocol == "" {
			n.Protocol = "lldp"
		}
		if n.ChassisIDType != "mac" && n.SysName == "" {
			notes = append(notes, fmt.Sprintf("neighbour on %s has neither a MAC chassis id nor a system name, ignored", local))
			continue
		}
		if n.peerPort() == "" {
			notes = append(notes, fmt.Sprintf("neighbour on %s has no usable port name, ignored", local))
			continue
		}
		out = append(out, n)
	}
	return out, notes, nil
}

// lldpChassis returns the system name (empty when unnamed) and the chassis id.
func lldpChassis(raw json.RawMessage) (string, lldpID, error) {
	var id lldpID
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", id, err
	}
	// Unnamed: {"id": {"type": ..., "value": ...}, "descr": ...}.
	if v, ok := m["id"]; ok {
		if err := json.Unmarshal(v, &id); err == nil && id.Value != "" {
			return "", id, nil
		}
	}
	// Named: {"<sysname>": {"id": {...}, ...}}.
	for name, v := range m {
		var inner struct {
			ID lldpID `json:"id"`
		}
		if err := json.Unmarshal(v, &inner); err != nil {
			return "", id, err
		}
		return name, inner.ID, nil
	}
	return "", id, nil
}

// peerPort is the name the neighbour's port goes by in its own interface
// table: the port id when it is an interface name or a locally assigned name,
// the description when the port id is a MAC address (then the name is only in the description).
func (n Neighbor) peerPort() string {
	switch n.PortIDType {
	case "ifname", "local", "ifalias":
		return n.PortID
	}
	return n.PortDescr
}

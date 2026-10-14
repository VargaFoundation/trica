package maas

import (
	"bytes"
	"fmt"
	"strconv"
)

// num is a JSON number that may also arrive as a quoted string or with a
// fractional part ("size": "480103981056", "storage": 480.1). MAAS has changed
// this kind of detail between releases; being lenient costs nothing.
type num int64

func (n *num) UnmarshalJSON(b []byte) error {
	b = bytes.Trim(bytes.TrimSpace(b), `"`)
	if len(b) == 0 || string(b) == "null" {
		*n = 0
		return nil
	}
	if v, err := strconv.ParseInt(string(b), 10, 64); err == nil {
		*n = num(v)
		return nil
	}
	f, err := strconv.ParseFloat(string(b), 64)
	if err != nil {
		return fmt.Errorf("%q is not a number", b)
	}
	*n = num(f)
	return nil
}

// whoami is the answer of GET users/?op=whoami.
type whoami struct {
	Username    string `json:"username"`
	IsSuperuser bool   `json:"is_superuser"`
}

// versionInfo is the answer of GET version/.
type versionInfo struct {
	Version    string `json:"version"`
	Subversion string `json:"subversion"`
}

// named is the {id, name} shape of a zone or a resource pool.
type named struct {
	Name string `json:"name"`
}

// vlanInfo is a VLAN as embedded in fabrics, subnets and interfaces.
type vlanInfo struct {
	VID    num    `json:"vid"`
	Name   string `json:"name"`
	Fabric string `json:"fabric"`
	MTU    num    `json:"mtu"`
	DHCPOn bool   `json:"dhcp_on"`
}

// fabric is one entry of GET fabrics/.
type fabric struct {
	Name  string     `json:"name"`
	VLANs []vlanInfo `json:"vlans"`
}

// subnet is one entry of GET subnets/, or the subnet of an interface link.
type subnet struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CIDR        string    `json:"cidr"`
	GatewayIP   string    `json:"gateway_ip"`
	VLAN        *vlanInfo `json:"vlan"`
}

// link is one address assignment of an interface.
type link struct {
	Mode      string  `json:"mode"`
	IPAddress string  `json:"ip_address"`
	Subnet    *subnet `json:"subnet"`
}

// iface is one entry of a machine's interface_set (or its boot_interface).
type iface struct {
	Name          string    `json:"name"`
	Type          string    `json:"type"`
	MACAddress    string    `json:"mac_address"`
	VLAN          *vlanInfo `json:"vlan"`
	Parents       []string  `json:"parents"`
	Enabled       *bool     `json:"enabled"`
	LinkConnected *bool     `json:"link_connected"`
	LinkSpeed     num       `json:"link_speed"`
	EffectiveMTU  num       `json:"effective_mtu"`
	Links         []link    `json:"links"`
}

// blockDevice is one entry of a machine's blockdevice_set.
type blockDevice struct {
	Name            string   `json:"name"`
	Type            string   `json:"type"`
	Model           string   `json:"model"`
	Serial          string   `json:"serial"`
	Size            num      `json:"size"`
	Tags            []string `json:"tags"`
	FirmwareVersion string   `json:"firmware_version"`
}

// machine is one entry of GET machines/. Only the fields trica reads are
// declared; the rest of the (large) document is ignored.
type machine struct {
	SystemID       string         `json:"system_id"`
	Hostname       string         `json:"hostname"`
	FQDN           string         `json:"fqdn"`
	StatusName     string         `json:"status_name"`
	OSystem        string         `json:"osystem"`
	DistroSeries   string         `json:"distro_series"`
	HWEKernel      string         `json:"hwe_kernel"`
	PowerType      string         `json:"power_type"`
	PowerState     string         `json:"power_state"`
	CPUCount       num            `json:"cpu_count"`
	Memory         num            `json:"memory"` // MiB
	HardwareUUID   string         `json:"hardware_uuid"`
	HardwareInfo   map[string]any `json:"hardware_info"`
	BootInterface  *iface         `json:"boot_interface"`
	InterfaceSet   []iface        `json:"interface_set"`
	BlockDeviceSet []blockDevice  `json:"blockdevice_set"`
	IPAddresses    []string       `json:"ip_addresses"`
	Owner          string         `json:"owner"`
	Zone           named          `json:"zone"`
	Pool           named          `json:"pool"`
	TagNames       []string       `json:"tag_names"`
}

package pfsense

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// pfSense stores most values as strings and the REST API is not uniform about
// returning them as strings, numbers or booleans (a subnet is "24" on one
// endpoint and 24 on another). The types below accept all three, so that a
// representation change in a REST API release degrades to "same value", not
// to a malformed-response failure of a whole endpoint.

// str is a scalar read as text. Objects and arrays read as "".
type str string

func (s *str) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return nil
	}
	switch b[0] {
	case '"':
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*s = str(strings.TrimSpace(v))
	case '{', '[':
		*s = ""
	default: // number, true, false, null
		if string(b) == "null" {
			*s = ""
			return nil
		}
		*s = str(b)
	}
	return nil
}

func (s str) String() string { return string(s) }

// strs is a list of texts. A scalar reads as a one-element list.
type strs []string

func (l *strs) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '[' {
		var raw []str
		if err := json.Unmarshal(b, &raw); err != nil {
			return err
		}
		for _, r := range raw {
			if r != "" {
				*l = append(*l, string(r))
			}
		}
		return nil
	}
	var s str
	if err := s.UnmarshalJSON(b); err != nil {
		return err
	}
	if s != "" {
		*l = strs{string(s)}
	}
	return nil
}

// flag is a boolean that may be written true, "true", "yes", "on", 1.
type flag bool

func (f *flag) UnmarshalJSON(b []byte) error {
	var s str
	if err := s.UnmarshalJSON(b); err != nil {
		return err
	}
	switch strings.ToLower(string(s)) {
	case "true", "yes", "on", "enabled", "1":
		*f = true
	default:
		*f = false
	}
	return nil
}

// Models. Only the fields the collector maps are declared; the rest of the
// answer is ignored, which keeps the collector tolerant to additions.

type versionInfo struct {
	Version str `json:"version"`
	Patch   str `json:"patch"`
}

type systemStatus struct {
	Platform  str             `json:"platform"`
	Serial    str             `json:"serial"`
	NetgateID str             `json:"netgate_id"`
	CPUModel  str             `json:"cpu_model"`
	CPUCount  str             `json:"cpu_count"`
	Uptime    json.RawMessage `json:"uptime"`
}

type hostnameInfo struct {
	Hostname str `json:"hostname"`
	Domain   str `json:"domain"`
}

// ifaceConfig is one entry of GET interfaces (the configuration).
type ifaceConfig struct {
	ID       str   `json:"id"`
	If       str   `json:"if"`
	Descr    str   `json:"descr"`
	Enable   *flag `json:"enable"`
	IPAddr   str   `json:"ipaddr"`
	Subnet   str   `json:"subnet"`
	IPAddrV6 str   `json:"ipaddrv6"`
	SubnetV6 str   `json:"subnetv6"`
	MTU      str   `json:"mtu"`
}

// ifaceStatus is one entry of GET status/interfaces (the live state).
type ifaceStatus struct {
	Name     str `json:"name"`
	Descr    str `json:"descr"`
	HWIf     str `json:"hwif"`
	If       str `json:"if"`
	Status   str `json:"status"`
	MACAddr  str `json:"macaddr"`
	IPAddr   str `json:"ipaddr"`
	Subnet   str `json:"subnet"`
	IPAddrV6 str `json:"ipaddrv6"`
	SubnetV6 str `json:"subnetv6"`
	Media    str `json:"media"`
	MTU      str `json:"mtu"`
}

type vlanConfig struct {
	If     str `json:"if"`
	Tag    str `json:"tag"`
	Descr  str `json:"descr"`
	VLANIf str `json:"vlanif"`
}

type gatewayConfig struct {
	Name      str  `json:"name"`
	Gateway   str  `json:"gateway"`
	Monitor   str  `json:"monitor"`
	Interface str  `json:"interface"`
	Disabled  flag `json:"disabled"`
	DefaultGW flag `json:"defaultgw"`
}

type gatewayStatus struct {
	Name      str `json:"name"`
	MonitorIP str `json:"monitorip"`
	Status    str `json:"status"`
	Delay     str `json:"delay"`
	Loss      str `json:"loss"`
}

type staticRoute struct {
	Network  str  `json:"network"`
	Gateway  str  `json:"gateway"`
	Descr    str  `json:"descr"`
	Disabled flag `json:"disabled"`
}

// rule covers the three rule families; each family fills the fields it has.
type rule struct {
	Tracker         str  `json:"tracker"`
	AssociatedRule  str  `json:"associated_rule_id"`
	CreatedTime     str  `json:"created_time"`
	Type            str  `json:"type"`
	Interface       strs `json:"interface"`
	Protocol        str  `json:"protocol"`
	Source          str  `json:"source"`
	Destination     str  `json:"destination"`
	DestinationPort str  `json:"destination_port"`
	Target          str  `json:"target"`
	TargetSubnet    str  `json:"target_subnet"`
	LocalPort       str  `json:"local_port"`
	External        str  `json:"external"`
	NoNAT           flag `json:"nonat"`
	Disabled        flag `json:"disabled"`
	Descr           str  `json:"descr"`
}

// openvpnConfig covers servers and clients.
type openvpnConfig struct {
	VPNID          str  `json:"vpnid"`
	Mode           str  `json:"mode"`
	Protocol       str  `json:"protocol"`
	Description    str  `json:"description"`
	Disabled       flag `json:"disabled"`
	ServerAddr     str  `json:"server_addr"`
	ServerPort     str  `json:"server_port"`
	TunnelNetwork  strs `json:"tunnel_network"`
	TunnelNetwork6 strs `json:"tunnel_networkv6"`
	LocalNetwork   strs `json:"local_network"`
	RemoteNetwork  strs `json:"remote_network"`
}

type openvpnStatus struct {
	VPNID  str `json:"vpnid"`
	Name   str `json:"name"`
	Status str `json:"status"`
}

type ipsecPhase1 struct {
	IKEID         str  `json:"ikeid"`
	RemoteGateway str  `json:"remote_gateway"`
	Descr         str  `json:"descr"`
	Disabled      flag `json:"disabled"`
	Mobile        flag `json:"mobile"`
}

type ipsecSA struct {
	ConID str `json:"con_id"`
	IKEID str `json:"ikeid"`
	State str `json:"state"`
}

// atoi reads a decimal integer, ok false when s is not one.
func atoi(s str) (int, bool) {
	n, err := strconv.Atoi(string(s))
	return n, err == nil
}

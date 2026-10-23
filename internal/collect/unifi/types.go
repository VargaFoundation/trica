package unifi

import (
	"bytes"
	"encoding/json"
	"strings"
)

// The decode structs below are an allow-list: a field that is not declared is
// never read, so it cannot reach a batch. The answers carry personal data
// (the account's e-mail, name and permissions under userData, the console's
// coordinates and address under location) and noise (ids, bridges, feature
// flags); none of it is declared here, and privacy_test.go keeps it so.

// str is a scalar read as text. Numbers and booleans read as their text,
// objects, arrays and null as "": the Site Manager API is young and a value
// that changes representation must degrade to "unset", not fail the call.
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
	default:
		if string(b) == "null" {
			*s = ""
			return nil
		}
		*s = str(b)
	}
	return nil
}

func (s str) String() string { return string(s) }

// flag is a boolean that may also be written "true", "yes" or 1.
type flag bool

func (f *flag) UnmarshalJSON(b []byte) error {
	var s str
	if err := s.UnmarshalJSON(b); err != nil {
		return err
	}
	switch strings.ToLower(string(s)) {
	case "true", "yes", "on", "1":
		*f = true
	default:
		*f = false
	}
	return nil
}

// host is one entry of GET /v1/hosts: a console or a self-hosted server.
type host struct {
	ID            str            `json:"id"`
	Type          str            `json:"type"`
	IPAddress     str            `json:"ipAddress"`
	IsBlocked     flag           `json:"isBlocked"`
	ReportedState *reportedState `json:"reportedState"`
}

type reportedState struct {
	Hostname    str          `json:"hostname"`
	Name        str          `json:"name"`
	MAC         str          `json:"mac"`
	IP          str          `json:"ip"`
	Version     str          `json:"version"`
	State       str          `json:"state"`
	IPAddrs     []str        `json:"ipAddrs"`
	Hardware    hardware     `json:"hardware"`
	WANs        []wan        `json:"wans"`
	Controllers []controller `json:"controllers"`
	Features    struct {
		HasGateway flag `json:"hasGateway"`
	} `json:"features"`
}

type hardware struct {
	Name            str `json:"name"`
	Shortname       str `json:"shortname"`
	MAC             str `json:"mac"`
	Serialno        str `json:"serialno"`
	FirmwareVersion str `json:"firmwareVersion"`
}

type wan struct {
	Enabled   flag `json:"enabled"`
	Plugged   flag `json:"plugged"`
	Interface str  `json:"interface"`
	IPv4      str  `json:"ipv4"`
	IPv6      str  `json:"ipv6"`
	MAC       str  `json:"mac"`
	SpeedType str  `json:"speedType"`
	Type      str  `json:"type"`
}

type controller struct {
	Name        str  `json:"name"`
	Version     str  `json:"version"`
	State       str  `json:"state"`
	IsInstalled flag `json:"isInstalled"`
	IsRunning   flag `json:"isRunning"`
}

// site is one entry of GET /v1/sites. Only the WAN statistics are read: the
// site's name, time zone, counts and gateway are not.
type site struct {
	SiteID     str `json:"siteId"`
	HostID     str `json:"hostId"`
	Statistics struct {
		ISPInfo ispInfo            `json:"ispInfo"`
		WANs    map[string]wanStat `json:"wans"`
	} `json:"statistics"`
}

type wanStat struct {
	ExternalIP str     `json:"externalIp"`
	PortUp     *flag   `json:"portUp"`
	ISPInfo    ispInfo `json:"ispInfo"`
}

// ispInfo keeps the provider's name only: the AS number and organisation are
// not mapped.
type ispInfo struct {
	Name str `json:"name"`
}

// deviceGroup is one entry of GET /v1/devices: the devices of one host.
type deviceGroup struct {
	HostID    str      `json:"hostId"`
	UpdatedAt str      `json:"updatedAt"`
	Devices   []device `json:"devices"`
}

type device struct {
	ID              str  `json:"id"`
	MAC             str  `json:"mac"`
	Name            str  `json:"name"`
	Model           str  `json:"model"`
	Shortname       str  `json:"shortname"`
	IP              str  `json:"ip"`
	ProductLine     str  `json:"productLine"`
	Status          str  `json:"status"`
	Version         str  `json:"version"`
	FirmwareStatus  str  `json:"firmwareStatus"`
	UpdateAvailable str  `json:"updateAvailable"`
	Note            str  `json:"note"`
	StartupTime     str  `json:"startupTime"`
	IsConsole       flag `json:"isConsole"`
	IsManaged       flag `json:"isManaged"`
}

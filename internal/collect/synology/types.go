package synology

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// The decode structs below are an allow-list: a field that is not declared is
// never read, so it cannot reach a batch. DSM answers carry account and
// network data trica has no use for (NTP and DNS servers, the SNMP community
// of the UPS, share names); none of it is declared here.

// str is a scalar read as text. Numbers and booleans read as their text,
// objects, arrays and null as "": DSM is not consistent about types (cpu_cores
// is "4", ram_size 4096), and a value that changes representation must degrade
// to "unset", not fail the call.
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

// num is a number written as a JSON number or as text. OK is false when the
// value is absent, null or not a number, so that 0 can be told from unset.
type num struct {
	V  float64
	OK bool
}

func (n *num) UnmarshalJSON(b []byte) error {
	var s str
	if err := s.UnmarshalJSON(b); err != nil {
		return err
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(string(s)), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		*n = num{}
		return nil
	}
	*n = num{V: v, OK: true}
	return nil
}

// Int returns the value truncated, and whether there was one.
func (n num) Int() (int64, bool) { return int64(n.V), n.OK }

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

// envelope is the answer shape of every DSM Web API call.
type envelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   *struct {
		Code num `json:"code"`
	} `json:"error"`
}

// loginData is the data of a successful login. The session id is the only
// field read; it is a credential and is never logged.
type loginData struct {
	SID str `json:"sid"`
}

// SYNO.Core.System info.
type systemInfo struct {
	Model              str  `json:"model"`
	Serial             str  `json:"serial"`
	FirmwareVer        str  `json:"firmware_ver"`
	UpTime             str  `json:"up_time"`  // H:MM:SS, H may exceed 24
	RAMSize            num  `json:"ram_size"` // MiB
	CPUCores           num  `json:"cpu_cores"`
	SysTempWarn        flag `json:"sys_tempwarn"`
	TemperatureWarning flag `json:"temperature_warning"`
}

// SYNO.DSM.Info getinfo.
type dsmInfo struct {
	Model         str  `json:"model"`
	Serial        str  `json:"serial"`
	VersionString str  `json:"version_string"`
	Uptime        num  `json:"uptime"` // seconds
	RAM           num  `json:"ram"`    // MiB
	TempWarn      flag `json:"temperature_warn"`
}

// SYNO.Core.System info, type=network.
type systemNetwork struct {
	Hostname str   `json:"hostname"`
	Nif      []nif `json:"nif"`
}

type nif struct {
	ID     str `json:"id"`
	MAC    str `json:"mac"`
	Addr   str `json:"addr"`
	Mask   str `json:"mask"`
	MTU    num `json:"mtu"`
	Speed  num `json:"speed"` // Mb/s, -1 or 0 when unknown
	Status str `json:"status"`
	Type   str `json:"type"`
}

// SYNO.Core.Network.Interface list: one element per interface.
type ifaceEntry struct {
	IfName str `json:"ifname"`
	IP     str `json:"ip"`
	Mask   str `json:"mask"`
	Speed  num `json:"speed"`
	Status str `json:"status"`
	Type   str `json:"type"`
}

// SYNO.Core.Network get.
type networkInfo struct {
	ServerName  str `json:"server_name"`
	GatewayInfo struct {
		IP str `json:"ip"`
	} `json:"gateway_info"`
}

// SYNO.Core.Package list.
type packageList struct {
	Packages []pkg `json:"packages"`
}

type pkg struct {
	ID         str `json:"id"`
	Name       str `json:"name"`
	Version    str `json:"version"`
	Additional struct {
		Status     str `json:"status"`
		Maintainer str `json:"maintainer"`
	} `json:"additional"`
}

// SYNO.Core.ExternalDevice.UPS get. The SNMP and ACL fields are not declared.
type upsInfo struct {
	Enable        flag `json:"enable"`
	USBUPSConnect flag `json:"usb_ups_connect"`
	Manufacture   str  `json:"manufacture"`
	Model         str  `json:"model"`
	Status        str  `json:"status"`
}

// SYNO.Docker.Container list.
type containerList struct {
	Containers []container `json:"containers"`
}

type container struct {
	Name   str `json:"name"`
	Image  str `json:"image"`
	Status str `json:"status"`
	State  struct {
		Running flag `json:"Running"`
		Status  str  `json:"Status"`
	} `json:"State"`
}

// SYNO.Storage.CGI.Storage load_info.
type storageInfo struct {
	Disks        []disk   `json:"disks"`
	StoragePools []status `json:"storagePools"`
	Volumes      []status `json:"volumes"`
	SSDCaches    []status `json:"ssdCaches"`
}

// status is what a pool, a volume or a cache contributes: its id and state.
type status struct {
	ID     str `json:"id"`
	Status str `json:"status"`
}

type disk struct {
	ID         str  `json:"id"`
	LongName   str  `json:"longName"`
	Serial     str  `json:"serial"`
	Model      str  `json:"model"`
	Firm       str  `json:"firm"`
	SizeTotal  num  `json:"size_total"`
	DiskType   str  `json:"diskType"`
	IsSSD      flag `json:"isSsd"`
	Temp       num  `json:"temp"`
	Status     str  `json:"status"`
	RemainLife struct {
		Trustable flag `json:"trustable"`
		Value     num  `json:"value"`
	} `json:"remain_life"`
}

package unifi

import "encoding/json"

// The decode structs of the console's Network Integration API are an
// allow-list like those of types.go: what is not declared is never read. The
// client list (names, MACs, addresses of people's devices), the networks and
// their VLANs, the radios' channels and the Wi-Fi configuration are not
// declared, and the endpoints that carry them are never requested.

// netPage is the envelope of every list of the Network Integration API.
type netPage struct {
	Offset     str             `json:"offset"`
	Limit      str             `json:"limit"`
	Count      str             `json:"count"`
	TotalCount str             `json:"totalCount"`
	Data       json.RawMessage `json:"data"`
}

// netInfo is GET /v1/info.
type netInfo struct {
	ApplicationVersion str `json:"applicationVersion"`
}

// netSite is one entry of GET /v1/sites.
type netSite struct {
	ID                str `json:"id"`
	InternalReference str `json:"internalReference"`
	Name              str `json:"name"`
}

// netDeviceOverview is one entry of GET /v1/sites/{id}/devices. Features and
// interfaces are lists of names here (switching, accessPoint, gateway; ports,
// radios).
type netDeviceOverview struct {
	ID                str     `json:"id"`
	MACAddress        str     `json:"macAddress"`
	IPAddress         str     `json:"ipAddress"`
	Name              str     `json:"name"`
	Model             str     `json:"model"`
	State             str     `json:"state"`
	Supported         flag    `json:"supported"`
	FirmwareVersion   str     `json:"firmwareVersion"`
	FirmwareUpdatable flag    `json:"firmwareUpdatable"`
	Features          strList `json:"features"`
	Interfaces        strList `json:"interfaces"`
}

// strList is a list of names. Anything else (the detail answer writes
// features as an object) reads as an empty list, never as an error.
type strList []str

func (l *strList) UnmarshalJSON(b []byte) error {
	var v []str
	if json.Unmarshal(b, &v) != nil {
		*l = nil
		return nil
	}
	*l = v
	return nil
}

// netDeviceDetails is GET /v1/sites/{id}/devices/{id}: the ports and the
// uplink. Its features are an object there and are not read (the overview's
// list is); the radios are not read.
type netDeviceDetails struct {
	ID         str `json:"id"`
	Interfaces struct {
		Ports []netPort `json:"ports"`
	} `json:"interfaces"`
	Uplink *struct {
		DeviceID str `json:"deviceId"`
	} `json:"uplink"`
}

// netPort is one entry of a device's ports.
type netPort struct {
	Idx          str     `json:"idx"`
	State        str     `json:"state"`
	Connector    str     `json:"connector"`
	SpeedMbps    str     `json:"speedMbps"`
	MaxSpeedMbps str     `json:"maxSpeedMbps"`
	PoE          *netPoE `json:"poe"`
}

// netPoE is the PoE of a port that can supply power. Its type (1 to 4) is
// declared both as a string and as an int32 in the specification, and is not
// read.
type netPoE struct {
	Enabled  flag `json:"enabled"`
	Standard str  `json:"standard"`
	State    str  `json:"state"`
}

// netSnapshot holds what one Network Integration API run read.
type netSnapshot struct {
	version string
	sites   []netSiteData
	// partial is set when something that should have been read was not and
	// the run is not a fault of the console (an id that is not a UUID).
	partial bool
}

// netSiteData is one selected site with its devices.
type netSiteData struct {
	site    netSite
	devices []netDeviceOverview
	// details are the answers of the per-device calls, by device id; a device
	// whose call failed is absent.
	details map[string]*netDeviceDetails
	// haveDevices is false when the site's device list could not be read.
	haveDevices bool
}

package redfish

import (
	"bytes"
	"encoding/json"
	"strings"
)

// The types below decode only what the collector reads. Go matches JSON keys
// case-insensitively, which absorbs the spellings vendors differ on
// (HealthRollup / HealthRollUp, Id / ID). Fields that exist in two vendor
// flavours (HPE OEM "Hp" on iLO 4, "Hpe" on iLO 5) are decoded side by side
// and merged by the accessors.

// link is a reference to another resource.
type link struct {
	ID string `json:"@odata.id"`
}

func (l *link) path() string {
	if l == nil {
		return ""
	}
	return strings.TrimSpace(l.ID)
}

// collection is a Redfish collection of links, possibly paged.
type collection struct {
	Members []link `json:"Members"`
	Next    string `json:"Members@odata.nextLink"`
}

func (c collection) paths() []string {
	out := make([]string, 0, len(c.Members))
	for _, m := range c.Members {
		if p := m.path(); p != "" {
			out = append(out, p)
		}
	}
	return out
}

type status struct {
	Health       string
	HealthRollup string
	State        string
}

// rollup is the health including sub-components when the resource says it,
// the resource's own health otherwise.
func (s status) rollup() string {
	if s.HealthRollup != "" {
		return s.HealthRollup
	}
	return s.Health
}

func (s status) absent() bool { return strings.EqualFold(strings.TrimSpace(s.State), "Absent") }

// fwVersion is a firmware version that iLO 4 spells as an object
// ({"Current": {"VersionString": "6.06"}}) and everything else as a string.
type fwVersion string

func (v *fwVersion) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || bytes.Equal(b, []byte("null")) {
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*v = fwVersion(strings.TrimSpace(s))
		return nil
	}
	var o struct {
		Current struct{ VersionString string }
	}
	if err := json.Unmarshal(b, &o); err != nil {
		return err
	}
	*v = fwVersion(strings.TrimSpace(o.Current.VersionString))
	return nil
}

// looseString is a string that ignores any other JSON kind. Some fields are a
// string on one firmware and an object on the next (a controller's Location);
// not being able to read one must not lose the whole document.
type looseString string

func (v *looseString) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*v = looseString(strings.TrimSpace(s))
	}
	return nil
}

type serviceRoot struct {
	RedfishVersion string
	Systems        *link
	Chassis        *link
	Managers       *link
	UpdateService  *link
}

type smartStorage struct {
	Links struct {
		ArrayControllers *link
	}
}

type hpeSystem struct {
	Bios struct {
		Current struct{ VersionString string }
	}
	Links struct {
		Memory            *link
		SmartStorage      *link
		FirmwareInventory *link
	}
}

type system struct {
	Name             string
	Manufacturer     string
	Model            string
	SKU              string
	SerialNumber     string
	UUID             string
	HostName         string
	PowerState       string
	BiosVersion      string
	IndicatorLED     string
	Status           status
	ProcessorSummary struct {
		Count *int
		Model string
	}
	MemorySummary struct {
		TotalSystemMemoryGiB *float64
		TotalSystemMemoryGB  *float64
	}
	Processors         *link
	Memory             *link
	Storage            *link
	EthernetInterfaces *link
	LogServices        *link
	Links              struct {
		Chassis   []link
		ManagedBy []link
	}
	Oem struct {
		Hp  *hpeSystem
		Hpe *hpeSystem
	}
}

// hpe returns the HPE OEM extension, whichever way the firmware spells it.
func (s system) hpe() hpeSystem {
	switch {
	case s.Oem.Hpe != nil:
		return *s.Oem.Hpe
	case s.Oem.Hp != nil:
		return *s.Oem.Hp
	}
	return hpeSystem{}
}

type processor struct {
	ID            string
	Name          string
	Socket        string
	Model         string
	ProcessorType string
	TotalCores    *int
	TotalThreads  *int
	Status        status
}

type memoryModule struct {
	ID                  string
	Name                string
	DeviceLocator       string
	SocketLocator       string
	PartNumber          string
	SerialNumber        string
	DIMMStatus          string
	CapacityMiB         *int64
	SizeMB              *int64
	OperatingSpeedMhz   *int64
	MaximumFrequencyMHz *int64
	Status              status
}

type ipv4 struct {
	Address       string
	SubnetMask    string
	Gateway       string
	AddressOrigin string
}

type ethernet struct {
	ID          string
	Name        string
	Description string
	MACAddress  string
	LinkStatus  string
	SpeedMbps   *int64
	FullDuplex  *bool
	Status      status
	IPv4        []ipv4 `json:"IPv4Addresses"`
}

type controller struct {
	MemberID        string
	ID              string
	Name            string
	Location        looseString
	Model           string
	SerialNumber    string
	FirmwareVersion fwVersion
	Status          status
}

type storage struct {
	ID                 string
	Name               string
	StorageControllers []controller
	Drives             []link
}

type arrayController struct {
	controller
	Links struct {
		PhysicalDrives *link
	}
}

type hpeDrive struct {
	SSDEnduranceUtilizationPercentage *float64
}

type drive struct {
	ID                                string
	Name                              string
	Model                             string
	SerialNumber                      string
	MediaType                         string
	Protocol                          string
	InterfaceType                     string
	Revision                          string
	FirmwareVersion                   fwVersion
	CapacityBytes                     *int64
	CapacityMiB                       *int64
	Status                            status
	PredictedMediaLifeLeftPercent     *float64
	SSDEnduranceUtilizationPercentage *float64
	Location                          json.RawMessage
	PhysicalLocation                  struct {
		PartLocation struct{ ServiceLabel string }
	}
	Identifiers []struct {
		DurableName       string
		DurableNameFormat string
	}
	Oem struct {
		Hp  *hpeDrive
		Hpe *hpeDrive
	}
}

// location is where the drive sits: iLO 4 writes it as a string
// ("1I:1:1"), the standard Drive as a list of {Info, InfoFormat}.
func (d drive) location() string {
	raw := bytes.TrimSpace(d.Location)
	if len(raw) == 0 {
		return strings.TrimSpace(d.PhysicalLocation.PartLocation.ServiceLabel)
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var l []struct{ Info string }
	if json.Unmarshal(raw, &l) == nil && len(l) > 0 {
		return strings.TrimSpace(l[0].Info)
	}
	return strings.TrimSpace(d.PhysicalLocation.PartLocation.ServiceLabel)
}

// wearPct is the share of the endurance used, from the standard
// PredictedMediaLifeLeftPercent or the HPE OEM
// SSDEnduranceUtilizationPercentage (top level on iLO 4, under Oem on iLO 5).
func (d drive) wearPct() *float64 {
	if d.PredictedMediaLifeLeftPercent != nil {
		v := 100 - *d.PredictedMediaLifeLeftPercent
		return &v
	}
	for _, p := range []*float64{
		d.SSDEnduranceUtilizationPercentage,
		oemEndurance(d.Oem.Hpe),
		oemEndurance(d.Oem.Hp),
	} {
		if p != nil {
			return p
		}
	}
	return nil
}

func oemEndurance(h *hpeDrive) *float64 {
	if h == nil {
		return nil
	}
	return h.SSDEnduranceUtilizationPercentage
}

type chassis struct {
	ID      string
	Name    string
	Power   *link
	Thermal *link
}

type psu struct {
	MemberID           string
	Name               string
	Model              string
	SerialNumber       string
	FirmwareVersion    string
	PowerCapacityWatts *float64
	PowerInputWatts    *float64
	Status             status
}

type power struct {
	PowerControl []struct {
		PowerConsumedWatts *float64
		PowerMetrics       struct {
			AverageConsumedWatts *float64
		}
	}
	PowerSupplies []psu
}

type temperature struct {
	MemberID                  string
	Name                      string
	PhysicalContext           string
	ReadingCelsius            *float64
	UpperThresholdNonCritical *float64
	UpperThresholdCritical    *float64
	Status                    status
}

type fan struct {
	MemberID       string
	Name           string
	FanName        string
	Reading        *float64
	CurrentReading *float64
	ReadingUnits   string
	Units          string
	Status         status
}

type thermal struct {
	Temperatures []temperature
	Fans         []fan
}

type manager struct {
	ID                 string
	FirmwareVersion    string
	EthernetInterfaces *link
	LogServices        *link
}

type updateService struct {
	FirmwareInventory *link
}

type fwEntry struct {
	ID            string
	Name          string
	Version       string
	VersionString string
	Status        status
	// ReleaseDate and SoftwareId are standard since SoftwareInventory 1.1;
	// Dell fills them, often with a placeholder date ("00:00:00Z").
	ReleaseDate string
	SoftwareID  string `json:"SoftwareId"`
	Updateable  *bool
	// Location is a string in the iLO 4 document; elsewhere it is an object
	// or absent, and HPE says where the component sits in DeviceContext.
	Location json.RawMessage
	Oem      struct {
		Hpe struct{ DeviceContext string }
	}
}

// firmwareInventory is either the standard collection of components or the
// iLO 4 flavour, one document listing them by category under "Current".
type firmwareInventory struct {
	Members []link
	Current map[string]json.RawMessage
}

type logService struct {
	ID      string
	Entries *link
}

type logEntry struct {
	ID       string
	Severity string
	Created  string
	Message  string
}

type logPage struct {
	Members []logEntry
	Next    string `json:"Members@odata.nextLink"`
}

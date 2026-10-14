package arista

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// The types below decode only what the collector uses, and decode it loosely:
// the JSON of a given command is not identical across EOS trains (see
// testdata/README.md), so numbers may arrive as strings, a map may be a list,
// and absent keys are normal. A value that cannot be understood is left unset
// rather than failing the whole command.

// num is a number that may be absent, null, a string ("N/A", "-inf", "12.5")
// or a real JSON number. OK is true only for a finite number.
type num struct {
	V  float64
	OK bool
}

// UnmarshalJSON never fails: an unreadable value is an absent value.
func (n *num) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if strings.HasPrefix(s, `"`) {
		var str string
		if json.Unmarshal(b, &str) != nil {
			return nil
		}
		s = strings.TrimSpace(str)
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return nil
	}
	n.V, n.OK = f, true
	return nil
}

// slots is the JSON of an inventory section: an object keyed by slot number
// ("1": {...}) or, on some trains, an array of the same records.
type slots[T any] map[string]T

// UnmarshalJSON accepts an object or an array (numbered from 1).
func (s *slots[T]) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '[' {
		var l []T
		if err := json.Unmarshal(b, &l); err != nil {
			return err
		}
		m := make(slots[T], len(l))
		for i, v := range l {
			m[strconv.Itoa(i+1)] = v
		}
		*s = m
		return nil
	}
	var m map[string]T
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	*s = m
	return nil
}

type showVersion struct {
	ModelName        string `json:"modelName"`
	SystemMacAddress string `json:"systemMacAddress"`
	SerialNumber     string `json:"serialNumber"`
	Version          string `json:"version"`
	MfgName          string `json:"mfgName"`
	MemTotal         num    `json:"memTotal"`
}

type showUptime struct {
	UpTime num `json:"upTime"`
}

type showHostname struct {
	Hostname string `json:"hostname"`
	FQDN     string `json:"fqdn"`
}

type invItem struct {
	Name      string `json:"name"`
	ModelName string `json:"modelName"`
	MfgName   string `json:"mfgName"`
	SerialNum string `json:"serialNum"`
}

type showInventory struct {
	SystemInformation invItem        `json:"systemInformation"`
	PowerSupplySlots  slots[invItem] `json:"powerSupplySlots"`
	XcvrSlots         slots[invItem] `json:"xcvrSlots"`
	FanTraySlots      slots[invItem] `json:"fanTraySlots"`
}

type ifaceRecord struct {
	Name             string          `json:"name"`
	Hardware         string          `json:"hardware"`
	PhysicalAddress  string          `json:"physicalAddress"`
	Description      string          `json:"description"`
	Bandwidth        num             `json:"bandwidth"`
	MTU              num             `json:"mtu"`
	Duplex           string          `json:"duplex"`
	InterfaceStatus  string          `json:"interfaceStatus"`
	LineProtocol     string          `json:"lineProtocolStatus"`
	InterfaceAddress json.RawMessage `json:"interfaceAddress"`
	InterfaceType    string          `json:"interfaceType"`
}

type showInterfaces struct {
	Interfaces map[string]ifaceRecord `json:"interfaces"`
}

type statusRecord struct {
	Description string `json:"description"`
	LinkStatus  string `json:"linkStatus"`
	Duplex      string `json:"duplex"`
	Bandwidth   num    `json:"bandwidth"`
	Type        string `json:"interfaceType"`
	VLAN        struct {
		Mode string `json:"interfaceMode"`
		ID   num    `json:"vlanId"`
	} `json:"vlanInformation"`
}

type showInterfacesStatus struct {
	InterfaceStatuses map[string]statusRecord `json:"interfaceStatuses"`
}

type ipMask struct {
	Address string `json:"address"`
	MaskLen num    `json:"maskLen"`
}

// addressBlock is the "interfaceAddress" of an interface: an object in
// "show ip interface", an array of objects in "show interfaces".
type addressBlock struct {
	Primary   ipMask            `json:"primaryIp"`
	Secondary map[string]ipMask `json:"secondaryIps"`
	Virtual   ipMask            `json:"virtualIp"`
}

type showIPInterface struct {
	Interfaces map[string]struct {
		InterfaceAddress json.RawMessage `json:"interfaceAddress"`
	} `json:"interfaces"`
}

type showVLAN struct {
	VLANs map[string]struct {
		Name       string                     `json:"name"`
		Status     string                     `json:"status"`
		Interfaces map[string]json.RawMessage `json:"interfaces"`
	} `json:"vlans"`
}

type showPortChannel struct {
	PortChannels map[string]struct {
		ActivePorts   map[string]json.RawMessage `json:"activePorts"`
		InactivePorts map[string]json.RawMessage `json:"inactivePorts"`
	} `json:"portChannels"`
}

type showMLAG struct {
	State       string `json:"state"`
	DomainID    string `json:"domainId"`
	PeerAddress string `json:"peerAddress"`
}

type lldpAddress struct {
	AddressType string `json:"addressType"`
	Address     string `json:"address"`
}

type showLLDPLocal struct {
	ChassisID           string        `json:"chassisId"`
	SystemName          string        `json:"systemName"`
	ManagementAddresses []lldpAddress `json:"managementAddresses"`
}

type lldpNeighbor struct {
	ChassisIDType string `json:"chassisIdType"`
	ChassisID     string `json:"chassisId"`
	SystemName    string `json:"systemName"`
	Port          struct {
		IDType      string `json:"interfaceIdType"`
		ID          string `json:"interfaceId"`
		IDV2        string `json:"interfaceId_v2"`
		Description string `json:"interfaceDescription"`
	} `json:"neighborInterfaceInfo"`
}

type showLLDPNeighbors struct {
	Neighbors map[string]struct {
		Info []lldpNeighbor `json:"lldpNeighborInfo"`
	} `json:"lldpNeighbors"`
}

type showMACTable struct {
	Unicast struct {
		Entries []struct {
			MAC       string `json:"macAddress"`
			VLAN      num    `json:"vlanId"`
			Interface string `json:"interface"`
			Type      string `json:"entryType"`
		} `json:"tableEntries"`
	} `json:"unicastTable"`
}

type tempSensor struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Current     num    `json:"currentTemperature"`
	Overheat    num    `json:"overheatThreshold"`
	Critical    num    `json:"criticalThreshold"`
	HwStatus    string `json:"hwStatus"`
	InAlert     bool   `json:"inAlertState"`
}

type showTemperature struct {
	SystemStatus string       `json:"systemStatus"`
	TempSensors  []tempSensor `json:"tempSensors"`
	PowerSupply  []struct {
		RelPos      string       `json:"relPos"`
		TempSensors []tempSensor `json:"tempSensors"`
	} `json:"powerSupplySlots"`
	Cards []struct {
		RelPos      string       `json:"relPos"`
		TempSensors []tempSensor `json:"tempSensors"`
	} `json:"cardSlots"`
}

type fanRecord struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Status      string `json:"status"`
	ActualSpeed num    `json:"actualSpeed"`
}

type coolingSlot struct {
	Label  string      `json:"label"`
	Status string      `json:"status"`
	Speed  num         `json:"speed"`
	Fans   []fanRecord `json:"fans"`
}

type showCooling struct {
	SystemStatus string        `json:"systemStatus"`
	FanTraySlots []coolingSlot `json:"fanTraySlots"`
	PowerSupply  []coolingSlot `json:"powerSupplySlots"`
}

type showPower struct {
	PowerSupplies map[string]struct {
		State       string `json:"state"`
		ModelName   string `json:"modelName"`
		Capacity    num    `json:"capacity"`
		OutputPower num    `json:"outputPower"`
		InputPower  num    `json:"inputPower"`
	} `json:"powerSupplies"`
}

type domRecord struct {
	VendorSN    string `json:"vendorSn"`
	VendorName  string `json:"vendorName"`
	MediaType   string `json:"mediaType"`
	Temperature num    `json:"temperature"`
	Voltage     num    `json:"voltage"`
	TxPower     num    `json:"txPower"`
	RxPower     num    `json:"rxPower"`
}

type showTransceiver struct {
	Interfaces map[string]domRecord `json:"interfaces"`
}

package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const sysBlock = "/sys/block/"

// notDisk matches block devices that are not drives: loop and ram devices,
// compressed RAM, device-mapper and md volumes, optical and floppy drives,
// network block devices.
var notDisk = regexp.MustCompile(`^(loop|ram|zram|dm-|md|sr|fd|nbd)[0-9]*$`)

// powerOnQuantum is the granularity of reported power-on hours. The counter
// moves every hour; reported exactly it would write one history row per drive
// per hour. Rounded down to whole days it moves once a day.
const powerOnQuantum = 24

// probeDisks lists the drives from /sys/block and asks smartctl, through the
// Runner, for health and wear. Without smartctl (or without root: smartctl
// opens the device) the drive is still listed from sysfs and the probe says
// the wear is not known, once.
func (c *collector) probeDisks() error {
	ents, err := c.fs.ReadDir(strings.TrimSuffix(sysBlock, "/"))
	if err != nil {
		if skippable(err) {
			return skipf("/sys/block not readable")
		}
		return err
	}
	virtual := c.inv.Host.Virt.Kind == VirtVM || c.inv.Host.Virt.Kind == VirtContainer
	var noSmart, denied, failed []string
	for _, e := range ents {
		name := e.Name()
		if notDisk.MatchString(name) {
			continue
		}
		dir := sysBlock + name + "/"
		d := Disk{Name: name}
		if sectors, ok := parseInt(readOptional(c.fs, dir+"size")); ok && sectors > 0 {
			d.CapacityBytes = sectors * 512 // sysfs counts 512-byte sectors whatever the drive's own size
		}
		d.Model = readOptional(c.fs, dir+"device/model")
		d.Serial = firstNonEmpty(readOptional(c.fs, dir+"device/serial"), readOptional(c.fs, dir+"serial"))
		d.Firmware = firstNonEmpty(readOptional(c.fs, dir+"device/firmware_rev"), readOptional(c.fs, dir+"device/rev"))
		d.WWN = wwnFromSysfs(firstNonEmpty(readOptional(c.fs, dir+"wwid"), readOptional(c.fs, dir+"device/wwid")))
		rot := readOptional(c.fs, dir+"queue/rotational")
		switch {
		case strings.HasPrefix(name, "nvme"):
			d.Media, d.Protocol = "nvme", "NVMe"
		case rot == "1":
			d.Media = "hdd"
		case rot == "0":
			d.Media = "ssd"
		default:
			d.Media = "unknown"
		}
		if !meaningful(d.Serial) {
			d.Serial = ""
		}

		// A virtual machine's disks are the hypervisor's business: SMART
		// would ask a virtual device, and the answer is meaningless.
		if !virtual {
			switch out, err := c.run.Run(c.ctx, "smartctl", "--json", "-a", "/dev/"+name); {
			case errors.Is(err, ErrNotFound):
				noSmart = append(noSmart, name)
			case errors.Is(err, ErrNotAllowed):
				// device name smartctl cannot be asked about (cciss!c0d0): sysfs data only.
			case len(out) == 0:
				failed = append(failed, fmt.Sprintf("%s: %v", name, err))
			default:
				sm, serr := applySmart(&d, out)
				switch {
				case errors.Is(serr, errSmartDenied):
					denied = append(denied, name)
				case serr != nil:
					failed = append(failed, fmt.Sprintf("%s: %v", name, serr))
				default:
					d.Smart = sm
				}
			}
		}
		c.inv.Disks = append(c.inv.Disks, d)
	}
	if len(noSmart) > 0 {
		c.inv.Skipped = append(c.inv.Skipped, "disks: smartctl not available, no wear or health data")
	}
	if len(denied) > 0 {
		c.inv.Skipped = append(c.inv.Skipped, fmt.Sprintf("disks: smartctl could not open %s (not root), no wear or health data", strings.Join(denied, ", ")))
	}
	if len(failed) > 0 {
		return fmt.Errorf("smartctl: %s", strings.Join(failed, "; "))
	}
	return nil
}

// wwnFromSysfs turns "naa.5001234012345678" or "eui.0012..." into the bare
// identifier. Other schemes (t10 vendor ids, uuid.) are not world-wide names
// and are ignored.
func wwnFromSysfs(s string) string {
	for _, p := range []string{"naa.", "eui."} {
		if v, ok := strings.CutPrefix(strings.ToLower(s), p); ok && meaningful(v) {
			return v
		}
	}
	return ""
}

var errSmartDenied = errors.New("smartctl could not open the device")

// smartJSON is the part of `smartctl --json -a` the agent reads. Field names
// are those of smartmontools 7.x.
type smartJSON struct {
	Smartctl struct {
		Messages []struct {
			String   string `json:"string"`
			Severity string `json:"severity"`
		} `json:"messages"`
	} `json:"smartctl"`
	ModelName       string `json:"model_name"`
	SerialNumber    string `json:"serial_number"`
	FirmwareVersion string `json:"firmware_version"`
	WWN             *struct {
		NAA uint64 `json:"naa"`
		OUI uint64 `json:"oui"`
		ID  uint64 `json:"id"`
	} `json:"wwn"`
	UserCapacity struct {
		Bytes int64 `json:"bytes"`
	} `json:"user_capacity"`
	SmartStatus *struct {
		Passed *bool `json:"passed"`
	} `json:"smart_status"`
	PowerOnTime *struct {
		Hours *int64 `json:"hours"`
	} `json:"power_on_time"`
	NVMe *struct {
		CriticalWarning *int64   `json:"critical_warning"`
		AvailableSpare  *float64 `json:"available_spare"`
		PercentageUsed  *float64 `json:"percentage_used"`
		PowerOnHours    *int64   `json:"power_on_hours"`
		MediaErrors     *int64   `json:"media_errors"`
	} `json:"nvme_smart_health_information_log"`
	ATA struct {
		Table []struct {
			ID    int `json:"id"`
			Value int `json:"value"`
			Raw   struct {
				Value int64 `json:"value"`
			} `json:"raw"`
		} `json:"table"`
	} `json:"ata_smart_attributes"`
}

// SSD wear attributes, in order of preference. Their normalised value counts
// down from 100: wear is 100 minus it.
var ssdLifeAttrs = []int{233, 177, 231}

// applySmart parses smartctl's JSON, completes the drive's identity with what
// it says (sysfs does not always have a serial), and returns its health.
// smartctl exits non-zero to flag findings, so the exit status is not looked
// at: the JSON is. A device smartctl could not open has messages and no model.
func applySmart(d *Disk, out []byte) (*Smart, error) {
	var j smartJSON
	if err := json.Unmarshal(out, &j); err != nil {
		return nil, fmt.Errorf("unreadable output: %w", err)
	}
	if j.ModelName == "" && j.NVMe == nil && len(j.ATA.Table) == 0 {
		for _, m := range j.Smartctl.Messages {
			if m.Severity == "error" {
				return nil, errSmartDenied
			}
		}
		return nil, fmt.Errorf("no data in output")
	}
	if d.Model == "" {
		d.Model = j.ModelName
	}
	if m := strings.TrimSpace(j.SerialNumber); d.Serial == "" && meaningful(m) {
		d.Serial = m
	}
	if d.Firmware == "" {
		d.Firmware = j.FirmwareVersion
	}
	if d.WWN == "" && j.WWN != nil && j.WWN.OUI != 0 {
		d.WWN = fmt.Sprintf("%x%06x%09x", j.WWN.NAA, j.WWN.OUI, j.WWN.ID)
	}
	if d.CapacityBytes == 0 {
		d.CapacityBytes = j.UserCapacity.Bytes
	}

	s := &Smart{}
	if j.PowerOnTime != nil && j.PowerOnTime.Hours != nil {
		s.PowerOnHours = quantise(*j.PowerOnTime.Hours)
	}
	if n := j.NVMe; n != nil {
		s.WearPct = clampPct(n.PercentageUsed)
		s.SparePct = clampPct(n.AvailableSpare)
		s.MediaErrors = n.MediaErrors
		if s.PowerOnHours == nil && n.PowerOnHours != nil {
			s.PowerOnHours = quantise(*n.PowerOnHours)
		}
	}
	type ataAttr struct {
		value int
		raw   int64
	}
	attr := map[int]ataAttr{}
	for _, a := range j.ATA.Table {
		attr[a.ID] = ataAttr{a.Value, a.Raw.Value}
	}
	if a, ok := attr[5]; ok {
		v := a.raw
		s.ReallocatedSectors = &v
	}
	if a, ok := attr[197]; ok {
		v := a.raw
		s.PendingSectors = &v
	}
	if s.PowerOnHours == nil {
		if a, ok := attr[9]; ok {
			s.PowerOnHours = quantise(a.raw)
		}
	}
	if s.WearPct == nil && d.Media != "hdd" {
		for _, id := range ssdLifeAttrs {
			if a, ok := attr[id]; ok {
				w := float64(100 - a.value)
				s.WearPct = clampPct(&w)
				break
			}
		}
	}

	if j.SmartStatus != nil && j.SmartStatus.Passed != nil {
		switch {
		case !*j.SmartStatus.Passed:
			s.Health = "critical"
		case positive(s.ReallocatedSectors) || positive(s.PendingSectors) || positive(s.MediaErrors) ||
			(j.NVMe != nil && j.NVMe.CriticalWarning != nil && *j.NVMe.CriticalWarning != 0):
			s.Health = "warning"
		default:
			s.Health = "ok"
		}
	}
	return s, nil
}

func positive(p *int64) bool { return p != nil && *p > 0 }

func quantise(h int64) *int64 {
	h = h / powerOnQuantum * powerOnQuantum
	return &h
}

func clampPct(p *float64) *float64 {
	if p == nil {
		return nil
	}
	v := *p
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	return &v
}

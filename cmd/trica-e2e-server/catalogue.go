package main

// The catalogue rack: r23 in lyon-1, a sample of the device-type catalogue
// (docs/reference/devicetypes.md, "Catalogue"): upstream photographs and faces
// the project drew, servers, switches, a firewall, storage and power, every
// one healthy so that a screenshot shows the faces and nothing else.
const (
	idRackC  = 10    // r23
	firstCat = 0x700 // its devices and models
)

// catalogueSample is r23 from the top: name, role, model slug, lowest U.
var catalogueSample = []struct {
	name, role, slug string
	u                float64
}{
	{"sw-c1", "switch", "cisco-c9300-48p", 42},
	{"sw-c2", "switch", "cisco-ws-c3850-48t-s", 41},
	{"sw-c3", "switch", "juniper-ex4300-48t", 40},
	{"sw-c4", "switch", "ubiquiti-unifi-switch-48-gen2", 39},
	{"sw-c5", "switch", "mikrotik-crs326-24s-plus-2q-plus-rm", 38},
	{"fw-c1", "firewall", "fortinet-fg-200f", 37},
	{"srv-c1", "server", "dell-poweredge-r630-10sff", 36},
	{"srv-c2", "server", "dell-poweredge-r640", 35},
	{"srv-c3", "server", "hpe-proliant-dl360-gen10-8sff", 34},
	{"srv-c4", "server", "dell-poweredge-r730xd-12lff", 32},
	{"srv-c5", "server", "dell-poweredge-r740xd-24sff", 30},
	{"srv-c6", "server", "hpe-proliant-dl380-gen10-24sff", 28},
	{"srv-c7", "server", "hpe-proliant-dl380p-gen8-25sff", 26},
	{"srv-c8", "server", "lenovo-thinksystem-sr650", 24},
	{"srv-c9", "server", "supermicro-sys-6029p-tr", 22},
	{"nas-c1", "nas", "synology-rs2421-plus", 20},
	{"nas-c2", "nas", "netapp-ds4246", 16},
	{"pdu-c1", "pdu", "apc-ap7920", 15},
	{"ups-c1", "ups", "eaton-5px3000irt2u", 3},
	{"ups-c2", "ups", "apc-smt3000rmi2u", 1},
}

func buildCatalogue(inv *inventory) {
	inv.add(object(idRackC, "rack", "r23", idSiteL, idSiteL, obj{"name": "r23", "u_height": 42}))
	n := firstCat
	for _, d := range catalogueSample {
		m := library.BySlug(d.slug)
		if m == nil || m.FrontImage == nil {
			panic("no catalogue model with a front face: " + d.slug)
		}
		n++
		model := n
		inv.add(object(model, "device_type", m.Model, 0, 0, obj{"slug": m.Slug, "vendor": m.Vendor, "model": m.Model, "u_height": m.UHeight, "full_depth": m.FullDepth}))
		n++
		inv.add(object(n, "device", d.name, idRackC, idSiteL, obj{"hostname": d.name, "role": d.role, "health": "ok"}))
		inv.mount(n, idRackC, d.u, "front", model)
	}
}

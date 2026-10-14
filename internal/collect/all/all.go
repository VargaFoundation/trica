// Package all imports every collector, so that their init functions register
// them in internal/collect. Import it for its side effect, in the one place
// that runs collectors (the in-server component and `trica collector`), and
// nowhere else: a collector added under internal/collect is listed here.
package all

import (
	_ "github.com/VargaFoundation/trica/internal/collect/arista"     // registers "arista"
	_ "github.com/VargaFoundation/trica/internal/collect/kubernetes" // registers "kubernetes"
	_ "github.com/VargaFoundation/trica/internal/collect/maas"       // registers "maas"
	_ "github.com/VargaFoundation/trica/internal/collect/mikrotik"   // registers "mikrotik"
	_ "github.com/VargaFoundation/trica/internal/collect/pfsense"    // registers "pfsense"
	_ "github.com/VargaFoundation/trica/internal/collect/redfish"    // registers "redfish"
)

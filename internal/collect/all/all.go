// Package all imports every collector, so that their init functions register
// them in internal/collect. Import it for its side effect, in the one place
// that runs collectors (the in-server component and `trica collector`), and
// nowhere else: a module added under internal/collect is listed here, and
// that one line is all it takes for the API and the Collectors pages to show
// it (ADR 0055; TestEveryModulePackageIsImported catches a forgotten line).
package all

import (
	_ "github.com/VargaFoundation/trica/internal/collect/arista"     // registers "arista"
	_ "github.com/VargaFoundation/trica/internal/collect/hostagent"  // registers "agent", the host agent, which has no factory
	_ "github.com/VargaFoundation/trica/internal/collect/kubernetes" // registers "kubernetes"
	_ "github.com/VargaFoundation/trica/internal/collect/maas"       // registers "maas"
	_ "github.com/VargaFoundation/trica/internal/collect/mikrotik"   // registers "mikrotik"
	_ "github.com/VargaFoundation/trica/internal/collect/pfsense"    // registers "pfsense"
	_ "github.com/VargaFoundation/trica/internal/collect/redfish"    // registers "redfish"
)

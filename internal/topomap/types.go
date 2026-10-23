// Package topomap answers the topology map (ADR 0051): the places of the
// inventory at four levels (global, site, room, rack) and the links between
// them, aggregated on the server so that the browser draws what it receives.
//
// It is a pure function of a graph snapshot, of the live measurements and of
// the caller's reading rights; it issues no SQL.
package topomap

import "time"

// Levels of the map.
const (
	LevelGlobal   = "global"
	LevelSite     = "site"
	LevelRoom     = "room"
	LevelRack     = "rack"
	LevelUnplaced = "unplaced"
)

// Node kinds.
const (
	KindSite     = "site"
	KindRoom     = "room"
	KindArea     = "area"
	KindRack     = "rack"
	KindDevice   = "device"
	KindInternet = "internet"
	KindUnplaced = "unplaced"
	KindPortal   = "portal"
)

// Edge media.
const (
	MediumPhysical = "physical"
	MediumTunnel   = "tunnel"
	MediumUplink   = "uplink"
)

// Ids of the pseudo places.
const (
	InternetID = "internet"
	UnplacedID = "unplaced"
)

// TopologyMap is what one level of the map shows.
type TopologyMap struct {
	// Level is the level served.
	Level string `json:"level"`
	// Focus is the place the level is about; absent at the global and
	// unplaced levels.
	Focus *MapPlace `json:"focus,omitempty"`
	// Trail are the places above the focus the caller may drill back to,
	// outermost first (a rack: its site, its room).
	Trail []MapPlace `json:"trail"`
	// InventoryAt is when the inventory snapshot the map is computed from
	// was built.
	InventoryAt time.Time `json:"inventory_at"`
	// Truncated is true when the node or edge cap cut the answer.
	Truncated bool `json:"truncated"`
	// Live says whether live traffic is configured; the values themselves
	// are in the edges.
	Live MapLive `json:"live"`
	// Nodes are the places of the level, including the portals to what is
	// outside it.
	Nodes []MapNode `json:"nodes"`
	// Edges are the links between the nodes, aggregated per pair and medium.
	Edges []MapEdge `json:"edges"`
	// Notes count what the map could not place or resolve.
	Notes MapNotes `json:"notes"`
}

// MapLive is the state of the live overlay the rates come from.
type MapLive struct {
	Enabled        bool       `json:"enabled"`
	GeneratedAt    *time.Time `json:"generated_at,omitempty"`
	Stale          bool       `json:"stale"`
	RefreshSeconds int        `json:"refresh_seconds"`
}

// MapNotes are the things the map could not decide.
type MapNotes struct {
	// AmbiguousPlacements counts devices mounted in several racks at once.
	AmbiguousPlacements int `json:"ambiguous_placements"`
	// LinksWithoutPlace counts links one of whose ends belongs to an owner
	// the map cannot place (a virtual machine, a Kubernetes node).
	LinksWithoutPlace int `json:"links_without_place"`
	// UnresolvedTunnels counts tunnels whose peer matches no known place.
	UnresolvedTunnels int `json:"unresolved_tunnels"`
	// UnplacedDevices counts devices in no site.
	UnplacedDevices int `json:"unplaced_devices"`
}

// MapPlace names a place of the inventory.
type MapPlace struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Display string `json:"display"`
	// WidthMM, DepthMM and TileMM are the floor plan of a room focus.
	WidthMM int `json:"width_mm,omitempty"`
	DepthMM int `json:"depth_mm,omitempty"`
	TileMM  int `json:"tile_mm,omitempty"`
}

// MapGroup is the grouping element a node sits on: the building of a room,
// the row of a rack.
type MapGroup struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Display string `json:"display"`
}

// MapDrill says which level a node opens.
type MapDrill struct {
	Level string `json:"level"`
	ID    string `json:"id,omitempty"`
}

// MapFloor is a rack's position on the floor plan, in tiles.
type MapFloor struct {
	X      int    `json:"x"`
	Y      int    `json:"y"`
	Facing string `json:"facing,omitempty"`
}

// MapSize is the physical size of a node.
type MapSize struct {
	WidthMM int `json:"width_mm,omitempty"`
	DepthMM int `json:"depth_mm,omitempty"`
	UHeight int `json:"u_height,omitempty"`
}

// MapMount is where a device sits in its rack.
type MapMount struct {
	Position  float64 `json:"position"`
	Face      string  `json:"face,omitempty"`
	UHeight   int     `json:"u_height,omitempty"`
	FullDepth bool    `json:"full_depth"`
}

// MapCounts are what a node holds, readable devices only.
type MapCounts struct {
	Rooms         int `json:"rooms"`
	Racks         int `json:"racks"`
	Devices       int `json:"devices"`
	InternalLinks int `json:"internal_links"`
}

// MapHealth tallies the health of the devices a node holds.
type MapHealth struct {
	// Worst is critical, warning, unknown or ok.
	Worst          string `json:"worst"`
	OK             int    `json:"ok"`
	Warning        int    `json:"warning"`
	Critical       int    `json:"critical"`
	Unknown        int    `json:"unknown"`
	CriticalEvents int    `json:"critical_events"`
}

// MapNode is one place drawn at a level.
type MapNode struct {
	// ID is the object id, or internet, unplaced, area:<siteId> and
	// portal:<placeId> for the places that are not one object.
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// PlaceType is the object type of a portal's place.
	PlaceType string    `json:"place_type,omitempty"`
	Display   string    `json:"display"`
	Group     *MapGroup `json:"group,omitempty"`
	Drill     *MapDrill `json:"drill,omitempty"`
	Floor     *MapFloor `json:"floor,omitempty"`
	Size      *MapSize  `json:"size,omitempty"`
	Mount     *MapMount `json:"mount,omitempty"`
	Role      string    `json:"role,omitempty"`
	Counts    MapCounts `json:"counts"`
	Health    MapHealth `json:"health"`
	PowerW    *float64  `json:"power_w,omitempty"`
}

// MapKinds counts the links of an edge by what they are.
type MapKinds struct {
	Cable      int `json:"cable"`
	CabledTo   int `json:"cabled_to"`
	NeighborOf int `json:"neighbor_of"`
	Tunnel     int `json:"tunnel"`
	Uplink     int `json:"uplink"`
}

// MapTunnel describes the tunnels an edge carries.
type MapTunnel struct {
	// Kind is the technology, or mixed.
	Kind string `json:"kind"`
	// Status is up, down, partial or unknown.
	Status string `json:"status"`
	// Peer is the peer as the tunnel states it, when it is unresolved.
	Peer string `json:"peer,omitempty"`
	// Resolved is false when the peer matched no place and the edge goes to
	// the Internet instead.
	Resolved bool `json:"resolved"`
}

// MapUplink describes the Internet uplinks an edge carries.
type MapUplink struct {
	// Status is online, offline, degraded or unknown.
	Status        string  `json:"status"`
	LatencyMS     float64 `json:"latency_ms,omitempty"`
	LossPct       float64 `json:"loss_pct,omitempty"`
	Provider      string  `json:"provider,omitempty"`
	BandwidthMbps float64 `json:"bandwidth_mbps,omitempty"`
}

// MapMember is one physical link behind an edge.
type MapMember struct {
	ADevice    MapPlace `json:"a_device"`
	APort      MapPlace `json:"a_port"`
	BDevice    MapPlace `json:"b_device"`
	BPort      MapPlace `json:"b_port"`
	Kind       string   `json:"kind"`
	Confidence string   `json:"confidence"`
}

// MapEdge is every link between two nodes of one medium.
type MapEdge struct {
	// ID is a~b~medium, with a < b.
	ID     string   `json:"id"`
	A      string   `json:"a"`
	B      string   `json:"b"`
	Medium string   `json:"medium"`
	Kinds  MapKinds `json:"kinds"`
	// Links is how many links the edge stands for.
	Links int `json:"links"`
	// Confidence is the weakest of the links: declared, observed or deduced.
	Confidence string `json:"confidence"`
	// CapacityMbps sums the links whose speed is known.
	CapacityMbps float64 `json:"capacity_mbps,omitempty"`
	// CapacityPartial is true when some link has no known speed.
	CapacityPartial bool     `json:"capacity_partial"`
	AToBBps         *float64 `json:"a_to_b_bps,omitempty"`
	BToABps         *float64 `json:"b_to_a_bps,omitempty"`
	// Util is the busier direction over the capacity, when both are known.
	Util *float64 `json:"util,omitempty"`
	// Oper is up, down, partial or unknown.
	Oper string `json:"oper"`
	// Measured is true when some link has live values.
	Measured bool       `json:"measured"`
	Tunnel   *MapTunnel `json:"tunnel,omitempty"`
	Uplink   *MapUplink `json:"uplink,omitempty"`
	// Members are the physical links, at most MaxMembers.
	Members          []MapMember `json:"members"`
	MembersTruncated bool        `json:"members_truncated"`
}

// Caps on the size of an answer.
const (
	MaxNodes   = 500
	MaxEdges   = 2000
	MaxMembers = 50
)

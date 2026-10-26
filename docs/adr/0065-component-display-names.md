# ADR-0065: Readable names for hardware components, and their parent in the objects list

- **Status**: accepted (2026-10).
- **Concerns**: `internal/schema` (display templates, `memory.technology`), `internal/reconcile`
  (`DisplayAndSearch`, the one-shot recompute), `internal/store/migrations/00008_display_rules.sql`,
  `internal/server` (start), `internal/api` (`parent` on list items), `internal/mcp` (name
  resolution), `internal/collect/redfish` (DIMM technology), `web` (objects list),
  `docs/reference/`. The schema change is additive; so is the API change.

## Context

`/objects?q=proc` listed 16 cpus all called `Proc 1` or `Proc 2`, 28 DIMMs called `PROC 1 DIMM 12`
or `DIMM A1`, disks called `1I:0:1`. The `display` of a type is one attribute, and for a component
that attribute is the slot: it tells two parts of one server apart and nothing else. The list's
third column showed the site, the same for every part of every server.

## Decision

1. **A type may declare `display_template`**, next to `display` (which stays the fallback and the
   meaning of "the display attribute" everywhere else: `display` still names the attribute the
   fallback reads). Placeholders are `{attr}` and `{attr|formatter}`. A placeholder whose attribute is
   missing or empty disappears with its separator (the next placeholder that renders takes the
   separator of the first one skipped; nothing is written before the first value). A template whose first
   placeholder (the name) has no value is not used: a disk known only by its serial keeps showing the serial, not `SSD`. A template that
   renders empty falls back to the display attribute, then to the first identifying attribute, as
   before. The schema load rejects a template with an unknown attribute or formatter, an unclosed
   brace or no placeholder. An overlay does not set a template on a built-in type (overlays merge
   attributes and parents only); a custom type declares its own.
2. **Formatters**: `upper`; `media` (upper case, nothing for `unknown`); `cpu` (drops `(R)`, `(TM)`,
   the word `CPU`, collapses spaces); `watts` (`800 W`); and two size formatters, because one rule
   cannot be right for both families: `bytes` is **decimal** (1000-based: a disk is sold that way,
   `500107837440` is `500 GB`, `1.9 TB`) and `ram` is **binary shown as GB** (1024-based: a DIMM is
   sold that way, `17179869184` is `16 GB`, `68719476736` is `64 GB`). The brief asked for one rule;
   a single one would give `465.7 GB` for a 500 GB disk or `17.2 GB` for a 16 GB DIMM. A number is
   shown with one decimal under 100 (a trailing `.0` is dropped) and none above.
3. **Templates**: `cpu` `{name} · {model|cpu}`, `memory` `{name} · {technology} {size_bytes|ram}`,
   `disk` `{name} · {media|media} {capacity_bytes|bytes} {model}`, `psu`
   `{name} · {capacity_w|watts} {model}`. The slot or name stays first: it tells two identical parts of
   one server apart. `transceiver` is left as it is: its name is the port it sits in.
   *Amended (#207)*: `module` gets `{name} · {model}` — a storage controller listed as the parent of a
   drive read `Slot 0`, which says nothing; it now reads `Slot 0 · Smart Array P440ar Controller`.
4. **`memory.technology`** (string, searchable: `DDR4`, `DDR5`, `LPDDR5`, `HBM2`). The Redfish
   collector fills it from `MemoryDeviceType` (iLO 5, iDRAC) or `DIMMType` (iLO 4), reduced to the
   generation (`DDR4_SDRAM` and `DDR4E_SDRAM` are `DDR4`); `DRAM`, `Other`, `Unknown` give nothing.
   MAAS and the Linux agent do not create DIMM objects, so they have nothing to fill.
5. **The stored display is recomputed once per change of rule, by the engine.** `display` is stored
   in `objects.display`, so objects already in a database keep their old name until an attribute of
   theirs changes. Migration `00008_display_rules` adds `display_rules (type, rule, applied_at)`: the
   rule each type's stored names follow, as a short hash of its display attribute and template (empty
   for a type without a template, which is what every object created before templates follows).
   `Engine.RecomputeDisplays`, called by the server at start, compares the schema with the table and,
   for each type that differs, rewrites `display` and `search_text` of its objects in batches of 500
   (one transaction each, paged by id), then records the rule in the last batch's transaction. A type
   without template and without a row is skipped, so a database that never had templates is not
   walked. A second start finds every rule recorded and does nothing. Only derived columns are
   rewritten, by `internal/reconcile`; no value, history or claim changes and nothing is deleted. A
   failure is logged and does not stop the server (the old names are cosmetic).
   *Why a table and not a hash in a settings table*: there is no settings table; a one-row-per-type
   table is the simplest reversible record (`DROP TABLE`), and it lets a change of one type's rule
   touch only that type.
6. **Readers of `display`.** The stored display of a component is no longer its name, so a reader
   that compared it to a name had to move:
   - MCP name resolution (`device/part`): the text search matches the indexed attributes, which still
     include the name; the tie-break between candidates (`DIMM A1` against `DIMM A10`) compared the
     display and now also compares the `name` attribute.
   - TQL `name:` reads the attribute and is unchanged; free text matches `search_text` or the display
     (a substring of either). `display:` compares the stored display, which for these four types is
     the rendered name (documented in the TQL reference). `parent:` matches the parent's display: a
     parent that is a component of these types (none of them parents anything today) would need its
     rendered name.
   - The graph cache, topology views and exports read ids and attributes, or the display only to
     label; the label is now the readable one.
7. **Objects list**: `GET /objects` and `/search` items carry `parent: {id, type, display}` when the
   object has a parent the caller may read (readable type; a parent the caller may not read is left
   out, as the relation ends are). One query for the page. The UI column is named "Parent / site":
   the parent as a link when there is one, else the site.

## Consequences

- A cpu reads `Proc 1 · Intel Xeon E5-2695 v4 @ 2.10GHz` in lists, search results and graph labels.
- Changing a built-in template in a later release recomputes that type's names at the next start.
- Searching by `display:"Proc 1"` (exact) no longer matches; `name:"Proc 1"` does.
- Not done: templates for custom types are possible but no overlay can set one on a built-in type;
  the parent column does not walk up past the direct parent (a disk in a controller shows the
  controller).

## Amendment (#207): the device above a component's parent

A drive sits in a controller (`module`), which sits in the server: the list's parent column showed
`Slot 0` for every drive. List and search items now also carry `device {id, type, display}` when the
parent is not a device: the nearest device above it, found by walking up the parents (at most four
levels, one query per level), and only through objects the caller may read — the walk stops at the
first one they may not, and nothing above it is named. The objects page shows
`srv-01 › Slot 0 · Smart Array P440ar Controller`.

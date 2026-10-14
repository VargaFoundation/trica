# Query language (TQL)

The search bar, the API and the MCP tools take the same queries. Decision: ADR 0012.

```
sw-a1
192.0.2.13
aa-bb-cc-00-00-01
type:device vendor:arista os_version<4.20
type:disk wear_pct>70 rel.mounted_in:"rack 2"
type:device role:server sort:-power_w limit:10
(vendor:arista OR vendor:mikrotik) -has:serial
type:device mgmt_ip<<192.0.2.0/24
```

## Terms

| Form | Meaning |
|---|---|
| `word`, `"two words"` | Free text: name, address, MAC (any notation), serial, id |
| `field op value` | A condition on a field |
| `a b`, `a AND b` | Both |
| `a OR b` | Either; AND binds tighter: `a OR b c` is `a OR (b AND c)` |
| `NOT a`, `-a` | Not |
| `( … )` | Grouping |
| `value1,value2` | Any of the values |
| `"…"` | Quotes a value with spaces or commas; `\"` and `\\` escape |

A MAC, an IPv6 address or a URL is free text even though it contains `:`.

## Operators

| Op | Meaning |
|---|---|
| `:` or `=` | Equals, case-insensitive; `*` is a wildcard; matches one element of a list |
| `!=` | Differs, or the field is missing |
| `<` `<=` `>` `>=` | Numbers as numbers, versions as versions (`4.9.1 < 4.20.7M`), else text |
| `~` | Contains |
| `<<` | Address or prefix within a prefix |

## Fields

| Field | Values |
|---|---|
| `type` | Object type: `type:device,vm` |
| `status` | `active`, `absent`. Absent objects are hidden unless the query names `status` |
| `id` | Object id |
| `display` | Display name |
| `first_seen`, `last_seen`, `created_at` | `2026-03-03`, RFC 3339, or relative: `-7d`, `-24h` |
| `parent` | Id or display name of the parent: `type:disk parent:srv1` |
| `has` | The object has the attribute: `has:serial` |
| `source` | A source currently claims something on the object: `source:arista:*` |
| `rel.<type>` | The object, or one of its ancestors, has a current relation of that type with an object named or identified by the value; `*` is any: `rel.mounted_in:"rack 2"` |
| anything else | An attribute of the schema: `vendor`, `os_version`, `wear_pct`, `mgmt_ip`… |

## Directives

Only at the top level, not inside parentheses or after NOT.

| Directive | Meaning |
|---|---|
| `sort:f1,-f2` | Sort by fields, `-` for descending; any column or attribute |
| `limit:n` | Caps the whole result (1 to 1000): `sort:-power_w limit:10` is a top 10 |

Without `sort:`, free-text results come by relevance: exact display name first, then
similarity.

## Typos

Free text first matches exactly. When nothing matches, the search is retried tolerating typos
and the result says `"fuzzy": true`: `swa-1` finds `sw-a1`.

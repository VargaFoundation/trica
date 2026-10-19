# ADR-0045: Measurements over time, read from the attribute history

- **Status**: accepted (2026-10)
- **Concerns**: `internal/schema` (the `series` flag), `internal/store` (`HistoryBetween`),
  `internal/api` (`GET /objects/{id}/series`), `web/src/charts`. Amends ADR 0041 (which said trica
  keeps no history of measurements).

## Context

Operators asked for curves on an object's page: power draw, temperatures, fan speeds, optical
levels. ADR 0041 said "there is no history of measurements in trica, by design: the metrics store
keeps that". That is true of throughput, which trica never stores. It is not true of what the
collectors themselves report: Redfish, the switches and the gateways write power, temperatures,
fan speeds, disk wear and optical levels as attributes, and every attribute has a history
(`attr_history`, one row per distinct value with its validity range). The values are there; only a
way to read them as a curve is missing.

Two limits shape the reading:
- A collector runs every five minutes, so a month is about 8 600 rows for one attribute of one
  object, more than `GET /objects/{id}/history` reads in one answer (5 000) and far more than a
  curve can draw.
- A row holds until the value changes. A steady temperature is one row for hours; a fluctuating
  power draw is one row per run. Averaging rows would give a steady hour the weight of a five-minute
  blip.

## Decision

**A type declares which numeric attributes are measurements (`series: true`), and
`GET /objects/{id}/series` folds their history into buckets on the server.**

- `series` is allowed on an `int` or a `float` only; the schema load refuses it elsewhere. The
  built-in types mark power, temperatures, fan speeds, disk wear and spare, optical levels and
  voltage, sensor readings, outlet power and gateway latency and loss. `GET /schema` carries the
  flag, so the UI and a custom type follow the same rule.
- The endpoint takes `key`, `from`, `to` (the last 24 hours by default, at most 366 days) and
  `points` (120 by default, at most 1000). It reads the rows whose validity overlaps the range (the
  exclusion constraint's GiST index serves `&&`), at most 50 000, and cuts the range into buckets of
  whole seconds. Each bucket gets the average **weighted by how long each value held in it**, and
  the lowest and highest values. A value still current holds until now. A bucket where the attribute
  had no value is left out, so a gap in the points is a gap in the readings, not a line drawn across
  it. A non-numeric value is skipped. `truncated: true` says the range held more rows than one
  answer reads and its end is missing.
- An attribute the type does not declare as a series is a 400 `not-a-series`; a sensitive one a
  403, like every other read.
- The object page draws one curve per series attribute the object holds as a number, over 24 hours,
  7 days or 30 days (in the address), as a line (the average) on a band (lowest to highest). The
  drawing is SVG in real pixels, its width following the card: the CSP forbids inline styles, so
  nothing is laid over it in percent. The pointer and the arrow keys read one bucket out in a live
  region; a sentence gives the last, lowest and highest values.

## Consequences

- No new storage and no new collection: a curve is only as fine as the collection period (five
  minutes by default) and only as old as the history. A value that stays the same is one row,
  whatever the period, which is what makes a year of a steady sensor cheap.
- A reading is a change of value, not a sample: two equal readings an hour apart are one row valid
  for the hour. A collector that stops reporting leaves the last value open until something closes
  it (the object going absent, another source), and the curve stays flat over that time; it is not
  a gap.
- The cost is the rows of one attribute of one object over the range: about 8 600 for 30 days at
  five minutes, one index range scan, folded in Go. The bound of 50 000 rows covers a year of a
  value that changes every ten minutes.
- Throughput, errors and anything a metrics store keeps stay there (ADR 0041): trica stores what
  collectors report as the state of an object, not counters.
- No curve aggregates components yet: a server's page draws its own power and temperatures, not
  its PSUs' or disks'; those are drawn on each component's page.

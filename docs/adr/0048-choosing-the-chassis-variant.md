# ADR-0048: When the library is not sure of the chassis, a person chooses it on the device

- **Status**: accepted (2026-10)
- **Concerns**: `internal/devicetypes` (`Index.Choices`, `variant_of`, `Sync`), `internal/store`
  (`RelationClaims`), `internal/api` (`GET /objects/{id}/device-type-candidates`, rack contents,
  faces), `web/src/devicetypes`, `cmd/trica-e2e-server` (rack r22). Builds on ADR 0004 (claims and
  source priority), ADR 0020 (library), ADR 0039 (the UI declares as batches), ADR 0043 (chassis
  variants by part number), and settles the point ADR 0020 and ADR 0043 left open: picking a model
  by hand.

## Context

ADR 0043 made the part number pick the chassis. Where nothing names it the match falls back to
the name, and a name often maps to several chassis:

- Dell reports no chassis part number at all (the Redfish `SKU` is the service tag), so a device
  reporting `PowerEdge R330` gets the upstream generic model, whatever its front looks like
  (8 x 2.5-inch, 4 x 3.5-inch hot-plug, 4 x 3.5-inch cabled).
- HPE's `ProLiant DL380 Gen9` with a part number the library does not list gets the generic
  model; `ProLiant DL180 Gen9` has no generic model, so it gets nothing.

The rack then shows a generic face or the generic model's, and nothing says the match is a guess.
The owner asked: "if you are not certain, offer the variants directly on the object". Four
questions follow: how to know the candidates, how sure a match is, how a person's choice is
written, and what wins when the library or a collector later says something else.

## Decision

> Amended by [ADR 0056](0056-growing-the-device-type-library.md): in §2, a name match is not
> certain because a longer-named sibling exists, only because another model's `variant_of` names
> the matched model.

1. **Candidates are the family the library can name.** `Index.Choices(vendor, model, part
   number)` returns Find's match, whether it is certain, and the candidate models: the models of
   the same vendor whose name starts, word for word, with the name the device reports
   (`PowerEdge R330` → `PowerEdge R330 8 x 2.5in hot-plug`…; `DCS-7050SX-64` → its `-F`/`-R`
   models), the match itself, and the models a new optional key **`variant_of: <slug>`** links to
   any of them (a trica extension, for a variant whose name does not extend the generic one; no
   embedded file needs it today). Word boundaries keep `R330` from pulling in an `R3300`; a family
   larger than 12 is no family (a bare `PowerEdge` says too little).
2. **Certainty is a rule, said with the answer.** Certain: a part number listed by one model
   (`sku`); a name, a mounting suffix or an upstream part number when the matched model has no
   other candidate or is itself a variant (`variant_of`). Not certain: the name matched a model
   that has variants (`base`), an airflow family's synthesised base (`airflow`), nothing matched
   but models fit (`ambiguous`), nothing at all (`none`). A declaration by another source is
   certain (`declared`).
3. **The choice is a declared `instance_of`, sent as every UI declaration is.** The page builds a
   partial batch (`chooseModel` in `web/src/edit/batch.ts`): the model by its `slug` (strong
   identity: the engine finds the existing `device_type`, or creates it bare), the device by id,
   `instance_of` between them; it previews it on `POST /batches/preview` and submits it to
   `POST /batches`, attributed by the server to `user:<subject>` at `declared` confidence, queued
   as a proposal when the policy says so. Changing one's mind withdraws one's own earlier choice
   (an `unrelation`); a colleague's declaration is never withdrawn by the page (ADR 0004).
   Choosing needs `propose` on devices in the device's site **and** on device models; the API
   checks it (403), and `can_choose` tells the page beforehand.
4. **A declaration wins over the name match, and survives.** The library's `Sync` reads the
   `instance_of` claims of every other source (`store.RelationClaims`) and, for a device that has
   one, claims no `instance_of` of its own: being full-scope, its next pass withdraws the
   name match. It still describes the declared model's `device_type` (height, depth, ports,
   images), so a model a person chose gets its facts on the next pass. Collectors do not claim
   `instance_of`, so a later collection changes nothing. Until the library's next pass both
   relations exist: every read that picks a device's model (rack contents, faces, the
   candidates) prefers a relation resolved to a source other than the library, the most recent
   first; the rack and the faces take the library's height and name for a chosen model the
   inventory has not described yet.
5. **A part number that disagrees is said, not applied.** When the library's match is by SKU and
   names another chassis than the one declared, the declaration stands and the answer carries
   `sku_conflict`; the page says so and offers to choose that chassis in one click. Not a
   `discrepancy` row: only the engine opens those, from an attribute's declared and observed
   claims, and the library speaks only through batches (ADR 0005). Reconsidered if operators ask
   for it on the discrepancy board.
6. **On the page.** The device's Overview gets a **Device model** card: when not certain, a
   callout "Variant not certain — choose the chassis" with the reason, the candidates' fronts side
   by side (radio buttons, model name, U, depth), the chosen one's front and rear larger, then the
   usual preview and submit; when certain, the model and the rule that decided it (SKU, name,
   mounting suffix, "Declared by X"), with **Change the model** for an administrator and for the
   person whose declaration decides. The header carries a "Variant not certain" pill. On the rack,
   the device's tooltip says "variant not certain" and the mounted table links to the choice. The
   pictures go through the rack's loader (fetch with the token, `ImageBitmap`, canvas): no `img`
   source, no `blob:` or `data:` URL, the CSP is unchanged.

## Consequences

- A Dell R330 gets its real front once someone who stands in front of it says which it is; the
  library never guesses one.
- The rule "a declaration wins over the name" holds for any source that declares `instance_of`
  (a declaration file, an operator's import), not only the UI.
- Rack contents read the `instance_of` claims of the rack's devices: one more query per rack,
  whatever the number of devices. With no library the API offers no candidate (`none`).
- Rejected: letting a later SKU match override a declaration silently (a person's word would
  disappear without a trace), choosing from the number of drives a collector sees (ADR 0043),
  and a "model" attribute on the device the UI would set (the model is a relation to a
  `device_type`, ADR 0020).
- Not done here: the `sku_conflict` on the discrepancy board, bulk choice for many devices of the
  same model, and `variant_of` entries in the embedded files (none is needed by the current
  names).

## Addendum (2026-10): a variant of the part number's chassis is no conflict

A model may be a `variant_of` another chassis variant rather than of the generic model: the
DL380 Gen10 8SFF with the 8 NVMe cage in Box 3 is a variant of the 8SFF CTO, whose part number
the server keeps reporting (ADR 0047, addendum). Declaring it refines what the part number says
instead of contradicting it, so decision 5 does not apply: `sku_conflict` is set only when the
declared model is neither the part number's model nor a `variant_of` it.

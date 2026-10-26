# ADR-0067: Renaming a site, a room or a rack

- **Status**: accepted (2026-10).
- **Concerns**: `internal/reconcile` (`checkNames`, built-in priority of place names),
  `internal/api` (409 `name-taken`), `web` (the Rename action and dialog), `docs/reference/`.
  No API schema change, no SDK change.

## Context

An operator must be able to rename a site, a room or a rack. Renaming already worked mechanically:
the edit form designates the object by `id` (ADR 0006) and claims a new `name`, so the object, its
parents, children and relations stay and `attr_history` keeps the old name. Three things were
missing. Nothing stopped two rooms of one site, or two racks of one room, from ending up with the
same name. The precedence against the declaration import (`trica import --name pereire`, source
`import:pereire`) depended on the clock of the browser: the most recent claim won, so a re-import
could take the name back, or not, by chance. And nothing told the operator that a file also names
the object.

## Decision

1. **A rename is a batch that designates the object by id and sets `name`**, from the person's
   source (`user:<subject>`, declared). The same batch path serves the UI, the API and the MCP
   tools, with the permission of any other edit (`declare`). History, children and relations are
   untouched. The site `code` (strong identity) is not changed by a rename; it is edited, if at
   all, as its own attribute.
2. **A person's name outranks other sources** for `name` of `site`, `building`, `room`, `row`,
   `aisle` and `rack`, through a built-in priority rule (`user:*`), whatever the clocks say.
   A rule configured in `policy.priorities` for the same key wins over the built-in one. When the
   person withdraws the claim, the name goes back to the other sources.
3. **No duplicate on re-import.** The import's claim on the old name is kept (a claim is closed only
   when its source stops saying it), so the next import still matches the renamed object by that
   weak identity: it creates nothing and does not take the name back. The declaration file keeps
   saying the old name until someone edits it; the UI says so.
4. **A rename to a name a sibling already carries is refused**: same type, same parent, compared
   after normalisation (case and spaces), against the claims of any source, stale ones included.
   The error is `ErrNameTaken` (an `ErrConflict`), HTTP 409 with the problem type
   `urn:trica:problem:name-taken`; preview and apply both refuse, nothing is changed. The check only
   covers place types (`layer: location`); devices may share a name (ADR 0006).
5. **The dialog** (a "Rename" button on the site, room and rack pages) says which other sources
   give the current name and that the person's name takes precedence, and shows the 409 in words.

## Consequences

- The next import with the old name neither duplicates nor overwrites. An import with a name that
  matches no claim of any object still creates a new object (unchanged behaviour): to rename in
  the file, rename in the UI first, or edit the file to the new name (it then matches the UI claim).
- A rename made in the UI is not written back to the declaration file; the file is the operator's.
- The built-in rule applies to people only: a collector that names a place does not override an
  operator.

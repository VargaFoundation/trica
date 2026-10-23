# ADR-0055: Collector modules describe themselves; the API and the UI list them with their targets and health

- **Status**: accepted (2026-10)
- **Concerns**: `internal/collect` (`Descriptor`, `Register`, `Modules`, `Lookup`), every collector
  package (`arista`, `kubernetes`, `maas`, `mikrotik`, `pfsense`, `redfish`),
  `internal/collect/hostagent` (new), `internal/collect/collecttest` (`CheckCollects`),
  `internal/collect/all`, `internal/api` (four `/collectors` endpoints, `SourceHealth.module`),
  `internal/wiring` (the targets file is loaded once and shared), `web/src/collectors` (new),
  `cmd/trica-e2e-server`. Builds on ADR 0005 (single writer), ADR 0018 (the web UI), ADR 0026
  (collector runner) and ADR 0040 (the design system). `pkg/sdk` is untouched.

## Context

An operator asked to see the collection agents in the interface (the Arista one, the MAAS one, the
Ubiquiti one...) and said the solution must be modular: the next collector must not mean a new
page.

Until now a collector registered `kind -> Factory` and nothing else, so nothing could say what a
module is, what it collects, how it is configured or what it was tested with. The health board is
keyed by source with no notion of a module, and the targets file is read only by the collection
component. A UniFi collector comes right after this story.

"Agent" already means the host agent (`trica-agent`) in trica, so the page is called Collectors
(French: Collecteurs); the host agent is listed in it as the module `agent`.

## Decision

1. **A module registers a descriptor together with its factory**, in one call:
   `collect.Register(Descriptor, New)`. `Descriptor.Validate` refuses an incomplete descriptor at
   `init`, so a module that does not describe itself does not start.
2. **The descriptor says**: `Kind` (the source prefix), `Title`, `Vendors`, `Protocol`, `Summary`
   (one sentence, English and French), `Deployment` (`target`: a line of the targets file, run by
   the server or by `trica collector`; `agent`: installed on the host it inventories, with no
   factory), `Collects` (object types), `ReadOnly` (how read-only is enforced), `Address`,
   `Username` and `Secret` (`required`, `optional` or `unused`, and what to put there), `Options`,
   `Tested`, `Evidence` (`synthesised`, `recorded` or `field`, as the package's `testdata/README.md`
   says) and `Docs` (its README).
3. **Options are reflected from the struct the collector decodes.** `Descriptor.Options` is the zero
   value of that struct; the `yaml` tag is the key, and `doc`, `default` and `enum` tags give the
   rest. A field without a `doc` tag, a `yaml` tag or a known type is an error. The documented
   options therefore cannot drift from what `DecodeOptions` accepts.
4. **Declared types are checked against every fake run.** `collecttest.Run` fails for an object
   type the descriptor does not list in `Collects`, so what a module says it collects covers what
   its fakes emit. (The converse, that a declared type is ever emitted, is not checked.)
5. **The host agent is a module** with `deployment: agent` and no factory, registered by
   `internal/collect/hostagent`, which does not import `internal/agent` (the server binary does
   not link the agent). A target naming it is refused by `collect.New`.
6. **The view is computed at request time** from the registry, the targets file and the
   `source_health` table. Nothing is stored, there is no migration and only `internal/reconcile`
   writes, as before. Every replica loads the targets file for the API (it already does for the
   collection component), once.
7. **Instances are keyed by the source prefix**, which comes from the credential, not by the
   `collector` or `target` fields a collector declares for itself (the health row of
   `arista:sw-a1` may say target `192.0.2.10`). An instance's origin is `server` (in this server's
   targets file) or `reported` (seen only through its health: a remote collector, the host agent).
   Status is `ok`, `failing`, `pending` (configured, never ran) or `stale` (configured, last attempt
   older than three intervals plus a timeout); pending and stale apply to configured targets only.
   A health row whose prefix no module registers (a collector built with `pkg/sdk`) is listed apart,
   as `unregistered`.
8. **Secrets**: only `collect.Secret.String()` is shown, which is the reference or `[redacted]` for
   a value written without a prefix. The username and the secret reference of a target are returned
   to a caller holding `admin` only; others get `credentials_hidden: true`. The address is cleaned
   of URL credentials (`scrubError`); an option of the `secret_reference` kind or named like a
   secret (`api.Sensitive`) never shows a value.
9. **One generic UI** (`web/src/collectors`): a list, a module page and an instance page, with no
   per-module code, and a test that fails when the code names a module. Closed vocabularies
   (status, origin, deployment, evidence, credential use, option type) are translated; a value the
   UI does not know is shown as it is. The summary is bilingual; the other descriptor texts are in
   English, like the schema's descriptions.
10. **Adding a module** takes a package with a descriptor and an `init()` (`Register(Descriptor,
    New)`), a README, a fake with its `testdata/README.md`, and one import line in
    `internal/collect/all/all.go`. The pages and the endpoints need no change. The tests that guard
    this are `TestRegisterRefusesAnIncompleteDescriptor`, `TestOptionFields`,
    `TestEveryModuleIsDescribed`, `TestEveryModulePackageIsImported` (a package that registers a
    module without being imported by `all` fails), `TestRunRejectsUndeclaredTypes`, the wiring test
    that registers a module no API code knows and finds it in the running server, and the web test
    `modular.test.ts`.

## Alternatives rejected

- **Descriptors in `pkg/sdk`**: it would change the public contract for a need of the server's own
  modules; a collector built outside the repository already shows up as `unregistered`.
- **A database table of modules and targets**: a second copy of the targets file, to keep in sync.
- **A component or a plugin per module in the UI**: contradicts "no UI change for a new collector".
- **YAML descriptor files**: they would drift from the `Options` struct.
- **Serving the READMEs from the server**: the UI has no Markdown renderer. The module page links
  to the README on GitHub (the tag of the build, else `main`); possible later.

## Consequences

- The `collect.Register` signature changed (internal API; a collector outside the repository uses
  `pkg/sdk` and is not affected). The UniFi story must use `Register(Descriptor, New)` and rebase on
  this change.
- A remote `trica collector` keeps its targets on its own host: only its health is visible to the
  server. Showing its configuration would mean reporting a redacted description over the transport;
  a follow-up.
- A reported instance has no staleness (no interval is known for it).
- `docs_url` points to GitHub: an air-gapped installation sees the path but cannot follow it, and a
  build without a release tag links `main`.
- Descriptor texts other than the summary are in English.
- Readers see the collectors' addresses (they already see the management addresses in the
  inventory) and what each option is set to, but not the credentials.

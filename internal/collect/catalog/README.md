# catalog: the applications you declare

Discovery finds what runs; it cannot know who owns an application, how critical it is, whether it
is being retired, what it depends on, or that the identity provider is a SaaS. The catalog is where
people write that down, in YAML, next to the code, reviewed like code (ADR 0071). Its values are
**declared**: they rank below what a collector observes, and a declared value that an observed one
contradicts opens a `value_mismatch` discrepancy.

## Address

| Address | Read |
|---|---|
| `/etc/trica/catalog/applications.yaml` (or `file:/…`) | That file. |
| `/etc/trica/collect/configmaps/catalog` | Every `*.yaml` and `*.yml` file directly in the directory, in name order, merged into one catalog. Names starting with a dot are skipped, so a mounted ConfigMap (one file per key, edits visible without a restart) works as is. |
| `https://git.example.org/org/catalog/raw/main/applications.yaml` | One file, with one `GET` per run. `password` (a `file:` or `env:` reference), when set, is sent as `Authorization: Bearer <token>`: use a read-only token restricted to the catalog repository. Redirects are not followed (the token must not travel to another host). A query parameter named like a credential (`private_token`…) is refused at start: it would sit in the targets file and the logs. |

At most 4 MiB per run. `interval` as for any target (5 minutes by default).

## Format

```yaml
format: trica-catalog      # required, exactly this
version: 1                 # required, exactly this

teams:                     # owners; identified by name
  - {name: payments, email: payments@example.org, chat: "#payments"}
people:                    # owners; identified by e-mail (lowered)
  - {email: jane@example.org, name: Jane Doe}
environments:              # what an environment is for (kind: prod, staging, dev, test, build, other)
  - {name: prod, kind: prod}

applications:
  - name: billing          # required, a slug: the name discovery gives it (see below)
    display_name: Billing
    description: Invoices and payments.
    domain: finance
    criticality: high      # low, medium, high, critical
    lifecycle: production  # planned, development, production, deprecated, retired
    hosting: self          # self, saas
    vendor: ""             # who runs or publishes it (a SaaS: required in practice)
    repository: https://git.example.org/finance/billing
    owner: payments        # a team name or a person's e-mail declared above
    depends_on: [entra-id] # applications whose failure takes this one down
    calls: [notifier]      # applications whose failure degrades this one
    components:
      - {name: api, kind: service, technology: go}   # kind: service, worker, database, cache, queue, frontend, batch, other
      - {name: db, kind: database, technology: postgresql}
    instances:             # only what no collector discovers: not the Kubernetes ones
      - component: db
        environment: prod
        version: "16.4"
        location: db-01    # default: the name of the first runs_on
        runs_on:
          - {type: vm, name: db-01}            # vm: name or uuid
          - {type: device, serial: CZJ0000001} # device: name or serial
        endpoints: [db-01.example.org:5432, https://billing.example.org]
```

Unknown keys, a value outside an enumeration, a name that is not a slug, an owner nobody declared,
a component an instance names but the application does not list, a SaaS with instances, two
instances with the same key, an endpoint with credentials: each is an error, all are reported at
once, and **the run fails without applying anything**. An empty catalog fails too unless
`options.allow_empty` is set: applied, it would withdraw every application.

## Checking a catalog before it ships

```bash
trica catalog check applications.yaml       # or a directory
```

runs the same parsing and validation as the collector, prints every problem and exits non-zero on
any, with no database and no network: put it in the pipeline of the repository that holds the
catalog, so that a merge request that would fail the run in production is refused instead.

## Mapping

| Catalog | Object or relation |
|---|---|
| `teams`, `people` | `team` (name), `person` (e-mail) |
| `environments` | `environment` (name) with `kind` |
| `applications[]` | `application` (name) with its attributes; `owner` is also kept as the attribute `owner` |
| `owner` | `owned_by` to the team or the person |
| `vendor` | `vendor` (name), `supplied_by` from the application |
| `depends_on`, `calls` | `depends_on` (impact: hard), `calls` (impact: degrade) to the application. One of this catalog is related directly; another is found in the inventory by name, else the engine records an `unresolved_reference` discrepancy until something declares or discovers it |
| `components[]` | `component` (name) in the application |
| `instances[]` | `component_instance` in the component, key `application/component/environment/location` (the location slugged), `deployed_in` the environment |
| `runs_on` | `runs_on` to the `vm` or `device` found by the identities given. A name is a weak identity: it finds an object that has no parent (a server, a VM the agent reports); give a serial or a uuid otherwise. Not found: `unresolved_reference` |
| `endpoints` | `endpoint` (url; protocol from the scheme, `tcp` for host:port), `exposed_by` from the instance |

## Meeting discovery

The catalog does not invent its own names: an application, a component and an environment are
identified as the kubernetes collector identifies them (ADR 0069), so that a declared `billing` and
a discovered `billing` are **one object** carrying both sources. The name discovery gives is, in
order: the `trica.io/application` annotation or label of the Argo CD Application, `platform` for a
platform project, the `app.kubernetes.io/part-of` label, the project `tenant-<x>` (gives `x`), any
other project but `default`, the Application name without its environment suffix. Search for
`type:application` to see the names in place before writing the catalog.

Useful searches once it runs:

| Search | Lists |
|---|---|
| `type:application -source:catalog:*` | applications discovered but not declared: nobody owns them in the catalog |
| `type:application -rel.owned_by:*` | applications without an owner |
| `type:application lifecycle:deprecated` | applications to retire, with their instances under them |
| `type:application criticality:critical` | the critical ones |

## Absence

The batch is full-scope with the source `catalog:<target name>`: an application, a dependency or an
owner taken out of the catalog is withdrawn (history keeps it). An application only the catalog
declared then becomes absent; one a collector still sees stays, with what the collector says.
Renaming the target changes the source: the old one's claims are not withdrawn by the new one.

# ADR-0035: A class enrolment token mints each name under a prefix once

- **Status**: accepted (2026-10)
- **Concerns**: `internal/pki` (`ValidPrefix`, `UnderPrefix`), `internal/transport`
  (`Registry.NewClassEnrolmentToken`, `RevokeEnrolmentToken`, `TokenID`, `handleEnrol`),
  `internal/transport/wire` (`EnrolRequest.Binding`), `internal/transport/uplink`
  (`ClientConfig.Binding`), `cmd/trica-agent`, `cmd/trica` (`enrol create --prefix`,
  `enrol revoke`), `deploy/kubernetes/trica-agent-daemonset.yaml`. Closes issue #63; completes
  ADR 0008.

## Context

ADR 0008 enrols an agent with a one-time token bound to one binding (`agent:host-01`). A
DaemonSet has one pod template for every node: it can mount one Secret, the same everywhere,
and it cannot know in advance which hostname each node has. The DaemonSet manifest asked for a
token file placed on each node when it is provisioned: manual, one token per node, and no
command to mint them in bulk. A wildcard binding (`agent:*`) is refused by `pki.ValidBinding`
on purpose: the binding is what stops an agent from reporting as another.

Two ways out were considered:

1. **Kubernetes TokenReview**: the pod presents its service account token, the server asks the
   API server who it is and on which node it runs, and derives the binding. It proves the node,
   but it ties the server to one Kubernetes cluster's API (and to reaching it), and it does
   nothing for the hosts enrolled by Ansible or cloud-init, which are the other half of the
   fleet.
2. **A class token**: a token bound to a prefix, not consumed, that mints one binding per name
   under the prefix, and only a name nobody holds.

## Decision

The class token (option 2).

1. `trica enrol create --prefix agent --ttl 24h` stores a token whose `binding` column is a
   prefix (`pki.ValidPrefix`: the kind and optional further segments) and whose `class` column
   is true (migration `00006_class_enrol_tokens.sql`). It is shown once; its id (`TokenID`, the
   first 8 bytes of its SHA-256 in hex) is printed on stderr and is what logs carry.
2. The client names the binding it wants (`EnrolRequest.Binding`); the agent asks for its
   source, `agent:<hostname>`, read the way a collection reads it. With a class token the
   server accepts it only if it is **exactly one segment** under the prefix
   (`pki.UnderPrefix`): `agent:host-01:docker` under `agent` would speak for host-01's sources.
   Otherwise 403.
3. **One name, one holder.** In the enrolment's transaction the server takes an advisory lock
   on the binding (`pg_advisory_xact_lock(7426300, hashtext(binding))`) and refuses with 409 a
   binding that an unexpired, unrevoked certificate already holds. Two pods racing for one name
   get one certificate. Revoking a binding's certificates (`RevokeBinding`) frees the name, and
   so does their expiry.
4. A class token is not consumed: `used_at` stays empty, the token works for every name until
   `expires_at` or `trica enrol revoke --id`.
5. A one-time token keeps its meaning, and now checks the binding the client asks for: a client
   asking for another binding gets 403 and the token stays unused. It does not check whether
   the binding is held: the operator who minted it for that name meant it.

The DaemonSet mounts the token from an optional Secret `trica-agent-enrol`.

## Consequences

- One command and one Secret enrol a fleet of any size; the hosts outside Kubernetes can use the
  same token.
- **A leaked class token claims free names.** Until it expires or is revoked, its holder can
  enrol a name under the prefix that no node holds yet, and the real node then gets 409. It
  cannot take a name already enrolled, nor one outside the prefix. The mitigation is
  operational: the TTL of a rollout, then revoke the token and delete the Secret. A squatted
  name shows as a 409 in the agent's log and is undone by revoking the binding.
- A node that loses its state directory (no `hostPath`, a reinstalled machine with the same
  hostname) gets 409 until its previous certificate is revoked or expires (seven days by
  default). That is the price of point 3; without it a token holder could take over a running
  node's name.
- The hostname is the client's claim, not something the server verifies: the class token
  trusts whoever holds it to name itself, within the prefix and once per name. TokenReview
  would verify it, for Kubernetes only.
- There is no command listing the live tokens; a token is revoked by the id printed at its
  creation.

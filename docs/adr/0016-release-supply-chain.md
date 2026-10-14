# ADR-0016: A semver tag is the only thing that publishes, and everything it publishes is signed

- **Status**: accepted (2026-10)
- **Concerns**: `.github/workflows/release.yml`, `charts/trica`, `scripts/release-version-check.sh`

## Context

trica inventories infrastructure and holds read access to switches, firewalls and BMCs: its
image is a supply-chain target. Operators pull it through a registry cache and install the
chart from OCI; they must be able to tell what they run and who built it. The chart and the
image are versioned together, and a chart pointing at an image that was never published (or
at another version) is an install that fails at 3 a.m.

## Decision

- Only a `vX.Y.Z` tag publishes. Branches and PRs build and test, never push.
- The version is set in the repository before tagging (`charts/trica/Chart.yaml` `version`
  and `appVersion`); `scripts/release-version-check.sh` stops the release before anything is
  pushed when the tag disagrees. CI exercises the guard on every chart change.
- Images: `linux/amd64` and `linux/arm64`, each built on a native runner and pushed by
  digest, then one manifest list tagged `X.Y.Z`, `X.Y` and `sha-…`. Each carries an SBOM and
  `mode=max` provenance. The manifest list is scanned by Trivy (fixable CRITICAL fails the
  release) and signed with cosign keyless (GitHub OIDC), so no signing key exists to leak.
- The chart is pushed to `oci://ghcr.io/vargafoundation/charts` only after the image is
  published, scanned and signed.
- Static `trica` binaries (and every other `cmd/`) for both architectures, with `SHA256SUMS`
  signed by cosign keyless, and a changelog from git-cliff are attached to the GitHub release.

## Consequences

- Releasing is: bump `Chart.yaml`, merge, tag. Nothing else, nobody's laptop.
- A failed Trivy scan leaves a pushed but unsigned, untagged-by-chart image: unsigned means
  not released. Rerun after fixing; never sign by hand.
- While the repository is private, the packages are private: consumers need a token with
  `read:packages` (the `infra` registry cache does).
- deb/rpm packages of the agent are not built yet; they will join this workflow.

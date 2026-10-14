# Releasing

A `vX.Y.Z` tag publishes a release; nothing else does ([ADR 0016](../adr/0016-release-supply-chain.md)).

## Steps

1. In a PR, set `version` and `appVersion` in `charts/trica/Chart.yaml` to `X.Y.Z`. Merge it.
2. Tag the merge commit and push the tag:

   ```bash
   git switch main && git pull
   scripts/release-version-check.sh vX.Y.Z   # must print "matches"
   git tag -s vX.Y.Z -m vX.Y.Z && git push origin vX.Y.Z
   ```

3. Watch the `release` workflow. It publishes, in this order:

   | Artifact | Where |
   |---|---|
   | Image (amd64 + arm64, SBOM, provenance, cosign signature) | `ghcr.io/vargafoundation/trica:X.Y.Z`, `:X.Y`, `:sha-…` |
   | Helm chart | `oci://ghcr.io/vargafoundation/charts/trica --version X.Y.Z` |
   | Binaries, `SHA256SUMS` and its signature bundle, changelog | the GitHub release `vX.Y.Z` |

## Verifying what you pull

```bash
cosign verify ghcr.io/vargafoundation/trica:X.Y.Z \
  --certificate-identity-regexp '^https://github.com/VargaFoundation/trica/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com

cosign verify-blob SHA256SUMS --bundle SHA256SUMS.sigstore.json \
  --certificate-identity-regexp '^https://github.com/VargaFoundation/trica/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
sha256sum -c SHA256SUMS --ignore-missing
```

## When it fails

- **Version guard**: the tag and `Chart.yaml` disagree. Delete the tag
  (`git push origin :refs/tags/vX.Y.Z`), fix `Chart.yaml` in a PR, tag again.
- **Trivy**: a fixable CRITICAL vulnerability. Nothing was signed or charted. Bump the base
  image or the dependency, release `X.Y.Z+1`.

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
   | Ops image (backup CronJob, amd64 + arm64) | `ghcr.io/vargafoundation/trica-ops:X.Y.Z` |
   | Helm chart | `oci://ghcr.io/vargafoundation/charts/trica --version X.Y.Z` |
   | Binaries (the server with the UI embedded), `SHA256SUMS` and its signature bundle, changelog | the GitHub release `vX.Y.Z` |

## Without GitHub Actions

When the `release` workflow cannot run (runner minutes exhausted, an outage, a fork with no CI),
a maintainer publishes the same artifacts from a workstation. Steps 1 and 2 are unchanged: the
tag and `Chart.yaml` must agree. Needs Docker with buildx, Helm 3, `gh`, Go and Node (for the
binaries), `cosign` (optional), and a GitHub token with `write:packages` and `repo`.

```bash
git switch --detach vX.Y.Z
scripts/release-version-check.sh vX.Y.Z
V=X.Y.Z IMAGE=ghcr.io/vargafoundation/trica
echo "$GITHUB_TOKEN" | docker login ghcr.io -u "$GITHUB_USER" --password-stdin

# Images: both architectures in one manifest list, plus the ops image (backup CronJob).
docker buildx create --use --name trica-release 2>/dev/null || docker buildx use trica-release
docker buildx build --platform linux/amd64,linux/arm64 --build-arg VERSION=v$V \
  --sbom=true --provenance=mode=max \
  -t $IMAGE:$V -t $IMAGE:${V%.*} --push .
docker buildx build --platform linux/amd64,linux/arm64 --build-arg VERSION=v$V --target ops \
  -t ${IMAGE}-ops:$V --push .
docker buildx imagetools inspect $IMAGE:$V          # note the digest
trivy image --severity CRITICAL --ignore-unfixed --exit-code 1 $IMAGE:$V   # if available
cosign sign --yes $IMAGE@sha256:DIGEST               # optional, see below

# Chart, after the image exists.
helm package charts/trica
echo "$GITHUB_TOKEN" | helm registry login ghcr.io -u "$GITHUB_USER" --password-stdin
helm push trica-$V.tgz oci://ghcr.io/vargafoundation/charts

# Binaries, the UI embedded in the server, and the release.
make web-install web-build
mkdir -p dist
for arch in amd64 arm64; do for cmd in cmd/*/; do name=$(basename "$cmd")
  CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -tags webui \
    -ldflags "-s -w -X main.version=v$V" -o "dist/${name}_${V}_linux_${arch}" "./$cmd"
done; done
(cd dist && sha256sum * > SHA256SUMS)
cosign sign-blob --yes --bundle dist/SHA256SUMS.sigstore.json dist/SHA256SUMS   # optional
git cliff --latest --strip header > /tmp/notes.md
gh release create vX.Y.Z dist/* --notes-file /tmp/notes.md
```

A new package on `ghcr.io` is created **private**, whoever pushes it. Making it public (so that
anyone can `docker pull` and `helm pull` without credentials) is a decision of the organisation's
owners, in the package settings on GitHub; check it with an anonymous pull after the first push.

A local `cosign sign` is keyless with the maintainer's own identity, not the workflow's: verify it
with `--certificate-identity <the maintainer's e-mail>` and the issuer of the provider used to sign
(`--certificate-oidc-issuer https://github.com/login/oauth` for a GitHub login), not with the
command below. Say in the release notes that the release was published by hand, and by whom.

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

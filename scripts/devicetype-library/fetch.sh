#!/usr/bin/env bash
# Copies model files, unchanged, from netbox-community/devicetype-library into
# internal/devicetypes/library/, and keeps the manifest that proves they are
# still unchanged (ADR 0056).
#
# Usage:
#   scripts/devicetype-library/fetch.sh add <commit>     copy the files of selection.txt at <commit>
#   scripts/devicetype-library/fetch.sh adopt            list the files copied before the manifest existed
#   scripts/devicetype-library/fetch.sh verify [--upstream]
#                                                        check the files against the manifest
#                                                        (and, with --upstream, against GitHub)
#
# The manifest, internal/devicetypes/library/upstream.tsv, has one line per file
# of a vendor directory: <path under library/> TAB <commit> TAB <git blob sha1>.
# TestUpstreamFilesAreUnchanged recomputes the blobs in CI.
#
# A download is data: it is written straight to its target path, never executed,
# and nothing is run from it. A file is refused when its blob is not the one the
# upstream tree lists for that commit, and an existing file that differs is never
# overwritten. Needs bash, curl, jq and git (for hash-object only).
set -euo pipefail

repo=netbox-community/devicetype-library
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
root=$(cd "$here/../.." && pwd)
lib="$root/internal/devicetypes/library"
manifest="$lib/upstream.tsv"
selection="$here/selection.txt"

# The commits the library was copied at before this script existed (see
# library/README.md), newest first: adopt looks for each file's blob in them.
earlier=(
  565a9ba8e0a68a4fa102ce4b35f30c6695a2d574
  37f5f02c27eedf60767594174598daec5b9e2bee
  9510b3cc3c9fca781ec09b61ef5ca2f3a00cdb39
)

die() { echo "fetch.sh: $*" >&2; exit 1; }

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

blob() { git hash-object --no-filters -- "$1"; }

# tree <commit> writes "<path> TAB <blob>" for every file of device-types/, sorted.
tree() {
  local out="$tmp/tree-$1.tsv"
  if [ ! -s "$out" ]; then
    curl -fsSL "https://api.github.com/repos/$repo/git/trees/$1?recursive=1" -o "$tmp/tree-$1.json"
    [ "$(jq -r .truncated "$tmp/tree-$1.json")" = false ] || die "the tree of $1 is truncated"
    jq -r '.tree[] | select(.type == "blob" and (.path | startswith("device-types/")))
           | [.path[13:], .sha] | @tsv' "$tmp/tree-$1.json" | LC_ALL=C sort > "$out"
  fi
  echo "$out"
}

# vendor_files lists, relative to library/, the files of the vendor directories.
vendor_files() {
  (cd "$lib" && find . -mindepth 2 -type f \( -name '*.yaml' -o -name '*.yml' \) \
    -not -path './trica/*' -not -path './elevation-images/*' -not -path './drawings/*' \
    -not -path './layouts/*' -not -path './overlays/*' | sed 's#^\./##' | LC_ALL=C sort)
}

sort_manifest() { LC_ALL=C sort -o "$manifest" "$manifest"; }

cmd_add() {
  local commit=${1:-}
  [[ $commit =~ ^[0-9a-f]{40}$ ]] || die "add needs a full 40-character commit"
  local t added=0 same=0
  t=$(tree "$commit")
  touch "$manifest"
  while IFS= read -r p; do
    case $p in ''|'#'*) continue ;; esac
    local want
    want=$(awk -F'\t' -v p="$p" '$1 == p { print $2 }' "$t")
    [ -n "$want" ] || die "$p is not in $repo at $commit"
    local dst="$lib/$p"
    mkdir -p "$(dirname "$dst")"
    if [ -e "$dst" ]; then
      [ "$(blob "$dst")" = "$want" ] || die "$p exists and differs from upstream: not overwritten"
      same=$((same + 1))
    else
      # curl --path-as-is keeps a "+" in a name as it is.
      curl -fsSL --path-as-is "https://raw.githubusercontent.com/$repo/$commit/device-types/$p" -o "$dst"
      if [ "$(blob "$dst")" != "$want" ]; then
        rm -f "$dst"
        die "$p: the download is not the blob $want that $commit lists"
      fi
      added=$((added + 1))
    fi
    if ! grep -q -F -x -e "$(printf '%s\t%s\t%s' "$p" "$commit" "$want")" "$manifest"; then
      grep -v -F "$(printf '%s\t' "$p")" "$manifest" > "$tmp/m" || true
      printf '%s\t%s\t%s\n' "$p" "$commit" "$want" >> "$tmp/m"
      cp "$tmp/m" "$manifest"
    fi
  done < "$selection"
  sort_manifest
  echo "added $added files, $same already there, manifest has $(wc -l < "$manifest") lines"
}

cmd_adopt() {
  touch "$manifest"
  local p b c found=0 unknown=0
  while IFS= read -r p; do
    grep -q -F "$(printf '%s\t' "$p")" "$manifest" && continue
    b=$(blob "$lib/$p")
    c=
    for k in "${earlier[@]}"; do
      if awk -F'\t' -v p="$p" -v b="$b" '$1 == p && $2 == b { ok = 1 } END { exit !ok }' "$(tree "$k")"; then
        c=$k
        break
      fi
    done
    if [ -z "$c" ]; then
      echo "NOT UPSTREAM: $p (blob $b is in none of the earlier commits)" >&2
      unknown=$((unknown + 1))
      continue
    fi
    printf '%s\t%s\t%s\n' "$p" "$c" "$b" >> "$manifest"
    found=$((found + 1))
  done < <(vendor_files)
  sort_manifest
  echo "adopted $found files, $unknown not found upstream"
  [ "$unknown" -eq 0 ] || exit 1
}

cmd_verify() {
  local upstream=${1:-} bad=0 p
  [ -s "$manifest" ] || die "no manifest"
  diff <(cut -f1 "$manifest") <(vendor_files) > "$tmp/d" || { cat "$tmp/d"; die "the manifest and the vendor directories differ"; }
  while IFS=$'\t' read -r p c b; do
    if [ "$(blob "$lib/$p")" != "$b" ]; then
      echo "CHANGED: $p" >&2
      bad=$((bad + 1))
    fi
    if [ "$upstream" = --upstream ]; then
      awk -F'\t' -v p="$p" -v b="$b" '$1 == p && $2 == b { ok = 1 } END { exit !ok }' "$(tree "$c")" || {
        echo "NOT UPSTREAM: $p at $c" >&2
        bad=$((bad + 1))
      }
    fi
  done < "$manifest"
  [ "$bad" -eq 0 ] || die "$bad problems"
  echo "$(wc -l < "$manifest") files verified"
}

case ${1:-} in
  add) shift; cmd_add "$@" ;;
  adopt) cmd_adopt ;;
  verify) shift; cmd_verify "$@" ;;
  *) sed -n '2,12p' "$0" >&2; exit 2 ;;
esac

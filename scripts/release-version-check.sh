#!/bin/sh
# Usage: scripts/release-version-check.sh vX.Y.Z
# Fails unless charts/trica/Chart.yaml carries X.Y.Z as both `version` and
# `appVersion`. The release workflow runs it before publishing anything, so
# a chart never ships pointing at another image version.
set -eu
tag="${1:?usage: $0 vX.Y.Z}"
v="${tag#v}"
chart=$(awk '/^version:/ {print $2}' charts/trica/Chart.yaml)
app=$(awk '/^appVersion:/ {gsub("\"", "", $2); print $2}' charts/trica/Chart.yaml)
if [ "$chart" != "$v" ] || [ "$app" != "$v" ]; then
  echo "tag $tag: Chart.yaml has version $chart, appVersion $app; set both to $v before tagging" >&2
  exit 1
fi
echo "tag $tag matches charts/trica/Chart.yaml"

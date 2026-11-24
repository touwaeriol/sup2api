#!/bin/sh
# resolve-tags.sh
#
# Prints, as a JSON array, the release tags plugin-release.yml publishes on a
# push to develop: plugin-<key>-v<version> for every next/plugins/*/
# manifest.json at its manifest version, leaving out tags that already have a
# release (gh release view, GH_REPO). Run from the repository root.
set -eu

for m in next/plugins/*/manifest.json; do
  tag="plugin-$(jq -r .key "$m")-v$(jq -r .version "$m")"
  if gh release view "$tag" >/dev/null 2>&1; then
    echo "$tag: already released" >&2
  else
    echo "$tag"
  fi
done | jq -R . | jq -sc .

#!/bin/sh
# resolve-tags.sh [<key>...]
#
# Prints, as a JSON array, the release tags plugin-release.yml publishes when
# started by hand: plugin-<key>-v<version> for the given manifest keys, or for
# every next/plugins/*/manifest.json when none is given, at their manifest
# versions; tags that already have a release (gh release view, GH_REPO) are
# left out. Run from the repository root.
set -eu

keys=" $* "
tags=
for m in next/plugins/*/manifest.json; do
  key=$(jq -r .key "$m")
  [ "$keys" = "  " ] || case "$keys" in *" $key "*) ;; *) continue ;; esac
  keys=$(printf '%s' "$keys" | sed "s/ $key / /")
  tag="plugin-$key-v$(jq -r .version "$m")"
  if gh release view "$tag" >/dev/null 2>&1; then
    echo "$tag: already released, skipped" >&2
    continue
  fi
  tags="$tags $tag"
done
if [ "$(printf '%s' "$keys" | tr -d ' ')" != "" ] && [ "$#" -gt 0 ]; then
  echo "::error::no plugin under next/plugins has key(s):$keys" >&2
  exit 1
fi
printf '%s\n' $tags | jq -R . | jq -sc 'map(select(. != ""))'

#!/bin/sh
# Run only against a verified, already-started local E2E Compose project.
set -eu

fail() { printf '%s\n' "E2E runner: $*" >&2; exit 1; }
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
project=${E2E_PROJECT:-sub2api-next-e2e}
case "$project" in
  sub2api-next-e2e|sub2api-next-e2e-*) ;;
  *) fail "refusing project '$project'; use sub2api-next-e2e or its -suffix variant" ;;
esac
case "$project" in *[!a-z0-9_-]*) fail "invalid project name" ;; esac
export E2E_PROJECT="$project"
env_file=${E2E_ENV_FILE:-$script_dir/.env}
test -f "$env_file" || fail "missing dedicated env file: $env_file"
source_root=${E2E_SOURCE_ROOT:-$script_dir/../../..}
source_root=$(CDPATH= cd -- "$source_root" && pwd -P) || fail "missing source directory"
test -f "$source_root/next/e2e/publishers.go" || fail "full repository source is required"
test -f "$source_root/next/plugins/guard/go.mod" || fail "guard source is required"
export E2E_SOURCE_ROOT="$source_root"

# The runner mounts the local engine's socket, so a remote/default mismatch
# must be rejected before any test can operate on differently named containers.
endpoint=${DOCKER_HOST:-$(docker context inspect --format '{{.Endpoints.docker.Host}}')}
test "$endpoint" = unix:///var/run/docker.sock || fail "run this script on the test Docker host using /var/run/docker.sock"
test -S /var/run/docker.sock || fail "missing local Docker socket"
docker_bin=${E2E_DOCKER_BIN:-/usr/bin/docker}
test -f "$docker_bin" && test -x "$docker_bin" || fail "missing Linux Docker CLI: $docker_bin"
export E2E_DOCKER_BIN="$docker_bin"

compose() { docker compose --project-name "$project" --env-file "$env_file" -f "$script_dir/compose.yml" "$@"; }
compose config --quiet
for service in node-1 node-2 pg redis caddy mock-upstream; do
  container="$project-$service-1"
  identity=$(docker inspect --format '{{index .Config.Labels "com.docker.compose.project"}}|{{index .Config.Labels "com.docker.compose.service"}}|{{.State.Running}}' "$container") || fail "missing container: $container"
  test "$identity" = "$project|$service|true" || fail "wrong project/service or stopped container: $container"
done
edge_owner=$(docker network inspect --format '{{index .Labels "com.docker.compose.project"}}|{{index .Labels "com.docker.compose.network"}}' "${project}_edge")
test "$edge_owner" = "$project|edge" || fail "edge network belongs to another project"

printf 'Running acceptance tests against dedicated project %s (Go runner, /src source).\n' "$project"
# Compile here, without -trimpath: runtime.Caller locates guard sources at /src.
# Extra arguments are go test flags, e.g. -run '^TestAC17'; a later -timeout
# overrides the default, which covers a full run (about 27 minutes). No services
# are started.
compose run --rm --no-deps -T runner -count=1 -timeout=150m -v "$@" ./...

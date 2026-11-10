#!/bin/sh
# Runs inside the isolated gotest container (/src = approved OVH test dir).
# Usage: sh /src/next/deploy/ci/check.sh modules|plugins|realcore|mutation
set -u
phase=${1:?phase}
out=/src/checks/final
mkdir -p "$out"
cd /src/next

modules() {
  status=0
  : > "$out/modules.summary"
  for dir in runtime-contract sdk tools/sub2api-plugin deploy/mock-upstream gateway server plugins/*/; do
    dir=${dir%/}
    [ -f "$dir/go.mod" ] || continue
    name=$(echo "$dir" | tr '/' '_')
    log="$out/module-$name.log"
    (
      set -e
      cd "$dir"
      echo "=== $dir ==="
      bad=$(gofmt -l .)
      if [ -n "$bad" ]; then echo "gofmt: $bad"; exit 1; fi
      go vet ./...
      go build ./...
      go test -race -count=1 -p 4 -timeout 40m ./...
    ) > "$log" 2>&1
    rc=$?
    echo "$dir rc=$rc" | tee -a "$out/modules.summary"
    [ $rc -eq 0 ] || status=1
  done
  # e2e must compile but never run without a deployment.
  ( set -e; cd e2e; bad=$(gofmt -l .); [ -z "$bad" ] || { echo "gofmt: $bad"; exit 1; }; go vet ./...; go build ./... ) > "$out/module-e2e.log" 2>&1
  rc=$?
  echo "e2e(compile) rc=$rc" | tee -a "$out/modules.summary"
  [ $rc -eq 0 ] || status=1
  # Prove the database-backed cases really executed instead of skipping.
  (
    set -e
    cd server && go test -count=1 -v -run '^TestCoreMigrationsApplyAndAreIdempotent$' ./internal/store/
    cd ../gateway && go test -count=1 -v -run 'TestPostgres|TestRedis|TestTelemetry' ./internal/control/
  ) > "$out/db-guard.log" 2>&1
  rc=$?
  skips=$(grep -c -- '--- SKIP' "$out/db-guard.log" || true)
  passes=$(grep -c -- '--- PASS' "$out/db-guard.log" || true)
  echo "db-guard rc=$rc skips=$skips passes=$passes" | tee -a "$out/modules.summary"
  [ "$rc" -eq 0 ] && [ "$skips" -eq 0 ] && [ "$passes" -gt 0 ] || status=1
  return $status
}

# The pause/admit regression must fail when the in-transaction recheck is
# removed; otherwise it would not prove anything about the race.
mutation() {
  tmp=/tmp/s2-mutation
  rm -rf "$tmp" && cp -a /src/next "$tmp" && cd "$tmp"
  sed -i 's/^\tif err = e.planAuthorizesTx(ctx, tx, digest); err != nil {$/\tif err = error(nil); err != nil {/' gateway/internal/control/engine.go
  grep -c 'error(nil)' gateway/internal/control/engine.go > "$out/mutation.log"
  go test -count=1 -v -run 'TestPostgresPauseBetweenAdmissionCheckAndCommitWins|TestPostgresPlanCreatedBeforeBaselineAdmissionCommitWins' ./gateway/internal/control/ >> "$out/mutation.log" 2>&1
  echo "mutation rc=$? (non-zero expected)" | tee "$out/mutation.summary"
  rm -rf "$tmp"
}

# Signed test packages for the bundled-plugin bootstrap (anthropic, volcengine).
plugins() {
  set -e
  checks=/src/checks
  mkdir -p "$checks/builtin"
  CGO_ENABLED=0 go build -o "$checks/sub2api-plugin" ./tools/sub2api-plugin
  [ -f "$checks/keys/managed-test.key" ] || "$checks/sub2api-plugin" keygen --key-id managed-test --out "$checks/keys" > "$out/plugin-keygen.log"
  mkdir -p "$checks/upload" "$checks/market"
  # anthropic, volcengine and openai are bundled, guard is uploaded, relay comes from a market.
  for spec in anthropic:builtin volcengine:builtin openai:builtin guard:upload relay:market; do
    p=${spec%%:*}; dest="$checks/${spec#*:}"
    "$checks/sub2api-plugin" build --dir "plugins/$p" --out "$checks/$p-runtime" > "$out/plugin-build-$p.log"
    pkg=$("$checks/sub2api-plugin" pack --dir "plugins/$p" --runtimes "$checks/$p-runtime" --out-dir "$dest")
    "$checks/sub2api-plugin" sign --key "$checks/keys/managed-test.key" --key-id managed-test --publisher sub2api "$pkg" >> "$out/plugin-build-$p.log"
  done
  # R2 alone bundles a newer anthropic (the 0.3.2-test overlay, which adds a
  # migration), so updating the core must update it.
  mkdir -p "$checks/builtin-next"
  "$checks/sub2api-plugin" build --dir plugins/anthropic --overlay testdata/upgrade --out "$checks/anthropic-next-runtime" >> "$out/plugin-build-anthropic.log"
  pkg=$("$checks/sub2api-plugin" pack --dir plugins/anthropic --runtimes "$checks/anthropic-next-runtime" --overlay testdata/upgrade --out-dir "$checks/builtin-next")
  "$checks/sub2api-plugin" sign --key "$checks/keys/managed-test.key" --key-id managed-test --publisher sub2api "$pkg" >> "$out/plugin-build-anthropic.log"
  "$checks/sub2api-plugin" index --dir "$checks/market" --key "$checks/keys/managed-test.key" > "$out/plugin-market-index.log"
  ls "$checks/builtin" "$checks/builtin-next" "$checks/upload" "$checks/market"
}

realcore() {
  set -e
  checks=/src/checks
  CGO_ENABLED=0 go build -ldflags '-X main.Version=0.1.0' -o "$checks/core-r1" ./server/cmd/sub2api
  # R2 is built from a separate copy with one test-only SQL migration. It is
  # never added to the application's migration inventory.
  target="$checks/target-next"
  case "$target" in /src/checks/target-next) rm -rf "$target" ;; *) exit 1 ;; esac
  cp -a /src/next "$target"
  cat > "$target/server/internal/migrations/9999_managed_upgrade_probe.sql" <<'SQL'
CREATE TABLE managed_upgrade_probe (id integer PRIMARY KEY, migrated_at timestamptz NOT NULL DEFAULT now());
-- Long enough for the fault test to interrupt the running migration.
SELECT pg_sleep(4);
INSERT INTO managed_upgrade_probe(id) VALUES (1);
SQL
  (cd "$target" && CGO_ENABLED=0 go build -ldflags '-X main.Version=0.1.1' -o "$checks/core-r2" ./server/cmd/sub2api)
  "$checks/core-r1" schema-contract > "$out/schema-r1.txt"
  "$checks/core-r2" schema-contract > "$out/schema-r2.txt"
  sha256sum "$checks/core-r1" "$checks/core-r2" "$checks"/builtin/*.s2plugin "$checks"/upload/*.s2plugin "$checks"/market/*.s2plugin > "$out/artifact-digests.txt"
  export TEST_CORE_V1="$checks/core-r1" TEST_CORE_V2="$checks/core-r2" TEST_SHELL_NODES=3
  export TEST_BUILTIN_DIR="$checks/builtin" TEST_BUILTIN_NEXT_DIR="$checks/builtin-next" TEST_EXPECT_MIGRATION=9999_managed_upgrade_probe.sql TEST_MOCK_URL=http://mock:8080
  TEST_BUILTIN_KEY="managed-test=$(cat "$checks/keys/managed-test.pub")"
  TEST_UPLOAD_PACKAGE=$(ls "$checks"/upload/*.s2plugin) TEST_MARKET_DIR="$checks/market" TEST_MARKET_KEY=$(cat "$checks/keys/managed-test.pub")
  export TEST_UPLOAD_PACKAGE TEST_MARKET_DIR TEST_MARKET_KEY
  export TEST_BUILTIN_KEY
  set +e
  go test -race -count=1 -timeout 45m -v ./gateway/internal/control -run "${REALCORE_RUN:-TestRealCore}" > "$out/realcore.log" 2>&1
  rc=$?
  echo "realcore rc=$rc" | tee "$out/realcore.summary"
  return $rc
}

"$phase"

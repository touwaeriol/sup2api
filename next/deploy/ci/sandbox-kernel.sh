#!/bin/sh
# Runs the plugin sandbox tests (next/server/internal/plugin/sandbox) on the
# kernels of distribution packages, in QEMU (software emulation, no KVM
# needed), so the Landlock layers of newer kernels are executed rather than
# skipped (CONTRACTS §43.8, docs/DESIGN-2026-11-18-PLUGIN-SANDBOX-FS.md).
#
# Usage (from the repository root, on a Docker host):
#   sh next/deploy/ci/sandbox-kernel.sh <kernel .deb URL>... [-- <go test flags>]
# The package must hold the kernel image (vmlinuz): on Debian linux-image-*
# up to 6.x, linux-binary-* from 7.x (both in pool/main/l/linux-signed-amd64), e.g.
#   sh next/deploy/ci/sandbox-kernel.sh \
#     https://deb.debian.org/debian/pool/main/l/linux-signed-amd64/linux-binary-7.3.5+deb14-amd64_7.3.5-1_amd64.deb \
#     https://deb.debian.org/debian/pool/main/l/linux-signed-amd64/linux-image-6.12.117+deb13-amd64_6.12.117-1_amd64.deb
# Extra arguments after -- go to the test binary, e.g. -- -test.run Landlock.
#
# Each kernel boots with only an initramfs (the static test binary and a
# small init, testdata/vminit), lsm=landlock, and runs the tests as uid 1000.
# The script fails unless every kernel prints "SANDBOX-VM: exit 0". Logs stay
# in $SANDBOX_KERNEL_OUT (default ./sandbox-kernel-logs). Containers are
# removed; nothing else is kept. GO_IMAGE (default golang:1.27-bookworm) is
# used for the build and, with qemu-system-x86 installed on the fly, the VM.
# The build reuses the host's Go module and build caches when they exist
# ($HOME/go/pkg/mod, $HOME/.cache/go-build). Container names start with
# $SANDBOX_KERNEL_NAME (default sandbox-kernel).
set -eu
repo=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd -P)
image=${GO_IMAGE:-golang:1.27-bookworm}
out=${SANDBOX_KERNEL_OUT:-$PWD/sandbox-kernel-logs}
urls=""
while [ $# -gt 0 ]; do
  case "$1" in
    --) shift; break ;;
    *) urls="$urls $1"; shift ;;
  esac
done
[ -n "$urls" ] || { echo "usage: $0 <kernel .deb URL>... [-- <go test flags>]" >&2; exit 2; }
flags="$*"
prefix=${SANDBOX_KERNEL_NAME:-sandbox-kernel}-$$
caches=""
[ -d "$HOME/go/pkg/mod" ] && caches="$caches -v $HOME/go/pkg/mod:/gomod -e GOMODCACHE=/gomod"
[ -d "$HOME/.cache/go-build" ] && caches="$caches -v $HOME/.cache/go-build:/gocache -e GOCACHE=/gocache"
mkdir -p "$out"
work=$(mktemp -d)
trap 'docker run --rm -v "$work:/w" "$image" rm -rf /w/root /w/initramfs.gz /w/kernel >/dev/null 2>&1; rm -rf "$work"' EXIT

# Static binaries: plugins are CGO_ENABLED=0, and the initramfs has no libc.
# shellcheck disable=SC2086 # $caches is a list of options
docker run --rm --name "$prefix-build" -u "$(id -u):$(id -g)" -e HOME=/tmp -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=0 $caches \
  -v "$repo:$repo:ro" -v "$work:/w" -w "$repo/next/server" "$image" sh -ec '
    go test -c -o /w/sandbox.test ./internal/plugin/sandbox
    go build -o /w/init ./internal/plugin/sandbox/testdata/vminit'

status=0
for url in $urls; do
  name=$(basename "$url" .deb)
  log="$out/$name.log"
  echo "=== $name"
  # Unpacks the package, builds the initramfs and boots it. Only the
  # kernel image of the package is used; no modules are needed.
  docker run --rm --name "$prefix-vm" -e URL="$url" -e FLAGS="$flags" -v "$work:/w" "$image" sh -ec '
    apt-get update -qq >/dev/null
    apt-get install -y -qq --no-install-recommends qemu-system-x86 cpio >/dev/null
    curl -fsSL -o /tmp/k.deb "$URL"
    dpkg-deb -x /tmp/k.deb /tmp/k
    k=$(find /tmp/k -name "vmlinuz*" -type f | head -n1)
    [ -n "$k" ] || { echo "no vmlinuz in $URL"; exit 1; }
    cp "$k" /w/kernel
    rm -rf /w/root && mkdir -p /w/root/usr/share && cp /w/init /w/sandbox.test /w/root/
    cp -a /usr/share/zoneinfo /w/root/usr/share/  # time zones are on the allow list
    (cd /w/root && find . | cpio -o -H newc --quiet | gzip -1 > /w/initramfs.gz)
    qemu-system-x86_64 -accel tcg -cpu max -m 1024 -smp 2 -nographic -no-reboot \
      -kernel /w/kernel -initrd /w/initramfs.gz \
      -append "console=ttyS0 quiet lsm=landlock panic=-1 rdinit=/init -- $FLAGS"' > "$log" 2>&1 || true
  grep 'SANDBOX-VM:' "$log" || true
  if grep -q 'SANDBOX-VM: exit 0' "$log"; then echo "PASS $name"; else echo "FAIL $name (see $log)"; status=1; fi
done
exit $status

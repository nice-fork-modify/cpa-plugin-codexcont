#!/usr/bin/env sh
set -eu

arch="${1:-arm64}"

case "$arch" in
  arm64|amd64)
    ;;
  *)
    echo "usage: $0 [arm64|amd64]" >&2
    exit 1
    ;;
esac

runtime="${CONTAINER_RUNTIME:-}"
if [ -z "$runtime" ]; then
  if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
    runtime="docker"
  elif command -v podman >/dev/null 2>&1 && podman info >/dev/null 2>&1; then
    runtime="podman"
  elif command -v container >/dev/null 2>&1 && container system status >/dev/null 2>&1; then
    runtime="container"
  fi
fi

if [ -z "$runtime" ]; then
  echo "no container runtime found; set CONTAINER_RUNTIME to docker, podman, or container" >&2
  exit 1
fi

root_dir=$(
  CDPATH= cd -- "$(dirname "$0")/.." && pwd
)
artifact_relpath="dist/linux/$arch/codexcont.so"
out_dir="$root_dir/dist/linux/$arch"
mkdir -p "$out_dir"

case "$runtime" in
  container)
    extra_args="--arch $arch"
    if [ "$arch" = "amd64" ]; then
      extra_args="$extra_args --rosetta"
    fi
    container run --rm $extra_args \
      --mount "type=bind,source=$root_dir,target=/src" \
      -w /src \
      golang:1.26-bookworm \
      bash -lc "mkdir -p /src/dist/linux/$arch && GOCACHE=/tmp/gocache /usr/local/go/bin/go build -buildmode=c-shared -o /src/dist/linux/$arch/codexcont.so . && rm -f /src/dist/linux/$arch/codexcont.h"
    ;;
  docker|podman)
    "$runtime" run --rm \
      --platform "linux/$arch" \
      -v "$root_dir:/src" \
      -w /src \
      golang:1.26-bookworm \
      bash -lc "mkdir -p /src/dist/linux/$arch && GOCACHE=/tmp/gocache go build -buildmode=c-shared -o /src/dist/linux/$arch/codexcont.so . && rm -f /src/dist/linux/$arch/codexcont.h"
    ;;
  *)
    echo "unsupported container runtime: $runtime" >&2
    exit 1
    ;;
esac

echo "built $artifact_relpath"

#!/usr/bin/env bash
# TEIの実行体を、置き場所の異なる二つの経路でビルドし、成果物のdigestを比べる。
# 「複数経路で実行し、同等の結果かを確かめる」が、再現可能ビルドで成り立つ条件を測る。
#
# 使い方: measure_build.sh <go> <tei-platformのcheckout> <出力ディレクトリ>
set -euo pipefail

GO=${1:?go binary}
SRC=${2:?tei-platform checkout}
OUT=${3:?output dir}
mkdir -p "$OUT"

A=$(mktemp -d /tmp/tei-build-a.XXXXXX)
B=$(mktemp -d /tmp/tei-build-b-other.XXXXXX)
trap 'rm -rf "$A" "$B"' EXIT
rev=$(git -C "$SRC" rev-parse HEAD)
git -C "$SRC" archive "$rev" | tar -x -C "$A"
git -C "$SRC" archive "$rev" | tar -x -C "$B"

ms() { date +%s%3N; }
row() { # name dir flags...
  local name=$1 dir=$2; shift 2
  local s e
  s=$(ms)
  (cd "$dir" && CGO_ENABLED=0 "$GO" build "$@" -o "$OUT/$name" ./cmd/tei)
  e=$(ms)
  printf '{"name":"%s","flags":"%s","ms":%s,"sha256":"%s"}' "$name" "$*" $((e-s)) "$(sha256sum "$OUT/$name" | cut -c1-64)"
}

{
  printf '{"source_revision":"%s","go":"%s","builds":[\n' "$rev" "$("$GO" version)"
  row plain-a "$A" -buildvcs=false; printf ',\n'
  row plain-b "$B" -buildvcs=false; printf ',\n'
  row trimpath-a "$A" -trimpath -buildvcs=false; printf ',\n'
  row trimpath-b "$B" -trimpath -buildvcs=false; printf ',\n'
  GOCACHE=$(mktemp -d) row trimpath-a-coldcache "$A" -trimpath -buildvcs=false
  printf '\n]}\n'
} | tee "$OUT/build-measurements.json"

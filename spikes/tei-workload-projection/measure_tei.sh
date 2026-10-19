#!/usr/bin/env bash
# TEIの仕事を手元で実行し、EKIへ出す価値と、二経路で比べられるかを測る。
#
# 使い方: measure_tei.sh <teiバイナリ> <tei-platformのcheckout> <出力ディレクトリ>
#
# 同じワークスペースを、置き場所の異なる二つのディレクトリへ複製し、
# 同じコマンドの出力のsha256を比べる。置き場所に依存する出力は、
# 二経路での完全一致の比較にそのままでは使えない。
set -euo pipefail

TEI=${1:?tei binary}
SRC=${2:?tei-platform checkout}
OUT=${3:?output dir}
mkdir -p "$OUT"

A=$(mktemp -d /tmp/tei-path-a.XXXXXX)
B=$(mktemp -d /tmp/tei-path-b-longer-name.XXXXXX)
trap 'rm -rf "$A" "$B"' EXIT
for d in "$A" "$B"; do
  mkdir -p "$d/reference"
  cp -r "$SRC/reference/workspace" "$d/reference/workspace"
  cp -r "$SRC/reference/driver-scenarios" "$d/reference/driver-scenarios"
  cp -r "$SRC/examples" "$d/examples"
done

ms() { date +%s%3N; }

# name|subcommand (run inside the workspace copy root)
CASES=(
  "validate-reference|validate -workspace reference/workspace -json"
  "validate-first|validate -workspace examples/first-workspace -json"
  "world-reference|world -workspace reference/workspace"
  "source-index-first|source-index -workspace examples/first-workspace -subject-kind semantic-data -subject-id intake.request"
  "scenario-verify|scenario verify -scenario reference/driver-scenarios/order-entry-pattern-smoke.json -workspace reference/workspace"
  "scenario-run|scenario run -scenario reference/driver-scenarios/order-entry-pattern-smoke.json -workspace reference/workspace"
)

printf '%s\n' '{"cases":['  > "$OUT/measurements.json"
first=1
for c in "${CASES[@]}"; do
  name=${c%%|*}; cmd=${c#*|}
  for side in A B; do
    root=${!side}
    start=$(ms)
    set +e
    (cd "$root" && $TEI $cmd) > "$OUT/$name.$side.out" 2> "$OUT/$name.$side.err"
    code=$?
    set -e
    end=$(ms)
    eval "${side}_code=$code; ${side}_ms=$((end-start)); ${side}_sha=$(sha256sum "$OUT/$name.$side.out" | cut -c1-64); ${side}_bytes=$(wc -c < "$OUT/$name.$side.out")"
  done
  # second run on A to check run-to-run stability at the same path
  set +e
  (cd "$A" && $TEI $cmd) > "$OUT/$name.A2.out" 2>/dev/null
  set -e
  A2_sha=$(sha256sum "$OUT/$name.A2.out" | cut -c1-64)
  path_leak=$({ grep -c -e "$A" -e "$B" "$OUT/$name.A.out" "$OUT/$name.B.out" || true; } | awk -F: '{s+=$2} END {print s+0}')
  [ $first -eq 1 ] || printf ',\n' >> "$OUT/measurements.json"
  first=0
  printf '{"name":"%s","command":"tei %s","exit_a":%s,"exit_b":%s,"ms_a":%s,"ms_b":%s,"bytes_a":%s,"same_path_rerun_equal":%s,"cross_path_equal":%s,"lines_with_checkout_path":%s}' \
    "$name" "$cmd" "$A_code" "$B_code" "$A_ms" "$B_ms" "$A_bytes" \
    "$([ "$A_sha" = "$A2_sha" ] && echo true || echo false)" \
    "$([ "$A_sha" = "$B_sha" ] && echo true || echo false)" "$path_leak" >> "$OUT/measurements.json"
done
printf '\n]}\n' >> "$OUT/measurements.json"
cat "$OUT/measurements.json"

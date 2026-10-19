#!/usr/bin/env bash
# eki-controlを一時ディレクトリで起動し、TEI側のAdapter試作から実際に投げる。
#
# 使い方: run_live.sh <eki-controlバイナリ> <eki-fabricのcheckout> <出力ディレクトリ>
set -euo pipefail

CTRL=${1:?eki-control binary}
REPO=${2:?eki-fabric checkout}
OUT=${3:?output dir}
HERE=$(cd "$(dirname "$0")" && pwd)
mkdir -p "$OUT"
WORK=$(mktemp -d /tmp/eki-tei-live.XXXXXX)
trap 'kill $PID 2>/dev/null || true; rm -rf "$WORK"' EXIT

gen=$("$CTRL" gen-token 2>/dev/null)
token=$(printf '%s\n' "$gen" | awk '/^token:/ {print $2}')
sha=$(printf '%s\n' "$gen" | awk '/^sha256:/ {print $2}')
printf '%s' "$token" > "$WORK/token"
chmod 600 "$WORK/token"

PORT=18443
# 受理の上限は、DP-1の小さな構成を想定した値。全Taskで共通の一つしか書けない。
cat > "$WORK/control.json" <<JSON
{
  "listen": "127.0.0.1:$PORT",
  "database": "$WORK/state.db",
  "tokens": [ { "principal": "tei-adapter", "role": "client", "token_sha256": "$sha" } ],
  "policy": {
    "approved_executors": ["tei.validate", "tei.validate-batch", "tei.scenario-run", "tei.build",
                           "tei.generator-candidate", "tei.visual-evaluate", "tei.gate-c-evidence",
                           "tei.deploy-action"],
    "per_task": { "cpu_millis": 4000, "memory_bytes": 4294967296, "wall_time_seconds": 3600,
                  "output_bytes_max": 1073741824, "network_bytes_max": 1073741824,
                  "temporary_disk_bytes": 1073741824 },
    "max_attempts": 5,
    "max_outstanding_tasks": 10,
    "max_deadline_horizon_seconds": 86400
  }
}
JSON

"$CTRL" --config "$WORK/control.json" 2> "$OUT/eki-control.log" &
PID=$!
for _ in $(seq 1 50); do
  if (exec 3<>/dev/tcp/127.0.0.1/$PORT) 2>/dev/null; then break; fi
  sleep 0.1
done

python3 "$HERE/tei_eki_client.py" live --openapi "$REPO/spec/external/openapi.json" \
  --out "$OUT" --url "http://127.0.0.1:$PORT" --token-file "$WORK/token"

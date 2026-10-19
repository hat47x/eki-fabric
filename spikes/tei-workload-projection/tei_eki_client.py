#!/usr/bin/env python3
"""TEIの仕事をEKIのTaskRequestへ投影し、実際のControl Planeへ投げるSpike。

このファイルはTEI側に置かれるAdapterの試作です。EKIのCoreには依存せず、
公開されている言語中立の契約（spec/external/openapi.json）とHTTPだけを使います。
Python標準ライブラリだけで動くようにして、Go SDKを使わない第三者（PAS-13）
という条件も同時に確かめます。

二つの段階を持ちます。

1. offline: TEIの仕事ごとに、TaskRequestへ書けたことと、書けなかったこと
   （TEI側で必要なのに契約に場所がない情報）を記録し、OpenAPIのschemaで検査する。
2. live: 起動済みのeki-controlへ実際に投げ、受理、状態、取消、結果の取得、
   冪等性、上限、期限の扱いを観測する。

使い方:
  tei_eki_client.py offline --openapi PATH --out DIR
  tei_eki_client.py live --openapi PATH --out DIR --url URL --token-file FILE
"""

from __future__ import annotations

import argparse
import copy
import datetime as dt
import json
import re
import sys
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path

# ---------------------------------------------------------------------------
# 最小のJSON Schema検査。openapi.jsonが使う語彙だけを扱う。
# ---------------------------------------------------------------------------


class SchemaChecker:
    def __init__(self, openapi: dict):
        self.root = openapi

    def resolve(self, ref: str) -> dict:
        node = self.root
        for part in ref.lstrip("#/").split("/"):
            node = node[part]
        return node

    def check(self, value, schema: dict, path: str = "$") -> list[str]:
        if "$ref" in schema:
            return self.check(value, self.resolve(schema["$ref"]), path)
        errs: list[str] = []
        t = schema.get("type")
        types = {"object": dict, "array": list, "string": str, "boolean": bool}
        if t == "integer":
            if not isinstance(value, int) or isinstance(value, bool):
                return [f"{path}: expected integer"]
        elif t in types and not isinstance(value, types[t]):
            return [f"{path}: expected {t}"]
        if "const" in schema and value != schema["const"]:
            errs.append(f"{path}: expected const {schema['const']!r}")
        if "enum" in schema and value not in schema["enum"]:
            errs.append(f"{path}: {value!r} not in {schema['enum']}")
        if isinstance(value, str):
            if "pattern" in schema and not re.search(schema["pattern"], value):
                errs.append(f"{path}: {value!r} does not match pattern")
            if len(value) < schema.get("minLength", 0):
                errs.append(f"{path}: too short")
            if "maxLength" in schema and len(value) > schema["maxLength"]:
                errs.append(f"{path}: too long")
        if isinstance(value, int) and not isinstance(value, bool):
            if "minimum" in schema and value < schema["minimum"]:
                errs.append(f"{path}: below minimum {schema['minimum']}")
            if "maximum" in schema and value > schema["maximum"]:
                errs.append(f"{path}: above maximum {schema['maximum']}")
        if isinstance(value, list):
            if "maxItems" in schema and len(value) > schema["maxItems"]:
                errs.append(f"{path}: too many items")
            for i, item in enumerate(value):
                errs += self.check(item, schema.get("items", {}), f"{path}[{i}]")
        if isinstance(value, dict):
            props = schema.get("properties", {})
            for req in schema.get("required", []):
                if req not in value:
                    errs.append(f"{path}: missing required {req!r}")
            if "maxProperties" in schema and len(value) > schema["maxProperties"]:
                errs.append(f"{path}: too many properties")
            for k, v in value.items():
                if k in props:
                    errs += self.check(v, props[k], f"{path}.{k}")
                elif schema.get("additionalProperties") is False:
                    errs.append(f"{path}: unknown property {k!r}")
        return errs


# ---------------------------------------------------------------------------
# TEIの仕事のカタログ。
#
# 値の根拠は、同じSpikeの measure_tei.sh / measure_build.sh の実測と、
# tei-platform main 1f69562b8 のコードの読み取り。
# `needs` には、TEI側でその仕事を安全・正確に扱うために必要なのに、
# 現在のTaskRequestに書く場所がない情報を記録する。
# ---------------------------------------------------------------------------

GiB = 1 << 30
MiB = 1 << 20


def base_request(executor: str, params: dict, *, cpu=1000, mem=512 * MiB, wall=60,
                 out=16 * MiB, net=0, retryable=True, preemptible=True,
                 deterministic=True, attempts=3, priority="normal") -> dict:
    return {
        "schema": "eki.task-request/v1",
        "executor": {"id": executor, "version_constraint": "^0"},
        "inputs": [{"name": "workspace", "artifact_id": "art_tei_workspace_placeholder"}],
        "parameters": params,
        "resources": {"cpu_millis": cpu, "memory_bytes": mem, "wall_time_seconds": wall,
                      "output_bytes_max": out, "network_bytes_max": net},
        "reliability": {"retryable": retryable, "preemptible": preemptible,
                        "deterministic": deterministic, "max_attempts": attempts,
                        "effect_mode": "none"},
        "priority": priority,
    }


NEED = {
    "digest-pin": "実行体のdigestをClient側から安全側へ固定する場所（TEIはplugin lockでdigestまで固定する）",
    "workspace-input": "ワークスペース一式を内容digestで渡す経路（Artifact取り込みは501）",
    "result-fetch": "結果のArtifactと来歴を受け取る経路（/resultは501）",
    "runtime-class": "結果の来歴としての実行環境の区分（OS、arch、toolchainの版）",
    "assurance-request": "保証の要求（RA、経路の数、独立性の次元）",
    "equivalence": "「同等」の判定方法（digest一致、正規化後の一致、Validator）",
    "service-class": "応答の速さの等級（priorityは順番の希望で、応答時間の見込みを表さない）",
    "session": "枠を開いて小さな依頼を続けて流す形態（つなぐ）",
    "cache-affinity": "ビルドキャッシュやモジュールキャッシュの局所性の希望（実測で約15倍の差）",
    "toolchain-provisioning": "toolchainと依存モジュールを事前配置し、Worker内での取得を禁じる宣言",
    "escalation-budget": "期限に向けて保証された容量へ移す予算の権限（必達）",
    "partial-collection": "一群の依頼のうち期限内に終わった分だけを集める単位",
    "effect-profile": "外部への副作用と冪等キー、照合を利用者側で行う宣言（effect_modeはnoneのみ）",
    "loopback-network": "Worker内でのloopback通信とサーバ起動を許すnetwork profile",
    "model-lineage": "評価者ごとのmodel lineageを来歴に残し、一致で一つにまとめない指定",
    "resource-defaults": "必須の資源量5つを、等級の既定値で省略すること",
}

CATALOG = [
    {
        "id": "W1-validate-one",
        "tei": "tei validate -workspace reference/workspace -json",
        "observed": "実測 8〜9 ms、出力721 bytes、置き場所を変えても完全一致",
        "fabric_value": "単独ではない（RQ-014）。EKIの往復の方が遅い",
        "request": base_request("tei.validate", {"workspace_dir": "reference/workspace", "format": "json"},
                                wall=1, mem=256 * MiB, out=1 * MiB),
        "needs": ["digest-pin", "workspace-input", "result-fetch", "resource-defaults"],
    },
    {
        "id": "W2-validate-release-sweep",
        "tei": "全リリース版×全ワークスペースの一括検証（W1を数百件）",
        "observed": "一件 5〜11 ms。件数で初めて意味が出る",
        "fabric_value": "一件ずつのTaskでは往復の費用が勝つ。まとめた一件か、枠（session）が要る",
        "request": base_request("tei.validate-batch", {"manifest": "releases.json"},
                                wall=600, priority="background"),
        "needs": ["workspace-input", "result-fetch", "session", "partial-collection"],
    },
    {
        "id": "W3-scenario-run",
        "tei": "tei scenario run -scenario reference/driver-scenarios/order-entry-pattern-smoke.json -workspace reference/workspace",
        "observed": "実測 10 ms、出力1716 bytes、置き場所を変えても完全一致",
        "fabric_value": "シナリオ数が多い場合だけ",
        "request": base_request("tei.scenario-run", {"scenario": "order-entry-pattern-smoke.json"},
                                wall=30),
        "needs": ["digest-pin", "workspace-input", "result-fetch", "equivalence"],
    },
    {
        "id": "W4-reproducible-build-two-paths",
        "tei": "プラグイン/実行体の再現ビルドを、運営者の異なる二つのプールで行いdigestを比べる",
        "observed": "-trimpathなしでは置き場所だけでdigestが不一致。-trimpathありで一致。キャッシュ温 0.8 s / 冷 12.5 s",
        "fabric_value": "高い。二経路の比較が意味を持つ代表例",
        "request": base_request("tei.build", {"package": "./cmd/tei", "trimpath": True},
                                cpu=4000, mem=2 * GiB, wall=900, out=64 * MiB, net=0,
                                priority="background"),
        "needs": ["digest-pin", "workspace-input", "result-fetch", "assurance-request", "equivalence",
                  "runtime-class", "cache-affinity", "toolchain-provisioning"],
    },
    {
        "id": "W5-generator-candidates-fanout",
        "tei": "設計の場で生成器の候補12件を並列にビルドし、fixtureで30秒以内に実行して選ぶ（人が待っている）",
        "observed": "候補ごとに go build（キャッシュ温で約1 s、冷で十数秒）＋実行（上限30 s）",
        "fabric_value": "高い。ただし人が待つので最初の結果までの時間が効く",
        "fanout": 12,
        "request": base_request("tei.generator-candidate", {"candidate_index": 0},
                                cpu=2000, mem=1 * GiB, wall=60, priority="high"),
        "needs": ["workspace-input", "result-fetch", "service-class", "partial-collection",
                  "cache-affinity", "session"],
    },
    {
        "id": "W6-visual-evaluators",
        "tei": "画面の見た目を、系統の異なる3つの評価者で評価し、結果を並べて残す",
        "observed": "評価者の枠は manifest の evaluator identity/version/method で一つ",
        "fabric_value": "評価者を別資源で動かし、来歴を残す",
        "fanout": 3,
        "request": base_request("tei.visual-evaluate", {"evaluator": "lineage-a"},
                                cpu=4000, mem=8 * GiB, wall=600, deterministic=False),
        "needs": ["workspace-input", "result-fetch", "model-lineage", "assurance-request"],
    },
    {
        "id": "W7-must-complete-release-evidence",
        "tei": "リリース前夜に全証拠を翌朝までに必ず揃える。遊休PCが足りなければクラウドへ",
        "observed": "deadline は書けるが、間に合わせるための引き上げは書けない",
        "fabric_value": "高い（遊休資源と管理資源の統合の主目的）",
        "request": base_request("tei.validate-batch", {"manifest": "release-candidate.json"},
                                wall=3600, priority="high"),
        "deadline_in_s": 8 * 3600,
        "needs": ["escalation-budget", "service-class", "partial-collection", "workspace-input"],
    },
    {
        "id": "W8-gate-c-evidence",
        "tei": "tools/run-first-web-target-gate-c-evidence.sh（対象を127.0.0.1で起動しcurlで確かめる）",
        "observed": "Worker内でのサーバ起動とloopback通信が要る",
        "fabric_value": "中。network profileが決まるまで載らない",
        "request": base_request("tei.gate-c-evidence", {"target": "first-web"},
                                cpu=2000, mem=2 * GiB, wall=1800, net=0),
        "needs": ["loopback-network", "workspace-input", "result-fetch", "runtime-class"],
    },
    {
        "id": "W9-deployment-action",
        "tei": "配備Executor（IdempotencyKeyつき、accepted/rejected/unknown、TEI側で照合）",
        "observed": "外部への副作用がある",
        "fabric_value": "未決（業務Applicationの配置に近づく）",
        "request": base_request("tei.deploy-action", {"action_id": "act-1"},
                                retryable=False, preemptible=False, deterministic=False,
                                attempts=1),
        "needs": ["effect-profile"],
        "effect_mode_wanted": "client-reconciled",
    },
]


def offline(openapi: dict, out: Path) -> dict:
    chk = SchemaChecker(openapi)
    schema = {"$ref": "#/components/schemas/TaskRequest"}
    rows = []
    for w in CATALOG:
        req = copy.deepcopy(w["request"])
        if "deadline_in_s" in w:
            req["deadline"] = (dt.datetime.now(dt.timezone.utc)
                               + dt.timedelta(seconds=w["deadline_in_s"])).isoformat(timespec="seconds")
        errs = chk.check(req, schema)
        # TEIが本当は書きたかった形（表現できないため、schema違反になることを確かめる）
        wanted = copy.deepcopy(req)
        wanted["executor"]["digest"] = {"algorithm": "sha256", "value": "0" * 64}
        if w.get("effect_mode_wanted"):
            wanted["reliability"]["effect_mode"] = w["effect_mode_wanted"]
        wanted_errs = chk.check(wanted, schema)
        rows.append({
            "id": w["id"], "tei": w["tei"], "observed": w["observed"],
            "fabric_value": w["fabric_value"],
            "schema_errors": errs,
            "wanted_form_schema_errors": wanted_errs,
            "needs": [{"key": k, "why": NEED[k]} for k in w["needs"]],
        })
    counts: dict[str, int] = {}
    for w in CATALOG:
        for k in w["needs"]:
            counts[k] = counts.get(k, 0) + 1
    report = {"workloads": rows,
              "need_frequency": dict(sorted(counts.items(), key=lambda kv: -kv[1]))}
    (out / "offline-report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2))
    return report


# ---------------------------------------------------------------------------
# live: 実際のeki-controlへ投げる。
# ---------------------------------------------------------------------------


class Eki:
    def __init__(self, url: str, token: str):
        self.url = url.rstrip("/")
        self.token = token

    def call(self, method: str, path: str, body=None, key: str | None = None):
        data = None if body is None else json.dumps(body).encode()
        req = urllib.request.Request(self.url + path, data=data, method=method)
        req.add_header("Authorization", f"Bearer {self.token}")
        if body is not None:
            req.add_header("Content-Type", "application/json")
        if key:
            req.add_header("Idempotency-Key", key)
        t0 = time.perf_counter()
        try:
            with urllib.request.urlopen(req, timeout=10) as r:
                code, raw = r.status, r.read()
        except urllib.error.HTTPError as e:
            code, raw = e.code, e.read()
        ms = (time.perf_counter() - t0) * 1000
        try:
            payload = json.loads(raw) if raw else None
        except json.JSONDecodeError:
            payload = raw.decode(errors="replace")
        return code, payload, ms


def tei_outcome(status: dict | None) -> str:
    """EKIの公開状態を、TEIの配備Executorの三値へ写す（research §5.3の対応表）。"""
    if not status:
        return "unknown"
    s = status.get("status")
    a = (status.get("assurance") or {}).get("status")
    if s in ("queued", "running", "verifying"):
        return "pending"
    if s == "completed":
        return "accepted" if a in ("satisfied", "not-required") else "accepted-unverified-claim"
    if s in ("failed", "cancelled"):
        return "rejected"
    return "unknown"


def strip_inputs(req: dict) -> dict:
    r = copy.deepcopy(req)
    r["inputs"] = []
    return r


def live(openapi: dict, out: Path, eki: Eki) -> dict:
    log: dict = {"probes": []}

    def probe(name, **kv):
        kv["name"] = name
        log["probes"].append(kv)
        print(json.dumps(kv, ensure_ascii=False))

    # L1 そのままの投影（入力つき）と、入力を外した投影
    for w in CATALOG:
        code, body, ms = eki.call("POST", "/v1/tasks", w["request"], key=f"{w['id']}-with-input")
        probe("L1-submit-as-projected", workload=w["id"], http=code,
              code=(body or {}).get("code") if isinstance(body, dict) else None, ms=round(ms, 1))
    accepted = {}
    for w in CATALOG:
        code, body, ms = eki.call("POST", "/v1/tasks", strip_inputs(w["request"]), key=f"{w['id']}-no-input")
        probe("L1-submit-without-inputs", workload=w["id"], http=code,
              code=(body or {}).get("code") if isinstance(body, dict) else None, ms=round(ms, 1))
        if code == 202:
            accepted[w["id"]] = body["task_id"]

    # L2 TEIが本当に書きたい形（digest固定、副作用の宣言、拡張に入れる）
    w1 = strip_inputs(CATALOG[0]["request"])
    pinned = copy.deepcopy(w1)
    pinned["executor"]["digest"] = {"algorithm": "sha256", "value": "0" * 64}
    code, body, _ = eki.call("POST", "/v1/tasks", pinned, key="tei-pin-field-0001")
    probe("L2-digest-pin-as-field", http=code, code=(body or {}).get("code"))
    ext = copy.deepcopy(w1)
    ext["extensions"] = [{"id": "dev.tei/executor-digest-pin", "critical": True,
                          "payload": {"sha256": "0" * 64}}]
    code, body, _ = eki.call("POST", "/v1/tasks", ext, key="tei-pin-ext-0001")
    probe("L2-digest-pin-as-critical-extension", http=code, code=(body or {}).get("code"))

    # L3 冪等性: TEIのIdempotencyKeyの意味と合うか
    code1, b1, _ = eki.call("POST", "/v1/tasks", w1, key="tei-idem-0001")
    code2, b2, _ = eki.call("POST", "/v1/tasks", w1, key="tei-idem-0001")
    changed = copy.deepcopy(w1)
    changed["parameters"]["format"] = "text"
    code3, b3, _ = eki.call("POST", "/v1/tasks", changed, key="tei-idem-0001")
    probe("L3-idempotency", first=code1, replay=code2,
          same_task=(isinstance(b1, dict) and isinstance(b2, dict) and b1.get("task_id") == b2.get("task_id")),
          different_body_same_key=code3, code=(b3 or {}).get("code") if isinstance(b3, dict) else None)
    if code1 == 202:
        accepted["L3"] = b1["task_id"]

    # L4 状態とTEIの三値。結果の取得。期限を過ぎたqueuedのTask。
    short = copy.deepcopy(w1)
    short["deadline"] = (dt.datetime.now(dt.timezone.utc) + dt.timedelta(seconds=3)).isoformat(timespec="seconds")
    code, body, _ = eki.call("POST", "/v1/tasks", short, key="tei-deadline-3s-0001")
    deadline_task = body.get("task_id") if code == 202 else None
    probe("L4-submit-with-3s-deadline", http=code, code=(body or {}).get("code") if isinstance(body, dict) else None)
    time.sleep(5)
    if deadline_task:
        code, st, _ = eki.call("GET", f"/v1/tasks/{deadline_task}")
        probe("L4-status-after-deadline-passed", http=code, status=st.get("status"),
              terminal_code=st.get("terminal_code"), tei_outcome=tei_outcome(st))
        accepted["deadline"] = deadline_task
    any_task = next(iter(accepted.values()), None)
    if any_task:
        code, st, ms = eki.call("GET", f"/v1/tasks/{any_task}")
        probe("L4-status", http=code, status=st.get("status"), assurance=st.get("assurance"),
              tei_outcome=tei_outcome(st), ms=round(ms, 1))
        code, body, _ = eki.call("GET", f"/v1/tasks/{any_task}/result")
        probe("L4-result", http=code, code=(body or {}).get("code") if isinstance(body, dict) else None)
    code, body, _ = eki.call("POST", "/v1/artifacts/ingest-intents",
                             {"name": "workspace", "media_type": "application/zip"}, key="tei-ingest-0001")
    probe("L4-artifact-ingest-intent", http=code, code=(body or {}).get("code") if isinstance(body, dict) else None)

    # L5 取り消してから、候補12件の展開（人が待っている）
    for tid in list(accepted.values()):
        eki.call("POST", f"/v1/tasks/{tid}/cancel", {}, key=f"cancel-{tid}")
    cand = CATALOG[4]
    results, lat = [], []
    run = uuid.uuid4().hex[:8]
    for i in range(cand["fanout"]):
        r = strip_inputs(cand["request"])
        r["parameters"]["candidate_index"] = i
        code, body, ms = eki.call("POST", "/v1/tasks", r, key=f"cand-{run}-{i}")
        results.append(code)
        lat.append(ms)
        if code == 202:
            accepted[f"cand{i}"] = body["task_id"]
    probe("L5-fanout-12-candidates", http_codes=results,
          accepted=results.count(202), throttled=results.count(429),
          submit_ms_median=round(sorted(lat)[len(lat) // 2], 1), submit_ms_max=round(max(lat), 1))

    # L6 取消のTEI三値。取消後に枠が戻るか。
    first = accepted.get("cand0")
    if first:
        code, body, _ = eki.call("POST", f"/v1/tasks/{first}/cancel", {}, key=f"cancel-c0-{run}")
        _, st, _ = eki.call("GET", f"/v1/tasks/{first}")
        probe("L6-cancel", http=code, status=st.get("status"), terminal_code=st.get("terminal_code"),
              tei_outcome=tei_outcome(st))
    for tid in list(accepted.values()):
        eki.call("POST", f"/v1/tasks/{tid}/cancel", {}, key=f"cancel-all-{run}-{tid}")

    # L7 小さな依頼を続けて流したときの往復時間（つなぐ形態の代わり）
    times = []
    for i in range(30):
        code, body, ms_submit = eki.call("POST", "/v1/tasks", w1, key=f"burst-{run}-{i}")
        if code != 202:
            times.append(None)
            continue
        _, _, ms_status = eki.call("GET", f"/v1/tasks/{body['task_id']}")
        eki.call("POST", f"/v1/tasks/{body['task_id']}/cancel", {}, key=f"burst-cancel-{run}-{i}")
        times.append(ms_submit + ms_status)
    ok = sorted(t for t in times if t is not None)
    probe("L7-burst-30-submit-status-roundtrip", ok=len(ok),
          median_ms=round(ok[len(ok) // 2], 1) if ok else None,
          p90_ms=round(ok[int(len(ok) * 0.9) - 1], 1) if ok else None,
          tei_validate_local_ms=8)

    (out / "live-report.json").write_text(json.dumps(log, ensure_ascii=False, indent=2))
    return log


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("mode", choices=["offline", "live"])
    ap.add_argument("--openapi", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--url")
    ap.add_argument("--token-file")
    a = ap.parse_args()
    openapi = json.loads(Path(a.openapi).read_text())
    out = Path(a.out)
    out.mkdir(parents=True, exist_ok=True)
    if a.mode == "offline":
        rep = offline(openapi, out)
        for r in rep["workloads"]:
            print(r["id"], "schema_ok" if not r["schema_errors"] else r["schema_errors"],
                  "| wanted:", r["wanted_form_schema_errors"])
        print(json.dumps(rep["need_frequency"], ensure_ascii=False))
        return 0
    token = Path(a.token_file).read_text().strip()
    live(openapi, out, Eki(a.url, token))
    return 0


if __name__ == "__main__":
    sys.exit(main())

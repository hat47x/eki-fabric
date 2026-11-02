# TEI投影artifactの読み取り（TEI Projection Consumer Spike）

TAI側のパイロットが作る`tai.reference.tei-eki-task-request-projection/v0`を、EKI側から読めるかを確かめる小さなSpikeです。

## 役割

- artifactの封筒（`schema`・`contract`・`source`）と、平坦な`taskRequest`ヘッダを検査する
- `eki.task-request/v1`の全項目（`inputs`・`parameters`・`resources`・`reliability`）は対象外
- `eki-control`の呼び出し、Taskの投入、保存は行わない

## 既存Spikeとの違い

`spikes/tei-workload-projection`はTEIの仕事をEKIのTaskRequestへ投影する（TEI → EKI）。このSpikeは逆向きで、EKIの公開契約をTEIで記述したartifactを読む（EKI → TEI）。名前も範囲も別です。

## 実行

```bash
go test ./spikes/tei-projection-consumer/...
# 実artifactを読む場合
TEI_EKI_PROJECTION_ARTIFACT=<artifact> go test ./spikes/tei-projection-consumer/...
```

`TEI_EKI_PROJECTION_ARTIFACT`が未設定のとき、実artifactのテストはskipします。

## 限界

- 製品コード（`internal/`・`cmd/`）へは依存を持ち込まない
- EKIの受入判断や要件変更の根拠にしない
- 読み取りだけで、書き込み・検証済みの主張はしない

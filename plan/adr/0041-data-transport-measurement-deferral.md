# ADR-0041: M0/MVP の Data Transport は比較測定の完了を待って WebRTC/WebTransport/WebSocket の採否を決める

## 状態

Accepted（仮）。測定完了後に再評価する。

## 背景

- #753（`spikes/data-delivery-transport`）は、同一の Data/Delivery 契約を **Local IPC** と **Connect** で比較する Spike である。
- 2026-11-11 時点での #753 の到達点：
  - D0-A（Fixture の形式検査・参照意味テスト）は WSL Go 1.27.3 で PASS。
  - D0-B/C（Local IPC／Connect の本番 Data Adapter の実測）は **NOT RUN**。現状、Adapter の実装・配線が確認できていない。
  - D1（p50/p95/p99・欠落・転送量などの実測）は未着手。
- #754 の完了条件は「spike の測定値か、測れなかった理由を引用した ADR」である。

## 決断

**M0/MVP では、WebRTC・WebTransport・WebSocket を新たに採用しない。**

既存の ADR-0001 の選定を維持する：

- External Client Plane：HTTPS + JSON + OpenAPI
- Worker / Adapter Plane：Protobuf IDL + Connect
- Authority Decision：deterministic CBOR + CDDL + COSE_Sign1

Local IPC は同一ホストでの低遅延候補として位置付けるが、本番 Adapter の実装・配線が確認できていないため、M0/MVP の既定 Transport には含めない。

## 理由

1. **測定が結論に合わされないようにする**（#754 の前提）。実測がない段階で特定のプロトコルを採択すると、Spike の設計が後追いになる。
2. **既存経路で必要な性質が证明されていない**。Connect の継続通信や Unix ソケットの配送は、試験用の限定環境では成立しているが、本番の Data Adapter・Principal/Lease/Grant の実権限経路・Consumer の Consumed までは証拠がない。
3. **新たな複雑性を M0/MVP に持ち込まない**。WebRTC はシグナリング・NAT/TURN・中継の運用費、企業プロキシ、欠落・順序の扱いを一括で増やす。これらは、同じ Data/Delivery 契約を Local IPC と Connect で満たせることが確認できた後に評価する。
4. **M0/MVP の優先順位を変えない**。新しい Host/Registry/AI ゲートウェイを前提とせず、Worker 制御用 `WatchCommands` を汎用 Data Plane へ暗黙に拡張しない。

## 結果

- 依頼元は M0/MVP で **Connect** 経路を前提として計画できる。
- 低遅延が必要な利用場面は、**Local IPC が本番 Adapter として実装された場合**に検討する。
- WebRTC / WebTransport / WebSocket は、#753 の D0-B/C と D1 の実測が得られた後、**追加の ADR** で比較する。
- 本 ADR は「現時点で測定が不足しているため採否を留保する」判定であり、将来の測定結果によって覆される。

## 測定が揃ったときの引き渡し条件

- #753 の D0-B/C：Local IPC と Connect の双方で、同一 Fixture を実際の Adapter が処理し、独立 Expected と観測が一致する。
- #753 の D1：同一環境・負荷で遅延 p50/p95/p99、欠落・順序・転送量・CPU・失効時の挙動が記録されている。
- 上記を満たしたときだけ、新 ADR で WebRTC 等の追加採用・非採用を決める。

## 関連

- #753：最小契約例の Local IPC / Connect 比較 Spike
- #754：本 ADR（測定後の採否）
- ADR-0001：Protocol surfaces and canonical encoding
- RQ-064：Data/Delivery/Transport Profile
- QA-13：遅延と鮮度・同期の測定観点

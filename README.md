# EKI Fabric（益）

EKI Fabricは、業務端末、個人ワークステーション、専用サーバ、ブラウザ、クラウドなどに生じる計算余力を、安全性、利用者への影響、データの所在、ネットワーク負荷、コストを考慮しながら活用する **distributed compute fabric** を目指す実験的なOSSプロジェクトです。

一つの製品や組織だけの計算基盤には限定しません。SEI Cognition、TEI、SUIなど既知のOSSに加え、将来の未知のOSS、Webアプリ、他社製品などから共通利用できることを前提にしています。

現在は、具体的なArchitectureへ入る前の上流工程にあります。NATS、WASM、SPIFFEなど特定の技術を先に採用するのではなく、まず「何を守るのか」「どのような利用場面で成立する必要があるのか」「悪意のあるClientやWorkerに対して何を保証するのか」を具体化しています。

## まず全体像を知りたい方へ

初めてEKI Fabricを読む場合は、[はじめてEKI Fabricを読む方へ](docs/for-readers.md) から始めてください。

分散システムやRemote Attestationなどの専門知識を前提にせず、次の背景から説明しています。

- なぜ個人PCや業務端末の余力を使うのか
- なぜ一般的な固定クラスタとは違うのか
- なぜClientもWorkerも無条件には信用しないのか
- 悪意を前提にしながら、なぜ善意のアドホック参加者を受け入れられるのか
- Workerの結果をどのように検証するのか
- Compute Grantや資源提供実績を、どこまでEKI自身が扱うのか

文書全体の案内は [ドキュメント案内](docs/README.md)、専門用語は [用語集](docs/glossary.md) にまとめています。

## EKI Fabricが目指すもの

出発点の一つは、価格競争力の高い個人PCやワークステーションを日常業務のために十分な性能で用意し、本人が使っていない時間だけ、その余力を組織全体の計算資源として活用する考え方です。

ただし、「空いている資源はできるだけ使い切る」ことを目標にはしません。

```text
利用者の本来作業
        ↓ 最優先
端末・ネットワーク・電力の安全余力
        ↓
残った計算余力
        ↓
組織や社会の認知活動へ還流
```

業務端末では、利用者が戻った時点で計算を縮小・停止できることを重視します。分散させることでネットワーク負荷が増える場合は、ローカル実行を選べる必要があります。

また、高価な高可用性をすべての処理へ一律に求めるのではなく、途中で失敗しても再実行できる処理については、一時的で安価なWorkerを組み合わせ、Fabric全体として必要な信頼性を得ることを目指します。

## 安全性についての基本姿勢

EKI Fabricでは、ClientもWorkerも信頼境界の外に置きます。

認証済みであること、社内端末であること、公式Agentを使っていること、過去に正しく動いたことだけでは、善意や未改変性を保証できないためです。

そのため、たとえば次を区別します。

```text
認証された
    ≠
あらゆる操作を許可される

Workerが結果を返した
    ≠
その結果が正しい

Workerが資源を提供したと申告した
    ≠
その量の貢献が確認された
```

一方で、この考え方は参加者を排除するためのものではありません。安全性を参加者の人格や善意に依存させないことで、公開データを使う検証可能な処理などでは、一時的な個人参加やブラウザからの参加も受け入れられる余地を作ります。

## 企業内から公益利用まで

EKI Fabricは、まず企業内の遊休計算資源を安全に利用できることを重要な利用形態とします。そのうえで、同じ基本モデルを複数組織間や公益用途へ広げられることを想定しています。

たとえば、クラウド事業者や大手企業が、通常利用を妨げない範囲の余剰計算能力を、研究機関、OSS、公益性の高い団体などへ提供する構成が考えられます。

このような利用枠を、現段階では **Compute Grant** と呼んでいます。

また、資源を提供した事実を検証可能な形で記録し、社内配賦、相互利用枠、寄付実績、コミュニティ上の評価など、外部の価値交換制度へ接続できる余地を持たせます。

ただし、EKI Core自身が通貨を発行したり、現金化、市場価格、交換レートを提供したりすることは目標にしません。どのような価値へ対応付けるかは、それぞれの主体へ委ねます。

## 現在の主要な前提

- 特定の利用OSSやプログラミング言語へ依存しない。
- Reference ImplementationはGoを採用するが、外部契約をGo専用にはしない。
- 業務端末、専用ノード、Browser Worker、外部組織資源、クラウドなどを同じ性質のWorkerとはみなさない。
- 業務端末では利用者の本来作業とローカルな安全判断を中央Schedulerより優先する。
- ClientもWorkerも悪意を持ち得るものとして設計する。
- Workerの結果は、Taskに必要な検証を通るまで確定した結果とはみなさない。
- Workerが離脱することを異常事象だけではなく通常の状態変化として扱う。
- 計算性能だけでなく、データ局所性や転送量も配置判断に含める。
- 公益利用や組織間利用を可能にするが、参加するだけで相互に包括的な信頼を与えない。

## 要件レビューへ参加する方へ

要求の正本は [Product Requirements](requirements/PRODUCT_REQUIREMENTS.md) です。

初めて要求文書を読む場合は、先に [要求文書の読み方](requirements/README.md) を参照してください。その後、関心に応じて次へ進めます。

- [Threat Model](requirements/THREAT_MODEL.md) — どの攻撃者を想定し、何を守るか
- [Worker Assurance Model](requirements/ASSURANCE_MODEL.md) — Workerの保証を単一のTrust Levelにしない理由
- [Acceptance Scenarios](requirements/ACCEPTANCE_SCENARIOS.md) — 実際の利用・攻撃場面から要求を圧力試験する
- [Reference Systems Research](research/reference-systems.md) — 既存システムや標準から何を学ぶか
- [AI時代の価値と位置づけ](research/ai-era-value-positioning.md) / [既存オーケストレータとの接続](research/orchestrator-integration-and-layering.md) — 今後2〜5年を想定した位置づけの検討（調査メモ、正本ではない）
- [Upstream Plan](plan/UPSTREAM_PLAN.md) — Architectureへ入るまでに何を確認するか

## 現在地

Architecture Entry Gateを通過し、Authority / Contract / Control-Data Plane / Deployment Profile / Task State Model / Extension SPIから成る **Architecture Baseline 0.1** を定義済みです。

External Client Planeについては、HTTPS + JSON + OpenAPIを採用し、P0 machine-readable contractを [`spec/external/openapi.json`](spec/external/openapi.json) に置いています。Worker / Adapter PlaneはProtobuf IDL + Connect、署名付きAuthority Decisionはdeterministic CBOR + CDDL + COSE_Sign1をBaselineとしています。

Reference ImplementationはGoを採用し、External ContractとGo内部型を分離したうえで、M0 executable skeletonへ進む段階です。初回MVPでは、悪意あるClientからTaskを受理し、2台以上のNative Workerへ安全に配置し、reclaim / retry / Result Verificationまでを一続きで確認するvertical sliceを中心にします。

高度なFederation、Browser Worker、TEE、zkVM、learned scheduler等はExtension Pointを確保しつつ、初回MVPの成立条件にはしていません。

文書の正本関係と記述ルールは [AGENTS.md](AGENTS.md)、Architectureの現在地は [architecture/README.md](architecture/README.md)、実装計画は [MVP Implementation Plan](architecture/MVP_IMPLEMENTATION_PLAN.md) を参照してください。

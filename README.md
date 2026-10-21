# EKI Fabric（益）

EKI Fabricは、業務端末、個人のワークステーション、専用サーバ、ブラウザ、クラウドなどに生じる計算の余力を活用する、実験的なOSSプロジェクトです。安全性や利用者への影響、データの所在、ネットワークの負荷、コストを考慮した分散計算基盤を目指しています。

EKIの本質（コア）は、分散処理そのものではなく、委任先ごとに信頼の水準を定め、条件の合う委任先へ範囲と期限を限って仕事を任せ、その結果を確かめて記録する「委任の核」です。委任先は目的に応じて指名するほか、EKIに委ねて、許された余力、管理資源、クラウドを予算の範囲で一元的に最適化させることもできます。広域の資源最適化と分散計算は、この核の上に置く外面の一つで、ほかにAIの推論とツール実行の統治、AIが生成したコードの隔離実行、常駐する軽量な認知処理の実行面を外面とします。詳しくは[Product Value](product/value.md)の「外面とコア」と[ADR-0013](plan/adr/0013-product-core-and-faces.md)を参照してください。

一つの製品や一つの組織だけの計算基盤にはしません。SEI Cognition、TEI、SUIなど既知のOSSに加えて、将来現れる未知のOSS、Webアプリ、他社製品からも共通して使えることを前提にしています。

Architecture Entry Gateを通過し、現在の設計基準（Architecture Baseline）0.1を定めています。NATS、WASM、SPIFFEなどの技術を先に固定せず、守る対象、利用場面、悪意のあるClientやWorkerに対して保証する範囲を要求から導きます。

## まず全体像を知りたい方へ

初めてEKI Fabricを読む場合は、[はじめてEKI Fabricを読む方へ](docs/for-readers.md)から始めてください。

分散システムやRemote Attestationの専門知識がなくても読めるよう、次の背景から説明しています。

- なぜ個人PCや業務端末の余力を使うのか
- なぜ一般的な固定クラスタとは違うのか
- なぜClientもWorkerも無条件には信用しないのか
- 悪意を前提にしながら、なぜ善意のアドホック参加者を受け入れられるのか
- Workerの結果をどのように検証するのか
- Compute Grantや資源提供実績を、EKI自身がどこまで扱うのか

文書全体の案内は[ドキュメント案内](docs/README.md)に、専門用語は[用語集](docs/glossary.md)にまとめています。

## EKI Fabricがめざすもの

出発点の一つは、価格競争力の高い個人PCやワークステーションを、日常業務に十分な性能で用意する考え方です。そして、本人が使っていない時間だけ、その余力を組織全体の計算資源として活用します。

ただし、「空いている資源をできるだけ使い切る」ことは目標にしません。

```text
利用者の本来作業
        ↓ 最優先
端末・ネットワーク・電力の安全余力
        ↓
残った計算余力
        ↓
組織や社会の認知活動へ還流
```

業務端末では、利用者が戻った時点で、計算を縮小または停止できることを重視します。分散するとネットワークの負荷が増える場合は、ローカルで実行することを選べなければなりません。

また、高価な高可用性を、すべての処理に一律には求めません。途中で失敗しても再実行できる処理では、一時的で安価なWorkerを組み合わせます。こうして、Fabric全体として必要な信頼性を得ることをめざします。

## 安全性についての基本姿勢

EKI Fabricでは、ClientもWorkerも、信頼の境界の外に置きます。

認証されていること、社内の端末であること、公式のAgentを使っていること、過去に正しく動いたことだけでは、善意や未改変であることは保証できないからです。

そのため、たとえば次のものを区別します。

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

この考え方は、参加者を排除するためのものではありません。安全性を、参加者の人柄や善意に頼らずに成り立たせます。そのため、公開データを使う検証可能な処理などでは、一時的な個人の参加やブラウザからの参加も受け入れられます。

## 企業内から公益利用まで

EKI Fabricは、まず、企業内の遊休計算資源を安全に使えることを、重要な利用形態とします。そのうえで、同じ基本モデルを、複数の組織の間や公益の用途へ広げられることを想定しています。

たとえば、クラウド事業者や大手企業が、通常の利用を妨げない範囲の余剰の計算能力を、研究機関、OSS、公益性の高い団体へ提供する構成が考えられます。

このような利用枠を、現段階では**Compute Grant**と呼んでいます。

また、資源を提供した事実を、検証できる形で記録します。その記録を、社内の配賦、相互利用の枠、寄付の実績、コミュニティでの評価など、外部の価値交換の制度へ接続する余地を持たせます。

ただし、EKI Core自身が、通貨を発行すること、現金化の手段、市場価格、交換レートを提供することは、目標にしません。どのような価値に対応づけるかは、それぞれの主体に任せます。

## 現在の主要な前提

- 特定の利用OSSやプログラミング言語に依存しない。
- Reference ImplementationにはGoを採用するが、外部との契約はGo専用にしない。
- 業務端末、専用ノード、Browser Worker、他組織の資源、クラウドなどを、同じ性質のWorkerとはみなさない。
- 業務端末では、利用者の本来の作業と、端末側のローカルな安全判断を、中央のSchedulerより優先する。
- ClientもWorkerも悪意を持ち得るものとして設計する。
- Workerの結果は、そのTaskに必要な検証を通るまで、確定した結果とはみなさない。
- Workerの離脱を、異常な出来事としてだけでなく、通常の状態の変化として扱う。
- 計算性能だけでなく、データの局所性や転送量も、配置の判断に含める。
- 公益の利用や組織間の利用を可能にするが、参加しただけで互いに包括的な信頼を与えることはしない。

## 要件のレビューに参加する方へ

要求の正本は[Product Requirements](requirements/PRODUCT_REQUIREMENTS.md)です。

初めて要求の文書を読む場合は、先に[要求文書の読み方](requirements/README.md)を読んでください。そのあと、関心に応じて次の文書へ進めます。

- [Threat Model](requirements/THREAT_MODEL.md): どの攻撃者を想定し、何を守るか
- [Worker Assurance Model](requirements/ASSURANCE_MODEL.md): Workerの保証を、単一のTrust Levelにしない理由
- [Acceptance Scenarios](requirements/ACCEPTANCE_SCENARIOS.md): 実際の利用や攻撃の場面に当てはめて、要求を厳しく確かめる
- [Reference Systems Research](research/topics/reference-systems.md): 既存のシステムや標準から何を学ぶか
- [AI時代の価値と位置づけ](research/topics/ai-era-value-positioning.md)と[既存オーケストレータとの接続](research/integration/orchestrator-integration-and-layering.md): 今後2〜5年を想定した位置づけの検討（調査メモであり、正本ではない）
- [Upstream Plan](plan/UPSTREAM_PLAN.md): Architectureに入るまでに何を確認するか

## 現在地

Architecture Entry Gateを通過し、**Architecture Baseline 0.1**を定義済みです。Baselineは、Authority、Contract、Control-Data Plane、Deployment Profile、Task State Model、Extension SPIで構成されます。2026-10-20に、Authority Boundariesだけを0.2へ上げ、委任先の登録、予算、容量の権限と、管理者権限の委任状を加えました（[ADR-0014](plan/adr/0014-authorities-for-delegation-budget-capacity.md)）。2026-10-21に、Deployment Profilesを0.2へ上げ、一台で完結する配備形態（DP-7）を加えました（[ADR-0018](plan/adr/0018-single-machine-delegation-profile.md)）。

External Client Planeには、HTTPS、JSON、OpenAPIを採用しました。P0の機械可読な契約は、[`spec/external/openapi.json`](spec/external/openapi.json)に置いています。Worker PlaneとAdapter Planeは、Protobuf IDLとConnectを使います。署名付きのAuthority Decisionは、deterministic CBOR、CDDL、COSE_Sign1をBaselineとしています。

Reference ImplementationにはGoを採用しました。External ContractとGo内部の型を分けたうえで、M0のexecutable skeletonに進む段階です。初回のMVPでは、悪意のあるClientからTaskを受理し、2台以上のNative Workerへ安全に配置し、reclaim、retry、Resultの検証までを一続きで確認する、vertical sliceを中心にします。

高度なFederation、Browser Worker、TEE、zkVM、learned schedulerなどは、Extension Pointを確保します。ただし、初回のMVPが成り立つための条件には含めません。

文書の正本の関係と書き方のルールは[AGENTS.md](AGENTS.md)に、Architectureの現在地は[architecture/README.md](architecture/README.md)に、実装計画は[MVP Implementation Plan](architecture/MVP_IMPLEMENTATION_PLAN.md)にあります。

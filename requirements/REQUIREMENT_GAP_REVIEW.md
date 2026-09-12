# EKI Fabric 要求ギャップレビュー — 第二巡目

## 役割

この文書は、初版のProduct Requirements、Threat Model、Worker Assurance Model、Acceptance Scenariosを横断し、Architectureへ進む前に不足している要求を洗い出すためのレビュー記録です。

新しいRequirementの正本ではありません。ここで「追加が必要」と判断した項目は、`PRODUCT_REQUIREMENTS.md`や`THREAT_MODEL.md`へ反映して初めて正式な要求になります。

## 今回の見方

初版では、次の大きな境界はすでにRequirement化されています。

- 特定の利用OSSや言語へ依存しない
- 業務端末では利用者を優先する
- ネットワーク負荷とデータ局所性を考慮する
- ClientもWorkerも悪意を持ち得る
- Workerの結果を検証前に正しいとみなさない
- Sybilを単純多数決で解決したことにしない
- アドホック参加と機密Taskを両立させる
- Compute GrantとContributionを通貨・市場から分離する

第二巡目では、「この原則を実際の境界条件へ当てはめたとき、暗黙の前提が残っていないか」を確認します。

---

## GAP-01 — Browser参加者の意思がRequirementとして弱い

### 問題

RQ-005〜RQ-007では業務端末の利用者を優先し、RQ-032ではアドホック参加を許容しています。しかし、Browser Workerが本人の明示的な意思なしに起動されないことは、独立した要求として明記されていません。

Webページは通常のコンテンツ閲覧と計算資源提供を同じ実行環境で行えます。そのため、技術的に実行可能であることと、利用者が資源提供へ同意していることを分ける必要があります。

### 結論

**新しいProduct Requirementを追加する。**

要求する方向:

- Browserや個人端末によるアドホック資源提供は、利用者に認識可能であること
- 停止方法が明確であること
- hidden iframe等による意図しない資源提供を標準利用形態にしないこと
- Provider側Policyが許していても、利用者側の停止権を不要にしないこと

関連: RQ-005〜RQ-007, RQ-032

---

## GAP-02 — Browser Workerの可用性をNative Workerと同一視しない要求が弱い

### 問題

RQ-010で随時離脱を正常系として扱うことは要求していますが、Browserにはbackground throttling、tab close、browser/OSによるsuspension等、EKIが制御できない実行制約があります。

Browser実装の細部をProduct Requirementへ固定する必要はありません。しかし、Browser Workerを長時間常駐サービスと同じ可用性で扱わないことは明文化した方が安全です。

### 結論

**既存RQ-010を補強する。**

Browser固有の実装方式を要求するのではなく、「Worker profileごとに期待できるlifecycleを区別し、保証されないbackground executionをdeadlineや可用性の根拠にしない」とする。

関連調査: `research/browser-worker-lifecycle.md`

---

## GAP-03 — 悪意あるWorkerへ渡したデータの削除を保証できない

### 問題

RQ-024は、平文機密データを悪意Workerへ渡した後から機密性を回復できないことを明示しています。

同じ理由で、Task終了時にWorkerが「入力を削除した」と報告しても、通常のuntrusted Workerについてその削除を一般には確認できません。

ここを曖昧にすると、`retention = none`のような指定を付けただけで、実際の情報漏えい耐性が得られたと誤解する可能性があります。

### 結論

**新しいProduct Requirementを追加する。**

- untrusted Workerの事後削除を機密性の根拠にしない
- 削除保証が必要なTaskは、管理境界、Confidential Computing、暗号学的に利用不能化できる設計等、別の信頼根拠を必要とする
- retention policyは運用上の要求と、技術的に検証できる保証を区別する

---

## GAP-04 — Validator / Verifier自身が攻撃者になる場合の境界が弱い

### 問題

現在はWorkerが返す結果をValidator、replication、attestation、proof等で検証する方針です。

しかし、Validatorそのものが改変されていれば、偽ResultをAcceptedResultへ昇格できます。Remote Attestationでも、VerifierがEvidenceを誤って評価すれば同様です。

IETF RATSでもAttester、Verifier、Relying Partyを役割として分離し、Evidenceの評価Policyを明示的な構成要素として扱っています。

### 結論

**新しいProduct RequirementとThreat Actorを追加する。**

- Resultを受理する際、どのVerifier / Validator / Policy authorityを信頼基盤にしているか追跡できること
- Worker自身が自分のValidatorを自由に選んで保証を成立させないこと
- 高保証Taskでは、一つの低保証Validatorだけを無条件なroot of trustにしない構成を選べる余地を持つこと

すべてのValidatorをByzantine tolerantにすることまでは要求しない。どこを信頼基盤に置いたかを隠さないことを要求する。

---

## GAP-05 — Remote AttestationのEvidence自体が情報漏えい面になり得る

### 問題

Remote AttestationはWorkerの状態を確認するために有用ですが、Evidenceにはhardware、software version、device構成など、内部情報が含まれる可能性があります。

Topology confidentialityを守りながら、必要以上のAttestation情報をClientへ開示しない境界が必要です。

### 結論

**RQ-040 Audit without unnecessary disclosureを補強し、Assurance Evidenceにも適用する。**

Clientが必要とするのは通常、「このTaskの必要条件を満たしたか」という判断結果であり、生のEvidence全部ではありません。

---

## GAP-06 — 非決定的AI処理について「検証不能なら保証を下げる」がRequirement本文で弱い

### 問題

Threat ModelとAssurance Modelには、非決定的AI出力をbitwise quorumで保証しないことが書かれています。しかしProduct RequirementsではRQ-021の「Task特性に応じた検証方式」の中に埋もれています。

将来SEI Cognition等が主要利用者になる場合、ここは重要な境界です。

### 結論

**新しいProduct Requirementを追加する。**

- 「正しい答え」を機械的に判定できないTaskについて、EKIが完全なsemantic correctnessを偽ってはならない
- validation可能な性質、provenance、複数候補、独立実行など、確認できた事実だけを返せること
- 利用アプリケーションが最終判断を担うTaskを表現できること

---

## GAP-07 — 再実行と外部副作用の関係が弱い

### 問題

RQ-011、RQ-012はretryabilityやexactly-once非保証を定義しています。しかし、Taskが外部API呼び出し、メール送信、DB更新などの副作用を持つ場合、Worker消失後の自動再実行が二重実行を起こす可能性があります。

EKIは高throughput計算を主対象にしていますが、未知のOSSから利用される以上、副作用Taskが投入される可能性を無視できません。

### 結論

**新しいProduct Requirementを追加する。**

- 外部副作用を持つTaskは、その事実と再実行条件を明示できること
- idempotency等の安全根拠がないTaskを、EKIが勝手にretryableと判断しないこと
- 実行済みか不明な状態を、必ず再実行するだけで処理しないこと

---

## GAP-08 — Workerの能力自己申告を安全上の配置根拠へ使う境界が弱い

### 問題

Assurance ModelではResource Measurementの自己申告を信用しないことを明示しています。一方、Workerが「Confidential Runtimeがある」「特定GPUである」「このExecutorを隔離できる」と偽る場合について、Product Requirementの配置条件はやや間接的です。

### 結論

**RQ-019 / RQ-024 / RQ-025を補強する。**

性能最適化のための自己申告と、機密性や結果保証のためのcapability assertionを区別する。高保証配置に使うcapabilityは、必要に応じて外部Evidenceまたは管理Policyによる根拠を持つことを要求する。

---

## GAP-09 — 古いEvidence、Lease、WorkOrder等のreplay境界をSecurity Invariantへ上げたい

### 問題

Network attackerとしてreplayは想定済みで、Security Eventとしても記録対象です。しかし、「一度正しかった権限やEvidenceを期限後に再利用して権限を復活させてはならない」という不変条件が独立していません。

Remote AttestationでもEvidenceのfreshnessは重要な要素です。

### 結論

**Security Invariantを追加する。**

- expiry、revocation、freshnessを持つauthority / evidenceについて、古い情報のreplayで失効済み権限を復活させない
- 具体的なnonce、timestamp、counter方式はArchitectureで決める

---

## GAP-10 — Contributionの「測定事実」と「価値」をさらに明確に分ける

### 問題

RQ-036〜RQ-038はすでにかなり明確です。ただしContributionが将来外部Creditへ接続されると、「EKIが記録した=価値がある」と誤読される可能性があります。

たとえば自作自演Taskでも、計算を実行したという事実自体は正しいかもしれません。しかし、それを公益貢献や相互Creditとして評価するかは別問題です。

### 結論

**新Requirement追加ではなく、RQ-036 / RQ-037の理由と境界を補強する。**

Contribution Recordは観測・検証できた事実とassuranceを記録し、価値判断は外部Policyへ委ねる。

---

# 第二巡目で追加・補強する項目

Product Requirementsへ次を反映する。

1. Browser / ad-hoc参加の明示性と停止権
2. untrusted Workerに対する事後削除非保証
3. Verification authorityの明示
4. 非決定的・意味的に検証困難なTaskの保証上限
5. 外部副作用を持つTaskの再実行安全性
6. Worker capability claimとsecurity placementの根拠分離
7. Contribution Recordは価値そのものではないことの補強

Threat Modelへ次を反映する。

1. malicious / compromised Validator・Verifier
2. stale Evidence / replayによる権限復活
3. Browserでの意図しない資源利用

Acceptance Scenarioへ次を追加または補強する。

1. Browser Workerのtab suspensionと明示的参加
2. Workerが入力削除を偽るケース
3. compromised Validatorが偽Resultを受理するケース
4. side-effect Taskの実行済み状態が不明なケース
5. non-deterministic AI結果を最終判断へ直接昇格させないケース

## Architectureへ持ち越すもの

次は重要ですが、現段階でProduct Requirementへ方式を固定しません。

- Browser Workerの具体的なCPU/GPU利用制限方法
- Validatorを複数化する具体方式
- Attestation EvidenceのformatとVerifier構成
- replay防止のnonce / counter / time source
- side-effect Taskのidempotency key format
- secure deletionやcryptographic erasureの具体方式

これらはRequirementを満たすためのArchitecture候補として後段で比較します。

# 候補Scenario PAS-CAND-07〜16 の評価

## 役割

[Acceptance Scenarios](../../requirements/ACCEPTANCE_SCENARIOS.md)に登録した候補Scenario 10件を、既存のRequirement・Assurance Model・Architecture Baselineで扱えるかという観点で評価した記録です。

この記録の未決事項と次の手順は、GitHub Issue [#120](https://github.com/hat47x/eki-fabric/issues/120)で追っています。

AGENTS.mdの方針どおり、Requirementの数を増やすこと自体は目的にしません。既存の要求で扱える候補は、そのまま扱えると結論します。本質的な不足がある点だけを、Requirementへ戻します。

## 結論

| 区分 | 候補 | 結論 |
|---|---|---|
| 既存要求で扱える | 10、11、13、14、15 | 追加のRequirementは不要。Architecture側のAcceptance観点として維持する |
| 既存要求の記述を明確化した | 07、08、09 | 本評価でRequirementの文言を補った（下記） |
| 新しいRequirement候補 | 12 | RQ-054の草案を置く。レビューまで正本には入れない |
| 要求ではなく設計・運用の決定が要る | 16 | Open Questionとして残す |

53 Requirementの総数は、この評価では変えていません。

## 個別の評価

### PAS-CAND-07 — AI Agentの連鎖による依頼の増殖

- 既存の根拠：RQ-015、RQ-016、RQ-017、SEC-007（retryとfan-outは有限の予算を消費する）、SEC-023。
- 不足：RQ-017はfan-outとretry amplificationを扱うが、「認証済みの自動主体が、別の主体へ委譲して連鎖させる」場合に、予算と権限が縮小することを明示していなかった。
- 措置：[RQ-017](../../requirements/PRODUCT_REQUIREMENTS.md)に、委譲の連鎖でも同じ制限が及び、委譲された側が委譲した側を超えないことを追記した。新しいRequirementは作らない。
- 残る課題：委譲の連鎖を、Client Authorityのどの構造（予算の継承、権限の縮小）で表すかはArchitecture側の決定。Task Admission（ADM-2）の設計で扱う。

### PAS-CAND-08 — 相関した確認の一致

- 既存の根拠：RQ-022、RQ-045、Assurance Model Axis E（独立性のdimension）。
- 不足：Axis Eは、principal、operator、Executor implementationなどを挙げていたが、生成AIで重要になる**モデルの系統**を含んでいなかった。また、独立性の喪失を攻撃者に限定して読める記述だった。
- 措置：RQ-022に、悪意のない相関でも独立性が失われることを追記した。Axis Eへ`model lineage`と`resource provider`のdimensionを加え、判断に使える記録を残すことを求める記述を足した。モデル系譜の意味判断はEKIが所有しない点（RQ-045）は変えない。
- 残る課題：どのdimensionをResultのprovenanceへ必須で入れるかは、Result Verification Contractの設計で決める。

### PAS-CAND-09 — 端末上のローカルAIとの資源の競合

- 既存の根拠：RQ-005〜007、RQ-053。
- 不足：RQ-005の「利用者の本来の作業」が、利用者の操作だけを指すと読めた。利用者のために自律的に動く処理が使う資源は、操作の有無から判定できない。
- 措置：RQ-005に、利用者のために動く処理を本来の作業に含めることと、把握できない場合は安全側に倒すことを追記した。
- 残る課題：Local Governorが、端末上の他のAI処理の資源利用をどこまで観測できるか（OS・NPUドライバの観測手段）は、実装前のSpike候補。観測できないときの既定値は「背景計算を控える」とする。

### PAS-CAND-10 — AIが生成したコードの実行

- 既存の根拠：RQ-024、RQ-026、RQ-027、RQ-029、RQ-039、RQ-050。
- 評価：生成コードは作者の分からない外部入力と同じであり、既存の要求（機密を信頼できないWorkerへ置かない、実行体のdigestの固定、任意のfetchの禁止、Artifactの展開上限）で扱える。追加は不要。
- Architecture側の観点：Executor Runtimeがsandboxとnetwork profileを強制できること。

### PAS-CAND-11 — 配置の証跡を第三者へ示す

- 既存の根拠：RQ-018、RQ-028、RQ-039、RQ-040、RQ-044。
- 評価：RQ-040（必要以上に開示しない監査）で扱える。EKIの記録が法令適合を保証しないことは、候補の受け入れ条件に書いてあり、Requirementとして重ねる必要はない。
- 備考：証跡をどの形式で第三者へ提示するか（署名付きAuthority Decisionの参照など）は、Architecture側の設計項目。

### PAS-CAND-12 — 下位オーケストレータの見えない再試行

- 既存の根拠：RQ-012、RQ-046。
- 不足：RQ-046は、副作用を持つTaskの再実行条件を明示することを求めるが、**EKIが配置した先の資源提供基盤（管理クラスタ等）が、EKIの知らない間に内部で再実行する**場合を想定していない。EKIのAttemptは一度の試行（Task State Model §2.1）という前提が、この境界で崩れる。
- 措置：新しいRequirementの草案（RQ-054）を下に置く。レビュー前のため、正本（PRODUCT_REQUIREMENTS.md）には入れていない。

### PAS-CAND-13 — 下位オーケストレータによる一斉の横取り

- 既存の根拠：RQ-010、RQ-013、RQ-035、RQ-052、SEC-023。
- 評価：下位による横取りも、EKIから見れば資源の回収（reclaim）の一種であり、RQ-052が扱う。ただし、「reclaim」が端末利用者やProviderの主導に限られないことを、Architecture側で明示する必要がある。
- Architecture側の観点：Resource Provider Adapterは、下位の横取りをreclaimとして通知し、Lease Authority（LSE-1）が扱えること。

### PAS-CAND-14 — 資源提供者へ接続する資格情報の漏えい

- 既存の根拠：RQ-041、RQ-048、RQ-051、SEC-023。Extension SPIでは、資源へアクセスする拡張をEP-2に分類している。
- 評価：RQ-051（Control Plane内部の権限分離）とEP-2のscoped Credentialで扱える。追加は不要。

### PAS-CAND-15 — 下位のエラーからの内部構成の漏えい

- 既存の根拠：RQ-018、RQ-040。
- 評価：RQ-018（通常のClient権限から内部構成を列挙できない）で扱える。追加は不要。
- Architecture側の観点：Adapterが、下位のエラーをClient向けの分類とAudit向けの詳細へ分けること。

### PAS-CAND-16 — 統治基盤を名乗るClientからの方針の注入

- 既存の根拠：RQ-015、RQ-016（予算拡張、security classificationの低下をClientの記述だけで成立させない）、RQ-051、ADM-1とADM-2の分離（[Authority Boundaries](../../architecture/AUTHORITY_BOUNDARIES.md)）。
- 評価：方針の注入そのものは、RQ-016で禁じられている。残る問いは、**一つの製品が、利用者（Client）と管理者（Administrative）の両方の権限を持つ構成を許すか**であり、これはRequirementではなく、Deployment ProfileとAuthority設計の決定事項である。
- 措置：Open Questionとして残す（下記）。

## RQ-054 の草案 — Delegated execution transparency（レビュー待ち）

この草案は、PRODUCT_REQUIREMENTS.mdへ反映して初めて正式な要求になります。

> ### RQ-054 — Delegated execution transparency — Needs research
>
> EKIが配置したTaskを、管理クラスタ等の別の実行管理系（以下、委譲先）が内部で再実行・再配置・横取りする場合、その挙動をTaskの信頼性契約から外れないように扱えなければならない。
>
> 副作用を持つTask、または再実行の安全根拠がないTaskは、委譲先の自動再実行を無効にできる場合にだけ配置できる。無効にできない委譲先へは配置しない。委譲先が内部で再実行したことを観測できる場合は、その事実を記録する。観測できない場合は、実行回数が一度だけだったと主張しない。
>
> 委譲先による横取りは、利用者やProviderによる資源の回収と同じく、失敗ではなくreclaimとして扱う。
>
> 委譲先が「成功」と報告したことは、Workerの報告と同じくClaimである（RQ-019）。委譲先が管理されていることは、保証水準を上げる根拠の一つであり、Resultの検証を免除しない。
>
> **理由**：既存のオーケストレータは自動再試行・再配置・先取りを既定で持つことがあり、EKIのAttempt（一度の試行）という前提と、見えないところで二重になるため。
>
> **成立条件**：委譲先の再実行設定を、Adapterが宣言または制御できること。
>
> **非目標**：委譲先の内部スケジューリングの最適化、委譲先の機能の再実装、委譲先の管理者への信頼の強制。

### 草案の論点

- 名称の衝突：本書の「委譲先」は、Compute Grantの「Provider」（資源を提供する主体）と別の概念です。同じ「Provider」を使うと混同するため、用語を整理する必要がある。調査メモ（[既存オーケストレータとの接続](../../research/integration/orchestrator-integration-and-layering.md)）の「Resource Provider」も同様に見直す。
- ステータス：「Needs research」とする。実際の下位オーケストレータの既定の挙動を確認し、一つのAdapter契約で制御できるかを、Spikeで確かめる前に、Baselineへ昇格させない。

## Open Question

1. 「委譲先」を表す用語。Compute GrantのProviderと区別できる名称を決める。
2. 一つの製品のコンポーネントが、Client権限と管理者権限の両方を持つ構成を許すか（PAS-CAND-16）。許す場合、両者をどう分けて記録するか。
3. 委譲の連鎖における予算の継承を、Task Admissionのどの構造で表すか（PAS-CAND-07）。
4. Local Governorが、端末上の他のAI処理の資源利用を観測できる範囲（PAS-CAND-09）。
5. Result provenanceへ入れるべき独立性のdimensionの最小集合（PAS-CAND-08）。

## 次の手順

1. RQ-054の草案をレビューし、採用するなら、PRODUCT_REQUIREMENTS.md、THREAT_MODEL.md（SEC対応）、REQUIREMENT_GAP_REVIEW.mdへ反映する。その際、「53 Requirement」と書かれている箇所（UPSTREAM_PLAN.md、README等）も更新する。
2. 候補10件のうち、評価を終えたものを、PAS番号へ昇格するかを判断する。昇格は、Acceptance Scenariosの方針どおりレビューを経て行う。
3. Open Questionのうち、実装前に確認が要るもの（4）は、M0のvertical sliceの成立後にSpikeとして扱う。M0の優先順位（[Upstream Plan](../UPSTREAM_PLAN.md)のmainline priority guard）は変えない。

## 関連文書

- [Acceptance Scenarios](../../requirements/ACCEPTANCE_SCENARIOS.md)
- [Product Requirements](../../requirements/PRODUCT_REQUIREMENTS.md)
- [Worker Assurance Model](../../requirements/ASSURANCE_MODEL.md)
- [AI時代の価値と位置づけ](../../research/topics/ai-era-value-positioning.md)
- [既存オーケストレータとの接続、およびSEI・TEIとの層構造](../../research/integration/orchestrator-integration-and-layering.md)

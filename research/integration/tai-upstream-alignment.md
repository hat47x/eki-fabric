# TAI側の要求とEKIの三視点対応（上流の突き合わせ）

EKI Fabricは、TAI Pleromaから見て「範囲や権限を定めた処理の委任と、その結果の確認」を担う基盤です（`tai-pleroma/README.md`）。この文書は、TAI側の要求を**最上流の入力**として、EKIの三要素（データ・機能・業務）に引き直し、下流のIssueをどこへ置くかを決めるための突き合わせです。

`research/integration/`の文書であり、要求や設計の正本ではありません。正本は`requirements/`、`architecture/`、`plan/adr/`です。この文書は、それらをTAI側の要求から読み直すための材料です。

## 1. TAI側がEKIに求めていること（出典つき）

| # | TAI側の要求 | 出典 |
| --- | --- | --- |
| T1 | **受付は実行の完了ではない。** `queued`はWorkerによる実行完了を意味しない | `tai-pleroma/README.md`（「特に、EKIでの`queued`はWorkerによる実行完了を意味しません」）、`docs/yi-constellation/05-要件候補.md` RQ-YI-008 |
| T2 | **委任の実行・結果・委任完了を確認できること。** 現状のEKIはCLI起動・Control API認証・合成Taskの`queued`受理・cancel・result API 501・冪等再送までで、実行と結果は未検証 | `tai-pleroma/docs/critical-path-2026-10.md`、`integrations/capability-inventory.md` |
| T3 | **EKIをadmission/cancelから次の段階へ進める。** 実行結果は実際に実行した範囲だけを記録する | `tai-pleroma/evaluations/SCN-002-003-owner-decision-2026-10-30.md` |
| T4 | **改過（結果の検証で誤りを改めるループ）** を、価値として測れる形にする | `docs/yi-constellation/05-要件候補.md` RQ-YI-013 |
| T5 | **反哺の記録（提供と受益の対応）。** 一方的に取り、返さない状態を検出できるようにする | 同上 RQ-YI-014、`docs/yi-constellation/04-価値モデル.md`（受益の偏り） |
| T6 | **損の設計（停止・取消・上限の効果）** を、安全の制約ではなく価値として測る | 同上 RQ-YI-015 |

## 2. 三視点への引き直し

TAI側の要求を、EKIの三要素（データ・機能・業務）に引き直します。決める順序は**業務→機能→データ**です（ADR-0033）。

**業務（誰が何を決めるか）。** TAIの利用者は「この仕事を委任してよいか」「結果を受け取ってよいか」を決めます。EKIの業務は、その判断を代行せず、判断の材料（状態・結果・証拠）を返すことです。T1・T2・T3は、この業務の判断が成立するための前提です。T4〜T6は、委任の価値（改過・反哺・損）を測るための業務の観点です。

**機能（何をするか）。** 受理（受付）、配置、実行、結果の受理（検証と確定）、回復、失効の伝播、公開状態の投影。T1は「受理」と「結果の受理」を混同しないこと、T2は「実行」と「結果の受理」を実際に動かすこと、T3はその範囲を正直に記録することに対応します。

**データ（何を残すか）。** Task（phase）、Attempt、Lease、WorkOrder、ResultClaim、AcceptedResult、Artifact、Contributionの記録、公開状態。T1はTaskのphaseと公開状態（`queued`と終端）に、T4はAcceptedResultと検証の記録に、T5はContributionの記録（提供・受益）に、T6は取消・回収の記録に対応します。

## 3. EKIの現状との差（gap）

| TAI側 | EKIの現状 | 差 |
| --- | --- | --- |
| T1 受付≠完了 | 公開状態は`queued`で受付、終端は`terminal_code`で区別（#281で契約化）。`admitted`と`ready`はどちらも`queued`（#249） | 「実行中」「検証中」が公開状態に現れるかが未決（#249）。完了の境界は未整備（#365） |
| T2 実行・結果・委任完了 | 実行と結果受理の経路は実装済み（`conformance/process`、`internal/control/resultacceptance`）。ただし`active`→`verifying`の配線が無く、`verifying`は現実に現れない（#381） | 状態の遷移が設計と一致していない |
| T3 実行した範囲だけを記録 | AcceptedResultは受理済みの結果だけを持つ。実行しなかったTaskは`terminal_code`で区別（#281） | 実行の有無と結果の有無を、依頼元が照合する手段が弱い（#315） |
| T4 改過 | RQ-019（結果は検証までclaimしない）、RQ-021、RQ-044（検証の権限）が素材 | 「改められたこと」を測る観点が無い（TAI側で測る想定） |
| T5 反哺 | RQ-036（Contribution accounting）、RQ-037 | 受益者の分布を測る観点が中心に無い（TAI 04-価値モデルが指摘） |
| T6 損 | RQ-005、RQ-006、RQ-009、RQ-035（停止・取消・上限） | 停止・取消の効果を測る観点が無い |

## 4. 下流Issueの置き場所と順序

三視点とTAIの要求から、下流のIssueは次の順に置きます。

1. **業務の前提を満たす（T1・T2・T3）**: #365（完了の境界）、#381（`active`→`verifying`の配線）、#315（依頼元の照合）、#249（公開状態の区別）。ここが済むと、TAIは「受付」と「完了」を取り違えずに委任を試せる。
2. **機能を動かす（T2・T3）**: 実行・結果受理・公開状態の経路を、実プロセスで通す（`conformance/process`の拡張）。TAIのowner decision（T3）が求める「実際に実行した範囲だけを記録する」は、ここで確かめる。
3. **データを増やす（T4・T5・T6）**: 改過・反哺・損の記録。いずれも新しいデータ（検証の履歴、提供と受益の対応、停止・取消の効果）を要するため、業務の判断（何を測るか）を先に決める。EKI側の要求（RQ-036、RQ-037、RQ-005/006/009/035）を拡張するか、測り方をTAI側に置くかを決める。

## 5. 約束しないこと

- この文書は、TAIとの通し試験を実施した記録ではありません。TAI側のreceiptが示すのは、CLI起動・Control API認証・`queued`受理・cancel・冪等再送までです（`integrations/capability-inventory.md`）。
- EKIの公開状態が`queued`であることは、Workerが実行を始めたことを意味しません。この区別は#249と#365で扱います。
- 受益の分布（T5）と停止の効果（T6）は、EKI単体では測れません。TAI側の記録と合わせて測る前提であり、EKI側に新しい中央集計を作りません。

## 6. 未決

- T5・T6の測り方を、EKIの要求（RQ-036、RQ-037、RQ-005/006/009/035）の拡張として書くか、TAI側の評価に置くか。
- T2の「委任完了」を、EKIのどの状態（`terminal/completed`か、AcceptedResultの受理か）で表すか。#365の決定と合わせる。
- TAI側のreceiptの版（`integrations/capability-inventory.md`が固定する`84f03d1`など）と、EKIの現在の`main`の差を、どこで記録するか。

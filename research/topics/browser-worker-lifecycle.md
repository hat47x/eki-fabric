# Browser WorkerのライフサイクルとEKIへの影響

## 役割

この文書は、Browserを一時的なWorkerとして利用する場合に、どのような制約を前提にすべきかを整理する、調査メモです。

Product RequirementやArchitectureの正本ではありません。Browser Workerを「Native Workerの軽量版」とみなしてよいかを検討するための、材料を残します。

## 結論

Browser Workerに、専用クライアントと同じ可用性を期待することはできません。

ブラウザは、利用者の端末を守るために、非表示のタブやバックグラウンドのページの処理を抑制します。さらに、タブの終了、ブラウザの終了、OSによるプロセスの停止など、EKI側から制御できない理由で、Workerが消えることがあります。

そのため、少なくとも初期段階では、Browser Workerを次のような**一時的なWorker**として扱うのが妥当です。

- 離脱しても再実行できるTaskを優先する。
- 長時間の占有を前提にしない。
- 厳しいdeadlineを持つTaskの、唯一の実行先にしない。
- Browserが閉じたことを、障害ではなく、通常の離脱として扱う。
- バックグラウンドでの実行時間やtimerの精度を、保証として使わない。
- 高い機密性が必要なTaskを、Browserだからという理由で許可しない。

## 背景: Browserはバックグラウンドの処理を抑制する

MDNのPage Visibility APIと`setTimeout()`の説明によると、多くのブラウザは、バックグラウンドや非表示のタブでtimerの遅延を増やし、実行頻度を下げます。

Chromeでは、タブの状態に応じてtimerの実行頻度が段階的に下がり、条件によっては実行間隔が大きく延びます。Firefoxでも、非アクティブなタブには最低待ち時間が設けられています。Androidでは、タブが完全にアンロードされる場合もあります。

参考資料:

- MDN, Page Visibility API  
  https://developer.mozilla.org/en-US/docs/Web/API/Page_Visibility_API
- MDN, Window.setTimeout — Timeouts in inactive tabs  
  https://developer.mozilla.org/en-US/docs/Web/API/Window/setTimeout

これは、「Web Workerなら、必ず同じ制限を受ける」という単純な話ではありません。重要なのは、Browser上のcodeが、OSのserviceのような常駐性や、実行の期限を持たないということです。

EKIのRequirementでは、ブラウザの実装の細部に依存するのではなく、Browser Workerの可用性を低く見積もります。そして、停止や遅延を、正常な場合として吸収できることを求める方が、安全です。

## `requestIdleCallback()`は、分散計算の予約枠ではない

Background Tasks APIでは、idle callbackを短い空き時間に使い、時間の読めない長時間処理には使わないよう案内しています。

これは、EKIにとって重要です。

Browserが「いまはidleである」と通知しても、数分後までCPU時間が保証されたわけではありません。EKIのTask Leaseやcheckpointの設計を、Browserのidle callbackに直接対応させるべきではありません。

参考資料:

- MDN, Background Tasks API  
  https://developer.mozilla.org/en-US/docs/Web/API/Background_Tasks_API

## Browser Workerでは、参加者への説明が特に重要

専用のAgentであれば、OSのタスクトレイや設定画面などから、資源を提供している間は、そのことを継続的に表示できます。

一方、Browser Workerは、通常のWebページと同じ実行環境にあります。利用者が意図しないまま、CPUやGPUを消費する実装は、技術的には可能です。しかし、EKIのhuman-firstの原則とは、整合しません。

そのため、Browser Workerを利用する場合は、少なくとも次の点を上流の要件として検討します。

- 資源の提供を開始することが、利用者にとって明確である。
- 停止の方法が、分かりやすい。
- 提供している量や実行の状態を、確認できる。
- ページを閉じれば提供が止まることを、妨げない。
- hidden iframeなどを使った、意図しない参加を、標準の動作にしない。

これは、Browser固有のUIの要件だけではありません。EKIが「善意の参加者の資源を、本人の意思を越えて取り込まない」ための、Product Principleに関わります。

## Browserの自己申告は、保証にならない

Browser Workerが、次のように申告しても、それだけでは、SecurityやContributionの根拠にできません。

- 使えるCPUの数
- GPUの性能
- 実行時間
- タブがforegroundだった時間
- 実際に使った計算量

Browser側のJavaScriptは、参加者自身や拡張機能などによって、改変される可能性があります。

したがって、Browser Workerも、他のWorkerと同じように、自己申告の値をClaimとして扱います。必要な保証の水準に応じて、別のEvidenceや観測を使う必要があります。

## Requirementへの示唆

この調査から、少なくとも次のことを、明文化する価値があります。

1. Browser Workerは、一時的で、随時離脱できるWorkerとして扱う。
2. Browser Workerの参加は、利用者に対して明示的でなければならない。
3. Browserのバックグラウンドでの実行時間を、deadlineの保証やContributionの測定の、信頼の根拠にしない。
4. Browser固有の制約を、他のWorkerに無理に一般化しない。
5. Browserで実行できることと、機密のTaskを実行してよいこととを、分ける。

これらをProduct Requirementに追加するか、既存のRQ-003、RQ-005〜RQ-010、RQ-032を補強するかは、第二巡目のRequirement Gap Reviewで判断します。

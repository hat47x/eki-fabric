# Browser WorkerのライフサイクルとEKIへの影響

## 役割

この文書は、Browserを一時的なWorkerとして利用する場合に、どのような制約を前提にすべきかを整理する調査メモです。

Product RequirementやArchitectureの正本ではありません。Browser Workerを「Native Workerの軽量版」とみなしてよいかを検討するための材料を残します。

## 結論

Browser Workerは、専用クライアントと同じ可用性を期待するべきではありません。

ブラウザは利用者の端末を守るため、非表示タブやバックグラウンドページの処理を抑制します。さらに、タブの終了、ブラウザの終了、OSによるプロセス停止など、EKI側から制御できない理由でWorkerが消えることがあります。

したがってBrowser Workerは、少なくとも初期段階では次の性質を持つ**一時的なWorker**として扱うのが妥当です。

- 離脱しても再実行できるTaskを優先する
- 長時間の占有を前提にしない
- 厳しいdeadlineの唯一の実行先にしない
- Browserが閉じたことを障害ではなく通常の離脱として扱う
- バックグラウンド実行時間やtimer精度を保証として使わない
- 高い機密性が必要なTaskを、Browserというだけで許可しない

## 背景: Browserはバックグラウンド処理を抑制する

MDNのPage Visibility APIおよび`setTimeout()`の説明では、多くのブラウザがバックグラウンドまたは非表示のタブについて、timerの遅延や実行頻度の抑制を行うことが説明されています。

Chromeではタブの状態に応じた段階的なtimer throttlingがあり、条件によってはtimer確認が大きく間引かれます。Firefoxでも非アクティブなタブに最低待ち時間があり、Androidではタブを完全にアンロードする可能性も説明されています。

参考は次のとおりです。

- MDN, Page Visibility API  
  https://developer.mozilla.org/en-US/docs/Web/API/Page_Visibility_API
- MDN, Window.setTimeout — Timeouts in inactive tabs  
  https://developer.mozilla.org/en-US/docs/Web/API/Window/setTimeout

これらは「Web Workerなら必ず同じ制限を受ける」という単純な話ではありません。重要なのは、Browser上のコードがOSサービスのような常駐性や実行期限を持たないということです。

EKIのRequirementでは、ブラウザ実装の細部へ依存するのではなく、Browser Workerの可用性を低く見積もり、停止や遅延を正常系として吸収できることを求める方が安全です。

## `requestIdleCallback()`は分散計算の予約枠ではない

Background Tasks APIの説明では、idle callbackは短い空き時間を利用するための仕組みであり、予測できない長時間処理を避けることが推奨されています。

これはEKIにとって重要です。

Browserが「今はidleである」と通知したからといって、数分後までCPU時間が保証されたわけではありません。EKIのTask Leaseやcheckpoint設計をBrowserのidle callbackへ直接対応付けるべきではありません。

参考は次のとおりです。

- MDN, Background Tasks API  
  https://developer.mozilla.org/en-US/docs/Web/API/Background_Tasks_API

## Browser Workerでは参加者への説明が特に重要

専用Agentであれば、OSのタスクトレイや設定画面などから、資源提供中であることを継続的に表示できます。

一方、Browser Workerは通常のWebページと同じ実行環境にあります。利用者が意図しないままCPU/GPUを消費する実装は、技術的に可能であってもEKIのhuman-first原則と整合しません。

そのため、Browser Workerを利用する場合は、少なくとも次を上流要件として検討する必要があります。

- 資源提供を開始することが利用者に明確である
- 停止方法が分かりやすい
- 提供量や実行状態を確認できる
- ページを閉じれば提供が停止することを妨げない
- hidden iframe等を使った意図しない参加を標準動作にしない

これはBrowser固有のUI要件だけではなく、EKIが「善意の参加者の資源を、本人の意思を越えて取り込まない」ためのProduct Principleに関わります。

## Browserの自己申告は保証にならない

Browser Workerが次のように申告しても、それだけでSecurityやContributionの根拠にはできません。

- 使用可能なCPU数
- GPU性能
- 実行時間
- タブがforegroundだった時間
- 実際に使用した計算量

Browser側JavaScriptは参加者自身や拡張機能等によって改変される可能性があります。

したがって、Browser Workerも他のWorkerと同様に、自己申告値はClaimとして扱い、必要な保証水準に応じて別のEvidenceや観測を用いる必要があります。

## Requirementへの示唆

この調査から、少なくとも次を明文化する価値があります。

1. Browser Workerは一時的で、随時離脱できるWorkerとして扱う。
2. Browser Workerの参加は利用者に対して明示的でなければならない。
3. Browserのbackground実行時間をdeadline保証やContribution測定の信頼根拠にしない。
4. Browser固有の制約を、他のWorkerへ無理に一般化しない。
5. Browserで実行可能であることと、機密Taskを実行してよいことを分ける。

これらをProduct Requirementへ追加するか、既存のRQ-003、RQ-005〜RQ-010、RQ-032を補強するかは、第二巡目のRequirement Gap Reviewで判断します。

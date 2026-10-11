# Browser Workerのライフサイクルを調べる（Lifecycle Spike）

## このSpikeの役割

このSpikeは、BrowserをEKI Fabricの一時Workerとして扱う場合に、tabのbackground化、freeze、discard、navigation、明示的な停止をどのように安全側へ吸収するかを確認するものです。

対象は主に、RQ-010 Opportunistic availability、RQ-032 Ad-hoc benevolent participation、RQ-042 Visible and controllable ad-hoc participation、QA-04 Endpoint user impact、QA-06 Recoverability under churnです。

Browserを安定した常駐Workerとして扱うことや、特定BrowserのLifecycle APIを必須化することは目的ではありません。

## 前提

Browser上のJavaScriptは、Native Agentと同じ可用性を期待できません。

Page Visibility APIでは、tabがbackgroundへ移ったことを検出できます。一方、background tabではtimerがthrottleされることがあり、BrowserやOSの判断でpage自体がfreezeまたはdiscardされることもあります。

ChromeのPage Lifecycleでは、frozen stateではfreezable taskが停止し、discardは発生時にscriptへ通知できません。復帰後に初めてdiscardされていたことが分かる場合があります。

したがって、

> Browserが終了・停止するときに必ず最後の通知を送れる

という前提は置けません。

## 中心となる考え方

Browser Workerでは、Lifecycle eventを「必ず届く停止通知」として使うのではなく、次の二つを分けます。

~~~text
Local boundary
  利用者が停止した、hiddenになった、freezeを観測した
  → 可能な限り即座にlocal Workerを止める

Remote boundary
  Browserからheartbeatが来なくなった
  → 短いLeaseを失効させ、Taskを再配置可能にする
~~~

Local eventが取得できれば早く止めますが、Fabric全体の回復はevent受信に依存しません。

## 実装した最小state machine

'lifecycle.mjs' では、Browser参加状態とLeaseを分けています。

Browser側の主な状態入力は次です。

- 利用者の明示的な参加開始
- 利用者の明示的な停止
- visible / hidden
- freeze / resume
- pagehide / pageshow

停止・freeze・pagehide後にはepochを進め、古いLease grantを受け入れません。

復帰した場合も、以前のLeaseをそのまま再利用せず、新しいLeaseを取得する前提にします。

## 二つのProfile

### visible-only

初期の安全側Profileです。

tabがhiddenになった時点でLocal Workerを停止し、復帰時に新しいLeaseを取得します。

Browser Workerの提供状態が利用者から見えやすく、background timer throttlingにも依存しにくいため、最初のEnterprise / public participation Profileとして説明しやすい形です。

### background-opt-in

利用者が明示的にbackground参加を選んだ場合にのみ、hidden中もopportunisticに続行できるProfile候補です。

ただしhidden中も、

- timer throttling
- freeze
- discard
- mobile OSによるprocess停止

を受け得ます。

したがって、background-opt-inでも短いLeaseとheartbeatは必須です。

## Explicit Stopを最優先する

利用者が停止を選んだ場合、Browser state machineは参加intent自体をfalseへ変えます。

その後、

- tabを一度hiddenにする
- visibleへ戻す
- pageshowが発生する

といったLifecycle変化があっても、自動的に参加を再開しません。

再開には再度の明示的な参加開始が必要です。

これはRQ-042の「分かりやすく停止できる」を、単なる一時pauseではなく参加意思の境界として扱うためです。

## Lease側の最小モデル

同じdirectoryの'LeaseBook'では、Control Plane側の短期Leaseを単純化して表現しています。

各Workerについて現在有効なLeaseを一つだけ持ち、

- heartbeatが現在のLease IDと一致する
- Leaseがまだ失効していない

場合だけrenewします。

新しいLeaseが発行された後に古いLeaseからheartbeatが来ても、現在のLeaseを復活させません。

## Unit Testで確認したこと

2026-09-18時点でNode 22を使い、

~~~text
npm test
~~~

を実行し、7 Testすべて通過しています。

確認した主なScenarioは次です。

1. visible-onlyではhidden時にlocal Workerが停止する
2. visibleへ戻ったとき、古いepochのLeaseを再利用しない
3. 利用者の明示的Stopが後のvisibility changeで解除されない
4. freeze後はlocal実行を止め、resume時に新しいLeaseを必要とする
5. discard等でLifecycle通知が一切来なくても、heartbeat消失後にRemote Leaseが自然失効する
6. 新Lease発行後、古いLeaseのheartbeatで権限を復活できない
7. background-opt-inではhidden中の継続を表現できるが、Lease依存である

## Browser実測Harness

'index.html'、'app.js'、'worker.js' は、実BrowserでLifecycleを観察するための小さなHarnessです。

画面上には明示的な「参加開始」「停止」buttonを置き、Web Workerから500ms間隔のheartbeatを受け取ってgapを記録します。

観察対象:

- visibilitychange
- freeze
- resume
- pagehide
- pageshow
- document.wasDiscarded
- Worker heartbeat gap

現在のHarnessは初期Profileとしてvisible-onlyを使います。

## なぜService Workerを常駐計算Workerにはしないか

Service WorkerはBackground Workerのように見えますが、event-driven lifecycleを持ち、Browserによって停止・再起動され得る前提の仕組みです。

EKIの長時間Computeを「Service Workerなら常駐する」と期待することは避けます。

Browser Workerの継続性は、どのJavaScript execution contextを使うかではなく、

- 処理を小さく分割できる
- Worker消失を正常系として扱える
- Lease expiry後に別Workerへ戻せる
- Resultが途中状態なら捨てられる

ことによって成立させます。

## Worker.terminate()の位置付け

Dedicated Workerはmain threadから'Worker.terminate()'で即時停止できます。

これは利用者の明示Stopやvisible-onlyでhiddenへ移った場合のLocal boundaryとして有用です。

ただし、Browser process自体が突然discardされた場合には、このAPIを呼ぶ機会そのものがありません。

そのため、terminateはRemote Lease expiryの代替ではありません。

## Lifecycle eventを過信しない

特に'pagehide'、'beforeunload'、'unload'等は、mobileやresource pressure下で常に発火するとは限りません。

EKIでは、

~~~text
eventが届いた
  → 早期回収のhint

eventが届かない
  → Lease timeoutで必ず回収
~~~

とします。

## Architectureへの示唆

Browser Workerの契約には、少なくとも次の性質が必要です。

~~~text
worker class: browser
availability: opportunistic
lease: short
stateful task: avoid by default
confidential data: deny by default
retry: aggressive
participation: explicit and visible
local stop: immediate when observable
resume: new lease required
~~~

具体的なTTL秒数は、このSpikeでは決めません。

background tabでtimerが実際にどの程度遅れるかはBrowser、Platform、電源状態等で変わるため、実測後に設定します。

## 次に実測すること

Browser別に、少なくとも次を確認します。

1. foreground時のheartbeat gap
2. background化後のheartbeat gap
3. freeze後のheartbeat停止
4. discard後のControl Plane側Lease expiry
5. resume / reload後にold Leaseを再利用しないこと
6. Stop buttonからWorker.terminate()までの時間
7. mobile Browser / OS backgroundingの差
8. Browser Workerが複数tabで重複起動しないためのsession ownership

Chromeでは'chrome://discards'からfreeze / discardを手動で試験できるため、実機検証に利用できます。

## このSpikeでまだ扱わないもの

- WebAssembly Executorの実性能
- SharedWorker
- multi-tab coordination
- Browser storage cache
- IndexedDB Artifact cache
- WebGPU
- battery / thermal API
- background executionを長時間維持する技巧
- Browser fingerprintをIdentityへ利用する設計

特に、Browserのfreeze / discardを回避する技巧をEKIの標準要件にはしません。

Browserがresourceを守るために計算を止めることは、EKIにとって失敗ではなく正常なcapacity lossです。

## 参考資料

- MDN — Page Visibility API
  - https://developer.mozilla.org/en-US/docs/Web/API/Page_Visibility_API
- Chrome for Developers — Page Lifecycle API
  - https://developer.chrome.com/docs/web-platform/page-lifecycle-api
- MDN — Worker.terminate()
  - https://developer.mozilla.org/en-US/docs/Web/API/Worker/terminate
- MDN — setTimeout / inactive tabs
  - https://developer.mozilla.org/en-US/docs/Web/API/Window/setTimeout

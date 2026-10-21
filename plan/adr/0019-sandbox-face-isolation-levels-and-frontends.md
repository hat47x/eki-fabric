# ADR-0019 隔離実行の外面：隔離の水準、生成コードの扱い、外部の入口の原則

- 状態: **採用**
- 日付: 2026-10-21
- 関連: Issue [#132](https://github.com/hat47x/eki-fabric/issues/132)、[ADR-0013](0013-product-core-and-faces.md)、[ADR-0016](0016-delegation-class-as-registered-profile.md)、[ADR-0018](0018-single-machine-delegation-profile.md)、[Extension SPI](../../architecture/EXTENSION_SPI.md)、[Contract Boundaries](../../architecture/CONTRACT_BOUNDARIES.md)、RQ-024、RQ-026、RQ-029、RQ-047、RQ-050、RQ-055、RQ-058、PAS-CAND-10

## 1. 背景

ADR-0013で、AIが生成したコードの隔離実行を、EKIの外面の一つと定めました。開発者の端末でAI Agentにコードを実行させる使い方は、一台での導入（DP-7、ADR-0018）の主な入口になります。

既存の設計には、隔離実行のための仕組みがすでにあります。Executor Runtime SPIは隔離の強さをHARD、BEST_EFFORT、OBSERVE_ONLY、UNAVAILABLEで表し、Sandbox Backend SPIは隔離に失敗しても制限のないnative実行へ逃げないことを定めています。

一方で、次の三点が決まっていませんでした。

1. 隔離の強さを、委任の等級（ADR-0016）に載せ、仕事の側から要求するための語彙がない。
2. AIが生成したコードは依頼ごとに変わるため、事前に実行体として承認できない。そのまま実行すると、RQ-026（実行体の来歴）と矛盾する。
3. AI Agentが使うMCPのような外部の入口から来た依頼が、受理の権限を素通りしないための原則がない。

## 2. 決定

1. 隔離の水準を、次の四段階の語彙として定める。数字は、ホストへの攻撃面を狭める仕組みの種類を表し、安全性の総合点ではない。どの段階でも、資源の上限の効き方は、Executor Runtime SPIのenforcementの種類（HARD、BEST_EFFORTなど）で別に表す。
   - ISO-0: 資源の上限だけ。OSのアカウントを分けたnative実行。カーネルとファイルシステムの多くを共有する。
   - ISO-1: OSの隔離。LinuxのnamespaceとseccompやWindowsのAppContainerなどで、ファイルシステムとネットワークを既定で閉じる。カーネルは共有する。
   - ISO-2: ホストのカーネルへの攻撃面を狭める実行環境。ユーザー空間のカーネル（gVisorなど）や、能力の限られた実行環境（WASMとWASIなど）。
   - ISO-3: 仮想マシンの境界。Kata Containers、Firecrackerなどのmicro VM。
2. 隔離の水準は、管理者が実行環境（Runtime ClassとSandbox Backendの組）ごとに、構成のdigestなどの根拠とともに登録する。実行環境やWorkerの自己申告では決めない（RQ-047）。委任の等級は、委任先が提供できる隔離の水準を明示する。
3. 隔離して試す型（Sandbox、RQ-055）では、ISO-0の実行環境を使わない。どの水準以上を要求するかは、管理者の方針と仕事の要求のうち厳しい方で決める。既定の雛形では、最低をISO-1とし、外部のモデルが生成したコードや、社内限定以上のData Classを扱う場合はISO-2以上を推奨する。要求を満たす実行環境がなければ、受理しないか、待たせる。低い水準へ逃がさない。
4. AIが生成したコードは、実行体ではなく、入力のArtifactとして扱う。実行体は、事前に承認したインタプリタや実行環境（たとえば、決まった版のPythonの実行環境のdigest）とする。こうして、RQ-026の実行体の来歴を保ったまま、依頼ごとに変わるコードを実行できる。生成コードのdigestは、入力の来歴として記録する。
5. 隔離実行の枠は、既定で通信を持たず、secretを受け取らない。通信が必要な場合は、登録したnetwork profile（社内のパッケージのミラーなど）だけを許す（RQ-029）。依頼者の認証情報を、枠の中へ渡さない。
6. 温めておいた枠は、初期化した状態からだけ使い、ほかの依頼者の枠を再利用しない。同じ依頼者の同じSessionの中では状態を引き継げるが、Sessionが終われば枠を破棄する。
7. 隔離実行の出力は申告であり、信頼できない生成物として扱う。Artifactの安全な取り込み（RQ-050）を通してから依頼者へ返し、出力であることを示す印を付ける。出力の中の指示を、EKIは権限として扱わない。
8. 外部の入口（MCP、OpenAI互換の推論API、REAPI、ジョブ投入の互換API）は、受けた依頼を、TaskRequest、Session、Sandboxの意味に写すだけとし、権限を持たせない。入口から来た依頼も、同じ受理、予算、委任の等級の判断を通る。入口は依頼者のcredentialだけを持ち、管理者の操作を提供しない。入口の仕様にあってEKIの契約で表せない機能は、受け付けたうえで無視することはせず、その依頼を受理しない。この原則を[Contract Boundaries](../../architecture/CONTRACT_BOUNDARIES.md)に書く。
9. MCPの入口は、隔離して試す型を中心にする。DP-7では、AI Agentの実行環境が起動するローカルのMCPサーバとして、依頼者のOSのユーザーで動き、依頼者のcredentialだけを持つ。AI Agentが別のAgentへ仕事を渡す場合は、委譲の連鎖（#117）として予算を縛る。
10. Extension SPIとContract BoundariesをBaseline 0.2へ上げる。隔離の仕組みは、ADR-0013の決定8に従い、性質の異なる二つ以上で契約が成り立つことを確かめてから、EKIの契約と呼ぶ。

## 3. 移行

- M0とMVPの優先順位は変えない。隔離実行の外面は、ADR-0013の段階のS2で実装する。
- いまの参照実装のnative Runner（`internal/adapters/reference/executor/native`）は、OSレベルの隔離を持たないので、ISO-0として登録する。隔離して試す型には使わない。

## 4. 影響

### 良い点

- 隔離の強さを、管理者が登録した語彙で比べられ、委任の等級と仕事の要求に結び付けられる。
- 生成コードを入力として扱うことで、実行体の来歴の要求を弱めずに、AI Agentのコード実行を受けられる。
- 外部の入口の原則を一つにしたことで、MCP、OpenAI互換、REAPIの入口を同じ考え方で足せる。

### 悪い点と制約

- ISO-2以上の仕組みは、OSによって選べるものが異なる。Windowsの端末での選択肢は限られ、DP-7でISO-2を満たせない端末がある。
- 隔離の仕組みからの脱出を完全に防ぐことは保証しない（DP-7 §12A.6）。水準は、攻撃面を狭める仕組みの種類を表すだけである。

## 5. 未決事項

- Windows、macOS、LinuxのDP-7で、それぞれどの仕組みでISO-1とISO-2を満たすか。
- 隔離して試す型の既定の最低水準を、ISO-1とISO-2のどちらにするか。現在の雛形はISO-1で、ISO-2を推奨としている。
- MCPのリモートの入口（Streamable HTTPなど）での認証と、依頼者の識別への対応づけ。
- 隔離実行の出力に付ける印の形式。

# EKI Threat Model

## 役割

この文書は、EKI Fabricの上流要件に対するThreat Modelの初期正本です。具体的な暗号方式、通信方式、Sandbox製品、Identity製品はまだ決定しません。

## 基本姿勢

EKIは、**ClientもWorkerも悪意を持ち得る**ことを前提とします。公式Client、正規Worker Agent、所属組織の端末、長期参加者であることは、それ自体で善意・未改変・正しい結果を意味しません。

一方、善意のアドホック参加者は積極的に排除しません。安全性は参加者の人格審査ではなく、権限分離、資源上限、検証、暗号学的完全性、必要に応じたattestation等で成立させます。

## 保護対象

- 他主体のArtifact、Result、Secret、Credential
- Provider内部の端末・network topology・利用者情報
- Worker hostのfilesystem、process、network、device
- Client / Beneficiaryの入力データと成果物
- Fabric全体の可用性と資源予算
- ExecutorとArtifactの完全性
- AcceptedResultの完全性
- Contribution Recordの正確性
- Policy、Grant、quota、identity binding
- Audit / provenance記録の完全性

## 想定攻撃者

### A1 — Malicious anonymous client

大量Task、巨大input、SSRF、fan-out、retry amplification、parser attack、他主体ID推測等を試みる。

### A2 — Compromised legitimate client

正当なcredentialを保持したまま、許可範囲の拡大、他Resourceへの横移動、内部情報列挙を試みる。

### A3 — Malicious worker

WorkOrderを無視し、計算を省略し、偽Resultを返し、処理時間やresource量を偽り、入力を保存し、別Workerと結託する。

### A4 — Modified official worker

OSS Agentを改造し、Protocol上は正規に見えるままsecurity checkやresource governanceを無効化する。

### A5 — Sybil operator

多数のClient / Worker identityを生成してquorum、reputation、Contribution、fair-share等を操作する。

### A6 — Malicious or compromised executor artifact

承認プロセスをすり抜けた、または脆弱性を持つExecutorがhost resourceへ到達しようとする。

### A7 — Network attacker

盗聴、改変、replay、downgrade、credential theft、endpoint impersonationを試みる。

### A8 — Malicious federation peer

Federation関係を利用して自身のtrust domain外のidentityを偽装し、包括的なauthorityを得ようとする。

### A9 — Economic / contribution attacker

実際には提供していないcomputeを申告し、無意味なWorkを自作自演し、CreditやContributionを水増しする。

### A10 — Resource-griefing participant

正しいResultを返さずdeadline直前までleaseを保持する、巨大Resultを返す等、他者の資源を消費させる。

## Security invariants

### SEC-001 — Authentication does not imply authorization

認証成功だけでTask、Artifact、Worker、Grant、管理APIへの包括的authorityを得てはならない。

### SEC-002 — Client cannot directly command a Worker

外部Clientからのrequestを、検証・認可・正規化を経ずそのままWorker実行命令へ変換してはならない。

### SEC-003 — Worker cannot self-certify correctness

Worker自身の署名、reputation、identityだけでResultをAcceptedResultへ昇格させてはならない。

### SEC-004 — Modified endpoint must not widen authority

Client/Worker softwareを任意に改変しても、Control Plane等が許可した以上のArtifact access、network access、compute budget、role authorityを得られてはならない。

### SEC-005 — Security labels are monotonic toward safety

Client入力によってData Class、required assurance、quota、policy等を安全性の低い方向へ上書きできてはならない。

### SEC-006 — Resource use is bounded before execution

大量RequestをQueueへ受理した後で止めるだけでは不十分とし、Admission時に計算、memory、storage、network、fan-out、retry等の最大影響を制限できなければならない。

### SEC-007 — Retry and fan-out consume bounded budget

再実行や子Task生成によって元Requestの許可予算を無制限に増幅できてはならない。

### SEC-008 — Arbitrary URL/path is not authority

Client指定URL、host path、UNC path等を指定しただけでWorkerからのaccess authorityが成立してはならない。

### SEC-009 — Artifact identity survives transport

主要Artifactの完全性は保存先や転送チャネルだけに依存せず検証できなければならない。

### SEC-010 — Role credentials are non-transitive

Client credentialを取得してもWorker/Control Planeになれず、Worker credentialを取得しても管理者authorityを得られない。

### SEC-011 — Confidentiality requires an explicit trustworthy boundary

通常のuntrusted Workerへ平文を渡しながら機密性も保証する、という矛盾した保証を行わない。機密処理には管理下境界、verified TEE等、別の信頼根拠が必要であることを明示する。

### SEC-012 — Quorum independence must be justified

同一攻撃者が支配可能な複数Workerを「独立した多数」とみなして高保証を主張してはならない。

### SEC-013 — Contribution cannot rely on self-reported usage alone

価値交換に利用し得るContributionを、Worker自己申告のCPU/GPU時間等だけから確定してはならない。

### SEC-014 — Audit must not become an exfiltration surface

監査のために不要なRaw input、Secret、内部topology等を広範囲に保存・開示しない。

## Availability assumptions

完全なDoS防止は保証しません。十分な規模の攻撃者が自身の正当なquotaを消費し続ける、network path自体を遮断する、Control Planeの外部依存を停止させる等は別途運用・capacity planningの対象です。

EKIが要求するのは、単一Client / Worker / credential / malformed inputが、許可された境界を超えて全体の資源を無制限に消費できないことです。

## Integrity assuranceの原則

Result verificationには単一方式を強制しません。

- deterministic exact match
- numeric tolerance / canonicalization
- application-specific validator
- replication / quorum
- managed trusted recomputation
- remote attestation
- cryptographic verifiable computation

ただし「何らかの方式を選択できる」と「すべてのTaskについて完全に検証可能」は別です。非決定的AI出力など、正解を安価に検証できないTaskではassurance上限を明示し、保証を誇張しません。

## 信頼基盤まで侵害された場合

次のすべてを同時に敵対的と仮定することは、通常のEKI security guaranteeの外に置きます。

- Control Plane root signing authority
- Administrator policy authority
- OS / hypervisor / TEE root used as trusted boundary
- underlying cryptographic primitives

ただし、これらの単一障害点を減らす、key rotation、threshold / independent approval等を導入する余地はArchitecture検討事項として残します。

## 設計前に追加調査する攻撃面

- Worker Sybilとcollusionに対するassurance model
- Browser identity / storage / worker lifecycle
- GPU driverおよびuser-supplied GPU workloadのattack surface
- Executor supply chain compromise
- Artifact decompression / parser bombs
- Federation bundle / identity namespace confusion
- Contribution laundering / self-dealing
- malicious validator / verifier
- compromised Control Plane component間のblast-radius分離

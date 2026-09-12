# EKI Fabric Threat Model

## この文書の役割

この文書は、EKI Fabricがどのような攻撃者を想定し、何を安全上の不変条件として守るかを定めるThreat Modelの正本です。

現在はArchitectureを選ぶ前の段階にあるため、具体的な暗号方式、通信方式、Sandbox製品、Identity製品はここでは決定しません。

## 基本姿勢

EKIでは、**ClientもWorkerも悪意を持ち得る**ことを前提にします。

公式Clientであること、正規Worker Agentを使っていること、所属組織の端末であること、認証に成功したこと、長期間問題なく参加してきたことは、それだけでは善意、未改変性、正しい結果を保証しません。

一方で、善意のアドホック参加者を積極的に排除することも目標にしません。

安全性を参加者の人格審査へ依存させず、権限分離、資源上限、結果検証、Artifactの完全性、必要に応じたAttestationやProofなどで成立させることで、強い事前信頼関係のない善意の参加者も受け入れられる状態を目指します。

## 「完全な悪意耐性」の意味

EKIが目指す悪意耐性は、「すべての構成要素が同時に侵害されても絶対に安全」という意味ではありません。

主に次を目標にします。

- 悪意あるClientが、許可された以上の権限や資源を自分で作り出せない
- 悪意あるWorkerが、偽Resultを返すだけでそれを確定結果にできない
- 一つのCredentialや一つのParticipantの侵害が、別Roleや他主体へ自動的に波及しない
- untrusted Workerへ与えてよいDataと、与えてはいけないDataを分けられる
- 古いCredentialやEvidenceを再利用して失効済みの権限を復活させられない
- 検証基盤そのものをどこまで信頼したかを隠さない

Control Planeのroot signing authority、Administrator policy authority、利用する暗号primitiveなど、EKIが明示的な信頼基盤として置いたものまで同時に侵害された場合は、通常の保証範囲外です。

ただし、Architecture段階では信頼基盤を不必要に一箇所へ集中させないこと、key rotationや独立承認などでblast radiusを下げられることを検討します。

## 保護対象

EKIでは、少なくとも次を保護対象とします。

- 他主体のArtifact、Result、Secret、Credential
- Provider内部の端末情報、network topology、利用者情報
- Worker hostのfilesystem、process、network、device
- Client / Beneficiaryの入力データと成果物
- Fabric全体の可用性と資源予算
- ExecutorとArtifactの完全性
- Accepted Resultの完全性
- Contribution Recordの正確性とprovenance
- Policy、Grant、quota、identity binding
- Audit / provenance記録の完全性
- Assurance Evidenceに含まれ得る端末・構成情報
- 資源提供者の意思とlocal opt-out

## 想定する攻撃者

### A1 — 悪意ある匿名Client

大量Task、巨大input、異常に深いmetadata、SSRF相当のaccess、fan-out、retry amplification、parser attack、他主体IDの推測などを試みます。

目的は、EKIの資源を枯渇させる、内部情報へ到達する、別主体のauthorityを得ることなどです。

### A2 — 侵害された正規Client

正当なCredentialを保持したまま、許可範囲の拡大、他Resourceへの横移動、内部情報の列挙を試みます。

この攻撃者がいるため、「認証済みだから安全」という設計は採りません。

### A3 — 悪意あるWorker

WorkOrderを無視する、計算を省略する、偽Resultを返す、処理時間やresource量を偽る、入力を保存する、別Workerと結託するといった行為を行います。

### A4 — 改変された公式Worker

OSSとして配布されたAgentを改造し、Protocol上は正規Workerに見えるまま、resource governorやsecurity checkを無効化します。

そのため、正規binaryを配布していること自体をSecurity Root of Trustにはしません。

### A5 — Sybil operator

一人の攻撃者が多数のClient / Worker Identityを作成し、quorum、reputation、Contribution、fair-shareなどを操作します。

異なる`worker_id`が複数あることと、独立した複数主体が存在することを同一視しません。

### A6 — 悪意または脆弱性を持つExecutor Artifact

承認プロセスをすり抜けたExecutor、依存ライブラリを含め侵害されたExecutor、または単純な脆弱性を持つExecutorが、host resourceや他Taskの情報へ到達しようとします。

### A7 — Network attacker

盗聴、改変、replay、downgrade、Credential theft、endpoint impersonationなどを試みます。

### A8 — 悪意あるFederation peer

Federation関係を利用して、自身のTrust Domain外のIdentityを偽装する、限定された関係を包括的なauthorityへ拡大する、といった行為を試みます。

### A9 — Contribution / economic attacker

実際には提供していないcomputeを申告する、無意味なWorkを自作自演する、複数Identityを使ってContributionを水増しするなど、外部のCreditや評価制度へ有利な記録を作ろうとします。

### A10 — Resource-griefing participant

正しいResultを返す意思がなく、deadline直前までLeaseを保持する、巨大Resultを返す、意図的にtimeoutを繰り返すなど、限られた範囲でも他者へ運用コストを与えようとします。

### A11 — 悪意または侵害されたValidator / Verifier

偽Resultを正しいと判定する、Attestation Evidenceを誤って評価する、古いEvidenceを新しいものとして扱うなど、Workerの検証境界そのものを侵害します。

EKIでは「Workerを検証したから安全」とだけ考えず、誰がどのPolicyで検証したかもprovenanceの一部として扱う必要があります。

### A12 — 意図しないBrowser resource useを行う提供側

通常のWeb閲覧に紛れてCPU/GPUを利用する、停止方法を分かりにくくする、hidden iframe等で資源提供を継続するなど、利用者が認識していない計算資源利用を行います。

これはWorker側が悪意を持つ攻撃とは方向が逆ですが、human-first原則を守るためThreat Modelへ含めます。

## Security Invariants

以下は、具体的Architectureにかかわらず守るべき安全上の不変条件です。

### SEC-001 — Authentication does not imply authorization

認証に成功したことだけで、Task、Artifact、Worker、Grant、管理APIへの包括的authorityを得てはならない。

### SEC-002 — Client cannot directly command a Worker

外部Clientから受けたRequestを、検証、認可、Policy適用、必要な正規化を経ず、そのままWorkerの実行命令へ変換してはならない。

Client PlaneとWorker Planeの境界を、単なるProxyにしない。

### SEC-003 — Worker cannot self-certify correctness

Worker自身の署名、reputation、Identity、自己申告だけで、ResultをAccepted Resultへ昇格させてはならない。

### SEC-004 — Modified endpoint must not widen authority

Client / Worker softwareを任意に改変しても、Control Plane等が許可した以上のArtifact access、network access、compute budget、Role authorityを得られてはならない。

### SEC-005 — Security properties are monotonic toward safety

Client入力によってData Class、required assurance、quota、Policyなどを安全性の低い方向へ上書きできてはならない。

Clientが要求を厳しくすることと、権限を弱い保証へ落とすことを区別する。

### SEC-006 — Resource use is bounded before damage becomes unbounded

大量RequestをQueueへ受理した後で止めるだけでは不十分とする。

Admissionや実行境界で、compute、memory、storage、network、fan-out、retry、outputなどの最大影響を制限できなければならない。

### SEC-007 — Retry and fan-out consume bounded budget

再実行や子Task生成によって、元Requestの許可予算を無制限に増幅できてはならない。

### SEC-008 — Arbitrary URL or path is not authority

ClientがURL、host path、UNC pathなどの文字列を指定しただけで、Workerからその場所へaccessするauthorityが成立してはならない。

### SEC-009 — Artifact identity survives transport

主要Artifactの完全性は、保存先や転送チャネルだけに依存せず確認できなければならない。

### SEC-010 — Role credentials are non-transitive

Client Credentialを取得してもWorkerやControl Planeになれず、Worker Credentialを取得しても管理者authorityを得られない。

一つのRoleのCredential侵害が、別Roleへの自動的な権限昇格にならないことを要求する。

### SEC-011 — Confidentiality requires an explicit trustworthy boundary

通常のuntrusted Workerへ平文を渡しながら、そのWorker ownerに対する機密性も保証する、という矛盾した主張をしてはならない。

機密処理には、管理下境界、検証されたConfidential Computing環境など、別の信頼根拠が必要であることを明示する。

### SEC-012 — Quorum independence must be justified

同一攻撃者が支配できる複数Workerを「独立した多数」とみなし、高保証を主張してはならない。

単なるWorker IDの違いは、独立性の十分な証拠ではない。

### SEC-013 — Contribution cannot rely on self-reported usage alone

外部の価値交換や評価に利用され得るContributionを、Worker自己申告のCPU/GPU時間などだけから確定してはならない。

### SEC-014 — Audit must not become an exfiltration surface

監査やAssuranceのために、不要なRaw input、Secret、内部Topology、Raw Attestation Evidenceなどを広範囲に保存・開示してはならない。

### SEC-015 — Stale authority cannot be revived by replay

期限切れ、失効、revoke済みのCredential、Lease、Grant、Evidence、内部実行権限などを、古いMessageや署名済み情報のreplayによって再び有効にできてはならない。

具体的なfreshness機構はArchitectureで定める。

### SEC-016 — Verification authority must be traceable

ResultをAccepted Resultへ昇格させた判断について、どのValidator、Verifier、Policy authority、Proof verifierを根拠にしたかを追跡できなければならない。

Worker自身が検証者を自由に選び、自分のResultを自己承認できる構造にしてはならない。

### SEC-017 — Untrusted deletion claims do not restore confidentiality

untrusted Workerが「入力を削除した」と申告しても、それだけを根拠に、一度開示した平文データの機密性が回復したとみなしてはならない。

### SEC-018 — Resource donation must remain visible and stoppable where participation is voluntary

Browserや個人端末など、利用者自身の意思によるアドホック参加では、資源提供を本人から隠すことを安全な標準動作としてはならない。

資源提供中であることを認識でき、提供を停止できる必要がある。

## 可用性についての前提

EKIは、あらゆるDoSを完全に防げるとは主張しません。

たとえば、十分な規模の攻撃者が自身に正当に割り当てられたquotaを使い続ける、network pathそのものを遮断する、Control Planeの外部依存を停止させる、といった事象は、運用やcapacity planningを含めて扱う必要があります。

EKIが要求するのは、単一Client、単一Worker、単一Credential、単一malformed inputなどが、許可された境界を越えて全体資源を無制限に消費できないことです。

## Result Integrityについての原則

Result Verificationには単一方式を強制しません。

利用可能な方式として、たとえば次があります。

- deterministicな完全一致
- numeric toleranceやcanonicalizationを用いた比較
- application-specific validator
- replication / quorum
- 管理下Workerによるrecomputation
- Remote Attestation
- cryptographic verifiable computation

ただし、「複数の方式を選べる」ことと、「すべてのTaskについて完全に検証できる」ことは別です。

生成AIの文章のように正解を安価に判定できないTaskでは、確認できるprovenanceや実行条件を返しつつ、semantic correctnessの保証上限を明示します。

## 機密性についての原則

一度untrusted Workerへ平文を渡した場合、そのWorkerがコピーを保持していないことを一般には証明できません。

したがって、機密性が必要なTaskでは、次のどこを信頼境界にするかを明示する必要があります。

- 組織管理下のWorker
- 契約・運用上の管理境界
- Remote Attestationで確認したConfidential Computing環境
- その他、Architectureで採用する明示的な保護境界

暗号化された通信路だけでは、実行中の平文をWorker ownerから守る十分条件にはなりません。

## 信頼基盤まで侵害された場合

次のすべてを同時に敵対的と仮定することは、通常のEKI Security Guaranteeの外に置きます。

- Control Plane root signing authority
- Administrator policy authority
- OS / hypervisor / TEE rootを信頼境界として採用した場合の当該root
- underlying cryptographic primitives

また、特定Taskで一つのValidatorを明示的に信頼基盤とした場合、そのValidatorが侵害されれば、その保証Profileが破れる可能性があります。

重要なのは、この依存を隠さず、どの信頼基盤へ依存した保証なのかを追跡可能にすることです。

## Architectureへ入る前に追加調査する攻撃面

- Worker Sybilとcollusionに対するindependence model
- Browser identity、storage、worker lifecycle、明示的参加UI
- GPU driverおよびuser-supplied GPU workloadのattack surface
- Executor supply-chain compromise
- Artifact decompression / parser bomb
- Federation bundle / identity namespace confusion
- Contribution laundering / self-dealing
- malicious / compromised Validator・Verifier
- Attestation Evidenceのfreshness、privacy、replay
- compromised Control Plane component間のblast-radius分離
- 副作用Taskで「実行済みか不明」になった場合の安全なRecovery境界

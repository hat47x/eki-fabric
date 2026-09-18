# Executor Provenance / Content Admission Spike

## このSpikeの役割

このSpikeは、EKI Fabricで実行するExecutorについて、名前やtagだけを信頼せず、実際の内容、署名、provenance、EKI側のAdmission Policyを分けて確認できることを小さく実証します。

対象は主に次です。

- RQ-004 Task submissionとcode admissionの分離
- RQ-026 Executable provenance
- RQ-027 Content integrity
- RQ-039 End-to-end provenance
- RQ-048 Freshness and revocation of time-bounded authority
- RQ-051 Control-plane authority separation
- SEC-004 Modified endpoint must not widen authority
- SEC-009 Artifact identity survives transport
- SEC-015 Stale authority cannot be revived by replay
- PAS-15 Executor supply-chain substitution
- QA-01 Security containment

特定のRegistry、Sigstore、SLSA、in-toto、OCI、KMS、署名方式をArchitectureとして採用するものではありません。

## 背景

Executorは、Worker上で実際に動くコードです。

そのため、次のような指定をそのまま実行Identityにすることは危険です。

~~~text
executor:latest
my-tool:v1
example.com/tool:stable
~~~

これらは人間にとって便利な名前ですが、後から別内容を指す可能性があります。

EKIでは、

> 人間向けの名前と、実際に実行するbyte列のIdentityを分ける

ことを基本にします。

## 四つの層を分ける

今回のSpikeでは、次を別々の保証として扱います。

~~~text
1. Content Identity
   このbyte列は何か
   → Digest / Size / Media Type

2. Publisher Identity
   誰がこのArtifactへ署名したか
   → Publisher署名

3. Build Provenance
   どのBuilder / Source / Commitから作られたか
   → Provenance

4. EKI Executor Admission
   このArtifactを、このRuntime Classで実行してよいか
   → EKI Policy + Executor Approval
~~~

さらに実行時には、

~~~text
5. Fetched Content Verification
   実際にWorkerが取得したbyte列が承認済みDigestと一致するか
~~~

を確認します。

どれか一つを他の代用にはしません。

## OCIから借りられる考え方

OCI Content Descriptorは、参照対象について少なくとも、

- media type
- digest
- size

を持ち、untrusted sourceから取得した内容についてDigestを再計算して検証する考え方を取っています。

EKI Artifact Contractでも、この三つは自然な最小構成です。

ただし、EKIのExecutorがOCI Artifactでなければならないという意味ではありません。

WASM、Native binary、Container、その他将来形式でも、同じContent Identityの考え方を適用できます。

## SLSA / in-totoから借りられる考え方

SLSA Provenanceは、

> 特定のBuild Platformが、どのBuild DefinitionからどのArtifactを生成したか

を記録するためのAttestationです。

EKIにとって重要なのは、ProvenanceのSubjectが実行対象Digestへbindingされていることです。

たとえば、

~~~text
Builder: trusted-builder
Source: trusted/repo
Commit: approved-commit
Subject: digest-A
~~~

というProvenanceが正しく署名されていても、実行しようとしているArtifactがdigest-Bなら、そのProvenanceは使えません。

## Sigstoreから借りられる考え方

Sigstore / Cosignは、Artifact署名やAttestationについて、署名Identity、Certificate、Transparency Log等を使って検証できます。

EKIでは、将来このような検証Providerを利用できます。

ただし、

~~~text
Sigstoreで署名検証できた
        ↓
EKIで実行してよい
~~~

とはしません。

署名はPublisher Identityや証跡を確認する材料です。

そのPublisherを信頼するか、どのSource / Builder / Runtime Classを許可するか、現在revokedでないかはEKI側Policyの判断です。

## 最小モデル

`admission.go` では、次を使います。

### Executor Artifact

~~~text
Digest
MediaType
Size
~~~

### Publisher Statement

~~~text
SubjectDigest
PublisherID
Signature
~~~

### Provenance

~~~text
SubjectDigest
BuilderID
SourceURI
SourceCommit
Signature
~~~

### Admission Policy

~~~text
ExecutorID
AllowedDigest
AllowedPublisher + pinned public key
AllowedBuilder + pinned public key
AllowedSourceURI
AllowedCommit
AllowedRuntimeClass
PolicyRevision
ApprovalTTL
~~~

### Executor Approval

Admission AuthorityがすべてのEvidenceとPolicyを確認した後、

~~~text
ExecutorID
ArtifactDigest
MediaType
Size
RuntimeClass
PublisherID
BuilderID
SourceURI
SourceCommit
PolicyRevision
Expires
~~~

へ署名します。

## Executor名とDigestは別

例として、

~~~text
ExecutorID
  dev.example.transform@1
~~~

は人間・Application向けの論理Identityです。

一方で、実際に実行する内容は、

~~~text
sha256:...
~~~

へ固定します。

ExecutorIDだけからWorkerが任意のRegistry tagを解決して実行する構造にはしません。

可変aliasを利用する場合でも、

~~~text
alias
  ↓ resolve
digest
  ↓ Admission
signed Executor Approval
  ↓ WorkOrder
digest
~~~

という順序にし、aliasは実行Identityへ残さない方が安全です。

## Mutable tag substitution

Spikeでは、

1. 正規byte列AをAdmissionする
2. その後、同じ可変aliasの参照先を悪意あるbyte列Bへ変更したと仮定する
3. WorkerがBを取得する

ScenarioをTestしています。

Executor ApprovalはAのDigestとSizeへbindingされているため、Bは実行直前の再hashで拒否されます。

これにより、

> 承認時と実行時の間にtagが差し替えられる

というTOCTOUを、Content Digest境界で小さくできます。

## Publisher署名だけでは足りない

署名が暗号学的に正しくても、次は別に確認します。

- 信頼しているPublisher鍵か
- Publisher IDがPolicyと一致するか
- 署名Subjectが対象Artifact Digestと一致するか

別Artifactへ付けられた正しい署名を流用しても、Admissionできません。

また、Publisherが悪意を持つ場合や鍵が侵害された場合まで「署名があるから安全」とはみなしません。

## Provenanceだけでも足りない

Provenanceについても、

- Trust Root
- Subject Digest
- Builder ID
- Source URI
- Source Commit

をPolicyへ照合します。

ただし、これらがすべて正しいことは、

- 脆弱性がない
- 悪意ある処理を含まない
- Sandboxを突破しない
- 業務上安全である

ことを自動的に意味しません。

Provenanceは「どのように作られたか」というEvidenceです。

実行許可は別のAdmission Decisionです。

## EKI Admission Authority

Publisher署名とProvenanceが正しくても、それだけではWorkerは実行しません。

最終的に必要なのは、

> EKIが現在のPolicyで、このDigestを、このRuntime Classとして承認した

というExecutor Approvalです。

このApprovalのTrust RootはWorker / Executor Guard側へ固定し、Clientが任意のAdmission Authorityを指定できないようにします。

これはControl Plane Authority Separation Spike、Federation Trust Binding Spikeで確認した原則と同じです。

## Runtime ClassをClientが広げない

同じArtifactでも、

~~~text
wasm-component
native-restricted
native-unrestricted
container
browser
~~~

では攻撃面が異なります。

SpikeではPolicyが`wasm-component`を許可しているとき、Clientが`native-unrestricted`へ変更してAdmissionを得ることを拒否します。

Runtime Classは単なる性能hintではなく、実行Authority / Sandbox境界に関わるためです。

## RevocationとPolicy Revision

一度発行したApprovalを無期限に有効にはしません。

Spikeでは、

- Approval expiry
- Artifact Digest revocation
- Policy Revision

を別々に確認します。

### Approval expiry

時間制限のあるApprovalとして扱います。

### Digest revocation

脆弱性やSupply-chain Incidentが判明したArtifactを停止できます。

過去に正当にAdmissionされたApprovalがあっても、現在revokedなら実行しません。

### Policy Revision

Policy自体が更新された場合、古いPolicy Revisionで発行されたApprovalをrejectできます。

これにより、

~~~text
以前は許可されていた
        ↓
Policyを厳格化した
        ↓
古いApprovalをreplay
~~~

という経路を閉じられます。

## Revocationの限界

今回のモデルは、Executor Guardが現在のRevocation / Policy Revisionを知っていることを前提にしています。

実際の分散Workerでは、

- offline
- network partition
- stale cache
- Control Plane outage

があり得ます。

したがってArchitectureでは、

- Approval TTL
- revocation snapshot freshness
- high-risk Executorのonline check
- emergency stop
- Worker local cache invalidation

などを比較する必要があります。

「revocation recordを用意した」だけで即時失効を保証したとは扱いません。

## Unit Testで確認したこと

2026-09-18時点でGo 1.23.2を使い、

~~~bash
go test ./...
~~~

を実行し、Testは通過しています。

主なScenarioは次です。

1. 正常なDigest / Publisher / Provenance / PolicyからApprovalを生成し、実byte列を実行時検証できる
2. 可変aliasが別byte列へ差し替えられてもDigest不一致で拒否する
3. 正しいPublisher署名でも、別Subject Digest向けなら拒否する
4. 未信頼Publisher鍵を拒否する
5. Builder IDがPolicyと異なるProvenanceを拒否する
6. Source URIがPolicyと異なるProvenanceを拒否する
7. Publisher署名とProvenanceだけではAdmission Authorityを代替できない
8. Admission後にDigestをrevokeすると実行を止められる
9. 古いPolicy RevisionのApprovalを拒否する
10. expiry済みApprovalを拒否する
11. ClientがRuntime Classを広げることを拒否する
12. DescriptorのSizeと実byte列が一致しなければ拒否する

## Architectureへの示唆

Executor supply chainは、概念的には次のように分ける方が自然です。

~~~text
Build / Publish
  ↓
Content Digest
  ↓
Publisher Signature
  ↓
Build Provenance
  ↓
EKI Executor Admission
  ↓
signed Executor Approval
  ↓
Task / WorkOrder compilation
  ↓
Worker fetch
  ↓
size + digest verification
  ↓
Sandbox execution
~~~

重要なのは、途中のEvidenceが増えても、後段の判断を省略しないことです。

## Artifact Safe Processingとの分離

以前のArtifact Ingest Spikeと同様に、

~~~text
Digestが一致した
署名が正しい
Provenanceが正しい
Executor Admission済み
~~~

ことは、

~~~text
そのbyte列を安全にparse / load / executeできる
~~~

ことを意味しません。

Executorは最終的にSandbox / Runtime境界の内側で起動する必要があります。

特にNative / GPU runtimeは、WASM等と同じIsolation強度を仮定しません。

## OCI / Sigstore / SLSAをCore Contractへ固定しない

先行仕様は非常に有用ですが、EKI Coreは特定Supply-chain stackに依存しすぎない方がよいと考えます。

Coreが必要とする抽象情報は、まず次です。

~~~text
Content Descriptor
Signer / Publisher Evidence
Build Provenance Evidence
Admission Decision
Revocation / Freshness
Runtime Class
~~~

OCI Descriptor、Sigstore Bundle、in-toto Statement、SLSA Provenance等は、これらへ接続するAdapter候補として比較します。

## このSpikeで一般化してよいこと

- 実行ArtifactはDigestへ固定できる
- 名前・tag・aliasを実行Identityにしない構造を作れる
- Publisher署名とBuild Provenanceを別Evidenceとして扱える
- それらとEKI Executor Admissionをさらに分けられる
- Worker側で取得後のDigestを再検証できる
- Runtime ClassをAdmission Policyへbindingできる
- expiry / revocation / Policy Revisionで古いApprovalを制限できる

## このSpikeで一般化してはいけないこと

まだ次は確認していません。

- 実OCI Registry
- OCI Referrers
- Sigstore keyless verification
- Rekor / Transparency Log
- in-toto Statement / DSSE
- SLSA Provenance parser
- SBOM Policy
- vulnerability scanner
- malware scanning
- reproducible build
- multi-architecture Artifact
- Native code signing
- Windows Authenticode / Apple notarization等
- WASM Component signature
- Executor dependency graph
- emergency revocation propagation
- offline Workerのrevocation freshness
- Admission Authority自体の侵害
- Sandbox execution

## 次の検証

このSpikeから自然につながる候補は二つあります。

### Side-effect Task uncertain outcome

WorkOrder / AttemptのAuthorityが整理されてきたため、「実行済みか不明」のRecovery境界を具体化できます。

### TEE Remote Attestation abstraction

Executor Approvalと、Worker Execution EnvironmentのEvidenceを混同せず、

~~~text
何を実行してよいか
どこで実行してよいか
~~~

を別々に扱うモデルへ進めます。

## 参考資料

- OCI Image Specification — Content Descriptors
  - https://specs.opencontainers.org/image-spec/descriptor/
- OCI Image Manifest Specification
  - https://specs.opencontainers.org/image-spec/manifest/
- SLSA v1.2 — Build Provenance
  - https://slsa.dev/spec/v1.2/build-provenance
- Sigstore — Verifying Signatures
  - https://docs.sigstore.dev/cosign/verifying/verify/
- Sigstore — Bundle Format
  - https://docs.sigstore.dev/about/bundle/

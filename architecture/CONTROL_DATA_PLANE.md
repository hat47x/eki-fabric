# Control Plane / Data Plane Architecture

Status: **Baseline 0.1 — 2026-09-21**

## 1. この文書の目的

EKI Fabricでは、Taskの受け付け、Policyの判断、Placement、Lease、WorkOrderの発行などの制御の情報と、Model、Dataset、Document、Result、Checkpointなどの大きなbyte列とを、同じ経路に集約しません。

この文書では、次のことをArchitecture Baselineとして定義します。

- Control Planeが何を扱うか
- Data Planeが何を扱うか
- ArtifactのIdentityとAccess Authorityを、どう分けるか
- WorkerがDataに到達するまでに、何を検証するか
- Cacheとlocalityを、Security Authorityにしない方法
- 大量の転送やretryが、Networkを圧迫しないための境界
- 小規模なdeploymentからFederationまで、同じ論理構造を、どう保つか

関連する文書は、次のとおりです。

- AUTHORITY_BOUNDARIES.md
- CONTRACT_BOUNDARIES.md
- ../requirements/PRODUCT_REQUIREMENTS.md
- ../requirements/QUALITY_ATTRIBUTES.md
- ../spikes/artifact-locality-cost/README.md
- ../spikes/artifact-ingest/README.md
- ../spikes/control-plane-authority-separation/README.md

## 2. 最も重要な分離

EKIでは、次のものを同一視しません。

~~~text
Artifact Content Identity
  そのbyte列は何か

Artifact Record
  誰の、どの分類・保持条件のArtifactか

Storage Location
  どこにbyte列があるか

Artifact Grant
  誰が、どのTask / Attemptで、何の操作をしてよいか

Transfer Credential
  特定transportで実際にbyte列へ到達するための短命Credential

Cache Locality
  近くにcopyがある可能性
~~~

Digestが一致していることは、Access Authorityを意味しません。

Storage URIを知っていることも、Access Authorityを意味しません。

Cacheに同じDigestがあることも、Security requirementを満たす根拠ではありません。

---

# 3. Planeの定義

## 3.1 Control Plane

Control Planeが扱うのは、小さなmetadata、Decision、Stateです。

主な対象は、次のとおりです。

- TaskRequest / AdmittedTask / TaskIR
- Attempt
- ResourceAdvertisementの要約
- PlacementProposalとApproval
- Lease
- ExecutorApprovalのmetadata
- Artifactのmetadataと、Grantのmetadata
- WorkOrder
- TaskとAttemptのstate
- VerificationResultとAcceptedResultのmetadata
- Contributionのmetadata
- Auditのmetadata
- Quotaとbudgetの予約
- FederationとPolicyの設定

Control Planeの標準の経路に、ModelやDataset本体のGB単位のbyte列を流しません。

## 3.2 Data Plane

Data Planeが扱うのは、大きなbyte列の、保存、転送、検証、cacheです。

主な対象は、次のとおりです。

- Clientのupload
- Executor Artifactのbytes
- TaskのInput Artifact
- Result Artifact
- Checkpoint
- ProofとEvidenceのlarge object
- 安全にingestやdecodeする対象
- Workerのlocal cache
- 管理されたsite cache
- 外部のobject store
- アプリケーションが所有するstorage adapter

Data Planeも、Authorizationが不要な、自由なbyteのpipeではありません。

ArtifactGrantやOutput Bindingなど、Control PlaneのDecisionを、Data Plane側で強制します。

## 3.3 Administrative Plane

Storage backendの登録、Artifact Storeのcredentialの設定、Federationのexport policy、encryption keyのpolicyなどは、Administrative Planeです。

通常のTask requestからは、変更できません。

---

# 4. Control Planeを、Data Proxyにしない理由

すべてのArtifactをControl Plane Service経由にすると、実装の初期は単純に見えます。

しかし、長期的には、次のような問題が生じます。

- Control Planeのnetworkの帯域が、すべてのTaskのbottleneckになる。
- 大規模なModelの転送が、Task admissionにまで影響する。
- retry stormが、Control Planeを巻き込む。
- Data breachのときの、blast radiusが広がる。
- Federation、cloud、site-localなstorageの利点を失う。
- cache localityを活かしにくい。
- 小さなcontrol metadataと、bulk dataとで、SLOが衝突する。

そのため、標準の構成は、次のとおりにします。

~~~text
Control Plane
  metadata / authority / state
        │
        │ ArtifactGrant / Transfer Plan
        ▼
Worker ─────────────── Data Plane Store / Cache
       bulk bytes
~~~

小規模なDeploymentで、同じProcessが両方の機能を実装することは、認めます。

ただし、論理的なAPI、Quota、Credential、Auditは、ControlとDataで分離します。そして、あとで別のProcessに移せる構造を保ちます。

---

# 5. Artifact Content DigestとArtifact Recordを分ける

## 5.1 Content Digest

Content Digestは、byte列のIdentityです。

たとえば、次のとおりです。

~~~text
sha256:<digest>
~~~

実際のhash algorithmのnegotiationは、後続のContractで定義します。

Content Digestは、次の用途に使います。

- 転送で壊れたことの検出
- cacheの検証
- 変更できないidentity
- 重複したcontentの認識
- Executorの正確なartifactへのbinding

## 5.2 Artifact Record

同じbyte列でも、Security Contextは異なり得ます。

たとえば、同じpublicなmodel fileが、次のように、別々のRecordを持つことがあります。

- Organization Aの、承認済みのmodel
- Organization Bの、未審査のupload
- Public datasetのmirror

Artifact Recordは、概念上、次のものを持ちます。

~~~text
Artifact Record ID
Content Digest
Owner / Tenant / Trust Domain
Data Class
Media Type
Size
Provenance refs
Safe Processing status
Retention policy
Encryption metadata
Authorized storage location refs
Created / updated metadata
Policy revision
~~~

## 5.3 同じDigestだからといって、Recordを統合しない

Content-addressed storageを使っても、

> 同じDigestなので、同じAuthorizationでよい

とはしません。

Authorization、classification、retention、provenanceは、Artifact Recordの単位です。

物理的なbackendがdedupするかどうかは、Storage Adapterの実装の詳細です。

## 5.4 Tenantをまたぐdedup

Tenantをまたぐ物理的なdedupは、次のものを生む可能性があります。

- Artifactが存在することのside channel
- cache hitのtiming
- 削除とretentionの干渉
- classification metadataの混同

Baselineでは、cross-tenantなdedupを、Security機能として利用しません。

Storage backendが内部的にdedupする場合も、論理的なRecord、Grant、auditは、Tenantごとに分離します。そして、dedupがhitしたことを、Clientに露出しないことを、要求します。

---

# 6. Artifactのlocationは、Authorityではない

Artifact Recordは、Storage Locationを参照できます。

たとえば、次のとおりです。

~~~text
location-A
  managed object store / region-a

location-B
  site cache / office-1

location-C
  external application store
~~~

しかし、SchedulerやClientに、生のURI、bucket名、内部のhostを、広く公開しません。

transportに固有のlocationを解決するのは、Data AuthorityとStorage Adapterだけです。

Schedulerに渡すのは、必要に応じて、次のような、無害化した要約です。

- locality class
- siteやregionのaffinity
- 足りないbyte数の見積もり
- 転送のコストの見積もり
- cacheの確からしさと新しさ

---

# 7. Storage Adapter

EKI Coreは、特定のObject Storeに固定しません。

Storage Adapterは、概念上、次のものを提供します。

- PutとUpload Session
- GetとRead Session
- Headとmetadata
- 対応している場合の、range read capability
- content digestのevidence
- 変更できないobjectのサポート
- deleteとretentionの操作
- localityの記述
- 任意の、server-sideのencryption
- 任意の、multipartとresumableな転送

実装の候補は、次のとおりです。

- local filesystem
- HTTPのobject store
- S3互換のstorage
- 適したartifact向けの、OCI互換のregistry
- アプリケーションが所有するstore
- 管理されたcloudのobject storage

Artifact Contract自体を、S3 objectやOCI manifestと同一にはしません。

---

# 8. Data Access Broker / Resolver

ArtifactGrantは、Security Decisionです。

しかし、実際のtransportには、次のような違いがあります。

- mTLSのrequest
- 短い有効期間のbearer token
- presigned URL
- 署名付きのheader
- ローカルのIPC capability
- filesystem descriptor

そこで、Baselineでは、次の変換の境界を置きます。

~~~text
ArtifactGrant
      │
      ▼
Data Access Broker / Storage Adapter
      │
      ▼
transport-specific short-lived access
~~~

Data Access Brokerは、独立したMicroserviceである必要はありません。

Data AuthorityとStore Adapterの中の、論理的なfunctionとして実装できます。

## 8.1 WorkerはStorage credentialを持たない

Workerに、次のものを渡しません。

- bucket全体のcredential
- accountのsecret
- 長い有効期間のcloud key
- 組織全体のfilesystem credential

必要なArtifactと操作と有効期限に範囲を限った、短い有効期間のaccessに変換します。

## 8.2 Transport credentialを、WorkOrderに永続的に埋め込まない

WorkOrderに、長い有効期間のpresigned URLなどを直接埋め込むと、次の問題が増えます。

- logからの漏えい
- queueに滞留すること
- replay
- cancellationの後の利用

標準の形として、WorkOrderがArtifactGrant IDとbindingを持ちます。そして、Workerが実行の直前に、Worker IdentityとGrantを提示して、transportのaccessを取得する構造を、優先します。

小規模なDeploymentで、directなtokenを埋め込む場合も、WorkOrderやLeaseより短いTTLとscopeを、要求します。

---

# 9. Read path

WorkerがInput Artifactを取得する、標準の流れは、次のとおりです。

~~~text
1. Worker verifies WorkOrder
2. Worker verifies ArtifactGrant binding
3. Worker asks Data Access Broker for read access
4. Broker re-checks:
     Worker ID
     Task / Attempt
     Grant
     Lease / expiry
     operation
     Artifact Record / Data Class
5. Broker resolves Storage Location
6. Worker fetches bytes through Data Plane
7. Worker verifies Content Digest
8. Only verified Artifact is exposed to Executor
~~~

## 9.1 実行の前のDigestの検証

Executor Artifactや通常のInputは、完全性を確認する前に、Executorに渡さないことを、標準にします。

特に、Executable Artifactは、実行の直前の、実際のbytesをDigestに照合します。

## 9.2 大きなstreaming input

非常に大きなArtifactを、全量downloadしてから処理できない場合があります。

その場合は、後続の拡張として、次のものを利用できます。

- chunk manifest
- Merkle tree
- 独立して認証されたchunk
- rangeに範囲を限った、sub-artifact

Whole-objectのDigestしかないのに、未検証のstreamを、Security上「Digest確認済み」とは表現しません。

## 9.3 Range read

Range readを許す場合は、ArtifactGrantが、range accessを許可している必要があります。

部分的に読んだbyte列について、whole-objectのDigestだけから、未取得の範囲まで検証済みとは扱いません。

---

# 10. Upload / Ingest path

Clientや外部のSystemからのArtifactのuploadは、Control Planeのrequestと、Dataの転送とを、分けます。

~~~text
Client
  │ create ingest intent
  ▼
Control Plane
  │ upload authorization / limits
  ▼
Data Plane upload session
  │ bytes
  ▼
quarantine / staging
  │
  ├ content digest / size
  ├ format preflight
  ├ safe processing
  ├ provenance evaluation
  └ policy
  ▼
Artifact Record
~~~

## 10.1 Upload Intent

Control Planeは、概念上、次のことを確定します。

- uploaderのPrincipal
- 圧縮後と展開後の、最大のbyte数
- 期待するmedia type（ある場合）
- 期待するdigest（分かっている場合）
- Data Classの下限
- 許可するingest profile
- expiry
- quotaの予約

## 10.2 Quarantine / staging

upload完了の直後のbyte列を、すぐに、承認済みのTask Inputとして公開しません。

少なくとも、次の状態を区別できる必要があります。Content Identityを確定する前。Safe Processingの前。malwareやparserのpolicyの前。

一つの単純なtrusted=trueというfieldに、集約しません。

## 10.3 宣言されたdigestを信用しない

Uploaderが宣言したDigestは、期待値として利用できます。

最終的なArtifact Content Digestは、信頼できるingestの境界か、Storage AdapterのEvidenceで確認します。

ClientやWorkerの自己申告のDigestだけでは、検証済みのcontentに昇格しません。

---

# 11. Content digestの確定

Uploadの方式によって、Digestを計算する場所は変わり得ます。

## 11.1 Gatewayを経由するstreamed upload

Data Plane Gatewayがbytesを受け取る場合、streamingしながら、Digestとbyte数を計測できます。

## 11.2 Object storeへのdirect upload

ClientやWorkerが、object storeに直接uploadする場合、Control Planeを帯域の経路から外せます。

ただし、Uploaderの自己申告だけでは、Content Digestを確定しません。

Storage Adapterが、次のものをEvidenceとして提供できる場合は、Policyに応じて利用できます。

- 信頼できる暗号学的なchecksum
- 変更できないobjectのmetadata
- 確定したobjectのsize

十分なEvidenceがないbackendでは、Finalizerが再度readして、Digestを確認する必要があります。

## 11.3 確定のstate

Artifact Recordは、次のような、独立したfacetを持てる構造にします。

~~~text
uploaded
content-identified
safe-processing-evaluated
provenance-evaluated
available-for-profile-X
~~~

単一の直線的なstatusだけで、Provenance、Integrity、Safe Processing、Authorizationを、混同しません。

---

# 12. Safe Processing Plane

Archiveの展開、Documentのparse、Imageのdecode、Modelの検査などは、Task本体の外でもResourceを消費し、攻撃を受ける面になります。

Baselineでは、Safe Processingを、Data Plane内の、明示的に上限を設けたworkloadとして扱います。

## 12.1 Budget

少なくとも、必要に応じて、次のものを制限します。

- inputのbyte数
- 展開後のbyte数
- fileの数
- 入れ子の深さ
- CPU time
- Memory
- temp disk
- outputのbyte数
- wall time

## 12.2 Parserの隔離

正式な実装では、ParserやConverterを、Control Planeのmain processに、無制限に組み込みません。

候補は、次のとおりです。

- child sandbox
- container
- WASM
- 専用のparser service

どれを選ぶかは、後続で比べます。

## 12.3 障害の封じ込め

悪意のあるfile一つによる、parserのcrashやOOMが、Task AdmissionやScheduler全体を落とす構造は、標準にはしません。

---

# 13. Output path

Workerのoutputも、untrustedなbytesです。

~~~text
Executor
  │
  ▼
Worker Output Staging
  │ size / local limit
  ▼
authorized Output Upload Session
  │
  ▼
Data Plane Store
  │ digest / size finalization
  ▼
ResultClaim references Output Artifact
  │
  ▼
Verification / Acceptance
~~~

## 13.1 Output Binding

WorkOrderは、任意のoutput URLを持ちません。

Output Bindingは、次のものに結び付けます。

- Task
- Attempt
- Worker
- 最大のoutput size
- 対象のArtifact Recordのscope
- operation
- expiry

## 13.2 Outputのsize

Clientが、巨大なResultを生成して、Quotaを迂回できないよう、複数の境界で制限します。

- Executorのlocalなoutput limit
- Data Planeのupload limit
- Control Planeのbudgetとの突き合わせ

## 13.3 Result acceptanceの前のArtifact

uploadされたOutputが存在することと、Accepted Resultであることとは、別です。

RejectedなResultや、競合するResultのArtifactも、監査と再検証のために、retention policyに従って保持できる必要があります。

---

# 14. Checkpoint path

Checkpointは、Outputと似ています。ただし、再実行に利用されるため、追加の条件があります。

Checkpoint Artifactを再利用する前に、次のことを確認します。

- TaskとExecutorの互換性
- Checkpointのschemaとversion
- Artifact Digest
- Safe Processing
- Data Class
- 生成者のprovenance
- 対象のWorkerのauthorization

Workerがcheckpoint=trueと自己申告するだけで、再実行のInputに昇格することはありません。

Checkpointの再利用は、任意のoptimizationです。

安全に検証できないCheckpointは、捨てて、Taskを再実行できる構造を、優先します。

---

# 15. Cache architecture

## 15.1 CacheはOptimizationである

Cache hitは、Security Decisionではありません。

Schedulerは、cache localityをPlacementのcostに利用できます。ただし、Workerは、実際のbyte列をDigestに対して検証します。

## 15.2 Workerのlocal cache

Worker Agentの下にあるcacheは、次のものを管理できます。

- Content Digest
- 検証済みのsize
- localのpathやobject handle
- 最後に検証した時刻
- security scope
- 任意の、encryptionのstate

Clientは、cacheのpathを指定できません。

## 15.3 管理されたsite cache

Officeやsite単位で、共用のcacheを置くことは、WANのtrafficを減らすのに有効です。

ただし、enterpriseの既定では、任意のWorker同士を、P2Pのrelayにはしません。

管理されたsite cacheは、明示的なData Planeのcomponentとして、次のものを持ちます。

- identity
- policy
- quota
- audit
- data class

## 15.4 P2P

任意のworker間の直接の転送は、標準のEnterprise Profileでは、無効です。

将来、明示的なtrusted profileで、peer transferを導入する余地は残します。ただし、次の問題を、別に解決する必要があります。

- Grantの強制
- peerのidentity
- topologyの漏えい
- cache poisoning
- trafficの増幅
- NATとfirewall
- revocation

---

# 16. Cacheのsecurity scope

同じDigestだからといって、すべてのTaskが、同じcache entryを読めるとは限りません。

少なくとも、次の区別を表現できる必要があります。

- publicでcacheしてよい
- tenantの範囲
- organizationの範囲
- confidentialで、共有cacheを使わない
- 使い捨て
- executor専用

ConfidentialなArtifactを、アドホックなWorkerや、公開のBrowser cacheに置かないことを、Policyで表現します。

## 16.1 Cacheの存在のside channel

Clientには、

> digest Xは、Worker Yにcache済み

という生の情報を返しません。

Scheduler内部でも、必要以上に、正確なtopologyを広げません。

Cache hitとmissのlatencyを通じた、tenantをまたぐ存在の漏えいも、QualityとSecurityのレビューの対象です。

---

# 17. Locality Observation

Schedulerに渡すLocalityの情報は、AuthorityではなくObservationです。

概念上の例は、次のとおりです。

~~~text
Artifact Digest
Worker / Site
state = cached / likely / missing / unknown
estimated missing bytes
estimated fetch millis
confidence
observed at
source
~~~

Workerの自己申告のcache claimは、保証の低いsourceとして扱います。

管理されたcacheや、Artifact Storeのtelemetryなど、sourceに応じて、confidenceを区別できます。

どのObservationでも、Artifactへのaccess authorityは作りません。

---

# 18. Network Budget

RQ-008とRQ-017に従い、Networkの転送を、Resource Budgetの一部として扱います。

Task、Principal、Provider、Grantの単位で、必要に応じて、次のものを制限できる構造にします。

- inputのbyte数
- outputのbyte数
- retryの転送のbyte数
- prefetchのbyte数
- 同時の転送
- egressの金銭的なbudget
- bandwidth class

## 18.1 予約

Placementの前に、足りないinputと、期待されるoutputから、Network Budgetを予約できます。

見積もりより、実際の転送が増えた場合は、Data Plane側で、hard limitを強制できます。

## 18.2 Retry

Retryで、同じArtifactを再取得した場合も、元のTaskのNetwork Budgetを消費します。

Retryによって、新しい無制限のBudgetを得ることはありません。

## 18.3 ProviderとsiteのBudget

業務のNetworkを守るために、Providerやsite側にも、ローカルのtraffic limitを置けます。

Central SchedulerのTaskのpriorityが高くても、ローカルのnetwork policyを越えません。

---

# 19. 転送のaccounting

Data Planeは、少なくとも、次のものを計測できる余地を持ちます。

- 要求されたbyte数
- 実際のbyte数
- cacheのhitとmiss
- sourceとdestinationのclass
- 転送の所要時間
- retryとresumeのbyte数
- outputのbyte数
- rejectした転送
- throttleの理由

Contribution用のResource Measurementと、Operational Telemetryは、目的が異なります。そのため、同じCounterでも、AssuranceとObserverを区別します。

---

# 20. Mass reclaimのときのData Plane

大量のWorkerが同時に離脱すると、retryだけでなく、Artifactの再転送もstormを起こします。

標準のRecoveryでは、次のものを組み合わせます。

1. Taskのretry budget
2. retryのadmission window
3. Artifactの転送のbudget
4. storeごとのconcurrency
5. siteごとのbandwidth
6. cache locality
7. backoffとjitter
8. priority

## 20.1 重複した転送の抑制

同じWorkerやsiteに、同じDigestを多数のTaskが必要とする場合、Data Planeは、可能であれば、single-flightやtransfer coalescingを利用できます。

Taskごとに、同じ8 GiBのModelを、同時にdownloadすることを避けます。

## 20.2 Recoveryの優先順位付け

すべてのTaskを同時に再配置するのではなく、次のものでRecovery windowを制御します。

- deadline
- Taskのpriority
- 転送のコスト
- 利用できるcache
- Resultの価値
- retry budget

UserやProviderのreclaim自体を、遅らせることはしません。

---

# 21. Prefetch

Prefetchは、独立したOptimization Policyです。

通常のTaskのArtifact access authorityから、

> 将来使うかもしれないので、配ってよい

という権限を、自動的に生成しません。

Prefetchには、別に、次のものを適用します。

- 許可するArtifact class
- destination class
- Network Budget
- Storage Budget
- 期待される再利用
- expiry
- eviction policy
- userとproviderのpolicy

Business Endpointに、巨大なModelを無差別に配布しません。

---

# 22. Data Classのpropagation

OutputのData Classを、Workerの自己申告で下げません。

Baselineでは、原則として、次のように、安全な側へpropagationする構造を作れるようにします。

~~~text
output class
  >= Task effective Data Class
  >= relevant input Data Class
  >= Executor output policy floor
~~~

## 22.1 Declassification

汎用の、自動的なdeclassificationは、Baselineに含めません。

匿名化、集計、変換などによって、Data Classを下げる場合は、後続の拡張として、次のものを必要とします。

- 承認されたdeclassification Executor
- 明示的なPolicy
- validation
- audit
- 専用のAuthority

「Executorがpublicなoutputだと言った」だけでは、下げません。

---

# 23. Encryption

## 23.1 通信中

ControlとData Planeのtransportの、機密性と完全性は、企業の標準に適合する暗号化された通信を、前提とします。

具体的なTLS profileは、後続の実装で定義します。

## 23.2 保存時

Storage backendの、保存時の暗号化を利用できます。

ただし、保存時の暗号化があるかどうかと、Artifactのcontent integrityとは、別です。

## 23.3 アプリケーションまたはclient側の暗号化

高いConfidentialityが求められる場合には、将来、次の構成を利用できます。Data Planeがciphertextだけを保持し、承認された実行環境にだけ、keyをreleaseする構成です。

これは、Remote AttestationとKey Releaseと連携する、任意のprofileです。

Baselineの、managed deploymentの必須条件にはしません。

---

# 24. Secretを、一般のArtifact Cacheに入れない

API key、Databaseのpassword、private signing materialなどを、通常のArtifactとしてcacheしません。

Secretへのaccessは、Artifact Data Planeと似たCapability modelを使えます。ただし、次のものを標準とする、別のSecret PlaneやAdapterとして扱います。

- Workerに永続的なcacheを持たない
- Task、Attempt、Executorに、狭く結び付ける
- より短いexpiry
- contributionやlocalityのoptimizationの対象にしない
- 伏せ字にしたaudit

Secretのbytesを、Schedulerに渡しません。

---

# 25. Data Plane credentialの有効期間

Transport Credentialの有効期限は、原則として、依存するDecisionより長くしません。

~~~text
transport credential expiry
  <= ArtifactGrant expiry
  <= WorkOrder / Lease relevant lifetime
~~~

ただし、大容量の転送で、Leaseより長いsessionが必要な場合は、renewalを明示的に設計します。

古いdownload URLを、長期間再利用できる状態は、避けます。

---

# 26. Control Planeの停止

Data Planeへのaccessを、1 byteごとにControl Planeに同期して依存させると、Control Planeが停止したときに、進行中の転送まで、すぐに壊れます。

一方、長い有効期間のCredentialを発行すると、revocationのlatencyが増えます。

Baselineでは、このTrade-offを、次のように明示します。

- 短い有効期間のaccess
- TTLに上限を設ける
- renewal
- 機微性の高いprofileでは、onlineでmediationできる
- すでに渡したbytesは、revocationできない

Control Planeが停止した後も、すでに発行したCredentialは、expiryまで有効な場合があります。

これを、「即時のrevocation」とは表現しません。

---

# 27. CancellationとData access

Task cancellationの後は、次のことを行います。

- 将来のArtifactGrantの発行を止める
- 対応している場合は、既存のGrantをrevokeする
- transport credentialのrenewalを止める
- upload sessionを止める
- Workerに、停止を要求する

しかし、悪意のあるWorkerが、すでにdownloadしたDataを、

> cancellationしたので、削除された

とは保証しません。

Confidentialなdataは、そもそも、適切なWorker Assuranceと環境にだけ配置することが、基本です。

---

# 28. Artifactのretentionとdeletion

Artifactの削除では、少なくとも、次のものを分けます。

- 論理的な参照の削除
- Artifact Recordのretention
- 物理的なobjectの削除
- cacheのeviction
- 外部のstoreのretention

Content Digestが同じ、別のRecordが存在する場合、一つのRecordを削除して、他のRecordまで消してはいけません。

## 28.1 Accepted Result

Accepted Resultが参照するArtifactは、Resultのretention policyと整合するように、保持します。

## 28.2 受理されなかったResult

RejectedやfailedなAttemptのOutputも、debug、audit、security investigationのために、短い期間、保持する場合があります。

## 28.3 Workerのcopy

Workerに渡したbyte列を、あとから削除させることは、malicious Workerに対して、完全には保証できません。

---

# 29. FederationのData Plane

Identity Federationが成立しても、Dataの共有は、自動的には成立しません。

外部のOrganizationにArtifactを渡すには、別に、次のものを満たす必要があります。

- BeneficiaryとPrincipal
- Data Class
- Grantのscope
- 目的とTask
- expiry
- 転送先
- export policy

~~~text
Federated identity valid
  != Artifact export authorized
~~~

## 29.1 組織をまたぐstorage

Federationでは、次のような方式から選べます。

- Providerのstoreから、Beneficiary Workerへ、直接read
- 中立の、共有のobject store
- 制御されたexportのcopy
- 暗号化したArtifactの転送

Architecture Coreは、一つの方式に固定しません。

## 29.2 公益向けのCompute Grant

Compute Resource GrantとData Grantを分けます。

Cloud Providerが、公益団体にComputeを提供していても、そのWorkerに、Organization内部のData accessが、自動的に付くことはありません。

---

# 30. Browser / アドホックなWorker

Browser Workerは、Data Planeの上でも、保証の低いprofileとして扱います。

標準では、次のことを基本にします。

- publicや、機微性の低いArtifact
- 短い有効期間のGrant
- 小さく、上限のある転送
- 永続的なsecretを持たない
- 制限されたcache
- 任意の内部のendpointに到達しない
- 積極的なexpiry
- 明示的な参加

Browserのstorage APIに、Artifactが残る可能性を考慮して、confidentialなDataを、標準では配置しません。

---

# 31. Executor Artifact path

Executor Artifactも、Data Planeを通します。

Workerは、論理的なExecutor名から、外部のregistryを自由に解決しません。

標準の流れは、次のとおりです。

~~~text
ExecutorApproval
  │ exact Digest
  ▼
ArtifactGrant / Executor Distribution
  │
  ▼
Worker local verified cache
  │ actual digest re-check
  ▼
Sandbox execution
~~~

変更できるtagは、Admissionのときに、Digestに解決されます。実行時には、Digestが正本です。

---

# 32. ModelとDatasetの再利用

AIや認知のworkloadでは、巨大なModelやDatasetを、繰り返し使う可能性があります。

EKIでは、次のものを分離します。

- model artifactのidentity
- modelの承認
- modelのData Classとlicenseのmetadata
- Workerのcache
- locality observation
- prefetch policy

Modelをcacheしているという理由だけで、そのWorkerが、そのModelを利用する権限を持つとは限りません。

Licenseや契約上の制約も、Artifact policyのextensionとして扱える余地を持ちます。

---

# 33. Data PlaneとContribution

Contribution accountingで、networkやstorageの資源を扱う場合も、Operational Telemetryから、自動的にCreditを生成しません。

Data Plane Observerは、次のものを、Measurement Evidenceとして提供できます。

- 転送したbyte数
- cacheが提供したbyte数
- storageの時間

Contribution Authorityが、AssuranceとPolicyを確認した後に、Recordにします。

---

# 34. Audit

Data Planeの重要なEventは、次のとおりです。

- ingest intentの発行
- uploadの開始、完了、reject
- content digestの確定
- safe processingの結果
- Artifact Recordが利用可能になったこと
- ArtifactGrantの発行、拒否、revoke
- transport accessの発行
- downloadの完了と中断
- cacheのfillとeviction
- outputの確定
- retentionとdeletionの判断
- ドメインをまたぐexport

これらを、Task、Attempt、Artifact、Principal、Workerに関連付けられる必要があります。

Transport Credential、presigned URL、Secretなどを、Audit logに平文で記録しません。

---

# 35. Observability

Data Planeのmetricsは、少なくとも、次のものを取得できる余地を持ちます。

- 転送のbyte数
- 転送の所要時間
- 進行中の転送の数
- cacheのhit率
- 重複した転送の割合
- ingestのrejectの数
- safe processingのresourceの使用量
- outputのrejectとlimitの数
- storeのerror率
- siteごとのnetworkの飽和
- reclaimからの回復による増幅

OpenTelemetryやOTLPなどにexportできるようにします。ただし、具体的なTelemetry backendは固定しません。

---

# 36. 失敗のsemantics

## 36.1 Storageが利用できない

Taskそのものを、invalidとはしません。

別のlocation、cache、retryの可能性を、Task Stateが評価します。

## 36.2 Grantが拒否された

Placementの候補が、Dataの要件を満たせないため、そのAttemptとPlacementを再評価します。

Securityの要件を下げて、通すことはしません。

## 36.3 Digestが一致しない

ArtifactをExecutorに渡しません。

Cache entryであれば無効にして、sourceと転送の経路を、Auditの対象にします。

## 36.4 Uploadが中断された

Upload sessionをresumeできるようにするかは、Adapter次第です。

未完了のuploadを、Artifact Recordとして利用可能にはしません。

## 36.5 Safe processingの失敗

Content Identityが分かっていても、承認済みのinputにはなりません。

失敗の理由とResource Budgetを記録します。

## 36.6 Output budgetを超過した

Executorとuploadを止めて、Attemptの失敗として扱います。

巨大なOutputをControl Planeに返してから、rejectすることはしません。

---

# 37. Storage locationの障害と、Artifact identity

Artifactが、一つのStoreから消えても、Artifact Identityまで消えたことにはなりません。

Artifact Recordは、複数のLocationを参照できます。

Replicationとdurabilityのpolicyは、Artifact ClassやDeployment Profileに応じて、選びます。

EKIは、永続的なsource-of-truthのdatabaseそのものではありません。そのため、Applicationが所有するsource dataのdurabilityを、Fabricが自動的に引き受けるとは限りません。

---

# 38. アプリケーションが所有するData

すべてのDataを、EKI Artifact Storeにcopyする必要はありません。

アプリケーションが所有するstorageを利用する場合は、次のものを定義します。

- 登録済みのStorage Adapter
- 明示的なArtifactとobjectの参照
- Data Authority
- 範囲を限ったaccess
- 完全性への期待
- locality
- retentionの所有者

Workerに、Databaseのmaster credentialを渡して、Application storageを自由に探索させることはしません。

必要であれば、Application側のAdapterが、限定されたqueryやobjectの取得を提供します。

---

# 39. External Adapter

TaskがExternal APIや外部のSystemとDataをやり取りする場合は、任意のnetwork accessではなく、登録済みのExternal Adapterを利用できます。

External Adapterは、次のものを持ちます。

- 許可するdestination
- requestのschema
- credentialの扱い
- rate limit
- side-effectのsemantics
- receiptと確認
- audit

これによって、Worker sandboxに広いInternetのaccessを与えずに、外部と連携できます。

---

# 40. Network policy

Workerのnetwork capabilityは、default denyを基本とします。

許可する類型は、次のとおりです。

1. Control Planeのendpoint
2. Data Planeのendpoint
3. 登録済みのExternal Adapter
4. 明示的に承認されたnetwork profile

Data Planeのaccess先も、Grantから解決します。Clientが指定したdestinationを、そのままallowlistにすることはしません。

---

# 41. SiteとRegionのaffinity

EnterpriseやFederationでは、Dataをsiteやregionの外に出せない場合があります。

Artifact RecordとPolicyは、次のものを表現できる余地を持ちます。

- 許可するregion
- 許可するsite
- 許可するtrust domain
- egressの禁止
- residencyの要件

Schedulerのlocality scoreより前に、hard constraintとして適用します。

---

# 42. Cost modelへの入力

Control PlaneとData PlaneからSchedulerに提供するcostの入力は、Authorityを持たないObservationです。

たとえば、次のものです。

~~~text
missing input bytes
expected output bytes
estimated download time
estimated upload time
cache confidence
egress cost class
site network pressure
store availability
~~~

learned estimatorを利用しても、Data access、residency、Securityの制約を、上書きしません。

---

# 43. 小規模なdeployment

10台程度の、管理されたendpoint poolでは、次のような単純な構成を認めます。

~~~text
eki-control
  metadata / authority
  data-access resolver

local object store
  artifact bytes

endpoint agents
  verified local cache
~~~

Control PlaneとData Access Resolverを、同じBinaryにしてもかまいません。

専用のCDN、Kafka、Service Mesh、HSMを、必須にはしません。

---

# 44. Enterprise deployment

Enterpriseでは、必要に応じて、次のものを利用します。

- 冗長なControl Plane
- 専用のobject store
- site cache
- 企業のPKIとIdP
- KMSとHSM
- egressの制御
- outbound-onlyのWorker
- proxyに対応した転送
- regionごとのstorage
- security monitoring

Workerは、原則として、outboundの接続で、ControlとDataのendpointに到達できる構成を、優先します。

---

# 45. CloudとPublic-interestのprovider

Cloud Providerなどが、外部のCompute Grantを提供する場合、Provider側のData Planeに、Beneficiaryのdataを置くかどうかは、別のPolicyです。

Computeだけを提供し、DataはBeneficiaryが管理するStoreから、短い有効期間のGrantで取得する構成も、可能です。

Contribution Recordは、ComputeとTransferのEvidenceを記録できます。ただし、Data sharingの契約の代わりにはなりません。

---

# 46. Browser deployment

Browserでの参加では、次のことを基本にします。

- HTTPSのorigin
- 短い有効期間のWorker identity
- 上限を設けたfetch
- publicなArtifactのCDNとobject store
- 内部のtopologyを、生では見せない
- 長い有効期間のcloud secretを持たない
- タブを閉じることやsuspensionを前提にする

Service Workerなどのバックグラウンドの挙動を、安定したavailabilityとして扱いません。

---

# 47. 非目標

このBaselineは、次のものを、めざしません。

- 分散共有filesystem
- 任意のWorker間のP2P network
- global coherentなcache
- 分散database
- object storeの置き換え
- CDNの置き換え
- database replicationのsystem
- 汎用のsecret manager
- 無制限の、直接のInternet access
- tenantをまたぐdedupを、Security propertyとして利用すること
- 悪意のあるWorkerに対して、Dataの削除を完全に保証すること

---

# 48. Architecture Invariants

Control / Data Planeで維持する不変条件を、まとめます。

1. Control Planeのmetadata pathに、bulk Artifactを標準では集約しない。
2. Content Digestは、Access Authorityではない。
3. Artifact RecordのAuthorizationとClassificationを、Digestだけで統合しない。
4. Storage Locationを知るだけでは、Data access authorityを得ない。
5. ArtifactGrantは、Task、Attempt、Worker、operation、expiryに範囲を限る。
6. Workerに、長い有効期間のstorage credentialを配らない。
7. Executorに渡す前に、Artifact Integrityを確認する。
8. Safe Processingの前のArtifactを、承認済みのinputと同一視しない。
9. Cache localityは、Optimizationであり、Security Evidenceではない。
10. Clientに、内部のtopologyとcacheの一覧を、不必要に露出しない。
11. Retryの転送も、同じResource Budgetに計上する。
12. mass reclaimで、際限のない重複した転送を起こさない。
13. Outputは、認可されたsinkとsizeの上限を持つ。
14. OutputのData Classを、Workerの自己申告で下げない。
15. Secretを、通常のArtifact cacheに置かない。
16. Federation Identityだけで、Data Export Authorityを作らない。
17. Browserやアドホックなworkerに、confidentialなdataを、標準では配置しない。
18. cancellationの後も、すでに送ったDataの事後の削除を、保証しない。
19. 変更できるaliasを、実行時のContent Identityにしない。
20. 任意のWorker側のfetchを、標準の動作にしない。

---

# 49. 次のWork Unitへの入力

次は、Deployment Profilesを、Architectureの正本として具体化します。

この文書から引き継ぐものは、次のとおりです。

- 論理的なAuthorityは、Deploymentの規模によらず維持する。
- Control PlaneとData Planeの、論理的な分離を維持する。
- 小規模なdeploymentでは、Processを統合できる。
- enterpriseでは、site cache、KMS、proxyなどを追加できる。
- browserとアドホックは、保証の低いprofileとする。
- federationでは、IdentityとData Exportを分離する。
- public-interestのgrantでは、Compute GrantとData Grantを分離する。

Deployment Profileの後に、Task、Attempt、Lease、Recoveryのstate modelと、Extension SPIに進みます。

# Artifact取込時の安全境界

## この文書の役割

この文書は、EKI Fabricが外部Clientや他組織からArtifactを受け取るとき、内容の完全性だけでは防げない攻撃面を整理するための調査メモです。

Product RequirementやArchitectureの正本ではありません。ZIP、TAR、OCI Artifactなど特定形式の採用を決めるものでもありません。

## 1. Content Digestだけでは安全にならない

EKIでは、Task input、Executor、Resultなどの主要Artifactについて、保存先や転送経路に依存せず内容を識別できることを要求しています。

これは改ざん検知には有効ですが、**そのArtifactを処理して安全かどうか**までは示しません。

たとえば、攻撃者が自分で作ったZIP bombを正しくSHA-256で識別して送った場合、Digestは完全に正しくても、展開時に大量のDiskやMemoryを消費させられます。

そのため、次を分けて考える必要があります。

```text
Artifact identity / integrity
        !=
Artifact processing safety
```

## 2. 展開後サイズは入力サイズから推測できない

圧縮Artifactでは、小さな入力が非常に大きな展開結果になることがあります。

OWASP Web Security Testing Guideでは、ZIP bomb / decompression bombを、展開時にDiskやMemoryを枯渇させるDoS手法として扱っています。

EKIではClientも悪意を持つ前提なので、Artifact sizeを「転送されたbyte数」だけで制限しても不十分です。

必要になる境界の例:

- 展開後の総byte数
- 展開されるfile数
- directory階層の深さ
- 再帰的なarchive展開回数
- parserが使えるCPU時間
- parserが使えるMemory
- 一時Disk量

具体的な上限値はArchitectureや運用Policyで決めますが、**変換後・展開後の増幅量もboundedでなければならない**ことはRequirementとして持つ価値があります。

## 3. Path TraversalとSymlink

ArchiveをWorker側で展開するとき、Artifact内のpathをそのままhost filesystemへ反映すると、展開先の外へ書き出される危険があります。

典型的には次のようなpathです。

```text
../../../../tmp/file
```

また、Archive内のsymbolic linkがhost上の機密fileを指す場合、そのlinkを後続処理がたどることで意図しないfile readが起こることがあります。

OWASP WSTGも、Archive Directory TraversalとArchive Symlink Attackを別々の攻撃として扱っています。

EKIでは、Artifactを受け取ったこと自体から、Worker hostの任意pathへ書き込むAuthorityが生まれてはいけません。

## 4. Parserそのものが攻撃面になる

危険なのはArchive展開だけではありません。

EKIで扱う可能性がある入力には、たとえば次があります。

- PDF
- Office文書
- 画像
- 音声
- 圧縮file
- model file
- source tree
- 独自業務data

これらを解釈するParserに脆弱性があれば、正しい形式のArtifactでも攻撃に使われる可能性があります。

したがって「ArtifactをSandbox Executorへ渡したから安全」と単純化せず、取込・展開・検査・変換の各段階をresource-boundedにする必要があります。

## 5. Workerへの負荷転嫁を防ぐ

悪意あるClientが巨大Artifactを送ったとき、Control Plane側では小さなmetadataしか扱わず、実際の展開コストをWorkerへ押し付ける構成も危険です。

Workerが多数存在すると、一つのArtifactを複数Workerへ配布することで攻撃が増幅される可能性もあります。

```text
1 malicious Artifact
        ↓
10 replicated Attempts
        ↓
10 workersで同時に展開
```

そのため、Artifact処理コストはTask budget、retry budget、replication budgetと分離せず扱う必要があります。

## 6. EKIへの要求候補

第三巡目では、少なくとも次を要求へ反映する価値があります。

1. Content Digestが正しいことと、処理して安全であることを分離する
2. Artifactの展開・解析・変換は、CPU、Memory、Disk、file数、展開後sizeなどについてboundedであること
3. Archive pathやsymbolic linkからWorker hostの許可範囲外へ到達できないこと
4. Artifact前処理によるresource consumptionもTask / Principalの予算へ含められること
5. replicationやretryによってArtifact処理コストが無制限に増幅しないこと

## 参考資料

- OWASP Web Security Testing Guide — Test Upload of Malicious Files
  - https://wstg.owasp.org/latest/4-Web_Application_Security_Testing/10-Business_Logic_Testing/09-Test_Upload_of_Malicious_Files/

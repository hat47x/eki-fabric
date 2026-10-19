# Artifact取込と展開処理の安全性

## この文書の役割

この文書は、Clientや外部主体から受け取るArtifactをEKI Fabricが安全に取り扱うための要求を検討する調査メモです。

Product RequirementやArchitectureの正本ではありません。ZIP、TAR、OCI Artifactなど特定形式の採用を決めるものでもありません。

## 背景

EKIでは、未知のOSSや外部主体からTaskとArtifactを受け入れることを想定しています。そのため、Artifactは「正しい形式の入力ファイル」であるとは限りません。

攻撃者は内容を壊すだけでなく、Parserの脆弱性、極端な圧縮率、巨大な展開後サイズ、過度に深い入れ子、path traversal、symbolic link、過大なmetadataなどを利用して、CPU、Memory、Disk、inode、Process、Networkなどを消費させる可能性があります。

この問題はTask実行時だけでなく、Artifactを受理・検査・展開・索引付けする前処理の段階でも発生します。

## まず三つの保証を分ける

Artifactについて、少なくとも次の三つを同一視しないことが重要です。

```text
Provenance
  誰が・どのBuildから作ったか

Content Integrity
  受け取ったbyte列が期待したものか

Safe Processing
  現在のParser / Runtimeで
  許可されたResourceとAuthorityの範囲内に処理できるか
```

たとえば、攻撃者が自分で作ったdecompression bombを正しいDigest付きで送った場合、Content Integrityは完全に成立しています。それでも処理は危険です。

GitHub Artifact AttestationのようなSupply-chain provenanceも有用ですが、Artifact自体に危険な入力が含まれていないことまで自動的に保証するものではありません。

## 1. 「受け取れた」と「安全に処理できる」を分ける

Digestが正しいことは、そのArtifactが安全であることを意味しません。

同様に、Artifact Attestationや署名によって出所を確認できても、内容に脆弱な入力が含まれていないことまでは保証されません。

この境界はExecutor Artifactだけでなく、Task input、Result、Model、Documentなどにも適用します。

## 2. 展開前の受信サイズだけでは最大影響を表せない

圧縮Artifactでは、小さな入力が非常に大きな展開結果になることがあります。

OWASP Web Security Testing Guideでは、ZIP bomb / decompression bombを、展開時にDiskやMemoryを枯渇させるDoS手法として扱っています。

そのため、受信byte数だけをResource Budgetに使うと不十分です。

制約対象の例は次のとおりです。

- 展開後の総byte数
- 展開されるfile数
- directory階層の深さ
- nested archiveの深さ
- parserが使えるCPU時間
- parserが使えるMemory
- temporary Disk量
- wall time

形式上、事前に正確な展開量を求められない場合もあります。そのため、事前検査だけに依存せず、streaming処理と実行中のhard limitを組み合わせられることが重要です。

具体的な上限値はArchitectureや運用Policyで決めます。

## 3. Archive pathをHost authorityへ変換しない

Archive内に次のようなentryがあっても、展開先Sandboxの外へ書き込めてはいけません。

```text
../../../../tmp/file
```

OWASP WSTGでもArchive Directory Traversalを独立した攻撃として扱っています。

同様に、Archive内のsymbolic linkがHost上の機密fileを指している場合、そのlinkを後続処理がたどることで意図しないfile readが起こる可能性があります。

したがって、Clientがfile名やpathを指定しただけでHost filesystem上のAuthorityを得ない、というRQ-029と同じ原則をArtifact展開にも適用します。

## 4. Parser自体を信頼境界として扱う

EKIで扱う可能性がある入力には、たとえば次があります。

- PDF
- Office文書
- 画像
- 音声・動画
- 圧縮file
- model file
- source tree
- 独自業務data

これらを解釈するParserに脆弱性があれば、形式として正しいArtifactでも攻撃に使われる可能性があります。

EKI Core内の高権限Processが、無制限にすべての形式をparseする構造は避けるべきです。

具体的なSandbox方式はArchitectureで検討しますが、少なくともParser failureがControl Plane全体の障害や権限拡張へ直結しないことを要求する価値があります。

## 5. 安全検査もResource Budgetの外へ逃がさない

Malware scan、MIME判定、thumbnail生成、indexing、content extractionなども計算資源を使います。

「Task実行前の安全検査だからquota外」とすると、Artifact uploadだけでFabric資源を消費させるDoSが成立します。

Ingress / inspection / conversion costもResource Budgetの一部として扱える必要があります。

## 6. Replication / Retryによる増幅も考える

一つの悪意Artifactが複数Attemptへ配布されると、前処理コスト自体が増幅される可能性があります。

```text
1 malicious Artifact
        ↓
10 replicated Attempts
        ↓
10 Workersで同時に展開・parse
```

そのため、Artifact処理コストはTask Budget、Retry Budget、Replication Budgetと切り離さず扱う必要があります。

## Requirementへの反映

第三巡目では、次を`RQ-050 — Bounded artifact ingestion and expansion`としてProduct Requirementsへ反映しています。

- Artifactの受理・検査・展開・変換もresource-boundedであること
- compressed sizeだけでなくexpanded size、file count、nesting、処理時間等を制約できること
- Artifact pathやarchive entry名だけでHost filesystem authorityが成立しないこと
- Artifact前処理コストもTask / Principal Budgetへ含められること
- Provenance、Content Integrity、Safe Processingを別の保証として扱うこと

Threat Modelでは、malicious Artifact / parser abuseを独立した攻撃面として扱います。

## Architectureへ持ち越すもの

- Parserをprocess、container、WASM等のどこへ隔離するか
- compressed / expanded sizeの具体的な上限
- nested archiveを許可する範囲
- symbolic linkの扱い
- Malware scan等の位置付け
- Artifact検査結果をcacheできる条件

## 参考資料

- OWASP Web Security Testing Guide — Test Upload of Malicious Files
  - https://wstg.owasp.org/latest/4-Web_Application_Security_Testing/10-Business_Logic_Testing/09-Test_Upload_of_Malicious_Files/
- GitHub Artifact Attestations
  - https://docs.github.com/en/actions/concepts/security/artifact-attestations
- GitHub Artifact Attestation verification
  - https://docs.github.com/en/actions/how-tos/secure-your-work/use-artifact-attestations/use-artifact-attestations

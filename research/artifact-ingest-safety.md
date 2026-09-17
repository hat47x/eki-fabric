# Artifact取込と展開処理の安全性

## 役割

この文書は、Clientや外部主体から受け取るArtifactをEKI Fabricが安全に取り扱うための要求を検討する調査メモです。Product RequirementやArchitectureの正本ではありません。

## 背景

EKIでは、未知のOSSや外部主体からTaskとArtifactを受け入れることを想定しています。そのため、Artifactは「正しい形式の入力ファイル」であるとは限りません。

攻撃者は、内容を壊すだけでなく、Parserの脆弱性、極端な圧縮率、巨大な展開後サイズ、過度に深い入れ子、path traversal、過大なmetadataなどを利用して、CPU、Memory、Disk、inode、Process、Networkなどを消費させる可能性があります。

この問題はTask実行時だけでなく、Artifactを受理・検査・展開・索引付けする前処理の段階でも発生します。

## 基本原則

### 1. 「受け取れた」と「安全に処理できる」を分ける

Digestが正しいことは、そのArtifactが安全であることを意味しません。

同様に、Artifact Attestationや署名によって出所を確認できても、内容に脆弱な入力が含まれていないことまでは保証されません。GitHubのArtifact Attestation documentationでも、Attestationはbuild provenanceを示すものであり、Artifact自体の安全性を保証するものではないとされています。

### 2. 展開前に最大影響を見積もる

圧縮ファイルや複合形式では、受信サイズだけをresource budgetに使うと不十分です。

たとえば数MBの圧縮ファイルが数十GBへ展開される場合があります。展開後サイズ、file count、directory depth、nested archive depthなどを上限として扱える必要があります。

形式上、事前に正確な展開量を求められない場合もあるため、streaming処理と実行中のhard limitを組み合わせることが重要です。

### 3. Parser自体を信頼境界として扱う

画像、PDF、Office文書、動画、圧縮ファイルなどのParserは、外部入力を直接扱います。

EKI Core内の高権限Processが、無制限にすべての形式をparseする構造は避けるべきです。具体的なSandbox方式はArchitectureで検討しますが、少なくともParser failureがControl Plane全体の障害や権限拡張へ直結しないことを要求する価値があります。

### 4. PathをAuthorityにしない

Archive内に`../../...`やabsolute pathが含まれていても、展開先Sandboxの外へ書き込めてはなりません。

Clientがファイル名やpathを指定しただけでHost filesystem上のauthorityを得ない、というRQ-029と同じ原則をArtifact展開にも適用します。

### 5. 検査処理そのものにも予算を持たせる

Malware scan、MIME判定、thumbnail生成、indexing、content extractionなども計算資源を使います。

「Task実行前の安全検査だからquota外」とすると、Artifact uploadだけでFabric資源を消費させるDoSが成立します。Ingress / inspection costもResource Budgetの一部として扱える必要があります。

## Artifact provenanceとの関係

GitHub Artifact Attestationは、ArtifactのBuild provenanceとsubject digestを関連付け、消費側がそれを検証する仕組みを提供しています。

EKIでも、Executor Artifactについて出所とDigestを確認する考え方は有用です。ただし次を分けます。

```text
Provenance / Signature
  誰が・どのBuildから作ったか

Content Digest
  受け取ったbyte列が期待したものか

Safe Processing
  その内容を現在のParser / Runtimeで安全に扱えるか
```

三つは相互補完的ですが、代替関係ではありません。

## Requirementへの反映候補

- Artifactの受理・検査・展開・変換もresource-boundedでなければならない。
- compressed sizeだけでなくexpanded size、file count、nesting等の最大影響を制限できなければならない。
- Artifact pathやarchive entry名だけでHost filesystem authorityが成立してはならない。
- Parser / converter failureがControl Plane全体へ無制限に波及してはならない。
- Provenance、Content Integrity、Safe Processingを別の保証として扱う。

## 参考

- GitHub Artifact Attestations: https://docs.github.com/en/actions/concepts/security/artifact-attestations
- GitHub Artifact Attestation verification: https://docs.github.com/en/actions/how-tos/secure-your-work/use-artifact-attestations/use-artifact-attestations

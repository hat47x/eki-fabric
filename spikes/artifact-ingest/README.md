# Artifactの安全な取り込みを確かめる（Ingest Safety Spike）

## このSpikeの役割

EKI Fabricでは、Clientや外部組織から受け取るArtifactを、そのまま安全な入力とはみなしません。

Content Digestや署名が正しくても、展開・解析時に大量のResourceを消費したり、展開先の外へ書き出したり、symbolic linkを通じてHost上の別fileを参照したりするArtifactは作れます。

このSpikeでは、RQ-050で要求している **ArtifactのProvenance / IntegrityとSafe Processingを分ける** という考え方を、まずZIP archiveで小さく実証します。

正式なArtifact subsystemや対応formatを決めるものではありません。

## 今回の対象

`safezip/` に標準libraryだけを使った最小実装を置いています。

初期境界は次です。

- file数
- 1 fileあたりの展開後byte数
- archive全体の展開後byte数
- path深度
- compression ratio
- directory traversal
- Windows系backslash traversal
- drive-qualified path
- symbolic link
- special file
- 既存destinationへの上書き
- streaming中に実測した展開byte数
- 実展開時の必須有限limit（設定漏れを無制限として扱わない）
- `a/../b`のように正規化で別名になる曖昧なpath

## なぜ事前検査だけでは足りないか

Goの`archive/zip`では、各entryについて`CompressedSize64`と`UncompressedSize64`を参照できます。

これは事前のbudget判定に役立ちます。

しかし、EKIではClientを悪意あるものとして扱います。

したがって、

> headerに書かれた展開後sizeが上限内だった

という一つの確認だけにResource Safetyを依存させません。

初期実装では、

```text
central directoryを使った事前検査
        +
実際にdecompressしながらbyte数を制限
```

の二段階にしています。さらに、実際に展開する`Extract`では、file数、archive全体の展開後byte数、1 fileの展開後byte数、path深度について有限の上限を必須とします。設定漏れを「無制限でよい」という意味にはしません。

なお、このSpikeは`zip.Reader`を受け取った後の境界を検証しています。圧縮済みarchive自体の受信byte数やcentral directoryを読み込む前段の負荷は別途Ingress側で制限する必要があり、この点はまだ本Spikeの実装範囲外です。

## Pathを文字列のまま信用しない

OWASP Web Security Testing Guideでも、archive展開時のdirectory traversalとsymbolic linkは独立した攻撃面として扱われています。

たとえば、

```text
../../../../tmp/file
```

を単純に展開先directoryと結合すると、意図したsandbox外へ書き出す可能性があります。

また、別Platformで危険になるpathもあります。

```text
..\..\escape
C:/escape
```

EKIは異なるOSのWorkerを扱うため、「現在Linuxだからbackslashは普通の文字」として受理するより、portableな安全なpath subsetへ正規化する方が扱いやすいと考えています。

## Symlinkを初期状態では拒否する

Archive内symlinkは、それ自体が展開先の外へ出ていなくても、後続処理がlinkをfollowすることでHost fileを読ませる可能性があります。

そのため、このSpikeではsymlinkを保持・変換せず、明示的に拒否します。

将来、symlinkが本当に必要なExecutorがある場合は、別のSandboxとPolicyを持つ拡張として検討します。

## 展開先は新規のprivate directoryだけ

`Extract`はdestinationが既に存在する場合に失敗します。

展開時に既存fileを上書きしたり、既に置かれているsymlinkを経由したりする経路を減らすためです。

作成するroot directoryは`0700`、fileは`0600`、subdirectoryは`0700`を基本にします。

これは完全なSandboxではありません。正式Architectureでは、Parser / Converter process自体の隔離と組み合わせる必要があります。

## 実施したUnit Test

2026-09-18時点で、次を含む`go test ./...`が通過しています。

- 正常なnested fileの展開
- `../../` traversalの拒否
- backslash traversalの拒否
- `C:/`形式のdrive path拒否
- `a/../b`のような非canonical pathの拒否
- symlink拒否
- file count超過
- total expanded size超過
- 高compression ratio
- 既存destinationへの展開拒否
- 実展開時に有限limitが指定されていない場合の拒否

テストでは巨大なZIP bombを実際に作りません。

数KiB〜数十KiBの圧縮しやすいdataに小さな上限を設定し、同じ境界条件を安全に再現しています。

## このSpikeで確認できたこと

- ArtifactのContent IntegrityとSafe Processingは別の保証として実装できる。
- ZIPでは展開前metadataを使った早期拒否が可能である。
- streaming時にも展開量を制限することで、metadataだけへの依存を減らせる。
- path traversalとsymlinkを、業務固有のParserより前段で共通境界として拒否できる。
- 安全な共通subsetへ絞ることで、異なるOSのpath semantics差を小さくできる。

## まだ確認していないこと

- TAR / gzip / 7z等の他archive形式
- nested archiveを再帰的に展開する場合の総Budget
- PDF / Office / Image / Model parserのProcess隔離
- CPU time / MemoryをOS levelで制限したParser実行
- Malware scanの位置付け
- Unicode normalizationやPlatform固有reserved nameの完全な扱い
- raw archive byte数とcentral directory parseのIngress側上限
- 同一UIDの攻撃Processが展開directoryを同時に操作する場合
- Windows実機でのpath semantics

## Architectureへ持ち越す重要点

Artifact ingestは、最終的に次の複数段階へ分ける必要がありそうです。

```text
受理
 ↓
Content identity / provenance
 ↓
format-level preflight
 ↓
resource-bounded decode / extract
 ↓
application-specific validation
 ↓
approved Artifact
```

ただし、このpipeline構造を現時点で確定はしません。

重要なのは、**署名済みであること、Digestが一致すること、形式として読めること、安全に処理できることをそれぞれ別の事実として保持する**ことです。

## 参考資料

- OWASP Web Security Testing Guide — Test Upload of Malicious Files
  - https://wstg.owasp.org/latest/4-Web_Application_Security_Testing/10-Business_Logic_Testing/09-Test_Upload_of_Malicious_Files/
- Go `archive/zip`
  - https://pkg.go.dev/archive/zip
- EKI調査メモ
  - `../../research/artifact-ingest-safety.md`

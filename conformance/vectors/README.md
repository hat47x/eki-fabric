# Signed Decision COSE conformance vectors

このdirectoryは、EKI FabricのP2 signed Authority Decisionを運ぶstrict COSE_Sign1 profileについて、Concrete decoder / verifier Adapterが満たすべき入力例を保持します。

## 目的

`spec/decisions/v1/cose-profile.cddl`と`plan/adr/0005-strict-cose-sign1-decoder-boundary.md`はwire contractを定義します。

一方、hostile input parserでは「不正であること」だけでなく、**どの段階で最初に拒否すべきか**も重要です。このfixtureは、library実装へ仕様を合わせるのではなく、後から導入するCBOR/COSE AdapterをEKIの既存contractへ合わせるために使います。

## `signed-decision-cose-v1.json`

fixtureは次を含みます。

- Baseline profile identifier
- test-only Ed25519 public key
- fully validなtagged COSE_Sign1 1件
- envelope / protected header / payload / crypto段階のnegative vectors
- 各vectorで期待するstable `VerificationReason`
- 最初に失敗すべきlogical phase

現在のvector集合では、次を明示的に扱います。

- tagged-only COSE_Sign1
- wrong tag / untagged form
- empty unprotected map
- attached payload
- exact 64-byte signature shape
- trailing CBOR data
- indefinite-length outer array
- exact EKI profile
- mandatory `crit`
- algorithm allowlist
- `kid` size
- duplicate protected-header key
- unknown protected-header label
- non-deterministic protected-header key order
- non-shortest integer encoding
- indefinite-length protected map
- non-deterministic P2 payload map order
- cryptographically tampered Ed25519 signature

## Test key

fixture内の公開鍵はconformance test専用です。

~~~text
kid       = kid-test-ed25519
algorithm = EdDSA / Ed25519
~~~

この鍵はproduction credentialではありません。

正常系vectorは、Go標準libraryとtest-onlyの小さなdeterministic CBOR encoderから再生成できるようにしてあります。private seedはcredentialとして管理するものではなく、再現可能なtest inputとしてtest code内だけで生成します。

## First-failure semantics

`phase`はproduction parserの具体的なfunction名を固定するものではありません。

ただし、たとえばwrong tagをpayload decoderまで進めたり、unknown algorithmをsignature verificationまで進めたりしないことを要求します。

~~~text
envelope
  -> protected
  -> payload
  -> pre-crypto policy
  -> crypto
  -> post-crypto/current-state
~~~

浅い層で拒否できる入力を深い層へ送らないことで、attack surfaceとresource consumptionを減らします。

## Adapter conformance

将来のConcrete decoderは、このfixtureを少なくとも次の条件で利用します。

1. `cose_hex`をdecodeする。
2. total size / CBOR resource limitを適用する。
3. EKI strict COSE_Sign1 envelopeを検証する。
4. protected headerをstrictに検証する。
5. deterministic protected/payload bytesを検証する。
6. P2 payloadをdecodeする。
7. 既存`VerifierBoundary`へ接続する。
8. fixtureの`expected_reason`と一致する結果を返す。

library固有error textはcontractにしません。

## 変更ルール

vectorの追加は可能です。

既存vectorの意味、期待reason、またはfirst-failure phaseを変更する場合は、同時にwire/profile contract側の変更理由を示します。

Concrete libraryの都合だけを理由に既存vectorを緩めません。

## 実装に当てて見つかった修正（2026-10-06）

`kid_too_long`は、Concrete decoder（`internal/adapters/reference/cosedecoder/cbor`）に初めて当てた時点で、意図した段階まで届かないbytesだと分かりました。kidの長さ接頭辞は129 bytesなのに本体が133 bytesあり、protected headerの長さとも合わず、outer arrayの時点で`MALFORMED_COSE`になっていました。

期待reason（`PROTECTED_HEADER_INVALID`）とphase（`protected`）は変えず、同じ構造のまま本体を129 bytesにして作り直しました。vectorの意味を緩めたのではなく、説明どおりの入力になるようにbytesを直したものです。

negative vectorは、対象の段階より浅い段階で拒否されないことが重要です。vectorを追加するときは、手でhexを組み立てずにencoderで生成し、Concrete decoderで意図した段階に届くことを確認してください。

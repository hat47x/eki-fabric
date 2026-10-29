# 署名つきの連鎖を作る試験支援

プロセスをまたぐ試験のために、実際のPLC-1、LSE-1、WOR-1の署名つきの資料をState Storeへ書く補助です。製品のコードではありません。

`Authorities.Place`は、Task、Attempt、Workerへの結び付き、署名つきのPlacement Approval、Leaseの予約とEMITTED、署名つきのWorkOrderのEMITTEDまでを、`leaseissue`と`workorderissue`の参照実装で行います。Control Planeの別プロセスは、このDBを読むだけで、保存されたbytesを配れます。

- 鍵は種（seed）から決めます。別の種は、同じ鍵IDと主体名で別の鍵になり、偽造者の役をします。
- `Place`の`at`を過去にすると、期限切れのLeaseを作れます。
- Executor Approval（EXE-1）は合成の参照です。署名する部品がまだありません。
- `conformance/verticalslice`の署名つきの連鎖と同じ部品を使いますが、そちらの試験用ヘルパーは`_test.go`にあり、他のpackageから使えないため、必要な部分だけを置いています。

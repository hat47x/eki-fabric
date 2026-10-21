# メモリ上のReplay Guard（in-memory replay guard）

WorkOrderのnonceは、一度だけ使います。このパッケージは、その一度を確かめる最小の実装です。

```text
Claim(nonce, until)
  -> 既に使われていて、まだ期限内  -> false（実行しない）
  -> 未使用、または期限切れ        -> true（使用済みとして記録）
```

## 何を保証しないか

- プロセスを再起動すると記録が消えます。M0の単一Workerプロセスの試験用であり、Worker実行の証跡にはなりません（RQ-048）。
- nonceの意味や、WorkOrderの他の条件を検証しません。署名、宛先、参照先Decisionのcurrentnessは、`workorderverify`など別の境界で確かめます。
- 複数のWorkerや複数のプロセスで共有しません。共有が必要な場合は、durableなStoreを持つAdapterに差し替えます。

再起動後の記録の保持と、同じローカルDBを開くプロセス間の排他が必要な場合は、[SQLite実装](../sqlite/README.md)を使えます。Workerの起動設定を自動で切り替えるものではありません。

## 満杯のとき

上限に達した場合、古い記録を追い出さずに`ErrGuardCapacity`で失敗します。追い出すと、まだ期限内のnonceを再利用できてしまうためです。呼出し側は、このエラーを実行の許可として扱いません。

## 期限

記録の期限は、WorkOrderとLeaseの両方が期限切れになる最も早い時刻を渡します。期限ちょうどの時刻では、記録は失効しており、同じnonceを再利用できます。

# Linux sandboxの検証手順

`native-runner`のLinux専用testを、bwrapのあるLinux環境またはWSLで実行します。

## 目的

- `linux-bwrap` profileの正常実行
- 読み取り専用root
- network interfaceがないこと
- CPU上限超過での停止
- 実行ごとの作業ディレクトリ

## 手順

Goがある環境でLinux向けtest binaryを作ります。

```bash
GOOS=linux GOARCH=amd64 go test -c -o runner-linux.test ./internal/agent/nativerunner
```

bwrapのあるLinux環境またはWSLで実行します。

```bash
tools/linux-sandbox-verify/verify.sh ./runner-linux.test
```

WindowsでbuildしたbinaryをWSLで使う場合の例です。

```powershell
$env:GOOS = "linux"; $env:GOARCH = "amd64"
go test -c -o .tmp/runner-linux.test ./internal/agent/nativerunner
Remove-Item Env:GOOS; Remove-Item Env:GOARCH
```

```bash
wsl -e bash -lc 'chmod +x /mnt/d/.../.tmp/runner-linux.test && cd /tmp && /mnt/d/.../.tmp/runner-linux.test -test.v'
```

## 限界

- `bwrap`の導入は実行環境の条件です。
- このscriptはtest binaryをbuildしません。Goの有無と実行環境を分けています。
- CIへ常設しているわけではありません。

<#
.SYNOPSIS
  EKI Fabric のローカル検証を、ひとつのコマンドで再現する。

.DESCRIPTION
  gofmt、go vet、go test を順に実行し、そのままPRの検証欄へ貼れる形で出力する。
  CIの成否ではなく、この出力を実装のEvidenceにする（docs/development.md）。

  Windowsの既定の実行ポリシーでは、このスクリプトを直接実行できない場合がある。
  その場合は、次のように明示的に実行ポリシーを緩めて起動する。

    powershell -NoProfile -ExecutionPolicy Bypass -File scripts\verify.ps1
    powershell -NoProfile -ExecutionPolicy Bypass -File scripts\verify.ps1 -Quick
    powershell -NoProfile -ExecutionPolicy Bypass -File scripts\verify.ps1 -Race

.PARAMETER Quick
  go test を省略し、gofmt と go vet だけを実行する。

.PARAMETER Race
  go test の代わりに go test -race を実行する。データ競合の検出には cgo
  （CGO_ENABLED=1）とCコンパイラが必要で、使えない環境では失敗として終了する。
  Quick とは同時に指定できない。
#>
[CmdletBinding()]
param(
	[switch]$Quick,
	[switch]$Race
)

$ErrorActionPreference = 'Stop'
if ($Quick -and $Race) {
	Write-Error '-Quick と -Race は同時に指定できません（-Quick は go test を実行しません）。'
	exit 2
}
$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
	$status = 0

	$go = Get-Command go -ErrorAction SilentlyContinue
	if (-not $go) {
		Write-Error 'go が見つかりません。Go 1.27 以降を PATH へ入れてください。'
		exit 1
	}
	$gofmt = Join-Path (Split-Path -Parent $go.Source) 'gofmt.exe'
	if (-not (Test-Path -LiteralPath $gofmt)) {
		Write-Error "gofmt が見つかりません: $gofmt"
		exit 1
	}

	Write-Output '== go version =='
	& go version

	Write-Output ''
	Write-Output '== gofmt =='
	# CRLF の作業ツリーでも同じ判定になるよう、改行を LF へそろえた複製を
	# 一時ディレクトリへ作り、その複製を gofmt へ渡す。PowerShell の
	# パイプラインを通すと文字コードの変換で比較が揺れるため、byte列を
	# 保ったまま書き出す。
	$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ('eki-gofmt-' + [guid]::NewGuid().ToString('N'))
	New-Item -ItemType Directory -Path $tmp | Out-Null
	$map = @{}
	$i = 0
	foreach ($file in (git ls-files '*.go')) {
		if (-not (Test-Path -LiteralPath (Join-Path $root $file) -PathType Leaf)) { continue }
		$bytes = [System.IO.File]::ReadAllBytes((Join-Path $root $file))
		$text = [System.Text.Encoding]::UTF8.GetString($bytes).Replace("`r`n", "`n")
		$name = '{0:D4}.go' -f $i
		[System.IO.File]::WriteAllText((Join-Path $tmp $name), $text, (New-Object System.Text.UTF8Encoding($false)))
		$map[$name] = $file
		$i++
	}
	$bad = @(& $gofmt -l $tmp)
	if ($LASTEXITCODE -ne 0) {
		Write-Output 'gofmt の実行に失敗しました'
		$status = 1
	} elseif ($bad.Count -gt 0) {
		Write-Output 'gofmt が必要なファイル:'
		foreach ($path in $bad) {
			$name = Split-Path -Leaf $path
			Write-Output ("  " + $map[$name])
		}
		$status = 1
	} else {
		Write-Output 'OK: すべての .go ファイルが gofmt 済み'
	}
	Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue

	Write-Output ''
	Write-Output '== go vet =='
	& go vet ./...
	if ($LASTEXITCODE -ne 0) { $status = 1 }

	# spikes/配下の独立したmoduleのうち、ルートmoduleをreplaceで取り込むものは、
	# ルートの go vet ./... の対象にならない。ルートの変更で壊れていないかを、
	# 型検査（試験コードを含む）だけで確かめる。Dockerやlinux専用の試験は走らせない。
	Write-Output ''
	Write-Output '== go vet (ルートをreplaceで取り込むSpike) =='
	$spikeCount = 0
	foreach ($mod in (git ls-files spikes | Where-Object { $_ -match '/go\.mod$' })) {
		$modPath = Join-Path $root $mod
		if (-not (Select-String -LiteralPath $modPath -Pattern '^replace\s+github\.com/hat47x/eki-fabric\s+=>' -Quiet)) { continue }
		$dir = Split-Path -Parent $modPath
		$spikeCount++
		Write-Output ('-- ' + (Split-Path -Parent $mod))
		Push-Location $dir
		try {
			& go vet ./...
			if ($LASTEXITCODE -ne 0) { $status = 1 }
		} finally {
			Pop-Location
		}
	}
	Write-Output ("対象のSpike: $spikeCount")

	if (-not $Quick -and -not $Race) {
		Write-Output ''
		Write-Output '== go test =='
		& go test ./... -count=1
		if ($LASTEXITCODE -ne 0) { $status = 1 }
	}

	if ($Race) {
		Write-Output ''
		Write-Output '== go test -race =='
		# データ競合の検出はcgoとCコンパイラを要する。使えない環境では、
		# 通ったように見せず、足りないものを示して失敗させる。
		$cgo = (& go env CGO_ENABLED)
		$cc = (& go env CC)
		$ccName = ($cc -split ' ')[0]
		if ($cgo -ne '1' -or -not (Get-Command $ccName -ErrorAction SilentlyContinue)) {
			Write-Output ("go test -race にはcgo（CGO_ENABLED=1）とCコンパイラ（CC=" + $cc + "）が必要です。")
			$status = 1
		} else {
			# -raceは実行が数倍遅くなる。既定の10分では、SQLiteを使う
			# packageが途中で打ち切られる場合があるため、時間の上限を広げる。
			& go test -race ./... -count=1 -timeout 30m
			if ($LASTEXITCODE -ne 0) { $status = 1 }
		}
	}

	Write-Output ''
	if ($status -eq 0) {
		Write-Output 'VERIFY: PASS'
	} else {
		Write-Output 'VERIFY: FAIL'
	}
	exit $status
} finally {
	Pop-Location
}

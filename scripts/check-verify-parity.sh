#!/usr/bin/env bash
# CONTRIBUTING.mdの検証コマンドと、verify.sh / verify.ps1のgo testが
# 同じ形（go test ./... -count=1）であることを確かめる。Goは不要。
set -u
cd "$(dirname "$0")/.." || exit 2

want='go test ./... -count=1'
status=0
for f in CONTRIBUTING.md scripts/verify.sh scripts/verify.ps1; do
	if grep -qF -- "$want" "$f"; then
		echo "OK: $f"
	else
		echo "NG: $f に「$want」がありません"
		status=1
	fi
done
exit "$status"

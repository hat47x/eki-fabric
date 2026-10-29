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

# 文書の `./scripts/verify.sh` がLinuxでそのまま動くよう、gitのmodeに実行ビット
# (100755) があることを確かめる。Windowsではworking treeのmodeが落ちやすいので、
# indexのmodeを見る。gitの管理下でない場合は、この確認を省く。
if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
	for f in scripts/verify.sh scripts/check-verify-parity.sh; do
		mode=$(git ls-files -s -- "$f" | cut -d' ' -f1)
		if [ "$mode" = "100755" ]; then
			echo "OK: $f の実行ビット"
		else
			echo "NG: $f のgit modeが${mode:-未追跡}です（100755が必要）。git update-index --chmod=+x $f"
			status=1
		fi
	done
fi
exit "$status"

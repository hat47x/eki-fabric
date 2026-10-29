#!/usr/bin/env bash
# EKI Fabric のローカル検証を、ひとつのコマンドで再現する。
#
# 使い方:
#   ./scripts/verify.sh            # gofmt / go vet / go test
#   ./scripts/verify.sh --quick    # gofmt / go vet のみ
#   ./scripts/verify.sh --race     # go test の代わりに go test -race を実行する
#
# 出力は、そのままPRの検証欄へ貼れる形にしている。
# CIの成否ではなく、この出力を実装のEvidenceにする（docs/development.md）。
set -u -o pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root" || exit 1

quick=0
race=0
for arg in "$@"; do
	case "$arg" in
	--quick) quick=1 ;;
	--race) race=1 ;;
	-h | --help)
		sed -n '2,11p' "$0"
		exit 0
		;;
	*)
		echo "unknown argument: $arg" >&2
		exit 2
		;;
	esac
done

if ! command -v go >/dev/null 2>&1; then
	echo "go が見つかりません。Go 1.27 以降をPATHへ入れてください。" >&2
	exit 1
fi
if ! command -v gofmt >/dev/null 2>&1; then
	echo "gofmt が見つかりません。Goの配布物に含まれるgofmtをPATHへ入れてください。" >&2
	exit 1
fi

if [ "$quick" -eq 1 ] && [ "$race" -eq 1 ]; then
	echo "--quick と --race は同時に指定できません（--quick は go test を実行しません）。" >&2
	exit 2
fi

status=0
echo "== go version =="
go version

echo
echo "== gofmt =="
unformatted=""
while IFS= read -r file; do
	[ -f "$file" ] || continue
	# CRLF の作業ツリーでも判定できるよう、改行をLFへそろえてから比較する。
	normalized=$(tr -d '\r' <"$file")
	formatted=$(printf '%s' "$normalized" | gofmt)
	if [ "$normalized" != "$formatted" ]; then
		unformatted="$unformatted$file"$'\n'
	fi
done <<EOF
$(git ls-files '*.go')
EOF
if [ -n "$unformatted" ]; then
	echo "gofmt が必要なファイル:"
	printf '%s' "$unformatted"
	status=1
else
	echo "OK: すべての .go ファイルが gofmt 済み"
fi

echo
echo "== go vet =="
if ! go vet ./...; then
	status=1
fi

if [ "$quick" -eq 0 ] && [ "$race" -eq 0 ]; then
	echo
	echo "== go test =="
	if ! go test ./... -count=1; then
		status=1
	fi
fi

if [ "$race" -eq 1 ]; then
	echo
	echo "== go test -race =="
	# データ競合の検出はcgoとCコンパイラを要する。使えない環境では、
	# 通ったように見せず、足りないものを示して失敗させる。
	cc=$(go env CC)
	if [ "$(go env CGO_ENABLED)" != "1" ] || ! command -v "${cc%% *}" >/dev/null 2>&1; then
		echo "go test -race にはcgo（CGO_ENABLED=1）とCコンパイラ（CC=${cc}）が必要です。" >&2
		status=1
	else
		# -raceは実行が数倍遅くなる。既定の10分では、SQLiteを使う
		# packageが途中で打ち切られる場合があるため、時間の上限を広げる。
		if ! go test -race ./... -count=1 -timeout 30m; then
			status=1
		fi
	fi
fi

echo
if [ "$status" -eq 0 ]; then
	echo "VERIFY: PASS"
else
	echo "VERIFY: FAIL"
fi
exit "$status"

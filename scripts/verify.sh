#!/usr/bin/env bash
# EKI Fabric のローカル検証を、ひとつのコマンドで再現する。
#
# 使い方:
#   ./scripts/verify.sh            # gofmt / go vet / go test
#   ./scripts/verify.sh --quick    # gofmt / go vet のみ
#
# 出力は、そのままPRの検証欄へ貼れる形にしている。
# CIの成否ではなく、この出力を実装のEvidenceにする（docs/development.md）。
set -u -o pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root" || exit 1

quick=0
for arg in "$@"; do
	case "$arg" in
	--quick) quick=1 ;;
	-h | --help)
		sed -n '2,10p' "$0"
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

# spikes/配下の独立したmoduleのうち、ルートmoduleをreplaceで取り込むものは、
# ルートの go vet ./... の対象にならない。ルートの変更で壊れていないかを、
# 型検査（試験コードを含む）だけで確かめる。Dockerやlinux専用の試験は走らせない。
echo
echo "== go vet (ルートをreplaceで取り込むSpike) =="
spike_count=0
while IFS= read -r mod; do
	[ -n "$mod" ] || continue
	dir=$(dirname "$mod")
	spike_count=$((spike_count + 1))
	echo "-- $dir"
	if ! (cd "$dir" && go vet ./...); then
		status=1
	fi
done <<EOF
$(git ls-files spikes | grep '/go\.mod$' | while IFS= read -r m; do
	grep -qE '^replace[[:space:]]+github\.com/hat47x/eki-fabric[[:space:]]+=>' "$m" && echo "$m"
done)
EOF
echo "対象のSpike: ${spike_count}"

if [ "$quick" -eq 0 ]; then
	echo
	echo "== go test =="
	if ! go test ./... -count=1; then
		status=1
	fi
fi

echo
if [ "$status" -eq 0 ]; then
	echo "VERIFY: PASS"
else
	echo "VERIFY: FAIL"
fi
exit "$status"

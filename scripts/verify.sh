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
# shellcheck source=scripts/gofmt-check.sh
. "$root/scripts/gofmt-check.sh"
unformatted=$(git ls-files '*.go' | eki_gofmt_unformatted)
if [ -n "$unformatted" ]; then
	echo "gofmt が必要なファイル:"
	printf '%s\n' "$unformatted"
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

# go test を -v で走らせ、失敗と「skip の要約」だけを標準出力へ出す。
#
# skip された試験は、環境の都合（Windows専用、Linux専用、Docker必須など）で
# 一度も走っていない。これを出さないと、VERIFY: PASS が何を確かめたのかを
# 出力から読めない。件数と理由を必ず示す（#746）。
run_go_test() {
	log=$(mktemp) || {
		echo "一時ファイルを作れません。" >&2
		return 1
	}
	rc=0
	# 引数は -race などの追加フラグとして渡す。
	if ! go test ./... -count=1 -v "$@" >"$log" 2>&1; then
		rc=1
		# 失敗はそのままの形で見せる。原因の行を探しやすくするため。
		grep -E '^[[:space:]]*--- FAIL|^FAIL' "$log" || true
	fi

	ran=$(grep -cE '^[[:space:]]*=== RUN' "$log" || true)
	skipped=$(grep -cE '^[[:space:]]*--- SKIP' "$log" || true)
	: "${ran:=0}" "${skipped:=0}"
	echo "実行した試験の行数: ${ran}"
	echo "skip した試験の行数: ${skipped}"
	if [ "$skipped" -gt 0 ]; then
		echo "環境により実行していない試験（VERIFY の範囲に含まれない）:"
		# SKIP 行と、その直後の理由（インデントされた行）を対にして出す。
		# 空行や次の === RUN は理由ではないので飛ばす。
		awk '
			/^[ \t]*--- SKIP/ { print "  " $0; want = 1; next }
			want && /^[ \t]+[^ \t]/ && $0 !~ /^(===|---)/ { print "  " $0; want = 0; next }
			want && /^[ \t]*$/ { next }
			{ want = 0 }
		' "$log"
		echo "※ これらは通ったのではなく、走っていません。"
	fi
	rm -f "$log"
	return "$rc"
}

if [ "$quick" -eq 0 ] && [ "$race" -eq 0 ]; then
	echo
	echo "== go test =="
	if ! run_go_test; then
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
		if ! run_go_test -race -timeout 30m; then
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

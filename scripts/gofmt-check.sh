#!/usr/bin/env bash
# gofmtの判定だけを切り出した関数。verify.shと、scripts/check-gofmt-detection.shが読み込む。
#
# eki_gofmt_unformatted: 標準入力のファイル名（1行1件）のうち、gofmtの出力と
# byte列が一致しないものを標準出力へ1行ずつ書く。
#
# コマンド置換（$(...)）は末尾の改行をすべて落とすため、比較には使わない。
# 末尾の改行がない、または余分な空行があるだけの差分は、コマンド置換で比べると
# 等しく見えてしまい、gofmt -l とverify.ps1の判定と食い違う（#471）。
# CRLFの作業ツリーでもLFと同じ結果になるよう、CRを取り除いた複製を一時ファイルへ作り、
# その複製をgofmtの出力とcmpでbyte単位に比べる。
eki_gofmt_unformatted() {
	local file tmp
	tmp=$(mktemp) || return 2
	while IFS= read -r file; do
		[ -f "$file" ] || continue
		tr -d '\r' <"$file" >"$tmp"
		if ! gofmt <"$tmp" 2>/dev/null | cmp -s - "$tmp"; then
			printf '%s\n' "$file"
		fi
	done
	rm -f "$tmp"
}

#!/usr/bin/env bash
# verify.shのgofmt判定が、末尾の改行の過不足とCRLFを正しく扱うことを確かめる（#471）。
# gofmtが必要。一時ディレクトリにfixtureを作るだけで、リポジトリは変更しない。
set -u
cd "$(dirname "$0")/.." || exit 2
. scripts/gofmt-check.sh

tmp=$(mktemp -d) || exit 2
trap 'rm -rf "$tmp"' EXIT
status=0

printf 'package a\n\nfunc F() {}\n' >"$tmp/ok.go"
printf 'package a\n\nfunc F() {}' >"$tmp/no_final_newline.go"
printf 'package a\n\nfunc F() {}\n\n' >"$tmp/extra_blank_line.go"
printf 'package a\r\n\r\nfunc F() {}\r\n' >"$tmp/crlf_ok.go"
printf 'package a\r\n\r\nfunc F() {}' >"$tmp/crlf_no_final_newline.go"
printf 'package a\n\nfunc F()   {}\n' >"$tmp/spacing.go"

expect() { # expect <file> <unformatted|ok>
	local got=ok
	if printf '%s\n' "$tmp/$1" | eki_gofmt_unformatted | grep -q .; then got=unformatted; fi
	# gofmt -l（CONTRIBUTING.mdの検証コマンド）と同じ判定になることも確かめる。
	local ref=ok
	tr -d '\r' <"$tmp/$1" >"$tmp/ref.txt"
	cp "$tmp/ref.txt" "$tmp/ref_copy.go"
	if [ -n "$(gofmt -l "$tmp/ref_copy.go")" ]; then ref=unformatted; fi
	if [ "$got" = "$2" ] && [ "$ref" = "$2" ]; then
		echo "OK: $1 -> $2"
	else
		echo "NG: $1 は $2 のはずが、verify.shの判定=$got、gofmt -l=$ref"
		status=1
	fi
}

expect ok.go ok
expect no_final_newline.go unformatted
expect extra_blank_line.go unformatted
expect crlf_ok.go ok
expect crlf_no_final_newline.go unformatted
expect spacing.go unformatted
exit "$status"

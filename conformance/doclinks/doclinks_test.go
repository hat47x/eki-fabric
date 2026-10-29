// Package doclinks は、リポジトリ内のMarkdownの相対リンクが、存在するファイルや
// ディレクトリを指していることを確かめる。
//
// 契約や設計の根拠として引かれた文書へ辿れなくなると、版を固定して読む側が
// 正本へ戻れない。ルートの go test ./... に含まれるため、scripts/verify.sh と
// scripts/verify.ps1 の双方で同じ検査が走る。
//
// 対象は、リポジトリ内の *.md の、[文字列](相対パス) の形のリンクと、
// [ラベル]: 相対パス の形の参照定義である。次は対象にしない。
//   - http・https・mailto等のスキームを持つリンクと、#だけのページ内リンク
//   - コードブロック（```）とインラインコード（`...`）の中の記述
//   - リンクではないコード書式のパス言及（別リポジトリの文書名など）
//
// リンク先の#断片と?問い合わせは取り除き、ファイルまたはディレクトリの存在だけを見る。
// 見出しの存在までは確かめない。
package doclinks

import (
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// knownBroken は、解決を別のIssueで扱っているため、検査の失敗にしないリンクである。
// 解決したら、この表から消す（消さないと、不要な例外としてテストが失敗する）。
var knownBroken = map[string]string{
	// LICENSEの名義はOwner判断待ち（#247）。
	"README.md:LICENSE":       "#247",
	"CONTRIBUTING.md:LICENSE": "#247",
}

var (
	inlineLink = regexp.MustCompile(`!?\[[^\]]*\]\(\s*(<[^>]*>|[^)\s]*)[^)]*\)`)
	refDef     = regexp.MustCompile(`^\s{0,3}\[[^\]]+\]:\s*(<[^>]*>|\S+)`)
	scheme     = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)
	codeSpan   = regexp.MustCompile("`[^`]*`")
)

// link は、一つのMarkdown内の相対リンクを表す。
type link struct {
	file   string // リポジトリの根からの相対パス（/区切り）
	line   int
	target string
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("リポジトリの根（go.modとAGENTS.mdのある場所）が見つかりません")
		}
		dir = parent
	}
}

// extractLinks は、Markdown本文から検査対象の相対リンクを取り出す。
func extractLinks(file, body string) []link {
	var out []link
	inFence := false
	fence := ""
	for i, raw := range strings.Split(body, "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimLeft(line, " ")
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			marker := trimmed[:3]
			if !inFence {
				inFence, fence = true, marker
			} else if marker == fence {
				inFence = false
			}
			continue
		}
		if inFence {
			continue
		}
		text := codeSpan.ReplaceAllString(line, "")
		var targets []string
		for _, m := range inlineLink.FindAllStringSubmatch(text, -1) {
			targets = append(targets, m[1])
		}
		if m := refDef.FindStringSubmatch(text); m != nil {
			targets = append(targets, m[1])
		}
		for _, tg := range targets {
			tg = strings.TrimSuffix(strings.TrimPrefix(tg, "<"), ">")
			if tg == "" || strings.HasPrefix(tg, "#") || scheme.MatchString(tg) {
				continue
			}
			out = append(out, link{file: file, line: i + 1, target: tg})
		}
	}
	return out
}

// resolve は、リンク先をリポジトリの根からの相対パスへ直す。
// 根の外へ出るリンクは ok=false を返す。
func resolve(l link) (rel string, ok bool) {
	p := l.target
	if i := strings.IndexAny(p, "#?"); i >= 0 {
		p = p[:i]
	}
	if p == "" {
		return "", true
	}
	if dec, err := url.PathUnescape(p); err == nil {
		p = dec
	}
	var joined string
	if strings.HasPrefix(p, "/") {
		joined = strings.TrimPrefix(p, "/")
	} else {
		joined = filepath.Join(filepath.Dir(filepath.FromSlash(l.file)), filepath.FromSlash(p))
	}
	joined = filepath.ToSlash(filepath.Clean(filepath.FromSlash(joined)))
	if joined == ".." || strings.HasPrefix(joined, "../") {
		return joined, false
	}
	return joined, true
}

func TestMarkdownRelativeLinksResolve(t *testing.T) {
	root := repoRoot(t)

	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".md") {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("Markdownが一つも見つかりません。検査が空振りしています")
	}

	checked := 0
	used := map[string]bool{}
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range extractLinks(f, string(b)) {
			checked++
			rel, ok := resolve(l)
			if ok {
				if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
					continue
				}
			}
			key := l.file + ":" + l.target
			if issue, known := knownBroken[key]; known {
				used[key] = true
				t.Logf("既知のリンク切れ（%s）: %s:%d -> %s", issue, l.file, l.line, l.target)
				continue
			}
			t.Errorf("リンク先が存在しません: %s:%d -> %s（解決先: %s）", l.file, l.line, l.target, rel)
		}
	}
	for key, issue := range knownBroken {
		if !used[key] {
			t.Errorf("既知のリンク切れ（%s）の例外 %q が不要です。解決済みなら knownBroken から消してください", issue, key)
		}
	}
	if checked == 0 {
		t.Fatal("検査したリンクが0件です。検査が空振りしています")
	}
	t.Logf("Markdown %d件、相対リンク %d件を検査", len(files), checked)
}

func TestExtractLinks(t *testing.T) {
	body := "" +
		"[a](docs/a.md) と [b](https://example.com/x) と [c](#anchor)\n" +
		"`[d](skip.md)` はコード\n" +
		"```\n[e](skip2.md)\n```\n" +
		"![img](img/p.png \"title\") と [f](<sp ace.md#h>)\n" +
		"[ref]: ../r.md\n"
	got := extractLinks("x/y.md", body)
	var targets []string
	for _, l := range got {
		targets = append(targets, l.target)
	}
	want := []string{"docs/a.md", "img/p.png", "sp ace.md#h", "../r.md"}
	if strings.Join(targets, "|") != strings.Join(want, "|") {
		t.Fatalf("targets = %v, want %v", targets, want)
	}
}

func TestResolve(t *testing.T) {
	l := link{file: "internal/control/clientapi/README.md", target: "../../../plan/adr/x.md#h"}
	if rel, ok := resolve(l); !ok || rel != "plan/adr/x.md" {
		t.Fatalf("got %q %v", rel, ok)
	}
	l.target = "../../../../out.md"
	if _, ok := resolve(l); ok {
		t.Fatal("根の外は拒むべき")
	}
}

// Package buildinfo は、実行ファイルが自分のビルド情報を申告する形式を定める。
//
// 出力は自己申告であり、実行ファイルが改ざんされていないことの証明ではない。
// 署名やビルドの来歴（provenance）は別の判断として扱う。
package buildinfo

import (
	"runtime/debug"
	"strings"
)

// Unknown は、値を取得できなかったことを示す。「dev」のような名前で代用しない。
const Unknown = "unknown"

// Info は、実行ファイルに埋め込まれたビルド情報のうち、版の確認に使う項目。
type Info struct {
	// Revision は vcs.revision。ビルド情報にVCSの記録が無いときは Unknown。
	Revision string
	// Modified は、ビルド時に未コミットの変更があったか。Revision が Unknown のときは Unknown。
	Modified string
	// GoVersion は、ビルドに使ったGoの版。取得できないときは Unknown。
	GoVersion string
}

// readBuildInfo は、試験で差し替えるための入口。
var readBuildInfo = debug.ReadBuildInfo

// Read は、実行中の実行ファイルのビルド情報を返す。
func Read() Info {
	bi, ok := readBuildInfo()
	return fromBuildInfo(bi, ok)
}

func fromBuildInfo(bi *debug.BuildInfo, ok bool) Info {
	info := Info{Revision: Unknown, Modified: Unknown, GoVersion: Unknown}
	if !ok || bi == nil {
		return info
	}
	if v := strings.TrimSpace(bi.GoVersion); v != "" {
		info.GoVersion = v
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if v := strings.TrimSpace(s.Value); v != "" {
				info.Revision = v
			}
		case "vcs.modified":
			switch s.Value {
			case "true", "false":
				info.Modified = s.Value
			}
		}
	}
	// コミットが分からないのに「未変更」とは言えない。
	if info.Revision == Unknown {
		info.Modified = Unknown
	}
	return info
}

// Line は、--version の出力（1行）を返す。
// 例: eki-control revision=5582fcc... modified=false go=go1.27.0
func Line(name string, info Info) string {
	return name + " revision=" + info.Revision + " modified=" + info.Modified + " go=" + info.GoVersion
}

// Version は、実行中の実行ファイルの --version 出力を返す。
func Version(name string) string {
	return Line(name, Read())
}

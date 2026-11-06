package buildinfo

import (
	"runtime/debug"
	"strings"
	"testing"
)

func TestFromBuildInfo(t *testing.T) {
	const rev = "5582fcc0000000000000000000000000000000aa"
	tests := []struct {
		name string
		bi   *debug.BuildInfo
		ok   bool
		want Info
	}{
		{
			name: "VCSの記録あり、変更なし",
			bi: &debug.BuildInfo{GoVersion: "go1.27.0", Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: rev}, {Key: "vcs.modified", Value: "false"},
			}},
			ok:   true,
			want: Info{Revision: rev, Modified: "false", GoVersion: "go1.27.0"},
		},
		{
			name: "VCSの記録あり、未コミットの変更あり",
			bi: &debug.BuildInfo{GoVersion: "go1.27.0", Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: rev}, {Key: "vcs.modified", Value: "true"},
			}},
			ok:   true,
			want: Info{Revision: rev, Modified: "true", GoVersion: "go1.27.0"},
		},
		{
			name: "VCSの記録なし（go run や -buildvcs=false）",
			bi:   &debug.BuildInfo{GoVersion: "go1.27.0"},
			ok:   true,
			want: Info{Revision: Unknown, Modified: Unknown, GoVersion: "go1.27.0"},
		},
		{
			name: "revisionが無いとき、modifiedだけが残っても「変更なし」と言わない",
			bi: &debug.BuildInfo{GoVersion: "go1.27.0", Settings: []debug.BuildSetting{
				{Key: "vcs.modified", Value: "false"},
			}},
			ok:   true,
			want: Info{Revision: Unknown, Modified: Unknown, GoVersion: "go1.27.0"},
		},
		{
			name: "ビルド情報を読めない",
			ok:   false,
			want: Info{Revision: Unknown, Modified: Unknown, GoVersion: Unknown},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := fromBuildInfo(tc.bi, tc.ok); got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestLineNeverUsesDev(t *testing.T) {
	line := Line("eki-control", Info{Revision: Unknown, Modified: Unknown, GoVersion: Unknown})
	if want := "eki-control revision=unknown modified=unknown go=unknown"; line != want {
		t.Fatalf("line = %q, want %q", line, want)
	}
	if strings.Contains(line, " dev") {
		t.Fatalf("line must not say dev: %q", line)
	}
	if strings.Contains(line, "\n") {
		t.Fatalf("line must be a single line: %q", line)
	}
}

func TestVersionUsesReadBuildInfo(t *testing.T) {
	orig := readBuildInfo
	t.Cleanup(func() { readBuildInfo = orig })
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{GoVersion: "go1.27.0", Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abc123"}, {Key: "vcs.modified", Value: "true"},
		}}, true
	}
	if got, want := Version("eki-agent"), "eki-agent revision=abc123 modified=true go=go1.27.0"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

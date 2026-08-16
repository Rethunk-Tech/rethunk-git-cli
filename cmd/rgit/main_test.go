// resolveVersion coverage. Only the pure logic is tested here -- not
// debug.ReadBuildInfo itself, which cmd/rgit-install/main.go's own
// commentary already treats as ground truth for what the toolchain embeds.
package main

import (
	"runtime/debug"
	"testing"

	"github.com/go-quicktest/qt"
)

func TestResolveVersion(t *testing.T) {
	t.Parallel()

	fullRevision := "f5d09e331c0e29febb130d8ca105bbad787e63a8"

	tests := []struct {
		name    string
		ldflags string
		info    *debug.BuildInfo
		ok      bool
		want    string
	}{
		{
			name:    "ldflags wins over build info",
			ldflags: "v1.3.0+a7ece5f",
			info: &debug.BuildInfo{Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: fullRevision},
			}},
			ok:   true,
			want: "v1.3.0+a7ece5f",
		},
		{
			name:    "clean checkout abbreviates to 7 chars",
			ldflags: "dev",
			info: &debug.BuildInfo{Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: fullRevision},
				{Key: "vcs.modified", Value: "false"},
			}},
			ok:   true,
			want: "f5d09e3",
		},
		{
			name:    "dirty checkout matches git describe's suffix",
			ldflags: "dev",
			info: &debug.BuildInfo{Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: fullRevision},
				{Key: "vcs.modified", Value: "true"},
			}},
			ok:   true,
			want: "f5d09e3-dirty",
		},
		{
			name:    "no build info falls back to ldflags default",
			ldflags: "dev",
			info:    nil,
			ok:      false,
			want:    "dev",
		},
		{
			name:    "build info present but no VCS revision falls back",
			ldflags: "dev",
			info: &debug.BuildInfo{Settings: []debug.BuildSetting{
				{Key: "-buildmode", Value: "exe"},
			}},
			ok:   true,
			want: "dev",
		},
		{
			// `go install ...@v1.3.0` builds from the module proxy, not a
			// checkout, so the go tool stamps no vcs.* settings at all --
			// only Main.Version. Reading just the revision left this path
			// reporting "dev" for a precisely known release.
			name:    "module proxy install reports its own tag",
			ldflags: "dev",
			info:    &debug.BuildInfo{Main: debug.Module{Version: "v1.3.0"}},
			ok:      true,
			want:    "v1.3.0",
		},
		{
			// An untagged local build gets a synthesized pseudo-version.
			// The 7-character revision says the same thing in a form that
			// matches `git describe`, which is what the other install paths
			// already print.
			name:    "synthesized pseudo-version yields to the revision",
			ldflags: "dev",
			info: &debug.BuildInfo{
				Main: debug.Module{Version: "v0.0.0-20260816043017-f5d09e331c0e"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: fullRevision},
					{Key: "vcs.modified", Value: "false"},
				},
			},
			ok:   true,
			want: "f5d09e3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := resolveVersion(tt.ldflags, tt.info, tt.ok)
			qt.Assert(t, qt.Equals(got, tt.want))
		})
	}
}

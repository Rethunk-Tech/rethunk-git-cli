package gitx

import (
	"strings"
	"testing"
)

func FuzzParseLsTreeLine(f *testing.F) {
	f.Add("100644 blob abcdef0123456789	path/to/file.go")
	f.Add("100644 blob abcdef0123456789	path with spaces.go")
	f.Add("100644 blob abcdef0123456789 path/to/file.go")
	f.Add("100644 blob	path/to/file.go")
	f.Fuzz(func(t *testing.T, line string) {
		got, err := parseLsTreeLine(line)
		if err != nil {
			return
		}
		rebuilt := got.Mode + " " + got.Type + " " + got.SHA + "\t" + got.Path
		again, err := parseLsTreeLine(rebuilt)
		if err != nil {
			t.Fatalf("round-trip parseLsTreeLine(%q) rebuilt %q: %v", line, rebuilt, err)
		}
		if again != got {
			t.Fatalf("round-trip parseLsTreeLine(%q) = %+v; rebuilt %q -> %+v", line, got, rebuilt, again)
		}
	})
}

func FuzzParseNumstat(f *testing.F) {
	f.Add("")
	f.Add("1\t2\ta.go\x00")
	f.Add("-\t-\told.go\x00new.go\x00")
	f.Add("0\t0\t\x00old.go\x00new\tname.go\x00")
	f.Add("1\ta.go\x00")
	f.Fuzz(func(t *testing.T, in string) {
		got, err := parseNumstat([]byte(in))
		if err != nil {
			return
		}
		// Rebuild in the same NUL-terminated shape: a rename's counts record
		// carries no name, then the source and the destination follow as
		// their own fields.
		var rebuilt strings.Builder
		for _, e := range got {
			rebuilt.WriteString(e.Added + "\t" + e.Deleted + "\t")
			if e.OldPath != "" {
				rebuilt.WriteString("\x00" + e.OldPath + "\x00")
			}
			rebuilt.WriteString(e.Path + "\x00")
		}
		again, err := parseNumstat([]byte(rebuilt.String()))
		if err != nil {
			t.Fatalf("round-trip parseNumstat(%q) rebuilt %q: %v", in, rebuilt.String(), err)
		}
		if len(again) != len(got) {
			t.Fatalf("round-trip parseNumstat(%q) len %d -> %d (%+v vs %+v)", in, len(got), len(again), got, again)
		}
		for i := range got {
			if again[i] != got[i] {
				t.Fatalf("round-trip parseNumstat(%q)[%d] = %+v; want %+v", in, i, again[i], got[i])
			}
		}
	})
}

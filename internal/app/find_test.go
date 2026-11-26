package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/v3/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/v3/internal/gittest"
)

func TestMatchFindRank(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, query string
		want        findRank
	}{
		{"parseShell", "parseShell", findRankExact},
		{"ParseShell", "parseShell", findRankFold},
		{"parseShellArgs", "parseShell", findRankPrefix},
		{"doParseShell", "parseShell", findRankSubstring},
		{"parseShell", "prsShl", findRankSubseq},
		{"other", "parseShell", findRankNone},
	}
	for _, tt := range tests {
		t.Run(tt.name+"/"+tt.query, func(t *testing.T) {
			t.Parallel()
			qt.Assert(t, qt.Equals(matchFindRank(tt.name, tt.query), tt.want))
		})
	}
}

func TestRun_FindHelpAndUsage(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()

	for _, arg := range []string{"--help", "-h"} {
		t.Run(arg, func(t *testing.T) {
			t.Parallel()
			stdout, stderr, code := runApp(t, "-C", cwd, "find", arg)
			qt.Assert(t, qt.Equals(code, exitcode.Success))
			qt.Assert(t, qt.Equals(stdout, findHelp))
			qt.Assert(t, qt.Equals(stderr, ""))
		})
	}

	stdout, stderr, code := runApp(t, "-C", cwd, "find")
	qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
	qt.Assert(t, qt.Equals(stdout, ""))
	wantUsage := "rgit: find requires a query\n" + usageHint(findHelp)
	qt.Assert(t, qt.Equals(stderr, wantUsage))
}

func TestRunFindRankingOrder(t *testing.T) {
	t.Parallel()
	dir := findRankRepo(t)

	stdout, stderr, code := runApp(t, "-C", dir, "find", "--names", "abc")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.DeepEquals(findHeaderNames(t, stdout), []string{"abc", "ABC", "abcd", "xabc", "aXbXc"}))
}

func TestRunFindBodiesNamesLimit(t *testing.T) {
	t.Parallel()
	dir := findRankRepo(t)

	tests := []struct {
		name         string
		args         []string
		wantNames    []string
		wantBodyOf   []string
		wantNoBodyOf []string
	}{
		{
			name:         "names",
			args:         []string{"--names", "abc"},
			wantNames:    []string{"abc", "ABC", "abcd", "xabc", "aXbXc"},
			wantNoBodyOf: []string{"abc", "ABC", "abcd", "xabc", "aXbXc"},
		},
		{
			name:         "bodies 2 limit 4",
			args:         []string{"--bodies", "2", "--limit", "4", "abc"},
			wantNames:    []string{"abc", "ABC", "abcd", "xabc"},
			wantBodyOf:   []string{"abc", "ABC"},
			wantNoBodyOf: []string{"abcd", "xabc"},
		},
		{
			name:         "limit 1",
			args:         []string{"--names", "--limit", "1", "abc"},
			wantNames:    []string{"abc"},
			wantNoBodyOf: []string{"abc"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			stdout, stderr, code := runApp(t, append([]string{"-C", dir, "find"}, tt.args...)...)
			qt.Assert(t, qt.Equals(code, exitcode.Success))
			qt.Assert(t, qt.Equals(stderr, ""))
			qt.Assert(t, qt.DeepEquals(findHeaderNames(t, stdout), tt.wantNames))
			for _, name := range tt.wantBodyOf {
				qt.Assert(t, qt.StringContains(stdout, "func "+name+"()"))
			}
			for _, name := range tt.wantNoBodyOf {
				qt.Assert(t, qt.Not(qt.StringContains(stdout, "func "+name+"()")))
			}
		})
	}

	t.Run("json bodies 1", func(t *testing.T) {
		t.Parallel()
		stdout, stderr, code := runApp(t, "-C", dir, "find", "--json", "--bodies", "1", "--limit", "3", "abc")
		qt.Assert(t, qt.Equals(code, exitcode.Success))
		qt.Assert(t, qt.Equals(stderr, ""))
		var top struct {
			Schema int `json:"schema"`
			Hits   []struct {
				Name string `json:"name"`
				Body string `json:"body"`
			} `json:"hits"`
		}
		qt.Assert(t, qt.IsNil(json.Unmarshal([]byte(stdout), &top)))
		qt.Assert(t, qt.Equals(top.Schema, 1))
		qt.Assert(t, qt.Equals(len(top.Hits), 3))
		qt.Assert(t, qt.Equals(top.Hits[0].Name, "abc"))
		qt.Assert(t, qt.StringContains(top.Hits[0].Body, "func abc()"))
		qt.Assert(t, qt.Equals(top.Hits[1].Body, ""))
		qt.Assert(t, qt.Equals(top.Hits[2].Body, ""))
	})
}

func TestRunFindNoHit(t *testing.T) {
	t.Parallel()
	dir := findRankRepo(t)
	stdout, stderr, code := runApp(t, "-C", dir, "find", "ZZZNoSuchSymbol")
	qt.Assert(t, qt.Equals(code, findNoMatch))
	qt.Assert(t, qt.Equals(stdout, ""))
	qt.Assert(t, qt.StringContains(stderr, `no symbols matching "ZZZNoSuchSymbol"`))
	qt.Assert(t, qt.StringContains(stderr, dir))
	qt.Assert(t, qt.StringContains(stderr, "(1 files)"))
}

func TestRunFindAbsDir(t *testing.T) {
	t.Parallel()
	dir := findRankRepo(t)
	stdout, stderr, code := runApp(t, "find", "--names", "abc", dir)
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.DeepEquals(findHeaderNames(t, stdout), []string{"abc", "ABC", "abcd", "xabc", "aXbXc"}))
}

func TestRunFindDottedQuery(t *testing.T) {
	t.Parallel()
	dir, _ := gittest.New(t.Context(), t)
	writeAppFile(t, dir, "recv.go", "package demo\n\ntype Rec struct{}\n\nfunc (Rec) Method() {}\n")
	gittest.Commit(t.Context(), t, dir, "chore: recv")

	stdout, stderr, code := runApp(t, "-C", dir, "find", "--names", "Rec.Method")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.DeepEquals(findHeaderNames(t, stdout), []string{"Rec.Method"}))
}

func TestRunFindMultiLanguage(t *testing.T) {
	t.Parallel()
	dir, _ := gittest.New(t.Context(), t)
	writeAppFile(t, dir, "a.go", "package demo\n\nfunc parseShell() {}\n")
	writeAppFile(t, dir, "a.ts", "export function parseShell(): void {}\n")
	gittest.Commit(t.Context(), t, dir, "chore: langs")

	stdout, stderr, code := runApp(t, "-C", dir, "find", "--names", "parseShell")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stderr, ""))
	names := findHeaderNames(t, stdout)
	qt.Assert(t, qt.DeepEquals(names, []string{"parseShell", "parseShell"}))
	qt.Assert(t, qt.StringContains(stdout, "a.go:"))
	qt.Assert(t, qt.StringContains(stdout, "a.ts:"))
}

func findRankRepo(t *testing.T) string {
	t.Helper()
	dir, _ := gittest.New(t.Context(), t)
	src := "package demo\n\n" +
		"func aXbXc() {}\n\n" +
		"func abcd() {}\n\n" +
		"func abc() {}\n\n" +
		"func xabc() {}\n\n" +
		"func ABC() {}\n"
	if err := os.WriteFile(filepath.Join(dir, "rank.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t.Context(), t, dir, "add", "rank.go")
	gittest.Commit(t.Context(), t, dir, "chore: rank")
	return dir
}

var findHeaderRe = regexp.MustCompile(`^[^:]+:\d+-\d+ \S+ (\S+)$`)

func findHeaderNames(t *testing.T, stdout string) []string {
	t.Helper()
	var names []string
	for line := range strings.SplitSeq(stdout, "\n") {
		m := findHeaderRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		names = append(names, m[1])
	}
	return names
}

func TestTrimFindHitsKeepsBestAndOneForZero(t *testing.T) {
	t.Parallel()
	hits := []findHit{
		{file: "b.go", name: "B", rank: findRankSubstring},
		{file: "a.go", name: "A", rank: findRankExact},
		{file: "c.go", name: "C", rank: findRankPrefix},
	}
	got := trimFindHits(slices.Clone(hits), 2)
	if len(got) != 2 || got[0].name != "A" || got[1].name != "C" {
		t.Errorf("limit 2 = %+v; want A then C", got)
	}
	if got := trimFindHits(slices.Clone(hits), 0); len(got) != 1 {
		t.Errorf("limit 0 kept %d hits; want 1 so a match is not read as no match", len(got))
	}
	if got := trimFindHits(slices.Clone(hits), -1); len(got) != 3 {
		t.Errorf("limit -1 kept %d hits; want all 3", len(got))
	}
}

func TestRunFindNoHitJSONWritesAnEmptyObject(t *testing.T) {
	t.Parallel()
	dir := findRankRepo(t)
	stdout, _, code := runApp(t, "-C", dir, "find", "--json", "ZZZNoSuchSymbol")
	qt.Assert(t, qt.Equals(code, findNoMatch))
	qt.Assert(t, qt.StringContains(stdout, `"hits": []`))
}

func TestRunFindMissingDirIsUsage(t *testing.T) {
	t.Parallel()
	dir := findRankRepo(t)
	_, stderr, code := runApp(t, "-C", dir, "find", "init", "foo")
	qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
	qt.Assert(t, qt.StringContains(stderr, `no such directory "foo"`))
	qt.Assert(t, qt.Not(qt.StringContains(stderr, "rev-parse")))
}

func TestRunFindDirThatIsAFileIsUsage(t *testing.T) {
	t.Parallel()
	dir := findRankRepo(t)
	file := filepath.Join(dir, "afile.txt")
	if err := os.WriteFile(file, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := runApp(t, "-C", dir, "find", "init", "afile.txt")
	qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
	qt.Assert(t, qt.StringContains(stderr, `not a directory "afile.txt"`))
	qt.Assert(t, qt.Not(qt.StringContains(stderr, "rev-parse")))
}

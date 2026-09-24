package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/gittest"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

func TestRun_SymbolsHelpAndUsage(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()

	for _, arg := range []string{"--help", "-h"} {
		t.Run(arg, func(t *testing.T) {
			stdout, stderr, code := runApp(t, "-C", cwd, "symbols", arg)
			qt.Assert(t, qt.Equals(code, exitcode.Success))
			qt.Assert(t, qt.Equals(stdout, symbolsHelp))
			qt.Assert(t, qt.Equals(stderr, ""))
		})
	}

	stdout, stderr, code := runApp(t, "-C", cwd, "symbols")
	qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
	qt.Assert(t, qt.Equals(stdout, ""))
	wantUsage := "rgit: symbols requires at least one file argument\n" + symbolsHelp
	qt.Assert(t, qt.Equals(stderr, wantUsage))
}

func TestRun_SymbolsUnsupportedLanguage(t *testing.T) {
	t.Parallel()
	dir := tempRepo(t)
	writeAppFile(t, dir, "main.rb", "def main; end\n")

	_, stderr, code := runApp(t, "-C", dir, "symbols", "main.rb")
	if code != exitcode.UnsupportedLanguage {
		t.Fatalf("symbols unsupported-language exit code = %d, want %d", code, exitcode.UnsupportedLanguage)
	}
	want := "rgit: unsupported language for \"main.rb\"\n"
	if stderr != want {
		t.Fatalf("symbols unsupported-language stderr = %q, want %q", stderr, want)
	}
}

func TestRunSymbolsListsCleanWorktreeDeclarations(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := []byte("package demo\n\nconst answer = 42\n\nfunc First() {}\n\ntype Thing struct{}\n\nfunc (Thing) Method() {}\n")
	gittest.Git(t, root, "init", "--quiet")
	if err := os.WriteFile(filepath.Join(root, "main.go"), source, 0o600); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, root, "add", "main.go")
	gittest.Git(t, root, "-c", "user.name=rgit test", "-c", "user.email=rgit@example.invalid", "commit", "--quiet", "-m", "initial")

	var stdout, stderr strings.Builder
	code := runSymbols(context.Background(), root, []string{"main.go"}, &stdout, &stderr)
	if code != exitcode.Success {
		t.Fatalf("runSymbols() = %d, stderr = %q", code, stderr.String())
	}

	got := strings.Fields(stdout.String())
	for _, want := range []string{"answer", "First", "Thing", "Thing.Method"} {
		if !containsString(got, want) {
			t.Errorf("runSymbols() omitted %q from %q", want, got)
		}
	}
}

func TestRunSymbolsHonorsCoreIgnoreCaseForExtensions(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	gittest.Git(t, root, "init", "--quiet")
	gittest.Git(t, root, "config", "core.ignorecase", "true")
	if err := os.WriteFile(filepath.Join(root, "Foo.GO"), []byte("package demo\n\nfunc First() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr strings.Builder
	code := runSymbols(context.Background(), root, []string{"Foo.GO"}, &stdout, &stderr)
	if code != exitcode.Success {
		t.Fatalf("runSymbols(Foo.GO) with core.ignorecase=true = %d, stderr = %q", code, stderr.String())
	}
	if !containsString(strings.Fields(stdout.String()), "First") {
		t.Fatalf("runSymbols(Foo.GO) omitted First from %q", stdout.String())
	}

	gittest.Git(t, root, "config", "core.ignorecase", "false")
	stdout.Reset()
	stderr.Reset()
	code = runSymbols(context.Background(), root, []string{"Foo.GO"}, &stdout, &stderr)
	if code != exitcode.UnsupportedLanguage {
		t.Fatalf("runSymbols(Foo.GO) with core.ignorecase=false = %d, want %d", code, exitcode.UnsupportedLanguage)
	}
}

func TestRun_SymbolsListsHeadDeclarationsWhenWorktreeFileIsGone(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := []byte("package demo\n\nfunc Foo() {}\n")
	gittest.Git(t, root, "init", "--quiet")
	if err := os.WriteFile(filepath.Join(root, "a.go"), source, 0o600); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, root, "add", "a.go")
	gittest.Git(t, root, "-c", "user.name=rgit test", "-c", "user.email=rgit@example.invalid", "commit", "--quiet", "-m", "initial")
	if err := os.Remove(filepath.Join(root, "a.go")); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr strings.Builder
	code := runSymbols(context.Background(), root, []string{"a.go"}, &stdout, &stderr)
	if code != exitcode.Success {
		t.Fatalf("runSymbols() = %d, stderr = %q", code, stderr.String())
	}
	if !containsString(strings.Fields(stdout.String()), "Foo") {
		t.Fatalf("runSymbols() omitted HEAD symbol from %q", stdout.String())
	}
}

func TestRun_SymbolsMissingUntrackedFileStillErrors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	gittest.Git(t, root, "init", "--quiet")

	var stdout, stderr strings.Builder
	code := runSymbols(context.Background(), root, []string{"missing.go"}, &stdout, &stderr)
	if code != exitcode.GitFailure {
		t.Fatalf("runSymbols() = %d, want %d", code, exitcode.GitFailure)
	}
	if !strings.Contains(stderr.String(), `rgit: cannot read "missing.go":`) {
		t.Fatalf("runSymbols() stderr = %q, want cannot-read error", stderr.String())
	}
}

func TestRunSymbolsStructuredDataCommitMode(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := []byte("{\n  \"name\": \"demo\",\n  \"enabled\": true\n}\n")
	gittest.Git(t, root, "init", "--quiet")
	if err := os.WriteFile(filepath.Join(root, "config.json"), source, 0o600); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, root, "add", "config.json")
	gittest.Git(t, root, "-c", "user.name=rgit test", "-c", "user.email=rgit@example.invalid", "commit", "--quiet", "-m", "initial")

	var stdout, stderr strings.Builder
	code := runSymbols(context.Background(), root, []string{"config.json"}, &stdout, &stderr)
	if code != exitcode.Success {
		t.Fatalf("runSymbols() = %d, stderr = %q", code, stderr.String())
	}
	if !containsString(strings.Fields(stdout.String()), "name") {
		t.Fatalf("runSymbols() omitted structured-data symbol from %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = runSymbols(context.Background(), root, []string{"--for-commit", "config.json"}, &stdout, &stderr)
	if code != exitcode.Success {
		t.Fatalf("runSymbols(--for-commit) = %d, stderr = %q", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("runSymbols(--for-commit) = %q, want empty stdout", stdout.String())
	}
}

func TestRunCommitHonorsCoreIgnoreCaseForExtensions(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	gittest.Git(t, root, "init", "--quiet")
	gittest.Git(t, root, "config", "core.ignorecase", "true")
	// Identity must be set explicitly, same as gittest.New: an ambient
	// global user.email/user.name (a developer's own machine) would
	// otherwise mask a CI runner that has neither, which cannot commit at
	// all ("empty ident name").
	gittest.Git(t, root, "config", "user.email", "rgit-test@example.com")
	gittest.Git(t, root, "config", "user.name", "rgit Test")
	if err := os.WriteFile(filepath.Join(root, "Foo.GO"), []byte("package demo\n\nfunc First() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := runApp(t, "-C", root, "commit", "-m", "feat(demo): stage First", "Foo.GO:First")
	if code != exitcode.Success {
		t.Fatalf("commit Foo.GO:First with core.ignorecase=true = %d, stderr = %q", code, stderr)
	}
	committed := gittest.Git(t, root, "cat-file", "-p", "HEAD:Foo.GO")
	if !strings.Contains(committed, "func First()") {
		t.Fatalf("committed Foo.GO omitted First: %q", committed)
	}
}

// withLinesSource puts First on one line and Second across three, so a range
// that collapsed a multi-line extent to its first line still fails.
const withLinesSource = "package demo\n\nfunc First() {}\n\nfunc Second() {\n\treturn\n}\n"

// symbolsFixture commits source as name in a fresh repository and returns its
// root, so each --with-lines case states only what it is actually asserting.
func symbolsFixture(t *testing.T, name, source string) string {
	t.Helper()
	root := t.TempDir()
	gittest.Git(t, root, "init", "--quiet")
	if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	gittest.Git(t, root, "add", name)
	gittest.Git(t, root, "-c", "user.name=rgit test", "-c", "user.email=rgit@example.invalid", "commit", "--quiet", "-m", "initial")
	return root
}

func TestRunSymbolsWithLinesEmitsLineRanges(t *testing.T) {
	t.Parallel()
	root := symbolsFixture(t, "main.go", withLinesSource)

	var stdout, stderr strings.Builder
	code := runSymbols(context.Background(), root, []string{"--with-lines", "main.go"}, &stdout, &stderr)
	if code != exitcode.Success {
		t.Fatalf("runSymbols(--with-lines) = %d, stderr = %q", code, stderr.String())
	}
	want := "3,3\tFirst\n5,7\tSecond\n"
	if stdout.String() != want {
		t.Fatalf("runSymbols(--with-lines) = %q, want %q", stdout.String(), want)
	}
}

// The bare form is the one every existing caller already parses, so it stays
// byte-identical whether or not the new flag exists.
func TestRunSymbolsBareOutputUnchangedByWithLines(t *testing.T) {
	t.Parallel()
	root := symbolsFixture(t, "main.go", withLinesSource)

	var stdout, stderr strings.Builder
	code := runSymbols(context.Background(), root, []string{"main.go"}, &stdout, &stderr)
	if code != exitcode.Success {
		t.Fatalf("runSymbols() = %d, stderr = %q", code, stderr.String())
	}
	if want := "First\nSecond\n"; stdout.String() != want {
		t.Fatalf("runSymbols() = %q, want %q", stdout.String(), want)
	}
}

func TestRunSymbolsWithLinesForCommitStillOmitsStructuredData(t *testing.T) {
	t.Parallel()
	root := symbolsFixture(t, "config.json", "{\n  \"name\": \"demo\"\n}\n")

	var stdout, stderr strings.Builder
	code := runSymbols(context.Background(), root, []string{"--with-lines", "--for-commit", "config.json"}, &stdout, &stderr)
	if code != exitcode.Success {
		t.Fatalf("runSymbols(--with-lines --for-commit) = %d, stderr = %q", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("runSymbols(--with-lines --for-commit) = %q, want empty stdout", stdout.String())
	}
}

// The range --with-lines reports has to be the range blame and log bound
// themselves to: both call lineRange over resolve.Resolve's own extent
// (blame.go, log.go). Resolving each anchor back through that path, rather
// than through the table --with-lines itself read, is what would catch a
// second resolution path drifting away from the first.
func TestRunSymbolsWithLinesAgreesWithResolvedExtent(t *testing.T) {
	t.Parallel()
	root := symbolsFixture(t, "main.go", withLinesSource)
	src := []byte(withLinesSource)

	lang, ok, _, err := resolve.LanguageForPathFolding(root, "main.go", false,
		func() ([]byte, bool, error) { return nil, false, nil })
	if err != nil || !ok {
		t.Fatalf("LanguageForPathFolding() ok = %v, err = %v", ok, err)
	}

	var stdout, stderr strings.Builder
	code := runSymbols(context.Background(), root, []string{"--with-lines", "main.go"}, &stdout, &stderr)
	if code != exitcode.Success {
		t.Fatalf("runSymbols(--with-lines) = %d, stderr = %q", code, stderr.String())
	}

	records := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
	if len(records) == 0 {
		t.Fatal("runSymbols(--with-lines) emitted no records")
	}
	for _, record := range records {
		gotRange, anchor, found := strings.Cut(record, "\t")
		if !found {
			t.Fatalf("record %q has no tab separator", record)
		}
		res, err := resolve.Resolve(lang, src, anchor)
		if err != nil {
			t.Fatalf("resolve.Resolve(%q) = %v", anchor, err)
		}
		start, end := lineRange(src, res.Extent)
		if want := fmt.Sprintf("%d,%d", start, end); gotRange != want {
			t.Errorf("--with-lines range for %q = %q, want %q", anchor, gotRange, want)
		}
	}
}

// TestRunSymbolsRefusesPathAboveRoot pins symbols to the same containment
// diff and blame enforce. symbols normalizes an absolute argument itself
// rather than calling repoPath, so the escape check is the one piece of
// repoPath it has to reach for explicitly -- and the case it missed read
// files outside the repository and exited 0.
func TestRunSymbolsRefusesPathAboveRoot(t *testing.T) {
	t.Parallel()
	dir := tempRepo(t)

	outside := filepath.Join(filepath.Dir(dir), "outside.go")
	if err := os.WriteFile(outside, []byte("package x\n\nfunc SecretOutside() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, arg := range []string{"../outside.go", outside} {
		t.Run(arg, func(t *testing.T) {
			stdout, stderr, code := runApp(t, "-C", dir, "symbols", arg)
			qt.Assert(t, qt.Equals(code, exitcode.InvalidUsage))
			qt.Assert(t, qt.Equals(stdout, ""))
			qt.Assert(t, qt.StringContains(stderr, "escapes the repository root"))
		})
	}
}

// TestRunSymbolsAcceptsAbsolutePathInsideRoot guards the other side of the
// escape check: the absolute-path branch is why symbols cannot simply call
// repoPath, so a fix that routed through it would silently drop this.
func TestRunSymbolsAcceptsAbsolutePathInsideRoot(t *testing.T) {
	t.Parallel()
	dir := tempRepo(t)

	stdout, stderr, code := runApp(t, "-C", dir, "symbols", filepath.Join(dir, "a.go"))
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.StringContains(stdout, "A"))
}

// TestSymbols_MultipleFilesPrefixLikeGrep pins the batch contract: one file
// stays bare (the completion scripts parse that output), several prefix every
// line with the path exactly as given, so a line composes straight back into a
// FILE:SYMBOL anchor.
func TestSymbols_MultipleFilesPrefixLikeGrep(t *testing.T) {
	t.Parallel()
	dir := tempRepo(t)
	writeAppFile(t, dir, "b.go", "package a\n\nfunc C() int {\n\treturn 3\n}\n")

	stdout, stderr, code := runApp(t, "-C", dir, "symbols", "a.go")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.Equals(stdout, "A\nB\n"))

	stdout, _, code = runApp(t, "-C", dir, "symbols", "a.go", "b.go")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stdout, "a.go\tA\na.go\tB\nb.go\tC\n"))

	// --with-filename gives one file the same shape, so a caller looping
	// over an argument list need not branch on its length.
	stdout, _, code = runApp(t, "-C", dir, "symbols", "--with-filename", "a.go")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stdout, "a.go\tA\na.go\tB\n"))

	// The prefix composes with --with-lines rather than replacing it.
	stdout, _, code = runApp(t, "-C", dir, "symbols", "--with-lines", "a.go", "b.go")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.StringContains(stdout, "a.go\t3,6\tA"))
	qt.Assert(t, qt.StringContains(stdout, "b.go\t3,5\tC"))
}

// TestSymbols_ResolvesEveryFileBeforeWriting pins the all-or-nothing rule: an
// unsupported file anywhere in the list leaves stdout empty, so a truncated
// listing can never be read as "that file has no more symbols".
func TestSymbols_ResolvesEveryFileBeforeWriting(t *testing.T) {
	t.Parallel()
	dir := tempRepo(t)
	writeAppFile(t, dir, "main.rb", "def main; end\n")

	stdout, stderr, code := runApp(t, "-C", dir, "symbols", "a.go", "main.rb")
	qt.Assert(t, qt.Equals(code, exitcode.UnsupportedLanguage))
	qt.Assert(t, qt.Equals(stdout, ""))
	qt.Assert(t, qt.StringContains(stderr, "unsupported language"))
}

// TestSymbols_PorcelainNULRecords pins the record separator as the only thing
// --porcelain moves: identical fields, identical order, NUL instead of a
// newline. The guarantee it buys is that a symbol may contain any byte but
// NUL -- which the CSS grouped-selector defect showed the newline form could
// not promise.
func TestSymbols_PorcelainNULRecords(t *testing.T) {
	t.Parallel()
	dir := tempRepo(t)
	writeAppFile(t, dir, "b.go", "package a\n\nfunc C() int {\n\treturn 3\n}\n")

	stdout, stderr, code := runApp(t, "-C", dir, "symbols", "--porcelain", "a.go")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.Equals(stdout, "A\x00B\x00"))

	// Every other flag composes with it unchanged.
	stdout, _, code = runApp(t, "-C", dir, "symbols", "--porcelain", "--with-lines", "a.go")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stdout, "3,6\tA\x008,10\tB\x00"))

	stdout, _, code = runApp(t, "-C", dir, "symbols", "--porcelain", "a.go", "b.go")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stdout, "a.go\tA\x00a.go\tB\x00b.go\tC\x00"))

	// The default form is untouched -- the completion scripts parse it.
	stdout, _, code = runApp(t, "-C", dir, "symbols", "a.go")
	qt.Assert(t, qt.Equals(code, exitcode.Success))
	qt.Assert(t, qt.Equals(stdout, "A\nB\n"))
}

// TestSymbols_HelpSteersParsersToPorcelain pins the machine-contract
// steering: the first help line names --porcelain as the NUL-terminated
// parsing form, so a script reading usage knows which flag to reach for.
// Output bytes are unchanged -- only this help line moves.
func TestSymbols_HelpSteersParsersToPorcelain(t *testing.T) {
	t.Parallel()
	first, _, _ := strings.Cut(symbolsHelp, "\n")
	qt.Assert(t, qt.StringContains(first, "--porcelain"))
	qt.Assert(t, qt.StringContains(first, "NUL"))
}

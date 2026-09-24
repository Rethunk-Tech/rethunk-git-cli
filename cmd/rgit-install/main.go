// Command rgit-install is the one-command path for a user who does not want
// to run make: it checks prerequisites, generates the SQL parser when it
// can, builds rgit, and installs the binary. It is deliberately stdlib-only
// -- see CONTRIBUTING.md § Dependencies -- so verifying a build environment
// never itself needs a working build environment.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/prereq"
)

// sqlGrammarModule is the Go module that publishes the SQL grammar's
// grammar.js and tree-sitter.json, but not a working parser.c: the module
// gitignores it at every tag, so its own bindings/go cannot compile. rgit
// generates that file itself; see generateSQLParser.
//
// sqlGrammarVersion is pinned rather than left floating: internal/resolve/
// sqlgrammar's own binding imports neither this module nor its bindings/go
// package (that is the whole point -- the latter is what cannot compile), so
// nothing in this repo's build graph ever names it in an import statement.
// `go mod tidy` therefore has nothing to hold a go.mod requirement open
// with and would drop a bare `require` line the moment it ran. Resolving
// the module by explicit "module@version" instead of a bare module path
// (downloadSQLGrammarModule) sidesteps that entirely: `go mod download` and
// `go list -m` both accept a version-qualified query with no go.mod entry
// at all.
const (
	sqlGrammarModule  = "github.com/DerekStride/tree-sitter-sql"
	sqlGrammarVersion = "v0.3.11"
)

// sqlAdapterPackageName is the package name findSQLAdapter looks for.
// Matching by name rather than by import content: a check for a
// "tree-sitter-sql" substring in go list's own Imports field would assume
// whatever package needs generation imports the upstream module's own
// bindings/go directly. It never will -- internal/resolve/sqlgrammar's
// binding imports only "C" and "unsafe" (see its own doc comment)
// specifically because that upstream package is the one that cannot
// compile -- so an import-content check would silently match nothing.
const sqlAdapterPackageName = "sqlgrammar"

// main itself has no test: it wires together every function above (each
// already tested or, where it cannot be, carrying its own doc comment
// saying why) around flag.Parse's global os.Args and fatalf's os.Exit,
// neither of which is worth a subprocess re-exec harness just to reach.
// What would catch drift in the wiring itself: `go run ./cmd/rgit-install
// -dry-run` (no side effects) and `go run ./cmd/rgit-install
// -generate-only`, both run by hand as part of validating any change here.
func main() {
	dryRun := flag.Bool("dry-run", false, "print what would happen without building or installing")
	prefixFlag := flag.String("prefix", "", "install directory (default: $GOBIN, else $(go env GOPATH)/bin)")
	generateOnly := flag.Bool("generate-only", false, "generate the SQL parser if the tree-sitter CLI allows, then exit without building or installing (what `make cross` calls, so parser generation stays in this one place)")
	flag.Parse()

	repoRoot, err := resolveRepoRoot()
	if err != nil {
		fatalf("%v", err)
	}

	fmt.Println("Checking prerequisites...")
	checks, fatal := runPrereqChecks()
	width := prereq.Width(checks...)
	for _, c := range checks {
		prereq.Print(os.Stdout, width, c)
	}
	if fatal != nil {
		fatalf("%v", fatal)
	}

	sql := false
	var sqlPkgDir string
	if pkgDir, ok := findSQLAdapter(repoRoot); ok {
		sqlPkgDir = pkgDir
		fmt.Println("SQL adapter package detected:", relTo(repoRoot, pkgDir))
		var msg string
		sql, msg = generateSQLParser(repoRoot, pkgDir, *dryRun)
		fmt.Println(" ", msg)
	} else {
		fmt.Println("No SQL adapter package present yet; building without SQL support.")
	}

	// -generate-only exists so `make cross` can produce the parser without
	// also building or installing a host binary it has no use for. Placed
	// after generation and before everything else rgit's own build/install
	// needs, so the flag does exactly what it says: the parser, then
	// nothing else. A caller without the tree-sitter CLI still exits 0
	// here -- generateSQLParser already reported why, and cross builds
	// fall back to no SQL the same way `make install` does.
	if *generateOnly {
		return
	}

	ver := gitVersion(repoRoot)

	prefix, err := resolvePrefix(*prefixFlag)
	if err != nil {
		fatalf("%v", err)
	}
	dest := filepath.Join(prefix, "rgit")

	if *dryRun {
		tags := ""
		if sql {
			tags = " -tags rgit_sql"
		}
		fmt.Printf("Would build ./cmd/rgit%s (version %s) and install to %s\n", tags, versionOrDev(ver), dest)
		return
	}

	_, statErr := os.Stat(dest)
	replaced := statErr == nil

	fmt.Println("Building...")
	err = runInstall(repoRoot, sql, sqlPkgDir, ver, prefix)
	if err != nil && sql {
		// generateSQLParser only proves the C it wrote is well-formed enough
		// to reach the compiler; a cgo build can still fail past that (a
		// grammar.js version mismatch, a toolchain quirk). docs/INSTALL.md
		// promises a working rgit without the tree-sitter CLI -- keep that
		// promise here too rather than dying with SQL as the only path tried.
		fmt.Fprintf(os.Stderr, "rgit-install: SQL build failed, retrying without SQL support: %v\n", err)
		sql = false
		err = runInstall(repoRoot, sql, sqlPkgDir, ver, prefix)
	}
	if err != nil {
		fatalf("install failed: %v", err)
	}

	action := "Installed"
	if replaced {
		action = "Replaced existing binary at"
	}
	fmt.Printf("%s %s (version %s)\n", action, dest, versionOrDev(ver))
}

// fatalf never returns -- os.Exit skips this test binary the same way it
// would skip any caller, so only fatalMessage (the part with a return value)
// is covered directly; a subprocess re-exec harness to catch the exit code
// itself would be pure ceremony for a two-line wrapper with no branch to
// get wrong.
func fatalf(format string, args ...any) {
	fmt.Fprint(os.Stderr, fatalMessage(format, args...))
	os.Exit(1)
}

// fatalMessage is fatalf's formatting half, split out so it is testable
// without also triggering os.Exit.
func fatalMessage(format string, args ...any) string {
	return fmt.Sprintf("rgit-install: "+format+"\n", args...)
}

// resolveRepoRoot finds the module root by asking the go tool rather than
// walking up looking for go.mod by hand -- it already knows the answer and
// already accounts for GOFLAGS, workspace files, and everything else that
// can move where "the module" is.
func resolveRepoRoot() (string, error) {
	out, err := exec.CommandContext(context.Background(), "go", "env", "GOMOD").Output()
	if err != nil {
		return "", fmt.Errorf("go env GOMOD: %w", err)
	}
	return parseGOMODOutput(string(out))
}

// parseGOMODOutput is `go env GOMOD`'s output turned into a repo root, split
// out from resolveRepoRoot so the parsing -- trimming, and recognizing the
// "not in a module" cases -- is testable without shelling out to go for
// real. `go env GOMOD` prints os.DevNull, not an empty string, when run
// outside any module; both are checked because nothing in the go toolchain
// documents that as guaranteed forever.
func parseGOMODOutput(out string) (string, error) {
	gomod := strings.TrimSpace(out)
	if gomod == "" || gomod == os.DevNull {
		return "", fmt.Errorf("not inside a Go module -- run from within the rgit checkout")
	}
	return filepath.Dir(gomod), nil
}

// runPrereqChecks verifies what a build needs. go, git, cgo, and a C
// compiler are fatal -- rgit links tree-sitter through cgo, so none of them
// is optional (AGENTS.md's delegation boundary: git is shelled out to for
// everything git already does). tree-sitter is informational only, since SQL
// generation degrades gracefully without it -- and needs no JS runtime, since
// the CLI evaluates grammar.js with its own embedded engine.
//
// git and tree-sitter here use the exact same internal/prereq.LookPath
// mechanism internal/app's doctor does -- see that package's doc comment
// for why the check *list* still isn't shared: doctor reports run-time
// facts, this reports build-time ones too (go toolchain, CGO_ENABLED, a C
// compiler) that would be meaningless for an already-built rgit. Which
// check wins when more than one fails is prereqFatal's job, split out so
// that ordering is testable without shelling out to go/exec.LookPath for
// real five times per test.
func runPrereqChecks() (checks []prereq.Check, fatal error) {
	goCheck := prereq.LookPath("go toolchain", "go", "")
	gitCheck := prereq.LookPath("git", "git", "")

	cgo := goEnv("CGO_ENABLED")
	cgoCheck := prereq.Check{Name: "CGO_ENABLED", OK: cgo == "1", Detail: cgo}

	cc := goEnv("CC")
	ccCheck := prereq.LookPath("C compiler ("+cc+")", firstField(cc), "")

	tsCheck := prereq.LookPath("tree-sitter CLI", "tree-sitter", "optional -- needed only to generate the SQL parser")

	checks = []prereq.Check{goCheck, gitCheck, cgoCheck, ccCheck, tsCheck}
	fatal = prereqFatal(goCheck, gitCheck, cgoCheck, ccCheck, cc)
	return checks, fatal
}

// prereqFatal decides which failing check aborts the install and with what
// message. go, git, cgo, and the C compiler are each fatal on their own;
// first-failure-wins follows runPrereqChecks' own order, so a caller with
// several things missing at once always sees the same single root cause
// rather than a message that shuffles with which check ran last.
func prereqFatal(goCheck, gitCheck, cgoCheck, ccCheck prereq.Check, cc string) error {
	switch {
	case !goCheck.OK:
		return fmt.Errorf("go not found on PATH")
	case !gitCheck.OK:
		return fmt.Errorf("git not found on PATH")
	case !cgoCheck.OK:
		return fmt.Errorf("cgo is disabled (CGO_ENABLED=%s) -- rgit links tree-sitter through cgo and cannot build without it", cgoCheck.Detail)
	case !ccCheck.OK:
		return fmt.Errorf("no %q C compiler found on PATH", cc)
	default:
		return nil
	}
}

func goEnv(name string) string {
	out, err := exec.CommandContext(context.Background(), "go", "env", name).Output() //nolint:gosec // name is selected from installer-owned Go environment probes, not user input
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func firstField(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

// findSQLAdapter looks for an in-repo package, built under the rgit_sql tag,
// that is the cgo binding generation targets. The package is discovered by
// name (sqlAdapterPackageName) rather than by a hardcoded directory path, so
// the adapter can move without this installer silently going blind -- but
// not by import content: see sqlAdapterPackageName's own doc comment for why
// the earlier "does it import tree-sitter-sql" check could never match.
// len(CgoFiles) > 0 additionally confirms it is the cgo binding itself, not
// some unrelated package that happens to share the name.
//
// `go list -tags rgit_sql -e -json ./...` succeeds here even before
// generation has ever run: cgo's own #include "csrc/parser.c" is a
// C-preprocessor directive inside a Go source comment, invisible to `go
// list`, which only needs the .go file to parse -- exactly the state a
// clean checkout is in. -e is required, not optional, on that clean
// checkout: generated_check.go's own go:embed csrc/scanner.c has no match
// yet either, and without -e that load error alone fails the whole `go
// list` invocation (exit 1, no JSON at all) before CgoFiles is ever
// reported -- the discovery step this function exists for would never
// find its own target on the exact checkout state it is meant to run
// against. -e demotes that to a per-package Error field this function
// never reads, while CgoFiles/Name are still populated from the syntax
// scan that ran before the embed pattern was resolved.
func findSQLAdapter(repoRoot string) (dir string, ok bool) {
	cmd := exec.CommandContext(context.Background(), "go", "list", "-tags", "rgit_sql", "-e", "-json", "./...")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return parseSQLAdapterListing(out)
}

// parseSQLAdapterListing scans `go list -json ./...`'s own output -- a
// stream of concatenated JSON objects, not an array -- for the SQL adapter
// package. Split out from findSQLAdapter so the matching rule (name, plus a
// non-empty CgoFiles) is testable against a synthetic listing instead of a
// real `go list` run.
func parseSQLAdapterListing(out []byte) (dir string, ok bool) {
	dec := json.NewDecoder(bytes.NewReader(out))
	for dec.More() {
		var pkg struct {
			Dir      string   `json:"Dir"`
			Name     string   `json:"Name"`
			CgoFiles []string `json:"CgoFiles"`
		}
		if err := dec.Decode(&pkg); err != nil {
			return "", false
		}
		if pkg.Name == sqlAdapterPackageName && len(pkg.CgoFiles) > 0 {
			return pkg.Dir, true
		}
	}
	return "", false
}

// generateSQLParser produces ABI 15 C sources for the SQL grammar into
// pkgDir/csrc, when it can. It never fails the install: a missing
// tree-sitter CLI, an unresolvable grammar module, or a generation error
// all fall back to reporting why and continuing without SQL, so a user
// without the tree-sitter CLI still gets a working rgit.
//
// lookPath is the one seam worth injecting here: it makes the fast,
// no-network "tree-sitter isn't installed" path -- the common case on a
// machine that never opted into SQL support -- testable without a real CLI.
// Everything past it (downloadSQLGrammarModule, runSQLGeneration) needs the
// network and the tree-sitter CLI for real; see those functions' own doc
// comments for why that part stays exercised by hand rather than mocked.
func generateSQLParser(repoRoot, pkgDir string, dryRun bool) (ok bool, msg string) {
	return generateSQLParserWith(repoRoot, pkgDir, dryRun, exec.LookPath)
}

func generateSQLParserWith(repoRoot, pkgDir string, dryRun bool, lookPath func(string) (string, error)) (ok bool, msg string) {
	if _, err := lookPath("tree-sitter"); err != nil {
		return false, "tree-sitter CLI not found on PATH -- installing rgit without SQL support"
	}
	modDir, err := downloadSQLGrammarModule(repoRoot)
	if err != nil {
		return false, fmt.Sprintf("%s@%s not resolvable (%v) -- installing rgit without SQL support", sqlGrammarModule, sqlGrammarVersion, err)
	}
	if _, err := os.Stat(filepath.Join(modDir, "grammar.js")); err != nil {
		return false, fmt.Sprintf("%s has no grammar.js -- installing rgit without SQL support", sqlGrammarModule)
	}
	if dryRun {
		return true, fmt.Sprintf("would generate the SQL parser into %s/csrc", relTo(repoRoot, pkgDir))
	}
	if err := runSQLGeneration(modDir, pkgDir); err != nil {
		return false, fmt.Sprintf("SQL generation failed (%v) -- installing rgit without SQL support", err)
	}
	return true, fmt.Sprintf("generated the SQL parser into %s/csrc (ABI 15)", relTo(repoRoot, pkgDir))
}

// downloadSQLGrammarModule resolves the on-disk cache directory of the SQL
// grammar module at its pinned version, without ever touching go.mod.
//
// A bare `go list -m -f {{.Dir}} <path>` (no version) only answers for a
// module already on the main module's build list -- i.e. named in a
// `require`, direct or indirect -- which this one never is (see
// sqlGrammarVersion's doc comment). Qualifying the query with an explicit
// "<path>@<version>" instead makes both `go mod download` and `go list -m`
// treat it as an ad-hoc query against the module proxy/cache: verified
// directly against this module and version, `go mod download
// <path>@<version>` populates the module cache and leaves go.mod
// byte-for-byte unchanged, and a subsequent `go list -m -f {{.Dir}}
// <path>@<version>` then resolves the same cache directory `go env
// GOMODCACHE` would.
//
// No test seam here: the only two things worth asserting -- "it downloads
// the pinned version" and "it resolves the module cache path" -- both
// require the real network and the real module proxy, which is exactly what
// CONTRIBUTING.md's "prefer the real dependency over a double" already rules
// out mocking. What would catch drift: `go run ./cmd/rgit-install` by hand
// with the tree-sitter CLI on PATH -- a broken download surfaces immediately
// as "not resolvable" in generateSQLParser's own message, and ci.yml's own
// top comment records that this is deliberately the only place SQL
// generation ever runs, so nothing else in CI exercises it either.
func downloadSQLGrammarModule(repoRoot string) (string, error) {
	versioned := sqlGrammarModule + "@" + sqlGrammarVersion

	dlCmd := exec.CommandContext(context.Background(), "go", "mod", "download", versioned)
	dlCmd.Dir = repoRoot
	var stderr bytes.Buffer
	dlCmd.Stderr = &stderr
	if err := dlCmd.Run(); err != nil {
		return "", fmt.Errorf("go mod download %s: %w: %s", versioned, err, stderr.String())
	}

	cmd := exec.CommandContext(context.Background(), "go", "list", "-m", "-f", "{{.Dir}}", versioned)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		return "", fmt.Errorf("no cached module directory")
	}
	return dir, nil
}

// runSQLGeneration copies grammar.js and tree-sitter.json into a scratch
// directory (the module cache is read-only, and tree-sitter.json alongside
// grammar.js is what yields ABI 15 instead of a silent ABI 14), runs
// `tree-sitter generate` there, then copies the result -- plus the
// module's own scanner.c -- into pkgDir/csrc. The generated C must live in
// that subdirectory rather than pkgDir itself: putting it directly in the
// package directory makes cgo compile it and the adapter's #include pull
// it in again, a duplicate-symbol link error.
//
// No test seam: this needs the real tree-sitter CLI, and a real generation
// runs ~27s -- far past this package's whole test budget for one function.
// The two parts that could silently drift both already have their own
// direct checks downstream of this function: parserABIVersion (tested)
// catches a stale-ABI CLI, and finalize (below) either lands the new csrc/
// cleanly or restores the prior one, never leaving neither. What would
// catch drift in the mechanics here specifically: run `go run
// ./cmd/rgit-install` by hand with the tree-sitter CLI on PATH and confirm
// "generated the SQL parser into .../csrc (ABI 15)" prints, then `go build
// -tags rgit_sql ./cmd/rgit` to prove the result actually compiles.
func runSQLGeneration(modDir, pkgDir string) error {
	scratch, err := os.MkdirTemp("", "rgit-sql-gen-*")
	if err != nil {
		return fmt.Errorf("scratch dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(scratch) }()

	if err := copyFile(filepath.Join(modDir, "grammar.js"), filepath.Join(scratch, "grammar.js")); err != nil {
		return err
	}
	if err := copyFile(filepath.Join(modDir, "tree-sitter.json"), filepath.Join(scratch, "tree-sitter.json")); err != nil {
		return err
	}

	cmd := exec.CommandContext(context.Background(), "tree-sitter", "generate")
	cmd.Dir = scratch
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tree-sitter generate: %w: %s", err, stderr.String())
	}

	// tree-sitter.json alongside grammar.js is what is supposed to yield ABI
	// 15; an outdated CLI can still silently emit ABI 14 anyway (that's the
	// exact failure mode tree-sitter.json exists to prevent), which would
	// otherwise surface only much later as a confusing cgo/link error. Check
	// the thing that actually matters -- the LANGUAGE_VERSION the generated
	// parser.c itself declares -- before anything downstream trusts it.
	generatedParser := filepath.Join(scratch, "src", "parser.c")
	abi, err := parserABIVersion(generatedParser)
	if err != nil {
		return fmt.Errorf("read generated parser.c: %w", err)
	}
	if abi != 15 {
		return fmt.Errorf("generated parser.c is ABI %d, want 15 -- tree-sitter CLI may be outdated", abi)
	}

	// Staged as a sibling of the real csrc/ (same filesystem as pkgDir,
	// unlike the scratch dir above which may be on tmpfs) so the finishing
	// os.Rename is atomic. Nothing under csrc/ itself is touched during
	// copying: a failed copy here leaves any existing csrc/ from a prior
	// successful generation exactly as it was, rather than a
	// half-overwritten one a later run would build on top of. Finalizing
	// below (moving the prior csrc/ aside rather than deleting it first)
	// extends that same guarantee through the swap itself.
	staging := filepath.Join(pkgDir, "csrc.tmp")
	if err := os.RemoveAll(staging); err != nil {
		return fmt.Errorf("clear stale staging dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(staging) }()

	treeSitterDir := filepath.Join(staging, "tree_sitter")
	if err := os.MkdirAll(treeSitterDir, 0o750); err != nil {
		return err
	}
	copies := [][2]string{
		{generatedParser, filepath.Join(staging, "parser.c")},
		{filepath.Join(scratch, "src", "tree_sitter", "parser.h"), filepath.Join(treeSitterDir, "parser.h")},
		{filepath.Join(scratch, "src", "tree_sitter", "array.h"), filepath.Join(treeSitterDir, "array.h")},
		{filepath.Join(scratch, "src", "tree_sitter", "alloc.h"), filepath.Join(treeSitterDir, "alloc.h")},
		{filepath.Join(modDir, "src", "scanner.c"), filepath.Join(staging, "scanner.c")},
	}
	for _, c := range copies {
		if err := copyFile(c[0], c[1]); err != nil {
			return fmt.Errorf("copy %s: %w", filepath.Base(c[0]), err)
		}
	}

	return finalizeGenerated(staging, filepath.Join(pkgDir, "csrc"))
}

// finalizeGenerated swaps staging into place at final without ever leaving
// neither a working nor a prior-good directory there: an existing final is
// moved aside to final+".old" (same filesystem, so the move is a rename,
// not a copy) rather than removed outright, so a failure partway through
// still has something to restore. If the finishing rename itself fails,
// final.old is renamed back to final before returning the error -- the
// previous version of this function called os.RemoveAll(final) before
// os.Rename(staging, final), so a rename failure (or the process dying
// between the two calls) left a repository with no csrc/ at all, even
// though a perfectly good one existed a moment earlier.
func finalizeGenerated(staging, final string) error {
	old := final + ".old"
	// A leftover from a prior failed finalize would otherwise block the
	// rename below (a non-empty directory cannot be renamed onto).
	// Best-effort: if this fails, the rename immediately after reports the
	// real problem.
	_ = os.RemoveAll(old)

	hadFinal := false
	if _, err := os.Lstat(final); err == nil {
		hadFinal = true
		if err := os.Rename(final, old); err != nil {
			return fmt.Errorf("move existing %s aside: %w", final, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", final, err)
	}

	if err := os.Rename(staging, final); err != nil {
		if hadFinal {
			// Restore exactly what was there before finalize started --
			// the whole point of moving it aside instead of deleting it.
			if restoreErr := os.Rename(old, final); restoreErr != nil {
				return fmt.Errorf("finalize %s: %w (restoring prior version also failed: %w -- it is preserved at %s)", final, err, restoreErr, old)
			}
		}
		return fmt.Errorf("finalize %s: %w", final, err)
	}
	if hadFinal {
		_ = os.RemoveAll(old)
	}
	return nil
}

// parserABIVersion reads the ABI a generated parser.c declares by scanning
// its `#define LANGUAGE_VERSION N` line -- the cheap, direct check for what
// tree-sitter.json is supposed to guarantee (see runSQLGeneration).
func parserABIVersion(path string) (int, error) {
	data, err := readInstallerFile(path)
	if err != nil {
		return 0, err
	}
	const marker = "#define LANGUAGE_VERSION "
	_, rest, ok := bytes.Cut(data, []byte(marker))
	if !ok {
		return 0, fmt.Errorf("no LANGUAGE_VERSION define found")
	}
	if end := bytes.IndexByte(rest, '\n'); end >= 0 {
		rest = rest[:end]
	}
	return strconv.Atoi(strings.TrimSpace(string(rest)))
}

func copyFile(src, dst string) error {
	data, err := readInstallerFile(src)
	if err != nil {
		return err
	}
	return writeInstallerFile(dst, data, 0o600)
}

func readInstallerFile(path string) (data []byte, err error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	return root.ReadFile(filepath.Base(path))
}

func writeInstallerFile(path string, data []byte, perm os.FileMode) (err error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	return root.WriteFile(filepath.Base(path), data, perm)
}

func relTo(root, path string) string {
	if r, err := filepath.Rel(root, path); err == nil {
		return r
	}
	return path
}

// gitVersion mirrors what docs/USAGE.md documents: rgit --version reads
// "dev" whenever the build did not stamp one. An empty return here leaves
// that stamp unset rather than inventing a version scheme this repo has
// not established.
func gitVersion(repoRoot string) string {
	out, err := exec.CommandContext(context.Background(), "git", "-C", repoRoot, "describe", "--tags", "--always", "--dirty").Output() //nolint:gosec // repoRoot is passed as git's -C argument, never interpreted by a shell
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func versionOrDev(v string) string {
	if v == "" {
		return "dev"
	}
	return v
}

func ldflags(ver string) string {
	f := "-s -w"
	if ver != "" {
		f += " -X main.version=" + ver
	}
	return f
}

// installArgs constructs the `go install` argv, split out from runInstall
// so the flag wiring -- ldflags always present, -tags rgit_sql only when sql
// is requested, in that order -- is testable without a real, multi-second
// compile.
func installArgs(sql bool, ver string) []string {
	args := []string{"install", "-ldflags", ldflags(ver)}
	if sql {
		args = append(args, "-tags", "rgit_sql")
	}
	return append(args, "./cmd/rgit")
}

// runInstall builds and installs ./cmd/rgit with `go install`, which already
// resolves GOBIN, creates the directory, and renames the finished binary
// into place -- so a failed build never leaves a truncated rgit at the
// destination. GOBIN is set explicitly so -prefix reaches the same
// mechanism rather than a hand-rolled copy beside it.
//
// No test seam past installArgs: a real invocation compiles this repo's own
// cgo-linked binary, multiple seconds even from a warm cache -- far over
// this package's test budget, and every e2e case elsewhere in this repo
// already proves `go build ./cmd/rgit` itself works. What would catch drift
// specifically in the CGO_CFLAGS cache-busting below: touch a file under
// sqlPkgDir/csrc, rebuild with -tags rgit_sql, and confirm the change is
// reflected rather than silently served from a stale cached object -- the
// exact regression sqlCSRCContentHash (tested) exists to prevent.
func runInstall(repoRoot string, sql bool, sqlPkgDir, ver, prefix string) error {
	cmd := exec.CommandContext(context.Background(), "go", installArgs(sql, ver)...) //nolint:gosec // installer-owned go flags and repository-derived version are passed without a shell
	cmd.Dir = repoRoot
	env := append(os.Environ(), "GOBIN="+prefix)
	if sql {
		// Go's build cache does not otherwise notice csrc/ changing: the
		// generated C reaches the compiler only through a C #include inside
		// grammar.go's cgo comment, which the go tool never reads as a
		// build input, so the package's cache key stays keyed on grammar.go
		// alone -- a corrupted parser.c with grammar.go untouched would
		// build silently from a stale cached object. CGO_CFLAGS is one of
		// the environment variables Go's cache genuinely does key cgo
		// compiles on, so folding the actual csrc/ content into it -- as an
		// inert, unreferenced macro -- makes the cache key honestly track
		// what will get compiled, without the whole-world cost of -a.
		//
		// A hashing failure (e.g. csrc/ genuinely missing) is left for the
		// build itself to report -- it will fail with a much clearer
		// "no such file" than anything worth synthesizing here.
		if hash, herr := sqlCSRCContentHash(sqlPkgDir); herr == nil {
			flag := "-DRGIT_SQL_CSRC_HASH=" + hash
			if existing := os.Getenv("CGO_CFLAGS"); existing != "" {
				flag = existing + " " + flag
			}
			env = append(env, "CGO_CFLAGS="+flag)
		}
	}
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go install: %w: %s", err, stderr.String())
	}
	return nil
}

// sqlCSRCContentHash hashes every file under pkgDir/csrc, in path order, so
// buildBinary can fold the result into CGO_CFLAGS. See buildBinary for why
// this exists rather than relying on Go's ordinary dependency tracking.
func sqlCSRCContentHash(pkgDir string) (result string, err error) {
	csrc := filepath.Join(pkgDir, "csrc")
	var paths []string
	if err := filepath.WalkDir(csrc, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		return "", err
	}
	slices.Sort(paths)

	root, err := os.OpenRoot(csrc)
	if err != nil {
		return "", err
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil && err == nil {
			result = ""
			err = closeErr
		}
	}()

	h := sha256.New()
	for _, p := range paths {
		rel, err := filepath.Rel(csrc, p)
		if err != nil {
			return "", err
		}
		data, err := root.ReadFile(rel)
		if err != nil {
			return "", err
		}
		if _, err := io.WriteString(h, rel); err != nil {
			return "", err
		}
		if _, err := h.Write(data); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

// resolvePrefix follows Go convention: $GOBIN if set, else
// $(go env GOPATH)/bin. -prefix overrides both.
func resolvePrefix(flagPrefix string) (string, error) {
	if flagPrefix != "" {
		return flagPrefix, nil
	}
	if gobin := goEnv("GOBIN"); gobin != "" {
		return gobin, nil
	}
	gopath := goEnv("GOPATH")
	if gopath == "" {
		return "", fmt.Errorf("neither GOBIN nor GOPATH is set")
	}
	return filepath.Join(gopath, "bin"), nil
}

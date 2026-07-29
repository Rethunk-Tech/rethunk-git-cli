// Command rgit-install is the one-command path for a user who does not want
// to run make: it checks prerequisites, generates the SQL parser when it
// can, builds rgit, and installs the binary. It is deliberately stdlib-only
// -- see CONTRIBUTING.md § Dependencies -- so verifying a build environment
// never itself needs a working build environment.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// sqlGrammarModule is the Go module that publishes the SQL grammar's
// grammar.js and tree-sitter.json, but not a working parser.c (measured:
// the module gitignores it at every tag, so its own bindings/go cannot
// compile). rgit generates that file itself; see generateSQLParser.
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
// at all, verified against this exact module and version.
const (
	sqlGrammarModule  = "github.com/DerekStride/tree-sitter-sql"
	sqlGrammarVersion = "v0.3.11"
)

// sqlAdapterPackageName is the package name findSQLAdapter looks for. Matching
// by name rather than by import content is what changed here: an earlier
// draft searched go list's own Imports field for a "tree-sitter-sql"
// substring, on the assumption that whatever package needed generation would
// import the upstream module's own bindings/go directly. It never will --
// internal/resolve/sqlgrammar's binding imports only "C" and "unsafe" (see
// its own doc comment) specifically because that upstream package is the one
// that cannot compile -- so that check silently matched nothing, forever.
const sqlAdapterPackageName = "sqlgrammar"

func main() {
	dryRun := flag.Bool("dry-run", false, "print what would happen without building or installing")
	prefixFlag := flag.String("prefix", "", "install directory (default: $GOBIN, else $(go env GOPATH)/bin)")
	flag.Parse()

	repoRoot, err := resolveRepoRoot()
	if err != nil {
		fatalf("%v", err)
	}

	fmt.Println("Checking prerequisites...")
	checks, fatal := runPrereqChecks()
	for _, c := range checks {
		status := "ok"
		if !c.ok {
			status = "MISSING"
		}
		fmt.Printf("  [%s] %-24s %s\n", status, c.name, c.detail)
	}
	if fatal != nil {
		fatalf("%v", fatal)
	}

	sql := false
	if pkgDir, ok := findSQLAdapter(repoRoot); ok {
		fmt.Println("SQL adapter package detected:", relTo(repoRoot, pkgDir))
		var msg string
		sql, msg = generateSQLParser(repoRoot, pkgDir, *dryRun)
		fmt.Println(" ", msg)
	} else {
		fmt.Println("No SQL adapter package present yet; building without SQL support.")
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

	fmt.Println("Building...")
	bin, cleanup, err := buildBinary(repoRoot, sql, ver)
	if err != nil && sql {
		// generateSQLParser only proves the C it wrote is well-formed enough
		// to reach the compiler; a cgo build can still fail past that (a
		// grammar.js version mismatch, a toolchain quirk). docs/INSTALL.md
		// promises a working rgit without the tree-sitter CLI -- keep that
		// promise here too rather than dying with SQL as the only path tried.
		cleanup()
		fmt.Fprintf(os.Stderr, "rgit-install: SQL build failed, retrying without SQL support: %v\n", err)
		sql = false
		bin, cleanup, err = buildBinary(repoRoot, sql, ver)
	}
	if err != nil {
		fatalf("build failed: %v", err)
	}
	defer cleanup()

	replaced, err := installBinary(bin, dest)
	if err != nil {
		fatalf("install failed: %v", err)
	}

	action := "Installed"
	if replaced {
		action = "Replaced existing binary at"
	}
	fmt.Printf("%s %s (version %s)\n", action, dest, versionOrDev(ver))
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "rgit-install: "+format+"\n", args...)
	os.Exit(1)
}

// resolveRepoRoot finds the module root by asking the go tool rather than
// walking up looking for go.mod by hand -- it already knows the answer and
// already accounts for GOFLAGS, workspace files, and everything else that
// can move where "the module" is.
func resolveRepoRoot() (string, error) {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", fmt.Errorf("go env GOMOD: %w", err)
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == os.DevNull {
		return "", fmt.Errorf("not inside a Go module -- run from within the rgit checkout")
	}
	return filepath.Dir(gomod), nil
}

type prereqCheck struct {
	name   string
	ok     bool
	detail string
}

// runPrereqChecks verifies what a build needs. go, git, cgo, and a C
// compiler are fatal -- rgit links tree-sitter through cgo, so none of them
// is optional (AGENTS.md's delegation boundary: git is shelled out to for
// everything git already does). tree-sitter and a JS runtime are informational
// only, since SQL generation degrades gracefully without them.
func runPrereqChecks() (checks []prereqCheck, fatal error) {
	goPath, err := exec.LookPath("go")
	checks = append(checks, prereqCheck{"go toolchain", err == nil, goPath})
	if err != nil {
		fatal = fmt.Errorf("go not found on PATH")
	}

	gitPath, err := exec.LookPath("git")
	checks = append(checks, prereqCheck{"git", err == nil, gitPath})
	if err != nil && fatal == nil {
		fatal = fmt.Errorf("git not found on PATH")
	}

	cgo := goEnv("CGO_ENABLED")
	checks = append(checks, prereqCheck{"CGO_ENABLED", cgo == "1", cgo})
	if cgo != "1" && fatal == nil {
		fatal = fmt.Errorf("cgo is disabled (CGO_ENABLED=%s) -- rgit links tree-sitter through cgo and cannot build without it", cgo)
	}

	cc := goEnv("CC")
	ccPath, ccErr := exec.LookPath(firstField(cc))
	checks = append(checks, prereqCheck{"C compiler (" + cc + ")", ccErr == nil, ccPath})
	if ccErr != nil && fatal == nil {
		fatal = fmt.Errorf("C compiler %q not found on PATH", cc)
	}

	tsPath, tsErr := exec.LookPath("tree-sitter")
	tsDetail := tsPath
	if tsErr != nil {
		tsDetail = "optional -- needed only to generate the SQL parser"
	}
	checks = append(checks, prereqCheck{"tree-sitter CLI", tsErr == nil, tsDetail})

	return checks, fatal
}

func goEnv(name string) string {
	out, err := exec.Command("go", "env", name).Output()
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
// `go list -tags rgit_sql -json ./...` succeeds here even before generation
// has ever run: cgo's own #include "csrc/parser.c" is a C-preprocessor
// directive inside a Go source comment, invisible to `go list`, which only
// needs the .go file to parse -- verified directly, since this is exactly
// the state a clean checkout is in.
func findSQLAdapter(repoRoot string) (dir string, ok bool) {
	cmd := exec.Command("go", "list", "-tags", "rgit_sql", "-json", "./...")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	for dec.More() {
		var pkg struct {
			Dir      string
			Name     string
			CgoFiles []string
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
// all fall back to reporting why and continuing without SQL -- the
// pre-decided call that a user without the tree-sitter CLI still gets a
// working rgit.
func generateSQLParser(repoRoot, pkgDir string, dryRun bool) (ok bool, msg string) {
	if _, err := exec.LookPath("tree-sitter"); err != nil {
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
func downloadSQLGrammarModule(repoRoot string) (string, error) {
	versioned := sqlGrammarModule + "@" + sqlGrammarVersion

	dlCmd := exec.Command("go", "mod", "download", versioned)
	dlCmd.Dir = repoRoot
	var stderr bytes.Buffer
	dlCmd.Stderr = &stderr
	if err := dlCmd.Run(); err != nil {
		return "", fmt.Errorf("go mod download %s: %w: %s", versioned, err, stderr.String())
	}

	cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", versioned)
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

// runSQLGeneration is the mechanics measured for this repo: copy grammar.js
// and tree-sitter.json into a scratch directory (the module cache is
// read-only, and tree-sitter.json alongside grammar.js is what yields ABI
// 15 instead of a silent ABI 14), run `tree-sitter generate` there, then
// copy the result -- plus the module's own scanner.c -- into pkgDir/csrc.
// The generated C must live in that subdirectory rather than pkgDir itself:
// measured, putting it directly in the package directory makes cgo compile
// it and the adapter's #include pull it in again, a duplicate-symbol link
// error.
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

	cmd := exec.Command("tree-sitter", "generate")
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
	// os.Rename is atomic. Nothing under csrc/ itself is touched until every
	// file below has copied cleanly: a failed copy here leaves any existing
	// csrc/ from a prior successful generation exactly as it was, rather
	// than a half-overwritten one a later run would build on top of.
	staging := filepath.Join(pkgDir, "csrc.tmp")
	if err := os.RemoveAll(staging); err != nil {
		return fmt.Errorf("clear stale staging dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(staging) }()

	treeSitterDir := filepath.Join(staging, "tree_sitter")
	if err := os.MkdirAll(treeSitterDir, 0o755); err != nil {
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

	final := filepath.Join(pkgDir, "csrc")
	if err := os.RemoveAll(final); err != nil {
		return fmt.Errorf("remove stale %s: %w", final, err)
	}
	if err := os.Rename(staging, final); err != nil {
		return fmt.Errorf("finalize %s: %w", final, err)
	}
	return nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
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
	out, err := exec.Command("git", "-C", repoRoot, "describe", "--tags", "--always", "--dirty").Output()
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

// buildBinary builds ./cmd/rgit into a fresh temp directory rather than
// straight to the install prefix, so a build failure never leaves a
// half-written binary at the destination.
func buildBinary(repoRoot string, sql bool, ver string) (bin string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "rgit-install-build-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup = func() { _ = os.RemoveAll(dir) }

	bin = filepath.Join(dir, "rgit")
	args := []string{"build", "-ldflags", ldflags(ver), "-o", bin}
	if sql {
		args = append(args, "-tags", "rgit_sql")
	}
	args = append(args, "./cmd/rgit")

	cmd := exec.Command("go", args...)
	cmd.Dir = repoRoot
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", cleanup, fmt.Errorf("go build: %w: %s", err, stderr.String())
	}
	return bin, cleanup, nil
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

// installBinary writes to a temp file beside dest and renames over it, so a
// crash mid-install never leaves a truncated binary at the install path --
// the same reasoning the invariants table in AGENTS.md applies to staging.
func installBinary(bin, dest string) (replaced bool, err error) {
	if _, err := os.Stat(dest); err == nil {
		replaced = true
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return replaced, err
	}
	data, err := os.ReadFile(bin)
	if err != nil {
		return replaced, err
	}
	tmp := dest + ".tmp"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return replaced, err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return replaced, err
	}
	return replaced, nil
}

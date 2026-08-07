package prereq

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	qt "github.com/go-quicktest/qt"
)

func TestLookPath(t *testing.T) {
	t.Parallel()

	t.Run("found reports the resolved path", func(t *testing.T) {
		t.Parallel()
		// git is this repo's own hard requirement (CONTRIBUTING.md,
		// internal/gittest shells out to it already), so it is always on
		// PATH in a test environment -- the real dependency, not a double.
		c := LookPath("git", "git", "")
		qt.Assert(t, qt.Equals(c.Name, "git"))
		qt.Assert(t, qt.IsTrue(c.OK))
		qt.Assert(t, qt.Not(qt.Equals(c.Detail, "")))
	})

	t.Run("missing carries the caller's own note", func(t *testing.T) {
		t.Parallel()
		c := LookPath("nonexistent-tool", "rgit-prereq-test-does-not-exist", "optional -- see docs")
		qt.Assert(t, qt.IsFalse(c.OK))
		qt.Assert(t, qt.Equals(c.Detail, "optional -- see docs"))
	})

	// doctor's and cmd/rgit-install's git checks report an empty detail on
	// failure (no note at all); a LookPath that silently substituted
	// something else here would change both commands' output.
	t.Run("missing with no note reports an empty detail", func(t *testing.T) {
		t.Parallel()
		c := LookPath("nonexistent-tool", "rgit-prereq-test-does-not-exist", "")
		qt.Assert(t, qt.IsFalse(c.OK))
		qt.Assert(t, qt.Equals(c.Detail, ""))
	})
}

// TestPrint pins both alignment rules at once, since either one alone still
// leaves a jagged column: the status field is padded so [ok] and [MISSING]
// start the name at the same column, and the name field is padded to the
// width Width reported for the whole group rather than a constant a long
// language-server label overruns.
func TestPrint(t *testing.T) {
	t.Parallel()

	group := []Check{
		{Name: "git", OK: true, Detail: "/usr/bin/git"},
		{Name: "tree-sitter CLI", OK: false, Detail: "optional"},
		{Name: "vscode-json-language-server (json)", OK: true, Detail: "/home/u/.bun/bin/vscode-json-language-server"},
	}
	width := Width(group...)

	var buf bytes.Buffer
	for _, c := range group {
		Print(&buf, width, c)
	}

	want := "" +
		"  [ok]      git                                /usr/bin/git\n" +
		"  [MISSING] tree-sitter CLI                    optional\n" +
		"  [ok]      vscode-json-language-server (json) /home/u/.bun/bin/vscode-json-language-server\n"
	qt.Assert(t, qt.Equals(buf.String(), want))
}

func TestGitVersion_Less(t *testing.T) {
	t.Parallel()

	qt.Assert(t, qt.IsTrue(GitVersion{Major: 2, Minor: 31, Patch: 9}.Less(GitVersion{Major: 2, Minor: 32, Patch: 0})))
	qt.Assert(t, qt.IsFalse(GitVersion{Major: 2, Minor: 32, Patch: 0}.Less(GitVersion{Major: 2, Minor: 32, Patch: 0})))
	qt.Assert(t, qt.IsTrue(GitVersion{Major: 1, Minor: 99, Patch: 99}.Less(GitVersion{Major: 2, Minor: 0, Patch: 0})))
	qt.Assert(t, qt.IsFalse(GitVersion{Major: 2, Minor: 32, Patch: 1}.Less(GitVersion{Major: 2, Minor: 32, Patch: 0})))
}

func TestCheckGitVersion(t *testing.T) {
	t.Parallel()

	t.Run("real git on this machine meets the floor", func(t *testing.T) {
		t.Parallel()
		gitPath, err := exec.LookPath("git")
		qt.Assert(t, qt.IsNil(err))
		c := CheckGitVersion(gitPath, MinGitVersion)
		qt.Assert(t, qt.Equals(c.Name, "git version"))
		qt.Assert(t, qt.IsTrue(c.OK))
		qt.Assert(t, qt.Not(qt.Equals(c.Detail, "")))
	})

	t.Run("an unreachable binary is not fatal, just unconfirmed", func(t *testing.T) {
		t.Parallel()
		c := CheckGitVersion("rgit-prereq-test-does-not-exist", MinGitVersion)
		qt.Assert(t, qt.IsFalse(c.OK))
		qt.Assert(t, qt.Equals(c.Detail, "could not run `git --version`"))
	})

	t.Run("a version below the floor reports both numbers", func(t *testing.T) {
		t.Parallel()
		script := writeFakeGit(t, "git version 2.20.1")
		c := CheckGitVersion(script, GitVersion{Major: 2, Minor: 32, Patch: 0})
		qt.Assert(t, qt.IsFalse(c.OK))
		qt.Assert(t, qt.Equals(c.Detail, "2.20.1 found, need >= 2.32.0 -- see docs/INSTALL.md § Prerequisites"))
	})

	t.Run("a version at the floor passes", func(t *testing.T) {
		t.Parallel()
		script := writeFakeGit(t, "git version 2.32.0")
		c := CheckGitVersion(script, GitVersion{Major: 2, Minor: 32, Patch: 0})
		qt.Assert(t, qt.IsTrue(c.OK))
		qt.Assert(t, qt.Equals(c.Detail, "2.32.0"))
	})

	t.Run("platform-suffixed output still parses", func(t *testing.T) {
		t.Parallel()
		script := writeFakeGit(t, "git version 2.39.3 (Apple Git-146)")
		c := CheckGitVersion(script, GitVersion{Major: 2, Minor: 32, Patch: 0})
		qt.Assert(t, qt.IsTrue(c.OK))
		qt.Assert(t, qt.Equals(c.Detail, "2.39.3"))
	})

	t.Run("unparseable output is reported, not silently accepted", func(t *testing.T) {
		t.Parallel()
		script := writeFakeGit(t, "not a version string")
		c := CheckGitVersion(script, MinGitVersion)
		qt.Assert(t, qt.IsFalse(c.OK))
		qt.Assert(t, qt.Equals(c.Detail, `unparseable `+"`git --version`"+` output "not a version string"`))
	})
}

// writeFakeGit writes an executable shell script under t.TempDir() that
// echoes versionLine for any "--version" invocation, so CheckGitVersion's
// parsing can be pinned against exact strings without depending on the
// real git binary's own current version.
func writeFakeGit(t *testing.T, versionLine string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-git.sh")
	script := "#!/bin/sh\necho '" + versionLine + "'\n"
	qt.Assert(t, qt.IsNil(os.WriteFile(path, []byte(script), 0o755)))
	return path
}

// TestWidth_FloorKeepsShortGroupsFromCollapsing guards cmd/rgit-install's
// own output, whose five names are all short: without a floor the detail
// column would slide left to hug them, churning a layout that already reads
// well.
func TestWidth_FloorKeepsShortGroupsFromCollapsing(t *testing.T) {
	t.Parallel()

	qt.Assert(t, qt.Equals(Width(Check{Name: "go"}, Check{Name: "git"}), minNameWidth))
	qt.Assert(t, qt.Equals(Width(), minNameWidth))

	long := Check{Name: "vscode-json-language-server (json)"}
	qt.Assert(t, qt.Equals(Width(long), len(long.Name)))
}

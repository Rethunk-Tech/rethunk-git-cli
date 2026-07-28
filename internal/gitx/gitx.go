// Package gitx is the sole place in rgit permitted to exec git. Everything
// else — hooks, pathspec matching, trailers, credential prompting, index
// bookkeeping — is git's own job; see AGENTS.md § Delegation boundary.
//
// Every method distinguishes three outcomes:
//
//   - git ran and gave a normal negative answer (e.g. a revision that does
//     not exist): reported through the method's own return values, err is
//     nil.
//   - git ran but exited in a way the method does not treat as a plain
//     answer (a hook rejection, a malformed pathspec): reported as a
//     *GitError.
//   - git could not be run or waited on at all (binary missing, context
//     canceled): reported as a *ExecError.
//
// Callers never need to pattern-match stderr text to tell these apart.
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/term"
)

// Repo wraps git invocations rooted at a working directory. dir need not be
// the top of the worktree; git resolves that itself via -C.
type Repo struct {
	root string
	env  []string
}

// New returns a Repo rooted at dir.
//
// If the caller has not already set GIT_TERMINAL_PROMPT in its own
// environment, New sets it to "0" for every invocation whenever stdin is
// not a terminal, so a credential or GPG prompt fails fast instead of
// hanging a non-interactive caller forever. A caller-supplied value is
// always respected.
func New(dir string) *Repo {
	env := os.Environ()
	if _, isSet := os.LookupEnv("GIT_TERMINAL_PROMPT"); !isSet {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			env = append(env, "GIT_TERMINAL_PROMPT=0")
		}
	}
	return &Repo{root: dir, env: env}
}

// Root returns the directory this Repo was opened against.
func (r *Repo) Root() string { return r.root }

// Result is the raw outcome of a git invocation that ran to completion,
// whatever its exit status. A non-zero ExitCode is not itself an error —
// see the package doc for how individual methods interpret it.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// ExecError means git could not be executed or waited on at all: the
// binary was missing, the working directory was invalid, or the process
// was killed. It is never returned for an exit code git itself chose.
type ExecError struct {
	Args []string
	Err  error
}

func (e *ExecError) Error() string {
	return fmt.Sprintf("git %s: %v", strings.Join(e.Args, " "), e.Err)
}

func (e *ExecError) Unwrap() error { return e.Err }

// GitError means git ran and exited with a status the calling method does
// not recognize as a normal answer — a hook rejection, a malformed
// pathspec, a fatal internal error. Distinct from ExecError, which means
// git never got the chance to exit at all.
type GitError struct {
	Args     []string
	ExitCode int
	Stderr   []byte
}

func (e *GitError) Error() string {
	return fmt.Sprintf("git %s: exit %d: %s", strings.Join(e.Args, " "), e.ExitCode, bytes.TrimSpace(e.Stderr))
}

// run executes git with args against the repo root. err is non-nil only
// when git could not be run or waited on; a non-zero exit status is
// reported through Result.ExitCode with err nil, since that is git
// behaving normally, not failing to run.
func (r *Repo) run(ctx context.Context, stdin io.Reader, args ...string) (Result, error) {
	full := append([]string{"-C", r.root}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = r.env
	cmd.Stdin = stdin

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), ExitCode: exitErr.ExitCode()}, nil
		}
		return Result{}, &ExecError{Args: args, Err: err}
	}
	return Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), ExitCode: 0}, nil
}

// gitError builds a *GitError from a completed Result for the given
// logical args (as passed to the exported method, not the -C-prefixed
// form run() actually executed).
func gitError(args []string, res Result) *GitError {
	return &GitError{Args: args, ExitCode: res.ExitCode, Stderr: res.Stderr}
}

// CatFile reads the blob at rev:path (e.g. "HEAD:auth.go"). Per
// specs/design.md's blob-synthesis algorithm, a path absent from rev is a
// normal case — an untracked file has no HEAD blob — so exists reports
// that distinction directly rather than making callers inspect stderr.
func (r *Repo) CatFile(ctx context.Context, rev, path string) (content []byte, exists bool, err error) {
	res, err := r.run(ctx, nil, "cat-file", "-p", rev+":"+path)
	if err != nil {
		return nil, false, err
	}
	if res.ExitCode != 0 {
		// cat-file exits 128 uniformly for "bad revision" and "path does
		// not exist in tree" alike. Blob synthesis treats both the same
		// way (there is nothing at that rev), so no finer distinction is
		// needed here.
		return nil, false, nil
	}
	return res.Stdout, true, nil
}

// HashObject writes content as a blob via `git hash-object -w --path
// path --stdin`. --path is mandatory here, not optional: without it,
// .gitattributes clean filters and LFS normalization are bypassed.
func (r *Repo) HashObject(ctx context.Context, path string, content []byte) (sha string, err error) {
	args := []string{"hash-object", "-w", "--path", path, "--stdin"}
	res, err := r.run(ctx, bytes.NewReader(content), args...)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", gitError(args, res)
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

// UpdateIndexCacheinfo stages a single entry directly, as the final step
// of blob synthesis: `git update-index --add --cacheinfo mode,sha,path`.
func (r *Repo) UpdateIndexCacheinfo(ctx context.Context, mode, sha, path string) error {
	args := []string{"update-index", "--add", "--cacheinfo", mode + "," + sha + "," + path}
	res, err := r.run(ctx, nil, args...)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return gitError(args, res)
	}
	return nil
}

// Add stages pathspecs verbatim via `git add --`, for targets that are
// plain paths rather than symbol anchors. specs/design.md: staging by
// path is plain `git add <pathspec>`, delegated rather than synthesized,
// so every pathspec form (globs, ":(exclude)...") keeps working exactly
// as it does under bash git.
func (r *Repo) Add(ctx context.Context, pathspecs ...string) error {
	args := append([]string{"add", "--"}, pathspecs...)
	res, err := r.run(ctx, nil, args...)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return gitError(args, res)
	}
	return nil
}

// LsTreeEntry is one entry of `git ls-tree` output.
type LsTreeEntry struct {
	Mode string
	Type string
	SHA  string
	Path string
}

// LsTree reads a single path's entry from rev's tree. found is false when
// the path is simply absent from that tree — the normal case when, for
// example, resolving the mode of a file deleted from the worktree.
func (r *Repo) LsTree(ctx context.Context, rev, path string) (entry LsTreeEntry, found bool, err error) {
	args := []string{"ls-tree", rev, "--", path}
	res, err := r.run(ctx, nil, args...)
	if err != nil {
		return LsTreeEntry{}, false, err
	}
	if res.ExitCode != 0 {
		return LsTreeEntry{}, false, gitError(args, res)
	}
	line := strings.TrimRight(string(res.Stdout), "\n")
	if line == "" {
		return LsTreeEntry{}, false, nil
	}
	entry, perr := parseLsTreeLine(line)
	if perr != nil {
		return LsTreeEntry{}, false, perr
	}
	return entry, true, nil
}

func parseLsTreeLine(line string) (LsTreeEntry, error) {
	tab := strings.IndexByte(line, '\t')
	if tab < 0 {
		return LsTreeEntry{}, fmt.Errorf("gitx: malformed ls-tree line %q", line)
	}
	fields := strings.Fields(line[:tab])
	if len(fields) != 3 {
		return LsTreeEntry{}, fmt.Errorf("gitx: malformed ls-tree line %q", line)
	}
	return LsTreeEntry{Mode: fields[0], Type: fields[1], SHA: fields[2], Path: line[tab+1:]}, nil
}

// RevParseVerify answers whether rev names a valid object, per
// docs/USAGE.md § Argument shape rule 3 (rgit diff's revision/rev:path
// detection). --quiet makes any non-zero exit the plain "not a revision"
// answer rather than a fatal error, so ok=false, err=nil is the expected
// outcome for an ordinary pathspec or symbol anchor argument.
func (r *Repo) RevParseVerify(ctx context.Context, rev string) (sha string, ok bool, err error) {
	res, err := r.run(ctx, nil, "rev-parse", "--verify", "--quiet", rev)
	if err != nil {
		return "", false, err
	}
	if res.ExitCode != 0 {
		return "", false, nil
	}
	return strings.TrimSpace(string(res.Stdout)), true, nil
}

// DiffNumstat runs `git diff --numstat` with the given extra arguments
// (revision ranges, --staged, pathspecs, ...) and returns each line's raw
// fields. Added/Deleted stay strings because git prints "-" for a binary
// file's counts; Path is passed through verbatim, including git's own
// "old => new" rename shorthand, for the diff-rendering layer to interpret.
type NumstatEntry struct {
	Added   string
	Deleted string
	Path    string
}

func (r *Repo) DiffNumstat(ctx context.Context, extra ...string) ([]NumstatEntry, error) {
	args := append([]string{"diff", "--numstat"}, extra...)
	res, err := r.run(ctx, nil, args...)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, gitError(args, res)
	}
	return parseNumstat(res.Stdout), nil
}

func parseNumstat(out []byte) []NumstatEntry {
	trimmed := strings.TrimRight(string(out), "\n")
	if trimmed == "" {
		return nil
	}
	lines := strings.Split(trimmed, "\n")
	entries := make([]NumstatEntry, 0, len(lines))
	for _, line := range lines {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		entries = append(entries, NumstatEntry{Added: parts[0], Deleted: parts[1], Path: parts[2]})
	}
	return entries
}

// CheckIgnore answers whether path is gitignored, via `git check-ignore
// --quiet`. Exit 1 (not ignored) is a plain no, err nil. Any exit other
// than 0 or 1 — check-ignore documents 128 for a fatal error such as an
// invalid path — is a real failure, reported as a *GitError rather than
// folded into ignored=false.
func (r *Repo) CheckIgnore(ctx context.Context, path string) (ignored bool, err error) {
	args := []string{"check-ignore", "--quiet", "--", path}
	res, err := r.run(ctx, nil, args...)
	if err != nil {
		return false, err
	}
	switch res.ExitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, gitError(args, res)
	}
}

// CommitOptions mirrors the subset of `git commit` flags docs/USAGE.md
// exposes on `rgit commit`. Messages is repeatable -m, forwarded to git in
// order; git itself joins repeated -m values as blank-line-separated
// paragraphs, so no joining happens here.
type CommitOptions struct {
	Messages     []string
	MessageFile  string // -F value; "-" reads StdinMessage instead of a real file
	StdinMessage []byte
	Signoff      bool
	Trailers     []string // TOKEN:VALUE, forwarded as --trailer
	Amend        bool
	AllowEmpty   bool
	NoVerify     bool
}

// Commit runs `git commit` with opts translated to flags. A non-zero exit
// — including a hook rejection — is reported as a *GitError; per
// AGENTS.md, staging is never rolled back on that path, and Commit does
// not attempt to.
func (r *Repo) Commit(ctx context.Context, opts CommitOptions) error {
	args := []string{"commit"}
	for _, m := range opts.Messages {
		args = append(args, "-m", m)
	}
	if opts.MessageFile != "" {
		args = append(args, "-F", opts.MessageFile)
	}
	if opts.Signoff {
		args = append(args, "--signoff")
	}
	for _, t := range opts.Trailers {
		args = append(args, "--trailer", t)
	}
	if opts.Amend {
		args = append(args, "--amend")
	}
	if opts.AllowEmpty {
		args = append(args, "--allow-empty")
	}
	if opts.NoVerify {
		args = append(args, "--no-verify")
	}

	var stdin io.Reader
	if opts.MessageFile == "-" {
		stdin = bytes.NewReader(opts.StdinMessage)
	}

	res, err := r.run(ctx, stdin, args...)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return gitError(args, res)
	}
	return nil
}

// Push runs `git push` with the given arguments (remote, refspec, ...).
// AGENTS.md is explicit that a push failure does not roll back the commit
// that preceded it; Push reports the failure and nothing more.
func (r *Repo) Push(ctx context.Context, extra ...string) error {
	args := append([]string{"push"}, extra...)
	res, err := r.run(ctx, nil, args...)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return gitError(args, res)
	}
	return nil
}

// Status runs `git status --porcelain=v1` with extra arguments and
// returns the raw output for the caller to parse; rendering the
// "everything committable" view is the diff layer's job, not gitx's.
func (r *Repo) Status(ctx context.Context, extra ...string) ([]byte, error) {
	args := append([]string{"status", "--porcelain=v1"}, extra...)
	res, err := r.run(ctx, nil, args...)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, gitError(args, res)
	}
	return res.Stdout, nil
}

// LsFilesOthers lists untracked files via `git ls-files --others
// --exclude-standard -z`, NUL-terminated so no path-quoting rules apply.
// extra is appended after the flags, for pathspec scoping (`-- <pathspec>`).
func (r *Repo) LsFilesOthers(ctx context.Context, extra ...string) ([]string, error) {
	args := append([]string{"ls-files", "--others", "--exclude-standard", "-z"}, extra...)
	res, err := r.run(ctx, nil, args...)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, gitError(args, res)
	}
	trimmed := bytes.Trim(res.Stdout, "\x00")
	if len(trimmed) == 0 {
		return nil, nil
	}
	return strings.Split(string(trimmed), "\x00"), nil
}

// LsFilesStage reads path's index-stage-0 mode via `git ls-files --stage`.
// found is false when path is simply not in the index — the normal case
// for a file that is untracked or staged-deleted, not a failure.
func (r *Repo) LsFilesStage(ctx context.Context, path string) (mode string, found bool, err error) {
	args := []string{"ls-files", "--stage", "--", path}
	res, err := r.run(ctx, nil, args...)
	if err != nil {
		return "", false, err
	}
	if res.ExitCode != 0 {
		return "", false, gitError(args, res)
	}
	line := strings.TrimRight(string(res.Stdout), "\n")
	if line == "" {
		return "", false, nil
	}
	fields := strings.Fields(line)
	if len(fields) < 1 {
		return "", false, fmt.Errorf("gitx: malformed ls-files --stage line %q", line)
	}
	return fields[0], true, nil
}

// MergeBase resolves the merge base of a and b via `git merge-base`, needed
// for a diff-scope's `A...B` symmetric range: the range's "old" content
// endpoint is the merge base, not A itself. ok is false when the two
// revisions share no common ancestor — a normal negative answer, not a
// failure.
func (r *Repo) MergeBase(ctx context.Context, a, b string) (sha string, ok bool, err error) {
	args := []string{"merge-base", a, b}
	res, err := r.run(ctx, nil, args...)
	if err != nil {
		return "", false, err
	}
	if res.ExitCode != 0 {
		return "", false, nil
	}
	return strings.TrimSpace(string(res.Stdout)), true, nil
}

// Toplevel returns the working tree's root directory, via `git rev-parse
// --show-toplevel`.
func (r *Repo) Toplevel(ctx context.Context) (string, error) {
	args := []string{"rev-parse", "--show-toplevel"}
	res, err := r.run(ctx, nil, args...)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", gitError(args, res)
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

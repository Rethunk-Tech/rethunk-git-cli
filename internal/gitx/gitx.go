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
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/term"
)

// Repo wraps git invocations rooted at a working directory. dir need not be
// the top of the worktree; git resolves that itself via -C.
type Repo struct {
	root           string
	env            []string
	ignoreCaseOnce sync.Once
	ignoreCase     bool
	ignoreCaseErr  error
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

// Reroot returns a Repo pointed at a different directory, reusing r's
// already-computed environment (New's own GIT_TERMINAL_PROMPT detection)
// rather than probing it a second time for the same process -- for a
// caller that must query git relative to one directory (e.g. the
// invocation directory, to learn the repository root) before it can build
// the Repo it actually means to keep.
func (r *Repo) Reroot(dir string) *Repo {
	return &Repo{root: dir, env: r.env}
}

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

// checked runs git and returns stdout, treating any non-zero exit as a
// *GitError. It is the shape of every method that has no "normal negative
// answer" of its own to report.
//
// The methods that do have one -- CatFile, RevParseVerify, MergeBase,
// CheckIgnore, Add -- deliberately do not use it: each reads res.ExitCode
// itself, because folding their negative case into an error is precisely
// what this package's three-outcome contract forbids.
func (r *Repo) checked(ctx context.Context, args ...string) ([]byte, error) {
	return r.checkedStdin(ctx, nil, args...)
}

// checkedStdin is checked with content piped to git's stdin.
func (r *Repo) checkedStdin(ctx context.Context, stdin io.Reader, args ...string) ([]byte, error) {
	res, err := r.run(ctx, stdin, args...)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, gitError(args, res)
	}
	return res.Stdout, nil
}

// optionalLine runs a query whose non-zero exit IS a normal negative
// answer -- "no such revision", "no common ancestor" -- and returns its
// single-line stdout. ok=false with err=nil is the expected outcome there,
// which is why these cannot go through checked: turning that answer into a
// *GitError is exactly what the package's three-outcome contract forbids.
func (r *Repo) optionalLine(ctx context.Context, args ...string) (line string, ok bool, err error) {
	res, err := r.run(ctx, nil, args...)
	if err != nil {
		return "", false, err
	}
	if res.ExitCode != 0 {
		return "", false, nil
	}
	return strings.TrimSpace(string(res.Stdout)), true, nil
}

// checkedLine is checked with surrounding whitespace stripped, for the git
// queries that answer with exactly one value.
func (r *Repo) checkedLine(ctx context.Context, args ...string) (string, error) {
	out, err := r.checked(ctx, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// CatFile reads the blob at rev:path (e.g. "HEAD:auth.go"). Per
// specs/design.md's blob-synthesis algorithm, a path absent from rev is a
// normal case — an untracked file has no HEAD blob — so exists reports
// that distinction directly rather than making callers inspect stderr.
func (r *Repo) CatFile(ctx context.Context, rev, path string) (content []byte, exists bool, err error) {
	args := []string{"cat-file", "-p", rev + ":" + path}
	res, err := r.run(ctx, nil, args...)
	if err != nil {
		return nil, false, err
	}
	if res.ExitCode != 0 {
		exists, err := r.catFileFailure(ctx, rev, path, args, res)
		return nil, exists, err
	}
	return res.Stdout, true, nil
}

// catFileFailure preserves the normal absent-path answer while surfacing a
// known-but-unreadable promisor object. A tree lookup is the distinction
// cat-file -p itself does not expose: it reports both cases as exit 128.
func (r *Repo) catFileFailure(ctx context.Context, rev, path string, args []string, res Result) (exists bool, err error) {
	known, submodule, err := r.catFilePathStatus(ctx, rev, path)
	if err != nil {
		return false, err
	}
	if !known || submodule {
		return false, nil
	}
	return false, gitError(args, res)
}

func (r *Repo) catFilePathStatus(ctx context.Context, rev, path string) (known, submodule bool, err error) {
	if rev == "" {
		mode, found, err := r.LsFilesStage(ctx, path)
		if err != nil {
			return false, false, err
		}
		return found, found && mode == "160000", nil
	}
	res, err := r.run(ctx, nil, "ls-tree", rev, "--", path)
	if err != nil {
		return false, false, err
	}
	if res.ExitCode != 0 {
		// Preserve CatFile's existing normal-negative answer for a bad
		// revision. A valid revision with no matching output is handled
		// identically below.
		return false, false, nil
	}
	line := strings.TrimRight(string(res.Stdout), "\n")
	if line == "" {
		return false, false, nil
	}
	entry, err := parseLsTreeLine(line)
	if err != nil {
		return false, false, err
	}
	return true, entry.Type == "commit" || entry.Mode == "160000", nil
}

// CatFileSample reads at most limit bytes of the blob at rev:path via `git
// cat-file -p`, for a caller that only needs to sniff a leading sample of
// content -- classifying a path as binary never inspects more than
// util.BinarySampleLimit bytes, so materializing an entire, possibly huge,
// tracked blob into memory just to answer that question is wasted work.
// Git is still the one reading and decompressing the object; this only
// stops Go from copying more of its stdout than the caller asked for, so
// the delegation boundary (AGENTS.md) holds -- nothing here parses a git
// object itself.
//
// exists reports the same "absent from rev" distinction CatFile does: a
// non-zero exit (bad revision, path not in the tree) is folded into
// exists=false rather than a *GitError, since both mean "there is nothing
// at that rev" to this call's caller.
func (r *Repo) CatFileSample(ctx context.Context, rev, path string, limit int) (sample []byte, exists bool, err error) {
	args := []string{"cat-file", "-p", rev + ":" + path}
	full := append([]string{"-C", r.root}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = r.env
	// Every other helper in this package buffers stderr through run()'s own
	// bytes.Buffer; this one builds its *exec.Cmd by hand (it needs
	// StdoutPipe, which run() does not expose) and left Stderr unset. A nil
	// Stderr is not actually inherited -- os/exec's own Start (childStderr /
	// writerDescriptor) connects it to os.DevNull whether or not Run itself
	// is used, so this was never leaking git's cat-file noise to rgit's own
	// stderr. It is still worth buffering explicitly: every other exit path
	// in this package is consistent about it, an explicit buffer means the
	// content is available rather than silently discarded should a future
	// error path here ever want to report it, and it avoids a DevNull open
	// on every call for no benefit.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdout, perr := cmd.StdoutPipe()
	if perr != nil {
		return nil, false, &ExecError{Args: args, Err: perr}
	}
	if serr := cmd.Start(); serr != nil {
		return nil, false, &ExecError{Args: args, Err: serr}
	}

	buf := make([]byte, limit)
	n, rerr := io.ReadFull(stdout, buf)
	switch {
	case rerr == nil:
		// The blob holds at least limit bytes: enough is already known, so
		// the process is killed rather than drained -- reading the rest of
		// a multi-gigabyte blob just to let git exit on its own would
		// defeat the point of sampling it.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return buf[:n], true, nil
	case errors.Is(rerr, io.EOF), errors.Is(rerr, io.ErrUnexpectedEOF):
		// The whole blob fit inside limit (or there was nothing at all);
		// either way stdout is drained, so Wait needs no draining of its
		// own to observe the real exit status.
	default:
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, false, &ExecError{Args: args, Err: rerr}
	}

	if werr := cmd.Wait(); werr != nil {
		var exitErr *exec.ExitError
		if errors.As(werr, &exitErr) {
			res := Result{ExitCode: exitErr.ExitCode(), Stderr: stderr.Bytes()}
			exists, err := r.catFileFailure(ctx, rev, path, args, res)
			return nil, exists, err
		}
		return nil, false, &ExecError{Args: args, Err: werr}
	}
	return buf[:n], true, nil
}

// BatchCatFileRequest names one blob to read in a BatchCatFile call: rev
// and path combine the same way CatFile's own rev+":"+path does.
type BatchCatFileRequest struct {
	Rev  string
	Path string
}

// BatchCatFileResult is one BatchCatFile response, in the same order as its
// requests. Exists mirrors CatFile's own convention -- false for a path
// absent from rev, never an error.
type BatchCatFileResult struct {
	Content []byte
	Exists  bool
}

// BatchCatFile reads every rev:path in requests through one `git cat-file
// --batch` process instead of one `git cat-file -p` subprocess per blob --
// the same content and the same exists convention CatFile itself returns,
// batched for a caller reading many blobs in one pass (internal/diff's own
// per-file scope reads). Results come back in request order.
//
// Writing and reading run concurrently on purpose: `--batch` answers each
// request as it is read, so writing every request first and only then
// reading any response risks a full pipe on either side deadlocking the
// other once there are enough requests queued.
func (r *Repo) BatchCatFile(ctx context.Context, requests []BatchCatFileRequest) ([]BatchCatFileResult, error) {
	if len(requests) == 0 {
		return nil, nil
	}

	args := []string{"cat-file", "--batch"}
	full := append([]string{"-C", r.root}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = r.env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, &ExecError{Args: args, Err: err}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, &ExecError{Args: args, Err: err}
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, &ExecError{Args: args, Err: err}
	}

	writeErrCh := make(chan error, 1)
	go func() {
		w := bufio.NewWriter(stdin)
		for _, req := range requests {
			if _, err := fmt.Fprintf(w, "%s:%s\n", req.Rev, req.Path); err != nil {
				writeErrCh <- err
				_ = stdin.Close()
				return
			}
		}
		err := w.Flush()
		_ = stdin.Close()
		writeErrCh <- err
	}()

	results, readErr := readCatFileBatchResponses(stdout, len(requests))

	// The writer goroutine's own error only matters when reading did not
	// already fail: a write failure past the point every response was
	// already read (an early git exit, most commonly) is moot, and
	// reporting it instead of the real read error would hide the cause.
	writeErr := <-writeErrCh
	if readErr != nil {
		_ = cmd.Wait()
		return nil, readErr
	}
	if writeErr != nil {
		_ = cmd.Wait()
		return nil, &ExecError{Args: args, Err: writeErr}
	}
	if err := cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, &GitError{Args: args, ExitCode: exitErr.ExitCode(), Stderr: stderr.Bytes()}
		}
		return nil, &ExecError{Args: args, Err: fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))}
	}
	for i, req := range requests {
		if results[i].Exists {
			continue
		}
		known, submodule, err := r.catFilePathStatus(ctx, req.Rev, req.Path)
		if err != nil {
			return nil, err
		}
		if !known || submodule {
			continue
		}
		// --batch reports a promisor miss without fetching. Retry through
		// the singular form so an available promisor remote can satisfy it;
		// an unreachable remote then returns git's real exit-128 failure.
		content, exists, err := r.CatFile(ctx, req.Rev, req.Path)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, &GitError{
				Args:     []string{"cat-file", "-p", req.Rev + ":" + req.Path},
				ExitCode: 128,
				Stderr:   []byte("promisor object is unavailable"),
			}
		}
		results[i] = BatchCatFileResult{Content: content, Exists: true}
	}
	return results, nil
}

// readCatFileBatchResponses parses exactly count responses off r, in the
// three shapes `git cat-file --batch` ever writes: "<sha> <type> <size>\n"
// followed by exactly size content bytes and a trailing newline; "<object>
// missing\n" with no content at all; or "<sha> submodule\n", also with no
// content -- git's answer for a gitlink entry (mode 160000), which has no
// blob to read. The object name echoed back in the missing case is the
// literal request string, which unlike a sha or type may itself contain
// spaces (a pathspec with a space in it) -- tested by suffix, not by a
// fixed field count, for exactly that reason.
//
// The submodule shape is treated as Exists=false, not a fourth outcome:
// CatFile (singular, "-p") already reports a submodule path that way,
// because `cat-file -p rev:path` on a gitlink exits non-zero and that hits
// CatFile's own exists=false convention. Answering differently here would
// let prefetchBlobs' cache and contentSide.read's live CatFile fallback
// disagree about the same (rev, path) -- see blobCache's own doc comment
// in internal/diff/scope.go.
func readCatFileBatchResponses(r io.Reader, count int) ([]BatchCatFileResult, error) {
	br := bufio.NewReader(r)
	results := make([]BatchCatFileResult, count)
	for i := range count {
		header, err := br.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("gitx: cat-file --batch: reading response %d of %d: %w", i+1, count, err)
		}
		header = strings.TrimSuffix(header, "\n")
		if strings.HasSuffix(header, " missing") {
			results[i] = BatchCatFileResult{Exists: false}
			continue
		}
		fields := strings.Fields(header)
		if len(fields) == 2 && fields[1] == "submodule" {
			results[i] = BatchCatFileResult{Exists: false}
			continue
		}
		if len(fields) != 3 {
			return nil, fmt.Errorf("gitx: cat-file --batch: malformed response header %q", header)
		}
		size, serr := strconv.Atoi(fields[2])
		if serr != nil || size < 0 {
			return nil, fmt.Errorf("gitx: cat-file --batch: malformed size in header %q", header)
		}
		content := make([]byte, size)
		if _, err := io.ReadFull(br, content); err != nil {
			return nil, fmt.Errorf("gitx: cat-file --batch: reading %d content bytes for response %d of %d: %w", size, i+1, count, err)
		}
		if _, err := br.Discard(1); err != nil { // the content's own trailing newline
			return nil, fmt.Errorf("gitx: cat-file --batch: reading trailing newline for response %d of %d: %w", i+1, count, err)
		}
		results[i] = BatchCatFileResult{Content: content, Exists: true}
	}
	return results, nil
}

// HashObject writes content as a blob via `git hash-object -w --path
// path --stdin`. --path is mandatory here, not optional: without it,
// .gitattributes clean filters and LFS normalization are bypassed.
func (r *Repo) HashObject(ctx context.Context, path string, content []byte) (sha string, err error) {
	out, err := r.checkedStdin(ctx, bytes.NewReader(content), "hash-object", "-w", "--path", path, "--stdin")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// EmptyTree returns the SHA of the empty tree, computed rather than
// hardcoded so it is correct under both sha1 and sha256 object formats.
// It is the comparison base for an unborn branch: on a repository with no
// commits there is no HEAD to diff against, and every tracked path is an
// addition relative to nothing.
//
// Reads from an explicit empty stdin rather than a null-device path: this
// binary ships a windows/amd64 build, and a literal "/dev/null" argument is
// a platform assumption this package has no need to make when git already
// accepts an empty --stdin portably.
func (r *Repo) EmptyTree(ctx context.Context) (sha string, err error) {
	out, err := r.checkedStdin(ctx, bytes.NewReader(nil), "hash-object", "-t", "tree", "--stdin")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// UpdateIndexCacheinfo stages a single entry directly, as the final step
// of blob synthesis: `git update-index --add --cacheinfo mode,sha,path`.
func (r *Repo) UpdateIndexCacheinfo(ctx context.Context, mode, sha, path string) error {
	_, err := r.checked(ctx, "update-index", "--add", "--cacheinfo", mode+","+sha+","+path)
	return err
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
		// A path whose removal is already staged matches nothing in either
		// the worktree or the index, so `git add` calls it a bad pathspec.
		// Naming something that is already staged exactly as asked is not an
		// error — the commit will include it either way — and failing here
		// would make `rgit commit <path>` unusable after a `git rm`.
		if staged, serr := r.hasStagedChange(ctx, pathspecs); serr == nil && staged {
			return nil
		}
		return gitError(args, res)
	}
	return nil
}

// hasStagedChange reports whether any of pathspecs already differs between
// HEAD and the index.
func (r *Repo) hasStagedChange(ctx context.Context, pathspecs []string) (bool, error) {
	out, err := r.checked(ctx, append([]string{"diff", "--cached", "--name-only", "--"}, pathspecs...)...)
	if err != nil {
		return false, err
	}
	return len(bytes.TrimSpace(out)) > 0, nil
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
	out, err := r.checked(ctx, "ls-tree", rev, "--", path)
	if err != nil {
		return LsTreeEntry{}, false, err
	}
	line := strings.TrimRight(string(out), "\n")
	if line == "" {
		return LsTreeEntry{}, false, nil
	}
	entry, perr := parseLsTreeLine(line)
	if perr != nil {
		return LsTreeEntry{}, false, perr
	}
	return entry, true, nil
}

// LsTreeTolerant is LsTree with one git failure folded into the normal
// negative answer: a rev that does not resolve at all, which in practice
// means an unborn branch -- HEAD exists as a ref but names no commit yet.
// A tree that does not exist trivially contains no path, so found=false is
// the honest answer rather than an error every caller has to decode.
// An *ExecError (git could not be run at all) stays a real failure.
//
// Callers asking "is this path in HEAD" want this; callers that genuinely
// need to distinguish "no such tree" from "not in the tree" want LsTree.
func (r *Repo) LsTreeTolerant(ctx context.Context, rev, path string) (LsTreeEntry, bool, error) {
	entry, found, err := r.LsTree(ctx, rev, path)
	if err != nil {
		var execErr *ExecError
		if errors.As(err, &execErr) {
			return LsTreeEntry{}, false, err
		}
		return LsTreeEntry{}, false, nil
	}
	return entry, found, nil
}

func parseLsTreeLine(line string) (LsTreeEntry, error) {
	before, after, ok := strings.Cut(line, "\t")
	if !ok {
		return LsTreeEntry{}, fmt.Errorf("gitx: malformed ls-tree line %q", line)
	}
	fields := strings.Fields(before)
	if len(fields) != 3 {
		return LsTreeEntry{}, fmt.Errorf("gitx: malformed ls-tree line %q", line)
	}
	return LsTreeEntry{Mode: fields[0], Type: fields[1], SHA: fields[2], Path: after}, nil
}

// RevParseVerify answers whether rev names a valid object, per
// docs/USAGE.md § Argument shape rule 3 (rgit diff's revision/rev:path
// detection). --quiet makes any non-zero exit the plain "not a revision"
// answer rather than a fatal error, so ok=false, err=nil is the expected
// outcome for an ordinary pathspec or symbol anchor argument.
func (r *Repo) RevParseVerify(ctx context.Context, rev string) (sha string, ok bool, err error) {
	return r.optionalLine(ctx, "rev-parse", "--verify", "--quiet", rev)
}

// IsUnmerged reports whether path has any unmerged index entries, via
// `git ls-files -u`. An empty result is the normal clean-index answer.
func (r *Repo) IsUnmerged(ctx context.Context, path string) (bool, error) {
	out, err := r.checked(ctx, "ls-files", "-u", "--", path)
	if err != nil {
		return false, err
	}
	return len(bytes.TrimSpace(out)) > 0, nil
}

// SequencerOp reports the active git operation, if any. The pseudo-refs are
// checked in precedence order because git can leave more than one around
// while an operation is being continued.
func (r *Repo) SequencerOp(ctx context.Context) (op string, ok bool, err error) {
	refs := []struct {
		ref string
		op  string
	}{
		{ref: "MERGE_HEAD", op: "merge"},
		{ref: "CHERRY_PICK_HEAD", op: "cherry-pick"},
		{ref: "REVERT_HEAD", op: "revert"},
		{ref: "REBASE_HEAD", op: "rebase"},
		{ref: "BISECT_HEAD", op: "bisect"},
	}
	for _, candidate := range refs {
		if _, found, err := r.RevParseVerify(ctx, candidate.ref); err != nil {
			return "", false, err
		} else if found {
			return candidate.op, true, nil
		}
	}
	return "", false, nil
}

// IgnoreCase reports git's core.ignorecase setting. The result is cached for
// the lifetime of this Repo because the setting is repository configuration,
// not invocation state.
func (r *Repo) IgnoreCase(ctx context.Context) (bool, error) {
	r.ignoreCaseOnce.Do(func() {
		args := []string{"config", "--bool", "--get", "core.ignorecase"}
		res, err := r.run(ctx, nil, args...)
		if err != nil {
			r.ignoreCaseErr = err
			return
		}
		switch res.ExitCode {
		case 0:
			r.ignoreCase = strings.EqualFold(strings.TrimSpace(string(res.Stdout)), "true")
		case 1:
			r.ignoreCase = false
		default:
			r.ignoreCaseErr = gitError(args, res)
		}
	})
	return r.ignoreCase, r.ignoreCaseErr
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
	out, err := r.checked(ctx, append([]string{"diff", "--numstat"}, extra...)...)
	if err != nil {
		return nil, err
	}
	return parseNumstat(out)
}

// DiffPatch runs `git diff` (no `--numstat`) with the given extra arguments
// and returns git's own patch body unmodified -- the same "pass through
// git's own format" convention as LogLineRange and Blame, and the raw
// counterpart to DiffNumstat's parsed one. A caller wanting both the
// symbol-attributed report and the real patch text passes the identical
// extra arguments to each, guaranteeing the same scope (default/staged/
// unstaged/range) and the same pathspec filter underlies both.
func (r *Repo) DiffPatch(ctx context.Context, extra ...string) ([]byte, error) {
	return r.checked(ctx, append([]string{"diff"}, extra...)...)
}

// parseNumstat fails loudly on a line that does not split into exactly
// three tab-separated fields -- the same posture internal/diff takes on a
// numstat count that fails to parse as an integer -- rather than dropping
// it: a caller computing a total from a silently shortened list would
// underreport it with nothing to say why. SplitN's 3-way split already
// tolerates the one legitimate irregularity in a path field, git's own
// "{old => new}" rename shorthand, since that shorthand contains no tab of
// its own.
func parseNumstat(out []byte) ([]NumstatEntry, error) {
	trimmed := strings.TrimRight(string(out), "\n")
	if trimmed == "" {
		return nil, nil
	}
	entries := make([]NumstatEntry, 0, strings.Count(trimmed, "\n")+1)
	for line := range strings.SplitSeq(trimmed, "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("gitx: malformed numstat line %q", line)
		}
		entries = append(entries, NumstatEntry{Added: parts[0], Deleted: parts[1], Path: parts[2]})
	}
	return entries, nil
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
	ReuseMessage string // --reuse-message=<commit>
	Signoff      bool
	Trailers     []string // TOKEN:VALUE, forwarded as --trailer
	Amend        bool
	AllowEmpty   bool
	NoVerify     bool
	// NoEdit forwards --no-edit: reuse HEAD's message unchanged. rgit sets
	// this itself when --amend is given with neither -m nor -F, since rgit
	// never opens an editor and reusing HEAD's message is the only
	// sensible reading of "amend, but don't tell me what to say".
	NoEdit bool
	// Fixup and Squash are --fixup and --squash's raw values (e.g.
	// "HEAD~2", "amend:HEAD~2"), forwarded verbatim as
	// --fixup=<Fixup>/--squash=<Squash>. Both generate their own commit
	// message the same way --no-edit does, so the caller may leave
	// Messages and MessageFile empty; git appends any -m given on top of
	// the generated subject rather than rejecting the combination.
	Fixup  string
	Squash string
	// Author and Date forward --author and --date verbatim; plain git
	// passthrough, no rgit-owned semantics.
	Author string
	Date   string
	// ResetAuthor forwards --reset-author: take the author identity from
	// the committer rather than carrying the original forward. git rejects
	// it outside --amend and --fixup=amend: itself, so rgit forwards it
	// rather than policing the combination.
	ResetAuthor bool
	// GPGSign is --gpg-sign, bare or with a key id. GPGSignKeyID holds the
	// key id when one was given; empty means bare --gpg-sign (git signs
	// with the configured default key). NoGPGSign is --no-gpg-sign,
	// overriding commit.gpgsign=true.
	//
	// git's own -S accepts an OPTIONAL attached key id (-Skeyid), but
	// pflag's shorthand parser resolves an optional-value flag's default
	// before checking for an attached value, so -Skeyid misparses as a
	// chain of nonexistent single-letter flags. Only the long form is
	// exposed on rgit's command line; see docs/USAGE.md.
	GPGSign      bool
	GPGSignKeyID string
	NoGPGSign    bool
}

// Commit runs `git commit` with opts translated to flags and returns its
// output. The caller is expected to relay that to the user: git prints the
// branch, the new SHA, and the changed/insertion/deletion counts, and
// swallowing it forces the caller to run `git show` afterwards to learn
// what just happened. Hook output arrives on Stderr and matters for the
// same reason.
//
// A non-zero exit — including a hook rejection — is reported as a
// *GitError; per AGENTS.md, staging is never rolled back on that path, and
// Commit does not attempt to.
func (r *Repo) Commit(ctx context.Context, opts CommitOptions) (Result, error) {
	args := []string{"commit"}
	for _, m := range opts.Messages {
		args = append(args, "-m", m)
	}
	if opts.MessageFile != "" {
		args = append(args, "-F", opts.MessageFile)
	}
	if opts.ReuseMessage != "" {
		args = append(args, "--reuse-message="+opts.ReuseMessage)
	}
	if opts.Fixup != "" {
		args = append(args, "--fixup="+opts.Fixup)
	}
	if opts.Squash != "" {
		args = append(args, "--squash="+opts.Squash)
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
	if opts.NoEdit {
		args = append(args, "--no-edit")
	}
	if opts.Author != "" {
		args = append(args, "--author="+opts.Author)
	}
	if opts.Date != "" {
		args = append(args, "--date="+opts.Date)
	}
	if opts.ResetAuthor {
		args = append(args, "--reset-author")
	}
	if opts.GPGSign {
		if opts.GPGSignKeyID != "" {
			args = append(args, "--gpg-sign="+opts.GPGSignKeyID)
		} else {
			args = append(args, "--gpg-sign")
		}
	}
	if opts.NoGPGSign {
		args = append(args, "--no-gpg-sign")
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
		return Result{}, err
	}
	if res.ExitCode != 0 {
		return res, gitError(args, res)
	}
	return res, nil
}

// Push runs a bare `git push` -- rgit commit's only caller never has a
// remote or refspec of its own to add (docs/USAGE.md's --push is a plain
// "push what was just committed"), so there is no forwarding argument to
// carry; add one back only alongside a caller that actually has something
// to pass. AGENTS.md is explicit that a push failure does not roll back
// the commit that preceded it; Push reports the failure and nothing more.
//
// Push never adds `--set-upstream` on its own initiative, even for a
// branch with none configured: `push.default=current` (among other
// configurations) already pushes such a branch successfully with no
// upstream at all, so guessing `-u` here would fail a push for some
// callers that plain `git push` would have completed -- exactly the
// silent divergence AGENTS.md's one invariant forbids. A caller wanting a
// clearer message on the specific "no upstream" failure uses HasUpstream
// and CurrentBranch to add one after Push has already failed, never
// before.
func (r *Repo) Push(ctx context.Context) error {
	_, err := r.checked(ctx, "push")
	return err
}

// CurrentBranch returns HEAD's branch name via `git rev-parse --abbrev-ref
// HEAD`. Meaningful only once at least one commit exists (HEAD is
// otherwise unborn and this fails); rgit only calls it right after a
// commit has just succeeded, so that is always the case in practice. On a
// detached HEAD it returns the literal "HEAD", matching git's own output.
func (r *Repo) CurrentBranch(ctx context.Context) (string, error) {
	return r.checkedLine(ctx, "rev-parse", "--abbrev-ref", "HEAD")
}

// HasUpstream reports whether the current branch has an upstream tracking
// ref configured, via whether `@{u}` resolves. Like MergeBase and
// CheckIgnore, "no upstream configured" is a normal negative answer -- the
// default state of a newly created branch -- not a failure, so this goes
// through optionalLine rather than turning it into a *GitError.
func (r *Repo) HasUpstream(ctx context.Context) (bool, error) {
	_, ok, err := r.optionalLine(ctx, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	return ok, err
}

// Upstream reports the current branch's upstream tracking ref's own name
// (e.g. "origin/main"), or ok=false when none is configured -- the same
// "no upstream" is a normal negative answer, not a failure, HasUpstream
// already established. Kept separate from HasUpstream rather than having
// it report the name too: HasUpstream's one caller (commit.go's push hint)
// only ever needed the bool, and changing its signature to thread a name
// through that call site for no reason risks a behavior no test would
// catch.
func (r *Repo) Upstream(ctx context.Context) (name string, ok bool, err error) {
	return r.optionalLine(ctx, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
}

// AheadBehind reports how many commits the current branch is ahead of and
// behind its upstream, via `git rev-list --left-right --count HEAD...@{u}`.
// Callers check HasUpstream first -- with no upstream configured, `@{u}`
// fails to resolve and this returns a *GitError like any other bad
// revision, the same as every other rev-parse-backed query in this file.
func (r *Repo) AheadBehind(ctx context.Context) (ahead, behind int, err error) {
	line, err := r.checkedLine(ctx, "rev-list", "--left-right", "--count", "HEAD...@{u}")
	if err != nil {
		return 0, 0, err
	}
	fields := strings.Fields(line)
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("gitx: malformed rev-list --left-right --count output %q", line)
	}
	ahead, aerr := strconv.Atoi(fields[0])
	behind, berr := strconv.Atoi(fields[1])
	if aerr != nil || berr != nil {
		return 0, 0, fmt.Errorf("gitx: malformed rev-list --left-right --count output %q", line)
	}
	return ahead, behind, nil
}

// LsFilesOthers lists untracked files via `git ls-files --others
// --exclude-standard -z`, NUL-terminated so no path-quoting rules apply.
// extra is appended after the flags, for pathspec scoping (`-- <pathspec>`).
func (r *Repo) LsFilesOthers(ctx context.Context, extra ...string) ([]string, error) {
	out, err := r.checked(ctx, append([]string{"ls-files", "--others", "--exclude-standard", "-z"}, extra...)...)
	if err != nil {
		return nil, err
	}
	trimmed := bytes.Trim(out, "\x00")
	if len(trimmed) == 0 {
		return nil, nil
	}
	return strings.Split(string(trimmed), "\x00"), nil
}

// LsFilesTracked lists every tracked file via `git ls-files -z`,
// NUL-terminated so no path-quoting rules apply. extra is appended after
// the flags, for pathspec scoping (`-- <pathspec>`).
func (r *Repo) LsFilesTracked(ctx context.Context, extra ...string) ([]string, error) {
	out, err := r.checked(ctx, append([]string{"ls-files", "-z"}, extra...)...)
	if err != nil {
		return nil, err
	}
	trimmed := bytes.Trim(out, "\x00")
	if len(trimmed) == 0 {
		return nil, nil
	}
	return strings.Split(string(trimmed), "\x00"), nil
}

// LsFilesStage reads path's index-stage-0 mode via `git ls-files --stage`.
// found is false when path is simply not in the index — the normal case
// for a file that is untracked or staged-deleted, not a failure.
func (r *Repo) LsFilesStage(ctx context.Context, path string) (mode string, found bool, err error) {
	out, err := r.checked(ctx, "ls-files", "--stage", "--", path)
	if err != nil {
		return "", false, err
	}
	trimmed := strings.TrimRight(string(out), "\n")
	if trimmed == "" {
		return "", false, nil
	}
	for line := range strings.SplitSeq(trimmed, "\n") {
		before, _, ok := strings.Cut(line, "\t")
		if !ok {
			return "", false, fmt.Errorf("gitx: malformed ls-files --stage line %q", line)
		}
		fields := strings.Fields(before)
		if len(fields) != 3 {
			return "", false, fmt.Errorf("gitx: malformed ls-files --stage line %q", line)
		}
		if fields[2] == "0" {
			return fields[0], true, nil
		}
	}
	return "", false, nil
}

// Blame runs `git blame` on path, bounded to the 1-based, inclusive line
// range [start, end] via `-L`, plus any extra flags (e.g. "--porcelain")
// passed straight through. There is no "normal negative answer" of its own
// -- an invalid range or a path git cannot blame is a genuine failure -- so
// any non-zero exit is a *GitError like every other checked method.
func (r *Repo) Blame(ctx context.Context, path string, start, end int, extra ...string) ([]byte, error) {
	return r.blame(ctx, "", path, start, end, extra...)
}

// BlameRevision runs `git blame` against rev rather than the worktree, with
// the same bounded line range and pass-through flags as Blame.
func (r *Repo) BlameRevision(ctx context.Context, rev, path string, start, end int, extra ...string) ([]byte, error) {
	return r.blame(ctx, rev, path, start, end, extra...)
}

func (r *Repo) blame(ctx context.Context, rev, path string, start, end int, extra ...string) ([]byte, error) {
	args := append([]string{"blame", fmt.Sprintf("-L%d,%d", start, end)}, extra...)
	if rev != "" {
		args = append(args, rev)
	}
	args = append(args, "--", path)
	return r.checked(ctx, args...)
}

// LogLineRange runs `git log -L start,end:path`, bounded to the 1-based,
// inclusive line range [start, end] in path the same way Blame's `-L` is,
// plus any extra flags/args (e.g. "--no-patch", "--format=...") passed
// straight through. Git's own -L implementation re-derives the touched
// range at each ancestor commit by itself -- there is no per-commit extent
// for rgit to re-resolve on its own side. Like Blame, there is no "normal
// negative answer" of its own -- an invalid range or a path git cannot walk
// is a genuine failure -- so any non-zero exit is a *GitError.
//
// Unlike Blame, path cannot be moved after a "--" separator: measured
// directly (a temp repo, a path containing ':', `git log -L1,2:path --
// path`), `git log` refuses that shape outright --
// "fatal: -L<range>:<file> cannot be used with pathspec" -- so the range
// and the path are unavoidably one argument, joined by ':' with no escape
// for a literal ':' inside path. The git version this was measured against
// (2.55.0) happens to split on the first ':' after the numeric range and
// treats everything past it as the literal path verbatim, even one holding
// further colons -- but that splitting rule is documented nowhere, is free
// to differ across git versions or reimplementations, and a caller has no
// way to escape a path that sits on the wrong side of a future change to
// it. Rather than trust that undocumented behaviour indefinitely, a path
// containing ':' is refused up front, before it is ever embedded.
func (r *Repo) LogLineRange(ctx context.Context, path string, start, end int, extra ...string) ([]byte, error) {
	if strings.Contains(path, ":") {
		return nil, &LineRangePathError{Path: path}
	}
	args := append([]string{"log", fmt.Sprintf("-L%d,%d:%s", start, end, path)}, extra...)
	return r.checked(ctx, args...)
}

// LineRangePathError means LogLineRange refused a path containing ':' --
// see LogLineRange's own doc comment for why embedding it is not safe to
// rely on.
type LineRangePathError struct {
	Path string
}

func (e *LineRangePathError) Error() string {
	return fmt.Sprintf("gitx: %q: cannot be used with LogLineRange -- git log's own -L<range>:<path> argument joins the two with ':' and has no way to escape one inside path", e.Path)
}

// FindRename walks path's history backward from rev (via --follow, so
// renames are tracked) for the nearest commit whose own diff renamed it,
// used by `rgit log --follow-rename` to find each segment boundary without
// walking commit-by-commit itself -- git's own --follow already re-derives
// the rename chain, so there is nothing left for rgit to detect on its own
// side, the same delegation --follow-rename's own design record entry
// argues for.
//
// found is false when path was never renamed reaching back from rev (the
// common case, and the terminal one for any --follow-rename walk): commit
// and oldPath are then meaningless. When found, commit is the rename
// commit's own hash and oldPath is the name path carried immediately
// before it -- the caller resolves the anchor's extent against
// commit+"~1":oldPath to continue the walk one segment further back.
func (r *Repo) FindRename(ctx context.Context, rev, path string) (commit, oldPath string, found bool, err error) {
	out, err := r.checked(ctx, "log", "--follow", "--diff-filter=R", "--name-status", "--format=%H", "-1", rev, "--", path)
	if err != nil {
		return "", "", false, err
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) < 3 || lines[0] == "" {
		return "", "", false, nil
	}
	fields := strings.Split(lines[2], "\t")
	if len(fields) != 3 || !strings.HasPrefix(fields[0], "R") {
		return "", "", false, nil
	}
	return lines[0], fields[1], true, nil
}

// Log runs `git log`, optionally bounded by --since/--until and a path
// filter, plus any extra flags/args (e.g. "--no-patch", "--format=...",
// "-n", "5")
// passed straight through -- rgit log's own time- and path-scoped shape
// (docs/USAGE.md § Log), distinct from LogLineRange's single-symbol -L
// form. since and until are forwarded to git's own --since/--until
// unparsed; git accepts anything from an ISO date to "2 weeks ago", and
// reimplementing that parsing here would only ever be a worse copy of
// git's own. paths is forwarded after "--" so pathspec magic still
// applies, exactly as every other pathspec-accepting method in this
// package; an empty paths reports the whole repository's history within
// the same date bounds, matching plain `git log --since=X`. Like Blame and
// LogLineRange, there is no "normal negative answer" of its own -- an
// unparseable date or an unwalkable path is a genuine failure -- so any
// non-zero exit is a *GitError.
func (r *Repo) Log(ctx context.Context, since, until string, paths []string, extra ...string) ([]byte, error) {
	args := []string{"log"}
	if since != "" {
		args = append(args, "--since="+since)
	}
	if until != "" {
		args = append(args, "--until="+until)
	}
	args = append(args, extra...)
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	return r.checked(ctx, args...)
}

// CommitSummary is one commit's hash and subject, as RecentCommits reports
// it -- deliberately nothing more: rgit context (internal/app/context.go)
// is the sole caller, and it has no use for anything `git log --format`
// could add beyond the two fields its own record shape carries.
type CommitSummary struct {
	Hash    string
	Subject string
}

// RecentCommits returns the last limit commits reachable from HEAD, newest
// first, hash and subject only, via `git log -n limit --no-patch
// --format=%H%x09%s`. Bounding by count is git's own -n flag, not a slice
// on rgit's side after the fact -- git never produces more than limit
// commits to begin with.
//
// An unborn branch (no commit yet) reports no commits at all rather than a
// *GitError: RevParseVerify's own HEAD check is tried first, the same
// "unborn branch is a normal state" convention internal/diff's
// committableBase already applies, rather than pattern-matching `git log`'s
// own fatal-error text for the same fact.
func (r *Repo) RecentCommits(ctx context.Context, limit int) ([]CommitSummary, error) {
	if _, ok, err := r.RevParseVerify(ctx, "HEAD"); err != nil {
		return nil, err
	} else if !ok {
		return nil, nil
	}
	out, err := r.checked(ctx, "log", "-n", strconv.Itoa(limit), "--no-patch", "--format=%H%x09%s")
	if err != nil {
		return nil, err
	}
	return parseCommitSummaries(out)
}

// parseCommitSummaries splits RecentCommits' own "%H%x09%s" format, one
// commit per line -- a plain tab-cut, the same shape parseNumstat already
// applies to a different git format string, and held to the same standard:
// strings.Cut's own found bool must be checked, not discarded, or a line
// with no tab at all (its whole text becomes "hash" with subject silently
// empty) is read as a bogus commit instead of the malformed line it is.
func parseCommitSummaries(out []byte) ([]CommitSummary, error) {
	trimmed := strings.TrimRight(string(out), "\n")
	if trimmed == "" {
		return nil, nil
	}
	lines := strings.Split(trimmed, "\n")
	summaries := make([]CommitSummary, 0, len(lines))
	for _, line := range lines {
		hash, subject, ok := strings.Cut(line, "\t")
		if !ok {
			return nil, fmt.Errorf("gitx: malformed log summary line %q", line)
		}
		summaries = append(summaries, CommitSummary{Hash: hash, Subject: subject})
	}
	return summaries, nil
}

// MergeBase resolves the merge base of a and b via `git merge-base`, needed
// for a diff-scope's `A...B` symmetric range: the range's "old" content
// endpoint is the merge base, not A itself. ok is false when the two
// revisions share no common ancestor — a normal negative answer, not a
// failure.
func (r *Repo) MergeBase(ctx context.Context, a, b string) (sha string, ok bool, err error) {
	return r.optionalLine(ctx, "merge-base", a, b)
}

// Toplevel returns the working tree's root directory, via `git rev-parse
// --show-toplevel`.
func (r *Repo) Toplevel(ctx context.Context) (string, error) {
	return r.checkedLine(ctx, "rev-parse", "--show-toplevel")
}

// ShowPrefix returns the current directory's path relative to the
// repository root, slash-terminated, or "" at the root itself. git
// resolves pathspecs relative to the current directory, so this is what
// turns a caller's `a.go` into the root-relative `pkg/deep/a.go` that
// rgit works in internally.
func (r *Repo) ShowPrefix(ctx context.Context) (string, error) {
	return r.checkedLine(ctx, "rev-parse", "--show-prefix")
}

// The `rgit context` command surface: one-call repository orientation for
// an agent's first turn -- branch and upstream, optional diagnostics,
// committable diff rows, then recent commit subjects, as a single
// fixed-shape record stream.
//
// Pure read composition over internal/gitx and internal/diff, per
// specs/design.md § Commands: no new resolution or attribution machinery.
// The diff half is literally internal/diff.Run's default scope, rendered
// through its own RenderPorcelain and re-tagged per line, rather than a
// second walk of report.Files that could drift from what
// `rgit diff --porcelain` emits.
package app

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	diffpkg "github.com/Rethunk-Tech/rethunk-git-cli/internal/diff"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
)

const contextHelp = `usage: rgit context

One-call repository orientation for an agent's first turn: the same
per-file, per-symbol diffstat "rgit diff" itself reports for everything
committable (staged, unstaged, and untracked), then recent commit
subjects -- as a single, fixed-shape record stream. Replaces the separate
status, diffstat, diff, and log calls an agent would otherwise make before
editing.

Takes no flags or targets beyond --help: the output shape is fixed and
byte-budgeted on purpose ("a command with options becomes git status with
extra steps" -- see docs/USAGE.md § Context), so there is nothing here to
select or narrow.

Records, one per line, tab-separated, no header:

  B<TAB>BRANCH<TAB>UPSTREAM<TAB>AHEAD<TAB>BEHIND
      At most one, first when present: the current branch, its upstream
      tracking ref (empty when none is configured), and how many commits
      ahead/behind it (both 0 when there is no upstream). Absent entirely on
      an unborn branch or detached HEAD.

  H<TAB>SHA
      One detached-HEAD record with the full commit object id. Absent on an
      unborn branch.

  S<TAB>OP
      One active sequencer operation: merge, cherry-pick, revert, rebase, or
      bisect.

  W<TAB>ts-only
      One when at least one file had symbols to cross-check but no live
      language server was reached.

  W<TAB>warning<TAB>TEXT
      One per non-fatal diff diagnostic. TEXT omits the human [warning]
      prefix that remains on stderr.

  F<TAB>FILE<TAB>SYMBOL<TAB>STATUS<TAB>ADDED<TAB>DELETED
      One per "rgit diff --porcelain" row -- identical fields, with this
      stream's own leading type tag. See docs/CODES.md#output-records for
      what STATUS carries.

  C<TAB>HASH<TAB>SUBJECT
      One per recent commit, newest first, bounded to the last 20.

  X<TAB>TRUNCATED<TAB>COUNT
      At most one, always last: this many records were withheld because
      the stream reached its byte budget.

The whole stream is capped at 16 KiB. B sorts first (a single record, cost
next to nothing), then W diagnostics, F rows, and C rows. F rows survive
truncation before C rows do: the diff section has no natural bound of its own,
while commits are already bounded up front (the most recent 20, via git's own
-n) and cost little to drop. The X record names how many rows were withheld.
See specs/design.md#commands for the reasoning.

Full reference: docs/USAGE.md
`

// contextRecentCommitLimit bounds gitx.RecentCommits' own -n flag: an
// input bound, not a truncation applied after the fact -- git never
// produces more than this many commits to begin with (specs/design.md §
// Commands).
const contextRecentCommitLimit = 20

// contextByteBudget is the hard ceiling on rgit context's entire stdout
// stream. Chosen so the common case -- a handful of recent commits plus an
// ordinary in-progress change -- never comes close to it, while a worst
// case (a bulk rename or a vendored dependency bump touching hundreds of
// files) is bounded to a fixed, predictable cost rather than scaling with
// repository size (specs/design.md § Commands).
const contextByteBudget = 16384

// runContext is hand-parsed and has no runtime flag surface beyond --help.
//
// --help wins wherever it appears in args, not only as the sole argument
// (m12: every hand-parsed command follows the same rule now, matching
// blame.go/log.go's own loop) -- but since context takes nothing else,
// that reduces to just checking args[0]: any other token has already
// failed by the time a later --help could be reached.
func runContext(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) exitcode.Code {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprint(stdout, contextHelp)
		return exitcode.Success
	}
	if len(args) != 0 {
		return refuseExtraArgs("context", args, stderr, contextHelp)
	}

	root, _, repo, code := openRepo(ctx, dir, stderr)
	if code != exitcode.Success {
		return code
	}

	commits, err := repo.RecentCommits(ctx, contextRecentCommitLimit)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.GitFailure
	}

	// Branch identity is best-effort: an unborn branch has no commit to
	// identify, while detached HEAD is represented by its full object id
	// rather than the literal branch name "HEAD".
	branchRecord, detachedRecord := "", ""
	if branch, berr := repo.CurrentBranch(ctx); berr == nil && branch != "" {
		if branch == "HEAD" {
			if sha, ok, rerr := repo.RevParseVerify(ctx, "HEAD"); rerr != nil {
				fmt.Fprintf(stderr, "rgit: %v\n", rerr)
				return exitcode.GitFailure
			} else if ok {
				detachedRecord = fmt.Sprintf("H\t%s\n", sha)
			}
		} else {
			upstream, hasUpstream, _ := repo.Upstream(ctx)
			ahead, behind := 0, 0
			if hasUpstream {
				ahead, behind, _ = repo.AheadBehind(ctx)
			}
			branchRecord = fmt.Sprintf("B\t%s\t%s\t%d\t%d\n", branch, upstream, ahead, behind)
		}
	}

	sequencerOp, sequencerActive, err := repo.SequencerOp(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.GitFailure
	}

	// The default "everything committable" scope -- staged + unstaged vs
	// HEAD, plus untracked -- is exactly what a bare `rgit diff` already
	// reports; no Options fields are set here, per specs/design.md §
	// Commands's own guardrail against a flag surface that would let this
	// grow into a second `git status`.
	report, err := diffpkg.Run(ctx, repo, root, diffpkg.Options{})
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.GitFailure
	}

	// Same diagnostics rgit diff itself prints for the identical report,
	// on stderr rather than folded into the record stream.
	if report.TSOnly {
		fmt.Fprintln(stderr, tsOnlyNotice)
	}
	for _, w := range report.Warnings {
		fmt.Fprintf(stderr, "[warning] %s\n", w)
	}

	// B or H, if present, sorts first: a single, tiny, always-useful record
	// that costs the budget almost nothing. S follows it, then W diagnostics
	// and F rows:
	// F rows are the actionable, unbounded half of the stream, while the 20
	// commit subjects are cheap and expendable. Budget truncation below drops
	// from the end of records, so this ordering is what makes a busy branch's
	// commit history yield to the diff instead of crowding it out
	// (docs/CODES.md#output-records).
	records := make([]string, 0, 2+1+len(report.Warnings)+len(commits)+len(report.Files))
	if branchRecord != "" {
		records = append(records, branchRecord)
	}
	if detachedRecord != "" {
		records = append(records, detachedRecord)
	}
	if sequencerActive {
		records = append(records, fmt.Sprintf("S\t%s\n", sequencerOp))
	}
	if report.TSOnly {
		records = append(records, "W\tts-only\n")
	}
	for _, w := range report.Warnings {
		records = append(records, fmt.Sprintf("W\twarning\t%s\n", w))
	}
	// diffpkg.RenderPorcelain is the exact rendering `rgit diff --porcelain`
	// already produces, reused verbatim and re-tagged per line -- not a
	// second walk of report.Files that could drift from it.
	porcelain := strings.TrimRight(diffpkg.RenderPorcelain(report), "\n")
	if porcelain != "" {
		for line := range strings.SplitSeq(porcelain, "\n") {
			records = append(records, "F\t"+line+"\n")
		}
	}
	for _, c := range commits {
		records = append(records, fmt.Sprintf("C\t%s\t%s\n", c.Hash, c.Subject))
	}

	fmt.Fprint(stdout, buildContextStream(records, contextByteBudget))
	return exitcode.Success
}

// buildContextStream assembles records (each already newline-terminated,
// in priority order) into the fixed-budget stream runContext emits,
// stopping the moment the next record would exceed budget and appending a
// single X record naming how many were withheld. Kept separate from
// runContext so the truncation boundary is unit-testable against a small
// budget, without building a repository large enough to exceed the real
// one.
//
// The X record itself counts against budget. Appending it unconditionally
// after a budget-bounded loop, without reserving room for it first, could
// push the total past budget -- exactly the kind of gap that breaks the
// "capped at 16 KiB" contract this stream's own help text and
// docs/USAGE.md state as a hard limit, not a soft target.
func buildContextStream(records []string, budget int) string {
	total := 0
	for _, rec := range records {
		total += len(rec)
	}
	if total <= budget {
		// Nothing is ever withheld, so no X record is appended and there is
		// nothing to reserve room for.
		return strings.Join(records, "")
	}

	// At least one record will be withheld below, so the loop must stop
	// short of budget by however much the trailing "X\tTRUNCATED\t<n>\n"
	// record itself will cost. That cost is computed, not guessed: 12
	// literal bytes plus however many digits len(records) itself needs (the
	// worst case, every record omitted) plus the trailing newline.
	reserve := len("X\tTRUNCATED\t") + len(strconv.Itoa(len(records))) + 1
	limit := budget - reserve

	var buf strings.Builder
	kept := 0
	for _, rec := range records {
		if buf.Len()+len(rec) > limit {
			break
		}
		buf.WriteString(rec)
		kept++
	}
	fmt.Fprintf(&buf, "X\tTRUNCATED\t%d\n", len(records)-kept)
	return buf.String()
}

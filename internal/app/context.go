// Package app implements the `rgit context` command surface: one-call repository orientation for
// an agent's first turn -- branch and upstream, optional diagnostics,
// committable diff rows, then recent commit subjects, as a single
// fixed-shape record stream.
//
// Pure read composition over internal/gitx and internal/diff: no new
// resolution or attribution machinery.
// The diff half is literally internal/diff.Run's default scope, rendered
// through its own RenderPorcelain and re-tagged per line, rather than a
// second walk of report.Files that could drift from what
// `rgit diff --porcelain` emits.
package app

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	diffpkg "github.com/Rethunk-Tech/rethunk-git-cli/internal/diff"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
)

const contextHelp = `usage: rgit context [--json]

One-call repository orientation for an agent's first turn: the same
per-file, per-symbol diffstat "rgit diff" itself reports for everything
committable (staged, unstaged, and untracked), then recent commit
subjects -- as a single, fixed-shape record stream. Replaces the separate
status, diffstat, diff, and log calls an agent would otherwise make before
editing.

--json lists those same records as one JSON object: see
docs/CODES.md#rgit-context---json. Without it, the text stream below is
what is written. There is still nothing here to select or narrow ("a
command with options becomes git status with extra steps" -- see
docs/USAGE.md § Context): any other argument is refused.

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

  W<TAB>stash
      One when the repository has a stash ref.

  W<TAB>sparse
      One when core.sparseCheckout is true.

  W<TAB>warning<TAB>TEXT
      One per non-fatal diff diagnostic. TEXT omits the human [warning]
      prefix that remains on stderr.

  F<TAB>FILE<TAB>SYMBOL<TAB>STATUS<TAB>ADDED<TAB>DELETED
      One per "rgit diff --porcelain" row -- identical fields, with this
      stream's own leading type tag. See docs/CODES.md#output-records for
      what STATUS carries.

  T<TAB>FILE<TAB>WHERE
      One per changed file, in F-row order: where its change sits, staged,
      unstaged, both, or untracked.

  C<TAB>HASH<TAB>SUBJECT
      One per recent commit, newest first, bounded to the last 20.

  X<TAB>TRUNCATED<TAB>COUNT
      At most one, always last: this many records were withheld because
      the stream reached its byte budget.

The text stream is capped at 16 KiB. B or H sorts first (a single record, cost
next to nothing), then S, W diagnostics, F rows, T rows, and C rows. F rows survive
truncation before C rows do: the diff section has no natural bound of its own,
while commits are already bounded up front (the most recent 20, via git's own
-n) and cost little to drop. The X record names how many rows were withheld.
--json encodes those same records after that cap, so the two forms list the
same facts; the JSON text itself is not measured against the cap.

Full reference: docs/USAGE.md
`

// contextRecentCommitLimit bounds gitx.RecentCommits' own -n flag: an
// input bound, not a truncation applied after the fact -- git never
// produces more than this many commits to begin with.
const contextRecentCommitLimit = 20

// contextByteBudget is the hard ceiling on rgit context's text stream.
// --json encodes the records that ceiling kept; it does not re-measure the
// JSON text. Chosen so the common case -- a handful of recent commits plus an
// ordinary in-progress change -- never comes close to it, while a worst
// case (a bulk rename or a vendored dependency bump touching hundreds of
// files) is bounded to a fixed, predictable cost rather than scaling with
// repository size.
const contextByteBudget = 16384

// runContext is hand-parsed. --json is an encoding of the same record
// stream, not a filter. --help wins wherever it appears.
func runContext(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) exitcode.Code {
	asJSON := jsonFlag(ctx)
	var extra []string
	for _, arg := range args {
		switch arg {
		case "--help", "-h":
			fmt.Fprint(stdout, contextHelp)
			return exitcode.Success
		default:
			extra = append(extra, arg)
		}
	}
	if len(extra) != 0 {
		return refuseExtraArgs("context", extra, stderr, contextHelp)
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
			upstream, hasUpstream, err := repo.Upstream(ctx)
			if err != nil {
				fmt.Fprintf(stderr, "rgit: %v\n", err)
				return exitcode.GitFailure
			}
			ahead, behind := 0, 0
			if hasUpstream {
				ahead, behind, err = repo.AheadBehind(ctx)
				if err != nil {
					fmt.Fprintf(stderr, "rgit: %v\n", err)
					return exitcode.GitFailure
				}
			}
			branchRecord = fmt.Sprintf("B\t%s\t%s\t%d\t%d\n", branch, upstream, ahead, behind)
		}
	}

	sequencerOp, sequencerActive, err := repo.SequencerOp(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.GitFailure
	}

	hasStash, err := repo.HasStash(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.GitFailure
	}
	sparseCheckout, err := repo.SparseCheckout(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.GitFailure
	}

	// The default "everything committable" scope -- staged + unstaged vs
	// HEAD, plus untracked -- is exactly what a bare `rgit diff` already
	// reports; no Options fields are set here, guarding against a flag
	// surface that would let this grow into a second `git status`.
	report, err := diffpkg.Run(ctx, repo, root, diffpkg.Options{})
	if err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return exitcode.GitFailure
	}

	// Which side each changed file is on is two cheap name-only queries, not
	// a second attribution pass: the F rows stay the combined diff, and the
	// T records below say where each file's change sits.
	staged, unstaged, err := repo.ChangedPaths(ctx)
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
	if hasStash {
		records = append(records, "W\tstash\n")
	}
	if sparseCheckout {
		records = append(records, "W\tsparse\n")
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
	records = append(records, stageRecords(report, staged, unstaged)...)
	for _, c := range commits {
		records = append(records, fmt.Sprintf("C\t%s\t%s\n", c.Hash, c.Subject))
	}

	stream := buildContextStream(records, contextByteBudget)
	if asJSON {
		if err := writeContextJSON(stdout, stream); err != nil {
			fmt.Fprintf(stderr, "rgit: context: %v\n", err)
			return exitcode.GitFailure
		}
		return exitcode.Success
	}
	fmt.Fprint(stdout, stream)
	return exitcode.Success
}

// contextReport is the machine-readable form of the text stream: one object,
// one entry per record the byte budget kept, in that same order.
type contextReport struct {
	Schema  int                 `json:"schema"`
	Records []map[string]string `json:"records"`
}

func writeContextJSON(w io.Writer, stream string) error {
	records := make([]map[string]string, 0)
	trimmed := strings.TrimRight(stream, "\n")
	if trimmed != "" {
		for line := range strings.SplitSeq(trimmed, "\n") {
			rec, err := contextRecord(line)
			if err != nil {
				return err
			}
			records = append(records, rec)
		}
	}
	return encodeJSON(w, contextReport{Schema: jsonSchema, Records: records})
}

// contextRecord turns one text-stream line into the JSON object's fields.
// Empty columns stay present: an empty upstream or symbol is a fact, the
// same way the text stream prints the tab with nothing between it.
// The unbounded last field (a warning body, a commit subject) keeps any
// tabs it contained.
func contextRecord(line string) (map[string]string, error) {
	fields := strings.Split(line, "\t")
	if fields[0] == "" {
		return nil, fmt.Errorf("empty record")
	}
	rec := map[string]string{"type": fields[0]}
	switch fields[0] {
	case "B":
		if len(fields) != 5 {
			return nil, fmt.Errorf("b record has %d fields", len(fields))
		}
		rec["branch"] = fields[1]
		rec["upstream"] = fields[2]
		rec["ahead"] = fields[3]
		rec["behind"] = fields[4]
	case "H":
		if len(fields) != 2 {
			return nil, fmt.Errorf("h record has %d fields", len(fields))
		}
		rec["sha"] = fields[1]
	case "S":
		if len(fields) != 2 {
			return nil, fmt.Errorf("s record has %d fields", len(fields))
		}
		rec["op"] = fields[1]
	case "W":
		if len(fields) < 2 {
			return nil, fmt.Errorf("w record has %d fields", len(fields))
		}
		rec["kind"] = fields[1]
		if len(fields) > 2 {
			rec["text"] = strings.Join(fields[2:], "\t")
		}
	case "F":
		if len(fields) != 6 {
			return nil, fmt.Errorf("f record has %d fields", len(fields))
		}
		rec["file"] = fields[1]
		rec["symbol"] = fields[2]
		rec["status"] = fields[3]
		rec["added"] = fields[4]
		rec["deleted"] = fields[5]
	case "T":
		if len(fields) != 3 {
			return nil, fmt.Errorf("t record has %d fields", len(fields))
		}
		rec["file"] = fields[1]
		rec["where"] = fields[2]
	case "C":
		if len(fields) < 3 {
			return nil, fmt.Errorf("c record has %d fields", len(fields))
		}
		rec["hash"] = fields[1]
		rec["subject"] = strings.Join(fields[2:], "\t")
	case "X":
		if len(fields) != 3 || fields[1] != "TRUNCATED" {
			return nil, fmt.Errorf("x record %q", line)
		}
		rec["count"] = fields[2]
	default:
		return nil, fmt.Errorf("unrecognized record type %q", fields[0])
	}
	return rec, nil
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

// stageRecords renders one T record per file in the diff report: where its
// change sits, `staged`, `unstaged`, `both`, or `untracked`. Files appear
// in the order the F rows do, so a reader can pair them.
func stageRecords(report *diffpkg.Report, staged, unstaged []string) []string {
	var out []string
	for _, f := range report.Files {
		inStaged, inUnstaged := slices.Contains(staged, f.Path), slices.Contains(unstaged, f.Path)
		where := "untracked"
		switch {
		case inStaged && inUnstaged:
			where = "both"
		case inStaged:
			where = "staged"
		case inUnstaged:
			where = "unstaged"
		}
		out = append(out, fmt.Sprintf("T\t%s\t%s\n", f.Path, where))
	}
	return out
}

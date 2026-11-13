package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/gitx"
	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/resolve"
)

const symbolsHelp = `usage: rgit symbols [--for-commit] [--with-lines] [--with-filename] [--limit N] [--match REGEX] [--porcelain | --json] <file>... (parsers: use --porcelain for NUL-terminated records)

List every declared symbol that can be resolved from each worktree file or its
HEAD blob.
--for-commit     Omit structured-data symbols that commit refuses.
--with-lines     Emit "start,end<TAB>symbol" instead of the bare name, where
                 the range is git's own -L range for that symbol -- the same
                 range blame and log bound themselves to.
--with-filename  Prefix every line with "FILE<TAB>", using the path exactly as
                 given so it composes straight back into a FILE:SYMBOL anchor.
                 Implied by naming more than one file, the way grep prefixes
                 only when several are named; pass it so a script need not
                 special-case an argument list that happens to hold one.
--porcelain      Terminate each record with NUL instead of a newline. The
                 fields are unchanged; only the record separator moves, so a
                 symbol may contain any byte but NUL -- which source text
                 cannot, a file carrying one being binary and refused. Use it
                 wherever the listing is parsed rather than read.
--json           List each record as one JSON object: see
                 docs/CODES.md#rgit-symbols---json. The fields are the same
                 as --porcelain, including --with-lines and --with-filename.
                 Mutually exclusive with --porcelain.
--limit N        Print at most N records, after --match.
--match REGEX    Keep symbols whose name matches REGEX (Go regular expression).

Every file is resolved before any line is written, so an unreadable or
unsupported file anywhere in the list leaves stdout untouched rather than half
a listing.

Full reference: docs/USAGE.md
`

type symbolsOpts struct {
	forCommit, withLines, withFilename, porcelain, asJSON bool
	limit                                                 int
	match                                                 *regexp.Regexp
	positionals                                           []string
}

func parseSymbolsArgs(ctx context.Context, args []string, stdout, stderr io.Writer) (opts symbolsOpts, code exitcode.Code, done bool) {
	opts.asJSON = jsonFlag(ctx)
	opts.limit = -1
	opts.positionals = make([]string, 0, 1)
	afterSeparator := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" && !afterSeparator {
			afterSeparator = true
			continue
		}
		if afterSeparator {
			opts.positionals = append(opts.positionals, arg)
			continue
		}
		switch {
		case arg == "--help" || arg == "-h":
			fmt.Fprint(stdout, symbolsHelp)
			return opts, exitcode.Success, true
		case arg == "--for-commit":
			opts.forCommit = true
		case arg == "--with-lines":
			opts.withLines = true
		case arg == "--with-filename":
			opts.withFilename = true
		case arg == "--porcelain":
			opts.porcelain = true
		case arg == "--json":
			opts.asJSON = true
		case arg == "--limit" || strings.HasPrefix(arg, "--limit="):
			n, next, code, ok := parseSymbolsLimit(args, i, stderr)
			if !ok {
				return opts, code, true
			}
			opts.limit = n
			if next {
				i++
			}
		case arg == "--match" || strings.HasPrefix(arg, "--match="):
			re, next, code, ok := parseSymbolsMatch(args, i, stderr)
			if !ok {
				return opts, code, true
			}
			opts.match = re
			if next {
				i++
			}
		default:
			opts.positionals = append(opts.positionals, arg)
		}
	}
	if opts.porcelain && opts.asJSON {
		fmt.Fprintln(stderr, "rgit: --porcelain and --json are mutually exclusive")
		fmt.Fprint(stderr, symbolsHelp)
		return opts, exitcode.InvalidUsage, true
	}
	if len(opts.positionals) == 0 {
		fmt.Fprintln(stderr, "rgit: symbols requires at least one file argument")
		fmt.Fprint(stderr, symbolsHelp)
		return opts, exitcode.InvalidUsage, true
	}
	return opts, exitcode.Success, false
}

func parseSymbolsLimit(args []string, i int, stderr io.Writer) (n int, next bool, code exitcode.Code, ok bool) {
	value, next, ok := flagValue(args, i, "--limit")
	if !ok {
		fmt.Fprintln(stderr, "rgit: --limit requires a count")
		fmt.Fprint(stderr, symbolsHelp)
		return 0, false, exitcode.InvalidUsage, false
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		fmt.Fprintf(stderr, "rgit: symbols: invalid value %q for --limit\n", value)
		fmt.Fprint(stderr, symbolsHelp)
		return 0, false, exitcode.InvalidUsage, false
	}
	return n, next, exitcode.Success, true
}

func parseSymbolsMatch(args []string, i int, stderr io.Writer) (re *regexp.Regexp, next bool, code exitcode.Code, ok bool) {
	value, next, ok := flagValue(args, i, "--match")
	if !ok {
		fmt.Fprintln(stderr, "rgit: --match requires a regular expression")
		fmt.Fprint(stderr, symbolsHelp)
		return nil, false, exitcode.InvalidUsage, false
	}
	re, err := regexp.Compile(value)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: symbols: invalid value %q for --match: %v\n", value, err)
		fmt.Fprint(stderr, symbolsHelp)
		return nil, false, exitcode.InvalidUsage, false
	}
	return re, next, exitcode.Success, true
}

func filterSymbolRecords(records []symbolRecord, match *regexp.Regexp, limit int) []symbolRecord {
	if match != nil {
		kept := records[:0]
		for _, rec := range records {
			if match.MatchString(rec.Symbol) {
				kept = append(kept, rec)
			}
		}
		records = kept
	}
	if limit >= 0 && len(records) > limit {
		records = records[:limit]
	}
	return records
}

func runSymbols(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) exitcode.Code {
	opts, code, done := parseSymbolsArgs(ctx, args, stdout, stderr)
	if done {
		return code
	}

	root, prefix, repo, code := openRepo(ctx, dir, stderr)
	if code != exitcode.Success {
		return code
	}
	ignoreCase, err := repo.IgnoreCase(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: cannot read core.ignorecase: %v\n", err)
		return exitcode.GitFailure
	}

	// Every file is resolved before anything is written, matching show's own
	// all-or-nothing rule: a listing cut off midway reads as "that file has
	// no more symbols", which is exactly the wrong conclusion.
	listings := make([][]string, 0, len(opts.positionals))
	for _, positional := range opts.positionals {
		lines, code := symbolLines(ctx, root, prefix, repo, ignoreCase, positional, opts.forCommit, opts.withLines, stderr)
		if code != exitcode.Success {
			return code
		}
		listings = append(listings, lines)
	}

	showName := opts.withFilename || len(opts.positionals) > 1
	records, err := symbolRecords(opts.positionals, listings, showName, opts.withLines)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: symbols: %v\n", err)
		return exitcode.GitFailure
	}
	records = filterSymbolRecords(records, opts.match, opts.limit)
	if opts.asJSON {
		if err := writeSymbolsJSON(stdout, records); err != nil {
			fmt.Fprintf(stderr, "rgit: symbols: %v\n", err)
			return exitcode.GitFailure
		}
		return exitcode.Success
	}
	// The record separator is the only thing --porcelain moves. Every field
	// stays where it was, and the symbol stays last, so a parser splits on
	// NUL for records and on the first tabs for fields.
	end := "\n"
	if opts.porcelain {
		end = "\x00"
	}
	for _, rec := range records {
		line := rec.Symbol
		if opts.withLines {
			line = rec.Start + "," + rec.End + "\t" + rec.Symbol
		}
		if showName {
			fmt.Fprintf(stdout, "%s\t%s%s", rec.File, line, end)
			continue
		}
		fmt.Fprintf(stdout, "%s%s", line, end)
	}
	return exitcode.Success
}

// symbolRecord is one symbol in the machine-readable forms. Field names are
// the porcelain columns: file and the line range are present only when the
// text record would carry them.
type symbolRecord struct {
	File   string `json:"file,omitempty"`
	Start  string `json:"start,omitempty"`
	End    string `json:"end,omitempty"`
	Symbol string `json:"symbol"`
}

type symbolsReport struct {
	Schema  int            `json:"schema"`
	Symbols []symbolRecord `json:"symbols"`
}

func symbolRecords(positionals []string, listings [][]string, showName, withLines bool) ([]symbolRecord, error) {
	out := make([]symbolRecord, 0)
	for i, lines := range listings {
		for _, line := range lines {
			rec := symbolRecord{}
			if showName {
				rec.File = positionals[i]
			}
			if withLines {
				rangePart, symbol, ok := strings.Cut(line, "\t")
				if !ok {
					return nil, fmt.Errorf("line record %q has no symbol", line)
				}
				start, end, ok := strings.Cut(rangePart, ",")
				if !ok || start == "" || end == "" {
					return nil, fmt.Errorf("line record %q has no range", line)
				}
				rec.Start, rec.End, rec.Symbol = start, end, symbol
				out = append(out, rec)
				continue
			}
			rec.Symbol = line
			out = append(out, rec)
		}
	}
	return out, nil
}

func writeSymbolsJSON(w io.Writer, records []symbolRecord) error {
	return encodeJSON(w, symbolsReport{Schema: jsonSchema, Symbols: records})
}

// flagValue reads --name VALUE or --name=VALUE at args[i]. next is true
// when the value was a separate argument.
func flagValue(args []string, i int, name string) (value string, next, ok bool) {
	arg := args[i]
	if arg == name {
		if i+1 >= len(args) {
			return "", false, false
		}
		return args[i+1], true, true
	}
	if rest, ok := strings.CutPrefix(arg, name+"="); ok {
		return rest, false, true
	}
	return "", false, false
}

// symbolLines is one file's worth of runSymbols: resolve the path, pick the
// grammar, and render either bare anchors or git's own -L ranges. Split out
// so the multi-file loop resolves every file before the first line is
// written; every refusal has already been reported to stderr when code is
// not Success.
func symbolLines(ctx context.Context, root, prefix string, repo *gitx.Repo, ignoreCase bool, positional string, forCommit, withLines bool, stderr io.Writer) ([]string, exitcode.Code) {
	path := positional
	if filepath.IsAbs(path) {
		var err error
		path, err = filepath.Rel(root, path)
		if err != nil {
			fmt.Fprintf(stderr, "rgit: cannot resolve file %q: %v\n", positional, err)
			return nil, exitcode.GitFailure
		}
	} else {
		path = filepath.Join(prefix, path)
	}
	path = filepath.Clean(path)
	// The absolute-path branch above is why this cannot simply call
	// repoPath: PrefixPath has no notion of an absolute argument, so
	// routing through it would drop the Rel normalization that lets
	// `rgit symbols /abs/file.go` work from a subdirectory. The escape
	// check is the half of repoPath that must not be skipped -- without
	// it symbols reads files above root that diff and blame refuse.
	if err := checkPathEscape(root, path); err != nil {
		fmt.Fprintf(stderr, "rgit: %v\n", err)
		return nil, exitcode.InvalidUsage
	}

	src, err := readWorktreeFile(root, path)
	var headSrc []byte
	headExists := false
	if err != nil {
		if os.IsNotExist(err) {
			worktreeErr := err
			headSrc, headExists, err = repo.CatFile(ctx, "HEAD", path)
			if err == nil && headExists {
				src = headSrc
			} else if err == nil {
				err = worktreeErr
			}
		}
		if err != nil || !headExists {
			fmt.Fprintf(stderr, "rgit: cannot read %q: %v\n", positional, err)
			return nil, exitcode.GitFailure
		}
	}

	lang, ok, _, err := resolve.LanguageForPathFolding(root, path, ignoreCase, func() ([]byte, bool, error) {
		return headSrc, headExists, nil
	})
	if err != nil {
		fmt.Fprintf(stderr, "rgit: cannot read %q: %v\n", positional, err)
		return nil, exitcode.GitFailure
	}
	if !ok {
		fmt.Fprintf(stderr, "rgit: unsupported language for %q%s\n", positional, unsupportedLanguageHint(filepath.Ext(path)))
		return nil, exitcode.UnsupportedLanguage
	}
	if forCommit && resolve.IsStructuredData(lang) {
		return nil, exitcode.Success
	}

	if withLines {
		decls, err := resolve.DeclExtents(lang, src)
		if err != nil {
			fmt.Fprintf(stderr, "rgit: cannot resolve symbols in %q: %v\n", positional, err)
			return nil, exitcode.GitFailure
		}
		lines := make([]string, 0, len(decls))
		for _, decl := range decls {
			// lineRange, not a second conversion: blame and log turn the
			// same extent into the same range through it.
			start, end := lineRange(src, decl.Extent)
			lines = append(lines, fmt.Sprintf("%d,%d\t%s", start, end, decl.Anchor))
		}
		return lines, exitcode.Success
	}

	symbols, err := resolve.DeclOrder(lang, src)
	if err != nil {
		fmt.Fprintf(stderr, "rgit: cannot resolve symbols in %q: %v\n", positional, err)
		return nil, exitcode.GitFailure
	}
	return symbols, exitcode.Success
}

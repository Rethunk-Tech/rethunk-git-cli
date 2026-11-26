package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/Rethunk-Tech/rethunk-git-cli/v3/internal/exitcode"
	"github.com/Rethunk-Tech/rethunk-git-cli/v3/internal/gitx"
	"github.com/Rethunk-Tech/rethunk-git-cli/v3/internal/resolve"
)

const findHelp = `usage: rgit find [--bodies N] [--limit N] [--names] [--json] <query> [dir...] (parsers: use --json for JSON records)

Find declared symbols matching <query> in tracked files under each dir (default:
the repository root) and print their code.

Match order is exact name, case-insensitive exact, prefix, substring, then
subsequence (fuzzy: prsShl matches parseShell). A query with a dot also
matches Receiver.Method / Class.method qualified names.

--bodies N  Print each hit's own bytes -- the same extent show would -- for
            the top N hits (default 3). Remaining hits print the header only.
--limit N   Print at most N hits (default 20), after ranking.
--names     Print header lines only, ignoring --bodies.
--json      List each hit as one JSON object: see
            docs/CODES.md#rgit-find---json.

Each text hit starts with a header line "path:start-end kind name". Hits
beyond --bodies omit the body; --names omits every body.

Exit 0 when at least one symbol matches, 1 when none do.

Full reference: docs/USAGE.md
`

const (
	findDefaultBodies               = 3
	findDefaultLimit                = 20
	findNoMatch       exitcode.Code = 1
)

type findOpts struct {
	bodies, limit int
	names, asJSON bool
	query         string
	dirs          []string
}

func parseFindArgs(ctx context.Context, args []string, stdout, stderr io.Writer) (opts findOpts, code exitcode.Code, done bool) {
	opts.asJSON = jsonFlag(ctx)
	opts.bodies = findDefaultBodies
	opts.limit = findDefaultLimit
	opts.dirs = make([]string, 0)
	afterSeparator := false
	positionals := make([]string, 0, 1)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" && !afterSeparator {
			afterSeparator = true
			continue
		}
		if afterSeparator {
			positionals = append(positionals, arg)
			continue
		}
		switch {
		case arg == "--help" || arg == "-h":
			fmt.Fprint(stdout, findHelp)
			return opts, exitcode.Success, true
		case arg == "--names":
			opts.names = true
		case arg == "--json":
			opts.asJSON = true
		case arg == "--bodies" || strings.HasPrefix(arg, "--bodies="):
			n, next, code, ok := parseCountFlag(args, i, "find", "--bodies", findHelp, stderr)
			if !ok {
				return opts, code, true
			}
			opts.bodies = n
			if next {
				i++
			}
		case arg == "--limit" || strings.HasPrefix(arg, "--limit="):
			n, next, code, ok := parseCountFlag(args, i, "find", "--limit", findHelp, stderr)
			if !ok {
				return opts, code, true
			}
			opts.limit = n
			if next {
				i++
			}
		default:
			if strings.HasPrefix(arg, "-") {
				fmt.Fprintf(stderr, "rgit: find: unknown option %q\n", arg)
				printUsage(stderr, findHelp)
				return opts, exitcode.InvalidUsage, true
			}
			positionals = append(positionals, arg)
		}
	}
	if len(positionals) == 0 || positionals[0] == "" {
		fmt.Fprintln(stderr, "rgit: find requires a query")
		printUsage(stderr, findHelp)
		return opts, exitcode.InvalidUsage, true
	}
	opts.query = positionals[0]
	opts.dirs = positionals[1:]
	if opts.names {
		opts.bodies = 0
	}
	return opts, exitcode.Success, false
}

func runFind(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) exitcode.Code {
	opts, code, done := parseFindArgs(ctx, args, stdout, stderr)
	if done {
		return code
	}

	searches, code := planFindSearches(ctx, dir, opts.dirs, stderr)
	if code != exitcode.Success {
		return code
	}

	var hits []findHit
	var dirLabels []string
	fileCount := 0
	for _, s := range searches {
		ignoreCase, ignoreCaseCode := ignoreCaseOrFail(ctx, s.repo, stderr)
		if ignoreCaseCode != exitcode.Success {
			return ignoreCaseCode
		}
		files, err := listFindFiles(ctx, s.repo, s.specs)
		if err != nil {
			fmt.Fprintf(stderr, "rgit: cannot list files: %v\n", err)
			return exitcode.GitFailure
		}
		fileCount += len(files)
		dirLabels = append(dirLabels, s.labels...)
		hits = append(hits, collectFindHits(ctx, s.root, s.repo, ignoreCase, files, opts.query, opts.limit)...)
	}
	hits = trimFindHits(hits, opts.limit)
	if len(hits) == 0 {
		fmt.Fprintf(stderr, "rgit: no symbols matching %q in %s (%d files)\n", opts.query, strings.Join(dirLabels, ", "), fileCount)
		return findNoMatch
	}
	if opts.limit >= 0 {
		hits = hits[:min(len(hits), opts.limit)]
	}
	for i := range min(len(hits), opts.bodies) {
		hits[i].loadBody(ctx)
	}

	if opts.asJSON {
		if err := writeFindJSON(stdout, hits, opts.bodies); err != nil {
			fmt.Fprintf(stderr, "rgit: find: %v\n", err)
			return exitcode.GitFailure
		}
		return exitcode.Success
	}
	if err := writeFindText(stdout, hits, opts.bodies); err != nil {
		fmt.Fprintf(stderr, "rgit: cannot write: %v\n", err)
		return exitcode.GitFailure
	}
	return exitcode.Success
}

func listFindFiles(ctx context.Context, repo *gitx.Repo, pathspecs []string) ([]string, error) {
	if len(pathspecs) == 0 {
		return repo.LsFilesTracked(ctx)
	}
	extra := append([]string{"--"}, pathspecs...)
	return repo.LsFilesTracked(ctx, extra...)
}

type findSearch struct {
	root   string
	repo   *gitx.Repo
	specs  []string
	labels []string
	all    bool
}

// planFindSearches groups DIR arguments by the repository that contains
// them. An absolute DIR may be a different repo than -C/cwd, which is
// how `rgit find QUERY /other/repo` works without a matching -C.
func planFindSearches(ctx context.Context, chdir string, dirs []string, stderr io.Writer) ([]findSearch, exitcode.Code) {
	if len(dirs) == 0 {
		root, _, repo, code := openRepo(ctx, chdir, stderr)
		if code != exitcode.Success {
			return nil, code
		}
		return []findSearch{{root: root, repo: repo, labels: []string{root}}}, exitcode.Success
	}
	byRoot := make(map[string]*findSearch)
	order := make([]string, 0, 1)
	for _, d := range dirs {
		abs, code := absFindDir(chdir, d, stderr)
		if code != exitcode.Success {
			return nil, code
		}
		root, _, repo, code := openRepo(ctx, abs, stderr)
		if code != exitcode.Success {
			return nil, code
		}
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			fmt.Fprintf(stderr, "rgit: cannot resolve dir %q: %v\n", d, err)
			return nil, exitcode.GitFailure
		}
		if err := checkPathEscape(root, rel); err != nil {
			fmt.Fprintf(stderr, "rgit: %v\n", err)
			return nil, exitcode.InvalidUsage
		}
		g, ok := byRoot[root]
		if !ok {
			g = &findSearch{root: root, repo: repo}
			byRoot[root] = g
			order = append(order, root)
		}
		g.labels = append(g.labels, d)
		if rel == "." {
			g.all = true
			g.specs = nil
			continue
		}
		if !g.all {
			g.specs = append(g.specs, rel)
		}
	}
	out := make([]findSearch, 0, len(order))
	for _, root := range order {
		out = append(out, *byRoot[root])
	}
	return out, exitcode.Success
}

func absFindDir(chdir, d string, stderr io.Writer) (string, exitcode.Code) {
	if filepath.IsAbs(d) {
		return filepath.Clean(d), exitcode.Success
	}
	base := chdir
	if base == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "rgit: %v\n", err)
			return "", exitcode.GitFailure
		}
		base = cwd
	}
	return filepath.Clean(filepath.Join(base, d)), exitcode.Success
}

type findRank int

const (
	findRankExact findRank = iota
	findRankFold
	findRankPrefix
	findRankSubstring
	findRankSubseq
	findRankNone
)

type findHit struct {
	file       string
	start, end int
	kind, name string
	// body is filled after ranking, for the hits whose body is printed only:
	// holding every hit's slice would pin every matched file's source.
	body               []byte
	bodyStart, bodyEnd uint
	root               string
	repo               *gitx.Repo
	rank               findRank
}

func collectFindHits(ctx context.Context, root string, repo *gitx.Repo, ignoreCase bool, files []string, query string, limit int) []findHit {
	n := max(1, runtime.GOMAXPROCS(0))
	jobs := make(chan string)
	var mu sync.Mutex
	hits := make([]findHit, 0, 64)
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			for path := range jobs {
				fileHits := findHitsInFile(ctx, root, repo, ignoreCase, path, query)
				if len(fileHits) == 0 {
					continue
				}
				mu.Lock()
				hits = append(hits, fileHits...)
				// Only the best limit hits are ever printed, so the rest are
				// dropped as the walk goes rather than held to the end.
				if limit >= 0 && len(hits) >= max(2*limit, 1024) {
					hits = trimFindHits(hits, limit)
				}
				mu.Unlock()
			}
		})
	}
send:
	for _, path := range files {
		if !findHasGrammar(path, ignoreCase) {
			continue
		}
		select {
		case <-ctx.Done():
			break send
		case jobs <- path:
		}
	}
	close(jobs)
	wg.Wait()
	return hits
}

// trimFindHits sorts hits best-first and keeps at most limit of them (all
// when limit is negative). It keeps one hit for a zero limit so a match is
// still told apart from no match.
func trimFindHits(hits []findHit, limit int) []findHit {
	keep := max(limit, 1)
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].rank != hits[j].rank {
			return hits[i].rank < hits[j].rank
		}
		if hits[i].file != hits[j].file {
			return hits[i].file < hits[j].file
		}
		if hits[i].start != hits[j].start {
			return hits[i].start < hits[j].start
		}
		return hits[i].name < hits[j].name
	})
	if limit >= 0 && len(hits) > keep {
		hits = slices.Clone(hits[:keep])
	}
	return hits
}

func findHasGrammar(path string, ignoreCase bool) bool {
	_, ok := resolve.ForExtensionFolding(filepath.Ext(path), ignoreCase)
	return ok
}

// loadBody re-reads the hit's file and keeps only its declaration's bytes.
func (h *findHit) loadBody(ctx context.Context) {
	if src, ok := readFindSource(ctx, h.root, h.repo, h.file); ok && h.bodyEnd <= uint(len(src)) {
		h.body = bytes.Clone(src[h.bodyStart:h.bodyEnd])
	}
}

func readFindSource(ctx context.Context, root string, repo *gitx.Repo, path string) ([]byte, bool) {
	src, err := readWorktreeFile(root, path)
	if err == nil {
		return src, true
	}
	if !os.IsNotExist(err) {
		return nil, false
	}
	blob, exists, err := repo.CatFile(ctx, "HEAD", path)
	if err != nil || !exists {
		return nil, false
	}
	return blob, true
}

func findHitsInFile(ctx context.Context, root string, repo *gitx.Repo, ignoreCase bool, path, query string) []findHit {
	src, ok := readFindSource(ctx, root, repo, path)
	if !ok {
		return nil
	}
	lang, ok := resolve.ForExtensionFolding(filepath.Ext(path), ignoreCase)
	if !ok {
		return nil
	}
	decls, err := resolve.DeclExtents(lang, src)
	if err != nil {
		return nil
	}
	dotted := strings.Contains(query, ".")
	out := make([]findHit, 0)
	for _, decl := range decls {
		rank, name := rankFindDecl(decl, query, dotted)
		if rank == findRankNone {
			continue
		}
		start, end := lineRange(src, decl.Extent)
		out = append(out, findHit{
			file:      path,
			start:     start,
			end:       end,
			kind:      shortFindKind(decl.Kind),
			name:      name,
			bodyStart: decl.Extent.Start,
			bodyEnd:   decl.Extent.End,
			root:      root,
			repo:      repo,
			rank:      rank,
		})
	}
	return out
}

func rankFindDecl(decl resolve.Declared, query string, dotted bool) (findRank, string) {
	bare, qualified := findDeclNames(decl.Anchor)
	if dotted {
		if qualified == "" {
			return findRankNone, decl.Anchor
		}
		return matchFindRank(qualified, query), decl.Anchor
	}
	return matchFindRank(bare, query), decl.Anchor
}

func findDeclNames(anchor string) (bare, qualified string) {
	bare = anchor
	if i := strings.LastIndex(anchor, "."); i >= 0 && i+1 < len(anchor) {
		return anchor[i+1:], stripFindRecv(anchor)
	}
	return bare, ""
}

func stripFindRecv(anchor string) string {
	var b strings.Builder
	b.Grow(len(anchor))
	for i := 0; i < len(anchor); i++ {
		switch anchor[i] {
		case '(', ')', '*':
			continue
		default:
			b.WriteByte(anchor[i])
		}
	}
	return b.String()
}

func shortFindKind(nodeKind string) string {
	if nodeKind == "" {
		return "symbol"
	}
	for _, suf := range []string{"_declaration", "_definition", "_spec", "_statement", "_item"} {
		if rest, ok := strings.CutSuffix(nodeKind, suf); ok {
			return rest
		}
	}
	return nodeKind
}

func matchFindRank(name, query string) findRank {
	if name == query {
		return findRankExact
	}
	if strings.EqualFold(name, query) {
		return findRankFold
	}
	if strings.HasPrefix(name, query) {
		return findRankPrefix
	}
	if query != "" && strings.HasPrefix(strings.ToLower(name), strings.ToLower(query)) {
		return findRankPrefix
	}
	if strings.Contains(name, query) {
		return findRankSubstring
	}
	if query != "" && strings.Contains(strings.ToLower(name), strings.ToLower(query)) {
		return findRankSubstring
	}
	if isFindSubseq(name, query) {
		return findRankSubseq
	}
	return findRankNone
}

func isFindSubseq(name, query string) bool {
	if query == "" {
		return false
	}
	n := strings.ToLower(name)
	q := strings.ToLower(query)
	j := 0
	for i := 0; i < len(n) && j < len(q); i++ {
		if n[i] == q[j] {
			j++
		}
	}
	return j == len(q)
}

func writeFindText(w io.Writer, hits []findHit, bodies int) error {
	for i, hit := range hits {
		if _, err := fmt.Fprintf(w, "%s:%d-%d %s %s\n", hit.file, hit.start, hit.end, hit.kind, hit.name); err != nil {
			return err
		}
		if i >= bodies {
			continue
		}
		if err := writeVerbatim(w, hit.body); err != nil {
			return err
		}
		// A symbol with no terminating newline would glue the next header
		// onto its last line; show has no next record to protect.
		if len(hit.body) == 0 || hit.body[len(hit.body)-1] != '\n' {
			if _, err := io.WriteString(w, "\n"); err != nil {
				return err
			}
		}
	}
	return nil
}

type findRecord struct {
	File  string `json:"file"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	Body  string `json:"body,omitempty"`
}

type findReport struct {
	Schema int          `json:"schema"`
	Hits   []findRecord `json:"hits"`
}

func writeFindJSON(w io.Writer, hits []findHit, bodies int) error {
	records := make([]findRecord, len(hits))
	for i, hit := range hits {
		rec := findRecord{
			File:  hit.file,
			Start: hit.start,
			End:   hit.end,
			Kind:  hit.kind,
			Name:  hit.name,
		}
		if i < bodies {
			rec.Body = string(hit.body)
		}
		records[i] = rec
	}
	return encodeJSON(w, findReport{Schema: jsonSchema, Hits: records})
}

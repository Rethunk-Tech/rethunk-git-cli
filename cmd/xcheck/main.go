//go:build rgit_xcheck

// Command xcheck measures the LSP cross-check against a real corpus: it
// drives the same three public seams `rgit diff` uses -- resolve.Open plus
// File.DeclExtents for the declaration list, resolve.Resolve per anchor,
// lsp.Session.Dial plus Client.DocumentSymbols for the server's outline,
// and resolve.MatchAndCompare for the verdict -- and logs every
// disagreement instead of collapsing them into one exit code. It answers
// the question a fixture cannot: across real source, how often does the
// comparison fire, and when it fires, which side is wrong.
//
// Behind the rgit_xcheck tag so it never enters the shipped binary. It is
// committed rather than rebuilt per measurement because two defects in
// throwaway versions of it each produced confident, wrong numbers, and
// neither was visible in the output:
//
//   - Corpus paths must be absolute. A list of relative paths measures
//     nothing at all wherever the harness is run from a different
//     directory, and reports success with an empty table.
//   - A stdio language server is one-shot. Caching one session per
//     language measures only the first file of each; gopls alone survives
//     it, being a socket daemon, which makes Go look correct while every
//     other grammar silently reports a single file.
//
// Pseudo-anchors are skipped, matching CrossCheckExtents.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/lsp"
	"github.com/Rethunk-Tech/rethunk-git-cli/internal/resolve"
)

func main() {
	dump := flag.Bool("dump", false, "print every server symbol per file")
	timeout := flag.Duration("timeout", 25*time.Second, "per-file budget")
	rootFlag := flag.String("root", ".", "workspace root handed to the server")
	flag.Parse()

	root, _ := filepath.Abs(*rootFlag)
	sessions := map[string]*lsp.Session{}
	defer func() {
		for _, s := range sessions {
			s.Close()
		}
	}()

	failed := 0
	type counts struct{ files, compared, agree, disagree, notNamed int }
	stats := map[string]*counts{}

	for _, path := range flag.Args() {
		abs, err := filepath.Abs(path)
		if err != nil {
			continue
		}
		src, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		lang, ok := resolve.ForPathFolding(abs, src, false)
		if !ok {
			continue
		}
		name := lang.Name()
		if stats[name] == nil {
			stats[name] = &counts{}
		}
		st := stats[name]

		f, err := resolve.Open(lang, src)
		if err != nil {
			continue
		}
		sess := lsp.NewSession()
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		client, _ := sess.Dial(ctx, name, root)
		if client == nil {
			cancel()
			f.Close()
			continue
		}
		symbols, err := client.DocumentSymbols(ctx, abs, src)
		cancel()
		if err != nil {
			sess.Close()
			f.Close()
			failed++
			continue
		}
		st.files++
		if *dump {
			fmt.Printf("== %s (%s)\n", abs, name)
			for _, s := range symbols {
				fmt.Printf("   server: %-45q container=%-20q L%d..L%d\n", s.Name, s.Container, s.StartLine+1, s.EndLine+1)
			}
		}
		for _, d := range f.DeclExtents() {
			res, rerr := f.Resolve(d.Anchor)
			if rerr != nil || res == nil || res.Pseudo {
				continue
			}
			found, cerr := resolve.MatchAndCompare(src, res, symbols)
			switch {
			case !found:
				st.notNamed++
				if *dump {
					fmt.Printf("   NOTNAMED %q\n", d.Anchor)
				}
			case cerr != nil:
				st.compared++
				st.disagree++
				fmt.Printf("DISAGREE\t%s\t%s\t%s\t%v\n", name, abs, d.Anchor, cerr)
			default:
				st.compared++
				st.agree++
			}
		}
		f.Close()
		sess.Close()
	}

	fmt.Fprintf(os.Stderr, "files with no server answer: %d\n", failed)
	langs := make([]string, 0, len(stats))
	for k := range stats {
		langs = append(langs, k)
	}
	sort.Strings(langs)
	fmt.Println("\nlang\tfiles\tcompared\tagree\tdisagree\tnotNamed")
	for _, l := range langs {
		s := stats[l]
		fmt.Printf("%s\t%d\t%d\t%d\t%d\t%d\n", l, s.files, s.compared, s.agree, s.disagree, s.notNamed)
	}
}

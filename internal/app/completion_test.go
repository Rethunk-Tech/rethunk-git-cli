// Drift coverage for completion.go's hand-duplicated candidate lists.
// Neither test reconstructs flag or subcommand registration a second
// time -- that would just relocate the duplication this file exists to
// catch -- they instead read back what the real command surface already
// produces (topLevelHelp's own Commands section, and each subcommand's
// own --help, which is pflag's own FlagUsages rendering of its live
// FlagSet) and compare it against the completion constants.
package app

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestCompletionSubcommands checks rgitSubcommands against topLevelHelp's
// own Commands section (app.go) rather than a second hand-picked list:
// every documented subcommand must appear, and every word in
// rgitSubcommands must be either a documented subcommand or one of Run's
// own top-level aliases/flags ("help", "-h", "--help", "--version" --
// none of which is a row in Commands:, since they are not dispatched the
// same way a subcommand is).
func TestCompletionSubcommands(t *testing.T) {
	t.Parallel()
	documented := subcommandNamesFromHelp(t, topLevelHelp)
	if len(documented) == 0 {
		t.Fatal("could not parse any subcommand names out of topLevelHelp -- did its Commands: section move or change shape?")
	}

	got := tokenSet(rgitSubcommands)

	for _, name := range documented {
		if !got[name] {
			t.Errorf("topLevelHelp documents %q as a subcommand but rgitSubcommands omits it", name)
		}
	}

	aliases := map[string]bool{"help": true, "-h": true, "--help": true, "--version": true}
	for tok := range got {
		if aliases[tok] {
			continue
		}
		found := slices.Contains(documented, tok)
		if !found {
			t.Errorf("rgitSubcommands has %q, which topLevelHelp does not document as a subcommand and is not a known top-level alias/flag -- stale entry?", tok)
		}
	}
}

// subcommandNamesFromHelp extracts the first word of each line in help's
// "Commands:" section, up to the following blank line.
func subcommandNamesFromHelp(t *testing.T, help string) []string {
	t.Helper()
	const marker = "Commands:\n"
	_, after, ok := strings.Cut(help, marker)
	if !ok {
		return nil
	}
	rest := after
	if end := strings.Index(rest, "\n\n"); end >= 0 {
		rest = rest[:end]
	}
	var names []string
	for line := range strings.SplitSeq(rest, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 {
			names = append(names, fields[0])
		}
	}
	return names
}

// flagTokenRe matches a usage line's own flag declaration -- the fixed
// "  -x, --long" / "      --long" prefix pflag.FlagUsages emits -- and
// nothing past it, so a flag's own description text (commit's --gpg-sign
// entry spells out "-S/-S<key-id>/--gpg-sign=<key-id>" in prose) is never
// mistaken for a second flag declaration.
var flagTokenRe = regexp.MustCompile(`(?m)^\s*(?:-(\w), )?--([\w-]+)`)

// TestCompletionFlags_MatchLiveFlagSets checks rgitDiffFlags and
// rgitCommitFlags against each command's own --help output. diff.go and
// commit.go build their pflag.FlagSet as a local variable and never
// return it, so this reads pflag's own FlagUsages rendering back out of
// --help rather than reconstructing flag registration a third time.
//
// Two tokens can never appear in that output and are documented,
// per-command exceptions rather than a false failure: "-h"/"--help" are
// handled by parseFlagsOrHelp's shared pflag.ErrHelp path, never a
// registered flag on either FlagSet, and commit's "-S" is rewritten to
// --gpg-sign by expandGPGSignShorthand before Parse ever sees it
// (commit.go), so it is not a registered shorthand either.
func TestCompletionFlags_MatchLiveFlagSets(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		stdout   string
		constant string
		extra    []string
	}{
		{"diff", runDiffHelp(), rgitDiffFlags, []string{"-h", "--help"}},
		{"commit", runCommitHelp(), rgitCommitFlags, []string{"-h", "--help", "-S"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			live := map[string]bool{}
			for _, m := range flagTokenRe.FindAllStringSubmatch(tt.stdout, -1) {
				if m[1] != "" {
					live["-"+m[1]] = true
				}
				live["--"+m[2]] = true
			}
			for _, e := range tt.extra {
				live[e] = true
			}

			got := tokenSet(tt.constant)

			for tok := range live {
				if !got[tok] {
					t.Errorf("%s: live flag %q is missing from the completion constant", tt.name, tok)
				}
			}
			for tok := range got {
				if !live[tok] {
					t.Errorf("%s: completion constant has %q, which is not a live flag -- stale entry?", tt.name, tok)
				}
			}
		})
	}
}

func tokenSet(s string) map[string]bool {
	out := map[string]bool{}
	for tok := range strings.FieldsSeq(s) {
		out[tok] = true
	}
	return out
}

// runDiffHelp and runCommitHelp run their own subcommand with --help and
// return what it printed to stdout -- pflag's own FlagUsages rendering of
// the live FlagSet each command built for itself.
func runDiffHelp() string {
	var stdout, stderr strings.Builder
	runDiff(context.Background(), []string{"--help"}, &stdout, &stderr)
	return stdout.String()
}

func runCommitHelp() string {
	var stdout, stderr strings.Builder
	runCommit(context.Background(), []string{"--help"}, &stdout, &stderr)
	return stdout.String()
}

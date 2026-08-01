// The `rgit completion` command surface: prints a shell completion script
// for bash or zsh. There is nothing to resolve or synthesize here, so
// unlike diff and commit this needs no repository at all -- see
// docs/USAGE.md § Shell completion.
package app

import (
	"fmt"
	"io"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
)

const completionHelp = `usage: rgit completion <bash|zsh>

Print a shell completion script for that shell to stdout.

  bash: source <(rgit completion bash)
  zsh:  source <(rgit completion zsh)   # after compinit has run

Persistent installation: docs/INSTALL.md § Shell completion.
`

// runCompletion's --help check is a loop rather than a sole-argument test
// (m12: help wins wherever it appears, the same rule as every other
// hand-parsed command, e.g. `rgit completion bash --help` -- not only
// `rgit completion --help` on its own) so it can still see past the shell
// positional to find it.
func runCompletion(args []string, stdout, stderr io.Writer) exitcode.Code {
	var shell string
	haveShell := false
	for _, a := range args {
		if a == "--help" || a == "-h" {
			fmt.Fprint(stdout, completionHelp)
			return exitcode.Success
		}
		if haveShell {
			fmt.Fprintln(stderr, "rgit: completion requires exactly one shell argument (bash or zsh)")
			fmt.Fprint(stderr, completionHelp)
			return exitcode.InvalidUsage
		}
		shell = a
		haveShell = true
	}
	if !haveShell {
		fmt.Fprintln(stderr, "rgit: completion requires exactly one shell argument (bash or zsh)")
		fmt.Fprint(stderr, completionHelp)
		return exitcode.InvalidUsage
	}
	switch shell {
	case "bash":
		fmt.Fprint(stdout, bashCompletionScript)
		return exitcode.Success
	case "zsh":
		fmt.Fprint(stdout, zshCompletionScript)
		return exitcode.Success
	default:
		// m14: the missing-shell branch above already prints completionHelp;
		// an unrecognized shell name is the same kind of usage error and
		// must not leave the caller with less guidance than a bare
		// `rgit completion` gets.
		fmt.Fprintf(stderr, "rgit: unknown shell %q; supported: bash, zsh\n", shell)
		fmt.Fprint(stderr, completionHelp)
		return exitcode.InvalidUsage
	}
}

// rgitSubcommands is the completion script's first-word candidate list:
// every subcommand app.go's Run dispatches, plus its own top-level
// aliases and flags -- including -C, which is why neither script can read
// the command out of a fixed word index any more: `rgit -C <path> commit`
// puts it at word three, and the two scripts walk past each -C pair to
// find it (offering directories for the pair's own argument). completion_test.go's TestCompletionSubcommands
// checks it against topLevelHelp's own Commands section, generated at
// test time rather than copied, so a new subcommand missing here fails
// the suite instead of only being missing from a shell's tab completion.
const rgitSubcommands = "diff commit blame log context languages doctor completion help -C -h --help --version"

// rgitDiffFlags and rgitCommitFlags are the static parts of completion:
// each subcommand's own flag surface, mirroring docs/USAGE.md § Flags plus
// the --sym/--file pair diff and commit share (internal/app/shared.go's
// newTargetFlagSet -- no other subcommand builds a flag set with it, or
// takes --sym/--file at all). They are duplicated here rather than
// introspected at runtime because the completion script is a standalone
// text blob with no Go runtime behind it once emitted -- but
// completion_test.go's
// TestCompletionFlags_MatchLiveFlagSets drives each command's own --help
// output (pflag's own FlagUsages rendering of the live FlagSet, not a
// second hand copy) and fails the suite the moment either list drifts from
// what diff.go or commit.go actually registers.
const rgitDiffFlags = "--unstaged --staged --cached --range --porcelain --exit-code --quiet --sym --file -h --help"

const rgitCommitFlags = "-m --message -F --message-file -s --signoff --trailer --amend --allow-empty --push " +
	"--dry-run --no-verify --fixup --squash --author --date --reset-author --porcelain -q --quiet " +
	"-S --gpg-sign --no-gpg-sign --sym --file -h --help"

// rgitLanguagesFlags, rgitDoctorFlags, rgitCompletionFlags, rgitBlameFlags,
// rgitLogFlags, and rgitContextFlags are the same kind of static mirror as
// rgitDiffFlags/rgitCommitFlags above, for the subcommands small enough
// that languages.go, doctor.go, blame.go, log.go, context.go, and this
// file parse their own args by hand rather than building a pflag.FlagSet
// -- completion previously offered none of them at all, since neither
// shell script's case statement had an entry for these commands.
// TestCompletionFlags_MatchLiveFlagSets checks all of them against their
// own --help output the same way.
const rgitLanguagesFlags = "--porcelain -h --help"
const rgitDoctorFlags = "-h --help"
const rgitCompletionFlags = "-h --help"
const rgitBlameFlags = "--porcelain -h --help"
const rgitLogFlags = "--porcelain -p --patch --since --until -h --help"
const rgitContextFlags = "-h --help"

// bashCompletionScript is emitted verbatim by `rgit completion bash`. The
// one dynamic piece -- symbol names after "FILE:" -- shells back out to
// `rgit diff --porcelain` and cuts its SYMBOL column for the matching FILE
// (docs/CODES.md § Output records), so completion can never name a symbol
// `rgit commit` would not itself accept. Any failure of that call (not a
// repo, rgit not on PATH, anything) is swallowed by the 2>/dev/null and
// leaves the candidate list empty -- completion must never put an error on
// the prompt.
//
// Symbol completion only fires for diff, commit, blame, and log -- the
// four commands that actually take a FILE:SYMBOL anchor. context takes no
// targets at all (context.go's own arg-count check refuses any), and
// languages/doctor/completion take none either, so offering symbol names
// after a colon there would dangle a candidate none of them would accept.
const bashCompletionScript = `# rgit bash completion -- generated by ` + "`rgit completion bash`" + `.
# Load once:  source <(rgit completion bash)
# Persist:    rgit completion bash > ~/.local/share/bash-completion/completions/rgit

_rgit_diff_flags="` + rgitDiffFlags + `"
_rgit_commit_flags="` + rgitCommitFlags + `"
_rgit_blame_flags="` + rgitBlameFlags + `"
_rgit_log_flags="` + rgitLogFlags + `"
_rgit_context_flags="` + rgitContextFlags + `"
_rgit_languages_flags="` + rgitLanguagesFlags + `"
_rgit_doctor_flags="` + rgitDoctorFlags + `"
_rgit_completion_flags="` + rgitCompletionFlags + `"

_rgit_symbols() {
    rgit diff --porcelain 2>/dev/null | awk -F'\t' -v f="$1" '$1 == f && $2 != "" { print $2 }'
}

_rgit_completion() {
    local cur cmd i
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"

    # The global "-C <path>" options sit before the command, so the command
    # is not at a fixed index -- walk past each pair to find it.
    i=1
    while [[ "${COMP_WORDS[i]}" == "-C" ]]; do i=$((i+2)); done
    cmd="${COMP_WORDS[i]}"

    if [[ $COMP_CWORD -le $i ]]; then
        if [[ "${COMP_WORDS[COMP_CWORD-1]}" == "-C" ]]; then
            COMPREPLY=( $(compgen -d -- "$cur") )
        else
            COMPREPLY=( $(compgen -W "` + rgitSubcommands + `" -- "$cur") )
        fi
        return 0
    fi

    local flags=""
    case "$cmd" in
        diff) flags="$_rgit_diff_flags" ;;
        commit) flags="$_rgit_commit_flags" ;;
        blame) flags="$_rgit_blame_flags" ;;
        log) flags="$_rgit_log_flags" ;;
        context) flags="$_rgit_context_flags" ;;
        languages) flags="$_rgit_languages_flags" ;;
        doctor) flags="$_rgit_doctor_flags" ;;
        completion)
            flags="$_rgit_completion_flags"
            if [[ $COMP_CWORD -eq $((i+1)) && "$cur" != -* ]]; then
                COMPREPLY=( $(compgen -W "bash zsh" -- "$cur") )
                return 0
            fi
            ;;
        *) return 0 ;;
    esac

    if [[ "$cur" == -* ]]; then
        COMPREPLY=( $(compgen -W "$flags" -- "$cur") )
        return 0
    fi

    if [[ "$cur" == *:* ]]; then
        case "$cmd" in
            diff|commit|blame|log)
                local file="${cur%:*}" symprefix="${cur##*:}"
                COMPREPLY=( $(compgen -P "${file}:" -W "$(_rgit_symbols "$file")" -- "$symprefix") )
                return 0
                ;;
        esac
    fi

    COMPREPLY=( $(compgen -f -- "$cur") )
}

complete -F _rgit_completion rgit
`

// zshCompletionScript is `rgit completion zsh`'s output. It is a plain
// compadd-based completer rather than the _arguments/_describe machinery,
// since the shape here (one flag list per subcommand, one dynamic lookup)
// does not need it -- see bashCompletionScript's doc comment for the
// dynamic-symbol contract, identical here, including the same restriction
// to diff, commit, blame, and log.
const zshCompletionScript = `#compdef rgit
# rgit zsh completion -- generated by ` + "`rgit completion zsh`" + `.
# Load once (after compinit): source <(rgit completion zsh)
# Persist: rgit completion zsh > "$fpath[1]/_rgit", then re-run compinit

_rgit_diff_flags=(` + rgitDiffFlags + `)
_rgit_commit_flags=(` + rgitCommitFlags + `)
_rgit_blame_flags=(` + rgitBlameFlags + `)
_rgit_log_flags=(` + rgitLogFlags + `)
_rgit_context_flags=(` + rgitContextFlags + `)
_rgit_languages_flags=(` + rgitLanguagesFlags + `)
_rgit_doctor_flags=(` + rgitDoctorFlags + `)
_rgit_completion_flags=(` + rgitCompletionFlags + `)

_rgit_symbols() {
    rgit diff --porcelain 2>/dev/null | awk -F'\t' -v f="$1" '$1 == f && $2 != "" { print $2 }'
}

_rgit() {
    local cur cmd
    local -i i
    cur="${words[CURRENT]}"

    # Same walk past the global "-C <path>" options as the bash script's.
    i=2
    while [[ "${words[i]}" == "-C" ]]; do (( i += 2 )); done
    cmd="${words[i]}"

    if (( CURRENT <= i )); then
        if [[ "${words[CURRENT-1]}" == "-C" ]]; then
            _files -/
        else
            compadd -- ` + rgitSubcommands + `
        fi
        return
    fi

    local -a flags
    case "$cmd" in
        diff) flags=("${_rgit_diff_flags[@]}") ;;
        commit) flags=("${_rgit_commit_flags[@]}") ;;
        blame) flags=("${_rgit_blame_flags[@]}") ;;
        log) flags=("${_rgit_log_flags[@]}") ;;
        context) flags=("${_rgit_context_flags[@]}") ;;
        languages) flags=("${_rgit_languages_flags[@]}") ;;
        doctor) flags=("${_rgit_doctor_flags[@]}") ;;
        completion)
            flags=("${_rgit_completion_flags[@]}")
            if (( CURRENT == i+1 )) && [[ "$cur" != -* ]]; then
                compadd -- bash zsh
                return
            fi
            ;;
        *) return ;;
    esac

    if [[ "$cur" == -* ]]; then
        compadd -- "${flags[@]}"
        return
    fi

    if [[ "$cur" == *:* ]]; then
        case "$cmd" in
            diff|commit|blame|log)
                local file="${cur%:*}"
                local out
                out="$(_rgit_symbols "$file")"
                # Splitting "" with (f) still yields one empty element, not
                # zero -- guard it, or a file with no candidates offers a
                # bare "FILE:". Narrowing candidates against what's already
                # typed after the colon is compadd's own job here, the same
                # as it already is for the flag and subcommand lists above
                # -- zsh's completion system matches added candidates
                # against the current word automatically, unlike bash's
                # compgen, which needs "-- $cur" to do it explicitly.
                if [[ -n "$out" ]]; then
                    local -a syms
                    syms=("${(@f)out}")
                    compadd -P "${file}:" -- "${syms[@]}"
                fi
                return
                ;;
        esac
    fi

    _files
}

(( $+functions[compdef] )) && compdef _rgit rgit
`

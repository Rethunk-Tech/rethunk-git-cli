// The `rgit completion` command surface: prints a shell completion script
// for bash, zsh, fish, or PowerShell. There is nothing to resolve or synthesize here, so
// unlike diff and commit this needs no repository at all -- see
// docs/USAGE.md § Shell completion.
package app

import (
	"fmt"
	"io"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
)

const completionHelp = `usage: rgit completion <bash|zsh|fish|pwsh>

Print a shell completion script for that shell to stdout.

  bash: source <(rgit completion bash)
  zsh:  source <(rgit completion zsh)   # after compinit has run
  fish: rgit completion fish | source
  pwsh: rgit completion pwsh | Invoke-Expression

Persistent installation: docs/INSTALL.md § Shell completion.

Full reference: docs/USAGE.md
`

// runCompletion's --help check is a loop rather than a sole-argument test:
// help wins wherever it appears, the same rule as every other
// hand-parsed command, e.g. `rgit completion bash --help` -- not only
// `rgit completion --help` on its own -- so it can still see past the shell
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
			fmt.Fprintln(stderr, "rgit: completion requires exactly one shell argument (bash, zsh, fish, or pwsh)")
			fmt.Fprint(stderr, completionHelp)
			return exitcode.InvalidUsage
		}
		shell = a
		haveShell = true
	}
	if !haveShell {
		fmt.Fprintln(stderr, "rgit: completion requires exactly one shell argument (bash, zsh, fish, or pwsh)")
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
	case "fish":
		// io.WriteString, not fmt.Fprint: the script's own printf '%s\n'
		// lines make go vet's printf checker flag Fprint as a possible
		// missing-arguments mistake, even though nothing here is ever
		// meant to be formatted. The error is discarded on purpose, the
		// same as every other diagnostic/result write in this package
		// (.golangci.yml's errcheck exemption covers fmt.Fprint* only,
		// not io.WriteString).
		_, _ = io.WriteString(stdout, fishCompletionScript)
		return exitcode.Success
	case "pwsh":
		_, _ = io.WriteString(stdout, pwshCompletionScript)
		return exitcode.Success
	default:
		// The missing-shell branch above already prints completionHelp;
		// an unrecognized shell name is the same kind of usage error and
		// must not leave the caller with less guidance than a bare
		// `rgit completion` gets.
		fmt.Fprintf(stderr, "rgit: unknown shell %q; supported: bash, zsh, fish, pwsh\n", shell)
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
const rgitSubcommands = "diff commit show blame log context languages doctor completion symbols help -C -h --help --version"

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
const rgitDiffFlags = "--unstaged --staged --cached --range --porcelain --exit-code --quiet -p --patch --pathspec-from-file --pathspec-file-nul --sym --file -h --help"

const rgitCommitFlags = "-m --message -F --message-file -s --signoff --trailer --amend --allow-empty --push " +
	"--dry-run --no-verify --fixup --squash --reuse-message --reedit-message --author --date --reset-author --porcelain -q --quiet " +
	"-S --gpg-sign --no-gpg-sign -o --only --pathspec-from-file --pathspec-file-nul --sym --file -h --help"

// rgitLanguagesFlags, rgitDoctorFlags, rgitCompletionFlags, rgitShowFlags,
// rgitBlameFlags,
// rgitLogFlags, rgitContextFlags, and rgitSymbolsFlags are the same kind of static mirror as
// rgitDiffFlags/rgitCommitFlags above, for the subcommands small enough
// that languages.go, doctor.go, blame.go, log.go, context.go, symbols.go,
// show.go, and this file parse their own args by hand rather than building a
// pflag.FlagSet.
// TestCompletionFlags_MatchLiveFlagSets checks all of them against their
// own --help output the same way.
const rgitLanguagesFlags = "--porcelain --in-repo -h --help"
const rgitDoctorFlags = "--porcelain --deep -h --help"
const rgitCompletionFlags = "-h --help"
const rgitShowFlags = "--source -h --help"
const rgitBlameFlags = "-p --porcelain --follow-rename -h --help"
const rgitLogFlags = "--porcelain -p --patch --follow-rename --since --until -n --max-count -h --help"
const rgitContextFlags = "-h --help"
const rgitSymbolsFlags = "--for-commit --with-lines -h --help"

// bashCompletionScript is emitted verbatim by `rgit completion bash`. The
// one dynamic piece -- symbol names after "FILE:" -- shells back out to
// `rgit symbols FILE`, or its commit-safe mode for `commit`, lists declarations
// the corresponding command can accept. Any failure of that call (not a repo,
// rgit not on PATH, anything) is swallowed by the 2>/dev/null and leaves the
// candidate list empty -- completion must never put an error on the prompt.
//
// Symbol completion only fires for diff, commit, show, blame, and log --
// the five commands that actually take a FILE:SYMBOL anchor. context takes no
// targets at all (context.go's own arg-count check refuses any), and
// languages/doctor/completion take none either, so offering symbol names
// after a colon there would dangle a candidate none of them would accept.
const bashCompletionScript = `# rgit bash completion -- generated by ` + "`rgit completion bash`" + `.
# Load once:  source <(rgit completion bash)
# Persist:    rgit completion bash > ~/.local/share/bash-completion/completions/rgit

_rgit_diff_flags="` + rgitDiffFlags + `"
_rgit_commit_flags="` + rgitCommitFlags + `"
_rgit_show_flags="` + rgitShowFlags + `"
_rgit_blame_flags="` + rgitBlameFlags + `"
_rgit_log_flags="` + rgitLogFlags + `"
_rgit_context_flags="` + rgitContextFlags + `"
_rgit_languages_flags="` + rgitLanguagesFlags + `"
_rgit_doctor_flags="` + rgitDoctorFlags + `"
_rgit_completion_flags="` + rgitCompletionFlags + `"
_rgit_symbols_flags="` + rgitSymbolsFlags + `"

# Symbol candidates come from rgit symbols FILE (commit uses --for-commit).
_rgit_symbols() {
    if [[ "$2" == "commit" ]]; then
        rgit symbols --for-commit "$1" 2>/dev/null
    else
        rgit symbols "$1" 2>/dev/null
    fi
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
        show) flags="$_rgit_show_flags" ;;
        blame) flags="$_rgit_blame_flags" ;;
        log) flags="$_rgit_log_flags" ;;
        context) flags="$_rgit_context_flags" ;;
        languages) flags="$_rgit_languages_flags" ;;
        doctor) flags="$_rgit_doctor_flags" ;;
        symbols) flags="$_rgit_symbols_flags" ;;
        completion)
            flags="$_rgit_completion_flags"
            if [[ $COMP_CWORD -eq $((i+1)) && "$cur" != -* ]]; then
                COMPREPLY=( $(compgen -W "bash zsh fish pwsh" -- "$cur") )
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
            diff|commit|show|blame|log)
                local file="${cur%:*}" symprefix="${cur##*:}"
                COMPREPLY=( $(compgen -P "${file}:" -W "$(_rgit_symbols "$file" "$cmd")" -- "$symprefix") )
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
// to diff, commit, show, blame, and log.
const zshCompletionScript = `#compdef rgit
# rgit zsh completion -- generated by ` + "`rgit completion zsh`" + `.
# Load once (after compinit): source <(rgit completion zsh)
# Persist: rgit completion zsh > "$fpath[1]/_rgit", then re-run compinit

_rgit_diff_flags=(` + rgitDiffFlags + `)
_rgit_commit_flags=(` + rgitCommitFlags + `)
_rgit_show_flags=(` + rgitShowFlags + `)
_rgit_blame_flags=(` + rgitBlameFlags + `)
_rgit_log_flags=(` + rgitLogFlags + `)
_rgit_context_flags=(` + rgitContextFlags + `)
_rgit_languages_flags=(` + rgitLanguagesFlags + `)
_rgit_doctor_flags=(` + rgitDoctorFlags + `)
_rgit_completion_flags=(` + rgitCompletionFlags + `)
_rgit_symbols_flags=(` + rgitSymbolsFlags + `)

# Symbol candidates come from rgit symbols FILE (commit uses --for-commit).
_rgit_symbols() {
    if [[ "$2" == "commit" ]]; then
        rgit symbols --for-commit "$1" 2>/dev/null
    else
        rgit symbols "$1" 2>/dev/null
    fi
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
        show) flags=("${_rgit_show_flags[@]}") ;;
        blame) flags=("${_rgit_blame_flags[@]}") ;;
        log) flags=("${_rgit_log_flags[@]}") ;;
        context) flags=("${_rgit_context_flags[@]}") ;;
        languages) flags=("${_rgit_languages_flags[@]}") ;;
        doctor) flags=("${_rgit_doctor_flags[@]}") ;;
        symbols) flags=("${_rgit_symbols_flags[@]}") ;;
        completion)
            flags=("${_rgit_completion_flags[@]}")
            if (( CURRENT == i+1 )) && [[ "$cur" != -* ]]; then
                compadd -- bash zsh fish pwsh
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
            diff|commit|show|blame|log)
                local file="${cur%:*}"
                local out
                out="$(_rgit_symbols "$file" "$cmd")"
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

// fishCompletionScript is `rgit completion fish`'s output. Unlike bash's
// compgen/COMPREPLY or zsh's compadd, fish has no notion of a per-command
// completion function registered by name -- completions are driven by one
// dynamic candidate generator (__rgit_complete, run for its stdout on every
// tab) registered once via `complete`, so the walk-past-"-C", pick-flags-
// by-subcommand, and dynamic-symbol logic all live inside that one
// function rather than being split across cases the way bash's
// case-driven dispatch is. Flag lists are the same shared constants the
// bash and zsh scripts embed, so completion_test.go's
// TestCompletionFlags_MatchLiveFlagSets already covers fish's flags too --
// there is nothing fish-specific to duplicate a drift guard for.
const fishCompletionScript = `# rgit fish completion -- generated by ` + "`rgit completion fish`" + `.
# Load once:  rgit completion fish | source
# Persist:    rgit completion fish > ~/.config/fish/completions/rgit.fish

function __rgit_diff_flags; string split ' ' -- '` + rgitDiffFlags + `'; end
function __rgit_commit_flags; string split ' ' -- '` + rgitCommitFlags + `'; end
function __rgit_show_flags; string split ' ' -- '` + rgitShowFlags + `'; end
function __rgit_blame_flags; string split ' ' -- '` + rgitBlameFlags + `'; end
function __rgit_log_flags; string split ' ' -- '` + rgitLogFlags + `'; end
function __rgit_context_flags; string split ' ' -- '` + rgitContextFlags + `'; end
function __rgit_languages_flags; string split ' ' -- '` + rgitLanguagesFlags + `'; end
function __rgit_doctor_flags; string split ' ' -- '` + rgitDoctorFlags + `'; end
function __rgit_completion_flags; string split ' ' -- '` + rgitCompletionFlags + `'; end
function __rgit_symbols_flags; string split ' ' -- '` + rgitSymbolsFlags + `'; end

# Symbol candidates come from rgit symbols FILE (commit uses --for-commit).
function __rgit_symbols
    if test "$argv[2]" = commit
        rgit symbols --for-commit "$argv[1]" 2>/dev/null
    else
        rgit symbols "$argv[1]" 2>/dev/null
    end
end

function __rgit_complete
    # commandline -opc is every already-typed token including "rgit"
    # itself (tokens[1]) but never the one the cursor is still on -- unlike
    # bash's COMP_WORDS/zsh's words, which include it. That split is why
    # this walk differs from the bash/zsh scripts' own: a trailing,
    # unpaired "-C" (its argument is what cur is completing right now)
    # must not be mistaken for an already-consumed pair.
    set -l tokens (commandline -opc)
    set -l cur (commandline -ct)

    set -l i 2
    while test $i -le (count $tokens); and test "$tokens[$i]" = "-C"; and test (count $tokens) -ge (math $i + 1)
        set i (math $i + 2)
    end

    if test $i -le (count $tokens); and test "$tokens[$i]" = "-C"
        __fish_complete_directories $cur
        return
    end

    if test $i -gt (count $tokens)
        printf '%s\n' ` + rgitSubcommands + `
        return
    end

    set -l cmd $tokens[$i]
    set -l flags
    switch $cmd
        case diff
            set flags (__rgit_diff_flags)
        case commit
            set flags (__rgit_commit_flags)
        case show
            set flags (__rgit_show_flags)
        case blame
            set flags (__rgit_blame_flags)
        case log
            set flags (__rgit_log_flags)
        case context
            set flags (__rgit_context_flags)
        case languages
            set flags (__rgit_languages_flags)
        case doctor
            set flags (__rgit_doctor_flags)
        case symbols
            set flags (__rgit_symbols_flags)
        case completion
            set flags (__rgit_completion_flags)
            if test (count $tokens) -eq $i; and not string match -q -- '-*' $cur
                printf '%s\n' bash zsh fish pwsh
                return
            end
        case '*'
            return
    end

    if string match -q -- '-*' $cur
        printf '%s\n' $flags
        return
    end

    # Symbol completion only fires for diff, commit, show, blame, and log -- the
    # four commands that actually take a FILE:SYMBOL anchor, matching the
    # bash and zsh scripts' own restriction.
    if string match -q -- '*:*' $cur; and contains $cmd diff commit show blame log
        set -l file (string split -m1 -r ':' -- $cur)[1]
        for s in (__rgit_symbols "$file" "$cmd")
            printf '%s:%s\n' $file $s
        end
        return
    end

    # No flag, no colon: an ordinary path argument. -f below suppresses
    # fish's own automatic file completion for rgit entirely (it would
    # otherwise layer onto every candidate list above, including the
    # subcommand and flag ones), so it is requested explicitly here instead
    # -- the same fallback bash's "compgen -f" and zsh's "_files" give.
    __fish_complete_path $cur
end

complete -c rgit -f -a '(__rgit_complete)'
`

// pwshCompletionScript is a PowerShell 7 native completer. Native argument
// completers receive the command AST, the current word, and the cursor
// position; the AST supplies already-typed arguments while the current word
// preserves the token being completed.
const pwshCompletionScript = `# rgit pwsh completion -- generated by ` + "`rgit completion pwsh`" + `.
# Load once:  rgit completion pwsh | Invoke-Expression
# Persist:    Add the same command to your PowerShell profile

$rgitSubcommands = '` + rgitSubcommands + `'.Split(' ')
$rgitDiffFlags = '` + rgitDiffFlags + `'.Split(' ')
$rgitCommitFlags = '` + rgitCommitFlags + `'.Split(' ')
$rgitShowFlags = '` + rgitShowFlags + `'.Split(' ')
$rgitBlameFlags = '` + rgitBlameFlags + `'.Split(' ')
$rgitLogFlags = '` + rgitLogFlags + `'.Split(' ')
$rgitContextFlags = '` + rgitContextFlags + `'.Split(' ')
$rgitLanguagesFlags = '` + rgitLanguagesFlags + `'.Split(' ')
$rgitDoctorFlags = '` + rgitDoctorFlags + `'.Split(' ')
$rgitCompletionFlags = '` + rgitCompletionFlags + `'.Split(' ')
$rgitSymbolsFlags = '` + rgitSymbolsFlags + `'.Split(' ')

Register-ArgumentCompleter -Native -CommandName rgit -ScriptBlock {
    param($wordToComplete, $commandAst, $cursorPosition)

    $elements = @($commandAst.CommandElements)
    $arguments = [System.Collections.Generic.List[string]]::new()
    $currentIndex = -1

    for ($elementIndex = 1; $elementIndex -lt $elements.Count; $elementIndex++) {
        $element = $elements[$elementIndex]
        if ($element.Extent.StartOffset -gt $cursorPosition) {
            break
        }

        if ($element.Extent.StartOffset -le $cursorPosition -and $cursorPosition -le $element.Extent.EndOffset) {
            $currentIndex = $arguments.Count
            [void]$arguments.Add([string]$wordToComplete)
        } else {
            [void]$arguments.Add([string]$element.Extent.Text)
        }
    }

    if ($currentIndex -lt 0) {
        $currentIndex = $arguments.Count
        [void]$arguments.Add([string]$wordToComplete)
    }

    function Emit-Completion {
        param([string]$Text)

        if ($Text.StartsWith([string]$wordToComplete, [System.StringComparison]::OrdinalIgnoreCase)) {
            [System.Management.Automation.CompletionResult]::new(
                $Text,
                $Text,
                [System.Management.Automation.CompletionResultType]::ParameterValue,
                $Text)
        }
    }

    function Complete-Paths {
        try {
            $pathPrefix = Split-Path -Parent $wordToComplete
            foreach ($item in @(Get-ChildItem -Path ("{0}*" -f $wordToComplete) -Force -ErrorAction SilentlyContinue)) {
                $completionText = if ([string]::IsNullOrEmpty($pathPrefix)) {
                    $item.Name
                } else {
                    Join-Path -Path $pathPrefix -ChildPath $item.Name
                }
                Emit-Completion -Text $completionText
            }
        } catch {
            return
        }
    }

    $commandIndex = 0
    while ($commandIndex -lt $arguments.Count -and $arguments[$commandIndex] -eq '-C') {
        if ($currentIndex -eq $commandIndex) {
            break
        }
        if ($currentIndex -eq ($commandIndex + 1)) {
            Complete-Paths
            return
        }
        $commandIndex += 2
    }

    if ($commandIndex -ge $arguments.Count) {
        return
    }

    $command = $arguments[$commandIndex]
    if ($currentIndex -le $commandIndex) {
        foreach ($subcommand in $rgitSubcommands) {
            Emit-Completion -Text $subcommand
        }
        return
    }

    $flags = @()
    switch ($command) {
        diff { $flags = $rgitDiffFlags }
        commit { $flags = $rgitCommitFlags }
        show { $flags = $rgitShowFlags }
        blame { $flags = $rgitBlameFlags }
        log { $flags = $rgitLogFlags }
        context { $flags = $rgitContextFlags }
        languages { $flags = $rgitLanguagesFlags }
        doctor { $flags = $rgitDoctorFlags }
        symbols { $flags = $rgitSymbolsFlags }
        completion {
            $flags = $rgitCompletionFlags
            if ($currentIndex -eq ($commandIndex + 1) -and -not $wordToComplete.StartsWith('-')) {
                foreach ($shell in @('bash', 'zsh', 'fish', 'pwsh')) {
                    Emit-Completion -Text $shell
                }
                return
            }
        }
        default { return }
    }

    if ($wordToComplete.StartsWith('-')) {
        foreach ($flag in $flags) {
            Emit-Completion -Text $flag
        }
        return
    }

    $colon = $wordToComplete.LastIndexOf(':')
    if ($colon -ge 0 -and $command -in @('diff', 'commit', 'show', 'blame', 'log')) {
        $file = $wordToComplete.Substring(0, $colon)
        $symbolArgs = @('symbols')
        if ($command -eq 'commit') {
            $symbolArgs += '--for-commit'
        }
        $symbolArgs += $file

        try {
            $symbols = @(& rgit @symbolArgs 2>$null)
        } catch {
            return
        }

        foreach ($symbol in $symbols) {
            Emit-Completion -Text ('{0}:{1}' -f $file, $symbol)
        }
        return
    }

    Complete-Paths
}
`

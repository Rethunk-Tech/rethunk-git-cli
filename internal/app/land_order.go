package app

import (
	"regexp"
	"strings"
)

// pinRE is a git SHA pin or a Go pseudo-version's commit suffix: 7 to 40 hex digits.
var pinRE = regexp.MustCompile(`[0-9a-f]{7,40}`)

// pinDeps maps each repo to the other repos in the run whose unpushed commits its own unpushed
// package.json or go.mod change pins, so a consumer never reaches the remote ahead of the SHA it names.
func pinDeps(repos []string, d landDeps) map[string][]string {
	ahead := make(map[string][]string, len(repos))
	for _, r := range repos {
		if out, err := d.git.run(r, "rev-list", "@{u}..HEAD"); err == nil {
			ahead[r] = strings.Fields(out)
		}
	}
	deps := map[string][]string{}
	for _, r := range repos {
		if len(ahead[r]) == 0 {
			continue
		}
		diff, err := d.git.run(r, "diff", "--no-color", "-U0", "@{u}..HEAD", "--", "package.json", "go.mod")
		if err != nil || diff == "" {
			continue
		}
		var pins []string
		for line := range strings.SplitSeq(strings.ReplaceAll(diff, "\x00", "\n"), "\n") {
			if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
				pins = append(pins, pinRE.FindAllString(line, -1)...)
			}
		}
		for _, other := range repos {
			if other != r && pinsAny(pins, ahead[other]) {
				deps[r] = append(deps[r], other)
			}
		}
	}
	return deps
}

func pinsAny(pins, shas []string) bool {
	for _, pin := range pins {
		for _, sha := range shas {
			if strings.HasPrefix(sha, pin) {
				return true
			}
		}
	}
	return false
}

// orderByPins keeps the order given except that a repo follows every repo it pins; a cycle keeps its given order.
func orderByPins(ordered []string, deps map[string][]string) []string {
	if len(deps) == 0 {
		return ordered
	}
	done := make(map[string]bool, len(ordered))
	out := make([]string, 0, len(ordered))
	pending := ordered
	for len(pending) > 0 {
		var next []string
		for _, r := range pending {
			ready := true
			for _, dep := range deps[r] {
				ready = ready && done[dep]
			}
			if ready {
				done[r] = true
				out = append(out, r)
			} else {
				next = append(next, r)
			}
		}
		if len(next) == len(pending) {
			return append(out, next...)
		}
		pending = next
	}
	return out
}

// redDep is the first of deps that went red in this run, or "".
func redDep(deps []string, red map[string]bool) string {
	for _, dep := range deps {
		if red[dep] {
			return dep
		}
	}
	return ""
}

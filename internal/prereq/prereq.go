// Package prereq is the presence-check mechanism internal/app's `rgit
// doctor` and cmd/rgit-install both need: probe a binary on PATH, and
// format the result as the "[ok/MISSING] name   detail" line both commands
// print. It lives under internal/ rather than inside cmd/rgit-install
// itself because cmd/rgit-install is package main and cannot be imported --
// the dependency direction is installer -> internal/prereq, never the
// reverse.
//
// Each caller still decides which checks to run and what to say when one
// fails: doctor reports *run-time* facts (what an already-built rgit can
// reach right now), while cmd/rgit-install additionally reports *build-time*
// ones (go toolchain, CGO_ENABLED, a C compiler) that are meaningless for a
// binary that already exists. Only the mechanism and the line format are
// shared here; the check list and its wording stay caller-owned.
package prereq

import (
	"fmt"
	"io"
	"os/exec"
)

// Check is one probed fact: a name, whether it passed, and a human-readable
// detail -- the resolved path when found, or a caller-supplied note when
// not.
type Check struct {
	Name   string
	OK     bool
	Detail string
}

// LookPath probes bin on PATH and reports the result as a Check named name.
// missingDetail becomes Detail when bin is not found; pass "" for a check
// that reports nothing beyond MISSING on failure (both callers' git and
// go-toolchain checks read this way).
func LookPath(name, bin, missingDetail string) Check {
	path, err := exec.LookPath(bin)
	if err != nil {
		return Check{Name: name, OK: false, Detail: missingDetail}
	}
	return Check{Name: name, OK: true, Detail: path}
}

// minNameWidth is the floor Width reports for a group whose names are all
// shorter than it. cmd/rgit-install's five names are, and letting the detail
// column slide left to hug them would churn a layout that already reads
// well for no gain.
const minNameWidth = 24

// statusWidth is len("[MISSING]"), the wider of the two status spellings.
// Padding to it is what keeps the name column from shifting between an ok
// row and a missing one.
const statusWidth = 9

// Width reports the name-column width that aligns every check in checks:
// the longest name, floored at minNameWidth. Callers measure once over
// every check they are about to print -- doctor spans its own sections so
// the whole report shares one detail column, rather than each section
// aligning only against itself.
func Width(checks ...Check) int {
	width := minNameWidth
	for _, c := range checks {
		if len(c.Name) > width {
			width = len(c.Name)
		}
	}
	return width
}

// Print renders c as "  [ok/MISSING] name   detail\n" at the given name
// column width, the shared line shape doctor and cmd/rgit-install both
// print, kept byte-for-byte identical between them on purpose. Pass a width
// from Width over the whole group; a name longer than width is never
// truncated, it just pushes its own detail right.
func Print(w io.Writer, width int, c Check) {
	status := "ok"
	if !c.OK {
		status = "MISSING"
	}
	fmt.Fprintf(w, "  %-*s %-*s %s\n", statusWidth, "["+status+"]", width, c.Name, c.Detail)
}

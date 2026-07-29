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
// go-toolchain checks read this way today).
func LookPath(name, bin, missingDetail string) Check {
	path, err := exec.LookPath(bin)
	if err != nil {
		return Check{Name: name, OK: false, Detail: missingDetail}
	}
	return Check{Name: name, OK: true, Detail: path}
}

// Print renders c as "  [ok/MISSING] name   detail\n" -- the exact line
// shape both doctor and cmd/rgit-install already printed before this
// package existed, preserved byte-for-byte on purpose (that agreement was
// this extraction's whole reason to exist).
func Print(w io.Writer, c Check) {
	status := "ok"
	if !c.OK {
		status = "MISSING"
	}
	fmt.Fprintf(w, "  [%s] %-24s %s\n", status, c.Name, c.Detail)
}

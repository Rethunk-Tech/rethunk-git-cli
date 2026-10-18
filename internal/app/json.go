package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/Rethunk-Tech/rethunk-git-cli/internal/exitcode"
)

// jsonSchema is the envelope version every --json object carries as its
// first field. Bump it when a payload key is renamed or removed; adding a
// field inside an existing payload does not.
const jsonSchema = 1

type jsonFlagKey struct{}

func withJSONFlag(ctx context.Context, on bool) context.Context {
	if !on {
		return ctx
	}
	return context.WithValue(ctx, jsonFlagKey{}, true)
}

func jsonFlag(ctx context.Context) bool {
	on, _ := ctx.Value(jsonFlagKey{}).(bool)
	return on
}

// takeJSON pulls every --json token that is not after "--" out of args so
// Run can treat it as a global flag. A path literally named --json still
// works when it follows "--".
func takeJSON(args []string) (rest []string, asJSON bool) {
	rest = make([]string, 0, len(args))
	afterSep := false
	for _, a := range args {
		if a == "--" {
			afterSep = true
			rest = append(rest, a)
			continue
		}
		if !afterSep && a == "--json" {
			asJSON = true
			continue
		}
		rest = append(rest, a)
	}
	return rest, asJSON
}

func refuseUnsupportedJSON(asJSON bool, cmd string, stderr io.Writer) exitcode.Code {
	if !asJSON {
		return exitcode.Success
	}
	fmt.Fprintf(stderr, "rgit: %s does not support --json\n", cmd)
	fmt.Fprintln(stderr, usageLine)
	return exitcode.InvalidUsage
}

func encodeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

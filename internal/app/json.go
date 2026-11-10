package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/exitcode"
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

// takeJSON pulls a --json out of the global region of args -- the part
// before the command name -- so Run can treat it as a global flag. A --json
// after the command belongs to that command's own parser, which is the only
// thing that can tell a flag from a flag's value: `rgit commit -m --json`
// commits the subject "--json", exactly as plain git would.
//
// -C is skipped together with its argument, so `-C --json` names a directory
// rather than setting the flag.
func takeJSON(args []string) (rest []string, asJSON bool) {
	rest = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--json":
			asJSON = true
		case a == "-C":
			rest = append(rest, a)
			if i+1 < len(args) {
				i++
				rest = append(rest, args[i])
			}
		case a == "--" || !strings.HasPrefix(a, "-"):
			// The command (or the separator in front of it): everything from
			// here on is the subcommand's own to parse.
			return append(rest, args[i:]...), asJSON
		default:
			rest = append(rest, a)
		}
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

// hasJSONFlag reports whether args names --json before any "--", for the
// commands that refuse it: their own surfaces have no flag table to parse it
// with, and a caller who put it after the command still gets the same
// refusal and the same message as one who put it before.
func hasJSONFlag(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "--json" {
			return true
		}
	}
	return false
}

func encodeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

package exitcode

import "testing"

// TestCodes_MatchDocumentedTable pins every named constant's numeric value
// against docs/CODES.md's own Exit codes table -- the one place this
// package's own doc comment says a code's meaning is specified. Nothing
// else in this package has any test at all: these look like plain int
// constants with nothing structural forcing them to agree with the table,
// so an inserted code shifting every later one, or a copy-paste typo,
// would otherwise surface as a wrong exit status with no compiler error to
// catch it.
func TestCodes_MatchDocumentedTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		got  Code
		want Code
	}{
		{"Success", Success, 0},
		{"AnchorUnresolvable", AnchorUnresolvable, 3},
		{"AnchorAmbiguous", AnchorAmbiguous, 4},
		{"ContradictoryAnchors", ContradictoryAnchors, 5},
		{"ExtentMismatch", ExtentMismatch, 6},
		{"PathRefused", PathRefused, 7},
		{"PushFailed", PushFailed, 8},
		{"UnsupportedLanguage", UnsupportedLanguage, 9},
		{"SpecialPathRefused", SpecialPathRefused, 10},
		{"NothingToCommit", NothingToCommit, 11},
		{"StructuredDataAnchorRefused", StructuredDataAnchorRefused, 12},
		{"GitFailure", GitFailure, 128},
		{"InvalidUsage", InvalidUsage, 129},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %d; want %d (docs/CODES.md § Exit codes)", tt.name, tt.got, tt.want)
		}
	}
}

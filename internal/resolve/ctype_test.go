package resolve

import "testing"

// The markdown, bash, and zsh external scanners hand the code point after a
// digit, '{', or '.' to C's isdigit, whose table lookup faults on a large one;
// a stray tag character in a README must not kill the process.
func TestOpen_LargeCodePointAfterDigitDoesNotCrash(t *testing.T) {
	t.Parallel()
	high := string(rune(0x10FFFF))
	for ext, src := range map[string]string{
		".md":  "# Title\n\n1" + high + "\n",
		".sh":  "#!/bin/sh\necho {" + high + " {1.." + high + "\nf() { :; }\n",
		".zsh": "echo {" + high + "\nf() { :; }\n",
	} {
		lang, ok := ForExtension(ext)
		if !ok {
			t.Fatalf("no grammar for %s", ext)
		}
		decls, err := DeclExtents(lang, []byte(src))
		if err != nil {
			t.Fatalf("%s: %v", ext, err)
		}
		if len(decls) == 0 {
			t.Errorf("%s: no declarations found in %q", ext, src)
		}
	}
}

package synth

import (
	"testing"

	"github.com/Rethunk-Tech/rethunk-git-cli/v2/internal/resolve"
)

// FuzzIdentitySplice drives TestApplyEdits_IdentitySpliceIsByteIdentical's
// property over arbitrary source: replacing every declaration with its own
// bytes must give back the input, whatever the grammar made of it.
func FuzzIdentitySplice(f *testing.F) {
	f.Add(uint8(0), []byte("package p\n\ntype T struct {\n\tA, B int\n}\n\nfunc (T) M() {}\n"))
	f.Add(uint8(1), []byte("export class K {\n  get x() { return 1 }\n  set x(v) {}\n}\nexport const a = 1, b = 2;\n"))
	f.Add(uint8(2), []byte("class C:\n    @property\n    def x(self):\n        return 1\n\ndef f():\n    def g(): pass\n"))
	exts := []string{".go", ".ts", ".py", ".tsx", ".rs", ".sh", ".css", ".md"}
	f.Fuzz(func(t *testing.T, sel uint8, src []byte) {
		lang, ok := resolve.ForExtension(exts[int(sel)%len(exts)])
		if !ok || len(src) > 1<<14 {
			return
		}
		decls, err := resolve.DeclExtents(lang, src)
		if err != nil {
			return
		}
		ops := make([]editOp, 0, len(decls))
		for _, d := range decls {
			if d.Extent.Start > d.Extent.End || d.Extent.End > uint(len(src)) {
				t.Fatalf("%s extent [%d,%d) outside %d-byte source", d.Anchor, d.Extent.Start, d.Extent.End, len(src))
			}
			ops = append(ops, editOp{kind: editReplace, start: d.Extent.Start, end: d.Extent.End, text: src[d.Extent.Start:d.Extent.End]})
		}
		if got := applyEdits(src, ops); string(got) != string(src) {
			t.Fatalf("identity splice of %d declarations changed the source\nwant %q\ngot  %q", len(decls), src, got)
		}
	})
}

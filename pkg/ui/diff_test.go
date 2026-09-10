package ui

import (
	"fmt"
	"testing"
)

func TestDiff_KeepsRemovesAndAddsInOrder(t *testing.T) {
	got := Diff([]string{"a", "b", "c", "d"}, []string{"a", "x", "d", "e"})
	want := "=a -b -c +x =d +e"
	if s := script(got); s != want {
		t.Errorf("Diff = %q, want %q", s, want)
	}
	if s := script(Diff(nil, []string{"n"})); s != "+n" {
		t.Errorf("Diff from nothing = %q", s)
	}
	if s := script(Diff([]string{"same"}, []string{"same"})); s != "=same" {
		t.Errorf("Diff of equals = %q", s)
	}
}

// A region big enough to defeat the matching table still comes back, and
// says so in the direction that overstates rather than hides.
func TestDiff_HugeRegionFallsBackWholesale(t *testing.T) {
	n := 400 // 400*400 = 160k cells, past maxDiffCells
	old := make([]string, n)
	updated := make([]string, n)
	for i := range old {
		old[i] = fmt.Sprintf("old %d", i)
		updated[i] = fmt.Sprintf("new %d", i)
	}
	changed := 0
	for _, e := range Diff(old, updated) {
		if e.Kind != '=' {
			changed++
		}
	}
	if changed != 2*n {
		t.Errorf("%d changed, want the whole region reported as %d", changed, 2*n)
	}
}

func script(edits []Edit) string {
	s := ""
	for i, e := range edits {
		if i > 0 {
			s += " "
		}
		s += string(e.Kind) + e.Text
	}
	return s
}

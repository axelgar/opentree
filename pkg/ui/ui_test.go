package ui

import (
	"strings"
	"testing"
)

func TestTruncate(t *testing.T) {
	if got := Truncate("short", 40); got != "short" {
		t.Errorf("Truncate() = %q, want it untouched", got)
	}
	got := Truncate(strings.Repeat("x", 50), 10)
	if len([]rune(got)) != 10 || !strings.HasSuffix(got, "…") {
		t.Errorf("Truncate() = %q, want 10 runes ending in an ellipsis", got)
	}

	// The narrow widths are where the drifted copies disagreed: one returned
	// the string untouched, overflowing the box it was being fitted into.
	if got := Truncate("wide", 1); got != "…" {
		t.Errorf("Truncate(width=1) = %q, want the ellipsis alone", got)
	}
	if got := Truncate("wide", 0); got != "" {
		t.Errorf("Truncate(width=0) = %q, want empty", got)
	}
}

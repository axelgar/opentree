package ui

import (
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
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

// TestPalette_EveryRoleIsReadableOnBothBackgrounds is the guard on the defect
// this palette exists for: the agent's own replies were #EEE with no
// background set, which is 1.16:1 against white. On a light terminal the
// answer you were waiting for was invisible.
//
// WCAG AA for body text is 4.5:1. Every role is checked against the
// background it is meant to be read on — the Light value against white, the
// Dark value against black — because a role that adapts is only worth having
// if both halves are legible.
func TestPalette_EveryRoleIsReadableOnBothBackgrounds(t *testing.T) {
	// Structural colours are not text: a border and a rule are judged by
	// whether the shape reads as a shape, and holding them to a text ratio
	// would make every box shout.
	const structuralMin = 1.4

	for _, tc := range []struct {
		name string
		c    lipgloss.AdaptiveColor
		min  float64
	}{
		{"Body", Body, 4.5},
		{"BodyStrong", BodyStrong, 4.5},
		{"Muted", Muted, 4.5},
		{"Meta", Meta, 3.0},
		{"Dim", Dim, 3.0},
		{"Faint", Faint, 3.0},
		{"Ghost", Ghost, 3.0},
		{"Whisper", Whisper, 2.5},
		{"Accent", Accent, 4.5},
		{"Success", Success, 4.5},
		{"Warn", Warn, 4.5},
		{"WarnFile", WarnFile, 4.0},
		{"Info", Info, 4.5},
		{"Toast", Toast, 4.5},
		{"ToolOutput", ToolOutput, 4.0},
		{"Border", Border, structuralMin},
		{"Divider", Divider, structuralMin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := contrast(tc.c.Light, "#FFFFFF"); got < tc.min {
				t.Errorf("Light %s on white is %.2f:1, want at least %.2f:1", tc.c.Light, got, tc.min)
			}
			if got := contrast(tc.c.Dark, "#000000"); got < tc.min {
				t.Errorf("Dark %s on black is %.2f:1, want at least %.2f:1", tc.c.Dark, got, tc.min)
			}
		})
	}
}

// Band is the tint behind your own messages. It is the one entry that must NOT
// contrast with the background — it is a shade off it, on whichever side of it
// the background is — so it is checked the other way round: readable text on
// top of it.
func TestPalette_BandStaysBehindItsText(t *testing.T) {
	if got := contrast(Body.Light, Band.Light); got < 4.5 {
		t.Errorf("light body text on the light band is %.2f:1, want at least 4.5:1", got)
	}
	if got := contrast(Body.Dark, Band.Dark); got < 4.5 {
		t.Errorf("dark body text on the dark band is %.2f:1, want at least 4.5:1", got)
	}
}

// contrast is the WCAG 2.1 ratio between two sRGB hex colours, which is what
// "readable" means here rather than an eyeball.
func contrast(a, b string) float64 {
	la, lb := relativeLuminance(a), relativeLuminance(b)
	hi, lo := math.Max(la, lb), math.Min(la, lb)
	return (hi + 0.05) / (lo + 0.05)
}

func relativeLuminance(hex string) float64 {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) == 3 { // #EEE and #EEEEEE are the same colour
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	}
	channel := func(i int) float64 {
		v, err := strconv.ParseUint(hex[i:i+2], 16, 8)
		if err != nil {
			return 0
		}
		c := float64(v) / 255
		if c <= 0.04045 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(0) + 0.7152*channel(2) + 0.0722*channel(4)
}

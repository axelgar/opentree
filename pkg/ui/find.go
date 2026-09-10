package ui

// Finding text in rendered rows, and painting the finds. Both programs search
// what is on screen rather than what it was rendered from — the reader is
// looking for the thing they can see, and the highlight has to land on it —
// so both need the same two moves: measure a match past the colour codes, and
// restyle a stretch of a row without disturbing the rest of it.

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Match is one occurrence: the row, and the cells it covers.
type Match struct{ Line, Col, Width int }

// colEnd is a column past the end of any row.
const colEnd = 1 << 30

// FindMatches is every occurrence of query in the rows, colours stripped,
// case folded. The fold is applied to the row before it is measured too, so
// the columns are measured on the text the index was found in.
func FindMatches(rows []string, query string) []Match {
	q := strings.ToLower(query)
	if q == "" {
		return nil
	}
	var out []Match
	for i, row := range rows {
		plain := strings.ToLower(ansi.Strip(row))
		from := 0
		for {
			at := strings.Index(plain[from:], q)
			if at < 0 {
				break
			}
			at += from
			out = append(out, Match{
				Line:  i,
				Col:   ansi.StringWidth(plain[:at]),
				Width: max(ansi.StringWidth(plain[at:at+len(q)]), 1),
			})
			from = at + len(q)
		}
	}
	return out
}

// Paint restyles the cells [col, col+width) of a row, whatever colour they
// had: the stretch is cut out, stripped, and set again in the style, with
// the row's own styling resumed on either side. A stretch with no text in it
// leaves the row alone.
func Paint(row string, col, width int, style lipgloss.Style) string {
	mid := ansi.Strip(ansi.Cut(row, col, col+width))
	if mid == "" {
		return row
	}
	return ansi.Cut(row, 0, col) + style.Render(mid) + ansi.Cut(row, col+width, colEnd)
}

// PaintMatches marks every match on its row, the current one in cur and the
// rest in other. Matches on one row are painted from the right, so the
// columns of the ones still to paint are the columns they were measured at.
// A match's column is shifted by offset, for rows that grew a prefix after
// they were searched.
func PaintMatches(rows []string, matches []Match, current, offset int, cur, other lipgloss.Style) []string {
	if len(matches) == 0 {
		return rows
	}
	out := make([]string, len(rows))
	copy(out, rows)
	for i := len(matches) - 1; i >= 0; i-- {
		mt := matches[i]
		if mt.Line < 0 || mt.Line >= len(out) {
			continue
		}
		style := other
		if i == current {
			style = cur
		}
		out[mt.Line] = Paint(out[mt.Line], mt.Col+offset, mt.Width, style)
	}
	return out
}

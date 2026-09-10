package ui

// Matching two sequences — lines of a region for the chat, words of a line for
// the diff viewer — so what changed can be told from what merely moved past.

// Edit is one step of an edit script: a kept element ('='), one only the old
// side had ('-'), one only the new side has ('+').
type Edit struct {
	Kind byte
	Text string
}

// maxDiffCells bounds the matching table. Past it the region already dwarfs
// anything that will be shown, so exact matching stops paying for itself.
//
// ponytail: the fallback reports the whole region as changed. It overstates a
// big edit, which is the harmless direction.
const maxDiffCells = 1 << 16

// Diff is the edit script from old to updated: the longest common subsequence
// kept, everything else removed or added, in order.
func Diff(old, updated []string) []Edit {
	var out []Edit
	// Shared ends are kept without a table.
	for len(old) > 0 && len(updated) > 0 && old[0] == updated[0] {
		out = append(out, Edit{'=', old[0]})
		old, updated = old[1:], updated[1:]
	}
	var tail []Edit
	for len(old) > 0 && len(updated) > 0 && old[len(old)-1] == updated[len(updated)-1] {
		tail = append([]Edit{{'=', old[len(old)-1]}}, tail...)
		old, updated = old[:len(old)-1], updated[:len(updated)-1]
	}
	if len(old)*len(updated) > maxDiffCells {
		out = append(out, edits('-', old)...)
		out = append(out, edits('+', updated)...)
		return append(out, tail...)
	}

	// common[i][j] is the length of the longest common subsequence of old[i:]
	// and updated[j:], which is what says whether an element was replaced or
	// merely moved past.
	common := make([][]int, len(old)+1)
	for i := range common {
		common[i] = make([]int, len(updated)+1)
	}
	for i := len(old) - 1; i >= 0; i-- {
		for j := len(updated) - 1; j >= 0; j-- {
			if old[i] == updated[j] {
				common[i][j] = common[i+1][j+1] + 1
			} else {
				common[i][j] = max(common[i+1][j], common[i][j+1])
			}
		}
	}

	i, j := 0, 0
	for i < len(old) && j < len(updated) {
		switch {
		case old[i] == updated[j]:
			out = append(out, Edit{'=', old[i]})
			i, j = i+1, j+1
		case common[i+1][j] >= common[i][j+1]:
			out = append(out, Edit{'-', old[i]})
			i++
		default:
			out = append(out, Edit{'+', updated[j]})
			j++
		}
	}
	out = append(out, edits('-', old[i:])...)
	out = append(out, edits('+', updated[j:])...)
	return append(out, tail...)
}

func edits(kind byte, texts []string) []Edit {
	out := make([]Edit, len(texts))
	for i, t := range texts {
		out[i] = Edit{kind, t}
	}
	return out
}

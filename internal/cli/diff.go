package cli

import (
	"fmt"
	"strings"
)

// lineDiff returns a unified-style line diff of a and b: "-" removed, "+"
// added, " " unchanged within ctx lines of a change, "@@ line N" before each
// hunk. Small inputs only (environment files): it is O(len(a)*len(b)).
func lineDiff(a, b string, ctx int) []string {
	x, y := splitLines(a), splitLines(b)
	// lcs[i][j]: length of the longest common subsequence of x[i:] and y[j:].
	lcs := make([][]int, len(x)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(y)+1)
	}
	for i := len(x) - 1; i >= 0; i-- {
		for j := len(y) - 1; j >= 0; j-- {
			if x[i] == y[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	type op struct {
		kind byte
		text string
		line int // line number in b (1-based) of the op, for hunk headers
	}
	var ops []op
	i, j := 0, 0
	for i < len(x) || j < len(y) {
		switch {
		case i < len(x) && j < len(y) && x[i] == y[j]:
			ops = append(ops, op{' ', x[i], j + 1})
			i, j = i+1, j+1
		case i < len(x) && (j == len(y) || lcs[i+1][j] >= lcs[i][j+1]):
			ops = append(ops, op{'-', x[i], j + 1})
			i++
		default:
			ops = append(ops, op{'+', y[j], j + 1})
			j++
		}
	}
	keep := make([]bool, len(ops))
	for n, o := range ops {
		if o.kind != ' ' {
			for m := max(0, n-ctx); m <= min(len(ops)-1, n+ctx); m++ {
				keep[m] = true
			}
		}
	}
	var out []string
	prev := -2
	for n, o := range ops {
		if !keep[n] {
			continue
		}
		if n != prev+1 {
			out = append(out, fmt.Sprintf("@@ line %d", o.line))
		}
		out = append(out, string(o.kind)+" "+o.text)
		prev = n
	}
	return out
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

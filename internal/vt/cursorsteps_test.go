package vt

import "testing"

// TestCursorSteps: the characters between the cursor and a cell, along
// the cursor's line only, lines that wrapped being one, a wide character
// one, and no further than the end of the text.
func TestCursorSteps(t *testing.T) {
	type step struct {
		row, col, want int
		ok             bool
	}
	cases := []struct {
		name   string
		output string
		steps  []step
	}{
		{"one row", "$ hello", []step{
			{0, 2, -5, true}, {0, 6, -1, true}, {0, 7, 0, true},
			{0, 9, 0, true}, // past the text: its end
			{1, 0, 0, false}, {-1, 0, 0, false}, {4, 0, 0, false},
		}},
		{"another line", "out\r\n$ x", []step{
			{1, 2, -1, true}, {0, 1, 0, false},
		}},
		{"wrapped", "$ abcdefghijkl\r", []step{
			{0, 5, -5, true}, {1, 3, 3, true}, {0, 0, -10, true},
			{1, 9, 4, true}, {2, 0, 0, false},
		}},
		{"wide", "$ 中文ab", []step{
			{0, 2, -4, true},
			{0, 3, -4, true}, // the right half of 中
			{0, 4, -3, true}, {0, 7, -1, true},
		}},
		{"wide character that did not fit", "$ abcdefg中", []step{
			{0, 8, -2, true}, // g, past the blank left at the margin
			{0, 9, -1, true}, {1, 0, -1, true}, {1, 1, -1, true},
		}},
		{"typed blanks", "$ ab  ", []step{
			{0, 9, 0, true}, {0, 3, -3, true},
		}},
	}
	for _, c := range cases {
		term := New(10, 4)
		term.WriteString(c.output)
		s := term.Snapshot(0)
		for _, st := range c.steps {
			got, ok := s.CursorSteps(st.row, st.col)
			if got != st.want || ok != st.ok {
				t.Errorf("%s: steps to row %d col %d = %d, %v; want %d, %v", c.name, st.row, st.col, got, ok, st.want, st.ok)
			}
		}
	}
}

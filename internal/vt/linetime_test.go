package vt

import (
	"slices"
	"testing"
	"time"
)

// clocked is a terminal whose lines are stamped with *at.
func clocked(cols, rows int) (*Terminal, *int64) {
	t := New(cols, rows)
	at := new(int64)
	*at = 1000
	t.now = func() time.Time { return time.Unix(*at, 0) }
	return t, at
}

func times(s Snapshot) []int64 {
	out := make([]int64, len(s.Lines))
	for i, l := range s.Lines {
		out[i] = l.Time
	}
	return out
}

func TestLineTimeIsWhenOutputFirstReachedIt(t *testing.T) {
	term, at := clocked(20, 4)
	if got := times(term.Snapshot(0)); got[0] != 0 {
		t.Fatalf("a line no output reached has a time: %v", got)
	}
	term.WriteString("one")
	*at = 1005
	// Written to again, overwritten by a carriage return: it keeps its
	// first time. An empty line gets one as a line feed leaves it; the
	// line the feed reaches waits for its output.
	term.WriteString(" more\rONE\r\n\r\n")
	*at = 1010
	term.WriteString("three")
	if got, want := times(term.Snapshot(0)), []int64{1000, 1005, 1010, 0}; !slices.Equal(got, want) {
		t.Fatalf("times %v, want %v", got, want)
	}
}

func TestLineTimeFollowsTheLineIntoTheScrollback(t *testing.T) {
	term, at := clocked(10, 2)
	for i := range 5 {
		*at = int64(2000 + i)
		term.WriteString("line\r\n")
	}
	s := term.Snapshot(4)
	if got, want := times(s), []int64{2000, 2001}; !slices.Equal(got, want) {
		t.Fatalf("scrollback times %v, want %v", got, want)
	}
	// The screen erased: its lines are new and have none until output
	// reaches them again; the scrollback keeps its own.
	*at = 3000
	term.WriteString("\x1b[H\x1b[2J")
	if got := times(term.Snapshot(0)); !slices.Equal(got, []int64{0, 0}) {
		t.Fatalf("erased screen times %v", got)
	}
	term.WriteString("x")
	if got := times(term.Snapshot(0)); !slices.Equal(got, []int64{3000, 0}) {
		t.Fatalf("after printing %v", got)
	}
	if got := times(term.Snapshot(5)); got[0] != 2000 {
		t.Fatalf("scrollback lost its times: %v", got)
	}
}

func TestLineTimeSurvivesReflow(t *testing.T) {
	term, at := clocked(10, 4)
	term.WriteString("abcdefghijklmno\r\n") // wraps onto a second row
	*at = 1001
	term.WriteString("short")
	if got := times(term.Snapshot(0)); !slices.Equal(got, []int64{1000, 1000, 1001, 0}) {
		t.Fatalf("before resize %v", got)
	}
	// Wide enough to join the wrapped line: one row, one time.
	term.Resize(20, 4)
	if got := times(term.Snapshot(0)); !slices.Equal(got, []int64{1000, 1001, 0, 0}) {
		t.Fatalf("widened %v", got)
	}
	// Narrower: the pieces it wraps into keep the time.
	term.Resize(5, 4)
	if got := times(term.Snapshot(0)); !slices.Equal(got, []int64{1000, 1000, 1000, 1001}) {
		t.Fatalf("narrowed %v", got)
	}
}

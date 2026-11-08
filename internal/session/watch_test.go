package session

import (
	"strings"
	"sync"
	"testing"
)

// TestWatch: any number of watchers see what the session prints, beside
// the output tap, until each stops.
func TestWatch(t *testing.T) {
	f := newFakeStream()
	s := New(Config{}, Options{Open: opener(f)}, 80, 24)
	defer s.Close()
	s.Start()
	waitState(t, s, Connected)
	var mu sync.Mutex
	var a, b, tap strings.Builder
	keep := func(sb *strings.Builder) func([]byte) {
		return func(p []byte) {
			mu.Lock()
			sb.Write(p)
			mu.Unlock()
		}
	}
	s.SetOutputTap(keep(&tap))
	stopA := s.Watch(keep(&a))
	stopB := s.Watch(keep(&b))
	got := func(sb *strings.Builder) string {
		mu.Lock()
		defer mu.Unlock()
		return sb.String()
	}
	f.say("one ")
	waitFor(t, "both watch", func() bool { return got(&a) == "one " && got(&b) == "one " })
	stopA()
	stopA()
	f.say("two")
	waitFor(t, "b and the tap", func() bool { return got(&b) == "one two" && got(&tap) == "one two" })
	if got(&a) != "one " {
		t.Fatalf("a stopped but got %q", got(&a))
	}
	stopB()
}

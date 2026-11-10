package sftpsvc

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/sshtest"
)

func shortPagerTimeout(t *testing.T) {
	old := pagerReplyTimeout
	pagerReplyTimeout = 100 * time.Millisecond
	t.Cleanup(func() { pagerReplyTimeout = old })
}

// A server that stops answering partway through a paged listing: once
// no reply came within pagerReplyTimeout the pager gives up, and the
// folder is read whole when the server answers again.
func TestPagerTimeoutFallsBack(t *testing.T) {
	shortPagerTimeout(t)
	ffs := &sshtest.FakeFS{
		Dirs: map[string][]sshtest.FakeEntry{"/": {{Name: "big", Dir: true}}},
		Many: func(p string) (int, bool) { return 350, p == "/big" },
	}
	c := connectFake(t, ffs)
	t.Cleanup(ffs.Resume)
	var names atomic.Int32
	testPagerRecv = func(typ byte, b []byte) (byte, []byte, error) {
		if typ == fxpName && names.Add(1) == 1 {
			ffs.Stop()
			time.AfterFunc(10*pagerReplyTimeout, ffs.Resume)
		}
		return typ, b, nil
	}
	t.Cleanup(func() { testPagerRecv = nil })
	start := time.Now()
	items, _, err := c.PlanDownload(context.Background(), []string{"/big"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 351 {
		t.Fatalf("%d items", len(items))
	}
	if n := names.Load(); n != 1 {
		t.Fatalf("the pager read %d replies of names; it should have given up after the first", n)
	}
	if d := time.Since(start); d < 10*pagerReplyTimeout {
		t.Fatalf("planned in %v: before the server answered again?", d)
	}
}

// The whole read the plan falls back to ends when the plan is canceled,
// the server still silent; so does a plan without a pager, and one the
// server stopped answering before any listing.
func TestPlanCanceledWhileServerSilent(t *testing.T) {
	shortPagerTimeout(t)
	for _, tc := range []struct {
		name   string
		paged  bool
		listed bool // stops when listing; else when first asked about the folder
	}{
		{"paged", true, true},
		{"whole", false, true},
		{"stat", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.paged {
				withoutPager(t)
			}
			var ffs *sshtest.FakeFS
			ffs = &sshtest.FakeFS{
				Dirs: map[string][]sshtest.FakeEntry{"/": {{Name: "big", Dir: true}}},
				Many: func(p string) (int, bool) { return 350, p == "/big" },
			}
			if tc.listed {
				var once sync.Once // not again after Resume
				ffs.Listing = func(p string) {
					if p == "/big" {
						once.Do(ffs.Stop)
					}
				}
			}
			c := connectFake(t, ffs)
			t.Cleanup(ffs.Resume)
			if !tc.listed {
				ffs.Stop()
			}
			ctx, cancel := context.WithCancel(context.Background())
			wait := 5 * pagerReplyTimeout
			// Timed from before the timer, so the cancel is never early.
			start := time.Now()
			time.AfterFunc(wait, cancel)
			done := make(chan error, 1)
			go func() {
				_, _, err := c.PlanDownload(ctx, []string{"/big"}, t.TempDir())
				done <- err
			}()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("%v, want canceled", err)
				}
				if d := time.Since(start); d < wait {
					t.Fatalf("ended after %v, before the cancel", d)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the plan did not end when canceled")
			}
			if tc.listed && ffs.Listed() == 0 {
				t.Fatal("the folder was never listed")
			}
		})
	}
}

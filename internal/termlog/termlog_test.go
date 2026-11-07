package termlog

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var at = time.Date(2026, 11, 7, 10, 30, 1, 0, time.FixedZone("", 8*3600))

func TestRecorderWritesLines(t *testing.T) {
	dir := t.TempDir()
	r, err := Start(dir, Header{Name: "web/01", Target: "root@10.0.0.1:22"}, Options{}, at, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "web_01", "2026-11-07_103001.log"); r.Path() != want {
		t.Fatalf("path %q, want %q", r.Path(), want)
	}
	r.Line("total 0", false)
	r.Line("abcde", true)
	r.Line("fgh", false)
	r.Line("long", true)
	if err := r.Close("$ exit", at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(r.Path())
	if err != nil {
		t.Fatal(err)
	}
	want := "==> web/01 · root@10.0.0.1:22 · 2026-11-07 10:30:01 +08:00\n" +
		"total 0\nabcdefgh\nlong$ exit\n" +
		"<== 2026-11-07 10:31:01 +08:00\n"
	if string(b) != want {
		t.Fatalf("log %q, want %q", b, want)
	}
	if r.Size() != int64(len(want)) {
		t.Fatalf("size %d, want %d", r.Size(), len(want))
	}
	// Closed, it takes nothing more, and closes once.
	r.Line("late", false)
	if err := r.Close("", at); err != nil {
		t.Fatal(err)
	}
	if b2, _ := os.ReadFile(r.Path()); string(b2) != want {
		t.Fatalf("written after close: %q", b2)
	}
}

// readClosed closes r at a minute past at and returns its log.
func readClosed(t *testing.T, r *Recorder, tail string) string {
	t.Helper()
	if err := r.Close(tail, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(r.Path())
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRecorderStampsLines(t *testing.T) {
	r, err := Start(t.TempDir(), Header{Name: "box"}, Options{Stamps: true}, at, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := at
	r.now = func() time.Time { return now }
	r.Line("one", false)
	// A line wrapped onto the next has one time, where it starts.
	now = now.Add(time.Second)
	r.Line("abc", true)
	now = now.Add(time.Second)
	r.Line("def", false)
	r.Mark("reconnected", at.Add(5*time.Second))
	got := readClosed(t, r, "$")
	_, body, _ := strings.Cut(got, "\n")
	want := "[2026-11-07 10:30:01] one\n[2026-11-07 10:30:02] abcdef\n" +
		"--- reconnected · 2026-11-07 10:30:06 +08:00 ---\n[2026-11-07 10:30:03] $\n<== 2026-11-07 10:31:01 +08:00\n"
	if body != want {
		t.Fatalf("log %q, want %q", body, want)
	}
}

func TestRecorderRawKeepsBytes(t *testing.T) {
	r, err := Start(t.TempDir(), Header{Name: "box"}, Options{Raw: true}, at, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Raw([]byte("\x1b[31mred\x1b[0m\r\n$ "))
	// The line left open ends before a mark and before the last line;
	// a raw log takes no tail, which its bytes have already.
	r.Mark("reconnected", at)
	r.Raw([]byte("50%\r100%"))
	got := readClosed(t, r, "100%")
	_, body, _ := strings.Cut(got, "\n")
	want := "\x1b[31mred\x1b[0m\r\n$ \n--- reconnected · 2026-11-07 10:30:01 +08:00 ---\n50%\r100%\n<== 2026-11-07 10:31:01 +08:00\n"
	if body != want {
		t.Fatalf("log %q, want %q", body, want)
	}
}

func TestRecorderFilePrivateAndUnique(t *testing.T) {
	dir := t.TempDir()
	r1, err := Start(dir, Header{Name: "box"}, Options{}, at, nil)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := Start(dir, Header{Name: "box"}, Options{}, at, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r1.Close("", at)
	defer r2.Close("", at)
	if r1.Path() == r2.Path() || !strings.HasSuffix(r2.Path(), "2026-11-07_103001-2.log") {
		t.Fatalf("two logs of a second: %q and %q", r1.Path(), r2.Path())
	}
	if runtime.GOOS == "windows" {
		return
	}
	fi, err := os.Stat(r1.Path())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("log mode %v", fi.Mode().Perm())
	}
	di, err := os.Stat(filepath.Dir(r1.Path()))
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("folder mode %v", di.Mode().Perm())
	}
}

func TestRecorderWriteFailureStops(t *testing.T) {
	r, err := Start(t.TempDir(), Header{Name: "box"}, Options{}, at, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The file is gone from under it: the next write fails.
	r.f.Close()
	r.Line("lost", false)
	deadline := time.Now().Add(5 * time.Second)
	for r.Err() == nil {
		if time.Now().After(deadline) {
			t.Fatal("the failed write was not reported")
		}
		time.Sleep(time.Millisecond)
	}
	if err := r.Close("", at); err == nil {
		t.Fatal("close hid the failure")
	}
}

func TestRecorderBacklogStops(t *testing.T) {
	r, err := Start(t.TempDir(), Header{Name: "box"}, Options{}, at, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close("", at)
	// Once the header is written the writer waits for more; hold it back,
	// as a file that stopped taking writes would.
	deadline := time.Now().Add(5 * time.Second)
	for r.Size() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the header was not written")
		}
		time.Sleep(time.Millisecond)
	}
	r.mu.Lock()
	r.buf = make([]byte, maxBacklog)
	r.mu.Unlock()
	r.Line("more", false)
	if r.Err() != ErrBacklog {
		t.Fatalf("err %v, want ErrBacklog", r.Err())
	}
}

func TestSafeName(t *testing.T) {
	for in, want := range map[string]string{
		"web-01":                 "web-01",
		"a/b\\c:d*e?f\"g|h":      "a_b_c_d_e_f_g_h",
		"<x>":                    "_x_",
		" .hidden. ":             "hidden",
		"":                       "session",
		"...":                    "session",
		"con":                    "_con",
		"COM1.txt":               "_COM1.txt",
		"lpt9":                   "_lpt9",
		"console":                "console",
		"tab\there":              "tab_here",
		"中文主机":                   "中文主机",
		strings.Repeat("x", 100): strings.Repeat("x", 80),
	} {
		if got := SafeName(in); got != want {
			t.Errorf("SafeName(%q) = %q, want %q", in, got, want)
		}
	}
}

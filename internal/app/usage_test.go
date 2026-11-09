package app

import (
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Licoy/sshyard/internal/layout"
	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

// procsExec answers the usage panel's script with two processes whose
// CPU time grows by a different amount each reading; anything else fails.
type procsExec struct{ reads atomic.Int32 }

func (p *procsExec) run(ch ssh.Channel, cmd string) int {
	if !strings.Contains(cmd, "/proc/stat") || !strings.Contains(cmd, "read_bytes") {
		return 1
	}
	n := uint64(p.reads.Add(1))
	stat := func(pid int, name string, ticks, rss uint64) string {
		return fmt.Sprintf("%d (%s) S 0 1 1 0 -1 0 0 0 0 0 %d 0 0 0 20 0 1 0 100 1000 %d", pid, name, ticks, rss)
	}
	out := "K 4096\n" +
		fmt.Sprintf("C cpu %d 0 0 0 0 0 0 0 0 0\n", n*200) +
		"C cpu0 0 0 0 0 0 0 0 0 0 0\nC cpu1 0 0 0 0 0 0 0 0 0 0\n" +
		"M MemTotal: 8000000 kB\nU 0 root\nU 1000 demo\n" +
		fmt.Sprintf("P 1000 0 %d %s\n", n*4096, stat(42, "busyloop", n*80, 1000)) +
		fmt.Sprintf("P 0 - - %s\n", stat(7, "idled", n*2, 50000))
	io.WriteString(ch, out)
	return 0
}

// TestUsagePanel: the usage panel lists the server's processes, takes
// their CPU and disk from two readings, sorts by any column and filters.
func TestUsagePanel(t *testing.T) {
	ex := &procsExec{}
	srv := startMetricSrv(t, ex.run)
	a, tt := newTestApp(t)
	tt.SetSize(1400, 900)
	connected(t, a, tt, srv)
	mustClick(t, tt, "Process Usage")
	tt.Frame()
	if !a.toolHere(layout.Usage) {
		t.Fatal("the pane did not open")
	}
	waitFor(t, tt, "the processes", func() bool { return tt.HasText("busyloop") && tt.HasText("2 processes") })
	waitFor(t, tt, "the CPU", func() bool {
		tt.Advance(usageFirst)
		return tt.HasText("80.0%")
	})
	// 80 ticks of 100 a CPU; 4 KiB written in the second the clock moved.
	if !tt.HasText("2.0%") || !tt.HasText("195.3M") {
		t.Errorf("texts %q", tt.Texts())
	}
	names := func() []string {
		var got []string
		for _, s := range tt.Texts() {
			if s == "busyloop" || s == "idled" {
				got = append(got, s)
			}
		}
		return got
	}
	if got := names(); len(got) != 2 || got[0] != "busyloop" {
		t.Fatalf("by CPU: %v", got)
	}
	a.usage.sort = ui.SortOrder{Column: "mem", Descending: true}
	tt.Frame()
	if got := names(); len(got) != 2 || got[0] != "idled" {
		t.Fatalf("by memory: %v", got)
	}

	a.usage.filter = "busy"
	tt.Frame()
	if tt.HasText("idled") || !tt.HasText("1 process") {
		t.Fatalf("filter: %q", tt.Texts())
	}
}

// TestUsagePanelNotLinux: a server without /proc is told, and not asked
// again.
func TestUsagePanelNotLinux(t *testing.T) {
	var runs atomic.Int32
	srv := startMetricSrv(t, func(ch ssh.Channel, cmd string) int {
		if strings.Contains(cmd, "read_bytes") {
			runs.Add(1)
			return 3
		}
		return 1
	})
	a, tt := newTestApp(t)
	connected(t, a, tt, srv)
	a.ShowTool(layout.Usage)
	tt.Frame()
	waitFor(t, tt, "the note", func() bool { return tt.HasText("only Linux servers") })
	tt.Advance(3 * usageEvery)
	if n := runs.Load(); n != 1 {
		t.Fatalf("read %d times", n)
	}
}

package acp

import (
	"io/fs"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/ai/policy"
)

// failAudit is an audit log whose open (prep) or append (rec) fails.
type failAudit struct {
	mu    sync.Mutex
	prep  error
	rec   error
	lines int
}

func (a *failAudit) Prepare(time.Time) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.prep
}

func (a *failAudit) Record(policy.AuditRecord) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.rec != nil {
		return a.rec
	}
	a.lines++
	return nil
}

type updateLog struct {
	mu  sync.Mutex
	got []Update
}

func (l *updateLog) add(u Update) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.got = append(l.got, u)
}

func (l *updateLog) notices() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, u := range l.got {
		if u.Kind == "notice" {
			out = append(out, u.Text)
		}
	}
	return out
}

func TestRemoteRunAuditFailures(t *testing.T) {
	const path = "/Users/someone/Library/ai-audit/2026-10.jsonl"
	script := "mcp-http\n---\n" + `mcp-call remote_run {"command":"uptime"}` + "\n"
	cases := []struct {
		name   string
		aud    *failAudit
		runs   int
		notice string
		result string
	}{
		{
			name:   "unavailable before the run",
			aud:    &failAudit{prep: &fs.PathError{Op: "open", Path: path, Err: syscall.EACCES}},
			notice: "audit-unavailable:" + syscall.EACCES.Error(),
			result: "audit log is unavailable",
		},
		{
			name:   "lost after the run",
			aud:    &failAudit{rec: &fs.PathError{Op: "write", Path: path, Err: syscall.ENOSPC}},
			runs:   1,
			notice: "audit-failed:" + syscall.ENOSPC.Error(),
			result: "do not run it again",
		},
		{
			name:   "healthy",
			aud:    &failAudit{},
			runs:   1,
			result: "exit code: 0",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gate := policy.NewGate(policy.GateConfig{Mode: func() policy.Mode { return policy.ModeFull }, Auditor: tc.aud})
			ex := &fakeExec{}
			updates := &updateLog{}
			s := mustStart(t, script, Config{Gate: gate, Exec: ex, OnUpdate: updates.add})
			if _, err := s.Prompt(t.Context(), "go", nil); err != nil {
				t.Fatal(err)
			}
			d := waitDiag(t, s, "mcp-result")
			if !strings.Contains(d, tc.result) || strings.Contains(d, path) {
				t.Fatalf("diag %s", d)
			}
			ex.mu.Lock()
			runs := len(ex.cmds)
			ex.mu.Unlock()
			if runs != tc.runs {
				t.Fatalf("ran %d times", runs)
			}
			got := updates.notices()
			if tc.notice == "" && len(got) != 0 || tc.notice != "" && (len(got) != 1 || got[0] != tc.notice) {
				t.Fatalf("notices %q", got)
			}
		})
	}
}

package agent

import (
	"io/fs"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/ai/llm"
	"github.com/Licoy/sshyard/internal/ai/policy"
)

// failAudit is an audit log whose open (prep) or append (rec) fails.
type failAudit struct {
	prep  error
	rec   error
	lines []policy.AuditRecord
}

func (a *failAudit) Prepare(time.Time) error { return a.prep }

func (a *failAudit) Record(r policy.AuditRecord) error {
	if a.rec != nil {
		return a.rec
	}
	a.lines = append(a.lines, r)
	return nil
}

const auditPath = "/Users/someone/Library/ai-audit/2026-10.jsonl"

func fullGate(aud policy.Auditor) policy.Gate {
	return policy.NewGate(policy.GateConfig{
		Mode:    func() policy.Mode { return policy.ModeFull },
		Auditor: aud,
		Host:    "web",
	})
}

func runTwice() *scriptModel {
	return &scriptModel{replies: []llm.Message{
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "run_command", Arguments: `{"command":"uptime"}`}}},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c2", Name: "run_command", Arguments: `{"command":"df -h"}`}}},
		{Role: "assistant", Content: "done"},
	}}
}

func notices(ev []Event) []string {
	var out []string
	for _, e := range ev {
		if e.Kind == EventNotice {
			out = append(out, e.Text)
		}
	}
	return out
}

func TestAuditUnavailableRefusesTheCommand(t *testing.T) {
	model := &scriptModel{replies: []llm.Message{
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "run_command", Arguments: `{"command":"uptime"}`}}},
		{Role: "assistant", Content: "cannot"},
	}}
	tools := &fakeTools{output: "up 3 days"}
	aud := &failAudit{prep: &fs.PathError{Op: "open", Path: auditPath, Err: syscall.EACCES}}
	s := New(Config{Model: model, Gate: fullGate(aud), Tools: tools})
	var ev []Event
	if err := s.Turn(t.Context(), "uptime?", HostMeta{Name: "web"}, "", collect(&ev)); err != nil {
		t.Fatal(err)
	}
	if len(tools.calls) != 0 {
		t.Fatalf("ran without an audit log: %v", tools.calls)
	}
	code := "audit-unavailable:" + syscall.EACCES.Error()
	card := lastCard(t, ev)
	if card.Run || card.Verdict != "reject" || card.Refusal != code {
		t.Fatalf("%+v", card)
	}
	if got := notices(ev); len(got) != 1 || got[0] != code {
		t.Fatalf("notices %q", got)
	}
	if !messageHas(model.reqs[1].Messages, "tool", "audit log is unavailable") || !messageHas(model.reqs[1].Messages, "tool", syscall.EACCES.Error()) {
		t.Fatal("the model was not told why")
	}
	if messageHas(model.reqs[1].Messages, "tool", auditPath) {
		t.Fatal("local audit path reached the model")
	}
}

func TestAuditLostAfterRunIsANotice(t *testing.T) {
	model := runTwice()
	tools := &fakeTools{output: "real output"}
	aud := &failAudit{rec: &fs.PathError{Op: "write", Path: auditPath, Err: syscall.ENOSPC}}
	s := New(Config{Model: model, Gate: fullGate(aud), Tools: tools})
	var ev []Event
	if err := s.Turn(t.Context(), "check", HostMeta{Name: "web"}, "", collect(&ev)); err != nil {
		t.Fatal(err)
	}
	// Each command ran exactly once, and the turn went on to the next one.
	if len(tools.calls) != 2 || !strings.Contains(tools.calls[0], "uptime") || !strings.Contains(tools.calls[1], "df -h") {
		t.Fatalf("calls %v", tools.calls)
	}
	code := "audit-failed:" + syscall.ENOSPC.Error()
	if got := notices(ev); len(got) != 2 || got[0] != code || got[1] != code {
		t.Fatalf("notices %q", got)
	}
	card := lastCard(t, ev)
	if !card.Run || card.Phase != "done" || card.Exit != 0 || card.Output != "real output" {
		t.Fatalf("%+v", card)
	}
	if s.Paused() {
		t.Fatal("an audit failure paused the agent")
	}
	msgs := model.reqs[1].Messages
	if !messageHas(msgs, "tool", "real output") || !messageHas(msgs, "tool", "do not run it again") || messageHas(msgs, "tool", auditPath) {
		t.Fatalf("tool result %+v", msgs)
	}
}

func TestAuditHealthyRunHasNoNotice(t *testing.T) {
	model := runTwice()
	tools := &fakeTools{output: "real output"}
	aud := &failAudit{}
	s := New(Config{Model: model, Gate: fullGate(aud), Tools: tools})
	var ev []Event
	if err := s.Turn(t.Context(), "check", HostMeta{Name: "web"}, "", collect(&ev)); err != nil {
		t.Fatal(err)
	}
	if len(tools.calls) != 2 || len(aud.lines) != 2 || len(notices(ev)) != 0 {
		t.Fatalf("calls %v lines %d notices %q", tools.calls, len(aud.lines), notices(ev))
	}
	if aud.lines[0].ExitCode == nil || *aud.lines[0].ExitCode != 0 {
		t.Fatalf("%+v", aud.lines[0])
	}
	if messageHas(model.reqs[1].Messages, "tool", "audit") {
		t.Fatal("audit note on a healthy run")
	}
}

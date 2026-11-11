package app

import (
	"testing"

	"github.com/Licoy/sshyard/internal/ai/acp"
	"github.com/Licoy/sshyard/internal/ai/agent"
	"github.com/Licoy/sshyard/locales"
)

// An audit failure from either agent lands on the pane's timeline as an
// error line, with the reason kept.
func TestAuditNoticeReachesTheTimeline(t *testing.T) {
	a, _ := newTestApp(t)
	h := savedHost(t, a, "web", "10.0.0.8")
	id, err := a.OpenAI(h.ID, openAt{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	c := a.conns[id]
	a.onAIEvent(id, agent.Event{Kind: agent.EventNotice, Text: "audit-failed:no space left on device"})
	a.onACPUpdate(c, acp.Update{Kind: "notice", Text: "audit-unavailable:permission denied"})
	n := len(c.ai.items)
	if n < 2 || c.ai.items[n-2].kind != "err" || c.ai.items[n-2].text != "audit-failed:no space left on device" ||
		c.ai.items[n-1].kind != "err" || c.ai.items[n-1].text != "audit-unavailable:permission denied" {
		t.Fatalf("items %+v", c.ai.items)
	}
	if c.ai.paused || c.ai.running {
		t.Fatalf("paused %v running %v", c.ai.paused, c.ai.running)
	}
	m := locales.In("en")
	if aiErrLine(m, "audit-failed:no space left on device") != m.AiAuditRecordFailed("no space left on device") ||
		toolOutput(m, "audit-unavailable:permission denied") != m.AiAuditUnavailable("permission denied") {
		t.Fatal("audit code not mapped to its message")
	}
}

package app

import (
	"testing"

	"github.com/Licoy/sshyard/internal/ai/agent"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
)

func TestAIToolCardCollapsed(t *testing.T) {
	for _, mode := range []theme.Mode{theme.Light, theme.Dark} {
		t.Run(string(mode), func(t *testing.T) {
			a, tt := newTestApp(t)
			a.settings.Theme = mode
			h := savedHost(t, a, "web", "10.0.0.8")
			id, err := a.OpenAI(h.ID, openAt{}, 0)
			if err != nil {
				t.Fatal(err)
			}
			cn := a.conns[id]
			card := agent.ToolCard{ID: "command", Name: "run_command", Category: "unknown", Command: "df -h", Phase: "open", Verdict: "user", Run: true, Exit: -1}
			a.onAIEvent(id, agent.Event{Kind: agent.EventTool, Tool: &card})
			tt.Frame()
			if tt.HasText("df -h") || tt.HasText("unknown") {
				t.Fatalf("details visible by default: %q", tt.Texts())
			}
			for _, want := range []string{"Run command", "Running", "Approved · You"} {
				if !tt.HasText(want) {
					t.Fatalf("missing summary %q in %q", want, tt.Texts())
				}
			}
			mustClick(t, tt, "Show action details")
			tt.Frame()
			if !tt.HasText("df -h") || !cn.ai.items[0].open {
				t.Fatalf("mouse did not expand: %q", tt.Texts())
			}
			mustClick(t, tt, "Copy")
			tt.Frame()
			if tt.Clipboard() != "df -h" {
				t.Fatalf("clipboard %q", tt.Clipboard())
			}
			card.Phase, card.Exit, card.Output = "done", 1, "disk check failed"
			a.onAIEvent(id, agent.Event{Kind: agent.EventTool, Tool: &card})
			tt.Frame()
			if !cn.ai.items[0].open || !tt.HasText("disk check failed") || !tt.HasText("Failed") {
				t.Fatalf("update lost details or failure: %q", tt.Texts())
			}
			mustClick(t, tt, "Hide action details")
			tt.Frame()
			if cn.ai.items[0].open || tt.HasText("df -h") || tt.HasText("disk check failed") || !tt.HasText("Failed") {
				t.Fatalf("mouse did not collapse: %q", tt.Texts())
			}
			card.Exit = 0
			a.onAIEvent(id, agent.Event{Kind: agent.EventTool, Tool: &card})
			tt.Frame()
			if cn.ai.items[0].open || !tt.HasText("Succeeded") {
				t.Fatalf("update unfolded or lost success: %q", tt.Texts())
			}
		})
	}
}

func TestAIToolCardStates(t *testing.T) {
	a, tt := newTestApp(t)
	h := savedHost(t, a, "web", "10.0.0.8")
	id, err := a.OpenAI(h.ID, openAt{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	cn := a.conns[id]
	for _, tc := range []struct {
		card agent.ToolCard
		want string
	}{
		{agent.ToolCard{Name: "read_file", Phase: "done", Exit: 1}, "Failed"},
		{agent.ToolCard{Name: "read_file", Phase: "open", Exit: -1}, "Running"},
		{agent.ToolCard{Name: "read_file", Phase: "done", Exit: 0}, "Succeeded"},
		{agent.ToolCard{Name: "read_file", Phase: "done", Exit: -1, Verdict: "reject"}, "Rejected"},
		{agent.ToolCard{Name: "read_file", Phase: "done", Exit: -1, Verdict: "cancel"}, "Cancelled"},
		{agent.ToolCard{Name: "read_file", Phase: "done", Exit: -1, Verdict: "timeout"}, "Timed out"},
		{agent.ToolCard{Name: "read_file", Phase: "done", Exit: -1, Verdict: "user", Run: true, Status: "cancel"}, "Cancelled"},
		{agent.ToolCard{Name: "read_file", Phase: "done", Exit: -1, Verdict: "user", Run: true, Status: "timeout"}, "Timed out"},
	} {
		cn.ai.items = []aiItem{{kind: "tool", tool: tc.card}}
		tt.Frame()
		if !tt.HasText(tc.want) {
			t.Fatalf("%+v missing %q in %q", tc.card, tc.want, tt.Texts())
		}
		if tc.card.Status != "" && !tt.HasText("Approved · You") {
			t.Fatalf("interrupted card lost approval: %q", tt.Texts())
		}
		if tc.card.Verdict == "" && tt.HasText(locales.In("en").AiVerdictReject()) {
			t.Fatalf("missing verdict shown as rejection: %q", tt.Texts())
		}
	}
}

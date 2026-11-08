package agent

import (
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/ai/llm"
)

// TestTerminalTarget: a session on a device's terminal gives the model
// only the terminal tools and the device's instruction, sends the line
// and the key to the gate as what is typed, and refuses the SSH tools.
func TestTerminalTarget(t *testing.T) {
	model := &scriptModel{replies: []llm.Message{
		{Role: "assistant", ToolCalls: []llm.ToolCall{
			{ID: "s1", Name: "terminal_send", Arguments: `{"command":"show version","timeout":30,"reason":"vendor"}`},
			{ID: "k1", Name: "terminal_key", Arguments: `{"key":"space"}`},
			{ID: "r1", Name: "run_command", Arguments: `{"command":"uname -a"}`},
			{ID: "e1", Name: "terminal_send", Arguments: `{"command":"  "}`},
		}},
		{Role: "assistant", Content: "A Cisco switch."},
	}}
	tools := &fakeTools{output: "status: prompt\nCisco IOS"}
	gate := &recordGate{allow: true}
	s := New(Config{Model: model, Gate: gate, Tools: tools, Target: TargetDevice})
	if err := s.Turn(t.Context(), "what is it", HostMeta{Name: "core", Address: "10.0.0.1", Protocol: "Telnet"}, "", nil); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range model.reqs[0].Tools {
		names = append(names, d.Name)
	}
	if strings.Join(names, ",") != "pane_output,terminal_send,terminal_key" {
		t.Fatalf("tools %v", names)
	}
	msgs := model.reqs[0].Messages
	if msgs[0].Content != DevicePrompt || !strings.Contains(msgs[1].Content, "Connection: Telnet") || strings.Contains(msgs[1].Content, "Uname") {
		t.Fatalf("system %q host %q", msgs[0].Content, msgs[1].Content)
	}
	if len(gate.actions) != 2 || gate.actions[0].Tool != "terminal_send" || gate.actions[0].Command != "show version" ||
		gate.actions[0].Reason != "vendor" || gate.actions[1].Tool != "terminal_key" || gate.actions[1].Command != "space" {
		t.Fatalf("authorized %+v", gate.actions)
	}
	if len(tools.calls) != 2 {
		t.Fatalf("calls %v", tools.calls)
	}
	if !messageHas(model.reqs[1].Messages, "tool", "unknown tool") || !messageHas(model.reqs[1].Messages, "tool", "empty tool argument: command") {
		t.Fatal("run_command or an empty line was not refused")
	}
}

// TestShellTarget: a local terminal's session has its own instruction
// and the terminal tools.
func TestShellTarget(t *testing.T) {
	model := &scriptModel{}
	s := New(Config{Model: model, Gate: &recordGate{allow: true}, Tools: &fakeTools{}, Target: TargetShell})
	if err := s.Turn(t.Context(), "hi", HostMeta{Name: "zsh", Protocol: "local shell zsh", Cwd: "/Users/me"}, "", nil); err != nil {
		t.Fatal(err)
	}
	msgs := model.reqs[0].Messages
	if msgs[0].Content != ShellPrompt || !strings.Contains(msgs[1].Content, "Cwd: /Users/me") || len(model.reqs[0].Tools) != 3 {
		t.Fatalf("system %q host %q tools %d", msgs[0].Content, msgs[1].Content, len(model.reqs[0].Tools))
	}
}

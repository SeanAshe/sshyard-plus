package agent

import (
	"github.com/Licoy/sshyard/internal/ai/llm"
	"testing"
)

func TestInvalidToolArgumentsDoNotAuthorizeOrRead(t *testing.T) {
	for _, raw := range []string{`{`, `null`, `[]`, `{}`, `{"path":"/x"}`, `{"path":"/x","content":null}`, `{"path":"/x","content":42}`, `{"path":"/x","content":"ok","unknown":1}`} {
		t.Run(raw, func(t *testing.T) {
			tools := &fakeTools{}
			gate := &recordGate{allow: true}
			session := New(Config{Tools: tools, Gate: gate})
			var events []Event
			if err := session.execOne(t.Context(), llm.ToolCall{ID: "w", Name: "write_file", Arguments: raw}, collect(&events)); err != nil {
				t.Fatal(err)
			}
			if len(tools.calls) != 0 || tools.readPath != "" || len(gate.actions) != 0 || len(events) != 1 || events[0].Tool.Run || events[0].Tool.Exit != 1 {
				t.Fatalf("tools=%v gate=%v events=%v", tools.calls, gate.actions, events)
			}
		})
	}
	if _, err := toolArguments("write_file", `{"path":"/x","content":""}`); err != nil {
		t.Fatalf("explicit empty write rejected: %v", err)
	}
}

func TestConfigureRedactsExistingHistory(t *testing.T) {
	const secret = "AKIA0000000000000000"
	model := &scriptModel{replies: []llm.Message{{Content: "ok"}, {Content: "ok"}}}
	session := New(Config{Model: model, Gate: &recordGate{allow: true}})
	if err := session.Turn(t.Context(), secret, HostMeta{}, "", nil); err != nil {
		t.Fatal(err)
	}
	session.Configure(&recordGate{allow: true}, true, nil)
	if err := session.Turn(t.Context(), "next "+secret, HostMeta{}, "", nil); err != nil {
		t.Fatal(err)
	}
	for _, message := range model.reqs[1].Messages {
		if message.Content == secret || message.Content == "next "+secret {
			t.Fatal("unredacted history sent")
		}
	}
}

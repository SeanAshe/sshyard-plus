package acp

import (
	"encoding/json"
	"strings"

	"github.com/Licoy/sshyard/internal/ai/agent"
	"github.com/Licoy/sshyard/internal/ai/policy"
	sdk "github.com/coder/acp-go-sdk"
)

// PlanLine is one entry of an agent plan.
type PlanLine struct {
	Content  string
	Status   string
	Priority string
}

// ToolView is a tool call or a later update to one.
type ToolView struct {
	ID         string
	Title      string
	Kind       string
	Status     string
	Paths      []string
	Raw        string
	Diff       string
	TerminalID string
}

// Local says whether the agent ran the tool on this computer itself: a
// command or search that went neither through the client's terminal nor
// through the remote tools. Reads and edits reach the host through the
// client's file callbacks.
func (v ToolView) Local() bool {
	return (v.Kind == "execute" || v.Kind == "search") && v.TerminalID == "" && !RemoteTool(v.Title)
}

// Update is one session event the UI can show. Kind is message, thought,
// tool, plan, mode, config, warning, notice or error. Unknown agent events
// are dropped. A notice's Text is a policy.AuditNotice code.
// A config update carries the agent's selectors as they are now.
type Update struct {
	Kind    string
	Text    string
	Tool    *ToolView
	Plan    []PlanLine
	Mode    string
	Options []Option
}

// decodeUpdate handles a session update only when the variant's
// discriminator string matches that variant. The SDK will otherwise
// fall back by field shape and put an unknown kind on the wrong variant.
func decodeUpdate(u sdk.SessionUpdate) (Update, bool) {
	switch {
	case u.AgentMessageChunk != nil && u.AgentMessageChunk.SessionUpdate == "agent_message_chunk":
		return Update{Kind: "message", Text: blockText(u.AgentMessageChunk.Content)}, true
	case u.AgentThoughtChunk != nil && u.AgentThoughtChunk.SessionUpdate == "agent_thought_chunk":
		return Update{Kind: "thought", Text: blockText(u.AgentThoughtChunk.Content)}, true
	case u.ToolCall != nil && u.ToolCall.SessionUpdate == "tool_call":
		return Update{Kind: "tool", Tool: toolFromCall(u.ToolCall)}, true
	case u.ToolCallUpdate != nil && u.ToolCallUpdate.SessionUpdate == "tool_call_update":
		return Update{Kind: "tool", Tool: toolFromUpdate(u.ToolCallUpdate)}, true
	case u.Plan != nil && u.Plan.SessionUpdate == "plan":
		return Update{Kind: "plan", Plan: planLines(u.Plan.Entries)}, true
	case u.CurrentModeUpdate != nil && u.CurrentModeUpdate.SessionUpdate == "current_mode_update":
		return Update{Kind: "mode", Mode: string(u.CurrentModeUpdate.CurrentModeId)}, true
	case u.ConfigOptionUpdate != nil && u.ConfigOptionUpdate.SessionUpdate == "config_option_update":
		return Update{Kind: "config", Options: options(u.ConfigOptionUpdate.ConfigOptions)}, true
	case u.AvailableCommandsUpdate != nil && u.AvailableCommandsUpdate.SessionUpdate == "available_commands_update":
		return Update{}, false
	default:
		return Update{}, false
	}
}

func blockText(b sdk.ContentBlock) string {
	if b.Text != nil {
		return b.Text.Text
	}
	if b.Resource != nil && b.Resource.Resource.TextResourceContents != nil {
		return b.Resource.Resource.TextResourceContents.Text
	}
	return ""
}

func planLines(entries []sdk.PlanEntry) []PlanLine {
	out := make([]PlanLine, 0, len(entries))
	for _, e := range entries {
		out = append(out, PlanLine{Content: e.Content, Status: string(e.Status), Priority: string(e.Priority)})
	}
	return out
}

func toolFromCall(c *sdk.SessionUpdateToolCall) *ToolView {
	v := &ToolView{
		ID:     string(c.ToolCallId),
		Title:  c.Title,
		Kind:   string(c.Kind),
		Status: string(c.Status),
		Paths:  locationPaths(c.Locations),
		Raw:    rawJSON(c.RawInput),
	}
	v.Diff, v.TerminalID = toolContent(c.Content)
	return v
}

func toolFromUpdate(c *sdk.SessionToolCallUpdate) *ToolView {
	v := &ToolView{ID: string(c.ToolCallId), Paths: locationPaths(c.Locations), Raw: rawJSON(c.RawInput)}
	if c.Title != nil {
		v.Title = *c.Title
	}
	if c.Kind != nil {
		v.Kind = string(*c.Kind)
	}
	if c.Status != nil {
		v.Status = string(*c.Status)
	}
	v.Diff, v.TerminalID = toolContent(c.Content)
	return v
}

func locationPaths(locs []sdk.ToolCallLocation) []string {
	out := make([]string, 0, len(locs))
	for _, l := range locs {
		if l.Path != "" {
			out = append(out, l.Path)
		}
	}
	return out
}

func toolContent(content []sdk.ToolCallContent) (diff, terminalID string) {
	var b strings.Builder
	for _, c := range content {
		if c.Diff != nil {
			old := ""
			if c.Diff.OldText != nil {
				old = *c.Diff.OldText
			}
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(agent.Unified(c.Diff.Path, old, c.Diff.NewText))
		}
		if c.Terminal != nil && terminalID == "" {
			terminalID = c.Terminal.TerminalId
		}
	}
	return b.String(), terminalID
}

func rawJSON(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return clip(string(b), 2048)
}

// actionFrom grades a permission request as local. The tool is the ACP
// kind. rawInput's name is ignored: a claimed remote read must not
// auto-allow the command. The path is taken as given, not mapped onto a
// host. An empty command and path grade as unknown. The reason is display-only.
func actionFrom(tc sdk.ToolCallUpdate) (policy.Action, string) {
	kind := ""
	if tc.Kind != nil {
		kind = string(*tc.Kind)
	}
	raw, _ := tc.RawInput.(map[string]any)
	act := policy.Action{Where: policy.Local, Tool: kind}
	act.Command = commandOf(raw)
	act.Path = firstString(raw, "path", "file")
	if act.Path == "" && len(tc.Locations) > 0 {
		act.Path = tc.Locations[0].Path
	}
	act.Dir = firstString(raw, "cwd", "dir")
	act.Reason = clip(firstString(raw, "reason"), reasonCap)
	return act, diffFrom(tc)
}

func diffFrom(tc sdk.ToolCallUpdate) string {
	diff, _ := toolContent(tc.Content)
	return diff
}

func commandOf(raw map[string]any) string {
	if raw == nil {
		return ""
	}
	switch c := raw["command"].(type) {
	case string:
		args := stringSlice(raw["args"])
		if len(args) == 0 {
			return c
		}
		return QuoteCommand(c, args)
	case []any:
		parts := stringSlice(c)
		if len(parts) == 0 {
			return ""
		}
		return QuoteCommand(parts[0], parts[1:])
	default:
		return ""
	}
}

func firstString(raw map[string]any, keys ...string) string {
	if raw == nil {
		return ""
	}
	for _, k := range keys {
		if s, ok := raw[k].(string); ok {
			return s
		}
	}
	return ""
}

func stringSlice(v any) []string {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		s, ok := item.(string)
		if !ok {
			return nil
		}
		out = append(out, s)
	}
	return out
}

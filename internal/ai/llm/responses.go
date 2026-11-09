package llm

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// This file is the OpenAI Responses format: POST {base}/responses, with
// store off, so every request carries the whole conversation. Reasoning
// items come back encrypted when reasoning is asked for, and are replayed
// with the assistant message they preceded.

func (c *Client) responsesPayload(req Request) ([]byte, error) {
	body := map[string]any{
		"model":  req.Model,
		"input":  c.responsesInput(req.Messages, req.Model),
		"stream": true,
		"store":  false,
	}
	if c.Temperature != nil {
		body["temperature"] = *c.Temperature
	}
	if c.TopP != nil {
		body["top_p"] = *c.TopP
	}
	if n := c.maxTokens(req); n > 0 {
		body["max_output_tokens"] = n
	}
	if req.Effort != "" {
		r := map[string]any{"effort": req.Effort}
		if req.Effort != EffortNone {
			r["summary"] = "auto"
			body["include"] = []string{"reasoning.encrypted_content"}
		}
		body["reasoning"] = r
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, len(req.Tools))
		for i, t := range req.Tools {
			w := map[string]any{"type": "function", "name": t.Name}
			if t.Description != "" {
				w["description"] = t.Description
			}
			if len(t.Parameters) > 0 {
				w["parameters"] = json.RawMessage(t.Parameters)
			}
			tools[i] = w
		}
		body["tools"] = tools
	}
	return json.Marshal(body)
}

func (c *Client) responsesInput(in []Message, model string) []any {
	var out []any
	for _, m := range leadSystem(in) {
		switch m.Role {
		case "tool":
			out = append(out, map[string]any{"type": "function_call_output", "call_id": m.ToolCallID, "output": m.Content})
		case "assistant":
			if op := c.opaque(m, model); op != nil {
				var items []json.RawMessage
				if json.Unmarshal(op, &items) == nil {
					for _, it := range items {
						out = append(out, it)
					}
				}
			}
			if m.Content != "" {
				out = append(out, map[string]any{"role": "assistant", "content": m.Content})
			}
			for _, tc := range m.ToolCalls {
				out = append(out, map[string]any{"type": "function_call", "call_id": tc.ID, "name": tc.Name, "arguments": tc.Arguments})
			}
		default:
			if len(m.Parts) == 0 {
				out = append(out, map[string]any{"role": m.Role, "content": m.Content})
				continue
			}
			var parts []map[string]any
			if m.Content != "" {
				parts = append(parts, map[string]any{"type": "input_text", "text": m.Content})
			}
			for _, p := range m.Parts {
				if p.IsImage() {
					parts = append(parts, map[string]any{"type": "input_image", "image_url": dataURL(p)})
					continue
				}
				parts = append(parts, map[string]any{"type": "input_file", "filename": p.Name, "file_data": dataURL(p)})
			}
			out = append(out, map[string]any{"role": m.Role, "content": parts})
		}
	}
	return out
}

// respItem is one output item: a message, a function call or reasoning.
type respItem struct {
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Content   []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Encrypted string `json:"encrypted_content"`
}

type respObject struct {
	Status            string            `json:"status"`
	Output            []json.RawMessage `json:"output"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type respEvent struct {
	Type        string          `json:"type"`
	Delta       string          `json:"delta"`
	OutputIndex int             `json:"output_index"`
	Item        json.RawMessage `json:"item"`
	Response    *respObject     `json:"response"`
	Message     string          `json:"message"`
	Error       *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *Client) readResponses(br *bufioReader, emit func(Event)) (Message, error) {
	if br.JSON {
		b, err := br.all()
		if err != nil {
			return Message{}, err
		}
		var parsed respObject
		if err := json.Unmarshal(b, &parsed); err != nil {
			return Message{}, fmt.Errorf("llm: response: %w", err)
		}
		if parsed.Error != nil && parsed.Error.Message != "" {
			return Message{}, c.streamError(parsed.Error.Message)
		}
		msg, err := c.respMessage(&parsed, nil, "")
		if err == nil && emit != nil && msg.Content != "" {
			emit(Event{Content: msg.Content})
		}
		return msg, err
	}
	var (
		final *respObject
		text  strings.Builder
		done  = map[int]json.RawMessage{}
		// thought is set while a summary part has text, which a blank
		// line then ends.
		thought bool
	)
	err := consumeSSE(br.Reader, func(data string) error {
		var ev respEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return fmt.Errorf("llm: stream: %w", err)
		}
		if final != nil {
			return fmt.Errorf("%w: data after completion", ErrProtocol)
		}
		switch ev.Type {
		case "error":
			msg := ev.Message
			if ev.Error != nil && ev.Error.Message != "" {
				msg = ev.Error.Message
			}
			return c.streamError(msg)
		case "response.failed":
			msg := "response failed"
			if ev.Response != nil && ev.Response.Error != nil && ev.Response.Error.Message != "" {
				msg = ev.Response.Error.Message
			}
			return c.streamError(msg)
		case "response.output_text.delta":
			text.WriteString(ev.Delta)
			if emit != nil && ev.Delta != "" {
				emit(Event{Content: ev.Delta})
			}
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			if emit != nil && ev.Delta != "" {
				emit(Event{Thinking: ev.Delta})
				thought = true
			}
		case "response.reasoning_summary_part.done":
			if emit != nil && thought {
				emit(Event{Thinking: "\n\n"})
			}
			thought = false
		case "response.output_item.done":
			if len(ev.Item) > 0 {
				done[ev.OutputIndex] = ev.Item
			}
		case "response.completed", "response.incomplete":
			if ev.Response == nil {
				return fmt.Errorf("%w: completion without response", ErrProtocol)
			}
			final = ev.Response
		}
		return nil
	})
	if err != nil {
		return Message{}, err
	}
	if final == nil {
		return Message{}, fmt.Errorf("%w: missing or unsuccessful completion", ErrIncompleteResponse)
	}
	if len(final.Output) == 0 && len(done) > 0 {
		idx := make([]int, 0, len(done))
		for i := range done {
			idx = append(idx, i)
		}
		sort.Ints(idx)
		for _, i := range idx {
			final.Output = append(final.Output, done[i])
		}
	}
	return c.respMessage(final, emit, text.String())
}

// respMessage builds the assistant message from a finished response.
// streamed is the text already emitted; text only in the items is emitted.
func (c *Client) respMessage(r *respObject, emit func(Event), streamed string) (Message, error) {
	var (
		msg       Message
		reasoning []json.RawMessage
		text      strings.Builder
	)
	for _, raw := range r.Output {
		var it respItem
		if err := json.Unmarshal(raw, &it); err != nil {
			return Message{}, fmt.Errorf("llm: response: %w", err)
		}
		switch it.Type {
		case "message":
			for _, p := range it.Content {
				if p.Type == "output_text" {
					text.WriteString(p.Text)
				}
			}
		case "function_call":
			args := it.Arguments
			if strings.TrimSpace(args) == "" {
				args = "{}"
			}
			msg.ToolCalls = append(msg.ToolCalls, ToolCall{ID: it.CallID, Name: it.Name, Arguments: args})
		case "reasoning":
			// Without its encrypted content an item cannot be replayed
			// when nothing is stored.
			if it.Encrypted != "" {
				reasoning = append(reasoning, raw)
			}
		}
	}
	msg.Content = streamed
	if msg.Content == "" && text.Len() > 0 {
		msg.Content = text.String()
		if emit != nil {
			emit(Event{Content: msg.Content})
		}
	}
	switch r.Status {
	case "completed":
		msg.Finish = "stop"
		if len(msg.ToolCalls) > 0 {
			msg.Finish = "tool_calls"
		}
	case "incomplete":
		if r.IncompleteDetails != nil && r.IncompleteDetails.Reason == "max_output_tokens" {
			msg.Finish = "length"
		}
	}
	if err := validateMessage(msg); err != nil {
		return Message{}, err
	}
	if len(reasoning) > 0 {
		msg.Opaque, _ = json.Marshal(reasoning)
	}
	return msg, nil
}

package llm

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// serve answers every request with body and records the last request.
func serve(t *testing.T, body string) (*httptest.Server, *http.Request, *[]byte) {
	t.Helper()
	got := &http.Request{}
	payload := new([]byte)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = *r.Clone(r.Context())
		*payload, _ = io.ReadAll(r.Body)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, got, payload
}

func decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("payload %s: %v", b, err)
	}
	return m
}

func TestChatLegacyTokensEffortAndParts(t *testing.T) {
	srv, _, payload := serve(t, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n")
	c := &Client{BaseURL: srv.URL, APIKey: "test-key-not-real", Model: "m", MaxTokens: 64, LegacyMaxTokens: true, TopP: ptr(0.5)}
	_, err := c.Stream(t.Context(), Request{Model: "other", Effort: EffortHigh, Messages: []Message{
		{Role: "user", Content: "look", Parts: []Part{{Name: "a.png", MIME: "image/png", Data: []byte("png")}, {Name: "r.pdf", MIME: "application/pdf", Data: []byte("pdf")}}},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := decode(t, *payload)
	if m["model"] != "other" || m["max_tokens"] != 64.0 || m["max_completion_tokens"] != nil || m["reasoning_effort"] != "high" || m["top_p"] != 0.5 {
		t.Fatalf("payload %s", *payload)
	}
	s := string(*payload)
	if !strings.Contains(s, `"image_url":{"url":"data:image/png;base64,cG5n"}`) || !strings.Contains(s, `"file_data":"data:application/pdf;base64,cGRm"`) || !strings.Contains(s, `"filename":"r.pdf"`) {
		t.Fatalf("parts %s", s)
	}
}

// scatteredSystem has the host context after the prompt and a later system
// message mid-conversation, as a restored agent session sends them.
var scatteredSystem = []Message{
	{Role: "system", Content: "sys"}, {Role: "system", Content: "host"},
	{Role: "user", Content: "disk?"},
	{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "run_command", Arguments: `{"command":"df"}`}}},
	{Role: "tool", ToolCallID: "call_1", Content: "90%"},
	{Role: "system", Content: "later"},
	{Role: "user", Content: "and now?"},
}

func TestChatMergesSystemMessages(t *testing.T) {
	srv, _, payload := serve(t, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n")
	c := &Client{BaseURL: srv.URL, APIKey: "k", Model: "qwen"}
	if _, err := c.Stream(t.Context(), Request{Messages: scatteredSystem}, nil); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Messages []chatMsg `json:"messages"`
	}
	if err := json.Unmarshal(*payload, &body); err != nil {
		t.Fatal(err)
	}
	var roles []string
	for _, m := range body.Messages {
		roles = append(roles, m.Role)
	}
	if strings.Join(roles, ",") != "system,user,assistant,tool,user" {
		t.Fatalf("roles %v in %s", roles, *payload)
	}
	m := body.Messages
	if m[0].Content != "sys\n\nhost\n\nlater" || m[1].Content != "disk?" || m[4].Content != "and now?" ||
		len(m[2].ToolCalls) != 1 || m[2].ToolCalls[0].ID != "call_1" || m[3].ToolCallID != "call_1" || m[3].Content != "90%" {
		t.Fatalf("messages %s", *payload)
	}
}

func TestResponsesMergesSystemMessages(t *testing.T) {
	srv, _, payload := serve(t, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`)
	c := &Client{Format: FormatResponses, BaseURL: srv.URL, APIKey: "k", Model: "qwen"}
	if _, err := c.Stream(t.Context(), Request{Messages: scatteredSystem}, nil); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(*payload, &body); err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, it := range body.Input {
		k, _ := it["role"].(string)
		if k == "" {
			k, _ = it["type"].(string)
		}
		kinds = append(kinds, k)
	}
	if strings.Join(kinds, ",") != "system,user,function_call,function_call_output,user" {
		t.Fatalf("input %v in %s", kinds, *payload)
	}
	in := body.Input
	if in[0]["content"] != "sys\n\nhost\n\nlater" || in[1]["content"] != "disk?" || in[2]["call_id"] != "call_1" ||
		in[3]["call_id"] != "call_1" || in[3]["output"] != "90%" || in[4]["content"] != "and now?" {
		t.Fatalf("input %s", *payload)
	}
}

const respStream = `event: response.created
data: {"type":"response.created","response":{"status":"in_progress"}}

data: {"type":"response.reasoning_summary_text.delta","delta":"plan"}

data: {"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_1","encrypted_content":"enc","summary":[]}}

data: {"type":"response.output_text.delta","delta":"checking"}

data: {"type":"response.output_item.done","output_index":1,"item":{"type":"message","content":[{"type":"output_text","text":"checking"}]}}

data: {"type":"response.function_call_arguments.delta","output_index":2,"delta":"{\"command\""}

data: {"type":"response.output_item.done","output_index":2,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"run_command","arguments":"{\"command\":\"df\"}"}}

data: {"type":"response.completed","response":{"status":"completed","output":[]}}

`

func TestResponsesStreamToolCallAndReplay(t *testing.T) {
	srv, got, payload := serve(t, respStream)
	c := &Client{Format: FormatResponses, BaseURL: srv.URL + "/v1", APIKey: "test-key-not-real", Model: "gpt", MaxTokens: 100, Temperature: ptr(0.3)}
	var text, think string
	msg, err := c.Stream(t.Context(), Request{Effort: EffortLow, Messages: []Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "disk?"}},
		Tools: []Tool{{Name: "run_command", Parameters: json.RawMessage(`{"type":"object"}`)}}}, func(e Event) { text += e.Content; think += e.Thinking })
	if err != nil {
		t.Fatal(err)
	}
	if got.URL.Path != "/v1/responses" || got.Header.Get("Authorization") != "Bearer test-key-not-real" {
		t.Fatalf("request %s %v", got.URL.Path, got.Header)
	}
	m := decode(t, *payload)
	if m["store"] != false || m["max_output_tokens"] != 100.0 || m["temperature"] != 0.3 || m["reasoning"].(map[string]any)["effort"] != "low" {
		t.Fatalf("payload %s", *payload)
	}
	if tools := m["tools"].([]any); tools[0].(map[string]any)["name"] != "run_command" {
		t.Fatalf("tools %v", tools)
	}
	if text != "checking" || think != "plan" || msg.Content != "checking" || msg.Finish != "tool_calls" {
		t.Fatalf("msg %+v text %q think %q", msg, text, think)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].ID != "call_1" || msg.ToolCalls[0].Arguments != `{"command":"df"}` {
		t.Fatalf("calls %+v", msg.ToolCalls)
	}
	if msg.OpaqueFormat != FormatResponses || msg.OpaqueModel != "gpt" || !strings.Contains(string(msg.Opaque), `"enc"`) {
		t.Fatalf("opaque %+v", msg)
	}

	msg.Role = "assistant"
	hist := []Message{{Role: "user", Content: "disk?"}, msg}
	in, _ := json.Marshal(c.responsesInput(hist, "gpt"))
	var items []map[string]any
	json.Unmarshal(in, &items)
	if len(items) != 4 || items[1]["type"] != "reasoning" || items[1]["encrypted_content"] != "enc" ||
		items[2]["role"] != "assistant" || items[3]["type"] != "function_call" || items[3]["call_id"] != "call_1" || items[3]["arguments"] != `{"command":"df"}` {
		t.Fatalf("input %s", in)
	}
	hist[1].Content = ""
	in, _ = json.Marshal(c.responsesInput(append(hist, Message{Role: "tool", ToolCallID: "call_1", Content: "90%"}), "gpt"))
	if !strings.Contains(string(in), `"output":"90%"`) || !strings.Contains(string(in), `"type":"function_call_output"`) {
		t.Fatalf("input %s", in)
	}
	in, _ = json.Marshal(c.responsesInput(hist, "another"))
	if strings.Contains(string(in), "reasoning") {
		t.Fatalf("replayed reasoning to another model: %s", in)
	}
}

func TestResponsesIncompleteAndFailed(t *testing.T) {
	for body, want := range map[string]error{
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"x\"}\n\ndata: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"}}}\n\n": ErrTokenLimit,
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"x\"}\n\n":                                 ErrIncompleteResponse,
		`{"status":"completed","output":[{"type":"function_call","call_id":"c","name":"n","arguments":"{"}]}`: ErrProtocol,
	} {
		srv, _, _ := serve(t, body)
		c := &Client{Format: FormatResponses, BaseURL: srv.URL, APIKey: "k", Model: "m"}
		if _, err := c.Stream(t.Context(), Request{Messages: []Message{{Role: "user", Content: "q"}}}, nil); !errors.Is(err, want) {
			t.Fatalf("%q: %v", body, err)
		}
	}
	srv, _, _ := serve(t, "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"message\":\"boom k-secret\"}}}\n\n")
	c := &Client{Format: FormatResponses, BaseURL: srv.URL, APIKey: "k-secret", Model: "m"}
	_, err := c.Stream(t.Context(), Request{Messages: []Message{{Role: "user", Content: "q"}}}, nil)
	var api *APIError
	if !errors.As(err, &api) || strings.Contains(err.Error(), "k-secret") {
		t.Fatalf("failed response: %v", err)
	}
}

func TestResponsesJSONBody(t *testing.T) {
	srv, _, _ := serve(t, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"hi"}]}]}`)
	c := &Client{Format: FormatResponses, BaseURL: srv.URL, APIKey: "k", Model: "m"}
	var text string
	msg, err := c.Stream(t.Context(), Request{Messages: []Message{{Role: "user", Content: "q"}}}, func(e Event) { text += e.Content })
	if err != nil || msg.Content != "hi" || text != "hi" || msg.Finish != "stop" {
		t.Fatalf("%+v %v %q", msg, err, text)
	}
}

const antStream = `event: message_start
data: {"type":"message_start","message":{"role":"assistant","content":[]}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hmm"}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig"}}

data: {"type":"content_block_stop","index":0}

data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"let me check"}}

data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"tu_1","name":"run_command","input":{}}}

data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"command\":"}}

data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"\"df\"}"}}

event: ping
data: {"type":"ping"}

data: {"type":"content_block_start","index":3,"content_block":{"type":"tool_use","id":"tu_2","name":"list_dir","input":{}}}

data: {"type":"message_delta","delta":{"stop_reason":"tool_use"}}

data: {"type":"message_stop"}

`

func TestAnthropicStreamToolCallsAndReplay(t *testing.T) {
	srv, got, payload := serve(t, antStream)
	c := &Client{Format: FormatAnthropic, BaseURL: srv.URL + "/v1", APIKey: "test-key-not-real", Model: "claude", Headers: map[string]string{"X-Api-Key": "other", "X-Title": "SSHYard"}}
	var text, think string
	msg, err := c.Stream(t.Context(), Request{Effort: EffortMedium, Messages: []Message{
		{Role: "system", Content: "sys"}, {Role: "system", Content: "host"},
		{Role: "user", Content: "look", Parts: []Part{{Name: "a.jpg", MIME: "image/jpeg", Data: []byte("jpg")}}},
	}, Tools: []Tool{{Name: "run_command", Description: "run"}}}, func(e Event) { text += e.Content; think += e.Thinking })
	if err != nil {
		t.Fatal(err)
	}
	if got.URL.Path != "/v1/messages" || got.Header.Get("X-Api-Key") != "test-key-not-real" || got.Header.Get("Anthropic-Version") != anthropicVersion || got.Header.Get("Authorization") != "" || got.Header.Get("X-Title") != "SSHYard" {
		t.Fatalf("request %s %v", got.URL.Path, got.Header)
	}
	m := decode(t, *payload)
	if m["system"] != "sys\n\nhost" || m["max_tokens"] != float64(AnthropicMaxTokens) || m["output_config"].(map[string]any)["effort"] != "medium" || m["thinking"].(map[string]any)["type"] != "adaptive" {
		t.Fatalf("payload %s", *payload)
	}
	if !strings.Contains(string(*payload), `{"source":{"data":"anBn","media_type":"image/jpeg","type":"base64"},"type":"image"}`) || !strings.Contains(string(*payload), `"input_schema":{"type":"object"}`) {
		t.Fatalf("payload %s", *payload)
	}
	if text != "let me check" || think != "hmm" || msg.Finish != "tool_calls" || len(msg.ToolCalls) != 2 {
		t.Fatalf("msg %+v text %q think %q", msg, text, think)
	}
	if msg.ToolCalls[0].Arguments != `{"command":"df"}` || msg.ToolCalls[1].Arguments != `{}` {
		t.Fatalf("calls %+v", msg.ToolCalls)
	}

	msg.Role = "assistant"
	hist := []Message{{Role: "user", Content: "look"}, msg,
		{Role: "tool", ToolCallID: "tu_1", Content: "90%"}, {Role: "tool", ToolCallID: "tu_2", Content: ""}, {Role: "user", Content: "and?"}}
	_, wire := c.anthropicMessages(hist, "claude")
	if len(wire) != 3 || wire[2].Role != "user" || len(wire[2].Content) != 3 {
		t.Fatalf("turns %+v", wire)
	}
	b, _ := json.Marshal(wire)
	if !strings.Contains(string(b), `{"signature":"sig","thinking":"hmm","type":"thinking"}`) || !strings.Contains(string(b), `"input":{"command":"df"}`) {
		t.Fatalf("wire %s", b)
	}
	_, wire = c.anthropicMessages(hist, "other")
	if b, _ := json.Marshal(wire); strings.Contains(string(b), "thinking") {
		t.Fatalf("replayed thinking to another model: %s", b)
	}
}

func TestAnthropicIncompleteAndLimits(t *testing.T) {
	for body, want := range map[string]error{
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"x\"}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"max_tokens\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n": ErrTokenLimit,
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"x\"}}\n\n":                                                                                                                       ErrIncompleteResponse,
		"data: {\"type\":\"content_block_delta\",\"index\":4,\"delta\":{\"type\":\"text_delta\",\"text\":\"x\"}}\n\n":                                                                                                                         ErrProtocol,
	} {
		srv, _, _ := serve(t, body)
		c := &Client{Format: FormatAnthropic, BaseURL: srv.URL, APIKey: "k", Model: "m"}
		if _, err := c.Stream(t.Context(), Request{Messages: []Message{{Role: "user", Content: "q"}}}, nil); !errors.Is(err, want) {
			t.Fatalf("%q: %v", body, err)
		}
	}
	srv, _, _ := serve(t, "data: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n")
	c := &Client{Format: FormatAnthropic, BaseURL: srv.URL, APIKey: "k", Model: "m"}
	var api *APIError
	if _, err := c.Stream(t.Context(), Request{Messages: []Message{{Role: "user", Content: "q"}}}, nil); !errors.As(err, &api) || api.Message != "Overloaded" {
		t.Fatal(err)
	}
}

func TestAnthropicProbeSendsSmallLimitAndJSONBody(t *testing.T) {
	srv, _, payload := serve(t, `{"type":"message","content":[{"type":"text","text":"p"}],"stop_reason":"max_tokens"}`)
	c := &Client{Format: FormatAnthropic, BaseURL: srv.URL, APIKey: "k", Model: "m"}
	if err := c.Test(t.Context()); err != nil {
		t.Fatal(err)
	}
	if m := decode(t, *payload); m["max_tokens"] != 16.0 || m["thinking"] != nil {
		t.Fatalf("probe %s", *payload)
	}
}

func TestListModels(t *testing.T) {
	srv, got, _ := serve(t, `{"data":[{"id":"gpt-5"},{"id":"text-embedding-3-large"},{"id":"tts-1"},{"id":"gpt-5"},{"id":"o3"},{"id":"whisper-1"},{"id":"gpt-image-1"},{"id":"omni-moderation-latest"}]}`)
	c := &Client{BaseURL: srv.URL + "/v1", APIKey: "k"}
	ids, err := c.ListModels(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(ids, ",") != "gpt-5,o3" || got.URL.Path != "/v1/models" || got.Method != http.MethodGet || got.Header.Get("Authorization") != "Bearer k" {
		t.Fatalf("ids %v path %s", ids, got.URL.Path)
	}
}

func TestListModelsAnthropicPages(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		if r.Header.Get("X-Api-Key") != "k" {
			t.Errorf("headers %v", r.Header)
		}
		if r.URL.Query().Get("after_id") == "" {
			io.WriteString(w, `{"data":[{"id":"claude-b"}],"has_more":true,"last_id":"claude-b"}`)
			return
		}
		io.WriteString(w, `{"data":[{"id":"claude-a"}],"has_more":false,"last_id":"claude-a"}`)
	}))
	defer srv.Close()
	c := &Client{Format: FormatAnthropic, BaseURL: srv.URL, APIKey: "k"}
	ids, err := c.ListModels(t.Context())
	if err != nil || strings.Join(ids, ",") != "claude-a,claude-b" || len(queries) != 2 || !strings.Contains(queries[1], "after_id=claude-b") {
		t.Fatalf("%v %v %v", ids, err, queries)
	}
}

func TestListModelsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		io.WriteString(w, `{"error":{"message":"bad key k-secret"}}`)
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, APIKey: "k-secret"}
	_, err := c.ListModels(t.Context())
	var api *APIError
	if !errors.As(err, &api) || api.Status != 401 || strings.Contains(err.Error(), "k-secret") {
		t.Fatal(err)
	}
}

func TestIsChatModel(t *testing.T) {
	for id, want := range map[string]bool{
		"gpt-5.1": true, "claude-opus-5-5": true, "deepseek-chat": true, "Llama-3-70B-Instruct": true, "gpt-4o-search-preview": true,
		"text-embedding-3-small": false, "tts-1-hd": false, "gpt-4o-mini-transcribe": false, "gpt-realtime": false,
		"dall-e-3": false, "sora-2": false, "omni-moderation-latest": false, "BAAI/bge-reranker-v2": false, "davinci-002": false,
	} {
		if IsChatModel(id) != want {
			t.Errorf("%s: want %v", id, want)
		}
	}
}

// TestAPIErrorDetail checks that the message shown to the user carries no
// key, sits on one line and is cut short.
func TestAPIErrorDetail(t *testing.T) {
	e := &APIError{Status: 400, Message: "bad key sk-secret\n  in   request " + strings.Repeat("x", 400), key: "sk-secret"}
	d := e.Detail()
	if strings.Contains(d, "sk-secret") || strings.Contains(d, "\n") || !strings.HasPrefix(d, "bad key [redacted] in request x") {
		t.Fatalf("detail %q", d)
	}
	if r := []rune(d); len(r) != detailMax+1 || r[len(r)-1] != '…' {
		t.Fatalf("detail is %d runes", len(r))
	}
}

// TestResponsesSummaryPartBreak checks that a summary part ends with a
// blank line only when it had text, so an empty part sends no thinking.
func TestResponsesSummaryPartBreak(t *testing.T) {
	srv, _, _ := serve(t, `data: {"type":"response.reasoning_summary_part.done"}

data: {"type":"response.reasoning_summary_text.delta","delta":"one"}

data: {"type":"response.reasoning_summary_part.done"}

data: {"type":"response.reasoning_summary_part.done"}

data: {"type":"response.reasoning_summary_text.delta","delta":"two"}

data: {"type":"response.output_text.delta","delta":"ok"}

data: {"type":"response.completed","response":{"status":"completed","output":[]}}

`)
	c := &Client{Format: FormatResponses, BaseURL: srv.URL + "/v1", APIKey: "test-key-not-real", Model: "gpt"}
	var think []string
	if _, err := c.Stream(t.Context(), Request{Messages: []Message{{Role: "user", Content: "hi"}}}, func(e Event) {
		if e.Thinking != "" {
			think = append(think, e.Thinking)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(think, "|"); got != "one|\n\n|two" {
		t.Fatalf("thinking %q", got)
	}
}

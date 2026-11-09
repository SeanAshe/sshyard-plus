package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStreamAccumulatesToolCalls(t *testing.T) {
	const key = "test-key-not-real"
	var gotAuth, gotExtra string
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotExtra = r.Header.Get("X-Title")
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, strings.ReplaceAll(`: keep-alive

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"run_command","arguments":""}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"comm"}}]}}]}

data: {"choices":[{"delta":
data: {"content":"","tool_calls":[{"index":0,"function":{"arguments":"and\":\"df -h\"}"}}]}}]}

data: {"choices":[{"delta":{"content":"ok","reasoning_content":"look"},"finish_reason":"tool_calls"}]}

data: [DONE]

`, "\n", "\r\n"))
	}))
	defer srv.Close()

	var chunks []Event
	c := &Client{BaseURL: srv.URL + "/v1", APIKey: key, Model: "fake-model", Headers: map[string]string{"X-Title": "SSHYard", "Authorization": "Bearer other"}, Temperature: ptr(0.2), MaxTokens: 16}
	msg, err := c.Stream(context.Background(), Request{Messages: []Message{{Role: "user", Content: "disk?"}}, Tools: []Tool{{Name: "run_command", Description: "run", Parameters: []byte(`{"type":"object"}`)}}}, func(e Event) {
		chunks = append(chunks, e)
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer "+key {
		t.Fatalf("auth %q", gotAuth)
	}
	if gotExtra != "SSHYard" {
		t.Fatalf("extra header %q", gotExtra)
	}
	if !strings.Contains(body, `"model":"fake-model"`) || !strings.Contains(body, `"stream":true`) || !strings.Contains(body, `"temperature":0.2`) || !strings.Contains(body, `"max_completion_tokens":16`) {
		t.Fatalf("body %s", body)
	}
	if strings.Contains(body, key) {
		t.Fatal("key in the request body")
	}
	if msg.Finish != "tool_calls" || msg.Content != "ok" {
		t.Fatalf("message %+v", msg)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].ID != "call_1" || msg.ToolCalls[0].Name != "run_command" || msg.ToolCalls[0].Arguments != `{"command":"df -h"}` {
		t.Fatalf("calls %+v", msg.ToolCalls)
	}
	var text, think string
	for _, e := range chunks {
		text += e.Content
		think += e.Thinking
	}
	if text != "ok" || think != "look" {
		t.Fatalf("chunks text %q think %q", text, think)
	}
}

func TestErrorBodyScrubsKey(t *testing.T) {
	const key = "test-key-not-real"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		io.WriteString(w, `{"error":{"message":"rejected `+key+`"}}`)
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, APIKey: key, Model: "m"}
	err := c.Test(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), key) {
		t.Fatalf("key leaked: %v", err)
	}
	if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("error %v", err)
	}
}

func TestTruncatedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		io.WriteString(w, strings.Repeat("x", errorBodyCap+80))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, APIKey: "test-key-not-real", Model: "m"}
	_, err := c.Stream(context.Background(), Request{Messages: []Message{{Role: "user", Content: "ping"}}}, nil)
	if err == nil || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "truncated") {
		t.Fatal(err)
	}
	if strings.Contains(err.Error(), "test-key-not-real") {
		t.Fatal(err)
	}
}

func TestResponseSizeCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, strings.Repeat("data: {\"choices\":[{\"delta\":{\"content\":\"y\"}}]}\n\n", 50))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, APIKey: "test-key-not-real", Model: "m", MaxBytes: 40}
	_, err := c.Stream(context.Background(), Request{Messages: []Message{{Role: "user", Content: "ping"}}}, nil)
	if err == nil || !strings.Contains(err.Error(), "size") {
		t.Fatal(err)
	}
	if strings.Contains(err.Error(), "test-key-not-real") {
		t.Fatal(err)
	}
}

func TestNoRedirectToAnotherHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.test/stolen", http.StatusFound)
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, APIKey: "test-key-not-real", Model: "m"}
	_, err := c.Stream(context.Background(), Request{Messages: []Message{{Role: "user", Content: "ping"}}}, nil)
	if err == nil || !strings.Contains(err.Error(), "example.test") {
		t.Fatal(err)
	}
}

func TestRedirectHTTPSToHTTPRefused(t *testing.T) {
	const key = "test-key-not-real"
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://"+r.Host+"/chat/completions", http.StatusFound)
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, APIKey: key, Model: "m", HTTP: srv.Client()}
	_, err := c.Stream(context.Background(), Request{Messages: []Message{{Role: "user", Content: "ping"}}}, nil)
	if err == nil || !strings.Contains(err.Error(), "refusing redirect") || !strings.Contains(err.Error(), "http://") {
		t.Fatal(err)
	}
	if strings.Contains(err.Error(), key) {
		t.Fatal(err)
	}
}

func TestRedirectOtherPortRefused(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.Host
		if i := strings.LastIndex(name, ":"); i >= 0 {
			name = name[:i]
		}
		http.Redirect(w, r, "https://"+name+":1/chat", http.StatusFound)
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, APIKey: "test-key-not-real", Model: "m", HTTP: srv.Client()}
	_, err := c.Stream(context.Background(), Request{Messages: []Message{{Role: "user", Content: "ping"}}}, nil)
	if err == nil || !strings.Contains(err.Error(), "refusing redirect") {
		t.Fatal(err)
	}
}

func TestRedirectSameHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat/completions" {
			http.Redirect(w, r, "/v1/chat/completions/next", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL + "/v1", APIKey: "test-key-not-real", Model: "m"}
	msg, err := c.Stream(context.Background(), Request{Messages: []Message{{Role: "user", Content: "ping"}}}, nil)
	if err != nil || msg.Content != "ok" {
		t.Fatalf("%+v %v", msg, err)
	}
}

func TestCancelStopsStream(t *testing.T) {
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, APIKey: "test-key-not-real", Model: "m"}
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := c.Stream(ctx, Request{Messages: []Message{{Role: "user", Content: "ping"}}}, nil)
		errc <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not start")
	}
	cancel()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not stop the stream")
	}
}

func TestConnectionDoesNotDialBadURL(t *testing.T) {
	c := &Client{BaseURL: "ftp://example.test/v1", APIKey: "test-key-not-real", Model: "m", HTTP: &http.Client{Transport: denyTransport{}}}
	if err := c.Test(context.Background()); err == nil || !strings.Contains(err.Error(), "base URL") {
		t.Fatal(err)
	}
}

func TestTemperatureOmittedWhenZero(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"z\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, APIKey: "test-key-not-real", Model: "m"}
	if _, err := c.Stream(context.Background(), Request{Messages: []Message{{Role: "user", Content: "ping"}}}, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, "temperature") || strings.Contains(body, "max_") || strings.Contains(body, "reasoning") || strings.Contains(body, "tools") {
		t.Fatalf("body %s", body)
	}
}

type denyTransport struct{}

func (denyTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("dialed")
}

// TestStreamTimeout checks that a reply that stops coming fails with
// ErrTimeout once it has waited Timeout, while one whose pieces keep
// coming, each sooner than Timeout, runs to its end however long it takes.
func TestStreamTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		f := w.(http.Flusher)
		if strings.Contains(r.Header.Get("X-Case"), "stall") {
			io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n")
			f.Flush()
			<-r.Context().Done()
			return
		}
		for i := 0; i < 6; i++ {
			io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n")
			f.Flush()
			time.Sleep(40 * time.Millisecond)
		}
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	req := Request{Messages: []Message{{Role: "user", Content: "ping"}}}

	stall := &Client{BaseURL: srv.URL, APIKey: "test-key-not-real", Model: "m", Timeout: 150 * time.Millisecond, Headers: map[string]string{"X-Case": "stall"}}
	start := time.Now()
	if _, err := stall.Stream(context.Background(), req, nil); !errors.Is(err, ErrTimeout) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stalled reply: %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("timed out after %v", d)
	}

	steady := &Client{BaseURL: srv.URL, APIKey: "test-key-not-real", Model: "m", Timeout: 150 * time.Millisecond}
	msg, err := steady.Stream(context.Background(), req, nil)
	if err != nil || msg.Content != "aaaaaa" {
		t.Fatalf("steady reply: %q %v", msg.Content, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	if _, err := stall.Stream(ctx, req, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled reply: %v", err)
	}
}

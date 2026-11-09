// Package llm is the model client: one streaming completion against the
// configured base URL, with function tools, in one of three wire formats
// (OpenAI Chat Completions, OpenAI Responses and Anthropic Messages), and
// one decision from a decision model (OpenAI Decisions, TypeSafe Jev).
// The API key is a header only. It is never written to a log, and errors
// that would quote it are scrubbed.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// The wire formats a Client speaks. An empty format is FormatChat.
const (
	FormatChat      = "chat"
	FormatResponses = "responses"
	FormatAnthropic = "anthropic"
	// FormatTypeSafe is TypeSafe's System One API, which decides and does
	// not chat.
	FormatTypeSafe = "typesafe"
)

// Formats lists the wire formats in the order the settings show them.
var Formats = []string{FormatChat, FormatResponses, FormatAnthropic, FormatTypeSafe}

// ErrNoChat is the error for a completion asked of a format that cannot chat.
var ErrNoChat = errors.New("llm: this provider only makes decisions")

// The thinking levels a Request may ask for. An empty Effort leaves the
// provider's default; EffortNone turns reasoning off where the format can.
const (
	EffortNone   = "none"
	EffortLow    = "low"
	EffortMedium = "medium"
	EffortHigh   = "high"
	EffortXHigh  = "xhigh"
)

// AnthropicMaxTokens is the max_tokens an Anthropic request sends when none
// is configured, as the format requires one.
const AnthropicMaxTokens = 32000

// DefaultMaxBytes is the most of one response body a turn will read.
const DefaultMaxBytes = 8 << 20

// errorBodyCap is how much of a non-2xx body is kept for the message.
const errorBodyCap = 64 << 10

// ErrTooBig is the error when a response exceeds the size cap.
var ErrTooBig = errors.New("llm: response exceeds the size cap")

// ErrBaseURL is the error for a base URL this client will not dial.
var ErrBaseURL = errors.New("llm: base URL must be http or https with a host")

// Client talks to one endpoint. A nil Temperature or TopP, or a zero
// MaxTokens, is omitted so the provider's default applies; an Anthropic
// request, which needs max_tokens, then sends AnthropicMaxTokens.
type Client struct {
	Format      string
	BaseURL     string
	APIKey      string
	Model       string
	Headers     map[string]string
	Temperature *float64
	TopP        *float64
	MaxTokens   int
	// LegacyMaxTokens sends a Chat Completions limit as max_tokens rather
	// than max_completion_tokens, for endpoints that know only the old name.
	LegacyMaxTokens bool
	// MaxBytes caps the response body of one call. Zero means DefaultMaxBytes.
	MaxBytes int
	// HTTP is optional. Its Transport is reused; a redirect is allowed
	// only when its scheme, hostname and port all match BaseURL.
	HTTP *http.Client
	// Timeout is how long a completion waits for the reply to start, and
	// then between two pieces of it, before it fails with ErrTimeout.
	// Zero waits as long as the connection lasts.
	Timeout time.Duration
}

// Message is one chat message. ToolCalls is set on an assistant message
// that asked for tools; ToolCallID is set on a tool result. Parts are the
// files a user message carries besides its text.
//
// Opaque is what the provider needs back with an assistant message, such
// as reasoning items or signed thinking blocks. It is replayed only to the
// format and model that produced it (OpaqueFormat, OpaqueModel).
type Message struct {
	Role         string
	Content      string
	Parts        []Part
	ToolCalls    []ToolCall
	ToolCallID   string
	Finish       string
	Opaque       json.RawMessage
	OpaqueFormat string
	OpaqueModel  string
}

// Part is one file sent with a user message: an image or a PDF.
type Part struct {
	Name string
	MIME string
	Data []byte
}

// IsImage reports whether the part is sent as an image.
func (p Part) IsImage() bool { return strings.HasPrefix(p.MIME, "image/") }

// ToolCall is one function call, arguments accumulated from the stream.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// Tool is a function the model may call. Parameters is a JSON schema.
type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// Request is one completion. Model and Effort override the client's model
// and the provider's default thinking when set.
type Request struct {
	Messages []Message
	Tools    []Tool
	Model    string
	Effort   string
	// MaxTokens overrides the client's when not zero.
	MaxTokens int
}

// Event is one piece of streamed text. Thinking is set when the model
// sends a reasoning delta.
type Event struct {
	Content  string
	Thinking string
}

// APIError is a non-2xx response. Error never contains the API key.
type APIError struct {
	Status    int
	Message   string
	Truncated bool
	key       string
}

// Detail is the provider's message, without the API key, on one line of
// at most detailMax runes, to show to the user.
func (e *APIError) Detail() string {
	msg := strings.Join(strings.Fields(e.scrubbed()), " ")
	if r := []rune(msg); len(r) > detailMax {
		msg = string(r[:detailMax]) + "…"
	}
	return msg
}

const detailMax = 300

func (e *APIError) scrubbed() string {
	if e.key == "" {
		return e.Message
	}
	return strings.ReplaceAll(e.Message, e.key, "[redacted]")
}

func (e *APIError) Error() string {
	msg := e.scrubbed()
	if e.Truncated {
		return fmt.Sprintf("llm: %d: %s (truncated)", e.Status, msg)
	}
	return fmt.Sprintf("llm: %d: %s", e.Status, msg)
}

func (c *Client) format() string {
	switch c.Format {
	case FormatResponses, FormatAnthropic, FormatTypeSafe:
		return c.Format
	}
	return FormatChat
}

// Test checks the endpoint with a tiny request and no tools. Only the
// Anthropic format, which requires a limit, sends one.
func (c *Client) Test(ctx context.Context) error {
	if c.format() == FormatTypeSafe {
		_, err := c.Decide(ctx, "ping", []Question{{Name: "probe", Instructions: "Is this text a greeting?"}})
		return err
	}
	req := Request{Messages: []Message{{Role: "user", Content: "ping"}}}
	if c.format() == FormatAnthropic {
		req.MaxTokens = 16
	}
	_, err := c.Stream(ctx, req, nil)
	// An explicit token-limit completion is expected for the limited probe;
	// missing completion or malformed responses must still fail.
	if errors.Is(err, ErrTokenLimit) {
		return nil
	}
	return err
}

// Stream posts one completion with streaming on and returns the assembled
// assistant message. emit, when set, receives each text delta. Cancelling
// ctx stops the read. A redirect is refused unless its scheme, hostname
// and port all match BaseURL, so the key is not sent in clear or to
// another service.
func (c *Client) Stream(ctx context.Context, req Request, emit func(Event)) (Message, error) {
	model := req.Model
	if model == "" {
		model = c.Model
	}
	if model == "" {
		return Message{}, errors.New("llm: model is empty")
	}
	req.Model = model
	var (
		path    string
		payload []byte
		err     error
		read    func(*bufioReader, func(Event)) (Message, error)
	)
	switch c.format() {
	case FormatResponses:
		path, read = "/responses", c.readResponses
		payload, err = c.responsesPayload(req)
	case FormatAnthropic:
		path, read = "/messages", c.readAnthropic
		payload, err = c.anthropicPayload(req)
	case FormatTypeSafe:
		return Message{}, ErrNoChat
	default:
		path, read = "/chat/completions", c.readChat
		payload, err = c.chatPayload(req)
	}
	if err != nil {
		return Message{}, err
	}
	ctx, w := watch(ctx, c.Timeout)
	defer w.stop()
	resp, err := c.do(ctx, http.MethodPost, path, nil, payload)
	if err != nil {
		return Message{}, w.err(err)
	}
	defer resp.Body.Close()
	br, err := c.body(w.reader(resp.Body))
	if err != nil {
		return Message{}, w.err(c.readErr(ctx, err))
	}
	msg, err := read(br, emit)
	if err == nil && br.r.n > br.r.max {
		err = ErrTooBig
	}
	if err != nil {
		return Message{}, w.err(c.readErr(ctx, err))
	}
	if msg.Opaque != nil {
		msg.OpaqueFormat, msg.OpaqueModel = c.format(), model
	}
	return msg, nil
}

// opaque returns m's replay data when it was produced by this format and model.
func (c *Client) opaque(m Message, model string) json.RawMessage {
	if len(m.Opaque) == 0 || m.OpaqueFormat != c.format() || m.OpaqueModel != model {
		return nil
	}
	return m.Opaque
}

var (
	ErrProtocol           = errors.New("llm: invalid response protocol")
	ErrIncompleteResponse = errors.New("llm: incomplete response")
	// ErrTokenLimit is an explicit, structurally complete length response with
	// no tool calls. It remains incomplete for normal assistant turns.
	ErrTokenLimit = fmt.Errorf("%w: output token limit reached", ErrIncompleteResponse)
)

// validateMessage checks a decoded message, whose Finish each format maps
// to stop, length or tool_calls.
func validateMessage(msg Message) error {
	switch msg.Finish {
	case "stop":
		if len(msg.ToolCalls) != 0 {
			return fmt.Errorf("%w: tools without tool_calls completion", ErrProtocol)
		}
	case "length":
		if len(msg.ToolCalls) != 0 {
			return fmt.Errorf("%w: token limit interrupted tool calls", ErrProtocol)
		}
		return ErrTokenLimit
	case "tool_calls":
		if len(msg.ToolCalls) == 0 {
			return fmt.Errorf("%w: no completed tools", ErrProtocol)
		}
	default:
		return fmt.Errorf("%w: missing or unsuccessful completion", ErrIncompleteResponse)
	}
	ids := make(map[string]bool)
	for _, tc := range msg.ToolCalls {
		args := bytes.TrimSpace([]byte(tc.Arguments))
		if strings.TrimSpace(tc.ID) == "" || strings.TrimSpace(tc.Name) == "" || ids[tc.ID] || len(args) == 0 || args[0] != '{' || !json.Valid(args) {
			return fmt.Errorf("%w: incomplete tool call", ErrProtocol)
		}
		ids[tc.ID] = true
	}
	return nil
}

// dataURL is a part as a data: URL, as the OpenAI formats take files.
func dataURL(p Part) string {
	return "data:" + p.MIME + ";base64," + b64(p.Data)
}

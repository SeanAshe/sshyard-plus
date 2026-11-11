package acp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/Licoy/sshyard/internal/ai/policy"
	"github.com/Licoy/sshyard/internal/mcp"
)

// RemoteServer is the name of the MCP server that gives the agent the
// pane's host: its tools run commands and read and write files there,
// each through the pane's gate. Agents that run their own tools on this
// computer are pointed at it instead.
const RemoteServer = "sshyard"

// The remote tools' names. They are distinctive so a tool call shown by
// the agent can be told apart from the agent's own tools.
const (
	toolRun   = "remote_run"
	toolRead  = "remote_read_file"
	toolWrite = "remote_write_file"
)

var remoteToolNames = []string{toolRun, toolRead, toolWrite}

const (
	runDefault = 120 * time.Second
	runMax     = 30 * time.Minute
	runOutput  = 256 << 10
)

// Instructions tell the agent where it works. They go into its system
// prompt where it takes one, otherwise before the first request.
const Instructions = `You are working on a remote server for a user of SSHYard, an SSH client. The host, user and working directory come with each request.
- Everything the user asks about is on that remote host, never on this computer. Your local working directory is an empty scratch folder that has nothing to do with the task: do not look at it or at any other local file.
- Run commands on the host only with the ` + RemoteServer + ` MCP server's ` + toolRun + ` tool, or the terminal the client provides. Read and write the host's files only with ` + toolRead + ` and ` + toolWrite + `, or the client's file tools. Paths are paths on the remote host; a relative path is relative to the working directory.
- Do not run local shell commands, search or list local files, use other MCP servers, or connect to the host yourself with ssh: SSHYard already holds the connection and you have no credentials.
- SSHYard checks every action and may ask the user first. When one is refused, do not try to reach the same result another way.`

// remoteTools is the bridge's tool set. Every call belongs to the running
// turn and is graded by the gate like a client callback.
func (s *Session) remoteTools() []mcp.Tool {
	return []mcp.Tool{
		{
			Name:        toolRun,
			Description: "Run a shell command (sh -c) on the remote host and return its exit code and output (the last 256 KiB of stdout and stderr together).",
			Schema: json.RawMessage(`{"type":"object","properties":{` +
				`"command":{"type":"string","description":"The shell command"},` +
				`"cwd":{"type":"string","description":"Remote working directory; empty uses the session's"},` +
				`"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800,"description":"Default 120"}},` +
				`"required":["command"],"additionalProperties":false}`),
			Call: s.toolRun,
		},
		{
			Name:        toolRead,
			Description: "Read a text file on the remote host (at most 10 MB). line (1-based) and limit select lines.",
			Schema: json.RawMessage(`{"type":"object","properties":{` +
				`"path":{"type":"string","description":"Remote path"},` +
				`"line":{"type":"integer","minimum":1},"limit":{"type":"integer","minimum":0}},` +
				`"required":["path"],"additionalProperties":false}`),
			Call: s.toolRead,
		},
		{
			Name:        toolWrite,
			Description: "Write a text file on the remote host, replacing its content. The user sees the change as a diff.",
			Schema: json.RawMessage(`{"type":"object","properties":{` +
				`"path":{"type":"string","description":"Remote path"},` +
				`"content":{"type":"string","description":"The whole new content"}},` +
				`"required":["path","content"],"additionalProperties":false}`),
			Call: s.toolWrite,
		},
	}
}

// startBridge serves the remote tools on 127.0.0.1 behind a random token.
func (s *Session) startBridge() error {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return err
	}
	token := hex.EncodeToString(b[:])
	srv, err := mcp.Listen(0, &mcp.Handler{Name: RemoteServer, Version: "1", Instructions: Instructions, Token: token, Tools: s.remoteTools()})
	if err != nil {
		return err
	}
	s.bridge, s.bridgeToken = srv, token
	return nil
}

// toolContext admits a bridge call into the running turn, as
// callbackContext does for protocol callbacks.
func (s *Session) toolContext(ctx context.Context) (context.Context, func(), error) {
	s.mu.Lock()
	sid := s.sid
	s.mu.Unlock()
	ctx, release, err := s.callbackContext(ctx, sid)
	if err != nil {
		return nil, nil, errors.New("no request is running")
	}
	return ctx, release, nil
}

// remotePath turns a tool's path into a remote path: a relative one is
// under the working directory.
func (s *Session) remotePath(p string) (string, error) {
	if p != "" && !strings.HasPrefix(p, "/") && !strings.Contains(p, "\x00") {
		dir, err := remoteCwd(s.cfg.RemoteHome, s.cfg.CwdFollow)
		if err != nil {
			return "", err
		}
		p = path.Join(dir, p)
	}
	return s.mapPath(p)
}

func (s *Session) toolRun(ctx context.Context, raw json.RawMessage) (string, error) {
	var in struct {
		Command string `json:"command"`
		Cwd     string `json:"cwd"`
		Timeout int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(raw, &in); err != nil || strings.TrimSpace(in.Command) == "" {
		return "", errors.New("command is required")
	}
	if len(in.Command) > maxCmdLen || strings.Contains(in.Command, "\x00") {
		return "", errInvalid
	}
	ctx, release, err := s.toolContext(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	var dir string
	if in.Cwd != "" {
		dir, err = s.remotePath(in.Cwd)
	} else {
		dir, err = remoteCwd(s.cfg.RemoteHome, s.cfg.CwdFollow)
	}
	if err != nil {
		return "", err
	}
	act := policy.Action{Where: policy.Remote, Tool: "run_command", Command: in.Command, Dir: dir}
	start := time.Now()
	v, err := s.authorize(ctx, act, "")
	if err != nil {
		return "", err
	}
	if !v.Run {
		return "", refusalError(v)
	}
	if s.cfg.Exec == nil {
		s.finish(act, v, 1, start, 0)
		return "", errExec
	}
	timeout := runDefault
	if in.Timeout > 0 {
		timeout = min(time.Duration(in.Timeout)*time.Second, runMax)
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	buf := &tailBuf{limit: runOutput}
	exit, err := s.cfg.Exec.Run(rctx, in.Command, dir, buf, buf)
	if err != nil && exit == 0 {
		exit = 1
	}
	out, truncated, total := buf.snapshot()
	auditErr := s.finish(act, v, exit, start, total)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "exit code: %d\n", exit)
	if auditErr != nil {
		fmt.Fprintf(&b, "note: %v\n", auditErr)
	}
	if rctx.Err() == context.DeadlineExceeded {
		fmt.Fprintf(&b, "stopped after %s\n", timeout)
	} else if err != nil && exit == 1 && total == 0 {
		fmt.Fprintf(&b, "error: %v\n", err)
	}
	if truncated {
		b.WriteString("(output truncated; only the end is kept)\n")
	}
	b.WriteString(out)
	return s.outbound(b.String()), nil
}

func (s *Session) toolRead(ctx context.Context, raw json.RawMessage) (string, error) {
	var in struct {
		Path  string `json:"path"`
		Line  *int   `json:"line"`
		Limit *int   `json:"limit"`
	}
	if err := json.Unmarshal(raw, &in); err != nil || in.Path == "" {
		return "", errors.New("path is required")
	}
	ctx, release, err := s.toolContext(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	remote, err := s.remotePath(in.Path)
	if err != nil {
		return "", err
	}
	return s.readText(ctx, remote, in.Line, in.Limit)
}

func (s *Session) toolWrite(ctx context.Context, raw json.RawMessage) (string, error) {
	var in struct {
		Path    string  `json:"path"`
		Content *string `json:"content"`
	}
	if err := json.Unmarshal(raw, &in); err != nil || in.Path == "" || in.Content == nil {
		return "", errors.New("path and content are required")
	}
	ctx, release, err := s.toolContext(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	remote, err := s.remotePath(in.Path)
	if err != nil {
		return "", err
	}
	if err := s.writeText(ctx, remote, *in.Content); err != nil {
		return "", err
	}
	return "wrote " + remote, nil
}

// RemoteTool reports whether a tool call the agent shows is one of the
// remote tools, by its title.
func RemoteTool(title string) bool {
	for _, name := range remoteToolNames {
		if strings.Contains(title, name) {
			return true
		}
	}
	return false
}

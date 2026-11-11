package acp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"github.com/Licoy/sshyard/internal/ai/policy"
	sdk "github.com/coder/acp-go-sdk"
)

// termProc is one remote command started for the agent.
type termProc struct {
	id      string
	ctx     context.Context
	cancel  context.CancelFunc
	buf     *tailBuf
	cmd     string
	dir     string
	env     []string
	act     policy.Action
	verdict policy.Verdict
	done    chan struct{}
	mu      sync.Mutex
	exit    *int
	err     error
}

func (t *termProc) finished() bool {
	select {
	case <-t.done:
		return true
	default:
		return false
	}
}

func (t *termProc) exitCode() (int, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.exit == nil {
		return 0, false
	}
	return *t.exit, true
}

func (s *Session) CreateTerminal(ctx context.Context, p sdk.CreateTerminalRequest) (sdk.CreateTerminalResponse, error) {
	ctx, release, err := s.callbackContext(ctx, p.SessionId)
	if err != nil {
		return sdk.CreateTerminalResponse{}, err
	}
	defer release()
	if err := s.checkSession(p.SessionId); err != nil {
		return sdk.CreateTerminalResponse{}, err
	}
	limit, err := termLimit(p.OutputByteLimit)
	if err != nil {
		return sdk.CreateTerminalResponse{}, err
	}
	var dir string
	if p.Cwd != nil && *p.Cwd != "" {
		dir, err = s.mapPath(*p.Cwd)
	} else {
		dir, err = remoteCwd(s.cfg.RemoteHome, s.cfg.CwdFollow)
	}
	if err != nil {
		return sdk.CreateTerminalResponse{}, err
	}
	quoted := QuoteCommand(p.Command, p.Args)
	if len(quoted) > maxCmdLen {
		return sdk.CreateTerminalResponse{}, errInvalid
	}
	env := filterTermEnv(p.Env)
	act := policy.Action{Where: policy.Remote, Tool: "run_command", Command: quoted, Dir: dir}
	start := time.Now()
	v, err := s.authorize(ctx, act, "")
	if err != nil {
		return sdk.CreateTerminalResponse{}, err
	}
	if !v.Run {
		return sdk.CreateTerminalResponse{}, refusalError(v)
	}
	if s.cfg.Exec == nil {
		s.finish(act, v, 1, start, 0)
		return sdk.CreateTerminalResponse{}, errExec
	}
	s.mu.Lock()
	if s.closing || s.turn == nil || s.turn.stopped || ctx.Err() != nil {
		s.mu.Unlock()
		return sdk.CreateTerminalResponse{}, errSession
	}
	running := 0
	for _, t := range s.terms {
		if !t.finished() {
			running++
		}
	}
	if running >= maxTerminals {
		s.mu.Unlock()
		return sdk.CreateTerminalResponse{}, errTooMany
	}
	id := newTermID()
	tctx, cancel := context.WithCancel(s.turn.ctx)
	t := &termProc{
		id: id, ctx: tctx, cancel: cancel,
		buf: &tailBuf{limit: limit}, cmd: quoted, dir: dir, env: env,
		act: act, verdict: v, done: make(chan struct{}),
	}
	s.terms[id] = t
	s.turn.terms = append(s.turn.terms, t)
	s.wg.Add(1)
	s.mu.Unlock()
	go s.runTerm(t)
	return sdk.CreateTerminalResponse{TerminalId: id}, nil
}

func (s *Session) runTerm(t *termProc) {
	defer s.wg.Done()
	defer close(t.done)
	start := time.Now()
	var exit int
	var err error
	if er, ok := s.cfg.Exec.(EnvRunner); ok && len(t.env) > 0 {
		exit, err = er.RunEnv(t.ctx, t.cmd, t.dir, t.env, t.buf, t.buf)
	} else {
		exit, err = s.cfg.Exec.Run(t.ctx, t.cmd, t.dir, t.buf, t.buf)
	}
	if err != nil {
		if t.ctx.Err() != nil {
			exit = -1
		} else if exit == 0 {
			exit = 1
		}
	}
	t.mu.Lock()
	t.exit = &exit
	t.err = err
	t.mu.Unlock()
	_, _, total := t.buf.snapshot()
	s.finish(t.act, t.verdict, exit, start, total)
}

func (s *Session) TerminalOutput(ctx context.Context, p sdk.TerminalOutputRequest) (sdk.TerminalOutputResponse, error) {
	if err := s.checkSession(p.SessionId); err != nil {
		return sdk.TerminalOutputResponse{}, err
	}
	t := s.term(p.TerminalId)
	if t == nil {
		return sdk.TerminalOutputResponse{}, errTerminal
	}
	out, truncated, _ := t.buf.snapshot()
	resp := sdk.TerminalOutputResponse{Output: s.outbound(out), Truncated: truncated}
	if code, ok := t.exitCode(); ok {
		resp.ExitStatus = &sdk.TerminalExitStatus{ExitCode: &code}
	}
	return resp, nil
}

func (s *Session) WaitForTerminalExit(ctx context.Context, p sdk.WaitForTerminalExitRequest) (sdk.WaitForTerminalExitResponse, error) {
	if err := s.checkSession(p.SessionId); err != nil {
		return sdk.WaitForTerminalExitResponse{}, err
	}
	t := s.term(p.TerminalId)
	if t == nil {
		return sdk.WaitForTerminalExitResponse{}, errTerminal
	}
	select {
	case <-ctx.Done():
		return sdk.WaitForTerminalExitResponse{}, ctx.Err()
	case <-t.done:
	}
	code, _ := t.exitCode()
	return sdk.WaitForTerminalExitResponse{ExitCode: &code}, nil
}

func (s *Session) KillTerminal(ctx context.Context, p sdk.KillTerminalRequest) (sdk.KillTerminalResponse, error) {
	if err := s.checkSession(p.SessionId); err != nil {
		return sdk.KillTerminalResponse{}, err
	}
	t := s.term(p.TerminalId)
	if t == nil {
		return sdk.KillTerminalResponse{}, errTerminal
	}
	t.cancel()
	return sdk.KillTerminalResponse{}, nil
}

func (s *Session) ReleaseTerminal(ctx context.Context, p sdk.ReleaseTerminalRequest) (sdk.ReleaseTerminalResponse, error) {
	if err := s.checkSession(p.SessionId); err != nil {
		return sdk.ReleaseTerminalResponse{}, err
	}
	s.mu.Lock()
	t := s.terms[p.TerminalId]
	if t == nil {
		s.mu.Unlock()
		return sdk.ReleaseTerminalResponse{}, errTerminal
	}
	delete(s.terms, p.TerminalId)
	s.mu.Unlock()
	t.cancel()
	return sdk.ReleaseTerminalResponse{}, nil
}

func (s *Session) term(id string) *termProc {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.terms[id]
}

func (s *Session) killTerms() {
	s.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.terms))
	for _, t := range s.terms {
		cancels = append(cancels, t.cancel)
	}
	s.mu.Unlock()
	for _, c := range cancels {
		c()
	}
}

func filterTermEnv(in []sdk.EnvVariable) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, e := range in {
		if e.Name != "LANG" && e.Name != "TERM" && !strings.HasPrefix(e.Name, "LC_") {
			continue
		}
		if strings.ContainsAny(e.Name, "=\x00") || strings.Contains(e.Value, "\x00") {
			continue
		}
		if _, ok := seen[e.Name]; ok {
			continue
		}
		seen[e.Name] = struct{}{}
		out = append(out, e.Name+"="+e.Value)
	}
	return out
}

func newTermID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

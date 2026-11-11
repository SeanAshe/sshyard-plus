package agent

import (
	"context"

	"time"

	"github.com/Licoy/sshyard/internal/ai/policy"
)

// Building a write diff discloses the previous content, and needs its own read
// authorization before any bytes are fetched from the host.
func (s *Session) readForDiff(ctx context.Context, write policy.Action, emit func(Event)) (string, bool, error) {
	read := policy.Action{Where: write.Where, Tool: "read_file", Path: write.Path, Reason: write.Reason}
	v, err := s.gate.Authorize(ctx, read, "")
	if err != nil {
		return "", false, err
	}
	if !v.Run {
		if v.Answer == policy.Cancelled {
			return "", false, context.Canceled
		}
		if ctx.Err() != nil {
			return "", false, ctx.Err()
		}
		reason := v.Refusal
		if reason == "" {
			reason = "read permission denied"
		}
		return "", false, &readDenied{reason: reason}
	}
	start := time.Now()
	text, ok, err := s.tools.ReadFile(ctx, read.Path)
	exit := 0
	if err != nil {
		exit = 1
	}
	if ctx.Err() != nil {
		exit = -1
	}
	_ = s.auditLost(emit, s.gate.Done(read, v, exit, time.Since(start).Milliseconds(), int64(len(text))))
	return text, ok, err
}

type readDenied struct{ reason string }

func (e *readDenied) Error() string { return e.reason }

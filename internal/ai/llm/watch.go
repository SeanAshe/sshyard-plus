package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// ErrTimeout is the error when the reply did not start, or stopped coming,
// within Client.Timeout. It is a context.DeadlineExceeded.
var ErrTimeout = fmt.Errorf("llm: no response in time: %w", context.DeadlineExceeded)

// watchdog cancels a request once it has waited d for the reply, the wait
// starting again with every piece of it read.
type watchdog struct {
	ctx    context.Context
	cancel context.CancelCauseFunc
	t      *time.Timer
	d      time.Duration
}

// watch returns ctx, cancelled with ErrTimeout once it waits d, and its
// watchdog. Zero d watches nothing.
func watch(ctx context.Context, d time.Duration) (context.Context, *watchdog) {
	w := &watchdog{d: d}
	if d <= 0 {
		return ctx, w
	}
	w.ctx, w.cancel = context.WithCancelCause(ctx)
	w.t = time.AfterFunc(d, func() { w.cancel(ErrTimeout) })
	return w.ctx, w
}

func (w *watchdog) stop() {
	if w.t != nil {
		w.t.Stop()
		w.cancel(nil)
	}
}

// err is ErrTimeout for an error the watchdog caused, else err.
func (w *watchdog) err(err error) error {
	if err != nil && w.t != nil && errors.Is(context.Cause(w.ctx), ErrTimeout) {
		return ErrTimeout
	}
	return err
}

// reader is r, each piece read from it starting the wait again.
func (w *watchdog) reader(r io.Reader) io.Reader {
	if w.t == nil {
		return r
	}
	return watchedReader{r, w}
}

type watchedReader struct {
	r io.Reader
	w *watchdog
}

func (r watchedReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		r.w.t.Reset(r.w.d)
	}
	return n, err
}

package session

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// Challenge is one round of the questions a server asks in a
// keyboard-interactive login that the saved password does not answer: a
// one-time password, a code sent by text message, a password nothing
// saved answers.
type Challenge struct {
	// Hop names the host asking, the target or one of its jump hosts;
	// User is who logs in there.
	Hop, User string
	// Name and Instruction are what the server says about the
	// questions; either may be empty.
	Name, Instruction string
	// Questions are the prompts; Echo says, for each, whether its answer
	// may show as it is typed.
	Questions []string
	Echo      []bool
}

// Ask answers every question of a Challenge, in order. It is called off
// the UI goroutine and blocks until the user answers, or ctx ends.
type Ask func(ctx context.Context, ch Challenge) ([]string, error)

// ErrChallengeCanceled reports a login whose questions the user did not
// answer: they canceled, or took too long.
var ErrChallengeCanceled = errors.New("session: the server's login questions were not answered")

// ErrChallengeUnattended reports a login asking questions, such as a
// one-time password, of a connection no one watches (an automatic
// forward, MCP, a pane restored at launch).
var ErrChallengeUnattended = errors.New("session: the server asks login questions no one is here to answer")

// errPasswordWanted declines a round asking only for a password the
// login has no answer for, or had one refused: the login fails as a
// refused password does, for the caller to ask for one and connect again.
var errPasswordWanted = errors.New("session: unable to authenticate: the server asks for a password")

// askTimeout bounds how long one Challenge waits for the user: servers
// give up on a login within minutes (OpenSSH's LoginGraceTime is two).
const askTimeout = 3 * time.Minute

// responder answers the keyboard-interactive rounds of one login: the
// first question asking for a password with the saved one, the rest
// through ask.
type responder struct {
	ctx      context.Context
	password string
	// declinePassword fails a round that asks only for passwords the
	// login cannot answer instead of asking them through ask: the caller
	// asks for passwords itself.
	declinePassword bool
	ask             Ask
	hop, user       string
	// stop, when set, is why the login stops before any question: a key
	// file that needs its passphrase.
	stop func() error
	// pause stops the handshake's deadline while the user answers, false
	// when it already passed; resume starts it again.
	pause  func() bool
	resume func()

	used bool  // the password answered a question
	err  error // why a round was not answered
}

func (r *responder) fail(err error) error {
	r.err = err
	return err
}

// answer is an ssh.KeyboardInteractiveChallenge.
func (r *responder) answer(name, instruction string, questions []string, echos []bool) ([]string, error) {
	if r.stop != nil {
		if err := r.stop(); err != nil {
			return nil, r.fail(err)
		}
	}
	answers := make([]string, len(questions))
	var open []int
	for i, q := range questions {
		if r.password != "" && !r.used && passwordPrompt(q) {
			answers[i], r.used = r.password, true
			continue
		}
		open = append(open, i)
	}
	if len(open) == 0 {
		// Nothing asked (a round with only an instruction), or all of it
		// answered.
		return answers, nil
	}
	onlyPasswords := true
	for _, i := range open {
		onlyPasswords = onlyPasswords && passwordPrompt(questions[i])
	}
	if onlyPasswords && (r.ask == nil || r.declinePassword) {
		return nil, r.fail(errPasswordWanted)
	}
	if r.ask == nil {
		return nil, r.fail(ErrChallengeUnattended)
	}
	ch := Challenge{Hop: r.hop, User: r.user, Name: name, Instruction: instruction}
	for _, i := range open {
		ch.Questions = append(ch.Questions, questions[i])
		ch.Echo = append(ch.Echo, i < len(echos) && echos[i])
	}
	if r.pause != nil && !r.pause() {
		return nil, r.fail(context.DeadlineExceeded)
	}
	ctx, cancel := context.WithTimeout(r.ctx, askTimeout)
	got, err := r.ask(ctx, ch)
	cancel()
	if r.resume != nil {
		r.resume()
	}
	switch {
	case r.ctx.Err() != nil:
		return nil, r.fail(r.ctx.Err())
	case err != nil:
		if !errors.Is(err, ErrChallengeCanceled) && !errors.Is(err, ErrChallengeUnattended) {
			err = fmt.Errorf("%w: %w", ErrChallengeCanceled, err)
		}
		return nil, r.fail(err)
	case len(got) != len(open):
		return nil, r.fail(fmt.Errorf("%w: %d answers to %d questions", ErrChallengeCanceled, len(got), len(open)))
	}
	for k, i := range open {
		answers[i] = got[k]
	}
	return answers, nil
}

// passwordWords name a password in the prompts of servers in common
// languages; notPasswordWords mark a prompt naming one that is not the
// login password: a one-time or new one, or a code.
var (
	passwordWords    = []string{"password", "passwort", "mot de passe", "contraseña", "senha", "wachtwoord", "пароль", "密码", "密碼", "パスワード", "비밀번호", "암호"}
	notPasswordWords = []string{
		"new", "retype", "again", "repeat", "confirm", "one-time", "one time", "otp", "token", "code", "verification", "2fa", "mfa", "sms", "pin",
		"neu", "nouveau", "nueva",
		"新", "动态", "動態", "一次性", "验证", "驗證", "确认", "確認", "再次", "重复", "重複", "短信", "簡訊", "令牌", "權杖",
		"ワンタイム", "認証コード", "再入力",
		"일회용", "인증", "새 ", "확인", "다시",
	}
)

// passwordPrompt reports whether question q asks for the login password,
// which a saved password answers.
func passwordPrompt(q string) bool {
	q = strings.ToLower(q)
	found := false
	for _, w := range passwordWords {
		if strings.Contains(q, w) {
			found = true
			break
		}
	}
	if !found {
		return false
	}
	for _, w := range notPasswordWords {
		if strings.Contains(q, w) {
			return false
		}
	}
	return true
}

// handshakeClock bounds an SSH handshake by a deadline that stops while
// the user answers the server's questions, and starts again afterwards, a
// whole timeout from then. Its context ends at the deadline or with the
// parent's.
type handshakeClock struct {
	ctx     context.Context
	cancel  context.CancelFunc
	conn    net.Conn
	timeout time.Duration
	timer   *time.Timer
}

func startClock(parent context.Context, conn net.Conn, deadline time.Time, timeout time.Duration) *handshakeClock {
	ctx, cancel := context.WithCancel(parent)
	conn.SetDeadline(deadline)
	return &handshakeClock{ctx: ctx, cancel: cancel, conn: conn, timeout: timeout, timer: time.AfterFunc(time.Until(deadline), cancel)}
}

// pause stops the deadline; false when it already passed.
func (h *handshakeClock) pause() bool {
	if !h.timer.Stop() {
		return false
	}
	h.conn.SetDeadline(time.Time{})
	return true
}

func (h *handshakeClock) resume() {
	h.conn.SetDeadline(time.Now().Add(h.timeout))
	h.timer.Reset(h.timeout)
}

func (h *handshakeClock) stop() {
	h.timer.Stop()
	h.cancel()
}

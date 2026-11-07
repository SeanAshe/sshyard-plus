package app

import (
	"errors"
	"io"
	"os"
	"testing"

	"github.com/Licoy/sshyard/internal/session"
)

// TestMain keeps the tests off the SSH agent of the machine running them:
// hosts that ask for their password try the agent's keys first. A test
// that needs an agent serves its own.
func TestMain(m *testing.M) {
	session.DialAgent = func() (io.ReadWriteCloser, error) { return nil, errors.New("no agent in tests") }
	os.Exit(m.Run())
}

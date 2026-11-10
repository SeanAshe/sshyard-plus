package sftpsvc

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// fakeSudo is sudo's stderr, read in the steps given, and the stdin the
// handshake answers on.
func fakeSudo(t *testing.T, steps ...string) (io.Reader, *bytes.Buffer) {
	t.Helper()
	var stdin bytes.Buffer
	return io.MultiReader(stringsReaders(steps)...), &stdin
}

func stringsReaders(ss []string) []io.Reader {
	out := make([]io.Reader, len(ss))
	for i, s := range ss {
		out[i] = strings.NewReader(s)
	}
	return out
}

func TestSudoHandshakeWithoutPrompt(t *testing.T) {
	stderr, stdin := fakeSudo(t, sudoReady+"\n")
	if err := sudoHandshake(stderr, stdin, "secret"); err != nil {
		t.Fatal(err)
	}
	if stdin.Len() != 0 {
		t.Fatalf("password sent though sudo did not ask: %q", stdin.String())
	}
}

func TestSudoHandshakeAnswersPrompt(t *testing.T) {
	// The prompt arrives split across reads.
	stderr, stdin := fakeSudo(t, sudoPrompt[:5], sudoPrompt[5:], "\n"+sudoReady+"\n")
	if err := sudoHandshake(stderr, stdin, "secret"); err != nil {
		t.Fatal(err)
	}
	if stdin.String() != "secret\n" {
		t.Fatalf("stdin = %q", stdin.String())
	}
}

func TestSudoHandshakeNoPassword(t *testing.T) {
	stderr, stdin := fakeSudo(t, sudoPrompt)
	if err := sudoHandshake(stderr, stdin, ""); !errors.Is(err, ErrSudoPassword) {
		t.Fatalf("err = %v", err)
	}
	if stdin.Len() != 0 {
		t.Fatalf("stdin = %q", stdin.String())
	}
}

func TestSudoHandshakeWrongPassword(t *testing.T) {
	stderr, stdin := fakeSudo(t, sudoPrompt, "\nSorry, try again.\n"+sudoPrompt)
	if err := sudoHandshake(stderr, stdin, "wrong"); !errors.Is(err, ErrSudoPassword) {
		t.Fatalf("err = %v", err)
	}
	if stdin.String() != "wrong\n" {
		t.Fatalf("password sent %q, want once", stdin.String())
	}
}

func TestSudoHandshakeRefused(t *testing.T) {
	stderr, stdin := fakeSudo(t, "alice is not in the sudoers file.\n")
	err := sudoHandshake(stderr, stdin, "secret")
	if !errors.Is(err, ErrSudo) || !strings.Contains(err.Error(), "not in the sudoers file") {
		t.Fatalf("err = %v", err)
	}
}

func TestSudoHandshakeNoServer(t *testing.T) {
	stderr, stdin := fakeSudo(t, sudoNoServer+"\n")
	if err := sudoHandshake(stderr, stdin, ""); !errors.Is(err, ErrSudoNoServer) || !errors.Is(err, ErrSudo) {
		t.Fatalf("err = %v", err)
	}
}

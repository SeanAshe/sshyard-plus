package app

import (
	"errors"
	"io"
	"strings"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/vt"
	"github.com/egoist/mygo/ui"
)

var (
	errSnippetNoTerminal   = errors.New("snippet: no focused terminal")
	errSnippetDisconnected = errors.New("snippet: terminal disconnected")
	errSnippetUnsafePaste  = errors.New("snippet: bracketed paste required")
	errSnippetSend         = errors.New("snippet: input write failed")
	errSnippetBusy         = errors.New("snippet: input write in progress")
)

// sendSnippet validates and starts one write to the currently focused shell.
// nil means the write started; snippetSendResult reports its completion on the
// UI thread. Neither result confirms that a command executed successfully.
func (a *App) sendSnippet(id string, sendEnter bool) error {
	return a.sendInput(func() (string, error) { return a.snippetContent(id) }, sendEnter)
}

// sendInput is sendSnippet for the text content reads once the terminal is
// known to take it: a snippet's body, or a command of the history.
func (a *App) sendInput(content func() (string, error), sendEnter bool) error {
	c := a.focused()
	if c == nil || c.kind != layout.Terminal || c.tv == nil || c.s == nil {
		return errSnippetNoTerminal
	}
	if c.s.Status().State != session.Connected {
		return errSnippetDisconnected
	}
	v := c.tv
	if v.snippetSending {
		return errSnippetBusy
	}
	w, err := c.s.CaptureInput()
	if err != nil {
		return errSnippetDisconnected
	}
	text, err := content()
	if err != nil {
		return err
	}
	payload, err := snippetBytes(text, sendEnter, v.target.Terminal().Modes())
	if err != nil {
		return err
	}
	v.snippetSending = true
	a.focusReq = c.id
	go func() {
		err := writeSnippet(w, payload)
		a.post(func() {
			v.snippetSending = false
			if err == nil {
				v.offset, v.scrollAcc = 0, 0
				v.selected, v.selecting = false, false
			}
			a.snippetSendResult(err)
		})
	}()
	return nil
}

func snippetBytes(content string, sendEnter bool, modes vt.Modes) ([]byte, error) {
	if err := assets.ValidateSnippetContent(content); err != nil {
		return nil, err
	}
	content = strings.ReplaceAll(strings.ReplaceAll(content, "\r\n", "\n"), "\r", "\n")
	content = strings.TrimRight(content, "\n")
	// A TAB outside bracketed paste can trigger completion, even in execute
	// mode. Only an explicit execute action may submit multiline input without
	// bracketed paste; insert-only must never execute an embedded newline.
	if !modes.BracketedPaste && (strings.Contains(content, "\t") || !sendEnter && strings.Contains(content, "\n")) {
		return nil, errSnippetUnsafePaste
	}
	payload := pasteBytes(content, modes)
	if sendEnter {
		enter, _ := EncodeKey(ui.KeyEnter, 0, modes)
		payload = append(payload, enter...)
	}
	return payload, nil
}

func writeSnippet(w io.Writer, payload []byte) error {
	n, err := w.Write(payload)
	if errors.Is(err, session.ErrInputBusy) {
		return errSnippetBusy
	}
	if err != nil {
		return errors.Join(errSnippetSend, err)
	}
	if n != len(payload) {
		return errors.Join(errSnippetSend, io.ErrShortWrite)
	}
	return nil
}

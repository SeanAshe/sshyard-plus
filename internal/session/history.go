package session

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"

	"github.com/Licoy/sshyard/internal/shellhist"
	"golang.org/x/crypto/ssh"
)

// The command history panel reads a server's shell history files, the
// user's own, through one exec of sh: the end of each file there, after a
// line of a random mark, its shell, its size and its path. A history file
// holds any text, but never a mark made for this read.

// historyScript prints the login shell, then each history file there, its
// last TailBytes; $1 is the mark. It is read from stdin (sh -s), as the
// metrics script is, away from the login shell's quoting.
var historyScript = `m=$1
printf '%s login %s\n' "$m" "${SHELL:-}"
z=${ZDOTDIR:-$HOME}
d=${XDG_DATA_HOME:-$HOME/.local/share}
seen=
for e in "zsh $z/.zsh_history" "zsh $HOME/.zsh_history" "zsh $HOME/.zhistory" "bash $HOME/.bash_history" "fish $d/fish/fish_history" "sh $HOME/.sh_history" "sh $HOME/.ash_history" "pwsh $HOME/.local/share/powershell/PSReadLine/ConsoleHost_history.txt"; do
  k=${e%% *}; f=${e#* }
  [ -f "$f" ] && [ -r "$f" ] || continue
  case "$seen" in *"|$f|"*) continue;; esac
  seen="$seen|$f|"
  s=$(wc -c < "$f" 2>/dev/null | tr -d ' ')
  printf '\n%s file %s %s %s\n' "$m" "$k" "${s:-0}" "$f"
  tail -c ` + strconv.Itoa(shellhist.TailBytes) + ` "$f" 2>/dev/null
done
`

// historyMark is a fresh mark for one read.
func historyMark() string {
	var b [12]byte
	rand.Read(b[:])
	return "@SSHYARD-HIST-" + hex.EncodeToString(b[:])
}

// ReadHistory reads the shell history of client's user on the server.
func ReadHistory(ctx context.Context, client *ssh.Client) (shellhist.History, error) {
	mark := historyMark()
	var out, errb bytes.Buffer
	err := execStarted(ctx, client, "sh -s -- "+mark, execStreams{
		stdin:  strings.NewReader(historyScript),
		stdout: &out,
		stderr: &errb,
	}, nil)
	if err != nil {
		var exit *ssh.ExitError
		if errors.As(err, &exit) {
			if msg := strings.TrimSpace(errb.String()); msg != "" {
				return shellhist.History{}, errors.New(lastLine(msg))
			}
		}
		return shellhist.History{}, err
	}
	files, login := ParseHistoryDump(out.Bytes(), mark)
	h := shellhist.Merge(files, shellhist.MaxEntries)
	h.Login = shellhist.LoginShell(login)
	return h, nil
}

// ParseHistoryDump splits what historyScript printed with mark into the
// files it read and the login shell's path.
func ParseHistoryDump(out []byte, mark string) ([]shellhist.File, string) {
	var files []shellhist.File
	login := ""
	head := []byte(mark + " ")
	cur := -1
	rest := out
	for len(rest) > 0 {
		line := rest
		next := []byte(nil)
		if i := bytes.IndexByte(rest, '\n'); i >= 0 {
			line, next = rest[:i+1], rest[i+1:]
		}
		rest = next
		if !bytes.HasPrefix(line, head) {
			if cur >= 0 {
				files[cur].Data = append(files[cur].Data, line...)
			}
			continue
		}
		f := strings.Fields(strings.TrimSpace(string(line[len(head):])))
		switch {
		case len(f) >= 1 && f[0] == "login":
			if len(f) > 1 {
				login = f[1]
			}
		case len(f) >= 3 && f[0] == "file":
			size, _ := strconv.ParseInt(f[2], 10, 64)
			// The line break before the mark is the script's, not the
			// file's: the previous file loses it.
			if cur >= 0 {
				files[cur].Data = bytes.TrimSuffix(files[cur].Data, []byte("\n"))
			}
			files = append(files, shellhist.File{Shell: f[1], Cut: size > shellhist.TailBytes})
			cur = len(files) - 1
		}
	}
	return files, login
}

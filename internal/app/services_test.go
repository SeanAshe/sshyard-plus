package app

import (
	"io"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

const serviceFixture = `@V
systemd 255 (255.4-1)
@S
running
@L
Id=nginx.service
Description=A web server
ActiveState=active
UnitFileState=enabled
FragmentPath=/etc/systemd/system/nginx.service
LoadState=loaded

Id=ssh.service
Description=OpenSSH
ActiveState=active
UnitFileState=enabled
FragmentPath=/lib/systemd/system/ssh.service
LoadState=loaded

Id=ModemManager.service
Description=Modem Manager
ActiveState=inactive
UnitFileState=disabled
FragmentPath=/lib/systemd/system/ModemManager.service
LoadState=loaded
`

// svcExec plays a host whose services systemd runs.
type svcExec struct {
	mu      sync.Mutex
	actions []string
}

func (s *svcExec) run(ch ssh.Channel, cmd string) int {
	if !strings.Contains(cmd, "systemctl") && !strings.Contains(cmd, "journalctl") {
		return 1
	}
	switch {
	case strings.Contains(cmd, "list-unit-files"):
		io.WriteString(ch, serviceFixture)
	case strings.Contains(cmd, "journalctl"):
		io.WriteString(ch, "nginx started\n")
	case strings.Contains(cmd, "MainPID"):
		io.WriteString(ch, "Id=nginx.service\nDescription=A web server\nLoadState=loaded\nActiveState=active\nSubState=running\nUnitFileState=enabled\nFragmentPath=/etc/systemd/system/nginx.service\nMainPID=781\nMemoryCurrent=1990656\nTasksCurrent=5\nNRestarts=1\nExecMainStatus=0\nActiveEnterTimestamp=Tue 2026-09-15 17:49:07 CST\n")
	default:
		s.mu.Lock()
		s.actions = append(s.actions, cmd)
		s.mu.Unlock()
		io.WriteString(ch, "@R 0\n")
	}
	return 0
}

func (s *svcExec) has(part string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.actions {
		if strings.Contains(c, part) {
			return true
		}
	}
	return false
}

// TestServicesPanel lists a host's services, stops one, and opens its details.
func TestServicesPanel(t *testing.T) {
	ex := &svcExec{}
	srv := startMetricSrv(t, ex.run)
	a, tt := newTestApp(t)
	tt.SetSize(1400, 900)
	connected(t, a, tt, srv)
	mustClick(t, tt, "System Services")
	tt.Frame()
	waitFor(t, tt, "the services", func() bool {
		return tt.HasText("nginx.service") && tt.HasText("Custom") && tt.HasText("systemd 255 · Running")
	})
	for _, s := range []string{"A web server", "ssh.service", "Modem Manager", "Active", "Enabled"} {
		if !tt.HasText(s) {
			t.Errorf("missing %q: %q", s, tt.Texts())
		}
	}

	mustClick(t, tt, "nginx.service")
	waitFor(t, tt, "the details", func() bool { return tt.HasText("781") && tt.HasText("1.9 MB") })
	mustClick(t, tt, "Close")
	tt.Frame()

	mustClick(t, tt, "Stop")
	waitFor(t, tt, "stop", func() bool { return ex.has("systemctl stop") && ex.has("nginx.service") })

	a.services.filter = 2
	tt.Frame()
	waitFor(t, tt, "the stopped ones", func() bool { return tt.HasText("ModemManager.service") && !tt.HasText("nginx.service") })
}

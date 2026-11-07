package app

import (
	"io"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Licoy/sshyard/internal/session"
	"golang.org/x/crypto/ssh"
)

// dockerExec plays a server with Docker: two containers and an image, and
// the logs of any container. It keeps the actions it was asked for.
type dockerExec struct {
	mu      sync.Mutex
	actions []string
	missing bool
	// out, when set, is the whole list the server prints.
	out string
}

func (d *dockerExec) run(ch ssh.Channel, cmd string) int {
	if !strings.Contains(cmd, "command -v docker") {
		return 1
	}
	if d.missing {
		return 127
	}
	switch {
	case strings.Contains(cmd, "ps -a --no-trunc"):
		if d.out != "" {
			io.WriteString(ch, d.out)
			return 0
		}
		io.WriteString(ch, `@V 27.1.0
@D docker
@C
{"ID":"c0ffee000001","Names":"web","Image":"nginx:1.27","State":"running","Status":"Up 2 hours","Ports":"0.0.0.0:80->80/tcp","RunningFor":"2 hours ago"}
{"ID":"c0ffee000002","Names":"db","Image":"postgres:16","State":"exited","Status":"Exited (0) 3 days ago","Ports":"","RunningFor":"3 days ago"}
@I
{"ID":"sha256:5ca1ab1e0000","Repository":"nginx","Tag":"1.27","Size":"187MB","CreatedSince":"3 weeks ago"}
`)
	case strings.Contains(cmd, "$D compose"):
		d.mu.Lock()
		d.actions = append(d.actions, cmd)
		d.mu.Unlock()
		if strings.Contains(cmd, "logs --tail") {
			io.WriteString(ch, "shop is up\n")
		}
	case strings.Contains(cmd, "logs --tail"):
		io.WriteString(ch, "listening on :80\n")
	default:
		d.mu.Lock()
		d.actions = append(d.actions, cmd)
		d.mu.Unlock()
	}
	return 0
}

// did reports whether the action ran with the reference.
func (d *dockerExec) did(action, ref string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range d.actions {
		if strings.Contains(c, "$D "+action+` "$1"`) && strings.HasSuffix(c, " _ '"+ref+"'") {
			return true
		}
	}
	return false
}

func (d *dockerExec) has(part string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range d.actions {
		if strings.Contains(c, part) {
			return true
		}
	}
	return false
}

// TestDockerPanel: the Docker panel lists the containers and the images,
// and stops, removes and reads the logs of the container chosen.
func TestDockerPanel(t *testing.T) {
	ex := &dockerExec{}
	srv := startMetricSrv(t, ex.run)
	a, tt := newTestApp(t)
	tt.SetSize(1400, 900)
	connected(t, a, tt, srv)
	mustClick(t, tt, "Docker")
	tt.Frame()
	if !a.panelOpen(inspectDocker) {
		t.Fatalf("panels %q", a.panels)
	}
	waitFor(t, tt, "the containers", func() bool { return tt.HasText("web") && tt.HasText("db") })
	for _, s := range []string{"Containers (2)", "Images (1)", "nginx:1.27", "Up 2 hours", "Select a container to manage it."} {
		if !tt.HasText(s) {
			t.Errorf("missing %q: %q", s, tt.Texts())
		}
	}

	a.docker.ctrSel = 0
	tt.Frame()
	if !tt.HasText("0.0.0.0:80->80/tcp") || !tt.HasText("c0ffee000001") {
		t.Fatalf("card: %q", tt.Texts())
	}
	mustClick(t, tt, "Stop")
	waitFor(t, tt, "stop", func() bool { return ex.did("stop", "c0ffee000001") })
	waitFor(t, tt, "done", func() bool { return a.docker.busy == "" })

	mustClick(t, tt, "Logs")
	waitFor(t, tt, "the logs", func() bool { return tt.HasText("Logs of web") && tt.HasText("listening on :80\n") })
	mustClick(t, tt, "Close")
	tt.Frame()

	mustClick(t, tt, "Remove Container")
	tt.Frame()
	if !tt.HasText("Remove the container “web”? It is stopped first, and its data that is not in a volume is lost.") {
		t.Fatalf("no confirmation: %q", tt.Texts())
	}
	mustClick(t, tt, "Delete")
	waitFor(t, tt, "rm", func() bool { return ex.did("rm -f", "c0ffee000001") })
	waitFor(t, tt, "done", func() bool { return a.docker.busy == "" })

	a.docker.view = 1
	tt.Frame()
	if !tt.HasText("187MB") || !tt.HasText("3 weeks ago") {
		t.Fatalf("images: %q", tt.Texts())
	}
	a.docker.imgSel = 0
	tt.Frame()
	mustClick(t, tt, "Remove Image")
	tt.Frame()
	mustClick(t, tt, "Delete")
	waitFor(t, tt, "rmi", func() bool { return ex.did("rmi", "sha256:5ca1ab1e0000") })
}

// composeList is two containers of one project and one on its own.
const composeList = `@V 27.1.0
@D docker
@C
{"ID":"c0ffee000001","Names":"web","Image":"nginx:1.27","State":"running","Status":"Up 2 hours","Ports":"0.0.0.0:80->80/tcp","RunningFor":"2 hours ago","Labels":"com.docker.compose.project=shop,com.docker.compose.service=web,com.docker.compose.project.working_dir=/srv/shop,com.docker.compose.project.config_files=/srv/shop/compose.yml"}
{"ID":"c0ffee000002","Names":"db","Image":"postgres:16","State":"exited","Status":"Exited (0) 3 days ago","Ports":"","RunningFor":"3 days ago","Labels":"com.docker.compose.project=shop,com.docker.compose.service=db,com.docker.compose.project.working_dir=/srv/shop,com.docker.compose.project.config_files=/srv/shop/compose.yml"}
{"ID":"c0ffee000003","Names":"redis","Image":"redis:7","State":"running","Status":"Up 1 hour","Ports":"","RunningFor":"1 hour ago"}
@I
{"ID":"sha256:5ca1ab1e0000","Repository":"nginx","Tag":"1.27","Size":"187MB","CreatedSince":"3 weeks ago"}
`

// TestDockerCompose: containers of a project sit under it, and its menu
// runs compose. A container's menu has the same commands; one with no
// project does not.
func TestDockerCompose(t *testing.T) {
	ex := &dockerExec{out: composeList}
	srv := startMetricSrv(t, ex.run)
	a, tt := newTestApp(t)
	tt.SetSize(1400, 900)
	connected(t, a, tt, srv)
	a.panels = []inspectorKind{inspectDocker}
	tt.Frame()
	waitFor(t, tt, "the project", func() bool {
		return tt.HasText("shop") && tt.HasText("web") && tt.HasText("db") && tt.HasText("redis") && tt.HasText("1 of 2 running")
	})

	if err := tt.RightClick("shop"); err != nil {
		t.Fatal(err)
	}
	got := tt.Menu()
	for _, want := range []string{"Compose Up", "Compose Down", "Compose Restart", "Compose Stop", "Compose Start", "Compose Logs"} {
		if !slices.Contains(got, want) {
			t.Errorf("project menu %q lacks %q", got, want)
		}
	}
	if slices.Contains(got, "Stop") || slices.Contains(got, "Remove Container") {
		t.Errorf("project menu has container commands: %q", got)
	}
	if err := tt.ChooseMenuItem("Compose Stop"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "compose stop", func() bool {
		return ex.has("$D compose -p ") && ex.has("/srv/shop/compose.yml") && ex.has(" stop")
	})
	waitFor(t, tt, "done", func() bool { return a.docker.busy == "" })

	if err := tt.RightClick("web"); err != nil {
		t.Fatal(err)
	}
	got = tt.Menu()
	for _, want := range []string{"Stop", "Compose Up", "Compose Down", "Compose Logs"} {
		if !slices.Contains(got, want) {
			t.Errorf("container menu %q lacks %q", got, want)
		}
	}
	tt.CloseMenu()

	if err := tt.RightClick("redis"); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(tt.Menu(), "Compose Up") {
		t.Errorf("a container without a project has compose commands: %q", tt.Menu())
	}
	tt.CloseMenu()

	if err := tt.RightClick("shop"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Compose Down"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Stop and remove the containers of “shop”? Data that is not in a volume is lost.") {
		t.Fatalf("no confirmation: %q", tt.Texts())
	}
	mustClick(t, tt, "Delete")
	waitFor(t, tt, "compose down", func() bool { return ex.has(" down") })

	if err := tt.RightClick("shop"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Compose Logs"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "the logs", func() bool { return tt.HasText("Compose logs of shop") && tt.HasText("shop is up\n") })
}

// TestDockerShell: opening a shell in a container opens a terminal on its
// host that runs docker exec once connected.
func TestDockerShell(t *testing.T) {
	ex := &dockerExec{}
	srv := startMetricSrv(t, ex.run)
	a, tt := newTestApp(t)
	tt.SetSize(1400, 900)
	connected(t, a, tt, srv)
	a.panels = []inspectorKind{inspectDocker}
	tt.Frame()
	waitFor(t, tt, "the containers", func() bool { return tt.HasText("web") })
	a.docker.ctrSel = 0
	tt.Frame()
	tabs := len(a.ws.Tabs)
	mustClick(t, tt, "Open Shell in Container")
	if len(a.ws.Tabs) != tabs+1 {
		t.Fatalf("tabs %d, want %d", len(a.ws.Tabs), tabs+1)
	}
	cn := a.focused()
	waitFor(t, tt, "connected", func() bool { return cn.s.Status().State == session.Connected })
	waitFor(t, tt, "the command", func() bool {
		return startOnce(cn) == "" && strings.Contains(cn.s.Term.ScreenText(), "docker exec -it c0ffee000001")
	})
}

// TestDockerMissing: a server without Docker says so.
func TestDockerMissing(t *testing.T) {
	srv := startMetricSrv(t, (&dockerExec{missing: true}).run)
	a, tt := newTestApp(t)
	connected(t, a, tt, srv)
	a.panels = []inspectorKind{inspectDocker}
	tt.Frame()
	waitFor(t, tt, "the note", func() bool { return tt.HasText("Docker is not installed on this server.") })
}

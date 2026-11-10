package app

import (
	"io"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Licoy/sshyard/internal/layout"
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
	case strings.Contains(cmd, "image inspect"):
		io.WriteString(ch, `@J
[{"Id":"sha256:5ca1ab1e0000","RepoTags":["nginx:1.27"],"Os":"linux","Architecture":"amd64","Config":{"Cmd":["nginx","-g","daemon off;"]},"RootFS":{"Layers":["sha256:a"]}}]
@H
{"ID":"sha256:5ca1ab1e0000","CreatedBy":"CMD [\"nginx\"]","CreatedSince":"3 weeks ago","Size":"0B"}
@C
{"ID":"c0ffee000001","Names":"web","Image":"nginx:1.27","State":"running","Status":"Up 2 hours"}
`)
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
@N
{"ID":"net1","Name":"bridge","Driver":"bridge","Scope":"local"}
@U
{"Name":"pgdata","Driver":"local","Mountpoint":"/var/lib/docker/volumes/pgdata/_data","Scope":"local"}
`)
	case strings.Contains(cmd, "inspect --type container"):
		io.WriteString(ch, `@J
[{"Id":"c0ffee000001","Name":"/web","Path":"nginx","Args":["-g","daemon off;"],"State":{"Status":"running","Pid":42},
"Config":{"Env":["PATH=/usr/bin"],"Image":"nginx:1.27"},"Mounts":[{"Type":"volume","Name":"html","Destination":"/usr/share/nginx/html","RW":true}]}]
@S
{"CPUPerc":"3.50%","MemPerc":"1.20%","MemUsage":"24MiB / 1.9GiB","NetIO":"1kB / 2kB","BlockIO":"0B / 0B","PIDs":"3"}
@T
PID USER %CPU %MEM ELAPSED COMMAND
42 root 0.0 0.1 01:02 nginx: master process nginx -g daemon off;
`)
	case strings.Contains(cmd, "network inspect"), strings.Contains(cmd, "volume inspect"):
		io.WriteString(ch, "[{\"Name\":\"bridge\",\"Driver\":\"bridge\"}]\n")
	case strings.Contains(cmd, "prune"):
		d.mu.Lock()
		d.actions = append(d.actions, cmd)
		d.mu.Unlock()
		io.WriteString(ch, "Total reclaimed space: 12MB\n")
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

// TestDockerPanel: the Docker panel lists the containers and the images;
// a right click opens the app's own menu of a row, a click its details,
// read from docker inspect, stats and top for a container, image inspect,
// history and the containers made from it for an image; the containers
// are stopped, removed and their logs read, and the images removed.
func TestDockerPanel(t *testing.T) {
	ex := &dockerExec{}
	srv := startMetricSrv(t, ex.run)
	a, tt := newTestApp(t)
	tt.SetSize(1400, 900)
	connected(t, a, tt, srv)
	mustClick(t, tt, "Docker")
	tt.Frame()
	if !a.toolHere(layout.Docker) {
		t.Fatal("the pane did not open")
	}
	waitFor(t, tt, "the containers", func() bool { return tt.HasText("web") && tt.HasText("db") })
	for _, s := range []string{"Containers (2)", "Images (1)", "nginx:1.27", "Up 2 hours", "Select a container to manage it."} {
		if !tt.HasText(s) {
			t.Errorf("missing %q: %q", s, tt.Texts())
		}
	}

	if err := tt.RightClick("db"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	want := []string{"Details", "-", "Start", "Restart", "Pause", "-", "Logs", "Open Shell in Container", "-", "Remove Container"}
	if got := menuOf(a); !slices.Equal(got, want) || a.docker.detailOpen {
		t.Fatalf("menu %q (details open %v), want %q", got, a.docker.detailOpen, want)
	}
	if err := chooseMenu(a, tt, "Start"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "start", func() bool { return ex.did("start", "c0ffee000002") })
	waitFor(t, tt, "done", func() bool { return a.docker.busy == "" })

	mustClick(t, tt, "web")
	tt.Frame()
	if d := a.docker; !d.detailOpen || d.detailRef != "c0ffee000001" || d.ctrSel != 0 {
		t.Fatalf("the click opened %q (open %v, row %d)", d.detailRef, d.detailOpen, d.ctrSel)
	}
	waitFor(t, tt, "the details", func() bool { return a.docker.detailRead })
	det := a.docker.detail
	if det.Inspect.Command() != "nginx -g daemon off;" || det.Inspect.State.Pid != 42 || len(det.Inspect.Mounts) != 1 {
		t.Errorf("inspect %+v", det.Inspect)
	}
	if det.Stats == nil || det.Stats.PIDs != "3" || session.Percent(det.Stats.CPU) != 3.5 {
		t.Errorf("stats %+v", det.Stats)
	}
	if len(det.Top.Rows) != 1 || det.Top.Rows[0][5] != "nginx: master process nginx -g daemon off;" {
		t.Errorf("top %+v", det.Top)
	}
	mustClick(t, tt, "Stop")
	waitFor(t, tt, "stop", func() bool { return ex.did("stop", "c0ffee000001") })
	waitFor(t, tt, "done", func() bool { return a.docker.busy == "" })

	mustClick(t, tt, "Logs")
	waitFor(t, tt, "the logs", func() bool { return tt.HasText("Logs of web") && tt.HasText("listening on :80\n") })
	a.docker.logsOpen = false
	tt.Frame()

	mustClick(t, tt, "Remove Container")
	tt.Frame()
	if !tt.HasText("Remove the container “web”? It is stopped first, and its data that is not in a volume is lost.") {
		t.Fatalf("no confirmation: %q", tt.Texts())
	}
	mustClick(t, tt, "Delete")
	waitFor(t, tt, "rm", func() bool { return ex.did("rm -f", "c0ffee000001") })
	waitFor(t, tt, "done", func() bool { return a.docker.busy == "" })

	// Closed, the details leave no row chosen, so a click opens them again.
	a.docker.detailOpen = false
	tt.Frame()
	if a.docker.ctrSel != -1 {
		t.Errorf("row %d still chosen", a.docker.ctrSel)
	}

	a.docker.view = 1
	tt.Frame()
	if !tt.HasText("187MB") || !tt.HasText("3 weeks ago") {
		t.Fatalf("images: %q", tt.Texts())
	}
	if err := tt.RightClick("187MB"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := menuOf(a); !slices.Equal(got, []string{"Details", "Pull", "-", "Remove Image"}) {
		t.Fatalf("image menu %q", got)
	}
	if err := chooseMenu(a, tt, "Details"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "the image", func() bool { return a.docker.imgRead })
	img := a.docker.img
	if img.Inspect.Platform() != "linux/amd64" || len(img.History) != 1 || len(img.Containers) != 1 || img.Containers[0].Name != "web" {
		t.Errorf("image %+v", img)
	}
	a.docker.imgOpen = false
	tt.Frame()
	mustClick(t, tt, "187MB")
	tt.Frame()
	if !a.docker.imgOpen || a.docker.imgRef != "sha256:5ca1ab1e0000" {
		t.Fatalf("the click opened %q (open %v)", a.docker.imgRef, a.docker.imgOpen)
	}
	mustClick(t, tt, "Remove Image")
	tt.Frame()
	mustClick(t, tt, "Delete")
	waitFor(t, tt, "rmi", func() bool { return ex.did("rmi", "sha256:5ca1ab1e0000") })
}

// TestDockerNetVol: a network or a volume opens its inspect on a click,
// an image can be pulled, and prune asks before it removes unused objects.
func TestDockerNetVol(t *testing.T) {
	ex := &dockerExec{}
	srv := startMetricSrv(t, ex.run)
	a, tt := newTestApp(t)
	tt.SetSize(1400, 900)
	connected(t, a, tt, srv)
	a.ShowTool(layout.Docker)
	tt.Frame()
	waitFor(t, tt, "the lists", func() bool { return tt.HasText("Networks (1)") && tt.HasText("Volumes (1)") })

	a.docker.view = 2
	tt.Frame()
	if !tt.HasText("bridge") || !tt.HasText("local") {
		t.Fatalf("networks: %q", tt.Texts())
	}
	mustClick(t, tt, "bridge")
	waitFor(t, tt, "inspect", func() bool { return a.docker.inspectOpen && tt.HasText("\"Driver\": \"bridge\"") })
	mustClick(t, tt, "Close")
	tt.Frame()

	a.docker.view = 3
	tt.Frame()
	mustClick(t, tt, "pgdata")
	waitFor(t, tt, "volume inspect", func() bool {
		return a.docker.inspectOpen && a.docker.inspectKind == session.InspectVolume && a.docker.inspectRef == "pgdata"
	})
	mustClick(t, tt, "Close")
	tt.Frame()

	a.docker.view = 1
	tt.Frame()
	if err := tt.RightClick("nginx:1.27"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := chooseMenu(a, tt, "Pull"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "pull", func() bool { return ex.did("pull", "nginx:1.27") })
	waitFor(t, tt, "done", func() bool { return a.docker.busy == "" })

	mustClick(t, tt, "Prune images")
	tt.Frame()
	if !tt.HasText("Remove all dangling images?") {
		t.Fatalf("no prune confirmation: %q", tt.Texts())
	}
	mustClick(t, tt, "Prune")
	waitFor(t, tt, "prune", func() bool { return ex.has("image prune -f") })
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
	a.ShowTool(layout.Docker)
	tt.Frame()
	waitFor(t, tt, "the project", func() bool {
		return tt.HasText("shop") && tt.HasText("web") && tt.HasText("db") && tt.HasText("redis") && tt.HasText("1 of 2 running")
	})

	if err := tt.RightClick("shop"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	got := menuOf(a)
	for _, want := range []string{"Compose Up", "Compose Down", "Compose Restart", "Compose Stop", "Compose Start", "Compose Logs"} {
		if !slices.Contains(got, want) {
			t.Errorf("project menu %q lacks %q", got, want)
		}
	}
	if slices.Contains(got, "Stop") || slices.Contains(got, "Remove Container") {
		t.Errorf("project menu has container commands: %q", got)
	}
	if err := chooseMenu(a, tt, "Compose Stop"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "compose stop", func() bool {
		return ex.has("$D compose -p ") && ex.has("/srv/shop/compose.yml") && ex.has(" stop")
	})
	waitFor(t, tt, "done", func() bool { return a.docker.busy == "" })

	if err := tt.RightClick("web"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	got = menuOf(a)
	for _, want := range []string{"Stop", "Compose Up", "Compose Down", "Compose Logs"} {
		if !slices.Contains(got, want) {
			t.Errorf("container menu %q lacks %q", got, want)
		}
	}
	a.menu, a.ctx.entries = dropMenu{}, nil
	tt.Frame()

	if err := tt.RightClick("redis"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if slices.Contains(menuOf(a), "Compose Up") {
		t.Errorf("a container without a project has compose commands: %q", menuOf(a))
	}
	a.menu, a.ctx.entries = dropMenu{}, nil
	tt.Frame()

	if err := tt.RightClick("shop"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := chooseMenu(a, tt, "Compose Down"); err != nil {
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
	tt.Frame()
	if err := chooseMenu(a, tt, "Compose Logs"); err != nil {
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
	a.ShowTool(layout.Docker)
	tt.Frame()
	waitFor(t, tt, "the containers", func() bool { return tt.HasText("web") })
	a.openContainer(a.docker.info.Containers[0])
	tt.Frame()
	tabs := len(a.ws.Tabs)
	mustClick(t, tt, "Open Shell in Container")
	if len(a.ws.Tabs) != tabs+1 || a.docker.detailOpen {
		t.Fatalf("tabs %d, want %d; details open %v", len(a.ws.Tabs), tabs+1, a.docker.detailOpen)
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
	a.ShowTool(layout.Docker)
	tt.Frame()
	waitFor(t, tt, "the note", func() bool { return tt.HasText("Docker is not installed on this server.") })
}

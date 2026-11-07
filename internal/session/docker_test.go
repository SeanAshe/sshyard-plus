package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeDocker puts a docker on PATH that answers as script says, and
// returns the environment to run under.
func fakeDocker(t *testing.T, script string) []string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	// sudo must not be found, so that a refusal is not tried again with it:
	// PATH has only the fake docker and grep, as /usr/bin holds sudo, which
	// needs no password on CI runners.
	grep, err := exec.LookPath("grep")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(grep, filepath.Join(dir, "grep")); err != nil {
		t.Fatal(err)
	}
	return []string{"PATH=" + dir}
}

// TestDockerListScript runs the list script on a fake docker and reads
// the containers and images it prints.
func TestDockerListScript(t *testing.T) {
	env := fakeDocker(t, `case "$1" in
version) echo 27.1.0;;
ps) echo '{"ID":"abc123","Names":"web","Image":"nginx:1.27","State":"running","Status":"Up 2 hours","Ports":"0.0.0.0:80->80/tcp","RunningFor":"2 hours ago"}'
    echo '{"ID":"def456","Names":"job","Image":"busybox","State":"exited","Status":"Exited (0) 1 day ago"}';;
images) echo '{"ID":"sha1","Repository":"nginx","Tag":"1.27","Size":"192MB","CreatedSince":"3 weeks ago"}'
    echo '{"ID":"sha2","Repository":"<none>","Tag":"<none>","Size":"5MB"}';;
esac`)
	cmd := exec.Command("sh", "-c", dockerListScript)
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	d, err := ParseDockerList(out)
	if err != nil {
		t.Fatal(err)
	}
	if d.Version != "27.1.0" || len(d.Containers) != 2 || len(d.Images) != 2 {
		t.Fatalf("%+v", d)
	}
	if c := d.Containers[0]; c.Name != "web" || c.Image != "nginx:1.27" || !c.Running() || c.Ports == "" {
		t.Errorf("web %+v", c)
	}
	if d.Containers[1].Running() {
		t.Error("an exited container runs")
	}
	if d.Images[0].Ref() != "nginx:1.27" || d.Images[1].Ref() != "sha2" {
		t.Errorf("refs %q %q", d.Images[0].Ref(), d.Images[1].Ref())
	}
}

// TestDockerScriptExits: no docker is 127, a daemon that refuses the
// user (and no sudo) 77.
func TestDockerScriptExits(t *testing.T) {
	run := func(env []string) int {
		cmd := exec.Command("sh", "-c", dockerListScript)
		cmd.Env = env
		err := cmd.Run()
		if e, ok := err.(*exec.ExitError); ok {
			return e.ExitCode()
		}
		return 0
	}
	if runtime.GOOS == "windows" {
		t.Skip("no sh")
	}
	if code := run([]string{"PATH=" + t.TempDir()}); code != 127 {
		t.Errorf("no docker: %d", code)
	}
	denied := fakeDocker(t, `echo "permission denied while trying to connect to the Docker daemon socket" >&2; exit 1`)
	if code := run(denied); code != 77 {
		t.Errorf("denied: %d", code)
	}
}

func TestDockerCommand(t *testing.T) {
	if s := DockerCommand(DockerStop, "web"); !strings.HasSuffix(s, `$D stop "$1"`) {
		t.Errorf("stop: %q", s)
	}
	for _, bad := range []string{"", "web; rm -rf /", "$(id)", "a b", "'x'", "-rf"} {
		if DockerCommand(DockerStop, bad) != "" {
			t.Errorf("%q let through", bad)
		}
	}
	if DockerCommand("exec", "web") != "" {
		t.Error("an unknown action let through")
	}
	if !DockerRefOK("registry.example.com:5000/team/app:1.2@sha256:abcd") {
		t.Error("a full image reference refused")
	}
}

func TestGroupCompose(t *testing.T) {
	lab := func(project, service string) string {
		return "com.docker.compose.project=" + project +
			",com.docker.compose.service=" + service +
			",com.docker.compose.project.working_dir=/srv/shop" +
			",com.docker.compose.project.config_files=/srv/shop/compose.yml,/srv/shop/extra.yml" +
			",com.docker.compose.version=2.29.1"
	}
	cs := []Container{
		{ID: "a", Name: "web", State: "running", Labels: lab("shop", "web")},
		{ID: "b", Name: "redis", State: "running"},
		{ID: "c", Name: "db", State: "exited", Labels: lab("shop", "db")},
	}
	gs := GroupCompose(cs)
	if len(gs) != 2 || !gs[0].Project() || gs[0].Compose.Name != "shop" || gs[0].Compose.Dir != "/srv/shop" {
		t.Fatalf("%+v", gs)
	}
	if len(gs[0].Containers) != 2 || gs[0].Containers[0].Name != "web" || gs[0].Containers[1].Name != "db" {
		t.Fatalf("containers %+v", gs[0].Containers)
	}
	if len(gs[0].Compose.Files) != 2 || gs[0].Compose.Files[1] != "/srv/shop/extra.yml" {
		t.Fatalf("files %+v", gs[0].Compose.Files)
	}
	if gs[1].Project() || len(gs[1].Containers) != 1 || gs[1].Containers[0].Name != "redis" {
		t.Fatalf("redis %+v", gs[1])
	}
	if _, ok := cs[1].Compose(); ok {
		t.Error("a container without labels belongs to a project")
	}
}

func TestComposeCommand(t *testing.T) {
	p := Compose{Name: "shop", Dir: "/srv/shop", Files: []string{"/srv/shop/compose.yml", "/srv/shop/extra.yml"}}
	got := ComposeCommand(ComposeStop, p)
	want := `$D compose -p 'shop' --project-directory '/srv/shop' -f '/srv/shop/compose.yml' -f '/srv/shop/extra.yml' stop`
	if !strings.Contains(got, want) {
		t.Fatalf("%q", got)
	}
	if !strings.HasSuffix(ComposeCommand(ComposeUp, p), "up -d") {
		t.Fatal("up")
	}
	for _, bad := range []Compose{{Name: ""}, {Name: "a\nb"}, {Name: "has\r"}} {
		if ComposeCommand(ComposeStop, bad) != "" {
			t.Errorf("%q let through", bad.Name)
		}
	}
	if q := ComposeCommand(ComposeStop, Compose{Name: "o'reilly"}); !strings.Contains(q, shellQuote("o'reilly")) {
		t.Fatalf("quote: %q", q)
	}
}

package session

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
)

// ErrNoDocker is a server without the docker command; ErrDockerDenied one
// whose daemon the user may not reach (not in the docker group, and no
// sudo without a password).
var (
	ErrNoDocker     = errors.New("session: docker is not installed")
	ErrDockerDenied = errors.New("session: no permission to use docker")
)

// dockerPrelude finds docker, and runs it as the user or, when the
// daemon refuses the user, through sudo where that asks no password: $D
// is then the command. It exits 127 without docker and 77 without the
// right to use it.
const dockerPrelude = `command -v docker >/dev/null 2>&1 || exit 127
D=docker
if ! docker version --format "{{.Server.Version}}" >/dev/null 2>&1; then
  if sudo -n docker version --format "{{.Server.Version}}" >/dev/null 2>&1; then D="sudo -n docker"; else
    docker version --format "{{.Server.Version}}" 2>&1 | grep -qi "permission denied" && exit 77
  fi
fi
`

// dockerListScript prints the containers (after @C) and the images
// (after @I), one JSON object a line, the daemon's version (@V) and the
// command docker runs as (@D).
const dockerListScript = dockerPrelude + `echo "@V $($D version --format "{{.Server.Version}}" 2>/dev/null)"
echo "@D $D"
echo @C
$D ps -a --no-trunc --format "{{json .}}" || exit 1
echo @I
$D images --format "{{json .}}" || exit 1`

// Container is a Docker container as docker ps lists it.
type Container struct {
	ID      string `json:"ID"`
	Name    string `json:"Names"`
	Image   string `json:"Image"`
	Command string `json:"Command"`
	State   string `json:"State"` // running, exited, paused, created, restarting, dead
	Status  string `json:"Status"`
	Ports   string `json:"Ports"`
	Created string `json:"RunningFor"`
	// Labels is the comma-joined "key=value" list docker ps prints.
	// Project, WorkDir, ConfigFiles and Service override it when set:
	// a compose file path can itself contain a comma.
	Labels      string `json:"Labels"`
	Project     string `json:"Project"`
	WorkDir     string `json:"WorkDir"`
	ConfigFiles string `json:"ConfigFiles"`
	Service     string `json:"Service"`
}

// Running reports whether the container runs (paused or not).
func (c Container) Running() bool {
	return c.State == "running" || c.State == "paused" || c.State == "restarting"
}

// Image is a Docker image as docker images lists it.
type Image struct {
	ID         string `json:"ID"`
	Repository string `json:"Repository"`
	Tag        string `json:"Tag"`
	Size       string `json:"Size"`
	Created    string `json:"CreatedSince"`
}

// Ref is the image as docker names it: repository:tag, or its ID when it
// has neither.
func (i Image) Ref() string {
	if i.Repository == "" || i.Repository == "<none>" {
		return i.ID
	}
	if i.Tag == "" || i.Tag == "<none>" {
		return i.Repository
	}
	return i.Repository + ":" + i.Tag
}

// DockerInfo is what DockerList read. Sudo is set when the user reaches
// the daemon only through sudo.
type DockerInfo struct {
	Version    string
	Sudo       bool
	Containers []Container
	Images     []Image
}

// ParseDockerList reads the output of the list script.
func ParseDockerList(out []byte) (DockerInfo, error) {
	var d DockerInfo
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	part := ""
	seen := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "@V"):
			d.Version = strings.TrimSpace(strings.TrimPrefix(line, "@V"))
		case strings.HasPrefix(line, "@D"):
			d.Sudo = strings.Contains(line, "sudo")
		case line == "@C" || line == "@I":
			part, seen = line, true
		case line == "" || !strings.HasPrefix(line, "{"):
		case part == "@C":
			var c Container
			if json.Unmarshal([]byte(line), &c) == nil && c.ID != "" {
				d.Containers = append(d.Containers, c)
			}
		case part == "@I":
			var i Image
			if json.Unmarshal([]byte(line), &i) == nil && i.ID != "" {
				d.Images = append(d.Images, i)
			}
		}
	}
	if !seen {
		return DockerInfo{}, errors.New("session: unreadable docker output")
	}
	return d, nil
}

// runDocker runs script under sh, args its $1 …, its stdout returned and
// its stderr in the error of a failure. The args are checked references
// (DockerRefOK), which hold no quote.
func runDocker(ctx context.Context, client *ssh.Client, script string, args ...string) ([]byte, error) {
	var out, errb bytes.Buffer
	cmd := "sh -c " + shellQuote(script) + " _"
	for _, a := range args {
		cmd += " " + shellQuote(a)
	}
	err := Exec(ctx, client, cmd, &out, &errb)
	var exit *ssh.ExitError
	if errors.As(err, &exit) {
		switch exit.ExitStatus() {
		case 127:
			return nil, ErrNoDocker
		case 77:
			return nil, ErrDockerDenied
		}
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			return nil, errors.New(lastLine(msg))
		}
	}
	return out.Bytes(), err
}

func lastLine(s string) string {
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[i+1:])
	}
	return s
}

// DockerList reads the containers and the images of the server.
func DockerList(ctx context.Context, client *ssh.Client) (DockerInfo, error) {
	out, err := runDocker(ctx, client, dockerListScript)
	if err != nil {
		return DockerInfo{}, err
	}
	return ParseDockerList(out)
}

// DockerAction is what can be done to a container or an image.
type DockerAction string

const (
	DockerStart       DockerAction = "start"
	DockerStop        DockerAction = "stop"
	DockerRestart     DockerAction = "restart"
	DockerPause       DockerAction = "pause"
	DockerUnpause     DockerAction = "unpause"
	DockerRemove      DockerAction = "rm -f"
	DockerRemoveImage DockerAction = "rmi"
)

// dockerRef is a container or image reference docker may be given: IDs,
// names and repository:tag@digest refs, never shell syntax.
var dockerRef = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/@+-]*$`)

// DockerRefOK reports whether ref may go into a docker command.
func DockerRefOK(ref string) bool { return len(ref) <= 512 && dockerRef.MatchString(ref) }

func dockerActionOK(a DockerAction) bool {
	switch a {
	case DockerStart, DockerStop, DockerRestart, DockerPause, DockerUnpause, DockerRemove, DockerRemoveImage:
		return true
	}
	return false
}

// DockerCommand is the script doing action to ref, "" for an action or a
// reference that may not be.
func DockerCommand(action DockerAction, ref string) string {
	if !dockerActionOK(action) || !DockerRefOK(ref) {
		return ""
	}
	return dockerPrelude + `$D ` + string(action) + ` "$1"`
}

// DockerDo does action to the container or image ref.
func DockerDo(ctx context.Context, client *ssh.Client, action DockerAction, ref string) error {
	script := DockerCommand(action, ref)
	if script == "" {
		return errors.New("session: bad docker reference")
	}
	_, err := runDocker(ctx, client, script, ref)
	return err
}

// DockerLogs is the last lines of a container's output, stdout and
// stderr together.
func DockerLogs(ctx context.Context, client *ssh.Client, ref string, lines int) (string, error) {
	if !DockerRefOK(ref) {
		return "", errors.New("session: bad docker reference")
	}
	if lines <= 0 || lines > 5000 {
		lines = 500
	}
	out, err := runDocker(ctx, client, dockerPrelude+`$D logs --tail `+strconv.Itoa(lines)+` "$1" 2>&1`, ref)
	return string(out), err
}

// Compose is the Docker Compose project a container belongs to, from the
// labels compose writes onto it. Files are the compose files, Dir the
// project directory; either can be empty when the daemon did not record it.
type Compose struct {
	Name    string
	Dir     string
	Files   []string
	Service string
}

// Compose reports the project c belongs to.
func (c Container) Compose() (Compose, bool) {
	name, dir, files, service := c.Project, c.WorkDir, c.ConfigFiles, c.Service
	if name == "" || dir == "" || files == "" || service == "" {
		labs := parseDockerLabels(c.Labels)
		if name == "" {
			name = labs["com.docker.compose.project"]
		}
		if dir == "" {
			dir = labs["com.docker.compose.project.working_dir"]
		}
		if files == "" {
			files = labs["com.docker.compose.project.config_files"]
			if files == "" {
				files = labs["com.docker.compose.config_files"]
			}
		}
		if service == "" {
			service = labs["com.docker.compose.service"]
		}
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Compose{}, false
	}
	return Compose{Name: name, Dir: strings.TrimSpace(dir), Files: splitComposeFiles(files), Service: strings.TrimSpace(service)}, true
}

// parseDockerLabels reads the Labels field of docker ps. A value may
// contain commas (a list of compose files does); a piece without its own
// key continues the value before it.
func parseDockerLabels(s string) map[string]string {
	out := map[string]string{}
	var key, val string
	have := false
	flush := func() {
		if have {
			out[key] = val
		}
	}
	for _, part := range strings.Split(s, ",") {
		if i := strings.IndexByte(part, '='); i > 0 && dockerLabelKey(part[:i]) {
			flush()
			key, val, have = part[:i], part[i+1:], true
			continue
		}
		if have {
			val += "," + part
		}
	}
	flush()
	return out
}

func dockerLabelKey(s string) bool {
	if s == "" || len(s) > 256 {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case i > 0 && (r == '.' || r == '_' || r == '-'):
		default:
			return false
		}
	}
	return true
}

func splitComposeFiles(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ComposeGroup is one row of a compose layout: a project and the
// containers that belong to it, or a single container that belongs to
// none (its Compose name empty). Order is the order docker ps listed them,
// a project taking the place of its first container.
type ComposeGroup struct {
	Compose    Compose
	Containers []Container
}

// Project reports whether g is a Compose project.
func (g ComposeGroup) Project() bool { return g.Compose.Name != "" }

// GroupCompose lays containers out by Compose project, as the Containers
// view does: a project holds its containers, and a container with no
// project stands on its own.
func GroupCompose(cs []Container) []ComposeGroup {
	var out []ComposeGroup
	at := map[string]int{}
	for _, c := range cs {
		comp, ok := c.Compose()
		if !ok {
			out = append(out, ComposeGroup{Containers: []Container{c}})
			continue
		}
		i, seen := at[comp.Name]
		if !seen {
			at[comp.Name] = len(out)
			out = append(out, ComposeGroup{Compose: comp, Containers: []Container{c}})
			continue
		}
		g := &out[i]
		g.Containers = append(g.Containers, c)
		if g.Compose.Dir == "" {
			g.Compose.Dir = comp.Dir
		}
		if len(g.Compose.Files) == 0 {
			g.Compose.Files = comp.Files
		}
	}
	return out
}

// ComposeAction is a docker compose command run against a project.
type ComposeAction string

const (
	ComposeUp      ComposeAction = "up -d"
	ComposeDown    ComposeAction = "down"
	ComposeStart   ComposeAction = "start"
	ComposeStop    ComposeAction = "stop"
	ComposeRestart ComposeAction = "restart"
)

// ComposeOK reports whether p may be given to docker compose. The name is
// quoted into the command; a control character is refused.
func ComposeOK(p Compose) bool {
	return composeTextOK(p.Name) && len(p.Name) <= 128
}

func composeTextOK(s string) bool {
	if s == "" || len(s) > 4096 {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func composeActionOK(a ComposeAction) bool {
	switch a {
	case ComposeUp, ComposeDown, ComposeStart, ComposeStop, ComposeRestart:
		return true
	}
	return false
}

// composePrefix is "$D compose" with the project's name, directory and
// files, each quoted. A path that may not be quoted is left out.
func composePrefix(p Compose) string {
	var b strings.Builder
	b.WriteString(dockerPrelude)
	b.WriteString("$D compose -p ")
	b.WriteString(shellQuote(p.Name))
	if composeTextOK(p.Dir) {
		b.WriteString(" --project-directory ")
		b.WriteString(shellQuote(p.Dir))
	}
	for _, f := range p.Files {
		if composeTextOK(f) {
			b.WriteString(" -f ")
			b.WriteString(shellQuote(f))
		}
	}
	b.WriteByte(' ')
	return b.String()
}

// ComposeCommand is the script running action on p, "" when either may not be.
func ComposeCommand(action ComposeAction, p Compose) string {
	if !composeActionOK(action) || !ComposeOK(p) {
		return ""
	}
	return composePrefix(p) + string(action)
}

// ComposeDo runs action on the project p.
func ComposeDo(ctx context.Context, client *ssh.Client, action ComposeAction, p Compose) error {
	script := ComposeCommand(action, p)
	if script == "" {
		return errors.New("session: bad compose project")
	}
	_, err := runDocker(ctx, client, script)
	return err
}

// ComposeLogs is the last lines of a project's services, stdout and stderr
// together.
func ComposeLogs(ctx context.Context, client *ssh.Client, p Compose, lines int) (string, error) {
	if !ComposeOK(p) {
		return "", errors.New("session: bad compose project")
	}
	if lines <= 0 || lines > 5000 {
		lines = 500
	}
	out, err := runDocker(ctx, client, composePrefix(p)+"logs --tail "+strconv.Itoa(lines)+" --no-color")
	return string(out), err
}

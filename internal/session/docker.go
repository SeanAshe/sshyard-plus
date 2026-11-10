package session

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
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

// dockerListScript prints the containers (after @C), the images (after
// @I), the networks (after @N) and the volumes (after @U), one JSON
// object a line, the daemon's version (@V) and the command docker runs
// as (@D).
const dockerListScript = dockerPrelude + `echo "@V $($D version --format "{{.Server.Version}}" 2>/dev/null)"
echo "@D $D"
echo @C
$D ps -a --no-trunc --format "{{json .}}" || exit 1
echo @I
$D images --format "{{json .}}" || exit 1
echo @N
$D network ls --format "{{json .}}" || exit 1
echo @U
$D volume ls --format "{{json .}}" || exit 1`

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

// PullRef is the repository:tag a pull asks for. An image with no
// repository or no tag cannot be pulled.
func (i Image) PullRef() string {
	if i.Repository == "" || i.Repository == "<none>" || i.Tag == "" || i.Tag == "<none>" {
		return ""
	}
	ref := i.Repository + ":" + i.Tag
	if !DockerRefOK(ref) {
		return ""
	}
	return ref
}

// Network is a Docker network as docker network ls lists it.
type Network struct {
	ID     string `json:"ID"`
	Name   string `json:"Name"`
	Driver string `json:"Driver"`
	Scope  string `json:"Scope"`
}

// Volume is a Docker volume as docker volume ls lists it.
type Volume struct {
	Name       string `json:"Name"`
	Driver     string `json:"Driver"`
	Scope      string `json:"Scope"`
	Mountpoint string `json:"Mountpoint"`
}

// DockerInfo is what DockerList read. Sudo is set when the user reaches
// the daemon only through sudo.
type DockerInfo struct {
	Version    string
	Sudo       bool
	Containers []Container
	Images     []Image
	Networks   []Network
	Volumes    []Volume
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
		case line == "@C" || line == "@I" || line == "@N" || line == "@U":
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
		case part == "@N":
			var n Network
			if json.Unmarshal([]byte(line), &n) == nil && n.Name != "" {
				d.Networks = append(d.Networks, n)
			}
		case part == "@U":
			var v Volume
			if json.Unmarshal([]byte(line), &v) == nil && v.Name != "" {
				d.Volumes = append(d.Volumes, v)
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
	DockerPull        DockerAction = "pull"
)

// DockerPrune is a prune of unused objects. Images drops dangling images,
// as Containers: Prune Images does; networks and volumes drop what no
// container uses.
type DockerPrune string

const (
	PruneImages   DockerPrune = "image prune -f"
	PruneNetworks DockerPrune = "network prune -f"
	PruneVolumes  DockerPrune = "volume prune -f"
)

// InspectKind is a docker inspect of one object: an image, a network or a volume.
type InspectKind string

const (
	InspectImage   InspectKind = "image"
	InspectNetwork InspectKind = "network"
	InspectVolume  InspectKind = "volume"
)

// dockerRef is a container or image reference docker may be given: IDs,
// names and repository:tag@digest refs, never shell syntax.
var dockerRef = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/@+-]*$`)

// DockerRefOK reports whether ref may go into a docker command.
func DockerRefOK(ref string) bool { return len(ref) <= 512 && dockerRef.MatchString(ref) }

func dockerActionOK(a DockerAction) bool {
	switch a {
	case DockerStart, DockerStop, DockerRestart, DockerPause, DockerUnpause, DockerRemove, DockerRemoveImage, DockerPull:
		return true
	}
	return false
}

func dockerPruneOK(p DockerPrune) bool {
	switch p {
	case PruneImages, PruneNetworks, PruneVolumes:
		return true
	}
	return false
}

func dockerInspectOK(k InspectKind) bool {
	switch k {
	case InspectImage, InspectNetwork, InspectVolume:
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

// DockerPruneCommand is the script pruning p, "" for a prune that is not one.
func DockerPruneCommand(p DockerPrune) string {
	if !dockerPruneOK(p) {
		return ""
	}
	return dockerPrelude + `$D ` + string(p)
}

// PruneDocker removes dangling images, or unused networks or volumes.
// out is what docker printed, which names the space it reclaimed.
func PruneDocker(ctx context.Context, client *ssh.Client, p DockerPrune) (string, error) {
	script := DockerPruneCommand(p)
	if script == "" {
		return "", errors.New("session: bad docker prune")
	}
	out, err := runDocker(ctx, client, script)
	return string(out), err
}

// ReclaimedSpace is the "Total reclaimed space" line of a prune, "" when
// docker did not print one.
func ReclaimedSpace(out string) string {
	const mark = "Total reclaimed space:"
	i := strings.Index(out, mark)
	if i < 0 {
		return ""
	}
	rest := out[i+len(mark):]
	if n := strings.IndexByte(rest, '\n'); n >= 0 {
		rest = rest[:n]
	}
	return strings.TrimSpace(rest)
}

// DockerInspectCommand is the script inspecting ref, "" when either may not be.
func DockerInspectCommand(kind InspectKind, ref string) string {
	if !dockerInspectOK(kind) || !DockerRefOK(ref) {
		return ""
	}
	return dockerPrelude + `$D ` + string(kind) + ` inspect "$1"`
}

// InspectDocker is the JSON docker inspect prints for ref.
func InspectDocker(ctx context.Context, client *ssh.Client, kind InspectKind, ref string) (string, error) {
	script := DockerInspectCommand(kind, ref)
	if script == "" {
		return "", errors.New("session: bad docker reference")
	}
	out, err := runDocker(ctx, client, script, ref)
	return string(out), err
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

// dockerDetailScript prints what docker inspect says of the container $1
// (after @J), and, while it runs, its resource use (@S, one JSON object)
// and its processes (@T, as docker top prints them).
const dockerDetailScript = dockerPrelude + `echo @J
$D inspect --type container "$1" || exit 1
[ "$($D inspect --type container --format "{{.State.Running}}" "$1" 2>/dev/null)" = true ] || exit 0
echo @S
$D stats --no-stream --no-trunc --format "{{json .}}" "$1" 2>/dev/null
echo @T
$D top "$1" -eo pid,user,pcpu,pmem,etime,args 2>/dev/null || $D top "$1" 2>/dev/null
exit 0`

// ContainerStats is a running container's resource use, as docker stats
// words it.
type ContainerStats struct {
	CPU      string `json:"CPUPerc"`
	Mem      string `json:"MemPerc"`
	MemUsage string `json:"MemUsage"`
	NetIO    string `json:"NetIO"`
	BlockIO  string `json:"BlockIO"`
	PIDs     string `json:"PIDs"`
}

// Percent is a docker stats percentage ("12.5%") as a number, -1 when
// it holds none.
func Percent(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "%")), 64)
	if err != nil {
		return -1
	}
	return f
}

// Mount is a volume or a bind of a container.
type Mount struct {
	Type        string `json:"Type"`
	Name        string `json:"Name"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
	Mode        string `json:"Mode"`
	RW          bool   `json:"RW"`
}

// ContainerNet is a network the container is on.
type ContainerNet struct {
	Name    string
	IP      string `json:"IPAddress"`
	IPv6    string `json:"GlobalIPv6Address"`
	Gateway string `json:"Gateway"`
	MAC     string `json:"MacAddress"`
}

// ContainerInspect is the part of docker inspect the details show.
type ContainerInspect struct {
	ID      string   `json:"Id"`
	Name    string   `json:"Name"`
	Created string   `json:"Created"`
	Path    string   `json:"Path"`
	Args    []string `json:"Args"`
	Image   string   `json:"Image"`
	Driver  string   `json:"Driver"`
	Restart int      `json:"RestartCount"`
	State   struct {
		Status     string `json:"Status"`
		Pid        int    `json:"Pid"`
		ExitCode   int    `json:"ExitCode"`
		Error      string `json:"Error"`
		OOMKilled  bool   `json:"OOMKilled"`
		StartedAt  string `json:"StartedAt"`
		FinishedAt string `json:"FinishedAt"`
		Health     *struct {
			Status        string `json:"Status"`
			FailingStreak int    `json:"FailingStreak"`
		} `json:"Health"`
	} `json:"State"`
	Config struct {
		Hostname   string            `json:"Hostname"`
		User       string            `json:"User"`
		Env        []string          `json:"Env"`
		Cmd        []string          `json:"Cmd"`
		Entrypoint []string          `json:"Entrypoint"`
		WorkingDir string            `json:"WorkingDir"`
		Image      string            `json:"Image"`
		Labels     map[string]string `json:"Labels"`
	} `json:"Config"`
	HostConfig struct {
		NetworkMode   string `json:"NetworkMode"`
		Privileged    bool   `json:"Privileged"`
		RestartPolicy struct {
			Name  string `json:"Name"`
			Count int    `json:"MaximumRetryCount"`
		} `json:"RestartPolicy"`
		Memory   int64 `json:"Memory"`
		NanoCpus int64 `json:"NanoCpus"`
		ShmSize  int64 `json:"ShmSize"`
	} `json:"HostConfig"`
	Mounts          []Mount `json:"Mounts"`
	NetworkSettings struct {
		Networks map[string]ContainerNet `json:"Networks"`
	} `json:"NetworkSettings"`
}

// Command is how the container was started: its entrypoint and its
// arguments.
func (c ContainerInspect) Command() string {
	return strings.TrimSpace(strings.Join(append([]string{c.Path}, c.Args...), " "))
}

// Nets are the container's networks, by name.
func (c ContainerInspect) Nets() []ContainerNet {
	ns := make([]ContainerNet, 0, len(c.NetworkSettings.Networks))
	for name, n := range c.NetworkSettings.Networks {
		n.Name = name
		ns = append(ns, n)
	}
	slices.SortFunc(ns, func(a, b ContainerNet) int { return strings.Compare(a.Name, b.Name) })
	return ns
}

// Top is the processes of a container: the columns docker top printed,
// and a row of as many cells for each process.
type Top struct {
	Titles []string
	Rows   [][]string
}

// ContainerDetail is what DockerInspect read: Stats and Top are only
// read while the container runs.
type ContainerDetail struct {
	Inspect ContainerInspect
	Stats   *ContainerStats
	Top     Top
}

// ParseDockerDetail reads the output of the detail script.
func ParseDockerDetail(out []byte) (ContainerDetail, error) {
	var d ContainerDetail
	parts, ok := dockerSections(out, "@J", "@S", "@T")
	if !ok {
		return d, errors.New("session: unreadable docker output")
	}
	var all []ContainerInspect
	if err := json.Unmarshal([]byte(parts["@J"]), &all); err != nil || len(all) == 0 {
		return d, errors.New("session: unreadable docker output")
	}
	d.Inspect = all[0]
	if s := strings.TrimSpace(parts["@S"]); s != "" {
		var st ContainerStats
		if json.Unmarshal([]byte(s), &st) == nil {
			d.Stats = &st
		}
	}
	d.Top = parseTop(parts["@T"])
	return d, nil
}

// dockerSections splits a script's output at the lines that are one of
// marks into what follows each; ok is false when it has none of them.
func dockerSections(out []byte, marks ...string) (parts map[string]string, ok bool) {
	parts = map[string]string{}
	var b strings.Builder
	part := ""
	flush := func() {
		if part != "" {
			parts[part] = b.String()
		}
		b.Reset()
	}
	for line := range strings.Lines(string(out)) {
		if t := strings.TrimSpace(line); slices.Contains(marks, t) {
			flush()
			part = t
			continue
		}
		if part != "" {
			b.WriteString(line)
		}
	}
	flush()
	return parts, part != ""
}

// parseTop reads docker top: a line of titles, then a line a process,
// whose last cell (the command) may hold spaces.
func parseTop(s string) Top {
	var t Top
	for line := range strings.Lines(s) {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if t.Titles == nil {
			t.Titles = f
			continue
		}
		n := len(t.Titles)
		if len(f) > n {
			// The command is what is left after the other cells.
			rest := strings.TrimSpace(line)
			for range n - 1 {
				rest = strings.TrimLeft(rest[strings.IndexFunc(rest, isSpace):], " \t")
			}
			f = append(f[:n-1], strings.TrimSpace(rest))
		}
		t.Rows = append(t.Rows, f)
	}
	return t
}

func isSpace(r rune) bool { return r == ' ' || r == '\t' }

// DockerInspect reads the details of the container ref.
func DockerInspect(ctx context.Context, client *ssh.Client, ref string) (ContainerDetail, error) {
	if !DockerRefOK(ref) {
		return ContainerDetail{}, errors.New("session: bad docker reference")
	}
	out, err := runDocker(ctx, client, dockerDetailScript, ref)
	if err != nil {
		return ContainerDetail{}, err
	}
	return ParseDockerDetail(out)
}

// dockerImageScript prints what docker image inspect says of the image
// $1 (after @J), its layers as docker history lists them (@H, one JSON
// object a line) and the containers made from it (@C, as docker ps).
const dockerImageScript = dockerPrelude + `echo @J
$D image inspect "$1" || exit 1
echo @H
$D history --no-trunc --format "{{json .}}" "$1" 2>/dev/null
echo @C
$D ps -a --no-trunc --filter "ancestor=$1" --format "{{json .}}" 2>/dev/null
exit 0`

// ImageInspect is the part of docker image inspect the details show.
type ImageInspect struct {
	ID            string   `json:"Id"`
	RepoTags      []string `json:"RepoTags"`
	RepoDigests   []string `json:"RepoDigests"`
	Created       string   `json:"Created"`
	Author        string   `json:"Author"`
	DockerVersion string   `json:"DockerVersion"`
	Architecture  string   `json:"Architecture"`
	Variant       string   `json:"Variant"`
	Os            string   `json:"Os"`
	Size          int64    `json:"Size"`
	Config        struct {
		User         string              `json:"User"`
		Env          []string            `json:"Env"`
		Cmd          []string            `json:"Cmd"`
		Entrypoint   []string            `json:"Entrypoint"`
		WorkingDir   string              `json:"WorkingDir"`
		ExposedPorts map[string]struct{} `json:"ExposedPorts"`
		Volumes      map[string]struct{} `json:"Volumes"`
		Labels       map[string]string   `json:"Labels"`
	} `json:"Config"`
	RootFS struct {
		Layers []string `json:"Layers"`
	} `json:"RootFS"`
}

// Platform is the image's OS and architecture, as linux/arm64/v8.
func (i ImageInspect) Platform() string {
	return strings.Join(slices.DeleteFunc([]string{i.Os, i.Architecture, i.Variant}, func(s string) bool { return s == "" }), "/")
}

// ImageLayer is a step of an image's build, as docker history lists it.
type ImageLayer struct {
	ID        string `json:"ID"`
	CreatedBy string `json:"CreatedBy"`
	Created   string `json:"CreatedSince"`
	Size      string `json:"Size"`
	Comment   string `json:"Comment"`
}

// Step is the instruction that made the layer, without the shell the
// legacy builder wrapped it in.
func (l ImageLayer) Step() string {
	s := strings.TrimSpace(l.CreatedBy)
	s = strings.TrimPrefix(s, "/bin/sh -c #(nop) ")
	return strings.TrimSpace(s)
}

// ImageDetail is what DockerImageInspect read.
type ImageDetail struct {
	Inspect    ImageInspect
	History    []ImageLayer
	Containers []Container
}

// ParseDockerImage reads the output of the image script.
func ParseDockerImage(out []byte) (ImageDetail, error) {
	var d ImageDetail
	parts, ok := dockerSections(out, "@J", "@H", "@C")
	if !ok {
		return d, errors.New("session: unreadable docker output")
	}
	var all []ImageInspect
	if err := json.Unmarshal([]byte(parts["@J"]), &all); err != nil || len(all) == 0 {
		return d, errors.New("session: unreadable docker output")
	}
	d.Inspect = all[0]
	for line := range strings.Lines(parts["@H"]) {
		var l ImageLayer
		if json.Unmarshal([]byte(line), &l) == nil && (l.CreatedBy != "" || l.ID != "") {
			d.History = append(d.History, l)
		}
	}
	for line := range strings.Lines(parts["@C"]) {
		var c Container
		if json.Unmarshal([]byte(line), &c) == nil && c.ID != "" {
			d.Containers = append(d.Containers, c)
		}
	}
	return d, nil
}

// DockerImageInspect reads the details of the image ref.
func DockerImageInspect(ctx context.Context, client *ssh.Client, ref string) (ImageDetail, error) {
	if !DockerRefOK(ref) {
		return ImageDetail{}, errors.New("session: bad docker reference")
	}
	out, err := runDocker(ctx, client, dockerImageScript, ref)
	if err != nil {
		return ImageDetail{}, err
	}
	return ParseDockerImage(out)
}

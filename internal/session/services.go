package session

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
)

// ErrServiceRoot is a control command the user may not run (not root, and
// sudo asks for a password). ErrServiceNoSudo is the same where sudo is
// not installed.
var (
	ErrServiceRoot   = errors.New("session: service control needs root")
	ErrServiceNoSudo = errors.New("session: service control needs root and sudo is missing")
)

// serviceListScript reads systemd's version (@V), how the machine is
// doing (@S, systemctl is-system-running, which exits non-zero when some
// unit failed) and every service (@L, systemctl show, a blank line
// between units). A host without systemd prints @U and uname -s.
//
// Templates (getty@.service) are left out; instances (getty@tty1.service)
// stay. The case patterns open with "(": inside $(…), bash 3.2 takes a
// pattern's bare ")" for the end of the substitution.
const serviceListScript = `export LC_ALL=C
PATH=$PATH:/usr/sbin:/sbin
if ! test -d /run/systemd/system || ! command -v systemctl >/dev/null 2>&1; then
  echo @U
  uname -s 2>/dev/null
  exit 0
fi
echo @V
systemctl --version 2>/dev/null
echo @S
systemctl is-system-running 2>/dev/null || true
echo @L
names=$(
  {
    systemctl list-unit-files --type=service --no-legend --no-pager 2>/dev/null
    systemctl list-units --type=service --all --no-legend --no-pager --plain 2>/dev/null
  } | while read -r name rest; do
    case $name in
      (*.service)
        case $name in
          (*@.service) ;;
          (*) printf '%s\n' "$name" ;;
        esac
        ;;
    esac
  done
)
if test -n "$names"; then
  systemctl show -p Id -p Description -p ActiveState -p UnitFileState -p FragmentPath -p LoadState -- $names 2>/dev/null
fi
`

// Unit is one systemd service.
type Unit struct {
	Name        string
	Description string
	// Active is systemd's ActiveState: active, inactive, failed, …
	Active string
	// FileState is UnitFileState: enabled, disabled, static, …
	FileState string
	// Path is the unit file. One under /etc/systemd/system is the
	// administrator's.
	Path string
	Load string
}

// Custom reports whether the unit file is the administrator's.
func (u Unit) Custom() bool {
	return strings.HasPrefix(u.Path, "/etc/systemd/system/")
}

// SvcFilter is which services a list shows.
type SvcFilter int

const (
	SvcAll SvcFilter = iota
	SvcRunning
	SvcStopped
	SvcFailed
)

// Kind is the filter u belongs to. A service on its way up or down counts
// as running, as the list's Active tab does.
func (u Unit) Kind() SvcFilter {
	switch u.Active {
	case "active", "reloading", "activating", "deactivating":
		return SvcRunning
	case "failed":
		return SvcFailed
	}
	return SvcStopped
}

// ServiceInfo is one reading of a host's services. Ready is set when
// systemd runs there; otherwise System is uname -s.
type ServiceInfo struct {
	Version string
	// State is systemctl is-system-running: running, degraded, …
	State  string
	Units  []Unit
	System string
	Ready  bool
}

// Degraded reports whether some unit failed, so the machine is not quite well.
func (s ServiceInfo) Degraded() bool { return s.State == "degraded" }

// ParseServices reads the output of the list script.
func ParseServices(out []byte) (ServiceInfo, error) {
	var info ServiceInfo
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	part := ""
	seen := false
	var lines []string
	flush := func() {
		switch part {
		case "@V":
			info.Version = systemdVersion(lines)
		case "@S":
			info.State = firstText(lines)
		case "@U":
			info.System = firstText(lines)
		case "@L":
			info.Units = parseUnits(lines)
			info.Ready = true
		}
	}
	for sc.Scan() {
		line := sc.Text()
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "@") && !strings.Contains(trim, "=") && len(trim) == 2 {
			flush()
			part, lines, seen = trim, nil, true
			continue
		}
		lines = append(lines, line)
	}
	flush()
	if !seen {
		return ServiceInfo{}, errors.New("session: unreadable systemctl output")
	}
	return info, nil
}

func firstText(lines []string) string {
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

// systemdVersion keeps "systemd 249" of "systemd 249 (249.11-0ubuntu3)".
func systemdVersion(lines []string) string {
	line := firstText(lines)
	f := strings.Fields(line)
	if len(f) >= 2 && f[0] == "systemd" {
		return f[0] + " " + f[1]
	}
	return line
}

func parseUnits(lines []string) []Unit {
	var blocks []map[string]string
	cur := map[string]string{}
	flush := func() {
		if len(cur) > 0 {
			blocks = append(blocks, cur)
			cur = map[string]string{}
		}
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		cur[strings.TrimSpace(key)] = strings.TrimSpace(val)
	}
	flush()
	seen := map[string]bool{}
	var out []Unit
	for _, b := range blocks {
		name := b["Id"]
		if !strings.HasSuffix(name, ".service") || strings.HasSuffix(name, "@.service") {
			continue
		}
		if seen[name] {
			continue
		}
		missing := b["LoadState"] == "not-found"
		if missing && b["ActiveState"] != "failed" {
			continue
		}
		seen[name] = true
		out = append(out, Unit{
			Name:        name,
			Description: b["Description"],
			Active:      b["ActiveState"],
			FileState:   b["UnitFileState"],
			Path:        b["FragmentPath"],
			Load:        b["LoadState"],
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// CountUnits is how many units query finds under each filter, in the
// order all, running, stopped, failed. query matches the name or the
// description, ignoring case.
func CountUnits(units []Unit, query string) (all, running, stopped, failed int) {
	q := strings.ToLower(strings.TrimSpace(query))
	for _, u := range units {
		if !unitMatches(u, q) {
			continue
		}
		all++
		switch u.Kind() {
		case SvcRunning:
			running++
		case SvcFailed:
			failed++
		default:
			stopped++
		}
	}
	return
}

func unitMatches(u Unit, q string) bool {
	if q == "" {
		return true
	}
	return strings.Contains(strings.ToLower(u.Name), q) || strings.Contains(strings.ToLower(u.Description), q)
}

// FilterUnits is the units query finds under f, the administrator's own
// first, then the rest, each group in name order.
func FilterUnits(units []Unit, f SvcFilter, query string) (custom, system []Unit) {
	q := strings.ToLower(strings.TrimSpace(query))
	for _, u := range units {
		if f != SvcAll && u.Kind() != f {
			continue
		}
		if !unitMatches(u, q) {
			continue
		}
		if u.Custom() {
			custom = append(custom, u)
		} else {
			system = append(system, u)
		}
	}
	return
}

// ServicesList reads the host's systemd services.
func ServicesList(ctx context.Context, client *ssh.Client) (ServiceInfo, error) {
	out, err := runService(ctx, client, serviceListScript)
	if err != nil {
		return ServiceInfo{}, err
	}
	return ParseServices(out)
}

// ServiceAction is a systemctl verb.
type ServiceAction string

const (
	ServiceStart   ServiceAction = "start"
	ServiceStop    ServiceAction = "stop"
	ServiceRestart ServiceAction = "restart"
	ServiceEnable  ServiceAction = "enable"
	ServiceDisable ServiceAction = "disable"
)

// serviceName is a unit name systemd accepts and a shell may be given:
// letters, digits and :_.@-\ , ending in .service, never a quote.
var serviceName = regexp.MustCompile(`^[A-Za-z0-9:_.@\\-]+\.service$`)

// ServiceNameOK reports whether name may go into a systemctl command.
func ServiceNameOK(name string) bool {
	return len(name) <= 256 && serviceName.MatchString(name) && !strings.Contains(name, `\\`) && !strings.HasSuffix(name, "@.service")
}

func serviceActionOK(a ServiceAction) bool {
	switch a {
	case ServiceStart, ServiceStop, ServiceRestart, ServiceEnable, ServiceDisable:
		return true
	}
	return false
}

// Actions are the buttons a service shows: stop and restart while it
// runs, start otherwise.
func (u Unit) Actions() []ServiceAction {
	if u.Kind() == SvcRunning {
		return []ServiceAction{ServiceStop, ServiceRestart}
	}
	return []ServiceAction{ServiceStart}
}

// BootAction is enable or disable, when the unit file can be either.
func (u Unit) BootAction() (ServiceAction, bool) {
	switch u.FileState {
	case "enabled", "enabled-runtime":
		return ServiceDisable, true
	case "disabled":
		return ServiceEnable, true
	}
	return "", false
}

// serviceAsRoot runs script as root, or through sudo where that asks no
// password. script uses $1 for the unit name.
func serviceAsRoot(script string) string {
	return `if test "$(id -u)" = 0; then ` + script + `; else sudo -n ` + script + `; fi`
}

// ServiceCommand is the script doing action to name, "" when either may
// not be. It ends with @R and systemctl's status.
func ServiceCommand(action ServiceAction, name string) string {
	if !serviceActionOK(action) || !ServiceNameOK(name) {
		return ""
	}
	body := serviceAsRoot(`systemctl ` + string(action) + ` -- "$1"`)
	return `export LC_ALL=C
PATH=$PATH:/usr/sbin:/sbin
` + body + `
echo "@R $?"`
}

// ParseServiceResult reads a control command's output.
func ParseServiceResult(out []byte) error {
	text := string(out)
	i := strings.LastIndex(text, "@R")
	if i < 0 {
		return errors.New("session: unreadable systemctl output")
	}
	status := strings.TrimSpace(text[i+2:])
	said := strings.TrimSpace(text[:i])
	if status == "0" {
		return nil
	}
	switch {
	case strings.Contains(said, "password is required"),
		strings.Contains(said, "Interactive authentication required"),
		strings.Contains(said, "Access denied"),
		strings.Contains(said, "not in the sudoers"):
		return ErrServiceRoot
	case strings.Contains(said, "command not found") && strings.Contains(said, "sudo"):
		return ErrServiceNoSudo
	case said == "":
		return errors.New("systemctl exited " + status)
	}
	if n := strings.IndexByte(said, '\n'); n >= 0 {
		said = said[:n]
	}
	return errors.New(strings.TrimSpace(said))
}

// ServiceDo runs action on the unit name.
func ServiceDo(ctx context.Context, client *ssh.Client, action ServiceAction, name string) error {
	script := ServiceCommand(action, name)
	if script == "" {
		return errors.New("session: bad service name")
	}
	out, err := runService(ctx, client, script, name)
	if err != nil {
		return err
	}
	return ParseServiceResult(out)
}

// ServiceStatus is a service's state in full, as systemctl show says.
// Pointers are unset when systemd has no number (an unset counter is the
// largest uint64 it can write, which is left out).
type ServiceStatus struct {
	Description string
	Load        string
	Active      string
	Sub         string
	FileState   string
	MainPID     uint32
	Memory      *uint64
	Tasks       *uint64
	Restarts    *uint64
	ExitStatus  *int
	Started     string
	Stopped     string
	Path        string
}

const serviceStatusProps = `-p Id -p Description -p LoadState -p ActiveState -p SubState -p UnitFileState -p FragmentPath -p MainPID -p MemoryCurrent -p TasksCurrent -p NRestarts -p ExecMainStatus -p ActiveEnterTimestamp -p InactiveEnterTimestamp`

// ServiceStatusCommand is the script reading name's details, "" for a
// name that may not be.
func ServiceStatusCommand(name string) string {
	if !ServiceNameOK(name) {
		return ""
	}
	return `export LC_ALL=C
PATH=$PATH:/usr/sbin:/sbin
systemctl show ` + serviceStatusProps + ` -- "$1"`
}

// ParseServiceStatus reads systemctl show for one unit.
func ParseServiceStatus(out []byte) (ServiceStatus, bool) {
	props := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		key, val, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		props[strings.TrimSpace(key)] = strings.TrimSpace(val)
	}
	if props["Id"] == "" && len(props) == 0 {
		return ServiceStatus{}, false
	}
	prop := func(name string) string {
		v := props[name]
		if v == "" || v == "[not set]" {
			return ""
		}
		return v
	}
	number := func(name string) *uint64 {
		v := prop(name)
		if v == "" {
			return nil
		}
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil || n == ^uint64(0) {
			return nil
		}
		return &n
	}
	var st ServiceStatus
	st.Description = prop("Description")
	st.Load = prop("LoadState")
	st.Active = prop("ActiveState")
	st.Sub = prop("SubState")
	st.FileState = prop("UnitFileState")
	st.Path = prop("FragmentPath")
	st.Started = prop("ActiveEnterTimestamp")
	st.Stopped = prop("InactiveEnterTimestamp")
	if n := number("MainPID"); n != nil && *n != 0 {
		st.MainPID = uint32(*n)
	}
	st.Memory = number("MemoryCurrent")
	st.Tasks = number("TasksCurrent")
	st.Restarts = number("NRestarts")
	if v := prop("ExecMainStatus"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			st.ExitStatus = &n
		}
	}
	return st, true
}

// ServiceStatusOf reads name's details.
func ServiceStatusOf(ctx context.Context, client *ssh.Client, name string) (ServiceStatus, error) {
	script := ServiceStatusCommand(name)
	if script == "" {
		return ServiceStatus{}, errors.New("session: bad service name")
	}
	out, err := runService(ctx, client, script, name)
	if err != nil {
		return ServiceStatus{}, err
	}
	st, ok := ParseServiceStatus(out)
	if !ok {
		return ServiceStatus{}, errors.New("session: unreadable systemctl output")
	}
	return st, nil
}

// ServiceLogCommand is the script reading name's last 200 journal lines.
// The journal is root's to read, so a login that is not root asks sudo
// and settles for what it may see.
func ServiceLogCommand(name string) string {
	if !ServiceNameOK(name) {
		return ""
	}
	return `export LC_ALL=C
PATH=$PATH:/usr/sbin:/sbin
if test "$(id -u)" = 0; then
  journalctl -u "$1" -n 200 --no-pager
else
  sudo -n journalctl -u "$1" -n 200 --no-pager 2>/dev/null || journalctl -u "$1" -n 200 --no-pager
fi`
}

// ServiceLogs is the unit's last journal lines.
func ServiceLogs(ctx context.Context, client *ssh.Client, name string) (string, error) {
	script := ServiceLogCommand(name)
	if script == "" {
		return "", errors.New("session: bad service name")
	}
	out, err := runService(ctx, client, script, name)
	return string(out), err
}

func runService(ctx context.Context, client *ssh.Client, script string, args ...string) ([]byte, error) {
	var out, errb bytes.Buffer
	cmd := "sh -c " + shellQuote(script) + " _"
	for _, a := range args {
		cmd += " " + shellQuote(a)
	}
	err := Exec(ctx, client, cmd, &out, &errb)
	if err != nil {
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			var exit *ssh.ExitError
			if errors.As(err, &exit) {
				return out.Bytes(), errors.New(lastLine(msg))
			}
		}
		return out.Bytes(), err
	}
	return out.Bytes(), nil
}

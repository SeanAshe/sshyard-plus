package session

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const serviceList = `@V
systemd 249 (249.11-0ubuntu3.12)
+PAM +AUDIT
@S
degraded
@L
Id=socialc.service
Description=Social Callback Proxy Service
ActiveState=active
UnitFileState=enabled
FragmentPath=/etc/systemd/system/socialc.service
LoadState=loaded

Id=ssh.service
Description=OpenBSD Secure Shell server
ActiveState=active
UnitFileState=enabled
FragmentPath=/lib/systemd/system/ssh.service
LoadState=loaded

Id=ssh.service
Description=OpenBSD Secure Shell server
ActiveState=active
UnitFileState=enabled
FragmentPath=/lib/systemd/system/ssh.service
LoadState=loaded

Id=getty@.service
Description=Getty
ActiveState=inactive
UnitFileState=enabled
FragmentPath=/lib/systemd/system/getty@.service
LoadState=loaded

Id=auditd.service
Description=auditd.service
ActiveState=inactive
UnitFileState=
FragmentPath=
LoadState=not-found

Id=certbot.service
Description=Certbot
ActiveState=failed
UnitFileState=static
FragmentPath=/lib/systemd/system/certbot.service
LoadState=loaded

Id=ModemManager.service
Description=Modem Manager
ActiveState=inactive
UnitFileState=disabled
FragmentPath=/lib/systemd/system/ModemManager.service
LoadState=loaded
`

func TestParseServices(t *testing.T) {
	info, err := ParseServices([]byte(serviceList))
	if err != nil {
		t.Fatal(err)
	}
	if !info.Ready || info.Version != "systemd 249" || info.State != "degraded" || !info.Degraded() {
		t.Fatalf("%+v", info)
	}
	var names []string
	for _, u := range info.Units {
		names = append(names, u.Name)
	}
	want := []string{"ModemManager.service", "certbot.service", "socialc.service", "ssh.service"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("units %v", names)
	}
	if !info.Units[2].Custom() || info.Units[3].Custom() {
		t.Fatal("custom")
	}
	all, run, stop, failed := CountUnits(info.Units, "")
	if all != 4 || run != 2 || stop != 1 || failed != 1 {
		t.Fatalf("counts %d %d %d %d", all, run, stop, failed)
	}
	custom, system := FilterUnits(info.Units, SvcAll, "")
	if len(custom) != 1 || custom[0].Name != "socialc.service" || len(system) != 3 {
		t.Fatalf("groups %v %v", custom, system)
	}
	custom, system = FilterUnits(info.Units, SvcFailed, "cert")
	if len(custom) != 0 || len(system) != 1 || system[0].Name != "certbot.service" {
		t.Fatalf("search %+v %+v", custom, system)
	}
	if _, n, _, _ := CountUnits(info.Units, "secure"); n != 1 {
		t.Fatal("search count")
	}
	got, err := ParseServices([]byte("@U\nLinux\n"))
	if err != nil || got.Ready || got.System != "Linux" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := ParseServices([]byte("nope\n")); err == nil {
		t.Fatal("unreadable output accepted")
	}
}

func TestServiceCommand(t *testing.T) {
	got := ServiceCommand(ServiceRestart, "nginx.service")
	if !strings.Contains(got, `systemctl restart -- "$1"`) || !strings.Contains(got, "sudo -n") || !strings.Contains(got, `echo "@R $?"`) {
		t.Fatalf("%q", got)
	}
	for _, bad := range []string{"", "nginx", "a;reboot.service", "a b.service", "$(id).service", "getty@.service", `a\\b.service`} {
		if ServiceCommand(ServiceStop, bad) != "" {
			t.Errorf("%q let through", bad)
		}
	}
	if ServiceCommand("kill", "nginx.service") != "" {
		t.Fatal("unknown action")
	}
	if !ServiceNameOK(`systemd-fsck@dev-disk-by\x2duuid.service`) {
		t.Fatal("escaped name refused")
	}
	ssh := Unit{Active: "active", FileState: "enabled"}
	if acts := ssh.Actions(); len(acts) != 2 || acts[0] != ServiceStop || acts[1] != ServiceRestart {
		t.Fatalf("running %+v", acts)
	}
	if boot, ok := ssh.BootAction(); !ok || boot != ServiceDisable {
		t.Fatal("disable")
	}
	modem := Unit{Active: "inactive", FileState: "disabled"}
	if acts := modem.Actions(); len(acts) != 1 || acts[0] != ServiceStart {
		t.Fatalf("stopped %+v", acts)
	}
	if boot, ok := modem.BootAction(); !ok || boot != ServiceEnable {
		t.Fatal("enable")
	}
	if _, ok := (Unit{FileState: "static"}).BootAction(); ok {
		t.Fatal("static can be enabled")
	}
}

func TestParseServiceResult(t *testing.T) {
	if err := ParseServiceResult([]byte("done\n@R 0\n")); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(ParseServiceResult([]byte("sudo: a password is required\n@R 1\n")), ErrServiceRoot) {
		t.Fatal("root")
	}
	if !errors.Is(ParseServiceResult([]byte("sudo: command not found\n@R 127\n")), ErrServiceNoSudo) {
		t.Fatal("no sudo")
	}
	err := ParseServiceResult([]byte("Job for nginx.service failed because the control process exited with error code.\nSee systemctl status.\n@R 1\n"))
	if err == nil || !strings.Contains(err.Error(), "Job for nginx.service failed") {
		t.Fatal(err)
	}
	if ParseServiceResult([]byte("")) == nil {
		t.Fatal("empty accepted")
	}
}

// TestServiceListScript runs the list script against a stand-in systemctl
// and checks that templates are left out while instances stay.
func TestServiceListScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	script := `#!/bin/sh
case "$1" in
--version) echo 'systemd 249 (249.11)';;
is-system-running) echo degraded; exit 1;;
list-unit-files) printf '%s\n' 'ssh.service enabled enabled' 'getty@.service enabled enabled';;
list-units) printf '%s\n' 'getty@tty1.service loaded active running Getty' 'nginx.service loaded active running Nginx';;
show)
  shift
  while test "$1" = -p; do shift 2; done
  shift
  for unit in "$@"; do
    printf 'Id=%s\nDescription=%s\nActiveState=active\nUnitFileState=enabled\nFragmentPath=/etc/systemd/system/%s\nLoadState=loaded\n\n' "$unit" "$unit" "$unit"
  done
  ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	booted := t.TempDir()
	body := strings.Replace(serviceListScript, "/run/systemd/system", booted, 1)
	cmd := exec.Command("sh", "-c", body)
	cmd.Env = []string{"PATH=" + dir, "LC_ALL=C"}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	info, err := ParseServices(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "systemd 249" || !info.Degraded() {
		t.Fatalf("%+v", info)
	}
	var names []string
	for _, u := range info.Units {
		names = append(names, u.Name)
		if !u.Custom() {
			t.Errorf("%s not custom", u.Name)
		}
	}
	if strings.Join(names, ",") != "getty@tty1.service,nginx.service,ssh.service" {
		t.Fatalf("units %v", names)
	}
}

func TestParseServiceStatus(t *testing.T) {
	st, ok := ParseServiceStatus([]byte("Id=socialc.service\nDescription=Social Callback Proxy Service\nLoadState=loaded\nActiveState=active\nSubState=running\nUnitFileState=enabled\nFragmentPath=/etc/systemd/system/socialc.service\nMainPID=781\nMemoryCurrent=1990656\nTasksCurrent=5\nNRestarts=0\nExecMainStatus=0\nActiveEnterTimestamp=Tue 2026-09-15 17:49:07 CST\nInactiveEnterTimestamp=\n"))
	if !ok || st.MainPID != 781 || st.Memory == nil || *st.Memory != 1990656 || st.Tasks == nil || *st.Tasks != 5 || st.Restarts == nil || *st.Restarts != 0 || st.ExitStatus == nil || *st.ExitStatus != 0 {
		t.Fatalf("%+v", st)
	}
	if st.Started != "Tue 2026-09-15 17:49:07 CST" || st.Stopped != "" || st.Path == "" {
		t.Fatalf("%+v", st)
	}
	stopped, ok := ParseServiceStatus([]byte("Id=a.service\nMainPID=0\nMemoryCurrent=[not set]\nTasksCurrent=18446744073709551615\n"))
	if !ok || stopped.MainPID != 0 || stopped.Memory != nil || stopped.Tasks != nil {
		t.Fatalf("%+v", stopped)
	}
	if !strings.Contains(ServiceStatusCommand("nginx.service"), "MainPID") || ServiceStatusCommand("nginx") != "" {
		t.Fatal("status command")
	}
	if !strings.Contains(ServiceLogCommand("nginx.service"), `journalctl -u "$1" -n 200`) || ServiceLogCommand("a b.service") != "" {
		t.Fatal("log command")
	}
}

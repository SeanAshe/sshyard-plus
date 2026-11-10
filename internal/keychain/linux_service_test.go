package keychain

import (
	"bytes"
	"errors"
	"os"
	"testing"
)

type linuxUnsupportedError struct{ message string }

func (e *linuxUnsupportedError) Error() string { return e.message }
func (e *linuxUnsupportedError) Unwrap() error { return errors.ErrUnsupported }

func linuxFailureCases() []struct {
	message string
	kind    LinuxServiceErrorKind
} {
	return []struct {
		message string
		kind    LinuxServiceErrorKind
	}{
		{"no credential store: cannot load libsecret-1.so.0: missing (install libsecret: libsecret-1-0 on Debian/Ubuntu, libsecret on Fedora)", LinuxServiceDependency},
		{"no credential store: cannot load libglib-2.0.so.0: missing", LinuxServiceDependency},
		{"no credential store: cannot load libgio-2.0.so.0: missing", LinuxServiceDependency},
		{"no credential store: libsecret is too old: secrets need libsecret 0.19 or later", LinuxServiceDependency},
		{"no credential store: no session bus: secret service: Cannot autolaunch D-Bus", LinuxServiceSessionBus},
		{"secret service: GDBus.Error:org.freedesktop.DBus.Error.AccessDenied: policy denied", LinuxServiceAccessDenied},
		{"secret service: GDBus.Error:org.freedesktop.DBus.Error.AuthFailed: rejected", LinuxServiceAccessDenied},
		{"secret service: GDBus.Error:org.freedesktop.DBus.Error.Spawn.ExecFailed: failed", LinuxServiceStartFailed},
		{"secret service: GDBus.Error:org.freedesktop.DBus.Error.Spawn.ChildExited: exited", LinuxServiceStartFailed},
		{"no credential store: secret service: GDBus.Error:org.freedesktop.DBus.Error.ServiceUnknown: absent", LinuxServiceMissing},
		{"no credential store: secret service: The name org.freedesktop.secrets was not provided by any .service files", LinuxServiceMissing},
		{"no credential store: secret service: 没有提供 org.freedesktop.secrets 名称", LinuxServiceMissing},
		{"secret service: GDBus.Error:org.freedesktop.DBus.Error.ServiceUnknown: absent", LinuxServiceStartFailed},
		{"secret service: Error calling StartServiceByName for org.freedesktop.secrets: GDBus.Error:org.freedesktop.DBus.Error.Spawn.ChildExited: Process org.freedesktop.secrets exited with status 1", LinuxServiceStartFailed},
		{"secret service: 调用 StartServiceByName 来获取 org.freedesktop.secrets 时出错：: GDBus.Error:org.freedesktop.DBus.Error.TimedOut: activation timed out", LinuxServiceTimeout},
		{"secret service: GDBus.Error:org.freedesktop.DBus.Error.NoReply: no reply", LinuxServiceTimeout},
		{"secret service: GDBus.Error:org.freedesktop.Secret.Error.IsLocked: locked", LinuxServiceLocked},
		{"secret service: permission denied while keyring is locked", LinuxServiceUnknown},
		{"secret service: remote failure (OSStatus -25291)", LinuxServiceUnknown},
		{"no credential store: unknown loader failure", LinuxServiceUnknown},
	}
}

func TestLinuxServiceErrorMapping(t *testing.T) {
	for _, tc := range linuxFailureCases() {
		t.Run(tc.message, func(t *testing.T) {
			for _, unsupported := range []bool{false, true} {
				message := "mygo: secrets: " + tc.message
				var source error = errors.New(message)
				if unsupported {
					source = &linuxUnsupportedError{message}
				}
				err := mapErr(source)
				var le *LinuxServiceError
				if !errors.As(err, &le) || le.Kind != tc.kind {
					t.Fatalf("kind: %v", err)
				}
				if !errors.Is(err, source) || err.Error() == "" {
					t.Fatal("lost original error", err)
				}
				if errors.Is(err, ErrUnavailable) != unsupported || errors.Is(err, errors.ErrUnsupported) != unsupported {
					t.Fatal("unsupported chain changed", err)
				}
			}
		})
	}
}

func TestLinuxServiceErrorBoundaries(t *testing.T) {
	for _, message := range []string{
		"secret service: GDBus.Error:org.freedesktop.DBus.Error.AccessDenied: denied",
		"business: mygo: secrets: secret service: GDBus.Error:org.freedesktop.DBus.Error.AccessDenied: denied",
		"mygo: secrets: secret services: locked",
		"mygo: secrets: permission denied",
	} {
		source := errors.New(message)
		if got := mapErr(source); got != source {
			t.Fatalf("classified unrelated error: %v", got)
		}
	}
	for _, message := range []string{
		"secret service: prefix GDBus.Error:org.freedesktop.DBus.Error.AccessDenied: denied",
		"secret service: GDBus.Error:org.freedesktop.DBus.Error.AccessDeniedExtra: denied",
		"secret service: GDBus.Error:org.freedesktop.DBus.Error.AccessDenied:denied",
		"secret service: GDBus.Error:org.freedesktop.Secret.Error.IsLocked.More: locked",
		"secret service: GDBus.Error:org.freedesktop.DBus.Error.Spawn.Invented: failure",
		"secret service: org.freedesktop.Secret.Error.IsLocked: locked",
		"secret service: 无法解锁密钥环",
		"secret service: Error calling StartServiceByName for org.freedesktop.secrets: Timeout was reached",
		"secret service: Error calling StartServiceByName for org.freedesktop.secrets: GDBus.Error:org.freedesktop.DBus.Error.AccessDenied: denied",
		"secret service: failed: GDBus.Error:org.freedesktop.DBus.Error.Spawn.ChildExited: exited",
		"no credential store: prefix no session bus: unavailable",
		"no credential store: cannot load libsecret-1.so.0.extra: failure",
		"no credential store: libsecret is too old: secrets need libsecret 0.19 or later extra",
	} {
		source := errors.New("mygo: secrets: " + message)
		var le *LinuxServiceError
		if err := mapErr(source); !errors.As(err, &le) || le.Kind != LinuxServiceUnknown || !errors.Is(err, source) {
			t.Fatalf("inexact signal classified: %v", err)
		}
	}
}

type linuxFailureSystem struct {
	Backend
	err             error
	writes, deletes int
}

func (s *linuxFailureSystem) Get(string) ([]byte, error) { return nil, s.err }
func (s *linuxFailureSystem) Set(string, []byte) error   { s.writes++; return s.err }
func (s *linuxFailureSystem) Delete(string) error        { s.deletes++; return s.err }

func TestLinuxServiceFailurePreservesStore(t *testing.T) {
	for _, state := range []string{"new", "master-only", "existing"} {
		for _, tc := range linuxFailureCases() {
			t.Run(state+"/"+tc.message, func(t *testing.T) {
				o := options(t)
				mem := o.System.(*Memory)
				if state == "existing" {
					k := openTest(t, o)
					if err := k.Set("secret", []byte("value")); err != nil {
						t.Fatal(err)
					}
					k.Close()
				}
				if state == "master-only" {
					if err := mem.Set(MasterKey, bytes.Repeat([]byte{4}, 32)); err != nil {
						t.Fatal(err)
					}
				}
				beforeKey, _ := mem.Get(MasterKey)
				beforeFile, beforeErr := os.ReadFile(o.Path)
				source := &linuxUnsupportedError{"mygo: secrets: " + tc.message}
				s := &linuxFailureSystem{Backend: mem, err: mapErr(source)}
				o.System = s
				k, err := Open(o)
				var le *LinuxServiceError
				if k != nil || !errors.Is(err, source) || !errors.As(err, &le) || le.Kind != tc.kind {
					t.Fatalf("failure masked: %v", err)
				}
				afterKey, _ := mem.Get(MasterKey)
				afterFile, afterErr := os.ReadFile(o.Path)
				if s.writes != 0 || s.deletes != 0 || !bytes.Equal(beforeKey, afterKey) || !bytes.Equal(beforeFile, afterFile) || errors.Is(beforeErr, os.ErrNotExist) != errors.Is(afterErr, os.ErrNotExist) {
					t.Fatal("failure modified master or ciphertext")
				}
			})
		}
	}
}

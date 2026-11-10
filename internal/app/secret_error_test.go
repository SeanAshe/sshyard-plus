package app

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/locales"
)

func TestSecretStoreErrorTextSanitizesTypedErrors(t *testing.T) {
	for _, lang := range []string{"en", "zh-Hans"} {
		t.Run(lang, func(t *testing.T) {
			m := locales.In(lang)
			cases := []struct {
				err  error
				want string
			}{
				{&keychain.OpError{Op: "file-write", Err: &os.PathError{Op: "write", Path: "/private/secret-data/secrets.enc", Err: syscall.ENOSPC}}, m.SecretsFailureDetail(m.SecretsStageWriteFile(), m.SecretsReasonDiskFull())},
				{&keychain.OpError{Op: "file-decrypt", Err: fmt.Errorf("private-token: %w", keychain.ErrCorrupt)}, m.SecretsFailureDetail(m.SecretsStageDecrypt(), m.SecretsReasonCorrupt())},
				{&keychain.OpError{Op: "master-read", Err: &keychain.SystemError{Code: -25308, Err: errors.New("private-token /private/secret-data")}}, m.SecretsFailureDetail(m.SecretsStageReadMaster(), m.SecretsSystemCode(m.SecretsReasonInteraction(), "-25308"))},
			}
			for _, tc := range cases {
				got := secretStoreErrorText(m, tc.err)
				if got != tc.want || strings.Contains(got, "private-token") || strings.Contains(got, "/private/") {
					t.Fatalf("unsafe or imprecise error classification: %q", got)
				}
			}
			if got := secretStoreErrorText(m, errFlaky); got != errFlaky.Error() {
				t.Fatal("unrelated backend error wording changed")
			}
			if got := secretStoreErrorText(m, keychain.ErrMasterMissing); got != m.SecretsFailureDetail(m.SecretsStageOpen(), m.SecretsReasonMasterMissing()) {
				t.Fatal("known store error was not classified")
			}
		})
	}
}

func TestLinuxSecretErrorsSafeAndSpecific(t *testing.T) {
	for _, lang := range []string{"en", "zh-Hans", "zh-Hant", "ko", "ja"} {
		m := locales.In(lang)
		for _, tc := range linuxSecretReasons(m) {
			cause := &keychain.LinuxServiceError{Kind: tc.kind, Err: fmt.Errorf("private-token /private/keyring: %w", errors.ErrUnsupported)}
			for _, err := range []error{cause, &keychain.OpError{Op: "master-write", Err: errors.Join(keychain.ErrUnavailable, cause)}} {
				stage := m.SecretsStageOpen()
				var op *keychain.OpError
				if errors.As(err, &op) {
					stage = m.SecretsStageWriteMaster()
				}
				want := m.SecretsFailureDetail(stage, tc.reason)
				if got := secretStoreErrorText(m, err); got != want {
					t.Errorf("%s kind %d: got %q want %q", lang, tc.kind, got, want)
				}
				if got := snippetErrorText(m, err); got != want {
					t.Errorf("%s snippet kind %d: got %q want %q", lang, tc.kind, got, want)
				}
			}
		}
	}
}

func linuxSecretReasons(m locales.Messages) []struct {
	kind   keychain.LinuxServiceErrorKind
	reason string
} {
	return []struct {
		kind   keychain.LinuxServiceErrorKind
		reason string
	}{
		{keychain.LinuxServiceUnknown, m.SecretsReasonLinuxUnknown()},
		{keychain.LinuxServiceDependency, m.SecretsReasonLinuxDependency()},
		{keychain.LinuxServiceSessionBus, m.SecretsReasonLinuxSessionBus()},
		{keychain.LinuxServiceAccessDenied, m.SecretsReasonLinuxAccessDenied()},
		{keychain.LinuxServiceStartFailed, m.SecretsReasonLinuxStartFailed()},
		{keychain.LinuxServiceLocked, m.SecretsReasonLinuxLocked()},
		{keychain.LinuxServiceMissing, m.SecretsReasonLinuxMissing()},
		{keychain.LinuxServiceTimeout, m.SecretsReasonLinuxTimeout()},
	}
}

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/app"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// TestMain keeps the opt-in native event loop on the process's main thread.
// Normal unit tests neither open windows nor access a system credential store.
func TestMain(m *testing.M) {
	if mode := os.Getenv("SSHYARD_NATIVE_HELPER"); mode != "" {
		runtime.LockOSThread()
		if strings.HasPrefix(mode, "layout-") {
			runNativeLayout(mode, os.Getenv("SSHYARD_NATIVE_DIR"))
		} else {
			runNativeDesktop(mode, os.Getenv("SSHYARD_NATIVE_DIR"))
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestDesktopNativeLifecycle(t *testing.T) {
	if os.Getenv("SSHYARD_NATIVE_TEST") != "1" {
		t.Skip("set SSHYARD_NATIVE_TEST=1 on a desktop session to run real MyGo windows and tray")
	}
	for _, mode := range []string{"tray", "quit", "unavailable", "panel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(ctx, exe, "-test.run=^$")
			cmd.Env = append(os.Environ(), "SSHYARD_NATIVE_HELPER="+mode, "SSHYARD_NATIVE_DIR="+t.TempDir())
			output, err := cmd.CombinedOutput()
			if err != nil || !bytes.Contains(output, []byte("native lifecycle passed: "+mode)) {
				t.Fatalf("native subprocess: %v\n%s", err, output)
			}
			t.Log(string(output))
		})
	}
}

type nativeSecrets struct {
	*keychain.Memory
	release <-chan struct{}
}

func (s nativeSecrets) GetSilently(key string) ([]byte, error) {
	<-s.release
	return s.Get(key)
}

func nativeCheck(ok bool, message string) {
	if !ok {
		panic(message)
	}
}

// This sequential integration setup exceeds the usual function size so the
// isolated credential seed, actual UI loop and shutdown barrier stay together.
func runNativeDesktop(mode, dir string) {
	mygo.App.SetName("SSHYard Native Lifecycle Test")
	mygo.App.SetPath(mygo.PathUserData, dir)
	release := make(chan struct{})
	secrets := nativeSecrets{Memory: keychain.NewMemory(), release: release}
	store, err := keychain.Open(keychain.OpenOptions{
		Path: filepath.Join(dir, "secrets.enc"), LockPath: filepath.Join(dir, "secrets.lock"),
		Scope: "native-test", System: secrets,
	})
	nativeCheck(err == nil, fmt.Sprintf("open isolated credentials: %v", err))
	tokenBytes := make([]byte, 32)
	_, err = rand.Read(tokenBytes)
	nativeCheck(err == nil, "generate test token")
	token := hex.EncodeToString(tokenBytes)
	nativeCheck(store.Set(keychain.MCPToken, []byte(token)) == nil, "seed encrypted token")
	nativeCheck(store.Close() == nil, "close seed credentials")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	nativeCheck(err == nil, "allocate MCP test port")
	port := ln.Addr().(*net.TCPAddr).Port
	nativeCheck(ln.Close() == nil, "release MCP test port")
	s := settings.Default()
	s.MCPEnabled, s.MCPPort, s.CheckUpdates = true, port, false
	nativeCheck(settings.Save(filepath.Join(dir, "settings.json"), s) == nil, "save initial settings")
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	closed := 0
	completed := make(chan struct{})
	mygo.App.WhenReady(func() {
		var d *desktop
		a, err := app.New(app.Platform{
			DataDir: dir, SecretLockPath: filepath.Join(dir, "secrets.lock"), SecretScope: "native-test",
			HomeDir: dir, CacheDir: dir, Secrets: secrets, Post: postUI,
			SetTrayIcon: func(style string) error { return d.setTrayIcon(style) },
		})
		nativeCheck(err == nil, fmt.Sprintf("create native app: %v", err))
		draft := "retained native draft"
		w := mygo.NewWindow(mygo.WindowOptions{
			Title: "SSHYard Lifecycle Test", Width: 900, Height: 600,
			Content: ui.View(func(c *ui.Context) {
				ui.TextInput(c, &draft).Label("Lifecycle test draft")
				a.View(c)
			}),
		})
		d = newDesktop(a, w)
		nativeCheck(d.trayErr == nil, fmt.Sprintf("create real native tray: %v", d.trayErr))
		a.Start()
		mygo.App.OnQuit(func() {
			closed++
			a.Close()
			nativeCheck(closed == 1, "App.Close must run exactly once")
			_, err := (&http.Client{Timeout: time.Second}).Get(base + "/api/v1")
			nativeCheck(err != nil, "MCP listener survived app exit")
		})
		go driveNativeDesktop(mode, d, &draft, base, token, release, completed)
	})
	err = mygo.App.Run()
	nativeCheck(err == nil, fmt.Sprintf("native app loop: %v", err))
	<-completed
	nativeCheck(closed == 1, "App.Close must run exactly once")
	fmt.Println("native lifecycle passed:", mode)
}

func nativeMCP(base, token string) error {
	// Call a real MCP tool, beyond proving the listener can answer a ping.
	req, err := http.NewRequest(http.MethodPost, base+"/api/v1/list_hosts", strings.NewReader(`{}`))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK || bytes.Contains(body, []byte(`"error"`)) {
		return fmt.Errorf("MCP returned %d: %s", res.StatusCode, body)
	}
	return nil
}

// The long sequential scenario and explicit inputs keep native cancellation,
// MCP and restore evidence in one place rather than hiding ordering in fixtures.
func driveNativeDesktop(mode string, d *desktop, draft *string, base, token string, release, completed chan struct{}) {
	defer close(completed)
	mygo.RunOnMain(func() {
		d.window.Close()
		nativeCheck(!d.window.IsDestroyed() && !d.window.IsVisible(), "close must hide same native window")
		nativeCheck(len(mygo.Windows()) == 1, "hide must not create another window")
		nativeDock(false)
		// Complete silent unlock only after hiding; its UI callback must start
		// MCP without a visible view frame to drain the application queue.
		close(release)
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		err := nativeMCP(base, token)
		if err == nil {
			break
		}
		nativeCheck(time.Now().Before(deadline), fmt.Sprintf("MCP startup: %v", err))
		time.Sleep(20 * time.Millisecond)
	}
	mygo.RunOnMain(func() {
		nativeCheck(!d.window.IsVisible(), "silent unlock unexpectedly restored the window")
		nativeDock(false)
		for _, style := range []string{settings.TrayIconMono, settings.TrayIconColor} {
			s := d.app.Settings()
			s.TrayIcon = style
			nativeCheck(d.app.ApplySettings(s) == nil, "change native tray icon style live")
			nativeCheck(d.app.Settings().TrayIcon == style, "tray icon choice not applied")
		}
		d.refreshTrayIcon()
		nativeCheck(d.trayErr == nil, "style or appearance update broke tray")
	})
	nativeCheck(nativeMCP(base, token) == nil, "MCP tool unavailable while main window hidden")
	if mode == "panel" {
		driveNativePanel(d, draft, base, token)
	}
	mygo.RunOnMain(func() {
		d.show()
		d.show()
		nativeCheck(d.window.IsVisible() && !d.window.IsDestroyed(), "restore lost original window")
		nativeCheck(len(mygo.Windows()) == 1 && *draft == "retained native draft", "restore duplicated window or lost draft")
		nativeDock(true)
	})
	nativeCheck(nativeMCP(base, token) == nil, "restore blocked MCP")
	var editor *mygo.Window
	allowEditor := false
	mygo.RunOnMain(func() {
		editor = mygo.NewWindow(mygo.WindowOptions{Title: "Unsaved editor", Width: 300, Height: 200, Content: ui.View(func(*ui.Context) {})})
		editor.OnClose(func(e *mygo.CloseEvent) {
			if !allowEditor {
				e.PreventDefault()
			}
		})
		s := d.app.Settings()
		s.CloseBehavior = settings.CloseQuit
		nativeCheck(d.app.ApplySettings(s) == nil, "change close behavior live")
		d.window.Close()
	})
	mygo.RunOnMain(func() {
		nativeCheck(!d.window.IsDestroyed() && d.window.IsVisible() && !editor.IsDestroyed(), "editor veto lost main window")
		nativeDock(true)
		s := d.app.Settings()
		s.CloseBehavior = settings.CloseToTray
		nativeCheck(d.app.ApplySettings(s) == nil, "restore tray behavior live")
		d.window.Close()
		nativeCheck(!d.window.IsVisible() && !d.window.IsDestroyed(), "editor veto left permanent quit flag")
		nativeDock(false)
		d.show()
		if mode == "panel" {
			d.openPanel()
			nativeCheck(d.panel != nil, "open panel before cancelled quit")
		}
		// The tray's explicit Quit action must be retryable after the veto.
		d.menu().Items()[len(d.menu().Items())-1].Click(nil, nil)
	})
	mygo.RunOnMain(func() {
		nativeCheck(!d.window.IsDestroyed() && !editor.IsDestroyed(), "tray quit ignored editor veto")
		nativeCheck(d.panel == nil && d.panelView == nil, "cancelled quit retained popup state")
		nativeDock(true)
		allowEditor = true
		if mode == "unavailable" {
			d.trayErr = errors.New("injected native tray creation failure")
			d.window.Close()
			nativeCheck(d.window.IsVisible() && !d.window.IsDestroyed(), "tray unavailable hid the app")
			nativeDock(true)
		}
		if mode == "quit" {
			s := d.app.Settings()
			s.CloseBehavior = settings.CloseQuit
			nativeCheck(d.app.ApplySettings(s) == nil, "choose complete exit")
			d.window.Close()
		} else {
			if mode == "panel" {
				d.openPanel()
				nativeCheck(d.panel != nil, "open panel before complete quit")
			}
			d.menu().Items()[len(d.menu().Items())-1].Click(nil, nil)
		}
	})
}

func nativeDock(visible bool) {
	if runtime.GOOS != "darwin" {
		return // Windows/Linux taskbar state requires acceptance on those desktops.
	}
	want := mygo.ActivationPolicyAccessory
	if visible {
		want = mygo.ActivationPolicyRegular
	}
	nativeCheck(mygo.App.ActivationPolicy() == want, "native Dock activation policy did not follow window visibility")
}

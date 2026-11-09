package updater

import (
	"context"
	"net"
	"net/http"
	"net/url"
)

// Route is how the update window's requests go out: Proxy as an
// http.Transport's, then Dial for each connection.
type Route struct {
	Proxy func(*http.Request) (*url.URL, error)
	Dial  func(ctx context.Context, network, addr string) (net.Conn, error)
}

// Backend is the update window of the app: MyGo's updater plugin, which
// checks soon after launch and every interval after, shows the release
// notes of a newer version in its own window and installs it; a fake in
// tests.
type Backend interface {
	// Enabled reports whether this build can update itself: development
	// builds and those installed by a package manager cannot.
	Enabled() bool
	// Check checks as the user asked: the update window shows at once and
	// says what it found.
	Check()
	// SetAutomatic turns the checks in the background on or off.
	SetAutomatic(on bool)
	// SetRoute sends the requests that follow through r.
	SetRoute(r Route)
	// Available is the newer version the last check found, or "".
	Available() string
	// OnChange calls fn on the main thread after Available may have
	// changed, and returns a function that removes it.
	OnChange(fn func()) (off func())
}

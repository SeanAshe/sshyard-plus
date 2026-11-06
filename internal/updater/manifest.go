package updater

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
)

// Repo is the GitHub repository SSHYard's releases are published in.
const Repo = "SeanAshe/sshyard-plus"

// MaxManifestSize bounds a manifest read from the network.
const MaxManifestSize = 1 << 20

// Manifest is MyGo's update manifest, update-<os>-<arch>.json.
type Manifest struct {
	Version   string  `json:"version"`
	Notes     string  `json:"notes,omitempty"`
	Date      string  `json:"date,omitempty"`
	URL       string  `json:"url"`
	Size      int64   `json:"size"`
	Signature string  `json:"signature"`
	Deltas    []Delta `json:"deltas,omitempty"`
}

// Delta is a delta update from an earlier version.
type Delta struct {
	From      string `json:"from"`
	URL       string `json:"url"`
	Size      int64  `json:"size"`
	Signature string `json:"signature"`
}

// ManifestName is the manifest's file name for a platform.
func ManifestName(goos, goarch string) string {
	return fmt.Sprintf("update-%s-%s.json", goos, goarch)
}

// ManifestURL is where the latest release's manifest of a platform is.
func ManifestURL(repo, goos, goarch string) string {
	return fmt.Sprintf("https://github.com/%s/releases/latest/download/%s", repo, ManifestName(goos, goarch))
}

// ParseManifest reads and checks a manifest.
func ParseManifest(r io.Reader) (*Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(io.LimitReader(r, MaxManifestSize))
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("updater: manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Validate checks that the manifest is complete and safe to follow.
func (m *Manifest) Validate() error {
	if m.Version == "" || m.URL == "" || m.Signature == "" {
		return errors.New("updater: manifest: incomplete (version, url and signature are required)")
	}
	if _, err := ParseVersion(m.Version); err != nil {
		return fmt.Errorf("updater: manifest: %w", err)
	}
	if err := checkURL(m.URL); err != nil {
		return err
	}
	if m.Size < 0 {
		return errors.New("updater: manifest: negative size")
	}
	for _, d := range m.Deltas {
		if _, err := ParseVersion(d.From); err != nil {
			return fmt.Errorf("updater: manifest delta: %w", err)
		}
		if err := checkURL(d.URL); err != nil {
			return err
		}
	}
	if m.Date != "" {
		if _, err := time.Parse(time.RFC3339, m.Date); err != nil {
			return fmt.Errorf("updater: manifest date: %w", err)
		}
	}
	return nil
}

func checkURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("updater: manifest url: %w", err)
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && isLoopback(u.Hostname())) {
		return fmt.Errorf("updater: manifest url %q is not https", raw)
	}
	return nil
}

func isLoopback(h string) bool {
	return h == "localhost" || h == "127.0.0.1" || h == "::1" || strings.HasSuffix(h, ".localhost")
}

// Newer reports whether the manifest offers a version newer than current.
func (m *Manifest) Newer(current string) bool { return Compare(m.Version, current) > 0 }

// Package updater is SSHYard's update client. Releases are published as
// MyGo publishes them on GitHub Releases: a signed archive per platform
// with a manifest, update-<os>-<arch>.json, in the latest release of
// SeanAshe/sshyard-plus. Installing goes through mygo.Updater, which checks the
// Ed25519 signature against the public key built into the app.
package updater

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a semantic version: major.minor.patch with an optional
// prerelease and an optional fork revision (0.0.19_12). Build metadata
// is ignored. The revision is how many commits this tree is ahead of
// upstream; a version without one compares as revision 0.
type Version struct {
	Major, Minor, Patch int
	Pre                 []string
	Fork                int
}

// ParseVersion parses "1.2.3", "v1.2.3-beta.1", "1.2.3+build" or "0.0.19_12".
func ParseVersion(s string) (Version, error) {
	var v Version
	raw := s
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	s, _, _ = strings.Cut(s, "+")
	if i := strings.LastIndex(s, "_"); i >= 0 {
		n, err := strconv.Atoi(s[i+1:])
		fork := s[i+1:]
		if err != nil || n < 0 || (len(fork) > 1 && fork[0] == '0') {
			return v, fmt.Errorf("updater: version %q has an invalid fork revision %q", raw, fork)
		}
		v.Fork = n
		s = s[:i]
	}
	core, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return v, fmt.Errorf("updater: version %q is not major.minor.patch", raw)
	}
	nums := [3]*int{&v.Major, &v.Minor, &v.Patch}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || (len(p) > 1 && p[0] == '0') {
			return v, fmt.Errorf("updater: version %q has an invalid number %q", raw, p)
		}
		*nums[i] = n
	}
	if hasPre {
		if pre == "" {
			return v, fmt.Errorf("updater: version %q has an empty prerelease", raw)
		}
		v.Pre = strings.Split(pre, ".")
		for _, id := range v.Pre {
			if id == "" {
				return v, fmt.Errorf("updater: version %q has an empty prerelease identifier", raw)
			}
		}
	}
	return v, nil
}

// String formats the version without a "v".
func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if len(v.Pre) > 0 {
		s += "-" + strings.Join(v.Pre, ".")
	}
	if v.Fork > 0 {
		s += "_" + strconv.Itoa(v.Fork)
	}
	return s
}

// Prerelease reports whether the version is a prerelease.
func (v Version) Prerelease() bool { return len(v.Pre) > 0 }

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// Compare orders versions by semver precedence: -1, 0 or 1.
func (v Version) Compare(o Version) int {
	if c := cmpInt(v.Major, o.Major); c != 0 {
		return c
	}
	if c := cmpInt(v.Minor, o.Minor); c != 0 {
		return c
	}
	if c := cmpInt(v.Patch, o.Patch); c != 0 {
		return c
	}
	switch {
	case len(v.Pre) == 0 && len(o.Pre) == 0:
		return cmpInt(v.Fork, o.Fork)
	case len(v.Pre) == 0:
		return 1
	case len(o.Pre) == 0:
		return -1
	}
	for i := 0; i < len(v.Pre) && i < len(o.Pre); i++ {
		a, b := v.Pre[i], o.Pre[i]
		an, aerr := strconv.Atoi(a)
		bn, berr := strconv.Atoi(b)
		switch {
		case aerr == nil && berr == nil:
			if c := cmpInt(an, bn); c != 0 {
				return c
			}
		case aerr == nil:
			return -1
		case berr == nil:
			return 1
		default:
			if c := strings.Compare(a, b); c != 0 {
				return c
			}
		}
	}
	if c := cmpInt(len(v.Pre), len(o.Pre)); c != 0 {
		return c
	}
	return cmpInt(v.Fork, o.Fork)
}

// Compare compares two version strings; an unparsable one sorts first.
func Compare(a, b string) int {
	va, ea := ParseVersion(a)
	vb, eb := ParseVersion(b)
	switch {
	case ea != nil && eb != nil:
		return 0
	case ea != nil:
		return -1
	case eb != nil:
		return 1
	}
	return va.Compare(vb)
}

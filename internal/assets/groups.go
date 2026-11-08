package assets

import (
	"maps"
	"strings"

	"github.com/Licoy/sshyard/internal/theme"
)

// GroupSep parts a subgroup from its group: "Prod/Web", "Prod/Web/Blue".
// Groups nest to any depth.
const GroupSep = "/"

// GroupParent returns the group a subgroup is in, "" for a top-level group.
func GroupParent(g string) string {
	if i := strings.LastIndex(g, GroupSep); i >= 0 {
		return g[:i]
	}
	return ""
}

// GroupLeaf returns a group's own name, without its parents'.
func GroupLeaf(g string) string {
	return g[strings.LastIndex(g, GroupSep)+1:]
}

// SubGroup returns the path of the subgroup name in parent, name alone
// for a top-level group.
func SubGroup(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + GroupSep + name
}

// InGroupTree reports whether g is group or one of its subgroups, at any
// depth.
func InGroupTree(g, group string) bool {
	return g == group || strings.HasPrefix(g, group+GroupSep)
}

// CleanGroup trims a group's path and the names in it.
func CleanGroup(g string) string {
	parts := strings.Split(strings.TrimSpace(g), GroupSep)
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return strings.Join(parts, GroupSep)
}

// treeOrder puts each subgroup right after its group, under the subgroups
// before it, among the others of that group in their order.
func treeOrder(groups []string) []string {
	out := make([]string, 0, len(groups))
	var walk func(parent string)
	walk = func(parent string) {
		for _, g := range groups {
			if GroupParent(g) == parent {
				out = append(out, g)
				walk(g)
			}
		}
	}
	walk("")
	return out
}

// GroupLook is how a group shows in the sidebar: its icon, as a host's
// (Host.Icon), and the color of it (Host.Color); "" for the folder in
// the sidebar's own color.
type GroupLook struct {
	Icon  string `json:"icon,omitempty"`
	Color string `json:"color,omitempty"`
}

func (l *GroupLook) normalize() {
	l.Icon = IconText(l.Icon)
	if _, ok := theme.LightTokens().TintNamed(l.Color); !ok {
		l.Color = ""
	}
}

// GroupLook returns how group g looks.
func (s *Store) GroupLook(g string) GroupLook {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.groupLooks[g]
}

// setGroupLook gives group g look, a fresh map so that commit can roll
// back to the one before; the plain look is no entry.
func (s *Store) setGroupLook(g string, look GroupLook) {
	s.groupLooks = maps.Clone(s.groupLooks)
	if look == (GroupLook{}) {
		delete(s.groupLooks, g)
		return
	}
	if s.groupLooks == nil {
		s.groupLooks = map[string]GroupLook{}
	}
	s.groupLooks[g] = look
}

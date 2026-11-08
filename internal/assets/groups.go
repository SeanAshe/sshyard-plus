package assets

import (
	"maps"
	"strings"

	"github.com/Licoy/sshyard/internal/theme"
)

// GroupSep parts a subgroup from its group: "Prod/Web". Groups go two
// deep: all after the first separator names the subgroup.
const GroupSep = "/"

// GroupParent returns the group a subgroup is in, "" for a top-level group.
func GroupParent(g string) string {
	if parent, _, ok := strings.Cut(g, GroupSep); ok {
		return parent
	}
	return ""
}

// GroupLeaf returns a group's own name, without its parent's.
func GroupLeaf(g string) string {
	if _, leaf, ok := strings.Cut(g, GroupSep); ok {
		return leaf
	}
	return g
}

// SubGroup returns the path of the subgroup name in parent, name alone
// for a top-level group.
func SubGroup(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + GroupSep + name
}

// InGroupTree reports whether g is group or one of its subgroups.
func InGroupTree(g, group string) bool {
	return g == group || (GroupParent(group) == "" && GroupParent(g) == group)
}

// CleanGroup trims a group's path and the names in it.
func CleanGroup(g string) string {
	g = strings.TrimSpace(g)
	if parent, leaf, ok := strings.Cut(g, GroupSep); ok {
		return strings.TrimSpace(parent) + GroupSep + strings.TrimSpace(leaf)
	}
	return g
}

// treeOrder puts each subgroup right after its group, among the others
// of that group in their order.
func treeOrder(groups []string) []string {
	out := make([]string, 0, len(groups))
	for _, g := range groups {
		if GroupParent(g) == "" {
			out = append(out, g)
			for _, sub := range groups {
				if GroupParent(sub) == g {
					out = append(out, sub)
				}
			}
		}
	}
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

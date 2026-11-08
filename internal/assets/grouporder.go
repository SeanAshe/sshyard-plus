package assets

import "slices"

// MoveGroup places name immediately before or after target, saving the full
// display order without making implicit groups into permanent empty groups.
// A group moved beside one of another group goes into that group, with its
// subgroups, hosts and look; it never goes into itself.
func (s *Store) MoveGroup(name, target string, after bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	groups := s.groupNames()
	if !slices.Contains(groups, name) || !slices.Contains(groups, target) {
		return ErrNotFound
	}
	if InGroupTree(target, name) {
		return nil
	}
	return s.placeGroup(groups, name, GroupParent(target), func(order []string) int {
		to := slices.Index(order, target)
		if after {
			to++
		}
		return to
	})
}

// NestGroup moves name, with its subgroups, hosts and look, into parent,
// "" for the top level, after the groups already there.
func (s *Store) NestGroup(name, parent string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	groups := s.groupNames()
	if !slices.Contains(groups, name) || (parent != "" && !slices.Contains(groups, parent)) {
		return ErrNotFound
	}
	if GroupParent(name) == parent {
		return nil
	}
	return s.placeGroup(groups, name, parent, func(order []string) int { return len(order) })
}

// placeGroup moves name into parent, renaming it unless it is there
// already, and to the place in the order of the groups without it that
// at returns.
func (s *Store) placeGroup(groups []string, name, parent string, at func(order []string) int) error {
	path := name
	if GroupParent(name) != parent {
		path = SubGroup(parent, GroupLeaf(name))
		if err := s.checkGroup(path, name); err != nil {
			return err
		}
	}
	order := renamed(groups, name, path)
	order = slices.Delete(order, slices.Index(order, path), slices.Index(order, path)+1)
	order = slices.Insert(order, at(order), path)
	if path == name && slices.Equal(groups, order) {
		return nil
	}
	return s.commit(func() {
		if path != name {
			s.renameGroup(name, path)
		}
		s.groupOrder = order
	})
}

// restoreGroupOrder accepts only distinct, existing group names. Restored
// groups lead, followed by this device's other groups in their current order.
func (s *Store) restoreGroupOrder(order []string) error {
	if len(order) == 0 {
		if len(s.groupOrder) > 0 {
			s.groupOrder = s.groupNames()
		}
		return nil
	}
	groups := s.groupNames()
	seen := make(map[string]bool, len(order))
	for _, g := range order {
		if seen[g] || !slices.Contains(groups, g) {
			return &FieldError{Field: "groupOrder", Reason: "invalid"}
		}
		seen[g] = true
	}
	s.groupOrder = slices.Clone(order)
	for _, g := range groups {
		if !seen[g] {
			s.groupOrder = append(s.groupOrder, g)
		}
	}
	return nil
}

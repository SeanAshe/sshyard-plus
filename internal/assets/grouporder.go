package assets

import "slices"

// MoveGroup places name immediately before or after target, saving the full
// display order without making implicit groups into permanent empty groups.
// Groups move among those of their own level: a subgroup among the
// subgroups of its group.
func (s *Store) MoveGroup(name, target string, after bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	groups := s.groupNames()
	from, to := slices.Index(groups, name), slices.Index(groups, target)
	if from < 0 || to < 0 {
		return ErrNotFound
	}
	if from == to || GroupParent(name) != GroupParent(target) {
		return nil
	}
	order := slices.Delete(slices.Clone(groups), from, from+1)
	to = slices.Index(order, target)
	if after {
		to++
	}
	order = slices.Insert(order, to, name)
	if slices.Equal(groups, order) {
		return nil
	}
	return s.commit(func() { s.groupOrder = order })
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

package acp

import (
	"context"
	"errors"

	"github.com/Licoy/sshyard/internal/ai/policy"

	sdk "github.com/coder/acp-go-sdk"
)

// Option categories the pane shows: the model and how much it thinks.
// The mode is SSHYard's to pin and is never offered.
const (
	CategoryModel   = "model"
	CategoryThought = "thought_level"
)

// Option is one of the agent's session settings with a list of values,
// as the agent reports it.
type Option struct {
	ID       string
	Name     string
	Category string
	Current  string
	Values   []Choice
}

// Choice is one value of an Option.
type Choice struct {
	Value       string
	Name        string
	Description string
}

// Value is the choice with this value, if the option has it.
func (o Option) Value(v string) (Choice, bool) {
	for _, c := range o.Values {
		if c.Value == v {
			return c, true
		}
	}
	return Choice{}, false
}

// Find is the option with this id.
func Find(opts []Option, id string) (Option, bool) {
	for _, o := range opts {
		if o.ID == id {
			return o, true
		}
	}
	return Option{}, false
}

// shown says whether the pane offers an option of this category.
func shown(category string) bool {
	return category == CategoryModel || category == CategoryThought
}

// options keeps the agent's model and thinking selectors, in its order.
// Values are capped; an option without a value is left out.
func options(in []sdk.SessionConfigOption) []Option {
	var out []Option
	for _, o := range in {
		sel := o.Select
		if sel == nil || sel.Category == nil || !shown(string(*sel.Category)) {
			continue
		}
		opt := Option{ID: clip(string(sel.Id), 200), Name: clip(sel.Name, 200), Category: string(*sel.Category), Current: clip(string(sel.CurrentValue), 200)}
		add := func(c sdk.SessionConfigSelectOption) {
			if len(opt.Values) >= maxChoices || c.Value == "" {
				return
			}
			ch := Choice{Value: clip(string(c.Value), 200), Name: clip(c.Name, 200)}
			if c.Description != nil {
				ch.Description = clip(*c.Description, 500)
			}
			if ch.Name == "" {
				ch.Name = ch.Value
			}
			opt.Values = append(opt.Values, ch)
		}
		if sel.Options.Ungrouped != nil {
			for _, c := range *sel.Options.Ungrouped {
				add(c)
			}
		}
		if sel.Options.Grouped != nil {
			for _, g := range *sel.Options.Grouped {
				for _, c := range g.Options {
					add(c)
				}
			}
		}
		if opt.ID != "" && len(opt.Values) > 0 {
			out = append(out, opt)
		}
	}
	return out
}

const maxChoices = 200

// Options are the agent's model and thinking selectors as last reported.
func (s *Session) Options() []Option {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Option(nil), s.options...)
}

func (s *Session) setOptions(in []sdk.SessionConfigOption) []Option {
	opts := options(in)
	s.mu.Lock()
	s.options = opts
	s.mu.Unlock()
	return opts
}

// SetOption sets one of the agent's selectors to a value it offered and
// returns the selectors as they are now: a new model can change the
// thinking levels.
func (s *Session) SetOption(ctx context.Context, id, value string) ([]Option, error) {
	s.mu.Lock()
	sid := s.sid
	opt, ok := Find(s.options, id)
	s.mu.Unlock()
	if sid == "" {
		return nil, errors.New("session is not open")
	}
	if !ok {
		return nil, errors.New("agent has no such option")
	}
	if _, ok := opt.Value(value); !ok {
		return nil, errInvalid
	}
	resp, err := s.conn.SetSessionConfigOption(ctx, sdk.SetSessionConfigOptionRequest{
		ValueId: &sdk.SetSessionConfigOptionValueId{
			ConfigId:  sdk.SessionConfigId(id),
			SessionId: sid,
			Value:     sdk.SessionConfigValueId(value),
		},
	})
	if err != nil {
		return nil, err
	}
	if len(resp.ConfigOptions) == 0 {
		s.mu.Lock()
		for i := range s.options {
			if s.options[i].ID == id {
				s.options[i].Current = value
			}
		}
		opts := append([]Option(nil), s.options...)
		s.mu.Unlock()
		return opts, nil
	}
	return s.setOptions(resp.ConfigOptions), nil
}

// Prefer sets each selector to the value wanted for it, when the agent
// offers that value and it is not already set. A value no longer offered
// is skipped. The first failure stops and is returned.
func (s *Session) Prefer(ctx context.Context, want map[string]string) error {
	// A model is set first: it can change the thinking levels offered.
	for _, category := range []string{CategoryModel, CategoryThought} {
		for _, o := range s.Options() {
			v, ok := want[o.ID]
			if o.Category != category || !ok || v == o.Current {
				continue
			}
			if _, ok := o.Value(v); !ok {
				continue
			}
			if _, err := s.SetOption(ctx, o.ID, v); err != nil {
				return err
			}
		}
	}
	return nil
}

// Probe starts the agent only to read its model and thinking selectors,
// then stops it. It needs no host: nothing is asked of the agent.
func Probe(ctx context.Context, cfg Config) ([]Option, error) {
	cfg.Gate, cfg.probe, cfg.OnUpdate = denyAll{}, true, nil
	s, err := Start(ctx, cfg)
	if err != nil {
		if s != nil {
			_ = s.Close()
		}
		return nil, err
	}
	defer s.Close()
	return s.Options(), nil
}

// denyAll refuses everything: a probing agent is never prompted, and any
// request it makes on its own is turned down.
type denyAll struct{}

func (denyAll) Authorize(context.Context, policy.Action, string) (policy.Verdict, error) {
	return policy.Verdict{Refusal: "not allowed"}, nil
}
func (denyAll) Done(policy.Action, policy.Verdict, int, int64, int64) error { return nil }

package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Licoy/sshyard/internal/ai/llm"
	"github.com/Licoy/sshyard/internal/aibrands"
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// providerForm edits one built-in provider. The API key is never written
// into settings; an empty key on edit keeps the keychain entry. An
// advanced parameter is sent only while its box is ticked.
type providerForm struct {
	id                 string
	name, base, model  string
	key, headers       string
	format             int
	models             []string
	useTemp, useTopP   bool
	useTokens, legacy  bool
	temp, topP, tokens float64
	advanced           bool
	isNew              bool
	err, warn, test    string
	testing, fetching  bool
	// fetched is how many models the last fetch found, -1 before one;
	// fetchErr is why it failed.
	fetched  int
	fetchErr string
	hold     focusHold
	// proxy is the proxy choice (settings.AIProvider.Proxy)
	proxy string
}

// providerFormats are the wire formats in the order the sheet lists them.
var providerFormats = []string{settings.AIFormatChat, settings.AIFormatResponses, settings.AIFormatAnthropic, settings.AIFormatTypeSafe}

// jevModels are TypeSafe's decision models, which its API does not list.
var jevModels = []string{settings.AIJevModel, "jev-preview"}

func formatIndex(format string) int {
	for i, f := range providerFormats {
		if f == format {
			return i
		}
	}
	return 0
}

// provider is the profile the form describes, before it is checked.
func (f *providerForm) provider(id string) settings.AIProvider {
	p := settings.AIProvider{ID: id, Name: strings.TrimSpace(f.name), Format: providerFormats[f.format],
		BaseURL: strings.TrimSpace(f.base), Model: strings.TrimSpace(f.model), Models: f.models,
		LegacyMaxTokens: f.legacy && f.format == 0, Proxy: f.proxy}
	if f.useTemp {
		v := f.temp
		p.Temperature = &v
	}
	if f.useTopP {
		v := f.topP
		p.TopP = &v
	}
	if f.useTokens {
		v := int(f.tokens)
		p.MaxTokens = &v
	}
	return p
}

// llmClient is the client for one profile and its key, going out
// through the profile's proxy.
func (a *App) llmClient(p settings.AIProvider, key string) *llm.Client {
	hdr := map[string]string{}
	for k, v := range p.Headers {
		hdr[k] = v
	}
	cl := &llm.Client{Format: p.Format, BaseURL: p.BaseURL, APIKey: key, Model: p.Model, Headers: hdr,
		Temperature: p.Temperature, TopP: p.TopP, LegacyMaxTokens: p.LegacyMaxTokens, HTTP: a.proxyClient(p.Proxy),
		Timeout: time.Duration(a.settings.AIRequestTimeout) * time.Second}
	if p.MaxTokens != nil {
		cl.MaxTokens = *p.MaxTokens
	}
	return cl
}

func (a *App) aiSettings(c *ui.Context, m locales.Messages, tok theme.Tokens, s *settings.Settings, changed *bool) {
	a.settingsSection(c, tok, nil, ui.Color{}, "", func(g *settingsGroup) {
		g.row(m.AiDefaultAgent(), "", func() {
			// The built-in agent, then each ACP agent by name; a backend
			// the file names but this build lacks shows as it is.
			names := acpNames(s.AIACPAgents)
			opts := append([]string{m.AiBuiltin()}, names...)
			backends := []string{"builtin"}
			for _, cfg := range s.AIACPAgents {
				backends = append(backends, "acp:"+cfg.ID)
			}
			cur := m.AiBuiltin()
			if cfg, ok := s.ACP(s.AIDefaultAgent); ok {
				cur = names[slices.IndexFunc(s.AIACPAgents, func(o settings.ACPConfig) bool { return o.ID == cfg.ID })]
			} else if s.AIDefaultAgent != "" && s.AIDefaultAgent != "builtin" {
				opts = append(opts, s.AIDefaultAgent)
				backends = append(backends, s.AIDefaultAgent)
				cur = s.AIDefaultAgent
			}
			if formSelect(c.Key("aiagentdef"), &cur, opts).Label(m.AiDefaultAgent()).Width(180).Changed() {
				if i := slices.Index(opts, cur); i >= 0 {
					s.AIDefaultAgent = backends[i]
					*changed = true
				}
			}
		})
		g.row(m.AiACP(), a.acpSummary(m), func() {
			if pill(c, m.AiACPConfigure(), false, false, false) {
				a.openACP()
			}
		}, func() {
			if len(a.settings.AIACPAgents) > 0 {
				if icon := aibrands.Agent(a.settings.AIACPAgents[0].Agent); icon != nil {
					brandImage(c, icon, 20)
				}
			}
		})
		g.row(m.AiContextLines(), "", func() {
			if ui.NumberInput(c.Key("ailines"), &a.aiLines, 50, 2000, 10).Changed() {
				s.AIContextLines = int(a.aiLines)
				*changed = true
			}
		})
		g.row(m.AiMaxToolCalls(), m.AiMaxToolCallsNote(), func() {
			in := formTextInput(c.Key("aicalls"), &a.aiCalls).Label(m.AiMaxToolCalls()).
				Placeholder(strconv.Itoa(settings.DefaultMaxToolCalls)).Width(88)
			if in.Changed() {
				if v, ok := maxToolCallsValue(a.aiCalls); ok {
					s.AIMaxToolCalls = v
					*changed = true
				}
			}
		})
		g.row(m.AiRequestTimeout(), m.AiRequestTimeoutNote(), func() {
			if ui.NumberInput(c.Key("aiwait"), &a.aiWait, settings.AIRequestTimeoutMin, settings.AIRequestTimeoutMax, 10).Label(m.AiRequestTimeout()).Changed() {
				s.AIRequestTimeout = int(a.aiWait)
				*changed = true
			}
		})
		g.row(m.AiAuditDays(), "", func() {
			if ui.NumberInput(c.Key("aidays"), &a.aiDays, 1, 3650, 1).Changed() {
				s.AIAuditRetentionDays = int(a.aiDays)
				*changed = true
			}
		})
		g.row(m.AiRedact(), m.AiRedactNote(), func() {
			on := s.AIRedact
			if formSwitch(c.Key("airedact"), &on).Label(m.AiRedact()).Changed() {
				s.AIRedact = on
				*changed = true
			}
		})
	})
	a.settingsSection(c, tok, icons.InstallKey, tok.TintAt(0), m.AiConsentSection(), func(g *settingsGroup) {
		if len(s.AIConsentHosts) == 0 {
			ui.Row(c).MinHeight(44).Padding(8, 14).Children(func() {
				ui.Text(c, m.AiConsentEmpty()).FontSize(theme.Body).TextColor(tok.TextMuted)
			})
		}
		for _, id := range s.AIConsentHosts {
			id := id
			label := id
			if h, ok := a.host(id); ok {
				label = h.Label()
			}
			g.row(label, "", func() {
				if iconButton(c, "consent:"+id, icons.Delete, m.AiForgetConsent()) {
					a.forgetConsent(id)
				}
			})
		}
	})
	a.settingsSection(c, tok, icons.ModeAsk, tok.TintAt(1), m.AiPermissionSection(), func(g *settingsGroup) {
		g.row(m.AiDefaultMode(), "", func() {
			a.aiModeIdx = modeIndex(s.AIDefaultMode)
			if formSegmented(c.Key("aimodedef"), &a.aiModeIdx, false, m.AiModeAsk(), m.AiModeReview(), m.AiModeFull()).Label(m.AiDefaultMode()).Changed() {
				mode := []string{"ask", "review", "full"}[a.aiModeIdx]
				if mode == "full" && a.settings.AIDefaultMode != "full" {
					a.openFull("settings", 0)
				} else {
					s.AIDefaultMode = mode
					*changed = true
				}
			}
		})
		a.reviewSettings(c, m, g, s, changed)
	})
	a.settingsSection(c, tok, icons.Robot, tok.TintAt(3), m.AiProvidersSection(), func(g *settingsGroup) {
		if len(s.AIProviders) == 0 {
			ui.Row(c).MinHeight(44).Padding(8, 14).Children(func() {
				ui.Text(c, m.AiNoProvider()).FontSize(theme.Body).TextColor(tok.TextMuted)
			})
		}
		def := s.AIDefaultProvider
		if def == "" {
			def = firstChat(s.AIProviders)
		}
		for _, p := range s.AIProviders {
			p := p
			g.row(providerLabel(p), p.BaseURL, func() {
				// The default shows a filled star; a star on another
				// makes it the default.
				star := IconButton{Icon: icons.Star, On: icons.StarOn, Label: m.AiSetDefaultProvider(), Active: p.ID == def, Disabled: !p.CanChat()}
				if star.Active {
					star.Label = m.AiDefaultProvider()
				}
				if star.Clicked(c, "prov:def:"+p.ID) && !star.Active {
					s.AIDefaultProvider = p.ID
					*changed = true
				}
				if iconButton(c, "prov:edit:"+p.ID, icons.Edit, m.Edit()) {
					pp := p
					a.openProvider(&pp)
				}
				if iconButton(c, "prov:del:"+p.ID, icons.Delete, m.Delete()) {
					a.provDeleteID, a.provDeleting = p.ID, true
				}
			}, func() { brandImage(c, aibrands.Provider(p), 20) })
		}
		g.divider()
		ui.Row(c).Padding(8, 14).Children(func() {
			if pill(c, m.AiAddProvider(), true, false, false) {
				a.openProvider(nil)
			}
		})
	})
}

// forgetConsent drops one host's first-use confirmation and saves it.
// A save error leaves the in-memory list unchanged and shows a toast.
func (a *App) forgetConsent(hostID string) {
	old := a.settings
	s := a.settings
	next := make([]string, 0, len(s.AIConsentHosts))
	for _, id := range s.AIConsentHosts {
		if id != hostID {
			next = append(next, id)
		}
	}
	s.AIConsentHosts = next
	if err := a.ApplySettings(s); err != nil {
		a.settings = old
		a.toast(func(m locales.Messages) ui.Toast {
			return ui.Toast{Title: m.ErrGeneric(err.Error()), Type: "error"}
		})
	}
}

func providerLabel(p settings.AIProvider) string {
	if p.Name != "" && p.Model != "" {
		return p.Name + " · " + p.Model
	}
	if p.Name != "" {
		return p.Name
	}
	if p.Model != "" {
		return p.Model
	}
	return p.ID
}

func (a *App) openProvider(p *settings.AIProvider) {
	f := &providerForm{base: providerURLHint(0), isNew: p == nil, temp: 1, topP: 1, tokens: 8192, fetched: -1}
	if p != nil {
		f.id, f.name, f.base, f.model = p.ID, p.Name, p.BaseURL, p.Model
		f.format, f.models, f.legacy = formatIndex(p.Format), append([]string(nil), p.Models...), p.LegacyMaxTokens
		f.proxy = p.Proxy
		if p.Temperature != nil {
			f.useTemp, f.temp = true, *p.Temperature
		}
		if p.TopP != nil {
			f.useTopP, f.topP = true, *p.TopP
		}
		if p.MaxTokens != nil {
			f.useTokens, f.tokens = true, float64(*p.MaxTokens)
		}
		var b strings.Builder
		for k, v := range p.Headers {
			fmt.Fprintf(&b, "%s: %s\n", k, v)
		}
		f.headers = b.String()
		f.advanced = f.useTemp || f.useTopP || f.useTokens || f.headers != ""
	}
	a.prov, a.provOpen = f, true
}

// providerURLHint is the official default base URL for a wire format.
func providerURLHint(format int) string {
	switch providerFormats[format] {
	case settings.AIFormatAnthropic:
		return "https://api.anthropic.com/v1"
	case settings.AIFormatTypeSafe:
		return "https://api.typesafe.ai/v1"
	}
	return "https://api.openai.com/v1"
}

func (a *App) providerSheet(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	if a.prov == nil {
		return
	}
	f := a.prov
	title := m.AiAddProvider()
	if !f.isNew {
		title = m.AiEditProvider()
	}
	note := func(s string) { ui.Text(c, s).FontSize(theme.Caption).FillWidth().TextColor(tok.TextMuted) }
	sheet(ui.Modal(c.Key("aiprov"), &a.provOpen, func() {
		sheetScroll(ui.Scroll(c.Key("ap:scroll")).MaxHeight(640)).Children(func() {
			ui.Column(c).Gap(theme.Space(3)).Width(460).Children(func() {
				sheetTitle(c, title)
				ui.Field(c, m.FieldName(), func() {
					in := formTextInput(c.Key("ap:name"), &f.name).Label(m.FieldName()).Placeholder("OpenAI").AutoFocus()
					f.hold.apply(c, f, in)
				})
				ui.Field(c, m.AiFormat(), func() {
					oldFormat := f.format
					if providerFormatControl(c.Key("ap:format"), m, &f.format).Changed() {
						if strings.TrimSpace(f.base) == "" || strings.TrimSpace(f.base) == providerURLHint(oldFormat) {
							f.base = providerURLHint(f.format)
						}
					}
				})
				typesafe := providerFormats[f.format] == settings.AIFormatTypeSafe
				if typesafe {
					note(m.AiTypeSafeNote())
				}
				ui.Field(c, m.AiProviderURL(), func() {
					formTextInput(c.Key("ap:url"), &f.base).Label(m.AiProviderURL()).Placeholder(providerURLHint(f.format))
				})
				ui.Field(c, m.AiAPIKey(), func() {
					hint := m.AiAPIKeyKeep()
					if f.isNew {
						hint = m.AiAPIKeyPlaceholder()
					}
					formTextInput(c.Key("ap:key"), &f.key).Password().Label(m.AiAPIKey()).Placeholder(hint)
				})
				note(m.AiAPIKeyNote())
				ui.Field(c, m.FieldProxy(), func() {
					a.proxySelect(c.Key("ap:proxy"), m, &f.proxy, proxyPick{system: true, owner: f,
						leave: func() { a.provOpen = false },
						back: func(id string) {
							a.prov, a.provOpen = f, true
							if id != "" {
								f.proxy = id
							}
						}})
				})
				note(m.ProxySystemNote())
				ui.Field(c, m.AiDefaultModel(), func() {
					ui.Row(c).Gap(theme.Space(2)).AlignItems(ui.Center).FillWidth().Children(func() {
						models, hint := f.models, "gpt-5.1"
						if typesafe {
							models, hint = jevModels, settings.AIJevModel
						}
						brandImage(c, aibrands.Model(f.model, f.provider(f.id)), 16)
						brandAutocomplete(c.Key("ap:model"), &f.model, models, f.provider(f.id)).Label(m.AiDefaultModel()).Placeholder(hint).Grow(1).MinWidth(0)
						if (IconButton{Icon: icons.Refresh, Label: m.AiFetchModels(), Disabled: f.fetching}).Clicked(c, "ap:fetch") {
							a.fetchModels()
						}
					})
				})
				switch {
				case f.fetching:
					note(m.AiFetchingModels())
				case f.fetchErr == "key":
					ui.Text(c, m.AiNoKey()).FontSize(theme.Caption).TextColor(tok.Danger)
				case f.fetchErr != "":
					ui.Text(c, m.AiFetchModelsFailed(f.fetchErr)).FontSize(theme.Caption).FillWidth().TextColor(tok.Danger)
				case f.fetched >= 0:
					note(m.AiFetchedModels(f.fetched))
				default:
					note(m.AiDefaultModelNote())
				}
				ui.Collapsible(c.Key("ap:adv"), m.AiAdvanced(), &f.advanced, func() {
					a.providerAdvanced(c, m, tok, f)
				})
				if f.warn != "" {
					ui.Text(c, f.warn).FontSize(theme.Footnote).FillWidth().TextColor(tok.Warning)
				}
				if f.err != "" {
					ui.Text(c, f.err).FontSize(theme.Footnote).FillWidth().TextColor(tok.Danger)
				}
				if f.test == "ok" {
					ui.Text(c, m.AiTestOK()).FontSize(theme.Footnote).TextColor(tok.Success)
				} else if f.test == "key" {
					ui.Text(c, m.AiNoKey()).FontSize(theme.Footnote).TextColor(tok.Danger)
				} else if f.test != "" {
					ui.Text(c, m.AiTestFail(f.test)).FontSize(theme.Footnote).FillWidth().TextColor(tok.Danger)
				}
				ui.Row(c).Gap(theme.Space(2)).Children(func() {
					if pill(c, m.AiTestConnection(), false, false, f.testing) {
						a.testProvider()
					}
					ui.Spacer(c)
					if pill(c, m.Cancel(), false, false, false) {
						a.provOpen = false
					}
					if pill(c, m.Save(), true, false, false) {
						a.saveProvider(m)
					}
				})
			})
		})
	}), tok)
	if !a.provOpen && !a.proxyKeeps(f) {
		a.prov = nil
	}
}

// providerAdvanced is the optional request parameters, each sent only
// while ticked, and the custom headers.
func (a *App) providerAdvanced(c *ui.Context, m locales.Messages, tok theme.Tokens, f *providerForm) {
	anthropic := providerFormats[f.format] == settings.AIFormatAnthropic
	param := func(key, label string, on *bool, v *float64, lo, hi, step float64) {
		ui.Row(c.Key(key)).Gap(theme.Space(3)).AlignItems(ui.Center).FillWidth().Children(func() {
			formCheckbox(c.Key("on"), on, label).Width(180)
			ui.NumberInput(c.Key("v"), v, lo, hi, step).Label(label).Disabled(!*on)
		})
	}
	ui.Column(c).Gap(theme.Space(2)).FillWidth().Padding(theme.Space(2), 0, 0, 0).Children(func() {
		// A decision model takes no sampling parameters.
		if providerFormats[f.format] != settings.AIFormatTypeSafe {
			ui.Text(c, m.AiParamsNote()).FontSize(theme.Caption).FillWidth().TextColor(tok.TextMuted)
			hi := 2.0
			if anthropic {
				hi = 1
			}
			param("ap:temp", m.AiProviderTemp(), &f.useTemp, &f.temp, 0, hi, 0.1)
			param("ap:topp", m.AiProviderTopP(), &f.useTopP, &f.topP, 0, 1, 0.05)
			param("ap:tok", m.AiProviderTokens(), &f.useTokens, &f.tokens, 1, settings.AIMaxTokensMax, 256)
		}
		switch providerFormats[f.format] {
		case settings.AIFormatChat:
			formCheckbox(c.Key("ap:legacy"), &f.legacy, m.AiLegacyTokens())
		case settings.AIFormatAnthropic:
			ui.Text(c, m.AiAnthropicTokensNote(strconv.Itoa(llm.AnthropicMaxTokens))).FontSize(theme.Caption).FillWidth().TextColor(tok.TextMuted)
		}
		ui.Field(c, m.AiHeaders(), func() {
			formTextArea(c.Key("ap:hdr"), &f.headers).Label(m.AiHeaders()).FillWidth().Height(72).Placeholder("X-Title: SSHYard")
		})
		ui.Text(c, m.AiHeadersNote()).FontSize(theme.Caption).FillWidth().TextColor(tok.TextMuted)
	})
}

func (a *App) providerDelete(c *ui.Context, m locales.Messages) {
	if !a.provDeleting {
		return
	}
	name := a.provDeleteID
	if p := a.providerByID(a.provDeleteID); p != nil && p.Name != "" {
		name = p.Name
	}
	switch ui.AlertDialog(c, &a.provDeleting, m.Delete(), m.AiDeleteProviderConfirm(name), m.Cancel(), m.Delete()) {
	case 1:
		id := a.provDeleteID
		a.runSecretAction(func() error { return a.deleteProvider(id) }, func(err error) { a.toastError(m.ErrGeneric(secretStoreErrorText(m, err))) })
	}
}

func (a *App) saveProvider(m locales.Messages) bool {
	if a.prov != nil && strings.TrimSpace(a.prov.key) != "" && !a.unlocked() {
		draft := a.prov
		a.withSecrets(func() {
			if a.prov == draft {
				a.saveProvider(m)
			}
		})
		return false
	}

	f := a.prov
	if f == nil {
		return false
	}
	f.err, f.warn = "", ""
	name, base, model := strings.TrimSpace(f.name), strings.TrimSpace(f.base), strings.TrimSpace(f.model)
	switch {
	case name == "":
		f.err = m.RequiredField(m.FieldName())
		return false
	case base == "":
		f.err = m.RequiredField(m.AiProviderURL())
		return false
	case model == "":
		f.err = m.RequiredField(m.AiDefaultModel())
		return false
	}
	warn, err := settings.CheckBaseURL(base)
	if err != nil {
		f.err = m.AiBadURL()
		return false
	}
	if warn != nil {
		f.warn = m.AiHTTPWarn()
	}
	hdr, err := parseHeaders(f.headers)
	if err != nil {
		f.err = m.AiBadHeader()
		return false
	}
	id := f.id
	if id == "" {
		id = newProviderID()
	}
	var previousKey []byte
	keyChanged := false
	if key := strings.TrimSpace(f.key); key != "" {
		previousKey, err = a.kc.Get(keychain.AIKey(id))
		if err != nil && !errors.Is(err, keychain.ErrNotFound) {
			f.err = m.ErrGeneric(secretStoreErrorText(m, err))
			return false
		}
		if err := a.kc.Set(keychain.AIKey(id), []byte(key)); err != nil {
			f.err = m.ErrGeneric(secretStoreErrorText(m, err))
			return false
		}
		keyChanged = true
		a.invalidateAIProvider(id)
	} else if f.isNew {
		f.err = m.AiNoKey()
		return false
	}
	p := f.provider(id)
	p.Headers = hdr
	s := a.settings
	next := append([]settings.AIProvider{}, s.AIProviders...)
	found := false
	for i := range next {
		if next[i].ID == id {
			next[i] = p
			found = true
		}
	}
	if !found {
		next = append(next, p)
	}
	s.AIProviders = next
	if err := a.ApplySettings(s); err != nil {
		f.err = m.AiSettingsSaveFailed()
		if keyChanged {
			var rollback error
			if previousKey == nil {
				rollback = a.kc.Delete(keychain.AIKey(id))
			} else {
				rollback = a.kc.Set(keychain.AIKey(id), previousKey)
			}
			if rollback != nil {
				f.err = m.AiSettingsRollbackFailed()
			}
		}
		return false
	}
	f.key = ""
	f.id = id
	// A plain-HTTP warning stays on the open sheet; the profile is saved.
	if warn == nil {
		a.provOpen = false
	}
	return true
}

func (a *App) deleteProvider(id string) error {
	// Revoke cached clients even if metadata persistence subsequently fails
	a.invalidateAIProvider(id)
	if err := a.kc.Delete(keychain.AIKey(id)); err != nil && !errors.Is(err, keychain.ErrNotFound) {
		return err
	}
	s := a.settings
	next := make([]settings.AIProvider, 0, len(s.AIProviders))
	for _, p := range s.AIProviders {
		if p.ID != id {
			next = append(next, p)
		}
	}
	s.AIProviders = next
	if s.AIReviewProvider == id {
		s.AIReviewProvider = ""
	}
	if s.AIDefaultProvider == id {
		s.AIDefaultProvider = ""
	}
	return a.ApplySettings(s)
}

// formKey is the key the form's requests use: the one typed, else the saved one.
func (a *App) formKey(f *providerForm) (string, error) {
	if key := strings.TrimSpace(f.key); key != "" {
		return key, nil
	}
	if f.id == "" {
		return "", nil
	}
	b, err := a.kc.Get(keychain.AIKey(f.id))
	if errors.Is(err, keychain.ErrNotFound) {
		return "", nil
	}
	return strings.TrimSpace(string(b)), err
}

func (a *App) testProvider() {
	if a.prov != nil && a.prov.id != "" && strings.TrimSpace(a.prov.key) == "" && !a.unlocked() {
		draft := a.prov
		a.withSecrets(func() {
			if a.prov == draft {
				a.testProvider()
			}
		})
		return
	}

	f := a.prov
	if f == nil || f.testing {
		return
	}
	key, err := a.formKey(f)
	if err != nil {
		f.err = secretStoreErrorText(locales.In(a.settings.Language), err)
		return
	}
	if key == "" {
		f.test = "key"
		return
	}
	hdr, _ := parseHeaders(f.headers)
	p := f.provider(f.id)
	p.Headers = hdr
	cl := a.llmClient(p, key)
	f.testing = true
	f.test = ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := cl.Test(ctx)
		a.post(func() {
			if a.prov != f {
				return
			}
			f.testing = false
			if err != nil {
				f.test = strings.ReplaceAll(err.Error(), key, "[redacted]")
				return
			}
			f.test = "ok"
		})
	}()
}

// fetchModels lists the endpoint's chat models for the model field and
// the panes' model menu; they are kept when the profile is saved.
func (a *App) fetchModels() {
	if a.prov != nil && a.prov.id != "" && strings.TrimSpace(a.prov.key) == "" && !a.unlocked() {
		draft := a.prov
		a.withSecrets(func() {
			if a.prov == draft {
				a.fetchModels()
			}
		})
		return
	}

	f := a.prov
	if f == nil || f.fetching {
		return
	}
	key, err := a.formKey(f)
	if err != nil {
		f.err = secretStoreErrorText(locales.In(a.settings.Language), err)
		return
	}
	if key == "" {
		f.fetchErr = "key"
		return
	}
	hdr, _ := parseHeaders(f.headers)
	p := f.provider(f.id)
	p.Headers = hdr
	cl := a.llmClient(p, key)
	f.fetching, f.fetchErr = true, ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		ids, err := cl.ListModels(ctx)
		a.post(func() {
			if a.prov != f {
				return
			}
			f.fetching = false
			if err != nil {
				f.fetchErr = strings.ReplaceAll(err.Error(), key, "[redacted]")
				return
			}
			f.models, f.fetched = ids, len(ids)
			if strings.TrimSpace(f.model) == "" && len(ids) > 0 {
				f.model = ids[0]
			}
		})
	}()
}

func parseHeaders(s string) (map[string]string, error) {
	h := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			return nil, settings.ErrHeader
		}
		h[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	if len(h) == 0 {
		return nil, nil
	}
	if err := settings.CheckHeaders(h); err != nil {
		return nil, err
	}
	return h, nil
}

func newProviderID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "p" + strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return "p" + hex.EncodeToString(b[:])
}

func linesOf(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.ContainsRune(line, 0) {
			out = append(out, line)
		}
	}
	return out
}

func hostModeIndex(mode string) int {
	switch mode {
	case "ask":
		return 1
	case "review":
		return 2
	case "full":
		return 3
	default:
		return 0
	}
}

// aiHostFields is the host editor's AI section. A Telnet or serial
// host, whose assistant has no files, has the mode only.
func (a *App) aiHostFields(c *ui.Context, m locales.Messages, tok theme.Tokens, f *hostForm, files bool) {
	ui.Collapsible(c.Key("f:ai"), m.AiSettings(), &f.aiOpen, func() {
		ui.Column(c).Gap(8).FillWidth().Margin(8, 0, 0, 0).Children(func() {
			ui.Text(c, m.AiHostMode()).FontSize(theme.Footnote).TextColor(tok.TextMuted)
			if formSegmented(c.Key("f:aimode"), &f.aiIdx, false, m.AiModeInherit(), m.AiModeAsk(), m.AiModeReview(), m.AiModeFull()).Label(m.AiHostMode()).Changed() {
				if f.aiIdx == 3 && f.orig.AIPolicy.Mode != "full" && !f.fullOK {
					f.aiIdx = f.aiPrev
					a.openFull("host", 0)
				} else {
					f.aiPrev = f.aiIdx
				}
			}
			if !files {
				return
			}
			ui.Text(c, m.AiWritableRoots()).FontSize(theme.Footnote).TextColor(tok.TextMuted)
			ui.Text(c, m.AiWritableRootsNote()).FontSize(theme.Caption).TextColor(tok.TextMuted)
			formTextArea(c.Key("f:roots"), &f.roots).FillWidth().Height(64)
			ui.Text(c, m.AiSensitive()).FontSize(theme.Footnote).TextColor(tok.TextMuted)
			ui.Text(c, m.AiSensitiveNote()).FontSize(theme.Caption).TextColor(tok.TextMuted)
			formTextArea(c.Key("f:sens"), &f.sens).FillWidth().Height(64)
		})
	})
}

// maxToolCallsValue reads the tool-call limit field: empty is the default
// (nil), 0 is no limit, and any other value must be a whole number the
// setting accepts. A text that is none of these leaves the setting as it was.
// No limit takes the explicit 0 so that clearing the field never lifts it.
func maxToolCallsValue(text string) (*int, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, true
	}
	n, err := strconv.Atoi(text)
	if err != nil || n < 0 || n > settings.AIMaxToolCallsMax {
		return nil, false
	}
	return &n, true
}

// maxToolCallsText is the setting as the field shows it, empty when unset
// so that the placeholder shows the default.
func maxToolCallsText(n *int) string {
	if n == nil {
		return ""
	}
	return strconv.Itoa(*n)
}

package migration

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"strings"
)

type object = map[string]any

func str(m object, k string) string { v, _ := m[k].(string); return v }
func obj(v any) object              { m, _ := v.(map[string]any); return m }
func arr(v any) []any               { a, _ := v.([]any); return a }
func port(v any) int {
	if v == nil || v == "" {
		return 0
	}
	n, e := strconv.Atoi(strings.TrimSpace(valueString(v)))
	if e != nil || n == 0 {
		return -1
	}
	return n
}
func mapPort(m object, k string) int {
	v, ok := m[k]
	if !ok {
		return 0
	}
	if v == nil || v == "" {
		return -1
	}
	return port(v)
}
func valueString(v any) string {
	switch n := v.(type) {
	case string:
		return n
	case json.Number:
		return string(n)
	case int:
		return strconv.Itoa(n)
	case float64:
		return strconv.FormatFloat(n, 'f', -1, 64)
	}
	return ""
}
func decodeJSON(data []byte) (any, error) {
	var v any
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if d.Decode(&v) != nil {
		return nil, ErrFormat
	}
	var rest any
	if d.Decode(&rest) != io.EOF {
		return nil, ErrFormat
	}
	return v, nil
}
func issue(e *Entry, code, field string) {
	e.Issues = append(e.Issues, Issue{Code: code, Name: e.Name, Field: field})
}
func target(s string) (string, string, int) {
	user := ""
	if i := strings.LastIndex(s, "@"); i >= 0 {
		user = s[:i]
		s = s[i+1:]
	}
	if h, p, err := net.SplitHostPort(s); err == nil {
		return h, user, port(p)
	}
	return strings.Trim(s, "[]"), user, 0
}
func keyPath(path, name string) string {
	if path != "" && !filepath.IsAbs(path) && !strings.HasPrefix(path, "~") && !(len(path) > 1 && path[1] == ':') {
		return filepath.Clean(filepath.Join(filepath.Dir(name), path))
	}
	return path
}
func secrets(e *Entry, m object, encrypted bool) {
	for _, f := range []string{"password", "privateKey", "passphrase"} {
		s := str(m, f)
		if s == "" {
			continue
		}
		if encrypted && f == "password" {
			issue(e, "encrypted-secret", f)
			continue
		}
		switch f {
		case "password":
			e.Password = []byte(s)
		case "privateKey":
			e.PrivateKey = []byte(s)
		case "passphrase":
			e.Passphrase = []byte(s)
		}
	}
}
func unsupported(e *Entry, m object, fields ...string) {
	for _, f := range fields {
		v := m[f]
		if v != nil && v != "" && v != false {
			if a, ok := v.([]any); ok && len(a) == 0 {
				continue
			}
			issue(e, "unsupported-field", f)
		}
	}
}
func parseWindTerm(data []byte, name string) (Result, error) {
	v, err := decodeJSON(data)
	if err != nil {
		return Result{}, err
	}
	rows, ok := v.([]any)
	if !ok {
		return Result{}, ErrFormat
	}
	r := Result{}
	for _, row := range rows {
		m := obj(row)
		if m == nil {
			return Result{}, ErrFormat
		}
		if _, ok := m["session.target"]; !ok {
			return Result{}, ErrFormat
		}
		e := Entry{ID: str(m, "session.uuid"), Name: str(m, "session.label"), Group: str(m, "session.folder")}
		if e.Group == "" {
			e.Group = windTermGroup(str(m, "session.group"))
		}
		protocol := str(m, "session.protocol")
		if protocol != "" && !strings.EqualFold(protocol, "SSH") {
			r.Issues = append(r.Issues, Issue{Code: "unsupported-protocol", Name: e.Name, Field: "protocol"})
			continue
		}
		e.Address, e.User, e.Port = target(str(m, "session.target"))
		if _, ok := m["session.port"]; ok {
			e.Port = mapPort(m, "session.port")
		}
		for k, v := range m {
			if v != nil && v != "" && (strings.Contains(strings.ToLower(k), "password") || strings.Contains(strings.ToLower(k), "private") || strings.Contains(strings.ToLower(k), "hidden")) {
				issue(&e, "encrypted-secret", k)
			}
		}
		r.Entries = append(r.Entries, e)
	}
	return r, nil
}

// windTermGroup turns WindTerm's "Parent>Child" (also written "Parent > Child")
// into the slash-separated path used for nested groups.
func windTermGroup(s string) string {
	var parts []string
	for _, p := range strings.Split(s, ">") {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "/")
}
func parseElecterm(data []byte, name string) (Result, error) {
	v, err := decodeJSON(data)
	if err != nil {
		return Result{}, err
	}
	root := obj(v)
	rows, ok := v.([]any)
	if root != nil {
		rows, ok = root["bookmarks"].([]any)
	}
	if !ok {
		return Result{}, ErrFormat
	}
	groups := map[string]object{}
	parent := map[string]string{}
	membership := map[string]string{}
	for _, g := range arr(root["bookmarkGroups"]) {
		m := obj(g)
		id := str(m, "id")
		if m == nil || id == "" || groups[id] != nil {
			return Result{}, ErrFormat
		}
		groups[id] = m
		for _, c := range arr(m["bookmarkGroupIds"]) {
			if s, ok := c.(string); ok {
				if old, exists := parent[s]; exists && old != id {
					return Result{}, ErrFormat
				}
				parent[s] = id
			}
		}
		for _, c := range arr(m["bookmarkIds"]) {
			if s, ok := c.(string); ok {
				if old, exists := membership[s]; exists && old != id {
					return Result{}, ErrFormat
				}
				membership[s] = id
			}
		}
	}
	r := Result{}
	for _, row := range rows {
		m := obj(row)
		if m == nil {
			return Result{}, ErrFormat
		}
		e := Entry{ID: str(m, "id"), Name: str(m, "title"), Address: str(m, "host"), User: str(m, "username"), Port: mapPort(m, "port")}
		if _, ok := m["host"]; !ok {
			return Result{}, ErrFormat
		}
		typ := str(m, "type")
		if typ != "" && typ != "ssh" && typ != "sftp" {
			r.Issues = append(r.Issues, Issue{Code: "unsupported-protocol", Name: e.Name, Field: "type"})
			continue
		}
		gid := membership[e.ID]
		seen := map[string]bool{}
		for gid != "" {
			if seen[gid] {
				issue(&e, "missing-reference", "group")
				break
			}
			seen[gid] = true
			g := groups[gid]
			if g == nil {
				issue(&e, "missing-reference", "group")
				break
			}
			e.Group = strings.Trim(str(g, "title")+"/"+e.Group, "/")
			gid = parent[gid]
		}
		encrypted, _ := m["passwordEncrypted"].(bool)
		secrets(&e, m, encrypted)
		e.KeyPath = keyPath(str(m, "privateKeyPath"), name)
		if str(m, "authType") == "password" {
			e.PrivateKey = nil
			e.KeyPath = ""
			e.Passphrase = nil
		}
		unsupported(&e, m, "proxy", "sshTunnels", "env", "loginScript", "profile", "connectionHoppings", "certificate", "certificatePath", "proxyCommand", "description", "runScripts", "setEnv")
		r.Entries = append(r.Entries, e)
	}
	return r, nil
}

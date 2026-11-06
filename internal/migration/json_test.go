package migration

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"testing"
)

func hasIssue(issues []Issue, code, field string) bool {
	for _, i := range issues {
		if i.Code == code && i.Field == field {
			return true
		}
	}
	return false
}
func TestMigrationJSON(t *testing.T) {
	tests := []struct {
		name                             string
		source                           Source
		data                             string
		wantName, wantGroup, wantAddress string
		wantPort                         int
	}{
		{"wind", WindTerm, `[{"session.target":"alice@[2001:db8::1]","session.label":"日本","session.folder":"工作/生产","session.port":2222,"session.protocol":"SSH"}]`, "日本", "工作/生产", "2001:db8::1", 2222},
		{"wind-nested", WindTerm, `[{"session.target":"host.example","session.label":"k3s","session.group":"工作>CMCC>k3s","session.protocol":"SSH"}]`, "k3s", "工作/CMCC/k3s", "host.example", 22},
		{"wind-spaced", WindTerm, `[{"session.target":"host.example","session.label":"web","session.group":" Imported > WindTerm >","session.protocol":"SSH"}]`, "web", "Imported/WindTerm", "host.example", 22},
		{"electerm", Electerm, `{"bookmarks":[{"id":"b","title":"server","host":"host.example","username":"alice","type":"ssh"}],"bookmarkGroups":[{"id":"a","title":"Parent","bookmarkGroupIds":["c"]},{"id":"c","title":"Child","bookmarkIds":["b"]}]}`, "server", "Parent/Child", "host.example", 22},
		{"netcatty", Netcatty, `{"hosts":[{"id":"h","label":"server","hostname":"host.example","username":"bob","group":"A/B"}]}`, "server", "A/B", "host.example", 22},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := parseData(tt.source, append([]byte{0xef, 0xbb, 0xbf}, []byte(tt.data)...), "sessions.json")
			if err != nil || len(r.Entries) != 1 {
				t.Fatalf("parse failed: %v", err)
			}
			e := r.Entries[0]
			validateEntry(&e)
			if e.Name != tt.wantName || e.Group != tt.wantGroup || e.Address != tt.wantAddress || e.Port != tt.wantPort {
				t.Fatal("metadata mismatch")
			}
		})
	}
	for _, s := range []Source{WindTerm, Electerm, Netcatty} {
		for _, data := range []string{`{}`, `{"hosts":`, `{} broken`, `{} {}`} {
			if _, err := parseData(s, []byte(data), "bad.json"); err != ErrFormat {
				t.Fatalf("%s accepted unknown/damaged schema", s)
			}
		}
	}
}
func TestMigrationCredentials(t *testing.T) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	secret := hex.EncodeToString(raw)
	m := object{"bookmarks": []any{object{"host": "example.test", "password": secret, "privateKey": secret, "passphrase": secret, "passwordEncrypted": true}}}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	r, err := parseElecterm(data, "export.json")
	if err != nil {
		t.Fatal(err)
	}
	e := r.Entries[0]
	if len(e.Password) != 0 || !bytes.Equal(e.PrivateKey, []byte(secret)) || !bytes.Equal(e.Passphrase, []byte(secret)) || !hasIssue(e.Issues, "encrypted-secret", "password") {
		t.Fatal("credential classification failed")
	}
	marshaled, err := json.Marshal(e)
	if err != nil || bytes.Contains(marshaled, []byte(secret)) {
		t.Fatal("credentials serialized")
	}
	ciphertext := "enc:v1:" + base64.StdEncoding.EncodeToString(append([]byte("v10"), make([]byte, 16)...))
	payload, _ := json.Marshal(object{"hosts": []any{object{"hostname": "example.test", "password": ciphertext}}})
	r, err = parseNetcatty(payload, "meta.json")
	if err != nil || len(r.Entries[0].Password) != 0 || !hasIssue(r.Entries[0].Issues, "encrypted-secret", "password") {
		t.Fatal("encrypted placeholder accepted")
	}
	for _, plain := range []string{"enc:human", "encrypted:human", "enc:v1:djEw", "enc:v1:placeholder"} {
		m := object{"password": plain}
		var direct Entry
		secrets(&direct, m, false)
		if string(direct.Password) != plain {
			t.Fatal("generic plain credential lost")
		}
		var nc Entry
		netcattySecrets(&nc, m)
		if string(nc.Password) != plain {
			t.Fatal("plain prefix misclassified")
		}
	}
	if _, err = parseNetcatty([]byte(`{"encrypted":true,"payloadData":"opaque"}`), "backup.json"); err != ErrFormat {
		t.Fatal("encrypted backup accepted")
	}
}
func TestMigrationTabby(t *testing.T) {
	data := []byte("profiles:\n  - id: app\n    type: ssh\n    name: App\n    group: child\n    options:\n      host: app.example\n      privateKeys: [./id_ed25519]\n      jumpHost: jump\nprofileDefaults:\n  ssh:\n    options:\n      port: 2200\n      user: global\ngroups:\n  - id: parent\n    name: Parent\n  - id: child\n    name: Child\n    parentGroupId: parent\n    defaults:\n      ssh:\n        options:\n          user: groupuser\n")
	r, err := parseTabby(data, "/exports/config.yaml")
	if err != nil || len(r.Entries) != 1 {
		t.Fatalf("failed: %v", err)
	}
	e := r.Entries[0]
	if e.User != "groupuser" || e.Port != 2200 || e.Group != "Parent/Child" || e.KeyPath != filepath.FromSlash("/exports/id_ed25519") || e.Jumps[0].Ref != "jump" {
		t.Fatal("profile defaults or group mismatch")
	}
	for _, v := range []string{"profiles: [", "other: []", "profiles: []\n---\nprofiles: []", "profiles: &a [*a]"} {
		if _, err := parseTabby([]byte(v), "bad.yaml"); err != ErrFormat {
			t.Fatal("malformed YAML accepted")
		}
	}
}
func TestMigrationNetcattyCSV(t *testing.T) {
	data := []byte("Groups,Label,Tags,Notes,Hostname/IP,Protocol,Port,Username,Password,KeyPath,Passphrase,Proxy,ProxyJump\nA/B,app,tag,note,[2001:db8::1],ssh,2222,alice,,__netcatty_csv_keypath_v1__:%40key,,,jump%40example\n")
	r, err := parseNetcatty(data, "/exports/hosts.csv")
	if err != nil || len(r.Entries) != 1 {
		t.Fatalf("failed: %v", err)
	}
	e := r.Entries[0]
	validateEntry(&e)
	if e.Address != "2001:db8::1" || e.Group != "A/B" || e.KeyPath != filepath.FromSlash("/exports/@key") || !hasIssue(e.Issues, "unsupported-field", "Tags") {
		t.Fatal("CSV metadata mismatch")
	}
	if _, err := parseNetcatty([]byte("Groups,Label\na"), "bad.csv"); err != ErrFormat {
		t.Fatal("bad CSV accepted")
	}
}

func TestMigrationExplicitDiagnostics(t *testing.T) {
	for _, value := range []any{nil, "", json.Number("0"), "invalid", json.Number("65536")} {
		e := Entry{Address: "host", Port: mapPort(object{"port": value}, "port")}
		validateEntry(&e)
		if !hasIssue(e.Issues, "invalid-entry", "port") {
			t.Fatal("explicit invalid port defaulted")
		}
	}
	r, err := parseElecterm([]byte(`{"bookmarks":[{"host":"internal","proxyCommand":"command","description":"metadata"}]}`), "bookmarks.json")
	if err != nil || !hasIssue(r.Entries[0].Issues, "unsupported-field", "proxyCommand") || !hasIssue(r.Entries[0].Issues, "unsupported-field", "description") {
		t.Fatal("unsupported routing field dropped")
	}
	r, err = parseTabby([]byte("profiles:\n  - type: ssh\n    name: agent\n    options:\n      host: host\n      auth: agent\n"), "config.yaml")
	if err != nil || hasIssue(r.Entries[0].Issues, "encrypted-secret", "password") || !hasIssue(r.Entries[0].Issues, "unsupported-field", "auth") {
		t.Fatal("agent auth misclassified")
	}
	r, err = parseNetcatty([]byte(`{"hosts":[{"hostname":"host","identityId":"user"}],"identities":[{"id":"user","username":"alice","keyId":"key"}],"keys":[{"id":"key","filePath":"./id_ed25519"}]}`), "/exports/meta.json")
	if err != nil || r.Entries[0].User != "alice" || r.Entries[0].KeyPath != filepath.FromSlash("/exports/id_ed25519") {
		t.Fatal("identity reference not resolved")
	}
	for _, source := range []Source{Electerm, Tabby} {
		data := []byte(`{"bookmarks":[{"host":"h"}],"bookmarkGroups":[{"id":"g"},{"id":"g"}]}`)
		if source == Tabby {
			data = []byte("profiles: []\ngroups: [{id: g}, {id: g}]")
		}
		if _, err := parseData(source, data, "bad"); err != ErrFormat {
			t.Fatal("duplicate group id accepted")
		}
	}
	r = Result{Entries: []Entry{{ID: "a", Jumps: []Hop{{Ref: "b"}}}, {ID: "b", Jumps: []Hop{{Ref: "c"}}}, {ID: "c", Jumps: []Hop{{Ref: "b"}}}}}
	resolveReferences(&r)
	if !hasIssue(r.Entries[0].Issues, "invalid-entry", "jump") {
		t.Fatal("downstream reference cycle missed")
	}
}

func TestMigrationNetcattyAuth(t *testing.T) {
	data := []byte(`{"hosts":[{"hostname":"internal","username":"alice","identityId":"i","identityFileId":"k","authMethod":"password","proxyConfig":{"type":"socks5","host":"proxy","port":1080}}],"identities":[{"id":"i","username":"","authMethod":"password"}],"keys":[{"id":"k","filePath":"unused"}]}`)
	r, err := parseNetcatty(data, "meta.json")
	if err != nil {
		t.Fatal(err)
	}
	e := r.Entries[0]
	if e.User != "alice" || e.KeyPath != "" || len(e.PrivateKey) > 0 || !hasIssue(e.Issues, "unsupported-field", "proxyConfig") {
		t.Fatal("selected password auth or proxy lost")
	}
	data = []byte(`{"hosts":[{"hostname":"internal","username":"alice","identityId":"i","identityFileId":"k"}],"identities":[{"id":"i","username":""}],"keys":[{"id":"k","filePath":"./id_ed25519"}]}`)
	r, err = parseNetcatty(data, "/exports/meta.json")
	if err != nil || r.Entries[0].User != "alice" || r.Entries[0].KeyPath != filepath.FromSlash("/exports/id_ed25519") {
		t.Fatal("empty identity erased host fallback")
	}
}

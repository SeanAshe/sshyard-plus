package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/keys"
	"github.com/Licoy/sshyard/internal/migration"
	"github.com/Licoy/sshyard/locales"
)

func TestMigrationTextFlow(t *testing.T) {
	a, tt := newTestApp(t)
	if _, err := a.store.Add(assets.Host{Name: "old", Address: "10.0.0.9", User: "root"}); err != nil {
		t.Fatal(err)
	}
	a.openSettings(prefMigration)
	tt.Frame()
	if !tt.HasText(en.MigrationText()) {
		t.Fatalf("no text import row: %q", tt.Texts())
	}
	mustClick(t, tt, en.MigrationTextInput())
	tt.Frame()
	f := a.migration
	if f == nil || !f.composing || !tt.HasText(en.MigrationTextHelp()) {
		t.Fatalf("composer not shown: %q", tt.Texts())
	}
	f.text = "host=192.168.31.22 port=22 user=root authType=password title=31.22\n" +
		"host=192.168.78.55 port=22 user=root authType=password password=11223344 title=test zone=cn\n" +
		"ip=10.0.0.9 username=root label=again\n"
	a.readTextMigration(en)
	f = a.migration
	if f.composing || f.text != "" || len(f.rows) != 3 || !f.saveSecrets {
		t.Fatalf("preview %+v", f)
	}
	r0, r1, r2 := f.rows[0], f.rows[1], f.rows[2]
	if r0.name != "31.22" || r0.auth != assets.AuthAsk || r0.password != "" || r1.name != "test" || r1.auth != assets.AuthPassword || r1.password != "11223344" || len(r1.entry.Password) != 0 {
		t.Fatalf("rows %+v %+v", r0, r1)
	}
	tt.Frame()
	for _, s := range []string{en.MigrationPreview(en.MigrationText()), en.MigrationBatchEdit(), en.MigrationUnsupportedField() + " · zone", en.MigrationConflict()} {
		if !tt.HasText(s) {
			t.Fatalf("preview lacks %q: %q", s, tt.Texts())
		}
	}
	if tt.HasText(en.MigrationSaveSecrets()) {
		t.Fatal("text import shows the credentials opt-in")
	}

	key, err := keys.Generate("batch", nil)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "id_batch")
	if err := os.WriteFile(keyPath, key.PEM, 0o600); err != nil {
		t.Fatal(err)
	}
	r2.selected = false
	b := &f.batch
	b.port = "http"
	a.applyMigrationBatch(en, f)
	if b.err != en.InvalidPort() || r0.port != "22" {
		t.Fatalf("bad batch port applied: %q %q", b.err, r0.port)
	}
	b.group, b.groupSet, b.user, b.port, b.auth, b.keyPath = "生产", true, "ops", "2222", assets.AuthKey, keyPath
	a.applyMigrationBatch(en, f)
	for _, r := range []*migrationRow{r0, r1} {
		if r.group != "生产" || r.entry.User != "ops" || r.port != "2222" || r.entry.Port != 2222 || r.auth != assets.AuthKey || r.entry.KeyPath != keyPath {
			t.Fatalf("batch not applied: %+v", r)
		}
	}
	if r2.group != "" || r2.entry.User != "root" || r2.entry.Port != 22 {
		t.Fatalf("batch changed an unselected row: %+v", r2)
	}
	if groups := a.migrationGroups(f); len(groups) != 1 || groups[0] != "生产" {
		t.Fatalf("groups %v", groups)
	}

	r1.auth = assets.AuthPassword
	r0.entry.Address = ""
	if k := a.checkMigration(en, f); k.blocking != 1 || k.problems[r0] != en.RequiredField(en.FieldHost()) || k.fields[r0] != "address" {
		t.Fatalf("missing host not reported: %+v", k)
	}
	r0.entry.Address = "192.168.31.22:22"
	if k := a.checkMigration(en, f); k.problems[r0] != en.MigrationInvalidHost() || k.fields[r0] != "address" {
		t.Fatalf("host with port not reported: %+v", k)
	}
	tt.Frame()
	if !tt.HasText(en.MigrationFixRows("1")) {
		t.Fatalf("no problem summary: %q", tt.Texts())
	}
	a.startMigrationSave(en)
	if f.saving || a.store.Len() != 1 {
		t.Fatal("import started with a wrong row")
	}
	r0.entry.Address = "192.168.31.22"
	a.startMigrationSave(en)
	waitForIn(t, tt, slowWait, "text import", func() bool { return f.done != nil })
	if *f.done != (migrationOutcome{added: 2, skipped: 1}) {
		t.Fatalf("outcome %+v", *f.done)
	}
	if r1.password != "" {
		t.Fatal("password kept after the import")
	}
	tt.Frame()
	if !tt.HasText(en.MigrationResultTitle()) || !tt.HasText(en.MigrationResult("2", "0", "1")) {
		t.Fatalf("no outcome: %q", tt.Texts())
	}
	saved := map[string]assets.Host{}
	for _, h := range a.store.List() {
		saved[h.Name] = h
	}
	keyed, pw := saved["31.22"], saved["test"]
	if keyed.Auth != assets.AuthKey || keyed.User != "ops" || keyed.Port != 2222 || keyed.Group != "生产" || pw.Auth != assets.AuthPassword || pw.Group != "生产" {
		t.Fatalf("saved %+v %+v", keyed, pw)
	}
	if data, err := a.kc.Get(keychain.Host(keyed.ID, keychain.PrivateKey)); err != nil || !strings.Contains(string(data), "PRIVATE KEY") {
		t.Fatalf("key not saved: %v", err)
	}
	if secret, err := a.kc.Get(keychain.Host(pw.ID, keychain.Password)); err != nil || string(secret) != "11223344" {
		t.Fatalf("password not saved: %v", err)
	}
	a.closeMigration()
	if a.migration != nil || a.migrationOpen {
		t.Fatal("outcome did not close")
	}
}

func TestMigrationTextFile(t *testing.T) {
	a, tt := newTestApp(t)
	path := filepath.Join(t.TempDir(), "hosts.txt")
	if err := os.WriteFile(path, []byte("host=file.example user=ops title=from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.p.PickFiles = func(title string) ([]string, error) {
		if title != en.MigrationTextChooseFile() {
			t.Errorf("dialog title %q", title)
		}
		return []string{path}, nil
	}
	a.openSettings(prefMigration)
	tt.Frame()
	mustClick(t, tt, en.MigrationTextChooseFile())
	waitFor(t, tt, "text file preview", func() bool { return a.migration != nil && !a.migration.reading && len(a.migration.rows) == 1 })
	if r := a.migration.rows[0]; !a.migration.editable() || r.name != "from-file" || r.entry.User != "ops" || r.auth != assets.AuthAsk {
		t.Fatalf("row %+v", r)
	}
}

func TestMigrationCSVFile(t *testing.T) {
	a, tt := newTestApp(t)
	path := filepath.Join(t.TempDir(), "hosts.csv")
	if err := os.WriteFile(path, []byte("host,user,password,title\ncsv.example,ops,s3cret,from-csv\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.p.PickFiles = func(title string) ([]string, error) {
		if title != en.MigrationCSVChooseFile() {
			t.Errorf("dialog title %q", title)
		}
		return []string{path}, nil
	}
	a.openSettings(prefMigration)
	tt.Frame()
	mustClick(t, tt, en.MigrationCSVChooseFile())
	waitFor(t, tt, "csv file preview", func() bool { return a.migration != nil && !a.migration.reading && len(a.migration.rows) == 1 })
	f := a.migration
	if r := f.rows[0]; !f.editable() || !f.saveSecrets || r.name != "from-csv" || r.entry.User != "ops" || r.auth != assets.AuthPassword || r.password != "s3cret" || len(r.entry.Password) != 0 {
		t.Fatalf("row %+v", r)
	}
}

func TestMigrationTextOverwriteLogin(t *testing.T) {
	a, _ := newTestApp(t)
	pw, err := a.store.Add(assets.Host{Name: "pw", Address: "pw.example", User: "root", Auth: assets.AuthPassword})
	if err != nil {
		t.Fatal(err)
	}
	keyed, err := a.store.Add(assets.Host{Name: "keyed", Address: "key.example", User: "root", Auth: assets.AuthKey})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.kc.Set(keychain.Host(pw.ID, keychain.Password), []byte("kept")); err != nil {
		t.Fatal(err)
	}
	if err := a.kc.Set(keychain.Host(keyed.ID, keychain.PrivateKey), []byte("old key")); err != nil {
		t.Fatal(err)
	}
	result, err := migration.ParseText("host=pw.example user=root title=renamed\nhost=key.example user=root title=asked\n")
	if err != nil {
		t.Fatal(err)
	}
	f := a.newMigration(migration.Text, result)
	r0, r1 := f.rows[0], f.rows[1]
	if r0.auth != assets.AuthPassword || r1.auth != assets.AuthKey {
		t.Fatalf("overwritten logins not kept: %q %q", r0.auth, r1.auth)
	}
	r0.action, r1.action = migrationKeepBoth, migrationOverwrite
	if k := a.checkMigration(en, f); k.blocking != 1 || k.problems[r0] != en.CredPasswordRequired() {
		t.Fatalf("new host without its password: %+v", k)
	}
	r0.action = migrationOverwrite
	if k := a.checkMigration(en, f); k.blocking != 0 || f.needsSecrets() {
		t.Fatalf("kept logins %+v ask for secrets", k)
	}
	r1.auth = assets.AuthAsk
	if k := a.checkMigration(en, f); k.blocking != 0 || !f.needsSecrets() {
		t.Fatalf("overwrite check %+v", k)
	}
	if n, err := a.saveMigration(en, f); err != nil || n != 2 {
		t.Fatalf("save %d %v", n, err)
	}
	got, _ := a.store.Get(pw.ID)
	if secret, err := a.kc.Get(keychain.Host(pw.ID, keychain.Password)); got.Name != "renamed" || got.Auth != assets.AuthPassword || err != nil || string(secret) != "kept" {
		t.Fatalf("blank password did not keep the saved one: %+v %v", got, err)
	}
	got, _ = a.store.Get(keyed.ID)
	if exists, err := a.kc.Exists(keychain.Host(keyed.ID, keychain.PrivateKey)); got.Auth != assets.AuthAsk || err != nil || exists {
		t.Fatalf("asking login kept the old key: %+v %v", got, err)
	}
}

func TestMigrationTextKeyProblem(t *testing.T) {
	a, tt := newTestApp(t)
	broken := filepath.Join(t.TempDir(), "id_broken")
	if err := os.WriteFile(broken, []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := migration.ParseText("host=k.example user=root title=k\n")
	if err != nil {
		t.Fatal(err)
	}
	f := a.newMigration(migration.Text, result)
	a.migration, a.migrationOpen = f, true
	r := f.rows[0]
	r.auth = assets.AuthKey
	if k := a.checkMigration(en, f); k.problems[r] != en.MigrationRelocateKey() {
		t.Fatalf("missing key not reported: %+v", k)
	}
	r.entry.KeyPath = broken
	a.startMigrationSave(en)
	waitFor(t, tt, "refused key", func() bool { return !f.saving })
	if r.problem == "" || f.done != nil || f.err != "" || a.store.Len() != 0 {
		t.Fatalf("broken key %q %q %d", r.problem, f.err, a.store.Len())
	}
	tt.Frame()
	if !tt.HasText(en.MigrationFixRows("1")) || !tt.HasText(r.problem) {
		t.Fatalf("key problem not shown: %q", tt.Texts())
	}
	if k := a.checkMigration(en, f); k.blocking != 0 {
		t.Fatal("a refused key blocks a retry")
	}
}

func TestMigrationTextRows(t *testing.T) {
	a, _ := newTestApp(t)
	var lines []string
	for i := range 24 {
		switch i % 3 {
		case 0:
			lines = append(lines, fmt.Sprintf("host=10.0.0.%d user=root password=p%d title=\"web %d\"", i, i, i))
		case 1:
			lines = append(lines, fmt.Sprintf("host=10.0.0.%d user=root title=\"db %d - Aliyun\" groupId=6a8e8bc5", i, i))
		default:
			lines = append(lines, fmt.Sprintf("host=10.0.0.%d port=http user=ops title=\"edge %d\"", i, i))
		}
	}
	parse := func() migration.Result {
		result, err := migration.ParseText(strings.Join(lines, "\n"))
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	// A preview takes the parsed issues without rewriting them (its rows
	// take the passwords, wiping them in the result).
	result := parse()
	a.newMigration(migration.Text, result)
	if issues := result.Entries[2].Issues; len(issues) != 1 || issues[0].Code != "invalid-entry" || issues[0].Field != "port" {
		t.Fatalf("parsed issues %v", issues)
	}
	f := a.newMigration(migration.Text, parse())
	f.batch.open, f.batch.auth = true, assets.AuthKey
	f.rows[1].auth, f.rows[4].auth = assets.AuthPassword, assets.AuthKey
	k := a.checkMigration(locales.In("en"), f)
	if k.fields[f.rows[1]] != "secret" || k.fields[f.rows[2]] != "port" || k.fields[f.rows[4]] != "secret" || k.problems[f.rows[0]] != "" {
		t.Fatalf("fields to correct %v", k.fields)
	}
}

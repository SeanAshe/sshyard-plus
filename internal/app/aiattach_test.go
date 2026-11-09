package app

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Licoy/sshyard/internal/ai/agent"
	"github.com/Licoy/sshyard/internal/ai/llm"
	"github.com/egoist/mygo/transfer"
)

// partsModel answers at once, keeping the last question it was asked.
type partsModel struct {
	mu   sync.Mutex
	last llm.Message
}

func (m *partsModel) Stream(_ context.Context, req llm.Request, _ func(llm.Event)) (llm.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, msg := range req.Messages {
		if msg.Role == "user" {
			m.last = msg
		}
	}
	return llm.Message{Role: "assistant", Content: "Seen."}, nil
}

func (m *partsModel) question() llm.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.last
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// TestAttachKinds checks that a file attaches by what it holds: images
// and PDFs as they are, text as text, anything else not at all.
func TestAttachKinds(t *testing.T) {
	img, err := attachData("shot.png", pngBytes(t))
	if err != nil || img.mime != "image/png" || img.bmp == nil {
		t.Fatalf("image %+v %v", img, err)
	}
	pdf, err := attachData("doc.pdf", []byte("%PDF-1.7\n..."))
	if err != nil || pdf.mime != "application/pdf" {
		t.Fatalf("pdf %+v %v", pdf, err)
	}
	txt, err := attachData("main.go", []byte("package main\n"))
	if err != nil || !txt.text() {
		t.Fatalf("text %+v %v", txt, err)
	}
	for name, data := range map[string][]byte{
		"blob.bin":  {0x7f, 'E', 'L', 'F', 0, 0, 1},
		"empty.txt": nil,
		"huge.log":  bytes.Repeat([]byte("a"), maxAttachText+1),
	} {
		if f, err := attachData(name, data); err == nil {
			t.Fatalf("%s attached as %+v", name, f)
		}
	}
	dir := t.TempDir()
	if _, err := readAttach(dir); err == nil {
		t.Fatal("a directory attached")
	}
}

// TestAIPaneAttachments checks that files picked or dropped are attached,
// that one can be taken off, and that the rest go with the question:
// images as parts, text inside it.
func TestAIPaneAttachments(t *testing.T) {
	a, tt := newTestApp(t)
	h := savedHost(t, a, "web", "10.0.0.8")
	consentHost(t, a, h.ID)
	model := &partsModel{}
	a.newModel = func(*conn) agent.Streamer { return model }
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	shot := write("shot.png", pngBytes(t))
	notes := write("notes.txt", []byte("disk is full"))
	extra := write("extra.md", []byte("# extra"))
	blob := write("blob.bin", []byte{0, 1, 2, 3})
	a.p.PickFiles = func(string) ([]string, error) { return []string{shot, notes, extra, blob}, nil }
	id, err := a.OpenAI(h.ID, openAt{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	cn := a.conns[id]
	tt.Frame()
	mustClick(t, tt, "Attach Files")
	waitFor(t, tt, "attachments", func() bool {
		return len(cn.ai.attach) == 3 && tt.HasText("“blob.bin” can’t be attached: only images, PDFs and text files can.")
	})
	names := func() []string {
		var out []string
		for _, f := range cn.ai.attach {
			out = append(out, f.name)
		}
		return out
	}
	if got := names(); !slices.Equal(got, []string{"shot.png", "notes.txt", "extra.md"}) {
		t.Fatalf("attached %q", got)
	}
	mustClick(t, tt, "Remove extra.md")
	tt.Frame()
	if got := names(); !slices.Equal(got, []string{"shot.png", "notes.txt"}) {
		t.Fatalf("after removing extra.md: %q", got)
	}
	mustClick(t, tt, "Preview shot.png")
	tt.Frame()
	if a.attachmentPreview == nil {
		t.Fatal("composer image did not open the top-level preview")
	}
	mustClick(t, tt, "Close Preview")
	tt.Frame()
	if a.attachmentPreview != nil {
		t.Fatal("composer image preview did not close")
	}
	mustClick(t, tt, "Remove shot.png")
	tt.Frame()
	if got := names(); !slices.Equal(got, []string{"notes.txt"}) {
		t.Fatalf("after removing shot.png: %q", got)
	}
	imageAgain, err := readAttach(shot)
	if err != nil {
		t.Fatal(err)
	}
	a.addAttach(cn, imageAgain)
	tt.Frame()
	mustClick(t, tt, "Send")
	waitFor(t, tt, "reply", func() bool { return !cn.ai.running && paneHas(a, id, "Seen.") })
	q := model.question()
	if len(q.Parts) != 1 || q.Parts[0].Name != "shot.png" || q.Parts[0].MIME != "image/png" {
		t.Fatalf("parts %+v", q.Parts)
	}
	if !strings.Contains(q.Content, `<untrusted-data source="file notes.txt">`) || !strings.Contains(q.Content, "disk is full") {
		t.Fatalf("question %q", q.Content)
	}
	if len(cn.ai.attach) != 0 {
		t.Fatalf("after send: attach %q", names())
	}

	mustClick(t, tt, "Preview shot.png")
	tt.Frame()
	if a.attachmentPreview == nil {
		t.Fatal("sent image did not open the top-level preview")
	}
	mustClick(t, tt, "Close Preview")
	tt.Frame()
	if a.attachmentPreview != nil {
		t.Fatal("sent image preview did not close")
	}

	in := mustFind(t, tt, "Ask about this host")
	if !tt.DropFiles(in.X+in.W/2, in.Y+in.H/2, notes) {
		t.Fatal("the composer took no drop")
	}
	waitFor(t, tt, "dropped", func() bool { return slices.Equal(names(), []string{"notes.txt"}) })

	cn.ai.agent = "acp"
	img, err := readAttach(shot)
	if err != nil {
		t.Fatal(err)
	}
	a.addAttach(cn, img)
	tt.Frame()
	if len(cn.ai.attach) != 1 || !tt.HasText("“shot.png” can’t be attached: this agent reads text files only.") {
		t.Fatalf("ACP took an image: %d %q", len(cn.ai.attach), tt.Texts())
	}
}

// TestAIPasteAttachments checks what a paste into the question attaches:
// files copied in a file manager, long text and an image copied without
// text, while shorter text, with or without an image beside it, is left
// to the question.
func TestAIPasteAttachments(t *testing.T) {
	a, tt := newTestApp(t)
	h := savedHost(t, a, "web", "10.0.0.8")
	id, err := a.OpenAI(h.ID, openAt{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	cn := a.conns[id]
	tt.Frame()
	notes := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(notes, []byte("disk is full"), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := transfer.FileData(notes)
	if err != nil {
		t.Fatal(err)
	}
	tt.SetClipboardData(files)
	if !a.pasteAttach(cn, a.svc, notes) {
		t.Fatal("pasted files not taken")
	}
	waitFor(t, tt, "pasted file", func() bool { return len(cn.ai.attach) == 1 && cn.ai.attach[0].name == "notes.txt" })

	shot := pngBytes(t)
	tt.SetClipboardData(transfer.New(transfer.NewItem(transfer.Bytes(transfer.PNG, shot))))
	if !a.pasteAttach(cn, a.svc, "") {
		t.Fatal("pasted image not taken")
	}
	waitFor(t, tt, "pasted image", func() bool { return len(cn.ai.attach) == 2 && cn.ai.attach[1].mime == "image/png" })
	if f := cn.ai.attach[1]; !bytes.Equal(f.data, shot) || !strings.HasSuffix(f.name, ".png") {
		t.Fatalf("pasted image %q, %d bytes", f.name, len(f.data))
	}

	long := strings.Repeat("kernel: eth0 link up\n", pasteLines+1)
	tt.SetClipboardData(transfer.TextData(long))
	if !a.pasteAttach(cn, a.svc, long) {
		t.Fatal("long text not taken")
	}
	if f := cn.ai.attach[len(cn.ai.attach)-1]; len(cn.ai.attach) != 3 || !f.text() || string(f.data) != long || !strings.HasSuffix(f.name, ".txt") {
		t.Fatalf("long text attached as %q, %d attachments", f.name, len(cn.ai.attach))
	}
	wide := strings.Repeat("磁", pasteRunes+1)
	if !a.pasteAttach(cn, a.svc, wide) || len(cn.ai.attach) != 4 {
		t.Fatal("text of many characters not attached")
	}
	cn.ai.attach = cn.ai.attach[:2]
	short := strings.Repeat("line\n", pasteLines)
	if a.pasteAttach(cn, a.svc, short) {
		t.Fatal("text of pasteLines lines taken as an attachment")
	}

	tt.SetClipboardData(transfer.TextData("df -h"))
	if a.pasteAttach(cn, a.svc, "df -h") {
		t.Fatal("pasted text taken as an attachment")
	}
	tt.SetClipboardData(transfer.New(transfer.NewItem(transfer.Bytes(transfer.Text, []byte("table")), transfer.Bytes(transfer.PNG, shot))))
	if a.pasteAttach(cn, a.svc, "table") {
		t.Fatal("text with an image beside it taken as an attachment")
	}
	tt.Frame()
	if len(cn.ai.attach) != 2 {
		t.Fatalf("attachments after text pastes: %d", len(cn.ai.attach))
	}
}

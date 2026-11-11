package keys

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// The keys here are made when the tests run: no key, encrypted or not,
// is kept in the repository.

func putMP(b *bytes.Buffer, n *big.Int) {
	v := n.Bytes()
	if len(v) > 0 && v[0]&0x80 != 0 {
		v = append([]byte{0}, v...)
	}
	putString(b, v)
}

// privateBlob is PuTTY's private part of a key.
func privateBlob(t *testing.T, priv any) []byte {
	var b bytes.Buffer
	switch k := priv.(type) {
	case ed25519.PrivateKey:
		putString(&b, k.Seed())
	case *rsa.PrivateKey:
		putMP(&b, k.D)
		putMP(&b, k.Primes[0])
		putMP(&b, k.Primes[1])
		putMP(&b, k.Precomputed.Qinv)
	case *ecdsa.PrivateKey:
		d, err := k.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		putMP(&b, new(big.Int).SetBytes(d))
	default:
		t.Fatalf("no blob for %T", priv)
	}
	return b.Bytes()
}

func lines(b []byte) (int, string) {
	s := base64.StdEncoding.EncodeToString(b)
	var out []string
	for len(s) > 64 {
		out, s = append(out, s[:64]), s[64:]
	}
	out = append(out, s)
	return len(out), strings.Join(out, "\n")
}

// writePPK writes a PuTTY key file as PuTTYgen does; kdf is the v3 key
// derivation ("" for Argon2id).
func writePPK(t *testing.T, priv any, version int, passphrase, kdf string) []byte {
	t.Helper()
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	if kdf == "" {
		kdf = "Argon2id"
	}
	k := &ppk{version: version, alg: signer.PublicKey().Type(), encryption: "none", comment: "test key", public: signer.PublicKey().Marshal(), header: map[string]string{}}
	private := privateBlob(t, priv)
	if passphrase != "" {
		k.encryption = "aes256-cbc"
		if pad := len(private) % aes.BlockSize; pad != 0 {
			private = append(private, make([]byte, aes.BlockSize-pad)...)
		}
		if version == 3 {
			k.header = map[string]string{"Key-Derivation": kdf, "Argon2-Memory": testArgon.mem, "Argon2-Passes": testArgon.passes, "Argon2-Parallelism": "1", "Argon2-Salt": "0123456789abcdef0123456789abcdef"}
		}
	}
	var kdfLines string
	if version == 3 && passphrase != "" {
		kdfLines = fmt.Sprintf("Key-Derivation: %s\nArgon2-Memory: %s\nArgon2-Passes: %s\nArgon2-Parallelism: 1\nArgon2-Salt: 0123456789abcdef0123456789abcdef\n", kdf, testArgon.mem, testArgon.passes)
	}
	var mac []byte
	enc := private
	if kdf == "Argon2d" {
		mac = make([]byte, 32)
	} else {
		ck, iv, mk, err := k.keys([]byte(passphrase), true)
		if err != nil {
			t.Fatal(err)
		}
		mac = k.macOf(mk, private)
		if passphrase != "" {
			blk, _ := aes.NewCipher(ck)
			enc = bytes.Clone(private)
			cipher.NewCBCEncrypter(blk, iv).CryptBlocks(enc, enc)
		}
	}
	pn, pub := lines(k.public)
	sn, sec := lines(enc)
	return fmt.Appendf(nil, "PuTTY-User-Key-File-%d: %s\r\nEncryption: %s\r\nComment: %s\r\nPublic-Lines: %d\r\n%s\r\n%sPrivate-Lines: %d\r\n%s\r\nPrivate-MAC: %s\r\n",
		version, k.alg, k.encryption, k.comment, pn, strings.ReplaceAll(pub, "\n", "\r\n"), strings.ReplaceAll(kdfLines, "\n", "\r\n"), sn, strings.ReplaceAll(sec, "\n", "\r\n"), hex.EncodeToString(mac))
}

func testKeys(t *testing.T) map[string]any {
	_, ed, _ := ed25519.GenerateKey(rand.Reader)
	rk, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]any{"ed25519": ed, "rsa": rk}
	for _, c := range []elliptic.Curve{elliptic.P256(), elliptic.P384(), elliptic.P521()} {
		ek, _ := ecdsa.GenerateKey(c, rand.Reader)
		out[c.Params().Name] = ek
	}
	return out
}

// opens checks that k is an OpenSSH key of priv's public half that
// passphrase opens.
func opens(t *testing.T, k Key, priv any, passphrase string) {
	t.Helper()
	var s ssh.Signer
	var err error
	if passphrase == "" {
		s, err = ssh.ParsePrivateKey(k.PEM)
	} else {
		if _, e := ssh.ParsePrivateKey(k.PEM); e == nil {
			t.Fatal("the converted key is not encrypted")
		}
		s, err = ssh.ParsePrivateKeyWithPassphrase(k.PEM, []byte(passphrase))
	}
	if err != nil {
		t.Fatal(err)
	}
	want, _ := ssh.NewSignerFromKey(priv)
	if !bytes.Equal(s.PublicKey().Marshal(), want.PublicKey().Marshal()) || k.Fingerprint() != ssh.FingerprintSHA256(want.PublicKey()) {
		t.Fatal("the converted key is another key")
	}
	if !bytes.Contains(k.PEM, []byte("BEGIN OPENSSH PRIVATE KEY")) {
		t.Fatalf("not OpenSSH: %s", k.PEM[:40])
	}
}

func TestPPK(t *testing.T) {
	for name, priv := range testKeys(t) {
		for _, v := range []int{2, 3} {
			for _, pass := range []string{"", "correct horse"} {
				t.Run(fmt.Sprintf("%s/v%d/enc=%v", name, v, pass != ""), func(t *testing.T) {
					data := writePPK(t, priv, v, pass, "")
					if !IsPPK(data) {
						t.Fatal("not seen as PPK")
					}
					k, err := Import(data, []byte(pass))
					if err != nil {
						t.Fatal(err)
					}
					if k.Encrypted != (pass != "") || k.Comment != "test key" {
						t.Fatalf("encrypted %v comment %q", k.Encrypted, k.Comment)
					}
					opens(t, k, priv, pass)
					if pass == "" {
						return
					}
					if k, err := Import(data, nil); !errors.Is(err, ErrPassphraseNeeded) || k.Fingerprint() == "" {
						t.Fatalf("no passphrase: %v, fingerprint %q", err, k.Fingerprint())
					}
					if _, err := Import(data, []byte("wrong")); !errors.Is(err, ErrPassphraseWrong) {
						t.Fatalf("wrong passphrase: %v", err)
					}
				})
			}
		}
	}
}

func TestPPKDerivations(t *testing.T) {
	_, ed, _ := ed25519.GenerateKey(rand.Reader)
	k, err := Import(writePPK(t, ed, 3, "pw", "Argon2i"), []byte("pw"))
	if err != nil {
		t.Fatal(err)
	}
	opens(t, k, ed, "pw")
	var ue *UnsupportedError
	if _, err := Import(writePPK(t, ed, 3, "pw", "Argon2d"), []byte("pw")); !errors.As(err, &ue) || !strings.Contains(err.Error(), "Argon2d") {
		t.Fatalf("Argon2d: %v", err)
	}
}

func TestPPKDamaged(t *testing.T) {
	_, ed, _ := ed25519.GenerateKey(rand.Reader)
	data := writePPK(t, ed, 3, "", "")
	i := bytes.Index(data, []byte("Private-MAC: ")) + len("Private-MAC: ")
	bad := bytes.Clone(data)
	// Another hex digit, so the MAC still parses but no longer matches.
	if bad[i] == '0' {
		bad[i] = '1'
	} else {
		bad[i] = '0'
	}
	if _, err := Import(bad, nil); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("damaged MAC: %v", err)
	}
	old := bytes.Replace(data, []byte("PuTTY-User-Key-File-3"), []byte("PuTTY-User-Key-File-1"), 1)
	var ue *UnsupportedError
	if _, err := Import(old, nil); !errors.As(err, &ue) {
		t.Fatalf("v1: %v", err)
	}
	if _, err := Import(data[:len(data)/2], nil); !errors.Is(err, ErrNotAKey) {
		t.Fatalf("truncated: %v", err)
	}
}

// A line count from the file must never reach a slice bound
// unchecked: near math.MaxInt, i+count wraps negative and the parser
// sliced past the end (issue #49).
func TestPPKLineCounts(t *testing.T) {
	_, ed, _ := ed25519.GenerateKey(rand.Reader)
	for _, pass := range []string{"", "pw"} {
		data := writePPK(t, ed, 3, pass, "")
		lines := strings.Count(string(data), "\n") + 1
		for _, field := range []string{"Public-Lines", "Private-Lines"} {
			re := regexp.MustCompile(field + `: \d+\r\n`)
			if !re.Match(data) {
				t.Fatalf("no %s in the test key", field)
			}
			for _, n := range []string{
				strconv.Itoa(math.MaxInt),
				strconv.Itoa(math.MaxInt - 1),
				strconv.Itoa(math.MaxInt - lines),
				strconv.Itoa(math.MinInt),
				"-1",
				strconv.Itoa(lines),    // more than the lines left
				"9223372036854775808",  // past int64
				"18446744073709551616", // past uint64
				"0x10",                 // not decimal
			} {
				t.Run(fmt.Sprintf("enc=%v/%s=%s", pass != "", field, n), func(t *testing.T) {
					bad := re.ReplaceAll(data, []byte(field+": "+n+"\r\n"))
					if _, err := parsePPK(bad); !errors.Is(err, ErrNotAKey) {
						t.Fatalf("parse: %v", err)
					}
					if _, err := Import(bad, nil); !errors.Is(err, ErrNotAKey) {
						t.Fatalf("import preview: %v", err)
					}
				})
			}
		}
	}
	// A count that is exactly the lines left, with nothing after it, is
	// in bounds; the file then lacks the rest and is no key.
	tail := []byte("PuTTY-User-Key-File-3: ssh-ed25519\nPublic-Lines: 1\nAAAA")
	if _, err := parsePPK(tail); !errors.Is(err, ErrNotAKey) {
		t.Fatalf("count at the end: %v", err)
	}
}

func TestReaderStrBounds(t *testing.T) {
	for _, b := range [][]byte{{0xff, 0xff, 0xff, 0xff, 1, 2}, {0xff, 0xff, 0xff, 0xfd}, {0, 0, 0, 3, 1, 2}, {0, 0}} {
		r := &reader{b: b}
		if s := r.str(); !r.err || s != nil {
			t.Fatalf("%x: read %x", b, s)
		}
	}
	r := &reader{b: []byte{0, 0, 0, 2, 'h', 'i', 0, 0, 0, 0}}
	if s := r.str(); r.err || string(s) != "hi" {
		t.Fatalf("read %q", s)
	}
	if s := r.str(); r.err || len(s) != 0 || len(r.b) != 0 {
		t.Fatalf("empty string: %q, left %x", s, r.b)
	}
}

func TestOpenSSH(t *testing.T) {
	_, ed, _ := ed25519.GenerateKey(rand.Reader)
	plain, _ := ssh.MarshalPrivateKey(ed, "c")
	k, err := Import(pem.EncodeToMemory(plain), nil)
	if err != nil || k.Encrypted {
		t.Fatal(err, k.Encrypted)
	}
	opens(t, k, ed, "")
	enc, _ := ssh.MarshalPrivateKeyWithPassphrase(ed, "c", []byte("pw"))
	data := pem.EncodeToMemory(enc)
	k, err = Import(data, nil)
	if !errors.Is(err, ErrPassphraseNeeded) || !k.Encrypted || k.Fingerprint() == "" || !bytes.Equal(bytes.TrimSpace(k.PEM), bytes.TrimSpace(data)) {
		t.Fatalf("encrypted, no passphrase: %v %v %q", err, k.Encrypted, k.Fingerprint())
	}
	if _, err := Import(data, []byte("nope")); !errors.Is(err, ErrPassphraseWrong) {
		t.Fatalf("wrong passphrase: %v", err)
	}
	k, err = Import(data, []byte("pw"))
	if err != nil || !k.Encrypted {
		t.Fatal(err)
	}
	opens(t, k, ed, "pw")
}

func TestLegacyPEM(t *testing.T) {
	rk, _ := rsa.GenerateKey(rand.Reader, 2048)
	blk := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(rk)}
	k, err := Import(pem.EncodeToMemory(blk), nil)
	if err != nil || k.Type() != ssh.KeyAlgoRSA {
		t.Fatal(err, k.Type())
	}
	//lint:ignore SA1019 legacy encrypted PEM is what old tools write
	eb, err := x509.EncryptPEMBlock(rand.Reader, blk.Type, blk.Bytes, []byte("pw"), x509.PEMCipherAES256) //nolint:staticcheck
	if err != nil {
		t.Fatal(err)
	}
	data := pem.EncodeToMemory(eb)
	if k, err := Import(data, nil); !errors.Is(err, ErrPassphraseNeeded) || k.Public != nil {
		t.Fatalf("legacy, no passphrase: %v", err)
	}
	if _, err := Import(data, []byte("bad")); !errors.Is(err, ErrPassphraseWrong) {
		t.Fatalf("legacy, wrong passphrase: %v", err)
	}
	if k, err := Import(data, []byte("pw")); err != nil || !k.Encrypted || k.Type() != ssh.KeyAlgoRSA {
		t.Fatalf("legacy: %v", err)
	}
}

func TestNotKeys(t *testing.T) {
	_, ed, _ := ed25519.GenerateKey(rand.Reader)
	pub, _ := ssh.NewSignerFromKey(ed)
	if _, err := Import(ssh.MarshalAuthorizedKey(pub.PublicKey()), nil); !errors.Is(err, ErrPublicKey) {
		t.Fatalf("public key: %v", err)
	}
	for _, s := range []string{"", "   \n", "hello", "-----BEGIN NOTHING-----\nAAAA\n-----END NOTHING-----\n"} {
		if _, err := Import([]byte(s), nil); !errors.Is(err, ErrNotAKey) {
			t.Errorf("%q: %v", s, err)
		}
	}
}

func TestGenerate(t *testing.T) {
	for _, pass := range []string{"", "pw"} {
		k, err := Generate("me@box", []byte(pass))
		if err != nil {
			t.Fatal(err)
		}
		if k.Type() != ssh.KeyAlgoED25519 || k.Encrypted != (pass != "") {
			t.Fatal(k.Type(), k.Encrypted)
		}
		line := k.AuthorizedLine()
		if !strings.HasPrefix(line, "ssh-ed25519 AAAA") || !strings.HasSuffix(line, " me@box") {
			t.Fatal(line)
		}
		again, err := Import(k.PEM, []byte(pass))
		if err != nil || again.Fingerprint() != k.Fingerprint() {
			t.Fatal(err)
		}
	}
}

// TestPuTTYgen reads keys PuTTYgen itself wrote, when it is installed.
func TestPuTTYgen(t *testing.T) {
	bin, err := exec.LookPath("puttygen")
	if err != nil {
		t.Skip("puttygen is not installed")
	}
	dir := t.TempDir()
	pw := filepath.Join(dir, "pw")
	os.WriteFile(pw, []byte("s3cret pass"), 0o600)
	for _, c := range []struct{ typ, bits, version, pass string }{
		{"ed25519", "", "3", pw}, {"ed25519", "", "3", ""}, {"rsa", "2048", "3", pw},
		{"ecdsa", "384", "3", pw}, {"rsa", "2048", "2", pw}, {"ed25519", "", "2", ""}, {"ecdsa", "256", "2", pw},
	} {
		name := c.typ + c.bits + "-v" + c.version + map[bool]string{true: "-enc", false: ""}[c.pass != ""]
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(dir, name+".ppk")
			args := []string{"-t", c.typ, "-o", out, "--ppk-param", "version=" + c.version, "-q"}
			if c.bits != "" {
				args = append(args, "-b", c.bits)
			}
			if c.pass != "" {
				args = append(args, "--new-passphrase", c.pass)
			} else {
				args = append(args, "--new-passphrase", os.DevNull)
			}
			if b, err := exec.Command(bin, args...).CombinedOutput(); err != nil {
				t.Fatalf("puttygen: %v %s", err, b)
			}
			listArgs := []string{out, "-l", "-E", "sha256"}
			if c.pass != "" {
				listArgs = append(listArgs, "--old-passphrase", c.pass)
			}
			fp, err := exec.Command(bin, listArgs...).Output()
			if err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(out)
			pass := ""
			if c.pass != "" {
				pass = "s3cret pass"
			}
			k, err := Import(data, []byte(pass))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(fp), k.Fingerprint()) {
				t.Fatalf("fingerprint %s, puttygen says %s", k.Fingerprint(), fp)
			}
			if pass != "" {
				if _, err := Import(data, []byte("not it")); !errors.Is(err, ErrPassphraseWrong) {
					t.Fatalf("wrong passphrase: %v", err)
				}
			}
		})
	}
}

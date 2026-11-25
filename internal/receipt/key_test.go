package receipt

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func pkcs8PEM(t *testing.T, key any) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func TestParsePrivateKey(t *testing.T) {
	key := testKey(t, 3)
	got, err := ParsePrivateKey(pkcs8PEM(t, key))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(key) {
		t.Fatal("parsed key differs")
	}
	rsaKey, err := rsa.GenerateKey(rand.Reader, 1024) //nolint:gosec // a test fixture of the wrong type
	if err != nil {
		t.Fatal(err)
	}
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct{ text, want string }{
		"rsa":     {pkcs8PEM(t, rsaKey), "not an Ed25519"},
		"ec":      {pkcs8PEM(t, ecKey), "not an Ed25519"},
		"garbage": {"hello", "no PEM"},
		"empty":   {"", "no PEM"},
		"public":  {string(PublicPEM(key.Public().(ed25519.PublicKey))), "PRIVATE KEY"},
		"bad der": {string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte{1, 2}})), "PKCS#8"},
	}
	for name, c := range cases {
		_, err := ParsePrivateKey(c.text)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to mention %q", name, err, c.want)
		}
	}
}

func TestParsePublicKeys(t *testing.T) {
	a, b := testKey(t, 4), testKey(t, 5)
	data := append(PublicPEM(a.Public().(ed25519.PublicKey)), PublicPEM(b.Public().(ed25519.PublicKey))...)
	keys, err := ParsePublicKeys(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || !keys[0].Equal(a.Public()) || !keys[1].Equal(b.Public()) {
		t.Fatalf("keys = %v", keys)
	}
	if _, err := ParsePublicKeys([]byte("nothing")); err == nil {
		t.Fatal("no PUBLIC KEY block should fail")
	}
	ecKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(&ecKey.PublicKey)
	ec := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	if _, err := ParsePublicKeys(ec); err == nil || !strings.Contains(err.Error(), "Ed25519") {
		t.Fatalf("EC public key: err = %v", err)
	}
	bad := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: []byte{1}})
	if _, err := ParsePublicKeys(bad); err == nil {
		t.Fatal("bad DER should fail")
	}
}

func TestKeyID(t *testing.T) {
	pub := testKey(t, 6).Public().(ed25519.PublicKey)
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(der)
	want := "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
	if got := KeyID(pub); got != want {
		t.Fatalf("KeyID = %q, want %q", got, want)
	}
}

func TestLoadOrCreate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state", "receipts")
	key, created, err := LoadOrCreate(dir)
	if err != nil || !created {
		t.Fatalf("first call: created=%v err=%v", created, err)
	}
	again, created, err := LoadOrCreate(dir)
	if err != nil || created || !again.Equal(key) {
		t.Fatalf("second call: created=%v err=%v same=%v", created, err, again.Equal(key))
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(dir)
		if info.Mode().Perm() != 0o700 {
			t.Errorf("dir mode %v", info.Mode().Perm())
		}
		info, _ = os.Stat(filepath.Join(dir, KeyFile))
		if info.Mode().Perm() != 0o600 {
			t.Errorf("key mode %v", info.Mode().Perm())
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("leftover files: %v", entries)
	}
}

func TestLoadOrCreateConcurrent(t *testing.T) {
	dir := t.TempDir()
	keys := make([]ed25519.PrivateKey, 8)
	created := make([]bool, 8)
	var wg sync.WaitGroup
	for i := range keys {
		wg.Go(func() {
			var err error
			keys[i], created[i], err = LoadOrCreate(dir)
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	winners := 0
	for i := range keys {
		if !keys[i].Equal(keys[0]) {
			t.Fatal("concurrent first use gave different keys")
		}
		if created[i] {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("%d callers say they created the key", winners)
	}
}

func TestLoadOrCreateBadFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, KeyFile), []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadOrCreate(dir); err == nil {
		t.Fatal("a broken key file must not be replaced or used")
	}
	if _, _, err := LoadExisting(dir); err == nil {
		t.Fatal("LoadExisting must report a broken key file")
	}
}

func TestLoadOrCreateUnwritable(t *testing.T) {
	parent := t.TempDir()
	file := filepath.Join(parent, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadOrCreate(filepath.Join(file, "receipts")); err == nil {
		t.Fatal("a directory under a file cannot be made")
	}
}

func TestLoadExisting(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "receipts")
	if _, ok, err := LoadExisting(dir); ok || err != nil {
		t.Fatalf("missing key: ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("LoadExisting created the directory")
	}
	key, _, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := LoadExisting(dir)
	if err != nil || !ok || !got.Equal(key) {
		t.Fatalf("existing key: ok=%v err=%v", ok, err)
	}
}

func TestPublicPEMRoundTrip(t *testing.T) {
	pub := testKey(t, 7).Public().(ed25519.PublicKey)
	keys, err := ParsePublicKeys(PublicPEM(pub))
	if err != nil || len(keys) != 1 || !keys[0].Equal(pub) {
		t.Fatalf("round trip: %v %v", keys, err)
	}
}

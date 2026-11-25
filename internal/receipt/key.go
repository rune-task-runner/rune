package receipt

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// KeyFile is the name of the local signing key in its directory.
const KeyFile = "key.pem"

// ParsePrivateKey reads a PKCS#8 PEM Ed25519 private key, as
// `openssl genpkey -algorithm ed25519` writes it.
func ParsePrivateKey(text string) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode([]byte(text))
	if block == nil {
		return nil, errors.New("no PEM block: want a PKCS#8 PEM Ed25519 private key")
	}
	if block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("a %s block: want a PKCS#8 PEM Ed25519 PRIVATE KEY", block.Type)
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("not a PKCS#8 private key: %w", err)
	}
	ed, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("not an Ed25519 private key")
	}
	return ed, nil
}

// ParsePublicKeys reads every PUBLIC KEY block of a PEM file. Each must be an
// Ed25519 key, and there must be one at least.
func ParsePublicKeys(data []byte) ([]ed25519.PublicKey, error) {
	var keys []ed25519.PublicKey
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			break
		}
		if block.Type != "PUBLIC KEY" {
			continue
		}
		key, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("not a PKIX public key: %w", err)
		}
		ed, ok := key.(ed25519.PublicKey)
		if !ok {
			return nil, errors.New("not an Ed25519 public key")
		}
		keys = append(keys, ed)
	}
	if len(keys) == 0 {
		return nil, errors.New("no PUBLIC KEY block")
	}
	return keys, nil
}

// KeyID names a public key: SHA256: and the unpadded base64 of the SHA-256
// of its PKIX encoding.
func KeyID(pub ed25519.PublicKey) string {
	// An Ed25519 key of the right length always marshals.
	der, _ := x509.MarshalPKIXPublicKey(pub)
	sum := sha256.Sum256(der)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// PublicPEM encodes a public key as a PKIX PEM block.
func PublicPEM(pub ed25519.PublicKey) []byte {
	der, _ := x509.MarshalPKIXPublicKey(pub)
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

// LoadExisting reads the local key in dir. A missing key is not an error: ok
// is false and nothing is created.
func LoadExisting(dir string) (key ed25519.PrivateKey, ok bool, err error) {
	data, err := os.ReadFile(filepath.Join(dir, KeyFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read signing key: %w", err)
	}
	key, err = ParsePrivateKey(string(data))
	if err != nil {
		return nil, false, fmt.Errorf("signing key %s: %w", filepath.Join(dir, KeyFile), err)
	}
	return key, true, nil
}

// LoadOrCreate reads the local key in dir, or creates it: the directory only
// its owner can enter, the file only its owner can read. Two first uses at
// once agree on one key, the one linked into place first.
func LoadOrCreate(dir string) (key ed25519.PrivateKey, created bool, err error) {
	if key, ok, err := LoadExisting(dir); ok || err != nil {
		return key, false, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, false, fmt.Errorf("create signing key: %w", err)
	}
	_, key, err = ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, false, fmt.Errorf("create signing key: %w", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, false, fmt.Errorf("create signing key: %w", err)
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	err = writeNew(dir, KeyFile, data, 0o600)
	if errors.Is(err, fs.ErrExist) {
		key, ok, err := LoadExisting(dir)
		if err == nil && !ok {
			err = errors.New("signing key vanished while it was created")
		}
		return key, false, err
	}
	if err != nil {
		return nil, false, fmt.Errorf("create signing key: %w", err)
	}
	return key, true, nil
}

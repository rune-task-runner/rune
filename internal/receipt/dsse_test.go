package receipt

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
)

func testKey(t *testing.T, seed byte) ed25519.PrivateKey {
	t.Helper()
	return ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seed}, ed25519.SeedSize))
}

func TestPAE(t *testing.T) {
	got := PAE(PayloadType, []byte("hello"))
	want := "DSSEv1 28 application/vnd.in-toto+json 5 hello"
	if string(got) != want {
		t.Fatalf("PAE = %q, want %q", got, want)
	}
}

func TestSignOpenRoundTrip(t *testing.T) {
	key := testKey(t, 1)
	line, err := Sign(key, []byte(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(line, []byte("\n")) || bytes.Count(line, []byte("\n")) != 1 {
		t.Fatalf("envelope is not one line: %q", line)
	}
	again, err := Sign(key, []byte(`{"a":1}`))
	if err != nil || !bytes.Equal(line, again) {
		t.Fatalf("signing is not deterministic: %q vs %q", line, again)
	}
	payload, keyID, err := Open(line, []ed25519.PublicKey{testKey(t, 2).Public().(ed25519.PublicKey), key.Public().(ed25519.PublicKey)})
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != `{"a":1}` {
		t.Fatalf("payload %q", payload)
	}
	if keyID != KeyID(key.Public().(ed25519.PublicKey)) {
		t.Fatalf("key id %q", keyID)
	}
}

func mutate(t *testing.T, line []byte, change func(*Envelope)) []byte {
	t.Helper()
	var env Envelope
	if err := json.Unmarshal(line, &env); err != nil {
		t.Fatal(err)
	}
	change(&env)
	out, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func flip(s string) string {
	raw, _ := base64.StdEncoding.DecodeString(s)
	raw[len(raw)/2] ^= 1
	return base64.StdEncoding.EncodeToString(raw)
}

func TestOpenRejectsTampering(t *testing.T) {
	key := testKey(t, 1)
	trusted := []ed25519.PublicKey{key.Public().(ed25519.PublicKey)}
	line, err := Sign(key, []byte(`{"task":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"payload":     mutate(t, line, func(e *Envelope) { e.Payload = flip(e.Payload) }),
		"signature":   mutate(t, line, func(e *Envelope) { e.Signatures[0].Sig = flip(e.Signatures[0].Sig) }),
		"payloadType": mutate(t, line, func(e *Envelope) { e.PayloadType = "application/vnd.in-toto+jsoN" }),
	}
	for name, bad := range cases {
		if _, _, err := Open(bad, trusted); !errors.Is(err, ErrBadSignature) {
			t.Errorf("%s: err = %v, want ErrBadSignature", name, err)
		}
	}
}

func TestOpenUnknownSigner(t *testing.T) {
	line, err := Sign(testKey(t, 1), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Open(line, []ed25519.PublicKey{testKey(t, 2).Public().(ed25519.PublicKey)}); !errors.Is(err, ErrUnknownSigner) {
		t.Fatalf("err = %v, want ErrUnknownSigner", err)
	}
	if _, _, err := Open(line, nil); !errors.Is(err, ErrUnknownSigner) {
		t.Fatalf("no trusted keys: err = %v, want ErrUnknownSigner", err)
	}
}

func TestOpenSignedOtherPayloadType(t *testing.T) {
	key := testKey(t, 1)
	body := []byte(`{}`)
	sig := ed25519.Sign(key, PAE("text/plain", body))
	env := Envelope{PayloadType: "text/plain", Payload: base64.StdEncoding.EncodeToString(body), Signatures: []Signature{{
		KeyID: KeyID(key.Public().(ed25519.PublicKey)), Sig: base64.StdEncoding.EncodeToString(sig),
	}}}
	line, _ := json.Marshal(env)
	if _, _, err := Open(line, []ed25519.PublicKey{key.Public().(ed25519.PublicKey)}); !errors.Is(err, ErrMalformed) {
		t.Fatalf("err = %v, want ErrMalformed", err)
	}
}

func TestDecodeMalformed(t *testing.T) {
	key := testKey(t, 1)
	line, err := Sign(key, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"not json":       []byte("nope"),
		"no payload":     mutate(t, line, func(e *Envelope) { e.Payload = "" }),
		"no type":        mutate(t, line, func(e *Envelope) { e.PayloadType = "" }),
		"bad base64":     mutate(t, line, func(e *Envelope) { e.Payload = "%%%" }),
		"bad sig base64": mutate(t, line, func(e *Envelope) { e.Signatures[0].Sig = "%%%" }),
		"no signature":   mutate(t, line, func(e *Envelope) { e.Signatures = nil }),
		"two signatures": mutate(t, line, func(e *Envelope) { e.Signatures = append(e.Signatures, e.Signatures[0]) }),
	}
	for name, bad := range cases {
		if _, _, err := Decode(bad); !errors.Is(err, ErrMalformed) {
			t.Errorf("Decode %s: err = %v, want ErrMalformed", name, err)
		}
		if _, _, err := Open(bad, []ed25519.PublicKey{key.Public().(ed25519.PublicKey)}); !errors.Is(err, ErrMalformed) {
			t.Errorf("Open %s: err = %v, want ErrMalformed", name, err)
		}
	}
	env, payload, err := Decode(line)
	if err != nil || string(payload) != "{}" || env.PayloadType != PayloadType {
		t.Fatalf("Decode of a good envelope: %v %q %+v", err, payload, env)
	}
}

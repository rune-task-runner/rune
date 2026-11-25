package receipt

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// PayloadType is the DSSE payload type of an in-toto statement.
const PayloadType = "application/vnd.in-toto+json"

// The ways opening an envelope fails.
var (
	ErrMalformed     = errors.New("malformed receipt")
	ErrUnknownSigner = errors.New("signed by an untrusted key")
	ErrBadSignature  = errors.New("the signature does not match the receipt")
)

// Envelope is a DSSE envelope.
type Envelope struct {
	PayloadType string      `json:"payloadType"`
	Payload     string      `json:"payload"`
	Signatures  []Signature `json:"signatures"`
}

// Signature is one signature of an envelope. KeyID only says which key to
// try: the key that verifies the signature is the signer.
type Signature struct {
	KeyID string `json:"keyid"`
	Sig   string `json:"sig"`
}

// PAE is DSSE's pre-authentication encoding of a payload, the bytes that are
// signed.
func PAE(payloadType string, body []byte) []byte {
	out := []byte("DSSEv1 ")
	out = strconv.AppendInt(out, int64(len(payloadType)), 10)
	out = append(out, ' ')
	out = append(out, payloadType...)
	out = append(out, ' ')
	out = strconv.AppendInt(out, int64(len(body)), 10)
	out = append(out, ' ')
	return append(out, body...)
}

// Sign wraps a statement in an envelope signed with key and returns it as
// one line of JSON with its line break.
func Sign(key ed25519.PrivateKey, statement []byte) ([]byte, error) {
	pub, ok := key.Public().(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("not an Ed25519 key")
	}
	env := Envelope{
		PayloadType: PayloadType,
		Payload:     base64.StdEncoding.EncodeToString(statement),
		Signatures: []Signature{{
			KeyID: KeyID(pub),
			Sig:   base64.StdEncoding.EncodeToString(ed25519.Sign(key, PAE(PayloadType, statement))),
		}},
	}
	line, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("encode envelope: %w", err)
	}
	return append(line, '\n'), nil
}

// Decode reads an envelope and its payload without checking the signature,
// so a caller can say which task an untrusted receipt claims to be about.
func Decode(line []byte) (Envelope, []byte, error) {
	var env Envelope
	if err := json.Unmarshal(line, &env); err != nil {
		return Envelope{}, nil, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	if env.PayloadType == "" || env.Payload == "" || len(env.Signatures) != 1 {
		return Envelope{}, nil, fmt.Errorf("%w: an envelope needs a payload type, a payload and one signature", ErrMalformed)
	}
	payload, err := base64.StdEncoding.DecodeString(env.Payload)
	if err != nil {
		return Envelope{}, nil, fmt.Errorf("%w: payload: %w", ErrMalformed, err)
	}
	if _, err := base64.StdEncoding.DecodeString(env.Signatures[0].Sig); err != nil {
		return Envelope{}, nil, fmt.Errorf("%w: signature: %w", ErrMalformed, err)
	}
	return env, payload, nil
}

// Open checks the envelope's signature with the trusted key its key ID names
// and returns the payload and that key's ID. No trusted key with that ID is
// ErrUnknownSigner; a signature that does not verify is ErrBadSignature.
func Open(line []byte, trusted []ed25519.PublicKey) (payload []byte, keyID string, err error) {
	env, payload, err := Decode(line)
	if err != nil {
		return nil, "", err
	}
	sig := env.Signatures[0]
	var key ed25519.PublicKey
	for _, k := range trusted {
		if KeyID(k) == sig.KeyID {
			key = k
			break
		}
	}
	if key == nil {
		return nil, sig.KeyID, ErrUnknownSigner
	}
	raw, _ := base64.StdEncoding.DecodeString(sig.Sig) // checked by Decode
	if !ed25519.Verify(key, PAE(env.PayloadType, payload), raw) {
		return nil, sig.KeyID, ErrBadSignature
	}
	if env.PayloadType != PayloadType {
		return nil, sig.KeyID, fmt.Errorf("%w: payload type %q", ErrMalformed, env.PayloadType)
	}
	return payload, sig.KeyID, nil
}

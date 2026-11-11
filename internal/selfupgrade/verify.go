package selfupgrade

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"strconv"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

// verifyChecksums proves that checksums was signed keylessly by exactly
// subject through issuer: the certificate chains to the trust root, carries a
// certificate transparency timestamp, and the signature is recorded in the
// transparency log at a time the certificate was valid. Every failure is an
// unverified *Error.
func verifyChecksums(trust root.TrustedMaterial, issuer, subject string, checksums, bundleJSON []byte) error {
	b, err := loadBundle(bundleJSON, checksums)
	if err != nil {
		return &Error{Reason: ReasonUnverified, Err: err}
	}
	verifier, err := verify.NewVerifier(trust,
		verify.WithSignedCertificateTimestamps(1),
		verify.WithTransparencyLog(1),
		verify.WithObserverTimestamps(1))
	if err != nil {
		return &Error{Reason: ReasonUnverified, Err: err}
	}
	identity, err := verify.NewShortCertificateIdentity(issuer, "", subject, "")
	if err != nil {
		return &Error{Reason: ReasonUnverified, Err: err}
	}
	sum := sha256.Sum256(checksums)
	policy := verify.NewPolicy(verify.WithArtifactDigest("sha256", sum[:]), verify.WithCertificateIdentity(identity))
	if _, err := verifier.Verify(b, policy); err != nil {
		return &Error{Reason: ReasonUnverified, Err: err}
	}
	return nil
}

// loadBundle reads a signature file in either form a release has carried: a
// standard Sigstore bundle, recognised by its mediaType, or cosign's older
// bundle, which is mapped onto a standard one so both pass the same checks.
func loadBundle(bundleJSON, checksums []byte) (*bundle.Bundle, error) {
	var probe struct {
		MediaType string `json:"mediaType"`
	}
	if err := json.Unmarshal(bundleJSON, &probe); err != nil {
		return nil, errors.New("signature file is not a JSON object")
	}
	if probe.MediaType == "" {
		converted, err := convertLegacyBundle(bundleJSON, checksums)
		if err != nil {
			return nil, err
		}
		bundleJSON = converted
	}
	var b bundle.Bundle
	if err := b.UnmarshalJSON(bundleJSON); err != nil {
		return nil, err
	}
	return &b, nil
}

// legacyBundle is cosign's pre-standard bundle layout.
type legacyBundle struct {
	Signature string `json:"base64Signature"`
	Cert      string `json:"cert"`
	Rekor     *struct {
		SignedEntryTimestamp string `json:"SignedEntryTimestamp"`
		Payload              struct {
			Body           string `json:"body"`
			IntegratedTime int64  `json:"integratedTime"`
			LogIndex       int64  `json:"logIndex"`
			LogID          string `json:"logID"`
		} `json:"Payload"`
	} `json:"rekorBundle"`
}

func convertLegacyBundle(raw, checksums []byte) ([]byte, error) {
	var lb legacyBundle
	if err := json.Unmarshal(raw, &lb); err != nil {
		return nil, errors.New("signature file is not a JSON object")
	}
	if lb.Signature == "" || lb.Cert == "" || lb.Rekor == nil {
		return nil, errors.New("signature file is neither a Sigstore bundle nor a cosign bundle")
	}
	certPEM, err := base64.StdEncoding.DecodeString(lb.Cert)
	if err != nil {
		return nil, errors.New("signature file: certificate is not base64")
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, errors.New("signature file: certificate is not PEM")
	}
	logID, err := hex.DecodeString(lb.Rekor.Payload.LogID)
	if err != nil {
		return nil, errors.New("signature file: log ID is not hex")
	}
	digest := sha256.Sum256(checksums)
	b64 := base64.StdEncoding.EncodeToString
	// Field names and string-encoded integers follow the protobuf JSON mapping.
	return json.Marshal(map[string]any{
		"mediaType": "application/vnd.dev.sigstore.bundle+json;version=0.1",
		"verificationMaterial": map[string]any{
			"x509CertificateChain": map[string]any{
				"certificates": []any{map[string]any{"rawBytes": b64(block.Bytes)}},
			},
			"tlogEntries": []any{map[string]any{
				"logIndex":          strconv.FormatInt(lb.Rekor.Payload.LogIndex, 10),
				"logId":             map[string]any{"keyId": b64(logID)},
				"kindVersion":       map[string]any{"kind": "hashedrekord", "version": "0.0.1"},
				"integratedTime":    strconv.FormatInt(lb.Rekor.Payload.IntegratedTime, 10),
				"inclusionPromise":  map[string]any{"signedEntryTimestamp": lb.Rekor.SignedEntryTimestamp},
				"canonicalizedBody": lb.Rekor.Payload.Body,
			}},
		},
		"messageSignature": map[string]any{
			"messageDigest": map[string]any{"algorithm": "SHA2_256", "digest": b64(digest[:])},
			"signature":     lb.Signature,
		},
	})
}

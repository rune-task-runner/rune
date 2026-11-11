package upgradetest

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/url"
	"time"

	ct "github.com/google/certificate-transparency-go"
	cttls "github.com/google/certificate-transparency-go/tls"
	ctx509 "github.com/google/certificate-transparency-go/x509"
	protobundle "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
	protocommon "github.com/sigstore/protobuf-specs/gen/pb-go/common/v1"
	protorekor "github.com/sigstore/protobuf-specs/gen/pb-go/rekor/v1"
	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/testing/ca"
)

// authority is a private Sigstore: a certificate authority, a certificate
// transparency log and a transparency log, each with its own key. The
// library's VirtualSigstore is not used because its certificates carry no
// certificate transparency timestamp, which the updater requires.
type authority struct {
	inter    *x509.Certificate
	interKey *ecdsa.PrivateKey
	ctKey    *ecdsa.PrivateKey
	rekorKey *ecdsa.PrivateKey
	trust    *root.TrustedRoot
}

func newAuthority() (*authority, error) {
	rootCert, rootKey, err := ca.GenerateRootCa()
	if err != nil {
		return nil, err
	}
	inter, interKey, err := ca.GenerateFulcioIntermediate(rootCert, rootKey)
	if err != nil {
		return nil, err
	}
	a := &authority{inter: inter, interKey: interKey}
	if a.ctKey, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader); err != nil {
		return nil, err
	}
	if a.rekorKey, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader); err != nil {
		return nil, err
	}
	start, end := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	logFor := func(key *ecdsa.PrivateKey, base string) (map[string]*root.TransparencyLog, error) {
		id, err := logID(&key.PublicKey)
		if err != nil {
			return nil, err
		}
		return map[string]*root.TransparencyLog{hex.EncodeToString(id): {
			BaseURL: base, ID: id, ValidityPeriodStart: start, ValidityPeriodEnd: end,
			HashFunc: crypto.SHA256, PublicKey: &key.PublicKey, SignatureHashFunc: crypto.SHA256,
		}}, nil
	}
	ctLogs, err := logFor(a.ctKey, "https://ctlog.localhost")
	if err != nil {
		return nil, err
	}
	rekorLogs, err := logFor(a.rekorKey, "https://rekor.localhost")
	if err != nil {
		return nil, err
	}
	fulcio := &root.FulcioCertificateAuthority{
		Root: rootCert, Intermediates: []*x509.Certificate{inter},
		ValidityPeriodStart: start, ValidityPeriodEnd: end, URI: "https://fulcio.localhost",
	}
	a.trust, err = root.NewTrustedRoot(root.TrustedRootMediaType01,
		[]root.CertificateAuthority{fulcio}, ctLogs, nil, rekorLogs)
	return a, err
}

func logID(pub *ecdsa.PublicKey) ([]byte, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(der)
	return sum[:], nil
}

// sign returns a Sigstore bundle over payload, as if signed keylessly by
// subject through issuer. The log issues an inclusion promise, which bundle
// version 0.1 carries.
func (a *authority) sign(subject, issuer string, payload []byte) ([]byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	leaf, err := a.issue(subject, issuer, &key.PublicKey, now)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(payload)
	sig, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		return nil, err
	}
	entry, err := a.logEntry(leaf, digest[:], sig, now)
	if err != nil {
		return nil, err
	}
	pb := &protobundle.Bundle{
		MediaType: "application/vnd.dev.sigstore.bundle+json;version=0.1",
		VerificationMaterial: &protobundle.VerificationMaterial{
			Content: &protobundle.VerificationMaterial_X509CertificateChain{
				X509CertificateChain: &protocommon.X509CertificateChain{
					Certificates: []*protocommon.X509Certificate{{RawBytes: leaf.Raw}},
				},
			},
			TlogEntries: []*protorekor.TransparencyLogEntry{entry},
		},
		Content: &protobundle.Bundle_MessageSignature{
			MessageSignature: &protocommon.MessageSignature{
				MessageDigest: &protocommon.HashOutput{Algorithm: protocommon.HashAlgorithm_SHA2_256, Digest: digest[:]},
				Signature:     sig,
			},
		},
	}
	b, err := bundle.NewBundle(pb)
	if err != nil {
		return nil, err
	}
	return b.MarshalJSON()
}

// issue creates the signing certificate with an embedded certificate
// transparency timestamp. The timestamp signs the certificate body without
// its own extension, so the certificate is issued twice: once with a
// placeholder to obtain that body, then with the real timestamp.
func (a *authority) issue(subject, issuer string, pub *ecdsa.PublicKey, now time.Time) (*x509.Certificate, error) {
	san, err := url.Parse(subject)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 100))
	if err != nil {
		return nil, err
	}
	issuerExt := pkix.Extension{Id: asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 1}, Value: []byte(issuer)}
	create := func(sctList []byte) (*x509.Certificate, error) {
		ext, err := asn1.Marshal(sctList)
		if err != nil {
			return nil, err
		}
		tmpl := &x509.Certificate{
			SerialNumber: serial,
			URIs:         []*url.URL{san},
			NotBefore:    now.Add(-time.Minute),
			NotAfter:     now.Add(10 * time.Minute),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
			ExtraExtensions: []pkix.Extension{
				issuerExt,
				{Id: asn1.ObjectIdentifier(ctx509.OIDExtensionCTSCT), Value: ext},
			},
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, a.inter, pub, a.interKey)
		if err != nil {
			return nil, err
		}
		return x509.ParseCertificate(der)
	}

	placeholder, err := a.sctList(nil, now)
	if err != nil {
		return nil, err
	}
	draft, err := create(placeholder)
	if err != nil {
		return nil, err
	}
	draftCT, err := ctx509.ParseCertificate(draft.Raw)
	if err != nil {
		return nil, err
	}
	tbs, err := ctx509.RemoveSCTList(draftCT.RawTBSCertificate)
	if err != nil {
		return nil, err
	}
	list, err := a.sctList(tbs, now)
	if err != nil {
		return nil, err
	}
	return create(list)
}

// sctList returns a serialized one-entry timestamp list. A nil body yields an
// unsigned placeholder of the same shape.
func (a *authority) sctList(tbs []byte, now time.Time) ([]byte, error) {
	id, err := logID(&a.ctKey.PublicKey)
	if err != nil {
		return nil, err
	}
	sct := ct.SignedCertificateTimestamp{
		SCTVersion: ct.V1,
		Timestamp:  uint64(now.UnixMilli()),
		Signature: ct.DigitallySigned{
			Algorithm: cttls.SignatureAndHashAlgorithm{Hash: cttls.SHA256, Signature: cttls.ECDSA},
			Signature: []byte{0},
		},
	}
	copy(sct.LogID.KeyID[:], id)
	if tbs != nil {
		leaf := ct.MerkleTreeLeaf{
			Version:  ct.V1,
			LeafType: ct.TimestampedEntryLeafType,
			TimestampedEntry: &ct.TimestampedEntry{
				EntryType: ct.PrecertLogEntryType,
				Timestamp: sct.Timestamp,
				PrecertEntry: &ct.PreCert{
					IssuerKeyHash:  sha256.Sum256(a.inter.RawSubjectPublicKeyInfo),
					TBSCertificate: tbs,
				},
			},
		}
		input, err := ct.SerializeSCTSignatureInput(sct, ct.LogEntry{Leaf: leaf})
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(input)
		if sct.Signature.Signature, err = ecdsa.SignASN1(rand.Reader, a.ctKey, sum[:]); err != nil {
			return nil, err
		}
	}
	raw, err := cttls.Marshal(sct)
	if err != nil {
		return nil, err
	}
	return cttls.Marshal(ctx509.SignedCertificateTimestampList{SCTList: []ctx509.SerializedSCT{{Val: raw}}})
}

// logEntry records the signature in the transparency log and returns the
// entry with the log's signed promise.
func (a *authority) logEntry(leaf *x509.Certificate, digest, sig []byte, now time.Time) (*protorekor.TransparencyLogEntry, error) {
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw})
	body, err := json.Marshal(map[string]any{
		"apiVersion": "0.0.1",
		"kind":       "hashedrekord",
		"spec": map[string]any{
			"data": map[string]any{"hash": map[string]any{"algorithm": "sha256", "value": hex.EncodeToString(digest)}},
			"signature": map[string]any{
				"content":   base64.StdEncoding.EncodeToString(sig),
				"publicKey": map[string]any{"content": base64.StdEncoding.EncodeToString(certPEM)},
			},
		},
	})
	if err != nil {
		return nil, err
	}
	id, err := logID(&a.rekorKey.PublicKey)
	if err != nil {
		return nil, err
	}
	const logIndex = 1000
	// The promise signs the canonical JSON of these four fields, keys sorted.
	promised := fmt.Sprintf(`{"body":%q,"integratedTime":%d,"logID":%q,"logIndex":%d}`,
		base64.StdEncoding.EncodeToString(body), now.Unix(), hex.EncodeToString(id), logIndex)
	sum := sha256.Sum256([]byte(promised))
	set, err := ecdsa.SignASN1(rand.Reader, a.rekorKey, sum[:])
	if err != nil {
		return nil, err
	}
	return &protorekor.TransparencyLogEntry{
		LogIndex:          logIndex,
		LogId:             &protocommon.LogId{KeyId: id},
		KindVersion:       &protorekor.KindVersion{Kind: "hashedrekord", Version: "0.0.1"},
		IntegratedTime:    now.Unix(),
		InclusionPromise:  &protorekor.InclusionPromise{SignedEntryTimestamp: set},
		CanonicalizedBody: body,
	}, nil
}

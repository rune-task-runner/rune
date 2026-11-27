package receipt

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

// wanted is the context in which passing(...) receipts qualify.
func wanted() Want {
	r := minimalReceipt()
	return Want{
		Project:    r.Project,
		Task:       r.Task,
		Tree:       vTree,
		Rune:       r.Rune,
		Definition: r.Definition,
		Now:        t0.Add(time.Hour),
		MaxAge:     24 * time.Hour,
	}
}

func reuse(t *testing.T, want Want, cands ...Candidate) Verdict {
	t.Helper()
	key := testKey(t, 1)
	return Reuse(cands, []ed25519.PublicKey{key.Public().(ed25519.PublicKey)}, want)
}

func TestReuseQualifies(t *testing.T) {
	key := testKey(t, 1)
	v := reuse(t, wanted(), local(signed(t, key, passing("a", t0))))
	if !v.Verified || v.Reason != "" || v.Receipt == nil || v.Receipt.ID != "a" {
		t.Fatalf("verdict %+v", v)
	}
	if v.Receipt.Signer != KeyID(key.Public().(ed25519.PublicKey)) {
		t.Fatalf("signer %q", v.Receipt.Signer)
	}

	// Arguments and outputs that match qualify too.
	r := passing("b", t0)
	r.Arguments = []Arg{{Name: "pkg", Values: []string{"./a"}}}
	r.Outputs = &Outputs{Count: 1, Digest: "g"}
	want := wanted()
	want.Arguments = []Arg{{Name: "pkg", Values: []string{"./a"}}}
	want.Outputs = &Outputs{Count: 1, Digest: "g"}
	if v := reuse(t, want, local(signed(t, key, r))); !v.Verified {
		t.Fatalf("matching arguments and outputs: %+v", v)
	}
}

func TestReuseNewest(t *testing.T) {
	key := testKey(t, 1)
	older, newer := passing("older", t0), passing("newer", t0.Add(time.Minute))
	v := reuse(t, wanted(), local(signed(t, key, older)), local(signed(t, key, newer)))
	if !v.Verified || v.Receipt.ID != "newer" {
		t.Fatalf("verdict %+v", v)
	}
}

func TestReuseReasons(t *testing.T) {
	key := testKey(t, 1)
	change := func(f func(*Receipt)) func(*Receipt, *Want) {
		return func(r *Receipt, _ *Want) { f(r) }
	}
	now := func(f func(*Want)) func(*Receipt, *Want) {
		return func(_ *Receipt, w *Want) { f(w) }
	}
	cases := []struct {
		reason string
		name   string
		apply  func(*Receipt, *Want)
	}{
		{ReasonFailed, "failed", change(func(r *Receipt) { r.Status = StatusFailed })},
		{ReasonInterrupted, "interrupted", change(func(r *Receipt) { r.Status = StatusInterrupted })},
		{ReasonRefused, "refused", change(func(r *Receipt) { r.Status = StatusRefused })},
		{ReasonCached, "a cached run never qualifies", change(func(r *Receipt) { r.Status = StatusCached })},
		{ReasonIncomplete, "incomplete", change(func(r *Receipt) { r.Status = StatusIncomplete })},
		{ReasonUnsupported, "version", change(func(r *Receipt) { r.Version = 2 })},
		{ReasonNoSnapshot, "no snapshot", change(func(r *Receipt) { r.Source = nil })},
		{ReasonChangedDuringRun, "changed", change(func(r *Receipt) { r.Source.After = vOther })},
		{ReasonDifferentSnap, "other files", change(func(r *Receipt) { r.Source.Before, r.Source.After = vOther, vOther })},
		{ReasonUncoveredRunefile, "uncovered runefile", change(func(r *Receipt) {
			r.Runefiles = append(r.Runefiles, RunefileEntry{Path: "../x.rune"})
		})},
		{ReasonUncoveredInputs, "uncovered inputs", change(func(r *Receipt) { r.UncoveredInputs = []string{"vendor/a"} })},
		{ReasonFailureHook, "a failure hook's run", change(func(r *Receipt) { r.Role = RoleFailureHook })},
		{ReasonStale, "older than the maximum age", change(func(r *Receipt) { r.FinishedAt = t0.Add(-24 * time.Hour) })},
		{ReasonStale, "finished in the future", change(func(r *Receipt) { r.FinishedAt = t0.Add(2 * time.Hour) })},
		{ReasonOtherPlatform, "other OS", change(func(r *Receipt) { r.Rune.OS = "darwin" })},
		{ReasonOtherPlatform, "other arch", change(func(r *Receipt) { r.Rune.Arch = "arm64" })},
		{ReasonOtherRune, "other version", change(func(r *Receipt) { r.Rune.Version = "0.11.0" })},
		{ReasonOtherRune, "other commit", change(func(r *Receipt) { r.Rune.Commit = "def" })},
		{ReasonOtherDefinition, "other definition", change(func(r *Receipt) { r.Definition = "e" })},
		{ReasonMaskedArguments, "masked in the receipt", change(func(r *Receipt) { r.ArgumentsRedacted = true })},
		{ReasonMaskedArguments, "masked now", now(func(w *Want) { w.ArgumentsMasked = true })},
		{ReasonOtherArguments, "other arguments", change(func(r *Receipt) {
			r.Arguments = []Arg{{Name: "pkg", Values: []string{"./b"}}}
		})},
		{ReasonOverridden, "overridden in the receipt", change(func(r *Receipt) { r.Overrides = []string{"mode"} })},
		{ReasonOverridden, "overridden now", now(func(w *Want) { w.Overridden = true })},
		{ReasonOutputsDiffer, "other digest", func(r *Receipt, w *Want) {
			r.Outputs = &Outputs{Count: 1, Digest: "g"}
			w.Outputs = &Outputs{Count: 1, Digest: "h"}
		}},
		{ReasonOutputsDiffer, "outputs then, none now", change(func(r *Receipt) { r.Outputs = &Outputs{Count: 1, Digest: "g"} })},
		{ReasonOutputsDiffer, "none then, outputs now", now(func(w *Want) { w.Outputs = &Outputs{Count: 1, Digest: "g"} })},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, want := passing("r", t0), wanted()
			c.apply(&r, &want)
			v := reuse(t, want, local(signed(t, key, r)))
			if v.Verified || v.Reason != c.reason || v.Receipt == nil || v.Receipt.ID != "r" {
				t.Fatalf("verdict %+v, want %s", v, c.reason)
			}
		})
	}
}

func TestReuseSignatures(t *testing.T) {
	key := testKey(t, 1)
	if v := reuse(t, wanted(), local(signed(t, testKey(t, 2), passing("u", t0)))); v.Reason != ReasonUnknownSigner {
		t.Fatalf("untrusted key: %+v", v)
	}
	line := signed(t, key, passing("b", t0))
	var env Envelope
	if err := json.Unmarshal(line, &env); err != nil {
		t.Fatal(err)
	}
	sig, _ := base64.StdEncoding.DecodeString(env.Signatures[0].Sig)
	sig[0] ^= 1
	env.Signatures[0].Sig = base64.StdEncoding.EncodeToString(sig)
	tampered, _ := json.Marshal(env)
	if v := reuse(t, wanted(), local(tampered)); v.Reason != ReasonBadSignature {
		t.Fatalf("changed signature: %+v", v)
	}
}

func TestReuseCandidates(t *testing.T) {
	key := testKey(t, 1)
	if v := reuse(t, wanted()); v.Verified || v.Reason != ReasonNoReceipt || v.Receipt != nil {
		t.Fatalf("no candidate: %+v", v)
	}
	other := passing("o", t0)
	other.Project = "sub/Runefile"
	if v := reuse(t, wanted(), local(signed(t, key, other))); v.Reason != ReasonNoReceipt {
		t.Fatalf("another project: %+v", v)
	}
	lint := passing("l", t0)
	lint.Task = "lint"
	if v := reuse(t, wanted(), local(signed(t, key, lint))); v.Reason != ReasonNoReceipt {
		t.Fatalf("another task: %+v", v)
	}
	// A line that is not a receipt is passed over; the others decide.
	if v := reuse(t, wanted(), local([]byte("x")), local(signed(t, key, passing("a", t0)))); !v.Verified {
		t.Fatalf("with a malformed line: %+v", v)
	}
}

func TestReuseMostRelevantReason(t *testing.T) {
	key := testKey(t, 1)
	failed := passing("failed", t0)
	failed.Status = StatusFailed
	elsewhere := passing("elsewhere", t0.Add(time.Minute))
	elsewhere.Source.Before, elsewhere.Source.After = vOther, vOther
	v := reuse(t, wanted(), local(signed(t, key, failed)), local(signed(t, key, elsewhere)))
	if v.Reason != ReasonFailed || v.Receipt.ID != "failed" {
		t.Fatalf("the newest whose snapshot matches should give the reason: %+v", v)
	}
	v = reuse(t, wanted(), local(signed(t, key, elsewhere)))
	if v.Reason != ReasonDifferentSnap {
		t.Fatalf("else the newest: %+v", v)
	}
}

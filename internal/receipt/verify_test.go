package receipt

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

const (
	vCommit = "c0ffee0000000000000000000000000000000000"
	vTree   = "7ee0000000000000000000000000000000000000"
	vOther  = "0123456789012345678901234567890123456789"
)

// passing is a receipt that is evidence for vCommit as it stands.
func passing(id string, finished time.Time) Receipt {
	r := minimalReceipt()
	r.ID, r.FinishedAt = id, finished
	r.ExitCode = new(0)
	r.Source = &Source{Commit: vCommit, Before: vTree, After: vTree}
	return r
}

func signed(t *testing.T, key ed25519.PrivateKey, r Receipt) []byte {
	t.Helper()
	st, err := Statement(r)
	if err != nil {
		t.Fatal(err)
	}
	line, err := Sign(key, st)
	if err != nil {
		t.Fatal(err)
	}
	return line
}

func local(line []byte) Candidate { return Candidate{Line: line, Origin: OriginLocal, Name: "x.json"} }

func evaluate(t *testing.T, cands ...Candidate) (Verdict, []Problem) {
	t.Helper()
	key := testKey(t, 1)
	verdicts, problems := Evaluate(cands, []ed25519.PublicKey{key.Public().(ed25519.PublicKey)}, "Runefile", []string{"test"}, vCommit, vTree)
	if len(verdicts) != 1 {
		t.Fatalf("verdicts %+v", verdicts)
	}
	return verdicts[0], problems
}

var t0 = time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)

func TestEvaluateAccepts(t *testing.T) {
	key := testKey(t, 1)
	v, problems := evaluate(t, local(signed(t, key, passing("a", t0))))
	if !v.Verified || v.Reason != "" || v.Receipt == nil || !v.Receipt.Exact || v.Receipt.Origin != OriginLocal {
		t.Fatalf("verdict %+v %+v", v, v.Receipt)
	}
	if v.Receipt.Signer != KeyID(key.Public().(ed25519.PublicKey)) || v.Receipt.ID != "a" || v.Receipt.Status != StatusPassed {
		t.Fatalf("summary %+v", v.Receipt)
	}
	if len(problems) != 0 {
		t.Fatalf("problems %+v", problems)
	}

	dirty := passing("b", t0)
	dirty.Source.Commit, dirty.Source.Dirty = vOther, true
	v, _ = evaluate(t, local(signed(t, key, dirty)))
	if !v.Verified || v.Receipt.Exact {
		t.Fatalf("an identical snapshot of other work: %+v %+v", v, v.Receipt)
	}
}

func TestEvaluateReasons(t *testing.T) {
	key := testKey(t, 1)
	withStatus := func(status string) Receipt {
		r := passing("r", t0)
		r.Status = status
		return r
	}
	change := func(f func(*Receipt)) Receipt {
		r := passing("r", t0)
		f(&r)
		return r
	}
	cases := map[string]Receipt{
		ReasonFailed:           withStatus(StatusFailed),
		ReasonInterrupted:      withStatus(StatusInterrupted),
		ReasonRefused:          withStatus(StatusRefused),
		ReasonCached:           withStatus(StatusCached),
		ReasonIncomplete:       withStatus(StatusIncomplete),
		ReasonUnsupported:      change(func(r *Receipt) { r.Version = 2 }),
		ReasonNoSnapshot:       change(func(r *Receipt) { r.Source = nil }),
		ReasonChangedDuringRun: change(func(r *Receipt) { r.Source.After = vOther }),
		ReasonDifferentSnap:    change(func(r *Receipt) { r.Source.Before, r.Source.After = vOther, vOther }),
		ReasonUncoveredRunefile: change(func(r *Receipt) {
			r.Runefiles = append(r.Runefiles, RunefileEntry{Path: "../x.rune", Covered: false})
		}),
		ReasonUncoveredInputs: change(func(r *Receipt) { r.UncoveredInputs = []string{"vendor/a"} }),
	}
	for want, r := range cases {
		v, _ := evaluate(t, local(signed(t, key, r)))
		if v.Verified || v.Reason != want || v.Receipt == nil || v.Receipt.ID != "r" {
			t.Errorf("%s: verdict %+v", want, v)
		}
	}
	// A status this version does not know is a newer writer's.
	v, _ := evaluate(t, local(signed(t, key, withStatus("skipped"))))
	if v.Reason != ReasonUnsupported {
		t.Errorf("unknown status: %+v", v)
	}
	// A failed status is the first reason, whatever else is wrong.
	failedElsewhere := withStatus(StatusFailed)
	failedElsewhere.Source.Before = vOther
	if v, _ := evaluate(t, local(signed(t, key, failedElsewhere))); v.Reason != ReasonFailed {
		t.Errorf("order: %+v", v)
	}
}

func TestEvaluateSignatures(t *testing.T) {
	key := testKey(t, 1)
	other := testKey(t, 2)
	v, _ := evaluate(t, local(signed(t, other, passing("u", t0))))
	if v.Verified || v.Reason != ReasonUnknownSigner || v.Receipt.Signer != KeyID(other.Public().(ed25519.PublicKey)) {
		t.Fatalf("unknown signer: %+v %+v", v, v.Receipt)
	}

	line := signed(t, key, passing("b", t0))
	var env Envelope
	if err := json.Unmarshal(line, &env); err != nil {
		t.Fatal(err)
	}
	payload, _ := base64.StdEncoding.DecodeString(env.Payload)
	var st map[string]any
	_ = json.Unmarshal(payload, &st)
	st["predicate"].(map[string]any)["durationMs"] = 7
	payload, _ = json.Marshal(st)
	env.Payload = base64.StdEncoding.EncodeToString(payload)
	tampered, _ := json.Marshal(env)
	v, _ = evaluate(t, local(tampered))
	if v.Verified || v.Reason != ReasonBadSignature || v.Receipt.ID != "b" {
		t.Fatalf("tampered payload: %+v", v)
	}
}

func TestEvaluateSelection(t *testing.T) {
	key := testKey(t, 1)
	older, newer := passing("older", t0), passing("newer", t0.Add(time.Minute))
	tie := passing("zzz", t0)
	v, _ := evaluate(t, local(signed(t, key, older)), local(signed(t, key, newer)), local(signed(t, key, tie)))
	if v.Receipt.ID != "newer" {
		t.Fatalf("newest by finish time: %+v", v.Receipt)
	}
	v, _ = evaluate(t, local(signed(t, key, older)), local(signed(t, key, tie)))
	if v.Receipt.ID != "zzz" {
		t.Fatalf("ties by ID: %+v", v.Receipt)
	}

	failedHere := passing("failed-here", t0)
	failedHere.Status = StatusFailed
	elsewhere := passing("elsewhere", t0.Add(time.Hour))
	elsewhere.Source.Before, elsewhere.Source.After = vOther, vOther
	v, _ = evaluate(t, local(signed(t, key, failedHere)), local(signed(t, key, elsewhere)))
	if v.Reason != ReasonFailed || v.Receipt.ID != "failed-here" {
		t.Fatalf("the reason of a matching snapshot comes first: %+v", v)
	}
	v, _ = evaluate(t, local(signed(t, key, elsewhere)))
	if v.Reason != ReasonDifferentSnap {
		t.Fatalf("else the newest: %+v", v)
	}

	otherTask := passing("o", t0)
	otherTask.Task = "lint"
	v, _ = evaluate(t, local(signed(t, key, otherTask)))
	if v.Verified || v.Reason != ReasonNoReceipt || v.Receipt != nil {
		t.Fatalf("no receipt for the task: %+v", v)
	}

	// A note holds the receipts of every project of the repository: a task
	// of the same name in another Runefile is not this project's evidence.
	otherProject := passing("p", t0)
	otherProject.Project = "sub/Runefile"
	otherProject.Runefiles = []RunefileEntry{{Path: "sub/Runefile", SHA256: "aa", Covered: true}}
	v, problems := evaluate(t, Candidate{Line: signed(t, key, otherProject), Origin: OriginNote, Name: NotesRef})
	if v.Verified || v.Reason != ReasonNoReceipt || v.Receipt != nil || len(problems) != 0 {
		t.Fatalf("another project's receipt: %+v %+v", v, problems)
	}
}

func TestEvaluateProblemsAndDuplicates(t *testing.T) {
	key := testKey(t, 1)
	notReceipt, err := Sign(key, []byte(`{"_type":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	untrustedNotReceipt, _ := Sign(testKey(t, 2), []byte(`{"_type":"x"}`))
	good := signed(t, key, passing("g", t0))
	v, problems := evaluate(t,
		Candidate{Line: []byte("junk"), Origin: OriginLocal, Name: "junk.json"},
		Candidate{Line: notReceipt, Origin: OriginNote, Name: "note"},
		Candidate{Line: untrustedNotReceipt, Origin: OriginLocal, Name: "u.json"},
		Candidate{Line: good, Origin: OriginLocal, Name: "g.json"},
		Candidate{Line: append([]byte(nil), good[:len(good)-1]...), Origin: OriginNote, Name: "note"},
	)
	if !v.Verified || v.Receipt.Origin != OriginLocal {
		t.Fatalf("verdict %+v", v)
	}
	want := []Problem{{Origin: "junk.json", Reason: ReasonMalformed}, {Origin: "note", Reason: ReasonMalformed}, {Origin: "u.json", Reason: ReasonUnknownSigner}}
	if len(problems) != len(want) {
		t.Fatalf("problems %+v", problems)
	}
	for i := range want {
		if problems[i] != want[i] {
			t.Errorf("problem %d = %+v, want %+v", i, problems[i], want[i])
		}
	}
}

// A well signed envelope of another payload type is not a receipt, not a bad
// signature.
func TestEvaluateOtherPayloadType(t *testing.T) {
	key := testKey(t, 1)
	st, err := Statement(passing("p", t0))
	if err != nil {
		t.Fatal(err)
	}
	const other = "text/plain"
	line, _ := json.Marshal(Envelope{
		PayloadType: other,
		Payload:     base64.StdEncoding.EncodeToString(st),
		Signatures: []Signature{{
			KeyID: KeyID(key.Public().(ed25519.PublicKey)),
			Sig:   base64.StdEncoding.EncodeToString(ed25519.Sign(key, PAE(other, st))),
		}},
	})
	v, problems := evaluate(t, local(line))
	if v.Verified || v.Reason != ReasonNoReceipt {
		t.Fatalf("verdict %+v", v)
	}
	if len(problems) != 1 || problems[0].Reason != ReasonMalformed {
		t.Fatalf("problems %+v", problems)
	}
}

func TestEvaluateSeveralTasks(t *testing.T) {
	key := testKey(t, 1)
	lint := passing("l", t0)
	lint.Task = "lint"
	verdicts, _ := Evaluate([]Candidate{local(signed(t, key, lint))}, []ed25519.PublicKey{key.Public().(ed25519.PublicKey)}, "Runefile", []string{"test", "lint", "test"}, vCommit, vTree)
	if len(verdicts) != 2 || verdicts[0].Task != "test" || verdicts[0].Verified || verdicts[1].Task != "lint" || !verdicts[1].Verified {
		t.Fatalf("verdicts %+v", verdicts)
	}
}

func TestJudgeAll(t *testing.T) {
	key := testKey(t, 1)
	trusted := []ed25519.PublicKey{key.Public().(ed25519.PublicKey)}
	test := signed(t, key, passing("a", t0))
	lint := passing("b", t0)
	lint.Task = "lint"
	other := passing("c", t0)
	other.Project = "sub/Runefile"
	cands := []Candidate{
		local(test),
		{Line: append([]byte(" "), test...), Origin: OriginNote, Name: "note"}, // the same line again
		local(signed(t, key, lint)),
		local(signed(t, key, other)),
		{Line: []byte("not a receipt"), Origin: OriginLocal, Name: "bad.json"},
	}
	byTask, problems := judgeAll(cands, trusted, "Runefile", vCommit, vTree)
	if len(byTask["test"]) != 1 || len(byTask["lint"]) != 1 || len(byTask) != 2 {
		t.Fatalf("grouped %+v", byTask)
	}
	if byTask["test"][0].reason != "" || !byTask["test"][0].matches {
		t.Fatalf("test judged %+v", byTask["test"][0])
	}
	if len(problems) != 1 || problems[0].Origin != "bad.json" || problems[0].Reason != ReasonMalformed {
		t.Fatalf("problems %+v", problems)
	}
}

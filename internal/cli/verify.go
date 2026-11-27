package cli

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rune-task-runner/rune/internal/config"
	"github.com/rune-task-runner/rune/internal/receipt"
)

// `rune verify` checks receipts against a revision, and `rune receipt`
// prints the signing key and attaches receipts to a commit's note (spec
// 060). internal/receipt reads, signs and judges; this file finds the
// project and the keys, and prints.

// VerifyFlags are the flags of `rune verify`.
type VerifyFlags struct {
	Keys []string // --key, repeatable: files of trusted public keys
	JSON bool     // --json
}

// VerifyTaskNote prints the note about a task named verify, when the
// Runefile found from the working directory has one.
func VerifyTaskNote(opts Options) { commandTaskNote(opts, "verify") }

// commandTaskNote prints the note about a task the built-in command name
// hides on the command line.
func commandTaskNote(opts Options, name string) {
	runefile, err := config.Resolve(opts.File, opts.Cwd)
	if err == nil && hasTask(runefile, name) {
		fmt.Fprintf(opts.Stderr, "note: %q is also a task of this Runefile; run it with: rune -- %s\n", name, name)
	}
}

// receiptProjectRepo finds the project's Runefile, its repository, the
// Runefile's directory's .rune relative to the top, and the project as a
// receipt names it: the Runefile relative to the top. Every failure is a
// usage error: the command cannot start.
func receiptProjectRepo(ctx context.Context, opts Options) (root string, repo *receipt.Repo, exclude, project string, err error) {
	runefile, err := config.Resolve(opts.File, opts.Cwd)
	if err != nil {
		return "", nil, "", "", &UsageError{Err: err}
	}
	root = filepath.Dir(runefile)
	repo, err = receipt.OpenRepo(ctx, root)
	if err != nil {
		return "", nil, "", "", &UsageError{Err: err}
	}
	prefix := ""
	if rel, err := filepath.Rel(repo.Top, resolvedPath(root)); err == nil && rel != "." {
		prefix = filepath.ToSlash(rel) + "/"
	}
	return root, repo, prefix + ".rune", prefix + filepath.Base(runefile), nil
}

// trustedKeys are the keys whose receipts count: the key this machine signs
// with, if it has one, and the public keys in files. No key is created.
func trustedKeys(opts Options, files []string) ([]ed25519.PublicKey, error) {
	var keys []ed25519.PublicKey
	own, _, _, err := signingKey(opts, false)
	if errors.Is(err, errNoStateDir) {
		// Without a state directory this machine has no local key.
		own, err = nil, nil
	}
	if err != nil {
		return nil, &UsageError{Err: err}
	}
	if own != nil {
		keys = append(keys, own.Public().(ed25519.PublicKey))
	}
	pubs, err := publicKeyFiles("--key", files)
	if err != nil {
		return nil, err
	}
	return append(keys, pubs...), nil
}

// publicKeyFiles reads the public keys in files, which flag named. A file
// that cannot be read or holds no key is a usage error.
func publicKeyFiles(flag string, files []string) ([]ed25519.PublicKey, error) {
	var keys []ed25519.PublicKey
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, &UsageError{Err: fmt.Errorf("%s %s: %w", flag, file, err)}
		}
		pubs, err := receipt.ParsePublicKeys(data)
		if err != nil {
			return nil, &UsageError{Err: fmt.Errorf("%s %s: %w", flag, file, err)}
		}
		keys = append(keys, pubs...)
	}
	return keys, nil
}

// receiptCandidates are the project's receipts, then those in the note of
// commit.
func receiptCandidates(ctx context.Context, root string, repo *receipt.Repo, commit string) ([]receipt.Candidate, error) {
	stored, err := receipt.List(receipt.Dir(root))
	if err != nil {
		return nil, err
	}
	var cands []receipt.Candidate
	for _, s := range stored {
		cands = append(cands, receipt.Candidate{Line: s.Data, Origin: receipt.OriginLocal, Name: ".rune/receipts/" + s.Name})
	}
	lines, err := repo.ReadNote(ctx, commit)
	if err != nil {
		return nil, err
	}
	for _, line := range lines {
		cands = append(cands, receipt.Candidate{Line: line, Origin: receipt.OriginNote, Name: receipt.NotesRef})
	}
	return cands, nil
}

// verifyReport is the JSON document of `rune verify --json`.
type verifyReport struct {
	Revision    string            `json:"revision"`
	Commit      string            `json:"commit"`
	Tree        string            `json:"tree"`
	Verified    bool              `json:"verified"`
	Tasks       []receipt.Verdict `json:"tasks"`
	WorkingTree workingTreeReport `json:"workingTree"`
	Problems    []receipt.Problem `json:"problems,omitempty"`
}

type workingTreeReport struct {
	Matches bool           `json:"matches"`
	Outputs []outputReport `json:"outputs,omitempty"`
}

type outputReport struct {
	Task  string `json:"task"`
	Path  string `json:"path"`
	State string `json:"state"`
}

// Verify reports, for each task named after the revision, whether a trusted
// receipt shows it passed on exactly that revision's files. It runs nothing
// and writes nothing.
func Verify(opts Options, flags VerifyFlags, args []string) error {
	// First, so that `rune verify` typed to run a task named verify says how.
	VerifyTaskNote(opts)
	if len(args) < 2 {
		return usagef("rune verify needs a revision and the tasks that need evidence: rune verify REVISION TASK...")
	}
	ctx := opts.ctx()
	root, repo, exclude, project, err := receiptProjectRepo(ctx, opts)
	if err != nil {
		return err
	}
	commit, tree, err := repo.Resolve(ctx, args[0])
	if err != nil {
		return &UsageError{Err: err}
	}
	trusted, err := trustedKeys(opts, flags.Keys)
	if err != nil {
		return err
	}
	cands, err := receiptCandidates(ctx, root, repo, commit)
	if err != nil {
		return &CommandFailure{Err: fmt.Errorf("verify: %w", err)}
	}
	verdicts, problems := receipt.Evaluate(cands, trusted, project, args[1:], commit, tree)
	matches, err := repo.WorktreeMatches(ctx, commit, exclude)
	if err != nil {
		return &CommandFailure{Err: fmt.Errorf("verify: %w", err)}
	}
	report := verifyReport{
		Revision: args[0], Commit: commit, Tree: tree, Verified: true, Tasks: verdicts,
		WorkingTree: workingTreeReport{Matches: matches}, Problems: problems,
	}
	failed := 0
	for _, v := range verdicts {
		if !v.Verified {
			report.Verified = false
			failed++
			continue
		}
		var manifest []byte
		if out := v.Receipt.Outputs; out != nil && out.Manifest != "" {
			manifest, _ = receipt.ReadManifest(receipt.Dir(root), out.Manifest)
		}
		for _, s := range receipt.CompareOutputs(root, v.Receipt.Outputs, manifest) {
			report.WorkingTree.Outputs = append(report.WorkingTree.Outputs, outputReport{Task: v.Task, Path: s.Path, State: s.State})
		}
	}
	if flags.JSON {
		enc := json.NewEncoder(opts.Stdout)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
	} else {
		printVerify(opts, report)
	}
	if failed > 0 {
		return &CommandFailure{Err: fmt.Errorf("verify: %d of %d tasks not verified at %s", failed, len(verdicts), shortCommit(commit))}
	}
	return nil
}

func shortCommit(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}

func printVerify(opts Options, r verifyReport) {
	at := shortCommit(r.Commit)
	for _, v := range r.Tasks {
		switch {
		case v.Verified && v.Receipt.Exact:
			fmt.Fprintf(opts.Stdout, "%s: passed at %s (%s)\n", v.Task, at, receiptDetail(v.Receipt))
		case v.Verified:
			fmt.Fprintf(opts.Stdout, "%s: passed on a snapshot identical to %s (%s, %s)\n", v.Task, at, ranAt(v.Receipt), receiptDetail(v.Receipt))
		case v.Receipt == nil:
			fmt.Fprintf(opts.Stdout, "%s: not verified: no receipt for %s\n", v.Task, at)
		default:
			fmt.Fprintf(opts.Stdout, "%s: not verified: %s (receipt %s)\n", v.Task, reasonText(v, at), v.Receipt.ID)
		}
	}
	if r.WorkingTree.Matches {
		fmt.Fprintf(opts.Stdout, "working tree: matches %s\n", at)
	} else {
		fmt.Fprintf(opts.Stdout, "working tree: differs from %s\n", at)
	}
	var tasks []string
	for _, o := range r.WorkingTree.Outputs {
		if !slices.Contains(tasks, o.Task) {
			tasks = append(tasks, o.Task)
		}
	}
	for _, task := range tasks {
		total, odd := 0, 0
		for _, o := range r.WorkingTree.Outputs {
			if o.Task != task {
				continue
			}
			total++
			if o.State != receipt.OutputMatch {
				odd++
				fmt.Fprintf(opts.Stdout, "outputs of %s: %s %s\n", task, o.Path, o.State)
			}
		}
		if odd == 0 {
			fmt.Fprintf(opts.Stdout, "outputs of %s: all %d match\n", task, total)
		}
	}
	for _, p := range r.Problems {
		fmt.Fprintf(opts.Stdout, "problem: %s: %s\n", p.Origin, p.Reason)
	}
}

// ranAt says what an accepted receipt that is not exact ran on.
func ranAt(s *receipt.Summary) string {
	switch {
	case s.Commit == "":
		return "ran before the first commit"
	case s.Dirty:
		return "ran at " + shortCommit(s.Commit) + " with changes"
	}
	return "ran at " + shortCommit(s.Commit)
}

// receiptDetail shows a receipt's arguments, where it says it came from, and
// the key that signed it: only the key is authenticated.
func receiptDetail(s *receipt.Summary) string {
	var parts []string
	if len(s.Arguments) > 0 {
		args := make([]string, len(s.Arguments))
		for i, a := range s.Arguments {
			args[i] = a.Name + "=" + strings.Join(a.Values, ",")
		}
		parts = append(parts, "with "+strings.Join(args, " "))
	}
	parts = append(parts, s.Channel)
	if c := s.Claims; c != nil {
		if c.Client != nil {
			parts = append(parts, strings.TrimSpace("client "+c.Client.Name+" "+c.Client.Version))
		}
		if c.Auth != "" {
			parts = append(parts, "auth "+c.Auth)
		}
		for _, name := range slices.Sorted(maps.Keys(c.Env)) {
			parts = append(parts, name+"="+c.Env[name])
		}
	}
	return strings.Join(append(parts, "signed by "+s.Signer), ", ")
}

// reasonText says why a receipt is not evidence (contracts/cli.md).
func reasonText(v receipt.Verdict, at string) string {
	s := v.Receipt
	switch v.Reason {
	case receipt.ReasonUnknownSigner:
		return "signed by an untrusted key " + s.Signer
	case receipt.ReasonBadSignature:
		return "the signature does not match the receipt"
	case receipt.ReasonUnsupported:
		return "its format version or status is not supported by this rune"
	case receipt.ReasonFailed:
		if s.ExitCode != nil {
			return fmt.Sprintf("the run failed with exit code %d", *s.ExitCode)
		}
		return "the run failed"
	case receipt.ReasonInterrupted:
		return "the run was interrupted"
	case receipt.ReasonRefused:
		return "the body did not run: it was refused"
	case receipt.ReasonCached:
		if s.Reuses != "" {
			return "a cache hit, not a run (reuses receipt " + s.Reuses + ")"
		}
		return "a cache hit, not a run"
	case receipt.ReasonIncomplete:
		return "the run was not fully observed"
	case receipt.ReasonNoSnapshot:
		return "the run was not in a git repository"
	case receipt.ReasonChangedDuringRun:
		return "files changed during the run"
	case receipt.ReasonDifferentSnap:
		return "the run was on other files than " + at
	case receipt.ReasonUncoveredRunefile:
		return "the Runefile or an import is outside the repository or ignored: " + s.Uncovered
	case receipt.ReasonUncoveredInputs:
		return fmt.Sprintf("%d declared inputs are ignored by git", s.UncoveredInputs)
	}
	return v.Reason
}

// AttachFlags are the flags of `rune receipt attach`.
type AttachFlags struct {
	Keys []string // --key, repeatable: files of trusted public keys
}

// ReceiptTaskNote prints the note about a task named receipt, when the
// Runefile found from the working directory has one.
func ReceiptTaskNote(opts Options) { commandTaskNote(opts, "receipt") }

// ReceiptKey prints the public key of the signer receipts are signed with,
// creating the local key when there is none and no CI key is set.
func ReceiptKey(opts Options) error {
	ReceiptTaskNote(opts)
	key, source, created, err := signingKey(opts, true)
	if err != nil {
		return &UsageError{Err: err}
	}
	if created {
		printKeyCreated(opts.Stderr, key)
	}
	pub := key.Public().(ed25519.PublicKey)
	if _, err := opts.Stdout.Write(receipt.PublicPEM(pub)); err != nil {
		return err
	}
	fmt.Fprintf(opts.Stderr, "key %s (%s)\n", receipt.KeyID(pub), source)
	return nil
}

// ReceiptAttach adds the project's receipts bound to a revision, those whose
// snapshots before and after are its tree and that a trusted key signed, to
// the note of its commit (FR-037).
func ReceiptAttach(opts Options, flags AttachFlags, args []string) error {
	ReceiptTaskNote(opts)
	if len(args) > 1 {
		return usagef("rune receipt attach takes one revision: rune receipt attach [REVISION]")
	}
	rev := "HEAD"
	if len(args) == 1 {
		rev = args[0]
	}
	ctx := opts.ctx()
	root, repo, _, _, err := receiptProjectRepo(ctx, opts)
	if err != nil {
		return err
	}
	commit, tree, err := repo.Resolve(ctx, rev)
	if err != nil {
		return &UsageError{Err: err}
	}
	trusted, err := trustedKeys(opts, flags.Keys)
	if err != nil {
		return err
	}
	stored, err := receipt.List(receipt.Dir(root))
	if err != nil {
		return &CommandFailure{Err: fmt.Errorf("receipt attach: %w", err)}
	}
	var lines [][]byte
	for _, s := range stored {
		payload, _, err := receipt.Open(s.Data, trusted)
		if err != nil {
			continue
		}
		rc, err := receipt.ParseStatement(payload)
		if err != nil || rc.Source == nil || rc.Source.Before != tree || rc.Source.After != tree {
			continue
		}
		lines = append(lines, s.Data)
	}
	at := shortCommit(commit)
	if len(lines) == 0 {
		return &CommandFailure{Err: fmt.Errorf("no receipt for %s to attach", at)}
	}
	added, present, err := repo.AttachNote(ctx, commit, lines)
	if err != nil {
		return &CommandFailure{Err: fmt.Errorf("receipt attach: %w", err)}
	}
	fmt.Fprintf(opts.Stderr, "attached %d receipts to %s (%d already there) in %s\n", added, at, present, receipt.NotesRef)
	return nil
}

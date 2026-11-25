# Contract: receipt format version 1

## Files

| Path | Content |
|---|---|
| `ROOT/.rune/receipts/ID.json` | one envelope, one line, ending in a newline |
| `ROOT/.rune/receipts/ID.outputs.json` | output manifest when a receipt has more than 64 output files |
| `ROOT/.rune/receipts/.gitignore` | `*` |

ROOT is the directory of the Runefile. ID is `YYYYMMDDTHHMMSSZ-` and 16 lowercase hex digits.
Files are created once and never replaced.

## Envelope (DSSE)

```json
{"payloadType":"application/vnd.in-toto+json","payload":"BASE64","signatures":[{"keyid":"SHA256:...","sig":"BASE64"}]}
```

- `payload`: standard base64 (with padding) of the statement bytes.
- `sig`: standard base64 of the Ed25519 signature over
  `"DSSEv1" + " " + len(payloadType) + " " + payloadType + " " + len(payload) + " " + payload`,
  lengths in decimal ASCII, `payload` the raw statement bytes.
- `keyid`: `SHA256:` + unpadded standard base64 of SHA-256 of the signer's PKIX DER public key.
  A hint only; the key that verifies the signature is the signer.

## Statement (in-toto v1)

```json
{
  "_type": "https://in-toto.io/Statement/v1",
  "subject": [{"name": "worktree", "digest": {"gitTree": "<source.before>"}}],
  "predicateType": "https://rune-task-runner.github.io/rune/receipt/v1",
  "predicate": { "...": "the receipt below" }
}
```

Outside a repository the subject is `{"name": "<project>", "digest": {"sha256": "<sha256 of the Runefile>"}}`.

## Predicate (the receipt)

```json
{
  "version": 1,
  "id": "20261125T101500Z-1a2b3c4d5e6f7a8b",
  "invocation": "9f8e7d6c5b4a3921",
  "parentInvocation": "0123456789abcdef",
  "task": "api::test",
  "arguments": [{"name": "pkg", "values": ["./..."]}],
  "argumentsRedacted": true,
  "role": "task",
  "related": {
    "dependencies": ["20261125T101459Z-aaaaaaaaaaaaaaaa"],
    "follows": "20261125T101458Z-bbbbbbbbbbbbbbbb",
    "failureOf": "20261125T101457Z-cccccccccccccccc",
    "reuses": "20261124T090000Z-dddddddddddddddd"
  },
  "status": "passed",
  "exitCode": 0,
  "startedAt": "2026-11-25T10:15:00.000000001Z",
  "finishedAt": "2026-11-25T10:15:00.412000001Z",
  "durationMs": 412,
  "rune": {"version": "0.12.0", "commit": "abc1234", "os": "linux", "arch": "amd64"},
  "executor": "sh",
  "definition": "<hex sha256>",
  "project": "Runefile",
  "runefiles": [{"path": "Runefile", "sha256": "<hex>", "covered": true}],
  "source": {"commit": "<hex>", "dirty": false, "before": "<tree>", "after": "<tree>"},
  "uncoveredInputs": ["vendor/gen.go"],
  "outputs": {
    "count": 1,
    "digest": "<hex sha256 of the canonical list>",
    "files": [{"path": "dist/app", "size": 1048576, "sha256": "<hex>"}]
  },
  "channel": "mcp",
  "claims": {
    "client": {"name": "claude-code", "version": "2.1.0"},
    "auth": "process",
    "env": {"CI": "true", "GITHUB_RUN_ID": "123"}
  }
}
```

Rules:

- Optional fields are omitted when empty: `parentInvocation`, `argumentsRedacted`, `related` and
  each of its members, `exitCode`, `source` (outside a repository), `source.commit` (no commit
  yet), `uncoveredInputs`, `outputs`, `claims` and each of its members.
- `status` is one of `passed`, `failed`, `interrupted`, `refused`, `cached`, `incomplete`.
- `role` is `task` or `failure-hook`. A post-hook or dependency has role `task` and its relation
  in `related`.
- `channel` is `cli`, `ci-run`, `mcp` or `agent`. `claims` are what was reported or found in the
  environment, never authenticated.
- `outputs.files` holds at most 64 entries. With more, `files` is absent and `manifest` names
  `ID.outputs.json`, whose content is the JSON array of `{path, size, sha256}` sorted by path;
  `digest` is the SHA-256 of that file's bytes. With 64 or fewer, `digest` is the SHA-256 of the
  same canonical array serialization of `files`.
- Paths use forward slashes. `runefiles` paths are relative to the repository top; output and
  input paths are relative to ROOT.
- No field holds task output, an environment value other than the listed claims, a variable
  value, an error message, or a digest of any of them.

## Notes

`refs/notes/rune-receipts`, one note per commit: envelopes as in the files, one per line, sorted
bytewise, no duplicates, final newline. Readers skip blank lines.

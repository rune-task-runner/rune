# Contract: the plan store (`internal/planstore`)

## Location and modes

`<runefile dir>/.rune/plans/`. Directories are created with mode `0700`, files with `0600` or
`0400`. `.rune` and `.rune/plans` must be real directories: a symbolic link is refused. On
POSIX hosts `.rune/plans` must belong to the current user. `.rune/plans/.gitignore` holds `*`.
On Windows the modes do not restrict access.

The store is created when the first plan is stored. A lookup on a missing store finds no plan
and creates nothing.

## API

```go
type Store struct {
    Now func() time.Time // nil means time.Now; every time the store writes comes from it
}

func Open(root string) *Store                        // no file-system access
func (s *Store) Save(in SaveInput) (rec Record, replaced []Record, err error)
func (s *Store) Latest(key string) (Record, State, bool, error)
func (s *Store) LastAttempt(key, exceptID string) (AttemptInfo, bool)
func (s *Store) Verify(rec Record) error             // artifact and summary against the record
func (s *Store) Summary(rec Record) (string, error)  // read through the same verified open
func (s *Store) Claim(rec Record, applyTask, surface string) (*Attempt, error)
func (a *Attempt) Finish(exit int) (tampered bool, err error)
func (s *Store) Prune()
func (s *Store) Log(e Event) error
```

`key` identifies the target: the engine computes it from the Runefile's path, the plan task
and the effective arguments ([../data-model.md](../data-model.md)). The store only compares it.

`SaveInput` carries the two source paths, the record fields the engine computed, and the
limits. `Save` opens each source without following a final symbolic link, requires a regular
file within the limit, and streams it into the store while hashing.

Typed errors:

- `ErrClaimed`, `ErrReplaced`, `ErrExpired` from `Claim`.
- `*ArtifactError{What, Detail}` from `Verify`, `Summary` and `Claim`. `What` is `artifact` or
  `summary`. `Detail` is `is missing`, `is not a regular file` or
  `does not have the recorded digest`.
- `*SourceError{Path, Detail}` from `Save`, with `is missing`, `is not a regular file`,
  `is a symbolic link` or `is larger than <limit>`.
- `*UnusableError` from any function, when the store directory is a link, is not a directory,
  or belongs to another user.

## Order of records

Records of one key are ordered by `created`, in nanoseconds, then by id. The newest is the last
in that order. `Save` sets `created` to its clock, or to just after the newest record of the key
it can see when the clock has not passed that one, so a plan stored later is the newer one also
after the clock stepped back. `expires` is counted from the clock, not from that adjusted time. Only the newest can be applied: `Latest` returns that one and no other. `Save`
and `Prune` mark every older record of the key that has no claim as replaced. A record never
marks a newer one, so concurrent `Save` calls cannot replace each other.

## State

Derived from the files of a record's directory, first match wins:

| State | Condition |
|---|---|
| `applied`, `failed` | `outcome.json` exists and says so |
| `unfinished` | `claim` exists and `outcome.json` does not, or cannot be read |
| `replaced` | `replaced` exists |
| `expired` | now is after `expires` |
| `ready` | otherwise |

A `record.json` that cannot be read makes its directory invisible to `Latest`.

## Claim

1. The state must be `ready`: otherwise `ErrClaimed` (applied, failed, unfinished),
   `ErrReplaced` or `ErrExpired`.
2. The artifact is read through one handle, opened without following a link, and copied to
   `<id>/attempt-<aid>/plan` while it is hashed. A digest that differs, or a file that is
   missing or not regular, removes the copy and returns an `*ArtifactError`.
3. `<id>/claim` is created with `O_CREATE|O_EXCL`, holding the attempt as JSON. If it exists,
   the copy is removed and the result is `ErrClaimed`.

A `Claim` that fails changes no state: it writes no outcome and leaves no claim. Exactly one
caller gets past step 3, across goroutines and processes.

## Guarantees

- The bytes at `Attempt.Path` have the record's digest when `Claim` returns.
- After `Finish`, the artifact, the summary and the private copy are gone.
- `Finish` hashes the copy first. A digest that differs gives `tampered` true and the state
  `failed`, whatever the exit code.
- No function writes plan or summary bytes anywhere but the artifact, the summary and the
  private copy.

## Prune

- Removes the artifact and summary of every record that is replaced, expired, applied or
  failed.
- Leaves an unfinished record alone while its claim is younger than 24 hours: an apply may
  still be reading the copy. After that it removes the artifact, the summary and the copy, and
  keeps the record.
- Removes a record's directory seven days after the latest of its creation, its claim and its
  outcome, except an unfinished record whose claim is younger than 24 hours.
- Removes a plan directory that has no readable record once it is 24 hours old. A `Save` writes
  its record last, so such a directory is what a killed `Save` leaves behind. A younger one may
  be a `Save` that is still copying.

## Limits

| What | Limit |
|---|---|
| Artifact | 256 MiB |
| Summary | 1 MiB |
| Record retention | 7 days after the record's last event |
| Unfinished attempt's bytes | 24 hours after the claim |
| Journal | 1 MiB, then the older half of the lines is dropped |
| Default expiry | 1 hour |

## Journal

`.rune/plans/journal.jsonl`, one JSON object per line, mode `0600`. `Log` appends under a lock
file (`journal.lock`, created exclusively, retried for up to two seconds), so two processes
neither interleave a line nor lose one while the file is cut. A lock older than ten seconds
belongs to a writer that died: it is taken away by moving it aside, which only one waiter can
do, and a lock that turns out to be fresh is put back. `Log` returns an error when it cannot write. The engine then prints a warning and goes
on. Fields and events are in [../data-model.md](../data-model.md). Example:

```json
{"time":"2026-11-17T10:04:11Z","event":"plan-created","surface":"mcp","planTask":"plan","plan":"7f3c9a1e52b84d06","digest":"9b1f...","args":"env=\"prod\""}
{"time":"2026-11-17T10:05:02Z","event":"approval-requested","surface":"mcp","planTask":"plan","plan":"7f3c9a1e52b84d06","digest":"9b1f...","applyTask":"apply","args":"env=\"prod\""}
{"time":"2026-11-17T10:05:09Z","event":"approval-given","surface":"mcp","planTask":"plan","plan":"7f3c9a1e52b84d06","digest":"9b1f...","applyTask":"apply","args":"env=\"prod\""}
{"time":"2026-11-17T10:05:09Z","event":"apply-started","surface":"mcp","planTask":"plan","plan":"7f3c9a1e52b84d06","digest":"9b1f...","applyTask":"apply","args":"env=\"prod\"","attempt":"c41d0e7a9b3355f2"}
{"time":"2026-11-17T10:05:31Z","event":"apply-finished","surface":"mcp","planTask":"plan","plan":"7f3c9a1e52b84d06","digest":"9b1f...","applyTask":"apply","args":"env=\"prod\"","attempt":"c41d0e7a9b3355f2","exit":0,"state":"applied"}
```

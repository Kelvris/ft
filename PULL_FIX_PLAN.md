# `ft pull` — Findings & Fix Plan (P0 → P2)

> **Scope**: every confirmed defect in `ft pull`, with file/line locations, reproduction evidence,
> and the recommended fix — designed to make `ft pull` behave like **`git pull`**
> (3-way comparison, fast-forward by default, refuse to clobber, real safety net).
>
> **Verified against**: `v1.10.4` (commit `3475627`), Go 1.26.3.
> **Method**: built the binary and ran it against a local FTP server (`pyftpdlib` on
> `127.0.0.1:2121`) with two simulated machines (`cli`, `cli2`), reproducing every finding below.
> Reproduction transcripts in this document are real output, not speculation.
>
> **Repo state**: `go build ./... && go vet ./... && go test ./...` all pass today.
> The `cmd` package has **zero tests** — which is exactly why none of this is caught.
> (Note: `cmd` tests are listed as absent by `llms.txt:67`.)

---

## 0. TL;DR — severity table

| ID | Priority | Finding | Primary location |
|----|----------|---------|------------------|
| **F1** | **P0** | Pull silently overwrites uncommitted local edits (no conflict detection) | `cmd/pull.go:174-192`, `cmd/pull.go:211-222` |
| **F2** | **P0** | Pull silently **absorbs new local files into the index** → they can never be pushed again | `cmd/pull.go:224-240` |
| **F3** | **P0** | `--backup` safety net is fake: captures only an index, never file content; `revert` cannot recover the lost work | `version/version.go:85-109`, `version/version.go:168-217` |
| **F4** | **P0** | Pull never looks at the real remote filesystem → files changed **directly on the server** are invisible (README promise broken) | `cmd/pull.go:118`, `cmd/pull.go:127-193` |
| **F5** | **P0** | Remote deletions never propagate; locally-deleted files get resurrected; `ft status` then reports "working tree clean" | `cmd/pull.go:127`, `cmd/pull.go:174-176` |
| **F6** | **P1** | One failing file aborts the whole pull, after pointless retries, with a doubled error and a full usage dump | `cmd/pull.go:219-221`, `cmd/pull.go:253-266`, `cmd/root.go:28-35` |
| **F7** | **P1** | Downloaded content is never hash-verified against anything (no integrity check) | `cmd/pull.go:211-222` |
| **F8** | **P1** | Pull never converges: it re-downloads the same file on every run after a stale remote index | `cmd/pull.go:224-240` + missing index sync |
| **F9** | **P1** | `ft diff` shares F4's blind spot (also index-only) | `cmd/diff.go:65-109` |
| **F10** | **P2** | `--include '*.php'` never matches `admin/index.php` — silently matches nothing | `cmd/pull.go:145-156`, `cmd/push.go:358-367` |
| **F11** | **P2** | No-op pull still rehashes the whole tree; double messaging; `-q` ignored in `--dry-run` | `cmd/pull.go:195-199`, `cmd/pull.go:205-207`, `cmd/pull.go:224-240` |
| **F12** | **P2** | Downloads are serial (push has `--jobs`); no byte-level progress | `cmd/pull.go:211-222` vs `cmd/push.go:161-163,184-221` |
| **F13** | **P2** | Selective pull writes the *remote machine's* mtime into the local index | `cmd/pull.go:228-236` |

---

## 1. How `ft pull` works today (current architecture)

```
                      .ft/index.json (local, "base")
                              │
   ft pull ───────────────────┼──────────────────────────────┐
                              ▼                              │
        transport.FetchIndexFromRemote()  (cmd/pull.go:118)  │
        → reads ONLY  <remote>/.ft/index.json                │
                              │                              │
                              ▼                              │
        for each path in remoteIdx.Files  (cmd/pull.go:127)  │
            compare  local disk file  vs  remote index entry │
            (cmd/pull.go:174-192)                            │
                              │                              │
                    needsDownload? ── yes ──► t.Download()    │
                              │                              │
                              ▼                              │
        index.BuildIndex(".")   (cmd/pull.go:224-227)  ◄─────┘
        → rebuild local index from the *entire working tree*
                              │
                              ▼
        newIdx.Save()          (cmd/pull.go:238-240)
        (remote index is NEVER updated by pull)
```

**The core defect is a 2-way comparison.** Git compares three things:

| side | git | `ft` today |
|------|-----|-----------|
| **base** — last known sync point | commit / merge base | `.ft/index.json` (loaded, but **never used** by pull) |
| **local** — working tree | worktree content | local file ✅ used |
| **remote** — upstream | `origin/<branch>` ref | remote `.ft/index.json` ⚠️ used *instead of* the real remote files |

`ft` has a base (`.ft/index.json`) and never consults it during pull. It also treats the remote
**index** as if it were the remote **filesystem**. Every P0 finding falls out of those two facts.

### Code map (current)

| Location | What it does today |
|----------|--------------------|
| `cmd/pull.go:30-251` | `pullCmd.RunE` — monolithic: arg parsing, connect, detect, download, re-index |
| `cmd/pull.go:118-121` | fetch remote index; errors out if remote has none |
| `cmd/pull.go:127-193` | the "what changed" loop (2-way compare) |
| `cmd/pull.go:174-192` | **decision rule**: `local != remote-index-entry` ⇒ download |
| `cmd/pull.go:195-199` | prints `already up to date` (but does not return) |
| `cmd/pull.go:201-209` | `--dry-run` |
| `cmd/pull.go:211-222` | serial download loop + `retryDownload(..., 3)` |
| `cmd/pull.go:224-240` | index rebuild: full `BuildIndex` (no pathspec) or selective merge (pathspec) |
| `cmd/pull.go:253-266` | `retryDownload` — fixed 3 attempts + 1s/2s sleeps, even for permanent errors |
| `cmd/pull.go:302-310` | flags: `-p`, `-n`, `-q`, `--backup`, `--include`, `--exclude` |
| `transport/transport.go:17-29` | `Transport` interface (no `Stat`) |
| `transport/transport.go:42-66` | `SyncIndexToRemote` / `FetchIndexFromRemote` |
| `index/index.go:18-22` | `FileEntry{Hash, Mtime, Size}` — `Mtime` is the **pusher's local** mtime |
| `index/index.go:110-212` | `DetectChanges` — the 2-way local compare used by `status`/`push` |

---

## 2. Git ↔ `ft` mapping (the target model)

The fix philosophy in one line: **`ft pull` should behave like `git pull` on a fast-forwardable
branch — and like git, it must refuse to destroy uncommitted work.**

| Git concept | Git behaviour | Proposed `ft` equivalent | Status today |
|---|---|---|---|
| merge base | commit both sides descend from | `.ft/index.json` entry (per path) | exists, **unused by pull** |
| working tree | your uncommitted edits | local file hash | used, but **overwritten** (F1) |
| `origin/<branch>` remote-tracking ref | local copy of upstream state | **new** `.ft/remote.json` | does not exist (F4/F8) |
| `git pull` (fast-forward) | update only when worktree matches base | download only when `local == base` | ❌ downloads whenever `local != remote` |
| conflict | refuse, mark, exit 1, change nothing | conflict list, exit 1, **no writes** | ❌ silent overwrite |
| `git pull --ff-only` | default safe behaviour | default behaviour | n/a |
| `git checkout -f` / `git reset --hard` | take upstream over local | new `--force` flag | ❌ always force |
| `git stash` / auto version | safety net with **content** | auto snapshot of files about to be overwritten | ❌ index-only, content never saved (F3) |
| files deleted upstream | removed if worktree clean | delete local file when `local == base` | ❌ never (F5) |
| local delete, upstream unchanged | stays deleted, shown by `status` | keep deleted, report it | ❌ resurrected (F5) |
| untracked files | untouched by pull | untouched, **not indexed** | ❌ absorbed into index (F2) |
| pathspec (`git pull -- path`) | restricts the operation | `ft pull origin <path>` / `--include` | ⚠️ glob broken (F10) |
| `git pull --dry-run` | preview only | `ft pull -n` | ✅ (minor output bugs, F11) |
| exit codes | 0 ok, 1 conflict, 128 fatal | 0 ok, **1 conflict**, **2 transfer error** | ❌ always 1 on any error + usage dump |

### The decision matrix (per path) — the heart of the fix

For each path, classify the three sides:

* **base** = entry in local `.ft/index.json` (last sync point)
* **local** = hash of the working-tree file, or *absent*
* **remote** = actual remote file state (from `Stat`/listing, reconciled in `.ft/remote.json`), or *absent*

| # | base | local | remote | Git analogue | `ft pull` action |
|---|------|-------|--------|--------------|------------------|
| 1 | any | — | — | up to date | nothing (do **not** touch index for this path) |
| 2 | present | `== base` | `!= base` | fast-forward | **download** (or, if remote absent, **delete local file**) |
| 3 | present | `!= base` | `== base` | local-only commit | **nothing to pull** — keep local (push will carry it) |
| 4 | present | `!= base` | `!= base` | diverged | if `local == remote` → just advance base; else **CONFLICT** |
| 5 | present | absent | `== base` | local `rm`, upstream unchanged | **leave deleted** (report as deleted; push propagates) |
| 6 | present | absent | `!= base` | modify/delete conflict | **CONFLICT** (report; `--force` restores remote) |
| 7 | absent | absent | present | new upstream file | **download** |
| 8 | absent | present | present | both created | `local == remote` → record base; else **CONFLICT** |
| 9 | absent | present | absent | untracked local | **leave alone, do not index** (fixes F2) |
| 10 | present | absent | absent | deleted both sides | drop from index only |

**Rules that follow from the matrix:**

* **Default is fast-forward only.** Any conflict ⇒ print the list, change **nothing**, exit `1`.
* **`--force`** is the only way to let remote win over local edits (git's `-f`), and it implies a backup.
* **A backup happens automatically whenever an overwrite will occur** (exactly like `push` auto-versioning).
* **Index updates are surgical**: only paths actually downloaded/deleted/verified are written.
  Never rebuild the index from the working tree during a pull (fixes F2, F8, F13).
* **Excluded/ignored paths are left completely untouched** — not downloaded, not deleted, not re-indexed.

---

## 3. Findings (with evidence + fix)

### 🔴 P0

---

#### F1 — Pull silently destroys uncommitted local edits

**Priority:** P0 · **Type:** data loss · **Locations:** `cmd/pull.go:174-192` (decision), `cmd/pull.go:211-222` (download), missing conflict handling

**What happens:** the decision rule is

```go
// cmd/pull.go:174-188
needsDownload := false
if !localExists {
    needsDownload = true
} else {
    if localInfo.Size() != remoteEntry.Size || localInfo.ModTime().Unix() != remoteEntry.Mtime {
        localHash, err := index.FileHash(localPath)
        ...
        if localHash != remoteEntry.Hash {
            needsDownload = true      // ← local edits are indistinguishable from "remote moved on"
        }
    }
}
```

The local index (base) is loaded nowhere in this loop, so *"I changed this locally"* and
*"the server changed this"* produce the identical outcome: **the server wins, silently**.

**Evidence (two machines, real run):**

```
$ printf 'MACHINE1 local work - uncommitted\n' > index.html     # machine 1
$ cd ../cli2 && printf 'machine2 change - already pushed\n' > index.html && ft push
$ cd ../cli
$ ft status
  modified:    index.html
203 files tracked, 1 changes: 0 added, 1 modified, 0 deleted

$ ft pull
remote has 203 tracked files
[1/1] downloading index.html
pulled 1 files from "origin"

$ cat index.html
machine2 change - already pushed          # ← MACHINE1's work is GONE
$ ft status
working tree clean (no changes since last sync)
```

No warning, no prompt, no `--force`, no backup (see F3), and `status` afterwards claims everything
is fine. The overwritten content exists nowhere on disk or in `.ft/`.

**Why this is wrong:** every git-like tool refuses this. Git would report a conflict and exit `1`
without touching the file.

**Fix (git-like):**

1. Introduce a pure planner (unit-testable, `cmd/pull_plan.go`):

```go
type side struct{ present bool; hash string }

type pullAction int
const (
    actNone pullAction = iota
    actDownload
    actDeleteLocal   // remote deleted it, local is clean
    actAdvanceBase   // local == remote, base stale (F8)
    actConflict
    actLeaveAlone    // untracked local / local-only change
)

type pullPlan struct {
    Path       string
    Action     pullAction
    LocalHash  string
    RemoteHash string
    BaseHash   string
    Reason     string   // human line for the conflict report
}

// planPull implements the matrix in §2. Pure function: no I/O.
func planPull(base, local, remote side, pathspec bool) pullPlan
```

2. `RunE` becomes: *fetch → stat/list remote → hash locals → plan → refuse-if-conflicts → backup → transfer → merge index*.
3. Conflicts print like git and abort **before any write**:

```
CONFLICT: index.html
  local  sha256:ab12… (modified since last sync, base sha256:9f00…)
  remote sha256:cd34…
hint: run `ft pull --force` to take the remote version (a backup is taken automatically),
      or `ft push` to send yours.
error: pull refused: 1 conflict (no files were changed)
```

4. New flag `--force` (short `-f`): lets rows 4/6 take the remote version; **implies backup**.

**Acceptance test:** machine 1 has an uncommitted edit + machine 2 pushed a different edit →
`ft pull` prints one conflict, exits `1`, `index.html` byte-identical to before, `ft status` still
shows `modified: index.html`.

---

#### F2 — Pull swallows new local files: they can never be pushed again

**Priority:** P0 · **Type:** silent data loss from the sync workflow · **Locations:** `cmd/pull.go:224-240` (esp. `224-227`)

**What happens:** after transferring, pull **rebuilds the entire local index from the working tree**:

```go
// cmd/pull.go:224-227
newIdx, err := index.BuildIndex(".", ignorePatterns)
```

Any local file that was never uploaded gets recorded as "synced", even though the server has never
seen it. Since `push` only uploads *changes vs the index* (`index.DetectChanges`,
`index/index.go:110-212`), that file becomes invisible forever.

**Evidence (real run):**

```
$ printf 'brand new local file\n' > brand-new.txt
$ ft status
  new file:    brand-new.txt
203 files tracked, 1 changes: 1 added, 0 modified, 0 deleted

$ ft pull
remote has 202 tracked files
already up to date
pulled 0 files from "origin"

$ ft status                        # ← file is now "synced"
working tree clean (no changes since last sync)
$ ft push
nothing to push (working tree clean)
$ ls /tmp/opencode/srv/brand-new.txt
ls: cannot access '/tmp/opencode/srv/brand-new.txt': No such file or directory
```

A pull that downloaded **zero** files changed the sync state of a file the server never received.
The user's file is stranded: `status` clean, `push` nothing, server missing it.

**Fix (git-like):** *pull must never stage/absorb untracked files* (git doesn't).

* Delete the full-rebuild branch entirely; make the **selective merge unconditional**:

```go
newIdx, err := index.Load()            // start from base
if err != nil { ... }
for _, p := range downloaded {         // only paths we actually fetched
    e, err := entryFromDisk(p)         // hash/size/mtime computed from the file we just wrote
    if err != nil { ... }
    newIdx.Files[p] = e
}
for _, p := range deletedLocal {       // only paths we intentionally removed (F5)
    delete(newIdx.Files, p)
}
// everything else keeps its existing entry untouched
```

* Note this also preserves entries for **locally-deleted** files (row 5 of the matrix), so
  `ft status` keeps reporting `deleted` and `ft push` can propagate it (matches git).
* `entryFromDisk` = reuse `index.FileHash` + `os.Stat`; do **not** copy the remote entry (see F13).

**Acceptance test:** create `new.txt` locally, run `ft pull`, then `ft status` → still
`new file: new.txt`, and `ft push` uploads it.

---

#### F3 — `--backup` is a fake safety net (and `revert` can't recover)

**Priority:** P0 · **Type:** data loss, false sense of safety · **Locations:**
`cmd/pull.go:92-101` (backup call), `version/version.go:85-109` (`Save` writes index only),
`version/version.go:168-217` (`Revert` never reads `files/`), `cmd/revert.go:17-20` (false help text),
`cmd/push.go:306-317` (`saveVersionFile` writes `files/` that nothing reads)

**What happens (three compounding defects):**

1. `version.Save(name)` (`version/version.go:85-109`) writes **only `index.json`** — no file content.
2. The index it snapshots is the *synced* index. By definition it does **not** contain your
   uncommitted local edit (that's what makes it uncommitted). So even the metadata doesn't record it.
3. `version.Revert` (`version/version.go:168-217`) never reads the `files/` directory at all:
   it skips when the current hash matches the snapshot (`:176-180`), otherwise prefers `deleted/`
   copies (`:182-201`), otherwise **downloads from the remote** (`:204-214`) — i.e. it re-fetches
   exactly the content that just clobbered you. Meanwhile `cmd/revert.go:18-19` claims
   *"If the version has file backups locally, those are used"* — untrue.
4. Separately, `files/` copies written by push (`cmd/push.go:306-317`) are **write-only dead
   weight**: `grep '"files"'` shows no reader anywhere in the codebase.

**Evidence (real run):**

```
$ printf 'PRECIOUS LOCAL WORK\n' > index.html
$ ft pull --backup
backed up current state as version "pre-pull-20260929-111127"
[1/1] downloading index.html
pulled 1 files from "origin"

$ find .ft/versions/pre-pull-20260929-111127 -type f
.ft/versions/pre-pull-20260929-111127/index.json          # ← index only, NO content

$ cat index.html
machine2 change - already pushed                           # ← work destroyed

$ ft revert pre-pull-20260929-111127
reverted to "pre-pull-20260929-111127": 0 restored, 203 skipped, 0 failed

$ grep -rl "PRECIOUS LOCAL WORK" .ft/ || echo "NOT FOUND - unrecoverable"
NOT FOUND - unrecoverable
```

`0 restored, 203 skipped` — the revert "succeeded" while recovering nothing.

**Fix (git-like — this is `git stash`, not `git tag`):**

1. Add a working-tree snapshot that copies **content**:

```go
// version/version.go
// SaveWorkingTree snapshots the *content* of the given paths (not just their hashes),
// so a later Revert can restore files that were never pushed.
func SaveWorkingTree(name string, paths []string) error {
    if err := Save(name); err != nil { return err }        // keep the index snapshot
    for _, p := range paths {
        dst := VersionPath(name, "files", p)               // same layout push already writes
        if err := copyFile(localPath(p), dst); err != nil { ... }
    }
    return nil
}
```

2. Make it **automatic in pull whenever an overwrite or local delete will happen**
   (parity with push's auto-version at `cmd/push.go:143-146,288-293`), gated by a new
   `--no-backup` flag; keep `--backup` as "force a snapshot even when nothing will be overwritten".
3. Fix `version.Revert` lookup order to **`files/` → `deleted/` → remote** (and only then),
   which simultaneously makes `cmd/revert.go:18-19` true and finally uses push's `files/` copies.
4. On overwrite, also write the losing local copy under
   `.ft/versions/<name>/files/<path>` **before** `t.Download` runs (see F1's flow).

**Acceptance test:** local edit + `ft pull` (with default auto-backup) → conflict or overwrite;
then `ft revert <pre-pull-*>` restores the **local** bytes exactly.

---

#### F4 — Pull never looks at the actual remote filesystem

**Priority:** P0 · **Type:** broken core promise · **Locations:**
`cmd/pull.go:118-121` + `cmd/pull.go:127-193` (index-only), `transport/transport.go:17-29` (no `Stat`),
`index/index.go:18-22` (`Mtime` = pusher's local mtime, not server mtime),
README `:100-106` and `:127-128` (advertised behaviour), `cmd/diff.go:65-109` (same blind spot → F9)

**What happens:** the remote side of the comparison is exclusively `<remote>/.ft/index.json`.
Files edited/added/deleted **directly on the server** (FileZilla, a CMS, a colleague, a deploy
script) are not in that index, so pull cannot see them — yet this is the documented workflow.

**Evidence (real run):**

```
$ echo "edited directly on server" > /tmp/opencode/srv/index.html
$ ft status
working tree clean (no changes since last sync)
$ ft pull
remote has 2 tracked files
already up to date
pulled 0 files from "origin"
$ cat index.html
hello v1                                     # ← server change invisible
```

A new file created directly on the server is likewise never downloaded, and a file deleted
directly on the server stays locally (F5).

**Additional design gap:** `FileEntry.Mtime` is the **pusher's local file mtime**, not the server's,
so you cannot compare it against a server-side stat to detect edits. There is currently no field
recording what the server's file looked like at push time.

**Fix (git-like — this is the `origin/<branch>` remote-tracking ref problem):**

1. **New transport capability** (`transport/transport.go:17-29`):

```go
type RemoteInfo struct {
    Size  int64
    Mtime time.Time   // zero if the server can't report it
    IsDir bool
}
// Stat returns the *actual* server-side state of a path.
Stat(remoteRelPath string) (RemoteInfo, error)
```

   * FTP (`transport/ftp.go`): `client.FileSize()` (`:176-186` already uses it) +
     `client.GetTime()` guarded by `client.IsGetTimeSupported()`; `GetEntry()` as fallback.
     Note `IsTimePreciseInList()` exists for directory listings.
   * SFTP (`transport/sftp.go`): `client.Stat()` already available (`:264-273` uses it today
     for `FileExists`).
   * Keep `FileExists` for compatibility; implement it on top of `Stat`.

2. **New local remote-tracking state: `.ft/remote.json`** — the `ft` equivalent of
   `refs/remotes/origin/*`. It records the last **observed** remote state per path
   (size/mtime/hash-when-known). Created/updated by `pull` and by `push` (after upload, push knows
   the true server state). The server's own `.ft/index.json` is treated as a *hint*, never as truth.

3. **Detection layers (cheap → thorough):**
   * **Tier 1 (default):** `Stat` every tracked path; if size/mtime differ from `.ft/remote.json`
     (or `.ft/remote.json` is missing) ⇒ candidate for download. Plus compare local hash vs base
     as today.
   * **Tier 2 (`--scan` / `--verify`):** recursively `ListDir` the remote tree to discover files
     **not** in any index (F4's "new file on server" case), and optionally download-to-temp+hash
     every file for a full reconcile. `transport.ListDir` already exists
     (`transport/transport.go:23`), and `jlaffaye/ftp` even exposes `Walk()`.
   * Fallback when mtime is unavailable (some FTP servers): size-only in Tier 1, and document that
     same-size edits need `--verify`.

4. Write the observed state back into `.ft/remote.json` after each observation → this is also what
   fixes F8 (convergence).

**Acceptance test:** edit a file directly on the server → `ft pull` downloads it; add a new file
directly on the server → `ft pull --scan` downloads it; delete one directly → `ft pull` removes it
when the local copy is clean.

---

#### F5 — Deletions never propagate, and local deletions are resurrected

**Priority:** P0 · **Type:** divergence + silent undo of user intent · **Locations:**
`cmd/pull.go:127` (loop only over `remoteIdx.Files`), `cmd/pull.go:174-176` (`!localExists ⇒ download`),
`cmd/status.go:49-52` (reports clean afterwards)

**What happens (both directions):**

* **Remote deleted → local keeps it.** The loop iterates the remote index, so a path that vanished
  upstream is simply never considered.
* **Local deleted → pull restores it.** `!localExists` is treated as "needs download", so a
  deliberate local delete is undone.

**Evidence (real run, both):**

```
# remote deletion ignored
$ cd cli2 && rm -f from-machine2.txt && ft push
[1/1] deleting  from-machine2.txt
$ cd ../cli && ft pull
remote has 202 tracked files
already up to date
$ ls from-machine2.txt
-rw------- 1 masum 21 ... from-machine2.txt     # ← still here
$ ft status
working tree clean (no changes since last sync) # ← status claims sync

# local deletion resurrected
$ rm -f many/f1.txt
$ ft status | head -1
  deleted:     many/f1.txt
$ ft pull
[1/1] downloading many/f1.txt
$ ls many/f1.txt
-rw------- 1 masum 39 ... many/f1.txt           # ← came back
$ ft status
working tree clean (no changes since last sync)
```

**Fix:** implement matrix rows **2 (delete local)** and **5 (leave deleted)** — both are
consequences of consulting **base**:

* `base==local && remote absent` → delete the local file (git removes files deleted upstream when
  the worktree is clean), then `delete(newIdx.Files, p)`.
* `local absent && remote==base` → do nothing; leave the index entry so `status` keeps showing
  `deleted` and `push` can propagate the removal (git-like).
* `local absent && remote!=base` → conflict (row 6).
* Gate upstream-driven local deletions behind the same backup as F3 (git doesn't need this because
  it has reflogs; `ft`'s equivalent safety is the snapshot).
* Optional flag `--no-delete` for parity with push (`cmd/push.go:123-125,433`).

**Acceptance test:** (a) delete upstream → local file gone, `status` clean; (b) delete locally →
`pull` leaves it deleted, `status` still shows `deleted`, `push` removes it upstream.

---

### 🟠 P1

---

#### F6 — One bad file aborts the whole pull (retries, doubled error, usage dump)

**Priority:** P1 · **Type:** robustness/UX · **Locations:**
`cmd/pull.go:219-221` (hard `return`), `cmd/pull.go:253-266` (`retryDownload`),
`cmd/root.go:28-35` (`Execute` prints the error; no `SilenceUsage`/`SilenceErrors` anywhere)

**Evidence (real run):**

```
$ rm /tmp/opencode/srv/index.html     # remote file gone, but still listed in the remote index
$ echo "x" > index.html
$ time ft pull
[1/1] downloading index.html
Error: downloading index.html: downloading index.html: 550 No such file or directory.
Usage:
  ft pull [remote] [files...] [flags]
  ... (full flag list) ...
downloading index.html: downloading index.html: 550 No such file or directory.
real 0m3.221s ; exit=1
```

Three separate defects in one shot: (a) 1s+2s of retries for a **permanent** 550,
(b) the error is printed **twice** (cobra + `Execute`), (c) a usage dump on a runtime failure,
(d) nothing was downloaded/saved even though the failure affected one path.

**Fix:**

1. **Retry policy:** retry only transient errors (timeouts, 421/450/451, connection reset,
   EOF). Never retry 550/404/permission errors. Keep backoff but cap total delay.
2. **Per-file isolation:** collect failures, continue with the rest, then summarise:

```
pulled 41 file(s), failed 1, conflicts 0
  failed  index.html: 550 No such file or directory
```

   Index is merged for the successful paths; failed paths keep their old entries (no partial state).
3. **Exit codes (git-like):** `0` success · `1` conflicts · `2` one or more transfer errors.
4. **Error output:** set `SilenceUsage: true` and `SilenceErrors: true` on `rootCmd`
   (`cmd/root.go:12-26`) and keep the single print in `Execute` (`cmd/root.go:31-34`) →
   exactly one message, no usage dump. Flag-parse errors still show usage because cobra checks
   `cmd.SilenceUsage` at the flag layer (verify with a bad flag after the change).
5. Close/refresh the transport between attempts when the connection itself died (today retries
   reuse a possibly-dead control connection — same latent issue in `cmd/push.go:375-388`).

**Acceptance test:** one stale remote entry among 50 → 49 pulled, 1 reported failed, exit `2`,
no usage text, total runtime ≪ 3s.

---

#### F7 — No integrity verification of downloaded content

**Priority:** P1 · **Type:** correctness · **Locations:** `cmd/pull.go:211-222`
(`t.Download` result trusted), `transport/ftp.go:106-140` / `transport/sftp.go:181-215`
(temp+rename is good — but nothing validates bytes), `cmd/pull.go:224-240`
(index rebuilt from whatever landed on disk, so a corrupt transfer is *ratified* as truth)

**What happens:** a truncated/duplicated transfer (FTP ASCII-mode mangling, dropped connection,
server bug) is renamed into place and then hashed into the index as the new baseline. Next push
propagates the corruption to the server.

**Fix:**

1. Download to the existing temp file, **hash the temp file before `os.Rename`**, compare with the
   expected hash when known (remote index entry / `.ft/remote.json`), then rename.
   Best implemented *inside* `Download` (both transports) or as a
   `DownloadVerified(remoteRel, localPath, wantHash string) error` wrapper in `transport`.
2. Mismatch ⇒ treat as transient (retry), then as failure (F6 reporting); never rename into place.
3. When the remote index is known-stale (F4), accept the computed hash and record it in
   `.ft/remote.json` — the download, not the index, is the source of truth.
4. Also force binary transfer type on FTP (`client.Type(ftp.TransferTypeBinary)`) before `Retr` —
   ASCII mode silently rewrites line endings and would make hashes disagree forever.

**Acceptance test:** hash of the file on disk after pull == hash recorded in `.ft/remote.json` ==
hash the server would report under `--verify`.

---

#### F8 — Pull never converges (endless re-download)

**Priority:** P1 · **Type:** correctness/perf · **Locations:** `cmd/pull.go:224-240` (base rebuilt from disk),
absence of any `SyncIndexToRemote` in `cmd/pull.go` (push does it at `cmd/push.go:274-286`),
no remote-tracking state (F4)

**Evidence (real run — four consecutive pulls):**

```
$ ft pull
[1/1] downloading index.html
$ ft pull
[1/1] downloading index.html
$ ft pull
[1/1] downloading index.html
$ ft pull
[1/1] downloading index.html
```

**Why:** pull sets the local base to the *new* content (`BuildIndex` after download) while the
server's index still records the *old* content, and pull never reconciles the server index.
`local(base) != remote-index` forever ⇒ download forever.

**Fix:** fixed by F2 (never rebuild base from disk) + F4 (`.ft/remote.json` remembers the observed
remote state). Do **not** "fix" this by rewriting the server's index from a client — that would let
one machine silently rewrite shared state for everyone (git never mutates the upstream ref on fetch).

**Acceptance test:** after one successful pull, two further `ft pull` runs both print
`already up to date` and transfer **0 bytes** — including after a direct server edit.

---

#### F9 — `ft diff` has the same blindness as pull

**Priority:** P1 · **Type:** consistency · **Locations:** `cmd/diff.go:60-63` (local index from disk),
`cmd/diff.go:65-109` (remote side = remote index only), `cmd/diff.go:71-84` (missing-remote-index special case)

`ft diff` compares the local working tree against the remote **index**, so it reports
"local and remote are in sync" in exactly the scenario F4 demonstrates (server file changed
directly). It also never sees files that exist only on the server outside the index.

**Fix:** once `Transport.Stat` + `.ft/remote.json` exist (F4), point `ft diff` at the same remote
state provider used by pull (extract a shared `remoteState` helper in `cmd/`), so `status`/`diff`/
`pull` agree. Keep `diff`'s nice "no remote index" fallback (`cmd/diff.go:71-84`).

**Acceptance test:** direct server edit ⇒ `ft diff` shows `modified: index.html` *before* any pull.

---

### 🟡 P2

---

#### F10 — `--include` globs never match nested paths

**Priority:** P2 · **Type:** UX · **Locations:** `cmd/pull.go:145-156` (esp. `:148`),
`cmd/push.go:358-367` (esp. `:361`, same bug — fix both), `README.md:161-163`

```go
// cmd/pull.go:148
if matched, _ := filepath.Match(pattern, relPath); matched {   // '*' does not cross '/'
```

**Evidence:** `ft pull --include '*.php'` printed `already up to date` for a tree containing
`admin/index.php` — `filepath.Match("*.php", "admin/index.php") == false`. Silent no-op, no
warning that the pattern matched nothing.

**Fix (git pathspec / gitignore semantics):** add one shared helper next to the ignore matcher
(`index/index.go:305-337` already implements exactly this shape for `.ftignore`):

```go
// index/index.go
// MatchPath reports whether relPath matches pattern using gitignore-ish semantics:
// exact match, directory-prefix match, basename match when the pattern has no '/',
// and filepath.Match against the full path for explicit globs.
func MatchPath(pattern, relPath string) bool
```

Use it for `--include`/`--exclude` in **both** push and pull (keeps them consistent), and warn when
a filter matches nothing (`warning: --include '*.php' matched 0 files`). Keep `.ftignore`'s `!`
negation behaviour untouched (`index/index.go:339-357`).

**Acceptance test:** `ft pull --include '*.php'` selects `admin/index.php`; `--include 'admin/*'`
selects the directory subtree; no filter silently matches nothing.

---

#### F11 — Output & wasted work on no-op pulls

**Priority:** P2 · **Type:** UX/perf · **Locations:** `cmd/pull.go:195-199`,
`cmd/pull.go:201-209` (esp. `:205-207`), `cmd/pull.go:224-240`, `cmd/pull.go:246-248`

* `already up to date` is printed, but execution continues → a **full-tree rehash** +
  index save on every no-op pull (`:224-240`). For a large tree this is the dominant cost of
  "doing nothing".
* Two success lines are printed for zero work: `already up to date` **and**
  `pulled 0 files from "origin"` (seen in evidence above).
* `-q` does not suppress the `--dry-run` file listing (`:205-207` prints unconditionally),
  so `ft pull -q -n` still prints `  download  index.html`.

**Fix:**

1. When the plan contains **no actions**, return immediately after the message
   (`return nil`) — no rehash, no save (safe: nothing changed, so the index can't need updating).
   This is the "fast no-op" path.
2. Single success line: `already up to date` **or** `pulled N file(s) from "origin"`, never both.
3. Gate the dry-run listing on `!pullQuiet` (decide once and apply to push too —
   `cmd/push.go:150-158` has the identical pattern, keep them consistent).
4. Keep `remote has N tracked files` behind `!pullQuiet` (already correct, `cmd/pull.go:123-125`).

**Acceptance test:** `ft pull` on a synced tree prints one line and completes in ~1/5 the current
time on a large tree; `ft pull -q -n` prints nothing.

---

#### F12 — Serial downloads, no progress

**Priority:** P2 · **Type:** performance/UX · **Locations:** `cmd/pull.go:211-222` (serial loop),
`cmd/pull.go:256` (`progress` hardcoded `nil`), vs `cmd/push.go:161-163` + `cmd/push.go:184-221`
(worker pool, `--jobs`, atomic counter), `llms.txt:63` (documented convention)

Push is concurrent with per-worker transports; pull is a single connection doing one file at a
time. FTP's `ServerConn` is explicitly **not safe for concurrent use** (one in-flight data
connection), so the push pattern must be copied, not shared.

**Fix:**

1. Add `-j, --jobs` (default 4) to pull, mirroring `cmd/push.go:161-163`:
   one `transport.NewTransport` + `Connect` per worker goroutine, jobs over a channel, failures to
   a buffered error channel, `sync.WaitGroup` + `atomic` progress counter — reuse the structure at
   `cmd/push.go:184-221` (and its `retryUpload`-style retry, fixed per F6).
2. Both `Download` implementations already write to a temp file and `os.Rename`
   (`transport/ftp.go:118-134`, `transport/sftp.go:193-209`), so concurrent downloads of
   **different** paths are safe. Never parallelise two writers to the same path.
3. Wire progress: pass `os.Stdout` (when `!pullQuiet`) instead of `nil` at `cmd/pull.go:256`;
   optionally add a byte-level `io.Writer` progress reporter (the parameter already exists in the
   interface, `transport/transport.go:20-21`).
4. Guard the human-readable `[i/n] downloading …` lines with the atomic counter exactly like push.

**Acceptance test:** `ft pull -j 8` on 200 files completes materially faster than `-j 1` with no
interleaved/corrupt output; `ft pull -q` silent.

---

#### F13 — Index entry after a selective pull records the wrong mtime

**Priority:** P2 · **Type:** hygiene (currently masked) · **Locations:** `cmd/pull.go:228-236` (esp. `:234`)

```go
newIdx.Files[relPath] = remoteIdx.Files[relPath]   // copies the *other machine's* mtime
```

The local file on disk has mtime = *now* (temp+rename), but the index entry gets the mtime the
remote machine recorded when *it* wrote the file. `index.DetectChanges` (`index/index.go:174-188`)
masks this today by falling back to a hash comparison, but any consumer trusting `Mtime` (or a
future fast-path that skips hashing when size+mtime match) sees a phantom difference.

**Fix:** after each download, build the entry from the **local** file that was just written
(hash + size + local mtime) — i.e. `entryFromDisk(p)` from F2 — in *both* the selective and the
full branches. As a bonus, this is one `os.Stat` + one hash per downloaded file instead of a
full-tree scan (also helps F11).

**Acceptance test:** after `ft pull origin admin/index.php`, `local.mtime == stat(index.html).mtime`
and `ft status` remains clean.

---

## 4. Target design summary

### 4.1 New/changed artefacts

| Artefact | Purpose | Git analogue |
|---|---|---|
| `.ft/index.json` | **base** — last known sync point (unchanged format, new write discipline) | index / `HEAD` |
| **`.ft/remote.json`** (new) | last **observed** remote state (size/mtime/hash) | `refs/remotes/origin/*` |
| `.ft/versions/<name>/files/**` | content snapshot of work about to be destroyed | `git stash` |
| `Transport.Stat()` (new) | real server-side state | `git ls-tree`/`stat` on remote |
| `cmd/pull_plan.go` (new) | pure 3-way planner + conflict reporting | merge-base logic |
| `index.MatchPath()` (new) | gitish pathspec for `--include`/`--exclude` | pathspec/`.gitignore` matching |

### 4.2 New/changed flags for `ft pull`

| Flag | Behaviour |
|---|---|
| `-f, --force` | let remote win over local edits (implies backup) — the only overwrite path |
| `--no-backup` | skip the automatic pre-overwrite snapshot |
| `--no-delete` | don't remove local files deleted upstream (parity with push) |
| `-j, --jobs` | concurrent downloads, default 4 (parity with push) |
| `--scan` | walk the remote tree to find files not in any index |
| `--verify` | download-to-temp + hash every candidate (paranoid reconcile) |
| `-n`, `-q`, `-p`, `--backup`, `--include`, `--exclude` | kept, semantics tightened (F10/F11) |

### 4.3 Execution order inside `RunE`

```
1.  parse args → remote, pathspec
2.  load base (.ft/index.json) + ignore/pathspec filters
3.  connect (1 connection for stat/list; workers for transfer)
4.  observe remote: FetchIndexFromRemote (hint) + Stat/list (truth) → merge into remoteState
5.  hash local candidates (only paths in the plan's candidate set, not the whole tree)
6.  planPull(...) → []pullPlan  (pure; unit-tested matrix)
7.  if conflicts && !force → print conflict report → exit 1   (NO writes)
8.  if actions will overwrite/delete → auto snapshot (version.SaveWorkingTree)
9.  if dry-run → print plan → exit 0
10. transfer (worker pool; temp + hash-verify + rename)
11. merge index surgically (only touched paths); update .ft/remote.json
12. summary + exit code (0 / 1 conflict / 2 errors)
```

---

## 5. Implementation plan (ordered)

### P0 — safety & truth (ship together; each has an acceptance test in §3)

| # | Step | Files | Depends on |
|---|------|-------|-----------|
| P0.1 | Add `Transport.Stat` + `RemoteInfo`; FTP (`FileSize` + `GetTime`, guarded) and SFTP (`Stat`) impls | `transport/transport.go`, `transport/ftp.go`, `transport/sftp.go` | — |
| P0.2 | Add `.ft/remote.json` read/write + "observe remote" helper (merge remote index hint with stat truth) | new `cmd/remotestate.go` (or `index/remote.go`) | P0.1 |
| P0.3 | Extract the **pure 3-way planner** implementing the §2 matrix + conflict type; table-driven tests | new `cmd/pull_plan.go`, `cmd/pull_plan_test.go` | — |
| P0.4 | Rewrite `pullCmd.RunE`: plan → conflict refusal (`--force`) → transfer → **surgical** index merge (drop `BuildIndex`) | `cmd/pull.go:118-240` | P0.2, P0.3 |
| P0.5 | Working-tree snapshots: `version.SaveWorkingTree`; auto-backup before overwrite; `--no-backup` | `version/version.go`, `cmd/pull.go:92-101` | P0.4 |
| P0.6 | Fix `version.Revert` to prefer `files/` → `deleted/` → remote (makes `cmd/revert.go:18-19` true, revives push's `files/`) | `version/version.go:168-217` | P0.5 |
| P0.7 | Deletion rows of the matrix (upstream delete ⇒ local delete when clean; local delete stays deleted) + `--no-delete` | `cmd/pull.go` (with P0.4) | P0.3, P0.4 |
| P0.8 | Remote-state observation on push too (record true post-upload state in `.ft/remote.json`) | `cmd/push.go:274-293` | P0.2 |

**P0 exit criteria:** F1–F5 acceptance tests green; `go build ./... && go vet ./... && go test ./...`.

### P1 — robustness & convergence

| # | Step | Files | Fixes |
|---|------|-------|-------|
| P1.1 | Error classification + bounded retries (no retry on 550/404/perm) + per-file failure collection + exit codes 0/1/2 | `cmd/pull.go:253-266`, `cmd/pull.go:211-222` | F6 |
| P1.2 | `SilenceUsage`/`SilenceErrors` on `rootCmd`, single error print in `Execute` | `cmd/root.go:12-35` | F6 |
| P1.3 | Hash-verify downloads (temp → hash → rename) + FTP binary type | `transport/ftp.go:106-140`, `transport/sftp.go:181-215`, `cmd/pull.go` | F7 |
| P1.4 | Convergence tests (3 consecutive pulls = 0 transfers) as a regression test | tests | F8 |
| P1.5 | Point `ft diff` at the shared remote-state provider | `cmd/diff.go:65-109` | F9 |

### P2 — polish

| # | Step | Files | Fixes |
|---|------|-------|-------|
| P2.1 | `index.MatchPath` + use in push/pull `--include`/`--exclude` + "matched 0 files" warning | `index/index.go`, `cmd/pull.go:145-156`, `cmd/push.go:358-367` | F10 |
| P2.2 | Early return on empty plan; single success line; gate dry-run listing on `-q` (both commands) | `cmd/pull.go:195-209`, `cmd/push.go:148-159` | F11 |
| P2.3 | `--jobs` worker pool for downloads + wire progress writer | `cmd/pull.go:211-222`, new worker code mirroring `cmd/push.go:184-221` | F12 |
| P2.4 | Build pulled entries from disk (`entryFromDisk`) in all branches | `cmd/pull.go:228-236` | F13 |

---

## 6. Test plan

`cmd` has no tests today (`llms.txt:67`). Add two layers:

### 6.1 Unit tests (no network)

* **`cmd/pull_plan_test.go`** — table-driven over all 10 matrix rows × variants
  (ignored paths, pathspec-filtered paths, `--force`, `--no-delete`, missing base/remote entries).
  This is the highest-value test file in the repo: it locks the git-like semantics.
* **`index/matchpath_test.go`** — pathspec cases (`*.php` → `admin/a.php`, `admin/*`,
  trailing `/`, negation, no false positives).
* **`version` tests** — `SaveWorkingTree` writes content; `Revert` prefers `files/` and restores
  bytes that were never pushed.

### 6.2 Integration harness (local FTP, the one used to produce this document)

`testdata/ftpsrv.py` (pyftpdlib) + a shell script asserting each acceptance test:

```bash
# harness sketch — two "machines" against one local FTP server
python3 testdata/ftpsrv.py "$SRV" 2121 &
ft remote add origin ftp://user:pass@127.0.0.1:2121/

# P0/F1  conflict, no clobber
echo local > A/index.html; echo remote > "$SRV"/index.html; ft -C cli2 push
ft -C cli pull            → exit 1, CONFLICT, file unchanged

# P0/F2  untracked not absorbed
echo new > A/new.txt; ft pull; ft status → "new file: new.txt"; ft push → uploads it

# P0/F3  backup restores local bytes
echo precious > A/index.html; ft pull --backup; ft revert <pre-pull> → precious restored

# P0/F4  direct server edit
echo v2 > "$SRV"/index.html; ft pull → downloads v2

# P0/F5  deletions both directions
# P1/F8  convergence: pull x3 → 0 transfers after the first
# P1/F6  one stale entry → 49 ok / 1 failed, exit 2, no usage dump
```

### 6.3 Regression guard

`go build ./... && go vet ./... && go test ./...` (existing suites must stay green), plus the
harness in CI if a service container is acceptable.

---

## 7. Non-goals (this plan)

* No rename detection / content merging — FTP has no atomic moves to observe; conflicts are
  all-or-nothing (git without `rename` detection either).
* No `ft fetch`/`ft merge` split yet (worth designing later: `pull` = fetch + merge, which would
  make `--dry-run` and conflict handling even closer to git).
* No change to `ft status` staying purely local (`cmd/status.go`) — it behaves like `git status`.
* No rewrite of the ignore engine (`index/index.go:305-357`); only *reuse* it for pathspecs.
* `ft log` / `appendLog` never being called (`llms.txt:71`) is out of scope, though pull/push
  should eventually write history once this lands.

---

## 8. Open decisions (need a call before coding)

1. **Remote discovery default.** Cheap (stat only, Tier 1) vs thorough (recursive `ListDir`, Tier 2).
   Recommendation: Tier 1 by default, `--scan` for discovery of un-indexed files — with a note in
   the README that direct-server changes to *new* files need `--scan`.
2. **`--force` vs `--take-remote`.** `--force` is git muscle memory; keep it, document the alias.
3. **Automatic backup cost.** Snapshotting before every overwrite copies bytes; for a 10k-file
   overwrite with `--force` this is heavy. Recommendation: snapshot only the files actually being
   overwritten (already the design), and let `--no-backup` opt out.
4. **Exit code choice** for conflicts (`1`) vs errors (`2`) — confirms nothing else in the wild
   depends on `1` meaning "any error" today.
5. **Backwards compatibility:** `.ft/remote.json` is additive (old clients ignore it); old indexes
   lacking any remote-state data simply fall back to index-hash comparison on first run.

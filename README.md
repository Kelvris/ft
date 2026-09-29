# ft — Git-like FTP/SFTP sync tool

[![MIT License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE) [![Discord](https://img.shields.io/badge/Discord-Join-5865F2?logo=discord&logoColor=white)](https://discord.gg/p2CK5guHZx)

`ft` lets you upload and download files between your computer and a web server using **FTP** or **SFTP** — with simple Git-style commands. No more dragging files in FileZilla or guessing which files changed.

If you have a website on shared hosting (like InfinityFree, Hostinger, etc.) and you're tired of manually uploading every file, `ft` is for you.

## Features

- **Git-like commands** — `init`, `status`, `push`, `pull`, `log`, `diff`
- **FTP + SFTP** — works with both protocols
- **Safe pull** — three-way comparison like `git pull`: fast-forwards when only the server changed, refuses (exit `1`) instead of clobbering your local edits, and auto-backs-up anything it overwrites
- **Concurrent transfers** — uploads *and* downloads run in parallel (`--jobs`, default 4)
- **Password vault** — stores passwords safely in a hidden, rotating folder
- **Version snapshots** — every push auto-saves a version; revert if something breaks
- **Interactive setup** — `ft setup` walks you through everything step by step
- **Selective sync** — push/pull only specific files if you want
- **Dry-run** — preview what would change without actually doing it
- **Ignore files** — use `.ftignore` to skip certain files (like `.env` or `node_modules`)
- **Quiet mode** — `-q` for less output

## Prerequisites

You need **Go 1.26+** installed to install `ft`.

### Installing Go

1. Go to https://go.dev/dl/
2. Download the installer for your system (Windows / macOS / Linux)
3. Run the installer
4. Open a new terminal and type `go version` to verify it's installed

> **Minimum requirement: Go 1.26** — older versions won't work.
>
> On Linux you can also use your package manager:
> ```bash
> sudo apt install golang-go   # Debian/Ubuntu
> sudo dnf install go          # Fedora
> ```

## Install

Once Go is installed, run:

```bash
go install github.com/Kelvris/ft@latest
```

This downloads and compiles `ft` into `~/go/bin/ft`. Make sure `~/go/bin` is in your PATH (Go usually adds it automatically).

### Verify installation

```bash
ft --version
```

You should see `ft v...`

## Quick start

### 1. Go to your project folder

```bash
cd my-website
```

### 2. Run the setup wizard

```bash
ft setup
```

It will ask you:
- **Protocol** — choose `ftp` or `sftp`
- **Host** — your server address (e.g. `ftpupload.net`)
- **Port** — usually `21` for FTP, `22` for SFTP
- **Username** — your FTP username
- **Password** — your FTP password (hidden as you type)

Then it connects to your server and shows your remote directories. Navigate into the folder where your website lives (e.g. `/htdocs`) and press Enter to select it.

Done! Your remote server is now configured as "origin".

### 3. See what files have changed

```bash
ft status
```

Shows files that are new, modified, or deleted since last sync.

### 4. Upload your files

```bash
ft push
```

This uploads changed files to your server. Only changed files are uploaded — not everything.

### 5. Download files from server

```bash
ft pull
```

Downloads server changes to your laptop. Like `git pull`, it fast-forwards when only the server changed — and refuses (exit `1`) rather than overwriting files you edited yourself.

### 6. Check connection

```bash
ft ping
```

Tests if your server credentials work.

## Real-world workflow

```bash
# You just finished coding on your laptop:
ft status           # check what changed
ft push             # upload to server

# Oops, something broke on the live site:
ft revert           # pick a previous version, restore it
ft push             # upload the old working files

# Someone uploaded something to the server directly:
ft pull             # download it to your laptop

# Both you and the server changed the same file:
ft pull             # refuses and lists the conflicts (nothing is overwritten)
ft pull --force     # take the server version anyway (your copy is backed up first)

# You want to deploy just one file:
ft push origin admin/fix.php

# You want to test without actually uploading:
ft push --dry-run
```

## Commands

### `ft init [url]`
Creates the `.ft/` folder that `ft` uses to track files. You usually don't need this — `ft setup` and `ft push` do it automatically.

### `ft setup`
Interactive wizard. Connects to your server, lets you browse directories, and saves everything as remote `origin`. Run this once when starting a new project.

### `ft push [remote] [files...]`
Upload changed files to your server.

```
Flags:
  -j, --jobs int      How many files to upload at once (default 4)
  -n, --dry-run       Show what would change, don't upload
  -p, --password      Prompt for password (ignore saved one)
  -q, --quiet         Less output
      --no-delete     Don't delete files on server
      --no-version    Don't create a version snapshot for this push
      --include       Only files matching this pattern (repeatable)
      --exclude       Skip files matching this pattern (repeatable)
```

```bash
ft push                         # upload everything
ft push origin admin/*.php      # only upload admin PHP files
ft push --include '*.html'      # only HTML files
ft push --exclude 'uploads/*'   # skip uploads folder
ft push --no-delete             # upload new/changed, but don't remove anything
```

### `ft pull [remote] [files...]`
Download changed files from your server.

Pull works in three directions, like `git pull`: it compares your last sync
point (`.ft/index.json`), your working files, and the *actual* server (via
`Stat`, not just the remote index). Your local edits are never clobbered —
when both sides changed the same file, pull refuses with a `CONFLICT` report
and exits `1` without touching anything. When only the server changed it
fast-forwards; when only you changed, your file is left alone.

```
Flags:
  -n, --dry-run       Show what would change, don't download
  -p, --password      Prompt for password
  -q, --quiet         Less output
  -f, --force         Take the server version over local edits (backed up first)
      --backup        Snapshot your working tree (contents included) before pulling
      --no-backup     Skip the automatic pre-overwrite snapshot
      --no-delete     Keep files that were deleted on the server
  -j, --jobs N        Concurrent downloads (default 4)
      --scan          Also walk the server tree for files missing from every index
      --verify        Hash each candidate download (catches same-size server edits)
      --include       Only files matching this pattern (gitish: '*.php' matches admin/index.php)
      --exclude       Skip files matching this pattern
```

Exit codes: `0` success (including "already up to date"), `1` conflicts —
nothing was changed — or a fatal error (e.g. can't connect), `2` some files
failed to transfer (the others were applied).

Anything pull overwrites or deletes is copied into a version snapshot first,
so `ft revert <name>` can bring your bytes back. Untracked local files are
never absorbed by a pull — they stay untracked until you `ft push` them.

### `ft status`
Shows which files are new, modified, or deleted since the last sync.

### `ft diff [remote]`
Compares your local files against the actual server (it asks the server directly, so it still works when the remote index is stale). For changed files, it shows a line-by-line diff (like `git diff`).

### `ft log`
Shows history of past pushes and pulls.

### `ft ls [remote[/path]]`
Lists files and folders on your server. Directories are shown with a `/` at the end.

### `ft ping [remote]`
Tests the connection to your server and shows how long it took.

### `ft info [remote]`
Shows useful info: how many files are tracked, how many versions saved, connection status.

### `ft restore <file>`
Downloads a single file from the server. Handy when you only need one file back.

```bash
ft restore config.php
```

### `ft revert [name]`
Restores your files to a previous version. If you don't give a name, it shows a list you can pick from.

```bash
ft revert              # interactive picker
ft revert 20260528-123456   # revert to a specific version
```

### `ft version`
Manage saved versions.

```
Subcommands:
  ls                  List all versions (local + on server)
  save <name>         Save current state as a version manually
  diff <name>         Compare a version against what you have now
```

Versions are created automatically every time you run `ft push`. Use `ft revert` to go back to one.

### `ft remote`
Manage server connections.

```
Subcommands:
  add <name> <url>       Add another server
  ls                     List all configured servers
  rm <name>              Remove a server
  set-password <name>    Save password for a server
  clear-password <name>  Remove saved password
```

## Ignoring files

Create a file called `.ftignore` (or `.ftpignore`) in your project folder. It works like `.gitignore`:

```
*.log
node_modules/
uploads/cache/
.env
```

Files and folders starting with `.ft` or `.git` are always ignored automatically.

## Password order

`ft` looks for your password in this order (first one wins):

1. `--password` flag (you type it each time)
2. `FT_PASSWORD` environment variable
3. Password saved in the config file
4. Password saved in the vault (`ft remote set-password`)

If the password comes from the vault, it gets moved to a new random folder after each push/pull (extra security).

## How `ft` tracks files

Everything lives in a hidden `.ft/` folder in your project:

- `.ft/index.json` — your **last sync point** (per-file hash, size, mtime). `ft status` compares your working files against it, and `ft pull` uses it as the "base" of its three-way comparison.
- `.ft/remote.json` — the last state `ft` actually **observed on the server**, filled in by `pull`, `diff`, and `push`. This is what lets pull detect files changed directly on the server, even when the server's own index is missing or stale (the server's `.ft/index.json` is only a first-run hint).
- `.ft/versions/` — version snapshots (see below).
- `.ft/config.json` + vault folder — connection settings, stored per working folder.

## How versions work

Every `ft push` creates a version snapshot automatically. Versions are stored in `.ft/versions/<name>/`:

- `index.json` — the list of files and their hashes at that moment
- `files/` — copies of the files that were uploaded
- `deleted/` — copies of files that were deleted (so you can restore them)

Versions are also synced to your server under `.ft/versions/`, so you can revert even from a different computer.

## Development

```bash
go build ./... && go vet ./... && go test ./...
```

Unit tests cover the pull decision matrix (`cmd/pull_plan_test.go`), pathspec
matching (`index/matchpath_test.go`), and version snapshots (`version`).

There is also an end-to-end harness that runs two simulated machines against a
local FTP server and asserts the acceptance scenarios (conflict refusal,
untracked-file isolation, backups/revert, direct server edits, deletions in
both directions, partial-failure exit code `2`, diff, pull convergence):

```bash
bash testdata/acceptance.sh   # needs python3 with pyftpdlib installed
```

## License

MIT License

Copyright (c) 2026 ft

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

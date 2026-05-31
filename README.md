# ft — Git-like FTP/SFTP sync tool

[![MIT License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE) [![Discord](https://img.shields.io/badge/Discord-elysiummc.xyz-5865F2?logo=discord&logoColor=white)](https://discord.elysiummc.xyz)

`ft` lets you upload and download files between your computer and a web server using **FTP** or **SFTP** — with simple Git-style commands. No more dragging files in FileZilla or guessing which files changed.

If you have a website on shared hosting (like InfinityFree, Hostinger, etc.) and you're tired of manually uploading every file, `ft` is for you.

## Features

- **Git-like commands** — `init`, `status`, `push`, `pull`, `log`, `diff`
- **FTP + SFTP** — works with both protocols
- **Concurrent uploads** — uploads multiple files at once (faster!)
- **Password vault** — stores passwords safely in a hidden, rotating folder
- **Version snapshots** — every push auto-saves a version; revert if something breaks
- **Interactive setup** — `ft setup` walks you through everything step by step
- **Selective sync** — push/pull only specific files if you want
- **Dry-run** — preview what would change without actually doing it
- **Ignore files** — use `.ftignore` to skip certain files (like `.env` or `node_modules`)
- **Quiet mode** — `-q` for less output

## Prerequisites

You need **Go** installed to install `ft`.

### Installing Go

1. Go to https://go.dev/dl/
2. Download the installer for your system (Windows / macOS / Linux)
3. Run the installer
4. Open a new terminal and type `go version` to verify it's installed

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

Downloads files from the server that are newer or different.

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

```
Flags:
  -n, --dry-run       Show what would change, don't download
  -p, --password      Prompt for password
  -q, --quiet         Less output
      --backup        Save a version before pulling (safety net)
      --include       Only files matching this pattern
      --exclude       Skip files matching this pattern
```

### `ft status`
Shows which files are new, modified, or deleted since the last sync.

### `ft diff [remote]`
Compares your local files against the server. For changed files, it shows a line-by-line diff (like `git diff`).

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

## How versions work

Every `ft push` creates a version snapshot automatically. Versions are stored in `.ft/versions/<name>/`:

- `index.json` — the list of files and their hashes at that moment
- `files/` — copies of the files that were uploaded
- `deleted/` — copies of files that were deleted (so you can restore them)

Versions are also synced to your server under `.ft/versions/`, so you can revert even from a different computer.

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

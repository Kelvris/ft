# ft — Git-like FTP/SFTP sync tool

`ft` syncs local files with remote servers via **FTP** or **SFTP** using familiar Git-like commands. Built for developers who deploy to shared hosting without shell access.

## Features

- **Git-like commands** — `init`, `status`, `push`, `pull`, `log`, `diff`
- **FTP + SFTP** — supports both protocols with configurable ports
- **Concurrent uploads** — parallel workers with per-worker transport connections
- **Password vault** — passwords stored in a rotating random directory for obscurity
- **Version snapshots** — every push auto-saves a version; revert anytime
- **Interactive setup** — `ft setup` walks you through connection + directory browsing
- **Selective sync** — push/pull specific files or use `--include`/`--exclude` patterns
- **Dry-run** — see what would change without actually syncing
- **Ignore files** — `.ftignore` / `.ftpignore` with `!` negation (gitignore-style)
- **Quiet mode** — `-q` for minimal output

## Install

```bash
go install github.com/Kelvris/ft@latest
```

Or build from source:

```bash
git clone https://github.com/Kelvris/ft.git
cd ft
go build -o /usr/local/bin/ft .
```

## Quick start

```bash
# Interactive setup — connects, browses directories, saves config
cd my-project
ft setup

# See what changed
ft status

# Upload everything
ft push

# Download changed files from remote
ft pull

# Test connection
ft ping
```

## Commands

### `ft init [url]`
Create the `.ft/` directory and index. Optionally add a remote URL.

### `ft setup`
Interactive wizard: chooses protocol (FTP/SFTP), enters credentials, connects, browses remote directories, selects the target path, and saves everything as remote `origin`.

### `ft push [remote] [files...]`
Upload changed files to the remote server.

```
Flags:
  -j, --jobs int      Concurrent uploads (default 4)
  -n, --dry-run       Show what would change
  -p, --password      Prompt for password
  -q, --quiet         Suppress progress output
      --no-delete     Don't remove remote files
      --include       Only files matching pattern (repeatable)
      --exclude       Exclude files matching pattern (repeatable)
```

```bash
ft push                         # push all changes
ft push origin admin/*.php      # push specific files
ft push --include '*.html'      # only HTML files
ft push --no-delete             # skip remote deletions
```

### `ft pull [remote] [files...]`
Download changed files from the remote server.

```
Flags:
  -n, --dry-run       Show what would change
  -p, --password      Prompt for password
  -q, --quiet         Suppress progress output
      --backup        Save a version snapshot before pulling
      --include       Only files matching pattern (repeatable)
      --exclude       Exclude files matching pattern (repeatable)
```

### `ft status`
Show files added, modified, or deleted since last sync.

### `ft diff [remote]`
Compare local files against remote. Shows a unified diff for modified files (requires the `diff` command).

### `ft log`
Show sync history from `.ft/log.json`.

### `ft ls [remote[/path]]`
List remote directory contents. Directories are shown with a trailing `/`.

### `ft ping [remote]`
Test connection to the remote server and report latency.

### `ft info [remote]`
Display project info: tracked files, local/remote versions, `.ft/` size, and connection status.

### `ft restore <file>`
Download a single file from the remote server without full pull logic.

### `ft revert [name]`
Restore files from a version snapshot. If no name is given, shows an interactive picker. Files are restored from local version backups or downloaded from the remote server if needed.

### `ft version`
Manage version snapshots.

```
Subcommands:
  ls                  List local + remote versions
  save <name>         Save current state as a version
  diff <name>         Show files changed in a version vs current
```

Versions are auto-created on every `ft push`. Use `ft revert` to roll back.

### `ft remote`
Manage remote configurations.

```
Subcommands:
  add <name> <url>       Add a remote (e.g. ftp://user:pass@host/path)
  ls                     List configured remotes
  rm <name>              Remove a remote
  set-password <name>    Save password to vault
  clear-password <name>  Remove password from vault
```

## Ignore files

Place a `.ftignore` or `.ftpignore` in your project root. Patterns follow gitignore rules:

```
*.log
node_modules/
uploads/cache/
!important.log
```

`.ft/` and `.git/` are always excluded from syncing.

## Password resolution order

1. `--password` flag (prompted)
2. `FT_PASSWORD` environment variable
3. Password stored in config
4. Password saved in vault (`ft remote set-password`)

Passwords from the vault are rotated (stored in a new random directory) after each successful push/pull.

## Version storage

Versions are stored in `.ft/versions/<name>/`:
- `index.json` — file manifest at that point in time
- `files/` — backup of each pushed file
- `deleted/` — backup of files deleted from remote

Versions are synced to the remote server under `.ft/versions/`.

## License

[MIT](LICENSE)

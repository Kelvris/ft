#!/usr/bin/env bash
# Acceptance harness for PULL_FIX_PLAN.md §6.2 (F1–F8).
#
# Two simulated machines ("machine1", "machine2") talk to one local FTP
# server rooted at $SRV.  Config, index and remote-state live in each
# machine's own .ft/, so plain subdirectories are enough to model two
# computers — no -C flag needed.
#
# Run:  bash testdata/acceptance.sh
# Needs: go, python3 with pyftpdlib (override with $PYTHON).
set -u

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/ft-accept.XXXXXX")"
PORT="${FT_ACCEPT_PORT:-2121}"
SRV="$WORK/server"
M1="$WORK/machine1"
M2="$WORK/machine2"
FT="$WORK/ft"
URL="ftp://user:pass@127.0.0.1:${PORT}/"

FAILS=0
OUT=""
CODE=0
SRV_PID=""

cleanup() {
    if [ -n "$SRV_PID" ]; then
        kill "$SRV_PID" 2>/dev/null
        wait "$SRV_PID" 2>/dev/null
    fi
    rm -rf "$WORK"
}
trap cleanup EXIT

ok()  { printf 'ok   %s\n' "$1"; }
bad() { printf 'FAIL %s\n' "$1"; FAILS=$((FAILS + 1)); }

# run_ft <machine-dir> <ft args...> — captures combined output in $OUT and
# the exit code in $CODE.
run_ft() {
    local dir="$1"
    shift
    OUT="$(cd "$dir" && "$FT" "$@" 2>&1)"
    CODE=$?
}

expect_exit() {
    if [ "$CODE" = "$2" ]; then ok "$1"; else bad "$1 (exit $CODE, want $2; output: $OUT)"; fi
}

expect_out() {
    if printf '%s\n' "$OUT" | grep -qF -- "$2"; then ok "$1"; else bad "$1 (missing '$2'; output: $OUT)"; fi
}

expect_no_out() {
    if printf '%s\n' "$OUT" | grep -qF -- "$2"; then bad "$1 (unexpected '$2'; output: $OUT)"; else ok "$1"; fi
}

expect_content() {
    # expect_content <desc> <path> <want>
    local got
    got="$(cat "$2" 2>/dev/null)"
    if [ "$got" = "$3" ]; then ok "$1"; else bad "$1 (file $2 = '$got', want '$3')"; fi
}

expect_absent() {
    if [ -e "$2" ]; then bad "$1 ($2 still exists)"; else ok "$1"; fi
}

expect_exists() {
    if [ -e "$2" ]; then ok "$1"; else bad "$1 ($2 missing)"; fi
}

# newest backup dir matching .ft/versions/<prefix>-*/ in a machine
newest_backup_dir() {
    # newest_backup_dir <machine-dir> <prefix>
    ls -d "$1"/.ft/versions/"$2"-*/ 2>/dev/null | sort | tail -1
}

# ---------------------------------------------------------------- build ----
printf 'building ft...\n'
if ! (cd "$ROOT" && go build -o "$FT" .); then
    echo "FAIL go build"
    exit 1
fi

# --------------------------------------------------------------- server ----
PY=""
for cand in ${PYTHON:-} python3 /tmp/opencode/ftvenv/bin/python; do
    [ -n "$cand" ] || continue
    if "$cand" -c "import pyftpdlib" >/dev/null 2>&1; then
        PY="$cand"
        break
    fi
done
if [ -z "$PY" ]; then
    echo "SKIP: pyftpdlib not available (install it or set \$PYTHON)"
    exit 0
fi

mkdir -p "$SRV" "$M1" "$M2"

"$PY" "$ROOT/testdata/ftpsrv.py" "$SRV" "$PORT" &
SRV_PID=$!
up=0
for _ in $(seq 1 100); do
    if (exec 9<>"/dev/tcp/127.0.0.1/$PORT") 2>/dev/null; then
        exec 9>&- 9<&-
        up=1
        break
    fi
    sleep 0.1
done
if [ "$up" != 1 ]; then
    echo "FAIL ftp server did not come up on port $PORT"
    exit 1
fi
printf 'ftp server ready on 127.0.0.1:%s\n' "$PORT"

# ----------------------------------------------------------------- setup ----
printf 'v1\n' >"$M1/index.html"
printf 'shared\n' >"$M1/common.txt"
printf 'notes\n' >"$M1/notes.txt"

run_ft "$M1" remote add origin "$URL"
expect_exit "setup: machine1 adds remote" 0
run_ft "$M1" push
expect_exit "setup: machine1 seeds the server" 0

run_ft "$M2" remote add origin "$URL"
expect_exit "setup: machine2 adds remote" 0
run_ft "$M2" pull
expect_exit "setup: machine2 initial pull" 0
expect_out "setup: three files pulled" 'pulled 3 files'
expect_content "setup: index.html landed" "$M2/index.html" "v1"

# ------------------------------------- F4: direct server edit is detected ----
printf 'server-direct-edit\n' >"$SRV/index.html"
run_ft "$M2" pull
expect_exit "F4: pull after direct server edit exits 0" 0
expect_out "F4: exactly one file transferred" 'pulled 1 files'
expect_content "F4: server content downloaded" "$M2/index.html" "server-direct-edit"

# bring machine1 back in sync for the steps below
run_ft "$M1" pull
expect_exit "F4: machine1 re-syncs" 0

# -------------------------------- F9: diff sees the actual server ------------
printf 'direct-diff-edit\n' >"$SRV/index.html"
run_ft "$M2" diff
expect_exit "F9: diff exits 0" 0
expect_out "F9: diff reports the direct server edit" "modified:"
expect_out "F9: diff names the file" "index.html"

# restore the seeded content and re-sync so the later steps start clean
printf 'v1\n' >"$SRV/index.html"
run_ft "$M2" pull
expect_exit "F9: re-sync after the diff check" 0
expect_content "F9: back on the seeded content" "$M2/index.html" "v1"

# ------------------------------------- F1: conflict refuses, no clobber ------
printf 'from-machine1\n' >"$M1/index.html"
run_ft "$M1" push
expect_exit "F1: machine1 pushes a change" 0

printf 'precious-local-edit\n' >"$M2/index.html"
run_ft "$M2" pull
expect_exit "F1: conflicting pull exits 1" 1
expect_out "F1: conflict is reported" 'CONFLICT: index.html'
expect_out "F1: refusal is explained" 'pull refused'
expect_no_out "F1: no usage dump on conflict" 'Usage:'
expect_content "F1: local file untouched" "$M2/index.html" "precious-local-edit"

# back to the sync point (the last pull's content), then a clean fast-forward
printf 'v1\n' >"$M2/index.html"
run_ft "$M2" pull
expect_exit "F1: clean pull fast-forwards" 0
expect_content "F1: fast-forwarded to machine1's version" "$M2/index.html" "from-machine1"

# ------------------------------------------ F2: untracked files stay local ----
printf 'untracked\n' >"$M2/new.txt"
run_ft "$M2" pull
expect_exit "F2: pull with an untracked file exits 0" 0
expect_out "F2: nothing to pull" "already up to date"

run_ft "$M2" status
expect_out "F2: status reports the new file" "new file:    new.txt"

run_ft "$M2" pull
expect_exit "F2: second pull still exits 0" 0
run_ft "$M2" status
expect_out "F2: pull did not absorb the untracked file" "new file:    new.txt"

run_ft "$M2" push
expect_exit "F2: push uploads the untracked file" 0
expect_content "F2: file reached the server" "$SRV/new.txt" "untracked"
run_ft "$M2" status
expect_no_out "F2: status clean after push" "new file:"

# ------------------------------- F3/F7: backup captures local bytes, revert ----
printf 'precious-work\n' >"$M2/index.html" # dirty local edit
printf 'v-later\n' >"$M1/index.html"
run_ft "$M1" push
expect_exit "F3: machine1 pushes while machine2 is dirty" 0

run_ft "$M2" pull
expect_exit "F3: dirty pull refuses (exit 1)" 1

run_ft "$M2" pull --force --backup
expect_exit "F3: --force --backup exits 0" 0
expect_content "F3: remote version wins under --force" "$M2/index.html" "v-later"

backup_dir="$(newest_backup_dir "$M2" pre-pull)"
if [ -n "$backup_dir" ] && [ "$(cat "${backup_dir}files/index.html" 2>/dev/null)" = "precious-work" ]; then
    ok "F3: backup snapshot captured the local bytes"
else
    bad "F3: backup snapshot missing or wrong (${backup_dir:-none})"
fi
backup_name="$(basename "${backup_dir%/}")"

run_ft "$M2" revert "$backup_name"
expect_exit "F7: revert exits 0" 0
expect_content "F7: revert restores the bytes never pushed" "$M2/index.html" "precious-work"

# the restored bytes were never pushed, so the file is dirty against the sync
# point again and pull refuses rather than clobbering them
run_ft "$M2" pull
expect_exit "F7: pull refuses over unpushed restored work" 1
expect_content "F7: restored bytes survive the refused pull" "$M2/index.html" "precious-work"

run_ft "$M2" pull --force
expect_exit "F7: --force resumes the sync" 0
expect_content "F7: back on the pulled version" "$M2/index.html" "v-later"

# ------------------------------------------- F5: deletions both directions ----
rm "$M1/common.txt"
run_ft "$M1" push
expect_exit "F5: machine1 pushes an upstream delete" 0
expect_absent "F5: delete reached the server" "$SRV/common.txt"

run_ft "$M2" pull
expect_exit "F5: upstream delete pulls cleanly" 0
expect_absent "F5: local copy removed" "$M2/common.txt"
del_backup="$(newest_backup_dir "$M2" pre-pull)"
if [ -n "$del_backup" ] && [ "$(cat "${del_backup}files/common.txt" 2>/dev/null)" = "shared" ]; then
    ok "F5: deleted file was backed up before removal"
else
    bad "F5: deleted file backup missing (${del_backup:-none})"
fi

rm "$M2/notes.txt"
run_ft "$M2" pull
expect_exit "F5: local delete is not resurrected" 0
expect_absent "F5: file stays deleted" "$M2/notes.txt"
run_ft "$M2" status
expect_out "F5: status shows the local delete" "deleted:     notes.txt"

run_ft "$M2" push
expect_exit "F5: push propagates the local delete" 0
expect_absent "F5: server copy removed" "$SRV/notes.txt"

# ------------------------------------- F6: one failure ⇒ exit 2, no usage ----
printf 'a1\n' >"$M1/a.txt"
printf 'b1\n' >"$M1/b.txt"
printf 'c1\n' >"$M1/c.txt"
run_ft "$M1" push
expect_exit "F6: machine1 pushes three new files" 0
run_ft "$M2" pull
expect_exit "F6: machine2 pulls them" 0
expect_out "F6: three files pulled" 'pulled 3 files'

printf 'a2\n' >"$M1/a.txt"
printf 'b2\n' >"$M1/b.txt"
printf 'c2\n' >"$M1/c.txt"
run_ft "$M1" push
expect_exit "F6: machine1 changes all three" 0

chmod 000 "$SRV/b.txt" # server-side read failure
run_ft "$M2" pull
expect_exit "F6: partial failure exits 2" 2
expect_out "F6: failure is summarised" "failed 1"
expect_no_out "F6: no usage dump on failure" "Usage:"
expect_content "F6: a.txt updated despite the failure" "$M2/a.txt" "a2"
expect_content "F6: c.txt updated despite the failure" "$M2/c.txt" "c2"
expect_content "F6: b.txt left untouched" "$M2/b.txt" "b1"

chmod 644 "$SRV/b.txt"
run_ft "$M2" pull
expect_exit "F6: retry after fixing the server succeeds" 0
expect_content "F6: b.txt recovers" "$M2/b.txt" "b2"

# ---------------------------------------------- F8: pull converges (3x) ------
run_ft "$M2" pull
expect_exit "F8: pull #2 exits 0" 0
expect_out "F8: pull #2 transfers nothing" "already up to date"
expect_no_out "F8: pull #2 reports no transfers" "pulled "

run_ft "$M2" pull
expect_exit "F8: pull #3 exits 0" 0
expect_out "F8: pull #3 transfers nothing" "already up to date"
expect_no_out "F8: pull #3 reports no transfers" "pulled "

# ----------------------------------------------------------------- report ----
printf '\n'
if [ "$FAILS" -eq 0 ]; then
    printf 'ALL ACCEPTANCE TESTS PASSED\n'
    exit 0
fi
printf '%d ACCEPTANCE TEST(S) FAILED\n' "$FAILS"
exit 1

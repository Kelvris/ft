package cmd

import "fmt"

// pullSide is one side of the three-way comparison performed by pull:
// base (last known sync point), local (working tree), remote (server).
type pullSide struct {
	Present bool
	Hash    string // "" means "content unknown" — treated as never equal
}

type pullAction int

const (
	// actNone: up to date for this path; index untouched.
	actNone pullAction = iota
	// actDownload: take the remote version (fast-forward or --force).
	actDownload
	// actDeleteLocal: remote deleted it and the local copy is clean (or --force).
	actDeleteLocal
	// actAdvanceBase: content already agrees; only the recorded sync point moves.
	actAdvanceBase
	// actConflict: diverged; pull refuses unless --force.
	actConflict
	// actLeaveAlone: local-only change (or --no-delete); push carries it.
	actLeaveAlone
)

func (a pullAction) String() string {
	switch a {
	case actNone:
		return "none"
	case actDownload:
		return "download"
	case actDeleteLocal:
		return "delete"
	case actAdvanceBase:
		return "advance"
	case actConflict:
		return "conflict"
	case actLeaveAlone:
		return "leave"
	default:
		return "unknown"
	}
}

// pullPlan is the planned fate of a single path.
type pullPlan struct {
	Path          string
	Action        pullAction
	BasePresent   bool
	LocalPresent  bool
	RemotePresent bool
	LocalHash     string
	RemoteHash    string
	BaseHash      string
	Reason        string
}

type planOptions struct {
	Force    bool // let the remote win over local edits (implies a backup)
	NoDelete bool // keep local files that were deleted upstream
}

// planPull implements the decision matrix of PULL_FIX_PLAN.md §2. It is a
// pure function: no I/O, no globals — the entire git-like semantics of
// `ft pull` live here so they can be locked down by table tests.
//
//	base  = entry in .ft/index.json (last sync point)
//	local = hash of the working-tree file, or absent
//	remote = actual remote file state ("" hash = unknown ⇒ treated as changed)
func planPull(base, local, remote pullSide, opts planOptions) pullPlan {
	plan := pullPlan{
		BasePresent:   base.Present,
		LocalPresent:  local.Present,
		RemotePresent: remote.Present,
		BaseHash:      base.Hash,
		LocalHash:     local.Hash,
		RemoteHash:    remote.Hash,
	}
	plan.Action, plan.Reason = decide(base, local, remote, opts)
	return plan
}

func decide(base, local, remote pullSide, opts planOptions) (pullAction, string) {
	if !base.Present {
		return decideNoBase(local, remote, opts)
	}
	return decideWithBase(base, local, remote, opts)
}

// decideNoBase handles paths with no sync point yet (matrix rows 7–9).
func decideNoBase(local, remote pullSide, opts planOptions) (pullAction, string) {
	switch {
	case !local.Present && !remote.Present:
		return actNone, "not present anywhere"
	case !local.Present && remote.Present:
		return actDownload, "new upstream file"
	case local.Present && !remote.Present:
		// Untracked local file: never absorbed by pull (git doesn't stage
		// untracked files either), so it can still be pushed later.
		return actLeaveAlone, "untracked local file (push to upload)"
	default: // both present
		if sameHash(local.Hash, remote.Hash) {
			return actAdvanceBase, "local and remote already agree"
		}
		return conflictOrForce(remote.Present, opts, "created locally and changed upstream")
	}
}

// decideWithBase handles paths with a sync point (matrix rows 1–6 and 10).
func decideWithBase(base, local, remote pullSide, opts planOptions) (pullAction, string) {
	switch {
	case !local.Present && !remote.Present:
		// Deleted on both sides: drop the stale sync-point entry only.
		return actAdvanceBase, "deleted locally and upstream"

	case local.Present && sameHash(local.Hash, base.Hash):
		// Working tree matches the sync point → safe to fast-forward.
		switch {
		case !remote.Present:
			if opts.NoDelete {
				return actLeaveAlone, "deleted upstream (--no-delete keeps it)"
			}
			return actDeleteLocal, "deleted upstream"
		case sameHash(remote.Hash, base.Hash):
			return actNone, "up to date"
		default:
			// remote hash unknown ("") counts as changed here.
			return actDownload, "changed upstream"
		}

	case remote.Present && sameHash(remote.Hash, base.Hash):
		// Server still has the synced content → only the local side moved.
		if !local.Present {
			return actLeaveAlone, "deleted locally (push to propagate)"
		}
		return actLeaveAlone, "modified locally (push to propagate)"

	case local.Present && remote.Present && sameHash(local.Hash, remote.Hash):
		// Diverged from base but identical on both sides → just advance base.
		return actAdvanceBase, "local and remote agree"

	default:
		if !local.Present {
			return conflictOrForce(remote.Present, opts, "deleted locally, changed upstream")
		}
		return conflictOrForce(remote.Present, opts, "changed on both sides")
	}
}

func conflictOrForce(remotePresent bool, opts planOptions, reason string) (pullAction, string) {
	if !opts.Force {
		return actConflict, reason
	}
	if remotePresent {
		return actDownload, "forced: remote wins over local changes (" + reason + ")"
	}
	return actDeleteLocal, "forced: remote deletion wins (" + reason + ")"
}

// sameHash reports whether two content hashes are known and equal. An empty
// hash is unknown and therefore never equal to anything.
func sameHash(a, b string) bool {
	return a != "" && b != "" && a == b
}

// formatHash renders a hash for conflict reports.
func formatHash(h string) string {
	if h == "" {
		return "<unknown>"
	}
	if len(h) > 12 {
		return "sha256:" + h[:12] + "…"
	}
	return "sha256:" + h
}

// renderConflicts prints the git-style conflict report for a refused pull.
func renderConflicts(conflicts []pullPlan) {
	for _, c := range conflicts {
		local := "<absent>"
		if c.LocalPresent {
			local = formatHash(c.LocalHash)
			if c.BasePresent && !sameHash(c.LocalHash, c.BaseHash) {
				if c.BaseHash == "" {
					local += " (modified since last sync)"
				} else {
					local += fmt.Sprintf(" (modified since last sync, base %s)", formatHash(c.BaseHash))
				}
			} else if !c.BasePresent {
				local += " (new local file)"
			}
		}
		remote := "<absent>"
		if c.RemotePresent {
			remote = formatHash(c.RemoteHash)
		}
		fmt.Printf("CONFLICT: %s\n", c.Path)
		fmt.Printf("  local  %s\n", local)
		fmt.Printf("  remote %s\n", remote)
	}
	fmt.Println("hint: run `ft pull --force` to take the remote version (a backup is taken automatically),")
	fmt.Println("      or `ft push` to send yours.")
}

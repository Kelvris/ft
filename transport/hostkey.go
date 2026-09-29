package transport

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
	"golang.org/x/term"
)

// hostKeyPrompt asks the user whether to trust a host key that is not yet in
// known_hosts. A nil result means trusted; a non-nil result is the error to
// surface. It must never be consulted for a known host presenting a different
// key — that mismatch is handled before prompting.
type hostKeyPrompt func(hostname string, key ssh.PublicKey) error

// Decisions are memoized per process so concurrent workers (push/pull
// connect one transport each) prompt only once for the same host key.
var (
	hostTrustMu    sync.Mutex
	hostTrustCache = map[string]error{}
)

// trustedHostKeyCallback wraps a known_hosts callback with ssh-style
// trust-on-first-use: unknown hosts are offered to the user and, on
// acceptance, appended to knownHostsPath. A host whose key does not match the
// recorded one (possible MITM) or is revoked is rejected outright.
func trustedHostKeyCallback(base ssh.HostKeyCallback, knownHostsPath string, prompt hostKeyPrompt) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := base(hostname, remote, key)
		if err == nil {
			return nil
		}
		var kErr *knownhosts.KeyError
		switch {
		case !errors.As(err, &kErr):
			return err // revoked or otherwise not a "unknown/mismatch" error
		case len(kErr.Want) > 0:
			return fmt.Errorf(
				"host key mismatch for %s: the server presented a different key than the one in %s "+
					"(possible interception — if the server was legitimately reinstalled, remove the old entry and reconnect): %w",
				knownhosts.Normalize(hostname), knownHostsPath, err)
		}
		// Host is unknown (Want empty) — the documented case for an
		// interactive prompt.
		cacheKey := knownhosts.Normalize(hostname) + " " + ssh.FingerprintSHA256(key)
		hostTrustMu.Lock()
		decided, seen := hostTrustCache[cacheKey]
		hostTrustMu.Unlock()
		if !seen {
			decided = trustDecision(prompt, hostname, key, knownHostsPath)
			hostTrustMu.Lock()
			hostTrustCache[cacheKey] = decided
			hostTrustMu.Unlock()
		}
		if decided != nil {
			return decided
		}
		return appendKnownHost(knownHostsPath, hostname, key)
	}
}

// trustDecision runs the prompt (or the no-prompt fallback) exactly once per
// host key and stores its outcome.
func trustDecision(prompt hostKeyPrompt, hostname string, key ssh.PublicKey, knownHostsPath string) error {
	if prompt != nil {
		return prompt(hostname, key)
	}
	return fmt.Errorf("host key for %s is unknown (%s %s); to trust it, run: %s",
		knownhosts.Normalize(hostname), hostKeyAlgLabel(key.Type()), ssh.FingerprintSHA256(key),
		keyscanHint(hostname, knownHostsPath))
}

// interactiveHostKeyPrompt prints ssh's trust-on-first-use question to out
// and reads the answer from in. Anything other than "yes" (also EOF or a
// non-interactive stdin) refuses, with the exact command to add the key
// manually.
func interactiveHostKeyPrompt(out io.Writer, in *os.File, knownHostsPath string) hostKeyPrompt {
	return func(hostname string, key ssh.PublicKey) error {
		shown := knownhosts.Normalize(hostname)
		hint := keyscanHint(hostname, knownHostsPath)
		if !term.IsTerminal(int(in.Fd())) {
			return fmt.Errorf("host key for %s is unknown (%s %s); to trust it, run: %s",
				shown, hostKeyAlgLabel(key.Type()), ssh.FingerprintSHA256(key), hint)
		}
		fmt.Fprintf(out, "The authenticity of host '%s' can't be established.\n", shown)
		fmt.Fprintf(out, "%s key fingerprint is %s.\n", hostKeyAlgLabel(key.Type()), ssh.FingerprintSHA256(key))
		fmt.Fprint(out, "Are you sure you want to continue connecting (yes/no)? ")
		line, err := readLine(in)
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("reading host key confirmation for %s: %w", shown, err)
		}
		if !strings.EqualFold(strings.TrimSpace(line), "yes") {
			return fmt.Errorf("host key for %s was not accepted; to trust it later, run: %s", shown, hint)
		}
		return nil
	}
}

// appendKnownHost records key for hostname in knownHostsPath, creating the
// file (and ~/.ssh) when needed. Entries are written in known_hosts form:
// "[host]:port" for non-default ports, bare host for port 22.
func appendKnownHost(knownHostsPath, hostname string, key ssh.PublicKey) error {
	if err := os.MkdirAll(dirOf(knownHostsPath), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dirOf(knownHostsPath), err)
	}
	f, err := os.OpenFile(knownHostsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("opening %s: %w", knownHostsPath, err)
	}
	defer f.Close()
	line := knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key)
	if _, err := f.WriteString(line + "\n"); err != nil {
		return fmt.Errorf("writing host key to %s: %w", knownHostsPath, err)
	}
	return nil
}

func dirOf(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[:i]
	}
	return "."
}

// keyscanHint returns the exact ssh-keyscan command that adds hostname's key
// to knownHostsPath, preserving a non-default port.
func keyscanHint(hostname, knownHostsPath string) string {
	host, port := hostname, ""
	if h, p, err := net.SplitHostPort(hostname); err == nil {
		host, port = h, p
	}
	if port == "" || port == "22" {
		return fmt.Sprintf("ssh-keyscan %s >> %s", host, knownHostsPath)
	}
	return fmt.Sprintf("ssh-keyscan -p %s %s >> %s", port, host, knownHostsPath)
}

// hostKeyAlgLabel turns key algorithm names into the labels ssh prints in
// its fingerprint line (ssh-ed25519 → ED25519).
func hostKeyAlgLabel(alg string) string {
	switch {
	case alg == "ssh-ed25519":
		return "ED25519"
	case alg == "ssh-rsa":
		return "RSA"
	case strings.HasPrefix(alg, "ecdsa-sha2-"):
		return "ECDSA"
	}
	return alg
}

// readLine reads one answer up to '\n' (the cooked-mode line terminator)
// byte-by-byte so it never buffers past the answer — a bufio reader would
// swallow input meant for the prompts that follow, e.g. in the setup wizard.
// A trailing '\r' (from terminals delivering CRLF) is stripped.
func readLine(r io.Reader) (string, error) {
	var sb strings.Builder
	var b [1]byte
	for {
		n, err := r.Read(b[:])
		if n > 0 {
			if b[0] == '\n' {
				return strings.TrimSuffix(sb.String(), "\r"), nil
			}
			sb.WriteByte(b[0])
			continue
		}
		if err != nil {
			return strings.TrimSuffix(sb.String(), "\r"), err
		}
	}
}

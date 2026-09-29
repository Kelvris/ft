package transport

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func testKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("converting key: %v", err)
	}
	return k
}

// freshTrustState clears the per-process prompt memo so tests don't leak
// decisions into each other.
func freshTrustState(t *testing.T) {
	t.Helper()
	hostTrustMu.Lock()
	saved := hostTrustCache
	hostTrustCache = map[string]error{}
	hostTrustMu.Unlock()
	t.Cleanup(func() {
		hostTrustMu.Lock()
		hostTrustCache = saved
		hostTrustMu.Unlock()
	})
}

func unknownBase() ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		return &knownhosts.KeyError{}
	}
}

func TestTrustedHostKeyAcceptsKnownHostWithoutPrompt(t *testing.T) {
	freshTrustState(t)
	called := false
	base := func(hostname string, remote net.Addr, key ssh.PublicKey) error { return nil }
	cb := trustedHostKeyCallback(base, "kh", func(string, ssh.PublicKey) error {
		called = true
		return nil
	})
	if err := cb("h:2022", nil, testKey(t)); err != nil {
		t.Fatalf("known host: %v", err)
	}
	if called {
		t.Fatal("prompt must not run for a host that passes known_hosts")
	}
}

func TestTrustedHostKeyPromptsUnknownAndAppends(t *testing.T) {
	freshTrustState(t)
	kh := filepath.Join(t.TempDir(), "known_hosts")
	key := testKey(t)
	prompted := 0
	cb := trustedHostKeyCallback(unknownBase(), kh, func(hostname string, k ssh.PublicKey) error {
		prompted++
		if hostname != "example.com:2022" {
			t.Errorf("hostname = %q", hostname)
		}
		return nil
	})
	if err := cb("example.com:2022", nil, key); err != nil {
		t.Fatalf("accepted unknown host: %v", err)
	}
	data, err := os.ReadFile(kh)
	if err != nil {
		t.Fatalf("reading known_hosts: %v", err)
	}
	want := knownhosts.Line([]string{"[example.com]:2022"}, key)
	if strings.TrimSpace(string(data)) != want {
		t.Errorf("known_hosts = %q, want %q", strings.TrimSpace(string(data)), want)
	}
}

func TestTrustedHostKeyRejectionKeepsFileUntouched(t *testing.T) {
	freshTrustState(t)
	kh := filepath.Join(t.TempDir(), "known_hosts")
	reject := errors.New("user said no")
	cb := trustedHostKeyCallback(unknownBase(), kh, func(string, ssh.PublicKey) error { return reject })
	err := cb("example.com:2022", nil, testKey(t))
	if !errors.Is(err, reject) {
		t.Fatalf("err = %v, want rejection", err)
	}
	if _, statErr := os.Stat(kh); !os.IsNotExist(statErr) {
		t.Fatal("known_hosts must not be created on rejection")
	}
}

func TestTrustedHostKeyMismatchNeverPrompts(t *testing.T) {
	freshTrustState(t)
	kh := filepath.Join(t.TempDir(), "known_hosts")
	base := func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		return &knownhosts.KeyError{Want: []knownhosts.KnownKey{{Key: testKey(t)}}}
	}
	prompted := false
	cb := trustedHostKeyCallback(base, kh, func(string, ssh.PublicKey) error {
		prompted = true
		return nil
	})
	err := cb("example.com:2022", nil, testKey(t))
	if err == nil {
		t.Fatal("key mismatch must fail")
	}
	if !strings.Contains(err.Error(), "host key mismatch") {
		t.Errorf("err = %v, want mismatch explanation", err)
	}
	if prompted {
		t.Fatal("prompt must never run on key mismatch (possible MITM)")
	}
	if _, statErr := os.Stat(kh); !os.IsNotExist(statErr) {
		t.Fatal("known_hosts must not be written on mismatch")
	}
}

func TestTrustedHostKeyPromptRunsOncePerHostKey(t *testing.T) {
	freshTrustState(t)
	kh := filepath.Join(t.TempDir(), "known_hosts")
	key := testKey(t)
	prompted := 0
	cb := trustedHostKeyCallback(unknownBase(), kh, func(string, ssh.PublicKey) error {
		prompted++
		return nil
	})
	for i := 0; i < 3; i++ {
		// Each call uses a fresh base (like a reconnect that re-read the
		// file before it was appended) — the decision must be reused.
		if err := cb("example.com:2022", nil, key); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if prompted != 1 {
		t.Errorf("prompt ran %d times, want 1", prompted)
	}
}

func TestInteractivePromptRefusesWithoutTTYAndGivesKeyscanCommand(t *testing.T) {
	var out bytes.Buffer
	// A regular file is never a terminal, which models piped/CI stdin.
	f, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	p := interactiveHostKeyPrompt(&out, f, "/home/u/.ssh/known_hosts")
	err = p("example.com:2022", testKey(t))
	if err == nil {
		t.Fatal("non-interactive stdin must refuse")
	}
	if !strings.Contains(err.Error(), "ssh-keyscan -p 2022 example.com >> /home/u/.ssh/known_hosts") {
		t.Errorf("err = %v, want keyscan hint with port", err)
	}
	if out.Len() != 0 {
		t.Errorf("no question should be printed without a tty, got %q", out.String())
	}
}

func TestKeyscanHint(t *testing.T) {
	cases := []struct{ host, want string }{
		{"example.com:2022", "ssh-keyscan -p 2022 example.com >> kh"},
		{"example.com:22", "ssh-keyscan example.com >> kh"},
		{"example.com", "ssh-keyscan example.com >> kh"},
	}
	for _, c := range cases {
		if got := keyscanHint(c.host, "kh"); got != c.want {
			t.Errorf("keyscanHint(%q) = %q, want %q", c.host, got, c.want)
		}
	}
}

func TestHostKeyAlgLabel(t *testing.T) {
	for alg, want := range map[string]string{
		"ssh-ed25519":           "ED25519",
		"ssh-rsa":               "RSA",
		"ecdsa-sha2-nistp256":   "ECDSA",
		"ssh-unknown-algorithm": "ssh-unknown-algorithm",
	} {
		if got := hostKeyAlgLabel(alg); got != want {
			t.Errorf("hostKeyAlgLabel(%q) = %q, want %q", alg, got, want)
		}
	}
}

func TestReadLineStopsAtNewlineWithoutOverBuffering(t *testing.T) {
	// "\r\n" answer plus extra input meant for a later prompt must survive.
	in := strings.NewReader("yes\r\nlater-input")
	got, err := readLine(in)
	if err != nil {
		t.Fatalf("readLine: %v", err)
	}
	if got != "yes" {
		t.Errorf("readLine = %q, want %q", got, "yes")
	}
	rest, _ := io.ReadAll(in)
	if string(rest) != "later-input" {
		t.Errorf("leftover = %q, want later-input untouched", rest)
	}
}

func TestReadLineEOFReturnsPartialAnswer(t *testing.T) {
	got, err := readLine(strings.NewReader("yes"))
	if !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want io.EOF", err)
	}
	if got != "yes" {
		t.Errorf("readLine = %q, want yes", got)
	}
}

func TestAppendKnownHostNormalizesPort(t *testing.T) {
	kh := filepath.Join(t.TempDir(), "known_hosts")
	key := testKey(t)
	if err := appendKnownHost(kh, "example.com:2022", key); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(kh)
	if !strings.HasPrefix(strings.TrimSpace(string(data)), "[example.com]:2022 ") {
		t.Errorf("entry = %q, want [host]:port form", data)
	}
	// Default port entries are stored without brackets/port.
	if got := knownhosts.Normalize("example.com:22"); got != "example.com" {
		t.Errorf("Normalize(example.com:22) = %q", got)
	}
}

func TestKnownHostsCallbackTreatsMissingFileAsUnknownHosts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cb, path, err := knownHostsCallback()
	if err != nil {
		t.Fatalf("missing known_hosts must not fail: %v", err)
	}
	if filepath.Dir(path) != filepath.Join(os.Getenv("HOME"), ".ssh") {
		t.Errorf("path = %q", path)
	}
	err = cb("example.com:22", nil, testKey(t))
	var kErr *knownhosts.KeyError
	if !errors.As(err, &kErr) || len(kErr.Want) != 0 {
		t.Fatalf("missing file must yield unknown-host KeyError, got %v", err)
	}
}

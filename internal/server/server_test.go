package server

import (
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"bethoven/internal/clock"
	"bethoven/internal/db"
	"bethoven/internal/service"
)

const testInvite = "secret"

// newClientKey generates an ephemeral ed25519 keypair for a test client and
// returns an auth method plus the key's SHA256 fingerprint.
func newClientKey(t *testing.T) (gossh.AuthMethod, string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	signer, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	return gossh.PublicKeys(signer), gossh.FingerprintSHA256(signer.PublicKey())
}

// startServer boots a real BEThoven SSH server (TUI and all) backed by a fresh
// temp DB, and returns its address plus the service for test setup.
func startServer(t *testing.T) (string, *service.Service) {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	store := db.NewStore(conn)
	tid, err := store.CreateTournament("Test Cup", true, time.Now().UTC())
	if err != nil {
		t.Fatalf("tournament: %v", err)
	}
	svc := service.New(store, clock.Real{}, testInvite, nil, tid)

	srv, err := New(svc, "", filepath.Join(t.TempDir(), "host_key"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().String(), svc
}

// renderFirstScreen connects, opens a PTY shell, lets the TUI render, then
// quits and returns everything the server sent.
func renderFirstScreen(t *testing.T, addr string, auth gossh.AuthMethod) string {
	t.Helper()
	client, err := gossh.Dial("tcp", addr, &gossh.ClientConfig{
		User:            "anyone", // ignored: identity comes from the key
		Auth:            []gossh.AuthMethod{auth},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	sess, err := client.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer sess.Close()

	stdin, err := sess.StdinPipe()
	if err != nil {
		t.Fatalf("stdin: %v", err)
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout: %v", err)
	}
	if err := sess.RequestPty("xterm", 40, 100, gossh.TerminalModes{}); err != nil {
		t.Fatalf("request pty: %v", err)
	}
	if err := sess.Shell(); err != nil {
		t.Fatalf("shell: %v", err)
	}

	// Let the initial frame render, then quit so the read can complete.
	time.Sleep(400 * time.Millisecond)
	stdin.Write([]byte("q"))
	out, _ := io.ReadAll(stdout)
	sess.Wait()
	return string(out)
}

// TestUnknownKeyShowsRegistration: a new key lands on the registration screen.
func TestUnknownKeyShowsRegistration(t *testing.T) {
	addr, _ := startServer(t)
	auth, _ := newClientKey(t)

	out := renderFirstScreen(t, addr, auth)
	if !strings.Contains(out, "BEThoven") {
		t.Errorf("expected app title; got %q", out)
	}
	if !strings.Contains(out, "new here") {
		t.Errorf("expected registration prompt; got %q", out)
	}
}

// TestKnownKeyShowsMenu: a registered key goes straight to the main menu.
func TestKnownKeyShowsMenu(t *testing.T) {
	addr, svc := startServer(t)
	auth, fp := newClientKey(t)

	if _, err := svc.Register(fp, testInvite, "Antonio"); err != nil {
		t.Fatalf("pre-register: %v", err)
	}

	out := renderFirstScreen(t, addr, auth)
	if !strings.Contains(out, "Place / edit bets") {
		t.Errorf("expected main menu; got %q", out)
	}
	if !strings.Contains(out, "Antonio") {
		t.Errorf("expected display name in menu; got %q", out)
	}
}

// TestDistinctKeysDistinctIdentity: two keys are two identities.
func TestDistinctKeysDistinctIdentity(t *testing.T) {
	addr, svc := startServer(t)
	authA, fpA := newClientKey(t)
	_, fpB := newClientKey(t)

	if fpA == fpB {
		t.Fatal("two generated keys collided on fingerprint")
	}
	if _, err := svc.Register(fpA, testInvite, "Alice"); err != nil {
		t.Fatalf("register A: %v", err)
	}
	// A is known -> menu with Alice; B was never registered.
	out := renderFirstScreen(t, addr, authA)
	if !strings.Contains(out, "Alice") {
		t.Errorf("client A should see Alice's menu; got %q", out)
	}
	if u, _ := svc.Lookup(fpB); u != nil {
		t.Errorf("client B should not exist yet, got %+v", u)
	}
}

package tui

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

// TestItermFileTransfer asserts the OSC 1337 file-transfer escape is well-formed:
// the right prefix, base64 name/data, an inline=0 (download, not display) arg, a
// size that matches the payload, and a BEL terminator.
func TestItermFileTransfer(t *testing.T) {
	data := []byte("\x89PNG\r\n\x1a\n-some-bytes-")
	name := "ada-lovelace-bethoven-2026.png"
	got := string(itermFileTransfer(name, data))

	if !strings.HasPrefix(got, "\x1b]1337;File=name=") {
		t.Fatalf("missing OSC 1337 File prefix: %q", got[:min(24, len(got))])
	}
	if !strings.HasSuffix(got, "\x07") {
		t.Fatal("escape must end with BEL")
	}
	if !strings.Contains(got, ";size="+fmt.Sprint(len(data))+";") {
		t.Errorf("size arg must equal payload length %d; got %q", len(data), got)
	}
	if !strings.Contains(got, ";inline=0:") {
		t.Error("must request a download (inline=0), not an inline display")
	}
	wantName := base64.StdEncoding.EncodeToString([]byte(name))
	if !strings.Contains(got, "name="+wantName+";") {
		t.Errorf("filename must be base64-encoded as %q", wantName)
	}
	// The body after the ':' separator must be the base64 of the data, ending at BEL.
	body := strings.TrimSuffix(got[strings.Index(got, ":")+1:], "\x07")
	wantData := base64.StdEncoding.EncodeToString(data)
	if body != wantData {
		t.Errorf("payload = %q; want base64 %q", body, wantData)
	}
}

// TestCardFilename covers slugging a display name into a safe download filename,
// including the empty/garbage fallback.
func TestCardFilename(t *testing.T) {
	cases := map[string]string{
		"Ada Lovelace":  "ada-lovelace-bethoven-2026.png",
		"  José  #1!! ": "jos-1-bethoven-2026.png",
		"日本語":           "player-bethoven-2026.png",
		"already-clean": "already-clean-bethoven-2026.png",
	}
	for in, want := range cases {
		if got := cardFilename(in); got != want {
			t.Errorf("cardFilename(%q) = %q; want %q", in, got, want)
		}
	}
}

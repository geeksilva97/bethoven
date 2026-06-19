package ai

import "testing"

// TestSanitizeText covers the ANSI-injection boundary for untrusted model text:
// escape sequences and control codes are stripped, whitespace collapses, and
// normal text and non-ASCII printables survive.
func TestSanitizeText(t *testing.T) {
	// 8-bit CSI introducer (0x9b) followed by a sequence — must be consumed whole.
	csi := string(rune(0x9b)) + "31m"
	// Lone C1 control bytes (non-CSI) — must be dropped.
	c1 := "a" + string(rune(0x80)) + "b" + string(rune(0x84)) + "c"

	cases := []struct{ name, in, want string }{
		{"plain", "Brazil are clear favourites", "Brazil are clear favourites"},
		{"ansi escape", "red\x1b[31mtext\x1b[0m done", "redtext done"},
		{"clear screen + cursor", "a\x1b[2J\x1b[Hb", "ab"},
		{"esc without bracket", "a\x1bcb", "ab"},
		{"control chars", "line1\nline2\ttabbed\r\n", "line1 line2 tabbed"},
		{"collapse spaces", "too    many     spaces", "too many spaces"},
		{"null + bell", "x\x00y\x07z", "xyz"},
		{"c1 controls dropped", c1, "abc"},
		{"8-bit CSI sequence", "x" + csi + "y", "xy"},
		{"unicode printable kept", "Curaçao 2–0 Iñtërnâtiônàl", "Curaçao 2–0 Iñtërnâtiônàl"},
		{"trim edges", "  \x1b[1m  hi  ", "hi"},
		{"confidence enum", "medium", "medium"},
	}
	for _, c := range cases {
		if got := sanitizeText(c.in); got != c.want {
			t.Errorf("%s: sanitizeText(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

package auth

import "testing"

func TestValidInvite(t *testing.T) {
	tests := []struct {
		name             string
		supplied, config string
		want             bool
	}{
		{"match", "s3cret", "s3cret", true},
		{"mismatch", "nope", "s3cret", false},
		{"empty config rejects everything (fail closed)", "", "", false},
		{"empty config rejects any supplied", "anything", "", false},
		{"empty supplied vs real config", "", "s3cret", false},
		{"case sensitive", "Secret", "secret", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValidInvite(tc.supplied, tc.config); got != tc.want {
				t.Errorf("ValidInvite(%q,%q)=%v, want %v", tc.supplied, tc.config, got, tc.want)
			}
		})
	}
}

func TestIsAdmin(t *testing.T) {
	admins := []string{"SHA256:a", "SHA256:b"}
	if !IsAdmin("SHA256:b", admins) {
		t.Error("expected SHA256:b to be admin")
	}
	if IsAdmin("SHA256:c", admins) {
		t.Error("SHA256:c should not be admin")
	}
	if IsAdmin("SHA256:a", nil) {
		t.Error("empty allowlist should grant nobody admin")
	}
}

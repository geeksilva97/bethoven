// Package auth turns SSH public keys into stable identities and answers the
// two authorization questions BEThoven asks: "is this an admin?" and "is this
// invite code valid?".
package auth

import (
	gossh "golang.org/x/crypto/ssh"
)

// Fingerprint returns the SHA256 fingerprint of a public key (e.g.
// "SHA256:abc123..."). This is the stable identifier we store per user — it
// cannot be forged without the matching private key.
func Fingerprint(key gossh.PublicKey) string {
	return gossh.FingerprintSHA256(key)
}

// IsAdmin reports whether a fingerprint is in the configured admin allowlist.
func IsAdmin(fingerprint string, admins []string) bool {
	for _, a := range admins {
		if a == fingerprint {
			return true
		}
	}
	return false
}

// ValidInvite reports whether the supplied code matches the configured invite
// code. An empty configured code rejects everything (fail closed).
func ValidInvite(supplied, configured string) bool {
	return configured != "" && supplied == configured
}

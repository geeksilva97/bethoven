package server

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"

	gossh "golang.org/x/crypto/ssh"
)

// ensureHostKey loads the server's persistent ed25519 host key from path,
// generating and writing one on first run. A stable host key is what lets
// clients pin the server's identity (TOFU) and avoids spurious
// "host key changed" warnings across redeploys.
func ensureHostKey(path string) (gossh.Signer, error) {
	if data, err := os.ReadFile(path); err == nil {
		signer, err := gossh.ParsePrivateKey(data)
		if err != nil {
			return nil, fmt.Errorf("parse host key %s: %w", path, err)
		}
		return signer, nil
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate host key: %w", err)
	}
	block, err := gossh.MarshalPrivateKey(priv, "")
	if err != nil {
		return nil, fmt.Errorf("marshal host key: %w", err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		return nil, fmt.Errorf("write host key %s: %w", path, err)
	}
	signer, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		return nil, fmt.Errorf("signer from host key: %w", err)
	}
	return signer, nil
}

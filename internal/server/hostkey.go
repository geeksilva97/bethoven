package server

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"

	gossh "golang.org/x/crypto/ssh"
)

// hostKeyPEM loads the server's persistent ed25519 host key from path as PEM,
// generating and writing one on first run. A stable host key is what lets
// clients pin the server's identity (TOFU) and avoids spurious
// "host key changed" warnings across redeploys.
//
// We return PEM (not a Signer) so it can be passed into wish.NewServer as a
// construction option — that keeps wish from generating its own throwaway
// "id_ed25519" key in the working directory.
func hostKeyPEM(path string) ([]byte, error) {
	if data, err := os.ReadFile(path); err == nil {
		// Validate it parses before trusting it.
		if _, err := gossh.ParsePrivateKey(data); err != nil {
			return nil, fmt.Errorf("parse host key %s: %w", path, err)
		}
		return data, nil
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate host key: %w", err)
	}
	block, err := gossh.MarshalPrivateKey(priv, "")
	if err != nil {
		return nil, fmt.Errorf("marshal host key: %w", err)
	}
	data := pem.EncodeToMemory(block)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return nil, fmt.Errorf("write host key %s: %w", path, err)
	}
	return data, nil
}

// Package server wires the wish SSH server to BEThoven's TUI. It is kept
// deliberately thin: it authenticates by public key, resolves the connecting
// key to an identity, and hands control to the Bubble Tea program. All rules
// live in the service layer.
package server

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/charmbracelet/wish/activeterm"
	bm "github.com/charmbracelet/wish/bubbletea"
	"github.com/charmbracelet/wish/logging"

	"bethoven/internal/auth"
	"bethoven/internal/service"
	"bethoven/internal/tui"
)

// New builds a wish SSH server bound to the given service, listening on addr
// (host:port) with a persistent host key at hostKeyPath.
func New(svc *service.Service, addr, hostKeyPath string) (*ssh.Server, error) {
	signer, err := ensureHostKey(hostKeyPath)
	if err != nil {
		return nil, err
	}

	srv, err := wish.NewServer(
		wish.WithAddress(addr),
		// Accept every public key at the transport layer; identity and
		// authorization are resolved per-session from the key fingerprint.
		ssh.PublicKeyAuth(func(ssh.Context, ssh.PublicKey) bool { return true }),
		// Drop idle/abandoned connections so they can't pile up. 15 minutes is
		// generous for filling in a bet form.
		wish.WithIdleTimeout(15*time.Minute),
		wish.WithMiddleware(
			bm.Middleware(teaHandler(svc)),
			activeterm.Middleware(), // require an interactive PTY
			logging.Middleware(),
		),
	)
	if err != nil {
		return nil, err
	}
	srv.AddHostKey(signer)
	return srv, nil
}

// teaHandler resolves the connecting key to a user and builds the session's
// Bubble Tea model. An unknown key yields a nil user, which the TUI turns into
// the registration flow.
func teaHandler(svc *service.Service) bm.Handler {
	return func(s ssh.Session) (tea.Model, []tea.ProgramOption) {
		fp := "(no key)"
		if pk := s.PublicKey(); pk != nil {
			fp = auth.Fingerprint(pk)
		}
		isAdminKey := svc.IsAdmin(fp)
		user, err := svc.Resolve(fp) // nil + err for unknown keys
		if err != nil {
			user = nil
		}
		model := tui.New(svc, fp, isAdminKey, user)
		return model, []tea.ProgramOption{tea.WithAltScreen()}
	}
}

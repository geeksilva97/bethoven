package server

import (
	"net"
	"sync"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
)

// Default resource bounds. A team pool is tiny; these only exist to blunt abuse
// of a public :2222 endpoint, where accept-all pubkey means every connection
// gets a crypto handshake + goroutine and idle sessions tie up the single
// SQLite writer.
const (
	maxConcurrentSessions = 64 // bounded by the single-writer DB anyway
	// MaxConcurrentConns is the listener-level cap on in-flight connections.
	MaxConcurrentConns = 256
)

// limitSessions caps concurrent interactive sessions. Beyond the cap a new
// session gets a friendly message and is closed, so a flood can't exhaust
// goroutines or starve DB access. Placed as the outermost middleware so it
// gates before a PTY or TUI program is set up.
func limitSessions(max int) wish.Middleware {
	sem := make(chan struct{}, max)
	return func(next ssh.Handler) ssh.Handler {
		return func(s ssh.Session) {
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
				next(s)
			default:
				wish.Println(s, "BEThoven is busy right now — please reconnect in a moment.")
			}
		}
	}
}

// limitListener wraps a net.Listener to cap the number of simultaneously open
// connections, bounding pre-session handshake work too. A minimal stand-in for
// golang.org/x/net/netutil.LimitListener (avoids the extra dependency).
type limitListener struct {
	net.Listener
	sem chan struct{}
}

// LimitedListen listens on addr and caps concurrent connections at maxConns.
func LimitedListen(addr string, maxConns int) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	return &limitListener{Listener: ln, sem: make(chan struct{}, maxConns)}, nil
}

func (l *limitListener) Accept() (net.Conn, error) {
	l.sem <- struct{}{} // block until a slot frees
	c, err := l.Listener.Accept()
	if err != nil {
		<-l.sem
		return nil, err
	}
	return &limitConn{Conn: c, release: l.releaseOnce()}, nil
}

func (l *limitListener) releaseOnce() func() {
	var once sync.Once
	return func() { once.Do(func() { <-l.sem }) }
}

type limitConn struct {
	net.Conn
	release func()
}

func (c *limitConn) Close() error {
	err := c.Conn.Close()
	c.release()
	return err
}

package server

import (
	"net"
	"testing"
	"time"
)

// TestLimitListenerCapsConnections verifies Accept blocks once the connection
// cap is reached and resumes when an accepted connection is closed.
func TestLimitListenerCapsConnections(t *testing.T) {
	ln, err := LimitedListen("127.0.0.1:0", 1)
	if err != nil {
		t.Fatalf("LimitedListen: %v", err)
	}
	defer ln.Close()
	addr := ln.Addr().String()

	c1, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial 1: %v", err)
	}
	defer c1.Close()
	a1, err := ln.Accept() // takes the only slot
	if err != nil {
		t.Fatalf("accept 1: %v", err)
	}

	c2, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial 2: %v", err)
	}
	defer c2.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		a, _ := ln.Accept()
		accepted <- a
	}()

	// With the slot full, the second Accept must not return yet.
	select {
	case <-accepted:
		t.Fatal("second Accept returned while at the connection cap")
	case <-time.After(150 * time.Millisecond):
	}

	// Closing the first accepted conn frees the slot.
	a1.Close()
	select {
	case a2 := <-accepted:
		if a2 != nil {
			a2.Close()
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second Accept did not proceed after a slot freed")
	}
}

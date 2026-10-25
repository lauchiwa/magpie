package gateway

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// fork: a gateway that handed its socket over takes no connection from it
// after: one waiting is left for the magpie handed over to, not accepted
// here to be dropped by this one's Shutdown (handover_fork.go).
func TestHandedOffGatewayAcceptsNoMore(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	// a connection already waiting in the socket's queue
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s := &Server{}
	s.ln = ln
	s.HandOff()
	got, err := ln.Accept()
	if err == nil {
		got.Close()
		t.Fatal("accepted a connection after handing the socket over")
	}
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("accept after the handover: %v, want a deadline passed", err)
	}
}

// fork: after the handover each reply ends its connection, so a client's
// next request goes to the magpie handed over to, not on a keep-alive
// connection this one's Shutdown can close under it (EOF for a POST,
// TestRestartHandsTheGatewayOver on ubuntu CI); before it, connections
// are kept.
func TestHandedOffGatewayEndsEachConnection(t *testing.T) {
	s := &Server{}
	h := s.closeWhenHandedOff(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	ask := func() string {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/v1/chat/completions", nil))
		return w.Header().Get("Connection")
	}
	if got := ask(); got != "" {
		t.Fatalf("before the handover: Connection %q, want the connection kept", got)
	}
	s.HandOff()
	if got := ask(); got != "close" {
		t.Fatalf("after the handover: Connection %q, want close", got)
	}
}

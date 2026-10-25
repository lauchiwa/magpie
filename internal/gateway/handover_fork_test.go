package gateway

import (
	"errors"
	"net"
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

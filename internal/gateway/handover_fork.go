package gateway

import (
	"net"
	"time"
)

// fork: stopAccepting has a gateway that handed its socket over stop
// accepting on it at once, as handover.go says it does: until its end
// closed the listener, both magpies accepted on the one socket, and a
// connection the old one took just before its Shutdown was closed with no
// answer (net/http drops a connection whose request it hasn't read once
// shutting down), so its client read EOF. A deadline in the past ends
// this process's Accept with a timeout, which net/http and tlsListener
// take as temporary and wait out until the Shutdown; the magpie handed
// over to accepts on its own copy of the socket, which no deadline here
// reaches.
func (s *Server) stopAccepting() {
	s.lnMu.Lock()
	ln := s.ln
	s.lnMu.Unlock()
	if t, ok := ln.(*tlsListener); ok {
		ln = t.Listener
	}
	if tl, ok := ln.(*net.TCPListener); ok {
		tl.SetDeadline(time.Unix(1, 0))
		// an Accept under way still takes a connection that arrives
		// before its goroutine runs again (the runtime lets the readiness
		// it is woken with win over the deadline). Accepts on one socket
		// take turns, so this one starts once that one has returned, and
		// ends at once on the deadline: nothing here accepts after
		if c, err := tl.Accept(); err == nil {
			c.Close()
		}
	}
}

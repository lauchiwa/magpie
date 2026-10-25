package gateway

import (
	"net"
	"net/http"
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

// handedOffLinger is how long a gateway that handed its socket over goes
// on answering, its background work ended, before its Shutdown: a client
// whose keep-alive connection is idle here when Shutdown closes it, and
// sends on it at that moment, reads EOF for a request net/http won't
// retry (a POST), and a request read once shutting down is dropped
// unanswered. Each reply meanwhile says Connection: close, so a client
// asking again within it is moved to a connection of the new magpie's.
var handedOffLinger = 2 * time.Second

// lingerHandedOff, at the gateway's end, waits handedOffLinger when it
// handed its socket over; an ordinary end goes on to its Shutdown at once.
func (s *Server) lingerHandedOff() {
	if s.handedOff.Load() {
		time.Sleep(handedOffLinger)
	}
}

// closeWhenHandedOff has every reply after the handover end its
// connection (HTTP/2: a GOAWAY), so the client's next request opens one
// to the magpie handed over to rather than reusing this one's.
func (s *Server) closeWhenHandedOff(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.handedOff.Load() {
			w.Header().Set("Connection", "close")
		}
		h.ServeHTTP(w, r)
	})
}

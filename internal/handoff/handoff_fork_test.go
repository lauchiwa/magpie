//go:build darwin || linux

package handoff

import (
	"net"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// fork: handing the socket over leaves the listener it was copied from
// non-blocking, so the gateway's Accept on it still ends at a deadline
// (handoff_fork_unix.go). Checked before the new magpie takes its copy,
// which would put the shared socket back in non-blocking mode.
func TestGiveLeavesTheListenerNonBlocking(t *testing.T) {
	ln, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fds[1])
	pf := os.NewFile(uintptr(fds[0]), "handoff")
	pc, err := net.FileConn(pf)
	pf.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()

	f, err := ln.File()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := (&Conn{c: pc.(*net.UnixConn)}).Give(f, Meta{Addr: ln.Addr().String()}); err != nil {
		t.Fatal(err)
	}
	rc, err := ln.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var flags int
	var ferr error
	rc.Control(func(fd uintptr) { flags, ferr = unix.FcntlInt(fd, unix.F_GETFL, 0) })
	if ferr != nil {
		t.Fatal(ferr)
	}
	if flags&unix.O_NONBLOCK == 0 {
		t.Fatal("the listener is in blocking mode after the handover: its Accept no longer ends at a deadline")
	}
}

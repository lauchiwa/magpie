//go:build darwin || linux

package handoff

import (
	"os"

	"golang.org/x/sys/unix"
)

// fork: rightsOf is the control message that hands f over, its
// descriptor read through SyscallConn rather than f.Fd: Fd puts the
// descriptor in blocking mode, and O_NONBLOCK belongs to the socket both
// copies share, so the gateway's own Accept became a blocking accept(2)
// that no deadline ends (gateway's stopAccepting). It went on, until a
// client connected, to take that connection from the magpie handed over
// to (TestRestartHandsTheGatewayOver on ubuntu).
func rightsOf(f *os.File) ([]byte, error) {
	rc, err := f.SyscallConn()
	if err != nil {
		return nil, err
	}
	var oob []byte
	if err := rc.Control(func(fd uintptr) { oob = unix.UnixRights(int(fd)) }); err != nil {
		return nil, err
	}
	return oob, nil
}

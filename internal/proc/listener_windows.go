package proc

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// getExtendedTcpTable is iphlpapi's table of TCP sockets with their owners,
// the one Get-NetTCPConnection and netstat -ano read.
var getExtendedTcpTable = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

const (
	tcpTableOwnerPIDListener = 3 // TCP_TABLE_OWNER_PID_LISTENER
	tcpListen                = 2 // MIB_TCP_STATE_LISTEN
	tcp4Row                  = 24
	tcp6Row                  = 56
)

// listeningOn reads the listening sockets on port, IPv4 and IPv6, from
// iphlpapi, in the process. It asked PowerShell's Get-NetTCPConnection
// before: 6 seconds a call on an idle Windows and 14 to 20 on a busy one,
// while a take-over gives the older magpie 5 to quit and 3 more once
// killed, and polls this to see the port let go, so an older magpie that
// had quit was still said "stuck" ("didn't quit. End it in your system's
// process list"). Nothing listening is no listener, not an error (#2231).
func listeningOn(ctx context.Context, port int) ([]int, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var ps []int
	seen := map[int]bool{}
	// each row's dwState, dwLocalPort and dwOwningPid offsets:
	// MIB_TCPROW_OWNER_PID and MIB_TCP6ROW_OWNER_PID
	for _, af := range []struct {
		family                uint32
		row, state, at, owner int
	}{{windows.AF_INET, tcp4Row, 0, 8, 20}, {windows.AF_INET6, tcp6Row, 48, 20, 52}} {
		rows, err := tcpListeners(af.family)
		if err != nil {
			return nil, err
		}
		n := int(binary.LittleEndian.Uint32(rows))
		for i := 0; i < n; i++ {
			r := rows[4+i*af.row : 4+(i+1)*af.row]
			state := binary.LittleEndian.Uint32(r[af.state:])
			pid := int(binary.LittleEndian.Uint32(r[af.owner:]))
			// the port is in network byte order, in its dword's low half
			p := binary.LittleEndian.Uint32(r[af.at:])
			if state != tcpListen || int(p&0xff)<<8|int(p>>8&0xff) != port || pid <= 0 || seen[pid] {
				continue
			}
			seen[pid] = true
			ps = append(ps, pid)
		}
	}
	return ps, nil
}

// tcpListeners is the listener table of family as GetExtendedTcpTable
// writes it: its count, then its rows. The table may grow between asking
// its size and reading it, so that is asked again a few times.
func tcpListeners(family uint32) ([]byte, error) {
	size := uint32(4096)
	for range 8 {
		buf := make([]byte, size)
		r, _, _ := getExtendedTcpTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0,
			uintptr(family), tcpTableOwnerPIDListener, 0)
		switch windows.Errno(r) {
		case 0:
			row := tcp4Row
			if family == windows.AF_INET6 {
				row = tcp6Row
			}
			if n := int(binary.LittleEndian.Uint32(buf)); 4+n*row > len(buf) {
				return nil, fmt.Errorf("GetExtendedTcpTable: %d rows in %d bytes", n, len(buf))
			}
			return buf, nil
		case windows.ERROR_INSUFFICIENT_BUFFER:
			continue // size is the one it needs now
		default:
			return nil, fmt.Errorf("GetExtendedTcpTable: %w", windows.Errno(r))
		}
	}
	return nil, errors.New("GetExtendedTcpTable: the table kept growing")
}

func executable(pid int) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buf[:n]), nil
}

// terminate ends it at once: a tray app has no console for a Ctrl+Break,
// and no window a close message would reach.
func terminate(pid int) error { return kill(pid) }

func kill(pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return ErrDenied
		}
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return nil // gone already
		}
		return err
	}
	defer windows.CloseHandle(h)
	if err := windows.TerminateProcess(h, 1); err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return ErrDenied
		}
		return err
	}
	return nil
}

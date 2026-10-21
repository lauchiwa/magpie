package provider

import (
	"hash/maphash"
	"syscall"
	"unsafe"
)

// environBlockSum is environSum read straight from the process's
// environment block. os.Environ on Windows makes every variable's string
// again on each call (syscall.Environ: ~160 allocations here), and the
// kept catalogs stamp the environment on every hit, so a kept list cost
// as much on Windows as it saved. The block's own bytes name the same
// environment, and environSum is only ever compared within this process.
func environBlockSum(h *maphash.Hash) bool {
	envp, err := syscall.GetEnvironmentStrings()
	if err != nil {
		return false
	}
	defer syscall.FreeEnvironmentStrings(envp)
	const size = unsafe.Sizeof(*envp)
	end := unsafe.Pointer(envp)
	// the block ends with an empty string: two NULs in a row
	for *(*uint16)(end) != 0 || *(*uint16)(unsafe.Add(end, size)) != 0 {
		end = unsafe.Add(end, size)
	}
	h.Write(unsafe.Slice((*byte)(unsafe.Pointer(envp)), uintptr(end)-uintptr(unsafe.Pointer(envp))))
	return true
}

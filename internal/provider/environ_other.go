//go:build !windows

package provider

import "hash/maphash"

// environBlockSum is os.Environ's own elsewhere: it copies one slice.
func environBlockSum(*maphash.Hash) bool { return false }

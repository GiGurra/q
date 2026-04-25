package util

import (
	"hash/crc32"
)

// CRC32 returns the IEEE crc32 of s. Used by the comptime fixture
// to demonstrate that closures can call into module-local
// non-stdlib helper packages.
func CRC32(s string) uint32 {
	return crc32.ChecksumIEEE([]byte(s))
}

// Reverse returns the reversed form of s. Pure function; safe
// to call at preprocessor time.
func Reverse(s string) string {
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}

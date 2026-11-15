//go:build unix

package configfile

import (
	"io/fs"
	"syscall"
)

// linkCount returns the number of hard links to the file, or 0 if unknown.
func linkCount(info fs.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Nlink)
	}
	return 0
}

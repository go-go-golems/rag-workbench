//go:build unix

package artifacts

import (
	"io/fs"
	"syscall"
)

func linkCount(fi fs.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Nlink)
	}
	return 1
}

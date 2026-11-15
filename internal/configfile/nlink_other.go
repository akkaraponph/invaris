//go:build !unix

package configfile

import "io/fs"

// linkCount is unknown outside Unix; Invaris supports Windows only through WSL.
func linkCount(fs.FileInfo) uint64 { return 0 }

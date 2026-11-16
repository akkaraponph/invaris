package configfile

import (
	"errors"
	"os"
	"path/filepath"
)

const WorkspaceDir = ".invaris"

// DefaultPath prefers workspace configuration. Only an absent workspace file
// permits fallback, so an invalid or unreadable workspace config is not hidden.
func DefaultPath(name string) string {
	path := filepath.Join(WorkspaceDir, name)
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return path
	}
	if _, err := os.Lstat(name); !errors.Is(err, os.ErrNotExist) {
		return name
	}
	return path
}

// ProjectRoot keeps project paths relative to the parent of a configuration
// workspace. Configurations elsewhere retain their directory as the root.
func ProjectRoot(configPath string) string {
	dir := filepath.Dir(filepath.Clean(configPath))
	if filepath.Base(dir) == WorkspaceDir {
		return filepath.Dir(dir)
	}
	return dir
}

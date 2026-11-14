package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PersistArtifact writes data to dir/name with exclusive creation (an existing
// file is never overwritten) and returns the hash of exactly the bytes written.
// Callers must redact first; PersistRedacted does both. name must be a plain
// file name. A failed write removes the partial file it created.
func PersistArtifact(dir, name string, data []byte) (Artifact, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		return Artifact{}, fmt.Errorf("invalid artifact name %q", name)
	}
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return Artifact{}, err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		if rerr := os.Remove(path); rerr != nil {
			err = errors.Join(err, rerr)
		}
		return Artifact{}, err
	}
	sum := sha256.Sum256(data)
	return Artifact{Path: name, SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(data))}, nil
}

// PersistRedacted redacts configured secrets from data and then persists it.
func PersistRedacted(dir, name string, data []byte, secrets []string) (Artifact, error) {
	return PersistArtifact(dir, name, Redact(data, secrets))
}

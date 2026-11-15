package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Decode strictly decodes and validates harness bytes exactly as Load does
// after reading a file. Authoring commands use it to validate the bytes they
// are about to write.
func Decode(raw []byte) (Specification, error) { return decodeSpecification(raw) }

// ProjectDetection records which marker files exist at a project root. It is
// presence only: no marker content, script or tool is read or run.
type ProjectDetection struct {
	GoMod       bool `json:"goMod"`
	GoWork      bool `json:"goWork"`
	PackageJSON bool `json:"packageJson"`
	CargoToml   bool `json:"cargoToml"`
	GitDir      bool `json:"gitDir"`
	GitFile     bool `json:"gitFile"`
	Gitmodules  bool `json:"gitmodules"`
}

// maxMarkerBytes bounds the marker files DetectProject accepts.
const maxMarkerBytes = 1 << 20

// DetectProject inspects only the root's own marker entries with Lstat. A
// marker that is a symbolic link, a special file or larger than 1 MiB is an
// error rather than a guess. Nothing is followed into nested directories.
func DetectProject(root string) (ProjectDetection, error) {
	var d ProjectDetection
	regular := func(name string, found *bool) error {
		info, err := os.Lstat(filepath.Join(root, name))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil
		case err != nil:
			return fmt.Errorf("inspect %s: %w", name, err)
		case !info.Mode().IsRegular():
			return fmt.Errorf("marker %s is not a regular file (links and special files are refused)", name)
		case info.Size() > maxMarkerBytes:
			return fmt.Errorf("marker %s exceeds 1 MiB", name)
		}
		// Confirm the marker is readable within the bound; its content is discarded.
		f, err := os.Open(filepath.Join(root, name))
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		defer f.Close()
		if n, err := io.Copy(io.Discard, io.LimitReader(f, maxMarkerBytes+1)); err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		} else if n > maxMarkerBytes {
			return fmt.Errorf("marker %s exceeds 1 MiB", name)
		}
		*found = true
		return nil
	}
	for _, m := range []struct {
		name  string
		found *bool
	}{{"go.mod", &d.GoMod}, {"go.work", &d.GoWork}, {"package.json", &d.PackageJSON}, {"Cargo.toml", &d.CargoToml}, {".gitmodules", &d.Gitmodules}} {
		if err := regular(m.name, m.found); err != nil {
			return d, err
		}
	}
	info, err := os.Lstat(filepath.Join(root, ".git"))
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return d, fmt.Errorf("inspect .git: %w", err)
	case info.IsDir():
		d.GitDir = true
	case info.Mode().IsRegular():
		d.GitFile = true
	default:
		return d, errors.New("marker .git is neither a directory nor a regular file (links are refused)")
	}
	return d, nil
}

// Recommendations explains what was detected. It never selects a preset.
func (d ProjectDetection) Recommendations() []string {
	out := []string{}
	ecosystems := 0
	for _, b := range []bool{d.GoMod || d.GoWork, d.PackageJSON, d.CargoToml} {
		if b {
			ecosystems++
		}
	}
	if d.GoMod {
		out = append(out, "go.mod found: `invaris init --preset go` adds one `go test ./...` guard; it runs the installed Go toolchain when checked")
	}
	if d.GoWork {
		out = append(out, "go.work found: this is a Go workspace; a root `go test ./...` may not cover every module, so review guards per module")
	}
	if d.PackageJSON {
		out = append(out, "package.json found: no built-in Node preset; add guards for your own tools (package scripts were not read or run)")
	}
	if d.CargoToml {
		out = append(out, "Cargo.toml found: no built-in Rust preset; add guards for your own tools")
	}
	if ecosystems > 1 {
		out = append(out, "several ecosystems detected: no preset is selected automatically")
	}
	if d.GitDir || d.GitFile {
		out = append(out, "Git metadata found: challenge copies exclude .git, so guards that need Git cannot be challenged")
	}
	if d.Gitmodules {
		out = append(out, ".gitmodules found: submodule metadata is not reconstructed in challenge copies")
	}
	if len(out) == 0 {
		out = append(out, "no known project markers: start from the generic configuration and add guards")
	}
	return out
}

// Init presets.
const (
	PresetGeneric = "generic"
	PresetGo      = "go"
)

type initGuard struct {
	ID           string   `json:"id"`
	Program      string   `json:"program"`
	Args         []string `json:"args"`
	EnvAllowlist []string `json:"envAllowlist"`
}

type initConfig struct {
	Version       string      `json:"version"`
	Name          string      `json:"name"`
	TrustMode     string      `json:"trustMode"`
	NetworkPolicy string      `json:"networkPolicy"`
	WritablePaths []string    `json:"writablePaths"`
	Policies      []string    `json:"policies"`
	Steps         []string    `json:"steps"`
	Guards        []initGuard `json:"guards"`
	Challenges    []string    `json:"challenges"`
}

// RenderInitialConfig returns the exact invaris.dev/v1alpha2 bytes init would
// write for preset, after validating them with the harness decoder. The gate
// is omitted so guards added later are required by the derived gate.
func RenderInitialConfig(preset string, d ProjectDetection) ([]byte, error) {
	c := initConfig{Version: versionV2, Name: "invaris-project", TrustMode: "trusted-local", NetworkPolicy: "unrestricted",
		WritablePaths: []string{}, Policies: []string{}, Steps: []string{}, Guards: []initGuard{}, Challenges: []string{}}
	switch preset {
	case PresetGeneric:
	case PresetGo:
		if !d.GoMod {
			return nil, errors.New("--preset go requires a regular go.mod at the selected root")
		}
		c.Guards = []initGuard{{ID: "go-tests", Program: "go", Args: []string{"test", "./..."},
			EnvAllowlist: []string{"PATH", "HOME", "GOCACHE", "GOMODCACHE", "GOPATH"}}}
	default:
		return nil, fmt.Errorf("unsupported preset %q (want generic or go)", preset)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(c); err != nil {
		return nil, err
	}
	if _, err := Decode(buf.Bytes()); err != nil {
		return nil, fmt.Errorf("rendered configuration is invalid: %w", err)
	}
	return buf.Bytes(), nil
}

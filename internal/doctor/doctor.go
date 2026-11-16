// Package doctor inspects a harness's setup without running it. It loads the
// harness, looks programs up, reads targets and walks the directory that a
// workspace would copy; it never starts a process, including provider
// adapters, and it writes nothing. Results describe setup only: a clean
// diagnosis does not mean the harness will accept.
package doctor

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/akkaraponph/invaris/internal/engine"
	"github.com/akkaraponph/invaris/internal/mutation"
	"github.com/akkaraponph/invaris/internal/runner"
	"github.com/akkaraponph/invaris/internal/workspace"
)

// SchemaVersion identifies the diagnosis document.
const SchemaVersion = "invaris.doctor/v1alpha1"

// Check statuses.
const (
	Pass    = "pass"
	Fail    = "fail"
	Warn    = "warn"
	Skipped = "skipped"
)

// Check is one inspection result. Executed is always false: doctor runs
// nothing.
type Check struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Executed bool   `json:"executed"`
	Message  string `json:"message"`
}

// Summary counts checks by status.
type Summary struct {
	Pass    int `json:"pass"`
	Fail    int `json:"fail"`
	Warn    int `json:"warn"`
	Skipped int `json:"skipped"`
}

// Diagnosis is the result of Inspect.
type Diagnosis struct {
	Version        string  `json:"version"`
	Harness        string  `json:"harness"`
	HarnessVersion *string `json:"harnessVersion"`
	Checks         []Check `json:"checks"`
	Summary        Summary `json:"summary"`
}

// JSON renders the diagnosis with two-space indentation, a trailing newline
// and no HTML escaping.
func (d Diagnosis) JSON() ([]byte, error) {
	if d.Checks == nil {
		d.Checks = []Check{}
	}
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	e.SetIndent("", "  ")
	if err := e.Encode(d); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Failed reports whether any check failed.
func (d Diagnosis) Failed() bool { return d.Summary.Fail > 0 }

type inspector struct {
	d Diagnosis
}

func (i *inspector) add(id, status, format string, args ...any) {
	i.d.Checks = append(i.d.Checks, Check{ID: id, Status: status, Message: fmt.Sprintf(format, args...)})
	switch status {
	case Pass:
		i.d.Summary.Pass++
	case Fail:
		i.d.Summary.Fail++
	case Warn:
		i.d.Summary.Warn++
	default:
		i.d.Summary.Skipped++
	}
}

// command is a program the harness would start.
type command struct {
	id, program string
	env         []string
}

// edit is one declared change to a target file.
type edit struct {
	id, file, before, preimage string
	builtin                    *mutation.Spec
}

// maxTargetBytes bounds the read of each target.
const maxTargetBytes = 10 << 20

// Inspect diagnoses the harness at harnessPath. It executes nothing.
func Inspect(harnessPath string) Diagnosis {
	i := &inspector{d: Diagnosis{Version: SchemaVersion, Harness: harnessPath, Checks: []Check{}}}
	abs, err := filepath.Abs(harnessPath)
	var spec engine.Specification
	if err == nil {
		spec, err = engine.Load(abs)
	}
	if err != nil {
		i.add("harness.load", Fail, "%s", err)
		i.add("inspection", Skipped, "harness did not load")
		return i.d
	}
	version := spec.Version
	i.d.HarnessVersion = &version
	i.add("harness.load", Pass, "loaded %s", spec.Version)

	for _, action := range []string{"run", "check", "challenge", "mutate"} {
		if spec.SupportsAction(action) {
			i.add("action."+action, Pass, "supported by %s", spec.Version)
		} else {
			i.add("action."+action, Skipped, "not supported by %s", spec.Version)
		}
	}

	dir := filepath.Dir(abs)
	var commands []command
	var edits []edit
	for _, c := range spec.Steps {
		commands = append(commands, command{c.ID, c.Program, c.EnvAllowlist})
	}
	for _, c := range spec.Guards {
		commands = append(commands, command{c.ID, c.Program, c.EnvAllowlist})
	}
	if e := spec.Experiment; e != nil {
		commands = append(commands, command{e.Validation.ID, e.Validation.Program, e.Validation.EnvAllowlist},
			command{e.Test.ID, e.Test.Program, e.Test.EnvAllowlist})
		edits = append(edits, edit{e.ID, e.Mutation.Path, e.Mutation.Before, e.Mutation.PreimageSHA256, nil})
	}
	for _, a := range spec.Adapters {
		commands = append(commands, command{a.ID, a.Program, a.EnvAllowlist})
	}
	for _, c := range spec.Challenges {
		edits = append(edits, edit{c.ID, c.File, c.Before, "", c.Builtin})
	}

	if len(commands)+len(edits)+len(spec.ProviderGuards) == 0 {
		i.add("workspace.copy", Skipped, "no workspace is prepared")
	} else if err := workspace.CheckCopyable(dir); err != nil {
		i.add("workspace.copy", Fail, "harness directory cannot be copied into a workspace: %s (limits: %d entries, %d bytes per file)", err, workspace.MaxEntries, workspace.MaxFileBytes)
	} else {
		i.add("workspace.copy", Pass, "the harness directory can be copied into a workspace")
	}

	for _, c := range commands {
		i.program(c, dir)
	}
	for _, c := range commands {
		for _, key := range c.env {
			if key == "PATH" {
				continue
			}
			if _, ok := os.LookupEnv(key); !ok {
				i.add("env."+c.id+"."+key, Warn, "allowlisted variable is not set in this environment")
			}
		}
	}

	root := dir
	if canonical, err := filepath.EvalSymlinks(dir); err == nil {
		root = canonical
	}
	data := make([][]byte, len(edits))
	readable := make([]bool, len(edits))
	for n, e := range edits {
		var err error
		if data[n], err = readTarget(root, e.file); err != nil {
			i.add("target."+e.id, Fail, "%s: %s", e.file, err)
			continue
		}
		readable[n] = true
		i.add("target."+e.id, Pass, "%s is a readable, writable regular file", e.file)
	}
	for n, e := range edits {
		i.edit(e, data[n], readable[n])
	}

	for _, p := range spec.WritablePaths {
		if target, err := workspace.ResolveTarget(root, p); err != nil {
			i.add("writable."+p, Fail, "%s", err)
		} else {
			i.add("writable."+p, Pass, "%s is a regular file", filepath.ToSlash(strings.TrimPrefix(target, root+string(filepath.Separator))))
		}
	}

	for _, a := range spec.Adapters {
		i.add("provider."+a.ID, Skipped, "discovery executes the adapter; not performed by doctor")
	}
	return i.d
}

func (i *inspector) program(c command, dir string) {
	id := "program." + c.id
	found, err := runner.Lookup(c.program, c.env, dir)
	if err == nil {
		i.add(id, Pass, "%s", found)
		return
	}
	if !errors.Is(err, exec.ErrNotFound) {
		i.add(id, Fail, "%s", err)
		return
	}
	allowed := false
	for _, key := range c.env {
		allowed = allowed || key == "PATH"
	}
	path, set := os.LookupEnv("PATH")
	if allowed && set {
		i.add(id, Fail, "executable %q not found in the allowlisted PATH %s", c.program, path)
	} else {
		i.add(id, Fail, "executable %q not found: PATH is not allowlisted, so %s is searched", c.program, runner.DefaultPath)
	}
}

func (i *inspector) edit(e edit, data []byte, readable bool) {
	id := "edit." + e.id
	if !readable {
		i.add(id, Skipped, "target unreadable")
		return
	}
	if e.builtin != nil {
		if _, err := mutation.Plan(*e.builtin, data); err != nil {
			i.add(id, Fail, "built-in mutation cannot be applied: %s", err)
			return
		}
		i.add(id, Pass, "built-in %s applies to %s", e.builtin.Operator, e.file)
		return
	}
	if e.preimage != "" {
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != e.preimage {
			i.add(id, Fail, "content does not match the declared preimage digest")
			return
		}
	}
	if err := workspace.CheckMarker(data, e.before); err != nil {
		i.add(id, Fail, "%s", err)
		return
	}
	i.add(id, Pass, "the before marker occurs exactly once in %s", e.file)
}

func readTarget(root, file string) ([]byte, error) {
	target, err := workspace.ResolveTarget(root, file)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(target)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxTargetBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxTargetBytes {
		return nil, fmt.Errorf("target exceeds %d bytes", maxTargetBytes)
	}
	return data, nil
}

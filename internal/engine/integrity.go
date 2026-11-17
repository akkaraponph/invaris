package engine

import (
	"errors"
	"fmt"
	"os"

	"github.com/akkaraponph/invaris/internal/workspace"
)

// guard is the integrity guarantee both experiment types share: between
// stages, the workspace root must be the same directory and the workspace
// manifest (paths, modes, content hashes) must equal the expected one. The
// expected manifest changes only by the engine's one declared replacement.
// Evaluators stay separate; they only have to leave the workspace alone.
//
// This detects persistent changes visible at stage boundaries. Commands that
// change external state, or alter and restore files within a stage, are not
// detected, and nothing here is a sandbox.
type integrity struct {
	root string
	info os.FileInfo
	opts workspace.Options
}

// changeListLimit bounds how many changed paths an error names.
const changeListLimit = 20

func newIntegrity(root string, opts workspace.Options) (integrity, error) {
	info, err := os.Lstat(root)
	return integrity{root: root, info: info, opts: opts}, err
}

// snapshot records the manifest to compare against later. Paths the
// harness excludes are not part of it.
func (g integrity) snapshot() (map[string]workspace.Entry, error) {
	return workspace.SnapshotWith(g.root, g.opts)
}

// verify checks the root identity and compares the manifest with want.
func (g integrity) verify(want map[string]workspace.Entry) error {
	info, err := os.Lstat(g.root)
	if err != nil {
		return err
	}
	if g.info == nil || !os.SameFile(g.info, info) || info.Mode() != g.info.Mode() {
		return errors.New("workspace root changed")
	}
	after, err := g.snapshot()
	if err != nil {
		return err
	}
	if len(workspace.Diff(want, after)) != 0 {
		// Name the paths so a tool cache or build output can be found and
		// declared in workspace.exclude without reproducing the run.
		return fmt.Errorf("workspace manifest changed: %s", workspace.DescribeChanges(want, after, changeListLimit))
	}
	return nil
}

// afterReplacement returns want with the replaced file's hash updated, so the
// next verify accepts exactly the engine's replacement and nothing else.
func afterReplacement(want map[string]workspace.Entry, m workspace.Mutation) map[string]workspace.Entry {
	next := make(map[string]workspace.Entry, len(want))
	for k, v := range want {
		next[k] = v
	}
	e := next[m.Path]
	e.SHA256 = m.AfterSHA256
	next[m.Path] = e
	return next
}

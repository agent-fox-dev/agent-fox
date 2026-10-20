package repomap

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/agent-fox-dev/agentfox/internal/project"
	"github.com/agent-fox-dev/agentkit-go/outline"
)

// testPath is how the map tells a test from the rest: the convention table of
// the revert check.
var testPath = project.IsTestPath

// errNotOutlined says that the hands have no outline for a file: a language
// they do not read, a file that is too large or binary, or one they cannot
// parse.
var errNotOutlined = errors.New("the file has no outline")

// NewOutliner is the outliner over the hands' outline, for the files below
// root. It is the one place that binds to the hands' API. A method is named
// with its type.
func NewOutliner(root string) Outliner {
	return handsOutliner{root: root}
}

type handsOutliner struct {
	root string
}

func (h handsOutliner) Outline(ctx context.Context, path string) ([]Decl, error) {
	abs := filepath.Join(h.root, filepath.FromSlash(path))
	f, err := outline.Outline(ctx, abs, nil, outline.Options{Root: h.root, MaxFileBytes: MaxOutlineBytes})
	if err != nil {
		return nil, err
	}
	if f.Backend == outline.BackendNone {
		return nil, errNotOutlined
	}
	decls := make([]Decl, len(f.Decls))
	for i, d := range f.Decls {
		name := d.Name
		if d.Container != "" {
			name = d.Container + "." + name
		}
		decls[i] = Decl{Kind: string(d.Kind), Name: name, Line: d.StartLine}
	}
	return decls, nil
}

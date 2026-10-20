package box

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentkit-go/core"
	"github.com/agent-fox-dev/agentkit-go/sandbox"
	"github.com/agent-fox-dev/agentkit-go/tools"
	"github.com/agent-fox-dev/agentkit-go/workspace"
)

// Here opens a session over a directory where it is: the tools of the hands work
// on the directory itself, with no copy of it, no sync and no export of the tree.
// It is the box of a process that is already in one, such as `af triage run`,
// which a box executes on its own workspace; there is nothing to confine, and
// nothing that Open does to a copy (the sync, the removal of git's state, the
// scrub of its configuration) is done to the directory, which is the user's own.
//
// HostDir is the directory, and must be one. Provider, Env and Registry are not
// used. The ledger gets box_open and box_close, and no box_sync.
func Here(ctx context.Context, req Request) (*Session, error) {
	abs, err := filepath.Abs(req.HostDir)
	if err == nil {
		abs, err = filepath.EvalSymlinks(abs)
	}
	if err == nil {
		var info os.FileInfo
		if info, err = os.Stat(abs); err == nil && !info.IsDir() {
			err = errors.New("it is not a directory")
		}
	}
	if err != nil {
		return nil, &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("--dir %s: %v", req.HostDir, err)}
	}
	ws, err := workspace.Open(abs, workspace.Options{})
	if err != nil {
		return nil, &toolio.Failure{Category: toolio.CategorySandbox, Message: fmt.Sprintf("cannot open %s as a workspace: %v", abs, err)}
	}
	set, err := tools.For(ws, tools.Options{})
	if err != nil {
		return nil, &toolio.Failure{Category: toolio.CategorySandbox, Message: fmt.Sprintf("cannot make the tools of %s: %v", abs, err)}
	}
	s := &Session{req: req, sb: &inPlace{dir: abs}, tools: set, provider: "here", hostDir: abs, inPlace: true}
	s.record("box_open", map[string]any{"provider": s.provider})
	return s, nil
}

// inPlace is the sandbox of a session over a directory in place: the directory,
// and nothing around it.
type inPlace struct{ dir string }

func (p *inPlace) ID() string                  { return "here" }
func (p *inPlace) Spec() sandbox.Spec          { return sandbox.Spec{Workdir: p.dir} }
func (p *inPlace) Agent() sandbox.AgentInfo    { return sandbox.AgentInfo{Workdir: p.dir} }
func (p *inPlace) Close(context.Context) error { return nil }

func (p *inPlace) Sync(context.Context, sandbox.Source) (sandbox.SyncResult, error) {
	return sandbox.SyncResult{}, errors.New("the tree is in place: there is nothing to sync")
}

func (p *inPlace) Tools(context.Context, sandbox.ToolOptions) ([]core.Tool, error) {
	return nil, errors.New("the tools of a tree in place are made by Here")
}

// Export copies one regular file of the directory to dst, as dst/<base name>. A
// link is refused, whatever it leads to, as the export of a box refuses it, and a
// directory is not exported: no one reads a tree in place that way.
func (p *inPlace) Export(_ context.Context, path string, dst sandbox.Destination) error {
	out, ok := dst.(sandbox.LocalDir)
	if !ok {
		return errors.New("a tree in place is exported to a directory only")
	}
	rel := filepath.Clean(filepath.FromSlash(path))
	if !filepath.IsLocal(rel) {
		return fmt.Errorf("%s is not a path of the directory", path)
	}
	full := filepath.Join(p.dir, rel)
	info, err := os.Lstat(full)
	switch {
	case err != nil:
		return err
	case info.Mode()&os.ModeSymlink != 0:
		return fmt.Errorf("%s is a symlink, which an export refuses", path)
	case !info.Mode().IsRegular():
		return fmt.Errorf("%s is not a regular file, and a tree in place exports files only", path)
	}
	src, err := os.Open(full)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	dstFile, err := os.OpenFile(filepath.Join(out.Path, filepath.Base(full)), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(dstFile, src)
	if cerr := dstFile.Close(); err == nil {
		err = cerr
	}
	return err
}

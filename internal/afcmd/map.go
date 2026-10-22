package afcmd

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/repomap"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// defaultTokens is the budget of a map when --tokens is not given.
const defaultTokens = 4000

// registerMap registers af map.
func registerMap(app *toolio.App) {
	var tokens int
	app.Register(toolio.Command{
		Path:    "map",
		Summary: "Render a map of the repository that fits a token budget",
		Usage:   "af map [--dir DIR] [--tokens N]",
		Flags: func(fs *flag.FlagSet) {
			fs.IntVar(&tokens, "tokens", defaultTokens, fmt.Sprintf("the budget of the map in `tokens`, from %d to %d", repomap.MinTokens, repomap.MaxTokens))
		},
		MaxArgs:        toolio.NoArgs,
		Dir:            true,
		Timeout:        true,
		DefaultTimeout: 2 * time.Minute,
		ResultSchema:   resultSchema("map"),
		Run:            func(r *toolio.Run) (toolio.Outcome, error) { return repoMap(r, tokens) },
	})
}

// repoMap renders the map of the file view of r.Dir within the budget.
func repoMap(r *toolio.Run, tokens int) (toolio.Outcome, error) {
	if tokens < repomap.MinTokens || tokens > repomap.MaxTokens {
		return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryUsage, Message: (&repomap.BudgetError{Tokens: tokens}).Error()}
	}
	files, err := fileView(r)
	if err != nil {
		return toolio.Outcome{}, err
	}
	entries := make([]repomap.Entry, len(files))
	for i, f := range files {
		entries[i] = repomap.Entry{Path: f, Size: -1}
		if len(files) <= repomap.MaxOutlineFiles { // beyond that no file is outlined, and no size is read
			if info, err := os.Lstat(filepath.Join(r.Dir, filepath.FromSlash(f))); err == nil {
				entries[i].Size = info.Size()
			}
		}
	}

	res, err := repomap.Build(r.Ctx, entries, repomap.NewOutliner(r.Dir), tokens)
	if err != nil {
		var budget *repomap.BudgetError
		switch {
		case errors.As(err, &budget):
			return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryUsage, Message: err.Error()}
		case r.Ctx.Err() != nil:
			return toolio.Outcome{}, err
		}
		return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryIO, Message: err.Error()}
	}
	for _, w := range res.Warnings {
		r.Warn(w.Code, w.Message)
	}
	return toolio.Outcome{
		Status:  toolio.StatusDone,
		Summary: fmt.Sprintf("level %d, %d of %d tokens, %d files", res.Level, res.EstimatedTokens, res.Tokens, res.FilesTotal),
		Result:  res,
	}, nil
}

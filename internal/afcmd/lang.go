package afcmd

import (
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/project"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// registerLang registers af lang detect.
func registerLang(app *toolio.App) {
	var write bool
	app.Register(toolio.Command{
		Path:    "lang detect",
		Summary: "Report the languages, gate checks, suite forms and gate programs of a repository",
		Usage:   "af lang detect [--dir DIR] [--write]",
		Flags: func(fs *flag.FlagSet) {
			fs.BoolVar(&write, "write", false, "store the freshly detected gate table in .agent-fox/gate.json")
		},
		MaxArgs:        toolio.NoArgs,
		Dir:            true,
		Timeout:        true,
		DefaultTimeout: time.Minute,
		ResultSchema:   resultSchema("lang_detect"),
		Run:            func(r *toolio.Run) (toolio.Outcome, error) { return langDetect(r, write) },
	})
}

// langDetect detects the repository at r.Dir. With write it stores the table
// the repository's files give, whatever gate.json says now.
func langDetect(r *toolio.Run, write bool) (toolio.Outcome, error) {
	var detection project.Detection
	if write {
		files, err := fileView(r)
		if err != nil {
			return toolio.Outcome{}, err
		}
		detection = project.DetectFresh(r.Dir, files)
		if err := project.WriteGateFile(r.Dir, detection.Gate.Checks); err != nil {
			return toolio.Outcome{}, &toolio.Failure{
				Category: toolio.CategoryIO,
				Message:  fmt.Sprintf("cannot write %s: %v", project.GatePath(r.Dir), err),
			}
		}
		r.Artifact(toolio.Artifact{Kind: "gate_file", Path: project.GatePath(r.Dir)})
	} else {
		var err error
		if detection, err = detect(r); err != nil {
			return toolio.Outcome{}, err
		}
	}

	for _, c := range detection.Gate.Checks {
		if !c.Available {
			r.Warn("check_unavailable", fmt.Sprintf("the check %q cannot run here: %s", c.Name, unavailableReason(c.Argv[0])))
		}
	}
	return toolio.Outcome{Status: toolio.StatusDone, Summary: detectSummary(detection), Result: detection}, nil
}

// fileView is the file view of r.Dir, with a failure in the io category when
// it cannot be made.
func fileView(r *toolio.Run) ([]string, error) {
	files, err := checks.FileView(r.Ctx, r.Dir)
	if err != nil {
		return nil, &toolio.Failure{Category: toolio.CategoryIO, Message: err.Error()}
	}
	return files, nil
}

// detect is the detection of the repository at r.Dir, with the gate.json it
// has. A gate.json that cannot be used is a failure in the config category.
func detect(r *toolio.Run) (project.Detection, error) {
	files, err := fileView(r)
	if err != nil {
		return project.Detection{}, err
	}
	detection, err := project.Detect(r.Dir, files)
	if err != nil {
		category := toolio.CategoryInternal
		if errors.Is(err, project.ErrGateConfig) {
			category = toolio.CategoryConfig
		}
		return project.Detection{}, &toolio.Failure{Category: category, Message: err.Error()}
	}
	return detection, nil
}

// unavailableReason says why a program cannot be run.
func unavailableReason(program string) string {
	if strings.Contains(program, "/") {
		return program + " is not an executable file"
	}
	return program + " is not on PATH"
}

// detectSummary is the one line that says what was found.
func detectSummary(d project.Detection) string {
	languages := "no languages"
	if len(d.Languages) > 0 {
		var found []string
		for _, l := range d.Languages {
			found = append(found, fmt.Sprintf("%s (%d)", l.Name, l.Files))
		}
		languages = strings.Join(found, ", ")
	}
	gate := "no checks"
	if len(d.Gate.Checks) > 0 {
		var names []string
		for _, c := range d.Gate.Checks {
			names = append(names, c.Name)
		}
		gate = strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s; gate (%s): %s", languages, d.Gate.Source, gate)
}

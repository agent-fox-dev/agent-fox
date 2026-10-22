package brain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/agent-fox-dev/agentfox/internal/agentdir"
	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

func init() {
	Default.Register(Entry{Name: ClaudeName, CostSource: CostReported, Credentials: []string{VendorKeyVar}, OwnHands: true, CommandRoute: true, New: newClaude})
}

// claudeNote is the appended system prompt of a run: the brief is the prompt, and this
// is what the run is, in a few sentences that do not change.
const claudeNote = "This run is headless: no one can answer a question. The only deliverable is one submission, " +
	"made with the command the brief names; do the work and submit once. You must never commit, push or write to the forge: " +
	"af does that from what you submit. A refusal of a tool call is not the end of the run, it is the next turn: " +
	"read the reason and do something else."

// hookTimeout is how long, in seconds, Claude Code waits for the hook.
const hookTimeout = 30

// claudeBrain runs Claude Code headless in the box, with the brief on its stdin, an
// allowlist of tools, and a settings file whose PreToolUse hook is af guard check.
type claudeBrain struct {
	proc  *Process
	model string
}

// newClaude makes the brain. Before any box opens it needs the key of the model vendor
// for the box, unless the provider is none, where a person at their own checkout may be
// logged in to Claude Code; and, unless the provider is podman, whose image has it,
// claude on the PATH of this machine.
func newClaude(opts Options) (Brain, error) {
	boxEntry, _ := opts.boxEntry()
	if opts.Provider != "none" && !boxEntry.SuppliesCredentials {
		if v, ok := opts.lookup()(VendorKeyVar); !ok || v == "" {
			return nil, &toolio.Failure{
				Category: toolio.CategoryAuth,
				Message:  "the claude brain needs the key of the model vendor in " + VendorKeyVar + ": a box has a private HOME with no login of Claude Code",
				FixHint:  &toolio.FixHint{Env: VendorKeyVar},
			}
		}
	}
	cfg := opts.ProcessConfig()
	if !boxEntry.ByName {
		look := cfg.LookPath
		if look == nil {
			look = exec.LookPath
		}
		if _, err := look(ProgramClaude); err != nil {
			return nil, &toolio.Failure{Category: toolio.CategoryBrain, Message: fmt.Sprintf("the claude brain needs claude on the PATH of this machine: %v", err)}
		}
	}
	return &claudeBrain{proc: NewProcess(cfg), model: opts.Model}, nil
}

// Run places guard.json and claude-settings.json, runs claude headless on the brief with
// the allowlist of the brief, and returns what it can say of the run.
func (c *claudeBrain) Run(ctx context.Context, b Brief) (Outcome, error) {
	if b.Box == nil {
		return Outcome{StopReason: StopError}, errors.New("the claude brain needs a box")
	}
	af, err := c.proc.program(b.Box.ByName(), ProgramAF)
	if err != nil {
		return Outcome{StopReason: StopError}, err
	}
	root := b.Box.Root()
	dir := path.Join(agentDir, "run", b.RunID)
	files := []struct {
		name string
		data []byte
	}{
		{"guard.json", claudeGuardFile(b.Hook)},
		{"claude-settings.json", claudeSettingsFile(af, root, dir)},
	}
	for _, f := range files {
		if err := b.Box.WriteFile(ctx, path.Join(dir, f.name), f.data); err != nil {
			return Outcome{StopReason: StopError}, err
		}
		if b.RunDir != "" {
			if err := agentdir.WriteFileAtomic(filepath.Join(b.RunDir, f.name), f.data); err != nil {
				return Outcome{StopReason: StopError}, &toolio.Failure{Category: toolio.CategoryIO, Message: fmt.Sprintf("cannot write %s in the run directory: %v", f.name, err)}
			}
		}
	}

	lists := claudeToolLists(b.Tools, b.Box.Tool())
	for _, tool := range lists.Dropped {
		b.note("claude_tool_dropped", map[string]any{"tool": tool})
	}
	args := claudeArgs(path.Join(root, dir, "claude-settings.json"), b.MaxTurns, lists, c.model)
	res, err := c.proc.Run(ctx, b, claudeProcess(args))
	if err != nil {
		return Outcome{StopReason: StopError}, err
	}

	out := c.outcome(b, res)
	out.Submitted = submissionCarriesNonce(ctx, b)
	if b.MaxCostUSD > 0 && out.CostUSD > b.MaxCostUSD {
		b.warn("budget_exceeded", fmt.Sprintf("claude reported $%s for a run bounded to $%s: its cost is known after the run, so only its turns and its time held it while it ran",
			decimal(out.CostUSD), decimal(b.MaxCostUSD)))
		if !out.Submitted {
			out.StopReason = StopBudget
		}
	}
	if res.ExitCode != 0 && !out.Submitted {
		return out, claudeError(res)
	}
	return out, nil
}

// claudeResult is the part of the JSON a headless Claude Code prints that the brain reads.
type claudeResult struct {
	Subtype      string          `json:"subtype"`
	IsError      bool            `json:"is_error"`
	NumTurns     int             `json:"num_turns"`
	TotalCostUSD *float64        `json:"total_cost_usd"`
	CostUSD      *float64        `json:"cost_usd"`
	Usage        json.RawMessage `json:"usage"`
	SessionID    string          `json:"session_id"`
	// Result is the text of the run's end: its answer, or what went wrong.
	Result string `json:"result"`
}

// claudeUsage is its usage.
type claudeUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// outcome is what the output of the process says of the run. An output that is missing,
// or is not a JSON object, says nothing: the cost is 0, its source none, and the warning
// brain_usage_unavailable says so. The cost is the one claude reports, from total_cost_usd
// or else cost_usd; the input tokens count what was written to the cache and read from it.
func (c *claudeBrain) outcome(b Brief, res ProcessResult) Outcome {
	var r claudeResult
	trimmed := strings.TrimSpace(string(res.Output))
	if !strings.HasPrefix(trimmed, "{") || json.Unmarshal([]byte(trimmed), &r) != nil {
		b.warn("brain_usage_unavailable", "claude left no output that could be read, so the run has no cost to report")
		return Outcome{StopReason: StopEnded, CostSource: CostNone}
	}
	var usage claudeUsage
	_ = json.Unmarshal(r.Usage, &usage) // a usage that is not one counts as none
	out := Outcome{
		Turns: r.NumTurns, CostSource: CostReported,
		InputTokens:  usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens,
		OutputTokens: usage.OutputTokens,
	}
	switch {
	case r.TotalCostUSD != nil:
		out.CostUSD = *r.TotalCostUSD
	case r.CostUSD != nil:
		out.CostUSD = *r.CostUSD
	}
	switch {
	case r.Subtype == "success":
		out.StopReason = StopEnded
	case r.Subtype == "error_max_turns":
		out.StopReason = StopTurns
	case strings.Contains(r.Subtype, "budget"):
		out.StopReason = StopBudget
	default:
		out.StopReason = StopError
	}
	b.note("claude_result", map[string]any{
		"subtype": r.Subtype, "is_error": r.IsError, "num_turns": r.NumTurns, "total_cost_usd": out.CostUSD,
		"usage": json.RawMessage(orEmptyObject(r.Usage)), "session_id": r.SessionID,
	})
	return out
}

// claudeError is the error of a claude that exited with a code and submitted nothing. Claude
// Code says what went wrong in the result of its JSON output, with nothing on stderr (an API
// key that is refused is exit code 1, the subtype success and "Invalid API key" there), so
// when the errors are empty that text is what the error carries.
func claudeError(res ProcessResult) error {
	if len(bytes.TrimSpace(res.Stderr)) == 0 {
		var r claudeResult
		if json.Unmarshal(bytes.TrimSpace(res.Output), &r) == nil && strings.TrimSpace(r.Result) != "" {
			res.Stderr = []byte("claude said: " + strings.TrimSpace(r.Result))
		}
	}
	return processError("claude", res)
}

func orEmptyObject(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("{}")
	}
	return raw
}

// submissionCarriesNonce says whether the box has a submission.json that carries the
// nonce of this attempt, from the run.json that was placed. Claude Code makes no claim of
// its own, so this is the claim of the brain; the host decides from the file all the same.
func submissionCarriesNonce(ctx context.Context, b Brief) bool {
	fsys := b.Box.FS(ctx)
	runData, err := fs.ReadFile(fsys, submission.RunPath(b.RunID))
	if err != nil {
		return false
	}
	var run submission.Run
	if json.Unmarshal(runData, &run) != nil || run.Nonce == "" {
		return false
	}
	fileData, err := fs.ReadFile(fsys, submission.SubmissionPath(b.RunID))
	if err != nil {
		return false
	}
	var file submission.File
	return json.Unmarshal(fileData, &file) == nil && file.Nonce == run.Nonce
}

// claudeArgs are the arguments of claude: headless, JSON output, the turn bound, no
// question asked of anyone (dontAsk), the settings file with the hook, no server of the
// repository's own, the tools it may use and may not, and the note. The brief is on
// stdin and not here, where a long one would not fit.
func claudeArgs(settings string, maxTurns int, lists toolLists, model string) []string {
	args := []string{"-p", "--output-format", "json"}
	if maxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(maxTurns))
	}
	args = append(args, "--permission-mode", "dontAsk", "--settings", settings, "--strict-mcp-config")
	args = append(args, "--allowedTools")
	args = append(args, lists.Allowed...)
	args = append(args, "--disallowedTools")
	args = append(args, lists.Disallowed...)
	args = append(args, "--append-system-prompt", claudeNote)
	if model != "" {
		args = append(args, "--model", model)
	}
	return args
}

// claudeGuardFile is guard.json for the hook, in the format of af guard check: the
// protected paths and the programs of the brief, and no suite, so that the guard finds
// it from the tree it is run in.
func claudeGuardFile(hook HookConfig) []byte {
	file := struct {
		Version        int      `json:"version"`
		ProtectedPaths []string `json:"protected_paths"`
		AllowPrograms  []string `json:"allow_programs"`
	}{1, hook.ProtectedPaths, hook.AllowPrograms}
	if file.ProtectedPaths == nil {
		file.ProtectedPaths = []string{}
	}
	if file.AllowPrograms == nil {
		file.AllowPrograms = []string{}
	}
	data, _ := json.MarshalIndent(file, "", "  ") // strings and numbers: it cannot fail
	return append(data, '\n')
}

// plainWord is what a path may be made of to stand in a command without quotes.
var plainWord = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

func word(s string) string {
	if plainWord.MatchString(s) {
		return s
	}
	return quote(s)
}

// claudeSettingsFile is the settings file of the run: one PreToolUse hook, for every
// tool, that asks af guard check, in the format of Claude Code, whether the call may be
// made, with the policy of guard.json.
func claudeSettingsFile(af, root, dir string) []byte {
	command := fmt.Sprintf("%s guard check --host claude --dir %s --policy %s", word(af), word(root), word(path.Join(root, dir, "guard.json")))
	type hook struct {
		Type    string `json:"type"`
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}
	settings := map[string]any{"hooks": map[string]any{"PreToolUse": []any{
		map[string]any{"matcher": "*", "hooks": []hook{{Type: "command", Command: command, Timeout: hookTimeout}}},
	}}}
	data, _ := json.MarshalIndent(settings, "", "  ") // strings and numbers: it cannot fail
	return append(data, '\n')
}

// toolLists are the tools Claude Code is allowed and denied, and the tools of the box it
// has no equivalent for.
type toolLists struct{ Allowed, Disallowed, Dropped []string }

// claudeTable maps the tools of the box to the tools of Claude Code: the one table. Read
// tools need no permission in Claude Code and are listed all the same.
var claudeTable = map[string][]string{
	"read_file":    {"Read"},
	"list_files":   {"LS", "Glob"},
	"find_files":   {"Glob"},
	"search_files": {"Grep"},
	"write_file":   {"Write"},
	"edit_file":    {"Edit", "MultiEdit"},
	"execute":      {"Bash"},
}

// claudeToolLists are the lists for the tools of a brief and the agent it is for. The
// allowed tools are the mapped ones and the command that submits, Bash(af <agent>
// submit:*), whatever the tools. The web is never allowed, a prompt-injection vector, and
// nor is a writer when the brief has none. A tool with no equivalent is dropped, and the
// caller notes each.
func claudeToolLists(tools []string, agent string) toolLists {
	var l toolLists
	writer := false
	for _, tool := range tools {
		mapped, ok := claudeTable[tool]
		if !ok {
			if !slices.Contains(l.Dropped, tool) {
				l.Dropped = append(l.Dropped, tool)
			}
			continue
		}
		for _, c := range mapped {
			if !slices.Contains(l.Allowed, c) {
				l.Allowed = append(l.Allowed, c)
			}
		}
		writer = writer || tool == "write_file" || tool == "edit_file"
	}
	l.Allowed = append(l.Allowed, "Bash(af "+agent+" submit:*)")
	l.Disallowed = []string{"WebFetch", "WebSearch"}
	if !writer {
		l.Disallowed = append(l.Disallowed, "Write", "Edit", "MultiEdit", "NotebookEdit")
	}
	return l
}

// claudeProcess is the process the brain runs in the box: claude, with af probed beside it, the brief
// on its standard input, and the name of the vendor key as the credential the box has to supply.
func claudeProcess(args []string) ProcessSpec {
	return ProcessSpec{
		Brain: ClaudeName, Program: ProgramClaude, Args: args, Probes: []string{ProgramAF, ProgramClaude}, StdinBrief: true,
		Credentials: []string{VendorKeyVar},
	}
}

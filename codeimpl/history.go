package codeimpl

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/agent-fox-dev/agentfox/internal/conform"
)

// A run that continues a branch knows only what the branch carries: the task
// state in tasks.json, and the commits. The tasks an earlier run landed are
// skipped, so what they declared would be lost with the run that declared it,
// and a blocker one of them answered would block again. Each landed commit
// therefore records its declarations as trailers beside its Spec: trailer,
// and the next run reads them back from the branch before it commits
// anything of its own.

// deviationTrailer opens a trailer that records one declared deviation, as
// JSON on one line.
const deviationTrailer = "Deviation:"

// deviationTrailers renders each declaration as a trailer line.
func deviationTrailers(ds []conform.Deviation) string {
	var b strings.Builder
	for _, d := range ds {
		j, err := json.Marshal(d)
		if err != nil {
			continue
		}
		b.WriteString(deviationTrailer + " " + string(j) + "\n")
	}
	return b.String()
}

// trailerBlock is the last paragraph of a landed commit of this spec — the
// one the program writes, with its Spec: trailer — or nil. Only that
// paragraph is read: the body above it is the model's summary, and a
// trailer-shaped line in it is not a record.
func trailerBlock(specDir, message string) []string {
	if strings.HasPrefix(message, wipPrefix) {
		return nil
	}
	paras := strings.Split(strings.TrimSpace(message), "\n\n")
	lines := strings.Split(paras[len(paras)-1], "\n")
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), specTrailer+" "+specDir+", ") {
			return lines
		}
	}
	return nil
}

// recordedDeviations reads back the declarations a commit recorded.
func recordedDeviations(specDir, message string) []conform.Deviation {
	var out []conform.Deviation
	for _, line := range trailerBlock(specDir, message) {
		raw, ok := strings.CutPrefix(strings.TrimSpace(line), deviationTrailer)
		if !ok {
			continue
		}
		var d conform.Deviation
		if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &d); err == nil && strings.TrimSpace(d.Key) != "" {
			out = append(out, d)
		}
	}
	return out
}

// isRepairCommit reports whether a commit is a landed baseline repair.
func isRepairCommit(specDir, message string) bool {
	for _, line := range trailerBlock(specDir, message) {
		if strings.TrimSpace(line) == specTrailer+" "+specDir+", "+repairMarker {
			return true
		}
	}
	return false
}

// readHistory reads what the earlier runs on the branch recorded in their
// commits: the deviations their tasks and resolve phases declared, and the
// files their baseline repair changed. It runs before this run commits, so
// every commit it reads is an earlier run's.
func (st *runState) readHistory(ctx context.Context) error {
	commits, err := st.git.CommitsSince(ctx, st.start)
	if err != nil {
		return err
	}
	specDir := filepath.Base(st.spec.Dir)
	for _, c := range commits {
		st.priorDeviations = append(st.priorDeviations, recordedDeviations(specDir, c.Message)...)
		if isRepairCommit(specDir, c.Message) {
			st.repairFiles = append(st.repairFiles, c.Files...)
		}
	}
	return nil
}

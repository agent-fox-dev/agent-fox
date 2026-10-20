package triage

import "strings"

// Fence is the line that opens and the line that closes a fence around content:
// backticks, one more than the longest run of backticks in the content and at
// least three. No line of the content can then equal the closing line, so text
// from a report cannot end its fence and pose as what follows it.
func Fence(content string) (open, close string) {
	longest, run := 0, 0
	for _, r := range content {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	line := strings.Repeat("`", max(3, longest+1))
	return line, line
}

// fenced is a part of the report: one provenance line, then the content in a
// fence of its own.
func fenced(provenance, content string) string {
	open, close := Fence(content)
	return provenance + "\n" + open + "\n" + content + "\n" + close + "\n"
}

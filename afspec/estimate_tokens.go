package afspec

// EstimateTokens estimates the token count of text using the chars/4
// heuristic: len(text) / 4 (integer floor division).
func EstimateTokens(text string) int {
	return len(text) / 4
}

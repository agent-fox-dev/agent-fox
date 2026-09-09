package agentspec

import "context"

// AssessSpec loads a SpecSession from disk and assesses its PRD.
func AssessSpec(ctx context.Context, specDir string) (Assessment, error) {
	return AssessSpecWith(ctx, specDir, RunOptions{})
}

// AssessSpecWith is AssessSpec under explicit run options: the workspace the
// model may read, the providers it talks to, and the bounds it runs under.
func AssessSpecWith(ctx context.Context, specDir string, o RunOptions) (Assessment, error) {
	session, err := ResumeSession(specDir)
	if err != nil {
		return Assessment{}, err
	}
	session.SetRunOptions(o)
	return session.Assess(ctx)
}

// RefineSpec loads a SpecSession from disk and refines its PRD with the given
// answers.
func RefineSpec(ctx context.Context, specDir string, answers map[string]string) (Assessment, error) {
	return RefineSpecWith(ctx, specDir, answers, RunOptions{})
}

// RefineSpecWith is RefineSpec under explicit run options.
func RefineSpecWith(ctx context.Context, specDir string, answers map[string]string, o RunOptions) (Assessment, error) {
	session, err := ResumeSession(specDir)
	if err != nil {
		return Assessment{}, err
	}
	session.SetRunOptions(o)
	return session.Refine(ctx, answers)
}

// GenerateSpec loads a SpecSession from disk and generates its artifacts.
func GenerateSpec(ctx context.Context, specDir string) (GenerateResult, error) {
	return GenerateSpecWith(ctx, specDir, RunOptions{})
}

// GenerateSpecWith is GenerateSpec under explicit run options.
func GenerateSpecWith(ctx context.Context, specDir string, o RunOptions) (GenerateResult, error) {
	session, err := ResumeSession(specDir)
	if err != nil {
		return GenerateResult{}, err
	}
	session.SetRunOptions(o)
	return session.Generate(ctx)
}

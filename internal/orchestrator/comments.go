package orchestrator

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// CommentPoster handles posting comments to issues.
type CommentPoster struct {
	cfg      *RunConfig
	enabled  bool
	platform string
}

// NewCommentPoster creates a new comment poster.
func NewCommentPoster(cfg *RunConfig, enabled bool) *CommentPoster {
	platform := "gitlab"
	if repo, err := cfg.PrimaryRepo(); err == nil {
		platform = repo.Platform
	}
	return &CommentPoster{
		cfg:      cfg,
		enabled:  enabled,
		platform: platform,
	}
}

// PostReviewFailure posts a comment about why an issue failed review.
func (cp *CommentPoster) PostReviewFailure(result *ReviewResult) error {
	if !cp.enabled {
		return nil
	}

	// Build comment body
	var sb strings.Builder
	sb.WriteString("## Orchestrator Review Gate Failed\n\n")
	sb.WriteString("This issue did not pass the automated review gate and cannot proceed to implementation.\n\n")
	sb.WriteString("### Issues Found\n\n")

	for _, reason := range result.Reasons {
		sb.WriteString(fmt.Sprintf("- %s\n", reason))
	}

	sb.WriteString("\n### Scores\n\n")
	if result.Completeness != nil {
		sb.WriteString(fmt.Sprintf("- **Completeness**: %.0f%% %s\n",
			result.Completeness.Score*100,
			passFailEmoji(result.Completeness.Passed)))
	}
	if result.Suitability != nil {
		sb.WriteString(fmt.Sprintf("- **Suitability**: %.0f%% %s\n",
			result.Suitability.Score*100,
			passFailEmoji(result.Suitability.Passed)))
	}
	if result.DependencyCheck != nil {
		sb.WriteString(fmt.Sprintf("- **Dependencies**: %.0f%% %s\n",
			result.DependencyCheck.Score*100,
			passFailEmoji(result.DependencyCheck.Passed)))
	}

	sb.WriteString("\n### How to Fix\n\n")
	sb.WriteString("Please address the issues above and re-run the review gate.\n")
	sb.WriteString("\n---\n")
	sb.WriteString("*Automated comment from Orchestrator Review Gate*\n")

	comment := sb.String()
	return cp.postComment(result.IssueNumber, comment)
}

// PostReviewSuccess posts a comment that the issue passed review.
func (cp *CommentPoster) PostReviewSuccess(result *ReviewResult) error {
	if !cp.enabled {
		return nil
	}

	var sb strings.Builder
	sb.WriteString("## Orchestrator Review Gate Passed\n\n")
	sb.WriteString("This issue has passed the automated review gate and is ready for implementation.\n\n")

	if len(result.Reasons) > 0 {
		sb.WriteString("### Notes\n\n")
		for _, reason := range result.Reasons {
			sb.WriteString(fmt.Sprintf("- %s\n", reason))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("### Scores\n\n")
	if result.Completeness != nil {
		sb.WriteString(fmt.Sprintf("- **Completeness**: %.0f%%\n", result.Completeness.Score*100))
	}
	if result.Suitability != nil {
		sb.WriteString(fmt.Sprintf("- **Suitability**: %.0f%%\n", result.Suitability.Score*100))
	}
	if result.DependencyCheck != nil {
		sb.WriteString(fmt.Sprintf("- **Dependencies**: %.0f%%\n", result.DependencyCheck.Score*100))
	}

	sb.WriteString("\n---\n")
	sb.WriteString("*Automated comment from Orchestrator Review Gate*\n")

	comment := sb.String()
	return cp.postComment(result.IssueNumber, comment)
}

// postComment posts a comment to an issue using the appropriate CLI.
func (cp *CommentPoster) postComment(issueNumber int, body string) error {
	repo, err := cp.cfg.PrimaryRepo()
	if err != nil {
		return fmt.Errorf("no repository configured: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	switch cp.platform {
	case "gitlab":
		cmd = exec.CommandContext(ctx, "glab", "issue", "note", fmt.Sprintf("%d", issueNumber), "-m", body)
	case "github":
		cmd = exec.CommandContext(ctx, "gh", "issue", "comment", fmt.Sprintf("%d", issueNumber), "-b", body)
	default:
		return fmt.Errorf("unsupported platform: %s", cp.platform)
	}

	cmd.Dir = repo.Path
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("posting comment: %w (output: %s)", err, string(output))
	}

	LogMsg(fmt.Sprintf("Posted review comment to issue #%d", issueNumber))
	return nil
}

// PostGateSummary posts a summary comment to the first failed issue.
func (cp *CommentPoster) PostGateSummary(gateResult *GateResult) error {
	if !cp.enabled || gateResult.Passed {
		return nil
	}

	// Find the first failed issue
	var firstFailed *ReviewResult
	for _, result := range gateResult.Results {
		if !result.Passed {
			firstFailed = result
			break
		}
	}
	if firstFailed == nil {
		return nil
	}

	var sb strings.Builder
	sb.WriteString("## Review Gate Summary\n\n")
	sb.WriteString(fmt.Sprintf("**%d of %d issues failed review**\n\n",
		gateResult.FailedIssues,
		gateResult.TotalIssues-gateResult.SkippedIssues))

	sb.WriteString("### Failed Issues\n\n")
	for _, result := range gateResult.Results {
		if !result.Passed {
			sb.WriteString(fmt.Sprintf("- **#%d**: %s\n", result.IssueNumber, result.Title))
			for _, reason := range result.Reasons {
				sb.WriteString(fmt.Sprintf("  - %s\n", reason))
			}
		}
	}

	sb.WriteString("\n---\n")
	sb.WriteString("*Automated summary from Orchestrator Review Gate*\n")

	comment := sb.String()
	return cp.postComment(firstFailed.IssueNumber, comment)
}

func passFailEmoji(passed bool) string {
	if passed {
		return "(pass)"
	}
	return "(fail)"
}

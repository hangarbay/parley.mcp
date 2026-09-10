// Package review implements the review tool: it sends a git diff, plan, or code
// change together with project context and rules to one or more external AIs
// and returns their structured code-review verdicts.
package review

import (
	"context"
	"errors"
	"strings"

	"github.com/hangarbay/parley.mcp/internal/dispatch"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fanOut is dispatch.FanOut, swappable in tests.
var fanOut = dispatch.FanOut

// Register adds the review tool and prompt to server.
func Register(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "review",
		Description: "Review a git diff, plan, or code change and return a verdict " +
			"(APPROVE / REQUEST_CHANGES / NEEDS_DISCUSSION) with specific findings. " +
			"Provide the change as diff, plus optional project context and rules to enforce. " +
			"provider defaults to both gemini and chatgpt.",
	}, reviewHandler)
	server.AddPrompt(&mcp.Prompt{
		Name:        "review",
		Description: "Review a git diff, plan, or code change against context and rules. No API key required.",
		Arguments: []*mcp.PromptArgument{
			{Name: "diff", Description: "The unified diff (git diff) or plan text to review", Required: true},
			{Name: "context", Description: "Project context to ground the review", Required: false},
			{Name: "rules", Description: "Rules or conventions the change must follow", Required: false},
			{Name: "provider", Description: "gemini, chatgpt, or both (default both)", Required: false},
		},
	}, reviewPrompt)
}

type reviewArgs struct {
	Diff     string `json:"diff" jsonschema:"the unified diff (git diff) or plan text to review"`
	Context  string `json:"context,omitempty" jsonschema:"optional project context to ground the review"`
	Rules    string `json:"rules,omitempty" jsonschema:"optional rules or conventions the change must follow"`
	Provider string `json:"provider,omitempty" jsonschema:"gemini, chatgpt, or both (default both)"`
}

// Options configures a review request.
type Options struct {
	Diff     string
	Context  string
	Rules    string
	Provider string
}

// Run composes the review prompt and dispatches it to the selected providers.
func Run(ctx context.Context, opts Options) (string, error) {
	if strings.TrimSpace(opts.Diff) == "" {
		return "", errors.New("diff is required")
	}
	names, err := dispatch.Resolve(opts.Provider)
	if err != nil {
		return "", err
	}
	results := fanOut(ctx, buildPrompt(opts), names)
	return dispatch.Format(results)
}

func reviewHandler(ctx context.Context, _ *mcp.CallToolRequest, args reviewArgs) (*mcp.CallToolResult, any, error) {
	text, err := Run(ctx, Options(args))
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}, nil, nil
}

func reviewPrompt(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	get := func(key string) string {
		if req.Params.Arguments == nil {
			return ""
		}
		return req.Params.Arguments[key]
	}
	text, err := Run(ctx, Options{
		Diff:     get("diff"),
		Context:  get("context"),
		Rules:    get("rules"),
		Provider: get("provider"),
	})
	if err != nil {
		return nil, err
	}
	return &mcp.GetPromptResult{
		Description: "Review a code change",
		Messages: []*mcp.PromptMessage{
			{Content: &mcp.TextContent{Text: text}, Role: mcp.Role("user")},
		},
	}, nil
}

const reviewerRole = `You are a meticulous senior software engineer performing a strict code review. ` +
	`Be direct and concrete, never flattering. Prioritize correctness, security, data integrity, concurrency, ` +
	`error handling, and backward compatibility. Judge the change only on what is shown.`

const reviewInstructions = `Respond with exactly these four sections and nothing else:

1. Verdict: one of APPROVE, REQUEST_CHANGES, or NEEDS_DISCUSSION.
2. Blocking issues: concrete problems that must be fixed before merge, each citing the relevant file and line from the diff. If none, write "None".
3. Non-blocking suggestions: improvements worth considering, each citing the relevant file and line. If none, write "None".
4. Rule violations: any supplied rule the change breaks, quoting the rule verbatim. If none, write "None".

Keep it concise. Do not restate the diff.`

func buildPrompt(opts Options) string {
	var b strings.Builder
	b.WriteString(reviewerRole)
	b.WriteString("\n\n")
	if c := strings.TrimSpace(opts.Context); c != "" {
		b.WriteString("## Project context\n\n")
		b.WriteString(c)
		b.WriteString("\n\n")
	}
	if r := strings.TrimSpace(opts.Rules); r != "" {
		b.WriteString("## Rules the change must follow\n\n")
		b.WriteString(r)
		b.WriteString("\n\n")
	}
	b.WriteString("## Change under review\n\n```diff\n")
	b.WriteString(strings.TrimSpace(opts.Diff))
	b.WriteString("\n```\n\n")
	b.WriteString(reviewInstructions)
	return b.String()
}

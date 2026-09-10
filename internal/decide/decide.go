// Package decide implements the decide tool: it sends an explicitly offered
// set of options, together with the criteria they must satisfy, to one or more
// external AIs and returns a committed recommendation for exactly one of them.
package decide

import (
	"context"
	"errors"
	"strings"

	"github.com/hangarbay/parley.mcp/internal/dispatch"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fanOut is dispatch.FanOut, swappable in tests.
var fanOut = dispatch.FanOut

// Register adds the decide tool and prompt to server.
func Register(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "decide",
		Description: "Select exactly one option from an explicitly enumerated list " +
			"using explicit criteria, and commit to it. Use only when the alternatives " +
			"are already known; when they are not, ask to explore them first. provider " +
			"defaults to both gemini and chatgpt.",
	}, decideHandler)
	server.AddPrompt(&mcp.Prompt{
		Name:        "decide",
		Description: "Choose one of several already-known options against explicit criteria. No API key required.",
		Arguments: []*mcp.PromptArgument{
			{Name: "question", Description: "The decision to make", Required: true},
			{Name: "options", Description: "The enumerated alternatives to choose between", Required: true},
			{Name: "criteria", Description: "Criteria the choice must be judged against", Required: false},
			{Name: "constraints", Description: "Hard constraints the choice must satisfy", Required: false},
			{Name: "context", Description: "Project context to ground the decision", Required: false},
			{Name: "provider", Description: "gemini, chatgpt, or both (default both)", Required: false},
		},
	}, decidePrompt)
}

type decideArgs struct {
	Question    string `json:"question" jsonschema:"the decision to make"`
	Options     string `json:"options" jsonschema:"the enumerated alternatives to choose between, one per line"`
	Criteria    string `json:"criteria,omitempty" jsonschema:"optional criteria the choice must be judged against"`
	Constraints string `json:"constraints,omitempty" jsonschema:"optional hard constraints the choice must satisfy"`
	Context     string `json:"context,omitempty" jsonschema:"optional project context to ground the decision"`
	Provider    string `json:"provider,omitempty" jsonschema:"gemini, chatgpt, or both (default both)"`
}

// Options configures a decide request.
type Options struct {
	Question    string
	Options     string
	Criteria    string
	Constraints string
	Context     string
	Provider    string
}

// Run composes the decision prompt and dispatches it to the selected providers.
func Run(ctx context.Context, opts Options) (string, error) {
	if strings.TrimSpace(opts.Question) == "" {
		return "", errors.New("question is required")
	}
	if strings.TrimSpace(opts.Options) == "" {
		return "", errors.New("options is required")
	}
	names, err := dispatch.Resolve(opts.Provider)
	if err != nil {
		return "", err
	}
	results := fanOut(ctx, buildPrompt(opts), names)
	return dispatch.Format(results)
}

func decideHandler(ctx context.Context, _ *mcp.CallToolRequest, args decideArgs) (*mcp.CallToolResult, any, error) {
	text, err := Run(ctx, Options(args))
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}, nil, nil
}

func decidePrompt(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	get := func(key string) string {
		if req.Params.Arguments == nil {
			return ""
		}
		return req.Params.Arguments[key]
	}
	text, err := Run(ctx, Options{
		Question:    get("question"),
		Options:     get("options"),
		Criteria:    get("criteria"),
		Constraints: get("constraints"),
		Context:     get("context"),
		Provider:    get("provider"),
	})
	if err != nil {
		return nil, err
	}
	return &mcp.GetPromptResult{
		Description: "Choose between known options",
		Messages: []*mcp.PromptMessage{
			{Content: &mcp.TextContent{Text: text}, Role: mcp.Role("user")},
		},
	}, nil
}

const deciderRole = `You are a pragmatic senior engineer making a firm decision. ` +
	`You must choose exactly one of the options provided and commit to it. ` +
	`Judge the options only against the criteria and constraints given, and never ` +
	`introduce an option that was not offered. Be decisive: a clear recommendation ` +
	`is more useful than a balanced summary.`

const decideInstructions = `Respond with exactly these sections and nothing else:

1. Recommendation: the single option you choose, quoted exactly as it was given.
2. Rationale: a short explanation of why it wins, tied to the criteria.
3. Evaluation: a markdown table with one row per option, a column for each
   criterion, a Pros column, a Cons column, and a Score out of 10.
4. Risks: a bulleted list of the main risks of your recommendation, each with a
   short mitigation.
5. What would change the decision: the evidence or condition that would reverse
   it, or "None".

Use numbered points for the sections and bullets for details. Keep it concise.
Do not restate the question or the options verbatim beyond the recommendation.`

func buildPrompt(opts Options) string {
	var b strings.Builder
	b.WriteString(deciderRole)
	b.WriteString("\n\n")
	if c := strings.TrimSpace(opts.Context); c != "" {
		b.WriteString("## Project context\n\n")
		b.WriteString(c)
		b.WriteString("\n\n")
	}
	b.WriteString("## Decision to make\n\n")
	b.WriteString(strings.TrimSpace(opts.Question))
	b.WriteString("\n\n")
	b.WriteString("## Options to choose between\n\n")
	b.WriteString(strings.TrimSpace(opts.Options))
	b.WriteString("\n\n")
	if c := strings.TrimSpace(opts.Criteria); c != "" {
		b.WriteString("## Criteria\n\n")
		b.WriteString(c)
		b.WriteString("\n\n")
	}
	if c := strings.TrimSpace(opts.Constraints); c != "" {
		b.WriteString("## Constraints\n\n")
		b.WriteString(c)
		b.WriteString("\n\n")
	}
	b.WriteString(decideInstructions)
	return b.String()
}

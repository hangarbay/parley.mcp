// Package plan implements the plan tool: it sends a project or task
// description, optionally grounded with context, to one or more external AIs
// and returns their structured breakdowns.
package plan

import (
	"context"
	"errors"
	"strings"

	"github.com/hangarbay/parley.mcp/internal/dispatch"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fanOut is dispatch.FanOut, swappable in tests.
var fanOut = dispatch.FanOut

// Register adds the plan tool and prompt to server.
func Register(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "plan",
		Description: "Plan a project, task, or feature: send a task description " +
			"to one or more external AIs and get back a structured breakdown " +
			"(goal, numbered phases with steps and deliverables, risks, open " +
			"questions). provider defaults to both gemini and chatgpt.",
	}, planHandler)
	server.AddPrompt(&mcp.Prompt{
		Name:        "plan",
		Description: "Plan a project, task, or feature with one or more external AIs. No API key required.",
		Arguments: []*mcp.PromptArgument{
			{Name: "task", Description: "The project or task to plan", Required: true},
			{Name: "context", Description: "Project context to ground the plan", Required: false},
			{Name: "provider", Description: "gemini, chatgpt, or both (default both)", Required: false},
		},
	}, planPrompt)
}

type planArgs struct {
	Task     string `json:"task" jsonschema:"the project or task to plan"`
	Context  string `json:"context,omitempty" jsonschema:"optional project context to ground the plan"`
	Provider string `json:"provider,omitempty" jsonschema:"gemini, chatgpt, or both (default both)"`
}

// Options configures a plan request.
type Options struct {
	Task     string
	Context  string
	Provider string
}

// Run composes the planning prompt and dispatches it to the selected providers.
func Run(ctx context.Context, opts Options) (string, error) {
	if strings.TrimSpace(opts.Task) == "" {
		return "", errors.New("task is required")
	}
	names, err := dispatch.Resolve(opts.Provider)
	if err != nil {
		return "", err
	}
	results := fanOut(ctx, buildPrompt(opts), names)
	return dispatch.Format(results)
}

func planHandler(ctx context.Context, _ *mcp.CallToolRequest, args planArgs) (*mcp.CallToolResult, any, error) {
	text, err := Run(ctx, Options(args))
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}, nil, nil
}

func planPrompt(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	get := func(key string) string {
		if req.Params.Arguments == nil {
			return ""
		}
		return req.Params.Arguments[key]
	}
	text, err := Run(ctx, Options{
		Task:     get("task"),
		Context:  get("context"),
		Provider: get("provider"),
	})
	if err != nil {
		return nil, err
	}
	return &mcp.GetPromptResult{
		Description: "Plan a project or task",
		Messages: []*mcp.PromptMessage{
			{Content: &mcp.TextContent{Text: text}, Role: mcp.Role("user")},
		},
	}, nil
}

const plannerRole = `You are a senior engineering lead creating a practical, concrete plan. ` +
	`Break the work down into phases that can be executed in order, each with ` +
	`clear deliverables. Prefer specifics over generic advice.`

const planInstructions = `Respond with exactly these sections and nothing else:

1. Goal: one sentence defining what the plan delivers.
2. Phases: a numbered list of phases, in execution order. Under each phase, use
   bullets for the concrete steps, a "Deliverables:" bullet, and a
   "Depends on:" bullet naming any earlier phase it requires (or "None").
3. Risks: a bulleted list of the main risks, each followed by a short mitigation.
4. Open questions: a bulleted list of decisions still needed before work starts,
   or "None".

Use numbered points for the sections and phases, and bullet points for all
details. Keep it concise. Do not restate the task.`

func buildPrompt(opts Options) string {
	var b strings.Builder
	b.WriteString(plannerRole)
	b.WriteString("\n\n")
	if c := strings.TrimSpace(opts.Context); c != "" {
		b.WriteString("## Project context\n\n")
		b.WriteString(c)
		b.WriteString("\n\n")
	}
	b.WriteString("## Task to plan\n\n")
	b.WriteString(strings.TrimSpace(opts.Task))
	b.WriteString("\n\n")
	b.WriteString(planInstructions)
	return b.String()
}

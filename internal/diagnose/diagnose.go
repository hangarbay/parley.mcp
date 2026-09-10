// Package diagnose implements the diagnose tool: it sends a failure together
// with observations, attempted fixes, and environment details to one or more
// external AIs and returns ranked competing causes plus tests that distinguish
// between them.
package diagnose

import (
	"context"
	"errors"
	"strings"

	"github.com/hangarbay/parley.mcp/internal/dispatch"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fanOut is dispatch.FanOut, swappable in tests.
var fanOut = dispatch.FanOut

// Register adds the diagnose tool and prompt to server.
func Register(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "diagnose",
		Description: "For an existing failure with observations and attempted fixes, " +
			"rank competing causes and propose tests that distinguish them. Use when " +
			"identifying the cause is the task and the cause is genuinely uncertain; " +
			"when there is no evidence yet, ask instead. provider defaults to both " +
			"gemini and chatgpt.",
	}, diagnoseHandler)
	server.AddPrompt(&mcp.Prompt{
		Name:        "diagnose",
		Description: "Rank competing causes of a failure and propose distinguishing tests. No API key required.",
		Arguments: []*mcp.PromptArgument{
			{Name: "problem", Description: "The failure or unexpected behavior observed", Required: true},
			{Name: "observations", Description: "Logs, errors, or measurements gathered so far", Required: false},
			{Name: "attempts", Description: "Fixes already tried and what happened", Required: false},
			{Name: "environment", Description: "Relevant environment or version details", Required: false},
			{Name: "context", Description: "Project context to ground the diagnosis", Required: false},
			{Name: "provider", Description: "gemini, chatgpt, or both (default both)", Required: false},
		},
	}, diagnosePrompt)
}

type diagnoseArgs struct {
	Problem      string `json:"problem" jsonschema:"the failure or unexpected behavior observed"`
	Observations string `json:"observations,omitempty" jsonschema:"optional logs, errors, or measurements gathered so far"`
	Attempts     string `json:"attempts,omitempty" jsonschema:"optional fixes already tried and what happened"`
	Environment  string `json:"environment,omitempty" jsonschema:"optional relevant environment or version details"`
	Context      string `json:"context,omitempty" jsonschema:"optional project context to ground the diagnosis"`
	Provider     string `json:"provider,omitempty" jsonschema:"gemini, chatgpt, or both (default both)"`
}

// Options configures a diagnose request.
type Options struct {
	Problem      string
	Observations string
	Attempts     string
	Environment  string
	Context      string
	Provider     string
}

// Run composes the diagnostic prompt and dispatches it to the selected providers.
func Run(ctx context.Context, opts Options) (string, error) {
	if strings.TrimSpace(opts.Problem) == "" {
		return "", errors.New("problem is required")
	}
	names, err := dispatch.Resolve(opts.Provider)
	if err != nil {
		return "", err
	}
	results := fanOut(ctx, buildPrompt(opts), names)
	return dispatch.Format(results)
}

func diagnoseHandler(ctx context.Context, _ *mcp.CallToolRequest, args diagnoseArgs) (*mcp.CallToolResult, any, error) {
	text, err := Run(ctx, Options(args))
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}, nil, nil
}

func diagnosePrompt(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	get := func(key string) string {
		if req.Params.Arguments == nil {
			return ""
		}
		return req.Params.Arguments[key]
	}
	text, err := Run(ctx, Options{
		Problem:      get("problem"),
		Observations: get("observations"),
		Attempts:     get("attempts"),
		Environment:  get("environment"),
		Context:      get("context"),
		Provider:     get("provider"),
	})
	if err != nil {
		return nil, err
	}
	return &mcp.GetPromptResult{
		Description: "Diagnose a failure",
		Messages: []*mcp.PromptMessage{
			{Content: &mcp.TextContent{Text: text}, Role: mcp.Role("user")},
		},
	}, nil
}

const diagnosticianRole = `You are a systematic debugging engineer. You reason only from the ` +
	`evidence given, rank competing causes instead of fixating on one, and never propose ` +
	`a fix without first stating what would confirm the cause. Prioritize the hypothesis ` +
	`that best explains every observation.`

const diagnoseInstructions = `Respond with exactly these sections and nothing else:

1. Most likely cause: one sentence naming the single most probable explanation.
2. Hypotheses: a numbered list of the competing causes, each with a
   Likelihood (High, Medium, or Low), the Evidence for it, and any observation
   it fails to explain.
3. Discriminating tests: a numbered list of concrete tests. For each, state the
   test, what result would confirm the hypothesis, and what result would rule it
   out.
4. Recommended next step: the single concrete action to take first.
5. What would change the diagnosis: new evidence that would revise it, or "None".

Use numbered points for the sections and bullets for details. Keep it concise.
Do not restate the problem, and do not propose a fix as the answer: the tests
that isolate the cause come first.`

func buildPrompt(opts Options) string {
	var b strings.Builder
	b.WriteString(diagnosticianRole)
	b.WriteString("\n\n")
	if c := strings.TrimSpace(opts.Context); c != "" {
		b.WriteString("## Project context\n\n")
		b.WriteString(c)
		b.WriteString("\n\n")
	}
	b.WriteString("## Problem\n\n")
	b.WriteString(strings.TrimSpace(opts.Problem))
	b.WriteString("\n\n")
	if c := strings.TrimSpace(opts.Observations); c != "" {
		b.WriteString("## Observations\n\n")
		b.WriteString(c)
		b.WriteString("\n\n")
	}
	if c := strings.TrimSpace(opts.Attempts); c != "" {
		b.WriteString("## Already attempted\n\n")
		b.WriteString(c)
		b.WriteString("\n\n")
	}
	if c := strings.TrimSpace(opts.Environment); c != "" {
		b.WriteString("## Environment\n\n")
		b.WriteString(c)
		b.WriteString("\n\n")
	}
	b.WriteString(diagnoseInstructions)
	return b.String()
}

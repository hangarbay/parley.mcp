// Package ask implements the generic ask tool: it sends a question to one or
// more external AI providers over their free web UIs and returns their answers
// under labeled sections.
package ask

import (
	"context"
	"errors"
	"strings"

	"github.com/hangarbay/parley.mcp/internal/dispatch"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fanOut is dispatch.FanOut, swappable in tests.
var fanOut = dispatch.FanOut

// Register adds the ask tool and prompt to server.
func Register(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "ask",
		Description: "Ask one or more external AIs a question over their free web UIs, " +
			"with no API key. provider defaults to both gemini and chatgpt; pass a name to " +
			"query just one. Answers come back under labeled sections.",
	}, askHandler)
	server.AddPrompt(&mcp.Prompt{
		Name:        "ask",
		Description: "Ask one or more external AIs a question. No API key required.",
		Arguments: []*mcp.PromptArgument{
			{Name: "prompt", Description: "The question or prompt to send", Required: true},
			{Name: "provider", Description: "gemini, chatgpt, or both (default both)", Required: false},
		},
	}, askPrompt)
}

type askArgs struct {
	Prompt   string `json:"prompt" jsonschema:"the question or prompt to send"`
	Provider string `json:"provider,omitempty" jsonschema:"gemini, chatgpt, or both (default both)"`
}

// Run asks the selected providers and returns their formatted answers.
func Run(ctx context.Context, prompt, provider string) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", errors.New("prompt is required")
	}
	names, err := dispatch.Resolve(provider)
	if err != nil {
		return "", err
	}
	results := fanOut(ctx, prompt, names)
	return dispatch.Format(results)
}

func askHandler(ctx context.Context, _ *mcp.CallToolRequest, args askArgs) (*mcp.CallToolResult, any, error) {
	text, err := Run(ctx, args.Prompt, args.Provider)
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}, nil, nil
}

func askPrompt(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	get := func(key string) string {
		if req.Params.Arguments == nil {
			return ""
		}
		return req.Params.Arguments[key]
	}
	text, err := Run(ctx, get("prompt"), get("provider"))
	if err != nil {
		return nil, err
	}
	return &mcp.GetPromptResult{
		Description: "Ask an external AI",
		Messages: []*mcp.PromptMessage{
			{Content: &mcp.TextContent{Text: text}, Role: mcp.Role("user")},
		},
	}, nil
}

//go:build integration

// Integration tests that drive the real providers through the MCP surface.
// They talk to live web UIs, so they need network access, and the Gemini case
// needs Firefox. Run them with `make integ`.
package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAskGemini(t *testing.T) {
	assertEchoes(t, "ask_gemini", 90*time.Second)
}

func TestAskChatGPT(t *testing.T) {
	assertEchoes(t, "ask_chatgpt", 2*time.Minute)
}

// assertEchoes asks the tool to reply with a sentinel token and checks that the
// token comes back, which proves the whole chain works: session bootstrap,
// request signing, streaming, and text extraction.
func assertEchoes(t *testing.T, tool string, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	server := newServer()
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "parley-integration",
		Version: "0.1.0",
	}, nil)

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	go func() {
		_ = server.Run(ctx, serverTransport)
	}()

	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	sentinel := "PARLEY-" + strings.ToUpper(tool) + "-OK"
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      tool,
		Arguments: map[string]any{"prompt": "Reply with exactly this token, nothing else: " + sentinel},
	})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}

	var answer strings.Builder
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			answer.WriteString(text.Text)
		}
	}
	got := strings.TrimSpace(answer.String())
	t.Logf("%s answered: %q", tool, got)

	if got == "" {
		t.Fatalf("%s: empty answer", tool)
	}
	if !strings.Contains(strings.ToUpper(got), sentinel) {
		t.Fatalf("%s: answer does not contain %q", tool, sentinel)
	}
}

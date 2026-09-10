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
	assertEchoes(t, "gemini", 90*time.Second)
}

func TestAskChatGPT(t *testing.T) {
	assertEchoes(t, "chatgpt", 2*time.Minute)
}

// TestAskBoth exercises the fan-out path: both providers answer concurrently and
// the result is returned under labeled sections.
func TestAskBoth(t *testing.T) {
	sentinel := "PARLEY-ASK-BOTH-OK"
	got := callTool(t, "ask", map[string]any{
		"prompt":   "Reply with exactly this token, nothing else: " + sentinel,
		"provider": "both",
	}, 2*time.Minute)
	t.Logf("ask both answered: %q", got)

	if !strings.Contains(got, "## Gemini") || !strings.Contains(got, "## ChatGPT") {
		t.Fatalf("ask both should include both provider sections: %q", got)
	}
	if !strings.Contains(strings.ToUpper(got), sentinel) {
		t.Fatalf("ask both: answer does not contain %q", sentinel)
	}
}

// TestReview drives the review tool end to end through the MCP surface. It
// omits provider, exercising the default fan-out to both providers.
func TestReview(t *testing.T) {
	diff := "--- a/greet.go\n" +
		"+++ b/greet.go\n" +
		"@@ -1,3 +1,4 @@\n" +
		" package main\n \n" +
		"+var Password = \"hunter2\"\n" +
		" func main() {}\n"
	got := callTool(t, "review", map[string]any{
		"diff":    diff,
		"context": "A tiny Go program.",
		"rules":   "Never hard-code secrets in source.",
	}, 3*time.Minute)
	t.Logf("review answered: %q", got)

	if !strings.Contains(strings.ToLower(got), "verdict") {
		t.Fatalf("review missing a verdict: %q", got)
	}
	if !strings.Contains(got, "## Gemini") || !strings.Contains(got, "## ChatGPT") {
		t.Fatalf("review should include both provider sections: %q", got)
	}
}

// assertEchoes asks one provider to reply with a sentinel token and checks that
// the token comes back, which proves the whole chain works: session bootstrap,
// request signing, streaming, and text extraction.
func assertEchoes(t *testing.T, provider string, timeout time.Duration) {
	t.Helper()
	sentinel := "PARLEY-" + strings.ToUpper(provider) + "-OK"
	got := callTool(t, "ask", map[string]any{
		"prompt":   "Reply with exactly this token, nothing else: " + sentinel,
		"provider": provider,
	}, timeout)
	t.Logf("ask %s answered: %q", provider, got)

	if got == "" {
		t.Fatalf("ask %s: empty answer", provider)
	}
	if !strings.Contains(strings.ToUpper(got), sentinel) {
		t.Fatalf("ask %s: answer does not contain %q", provider, sentinel)
	}
}

// callTool runs tool through a real in-memory MCP client/server pair and returns
// the concatenated text content.
func callTool(t *testing.T, tool string, args map[string]any, timeout time.Duration) string {
	t.Helper()
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

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}

	var answer strings.Builder
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			answer.WriteString(text.Text)
		}
	}
	return strings.TrimSpace(answer.String())
}

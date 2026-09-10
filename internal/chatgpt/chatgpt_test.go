package chatgpt

import "testing"

func TestTurnFailure(t *testing.T) {
	rejected := `<span data-conversation-control="failed" data-partial-island="conversation-control" hidden ` +
		`data-conversation="{&quot;messages&quot;:[{&quot;content&quot;:&quot;Invalid prompt&quot;,&quot;id&quot;:&quot;error-1&quot;,&quot;role&quot;:&quot;assistant&quot;}],&quot;title&quot;:&quot;New chat&quot;}" ` +
		`data-failure-attribution="{&quot;failureOrigin&quot;:&quot;server_policy&quot;}"></span>`
	if got := turnFailure(rejected); got != "Invalid prompt" {
		t.Fatalf("turnFailure(rejected) = %q, want %q", got, "Invalid prompt")
	}

	// The failed conversation repeats the user's prompt before the assistant
	// message; only the assistant message may be reported.
	withPrompt := `<span data-conversation-control="failed" hidden ` +
		`data-conversation="{&quot;messages&quot;:[{&quot;content&quot;:&quot;review this enormous diff&quot;,&quot;role&quot;:&quot;user&quot;},{&quot;content&quot;:&quot;Chat is temporarily unavailable. Try again.&quot;,&quot;role&quot;:&quot;assistant&quot;}]}"></span>`
	if got := turnFailure(withPrompt); got != "Chat is temporarily unavailable. Try again." {
		t.Fatalf("turnFailure(withPrompt) = %q, want the assistant message", got)
	}

	if got := turnFailure(`<span data-conversation-control="started"></span>Verdict: APPROVE`); got != "" {
		t.Fatalf("turnFailure(normal) = %q, want empty", got)
	}

	bare := `<span data-conversation-control="failed" hidden></span>`
	if got := turnFailure(bare); got != "the request was rejected" {
		t.Fatalf("turnFailure(bare) = %q, want %q", got, "the request was rejected")
	}
}

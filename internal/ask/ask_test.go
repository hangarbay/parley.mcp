package ask

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hangarbay/parley.mcp/internal/dispatch"
)

func stubFanOut(fn func(context.Context, string, []string) []dispatch.Result) func() {
	orig := fanOut
	fanOut = fn
	return func() { fanOut = orig }
}

func TestRunRequiresPrompt(t *testing.T) {
	if _, err := Run(context.Background(), "   ", ""); err == nil {
		t.Fatal("expected error for empty prompt")
	}
}

func TestRunUnknownProvider(t *testing.T) {
	if _, err := Run(context.Background(), "hi", "nope"); err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestRunDefaultsToBoth(t *testing.T) {
	var got []string
	restore := stubFanOut(func(_ context.Context, _ string, names []string) []dispatch.Result {
		got = names
		return []dispatch.Result{
			{Provider: "gemini", Title: "Gemini", Text: "g"},
			{Provider: "chatgpt", Title: "ChatGPT", Text: "c"},
		}
	})
	defer restore()

	out, err := Run(context.Background(), "hi", "")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Join(got, ",") != "gemini,chatgpt" {
		t.Fatalf("names = %v, want [gemini chatgpt]", got)
	}
	if !strings.Contains(out, "## Gemini") || !strings.Contains(out, "## ChatGPT") {
		t.Fatalf("expected labeled sections, got %q", out)
	}
}

func TestRunSingleProviderHasNoHeader(t *testing.T) {
	var got []string
	restore := stubFanOut(func(_ context.Context, _ string, names []string) []dispatch.Result {
		got = names
		return []dispatch.Result{{Provider: names[0], Title: "Gemini", Text: "just the answer"}}
	})
	defer restore()

	out, err := Run(context.Background(), "hi", "gemini")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Join(got, ",") != "gemini" {
		t.Fatalf("names = %v, want [gemini]", got)
	}
	if out != "just the answer" {
		t.Fatalf("got %q", out)
	}
}

func TestRunBestEffortKeepsSurvivors(t *testing.T) {
	restore := stubFanOut(func(_ context.Context, _ string, _ []string) []dispatch.Result {
		return []dispatch.Result{
			{Provider: "gemini", Title: "Gemini", Err: errors.New("no firefox")},
			{Provider: "chatgpt", Title: "ChatGPT", Text: "the answer"},
		}
	})
	defer restore()

	out, err := Run(context.Background(), "hi", "")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "the answer") || !strings.Contains(out, "_unavailable: no firefox_") {
		t.Fatalf("unexpected output %q", out)
	}
}

func TestRunAllProvidersFailed(t *testing.T) {
	restore := stubFanOut(func(_ context.Context, _ string, _ []string) []dispatch.Result {
		return []dispatch.Result{
			{Provider: "gemini", Title: "Gemini", Err: errors.New("no firefox")},
			{Provider: "chatgpt", Title: "ChatGPT", Err: errors.New("throttled")},
		}
	})
	defer restore()

	if _, err := Run(context.Background(), "hi", ""); err == nil {
		t.Fatal("expected error when all providers fail")
	}
}

func TestRunTrimsPromptBeforeDispatch(t *testing.T) {
	var gotPrompt string
	restore := stubFanOut(func(_ context.Context, prompt string, _ []string) []dispatch.Result {
		gotPrompt = prompt
		return []dispatch.Result{{Provider: "gemini", Title: "Gemini", Text: "ok"}}
	})
	defer restore()

	if _, err := Run(context.Background(), "  hi there  ", "gemini"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotPrompt != "hi there" {
		t.Fatalf("prompt = %q, want %q", gotPrompt, "hi there")
	}
}

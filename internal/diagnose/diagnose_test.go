package diagnose

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

func TestBuildPromptIncludesSections(t *testing.T) {
	got := buildPrompt(Options{
		Problem:      "requests time out under load",
		Observations: "p99 latency 5s, CPU normal",
		Attempts:     "raised the timeout, rolled back a dep",
		Environment:  "Go 1.27, linux/arm64",
		Context:      "an HTTP API behind a proxy",
	})
	for _, want := range []string{
		"## Project context",
		"an HTTP API behind a proxy",
		"## Problem",
		"requests time out under load",
		"## Observations",
		"p99 latency 5s, CPU normal",
		"## Already attempted",
		"raised the timeout, rolled back a dep",
		"## Environment",
		"Go 1.27, linux/arm64",
		"1. Most likely cause:",
		"2. Hypotheses:",
		"3. Discriminating tests:",
		"4. Recommended next step:",
		"5. What would change the diagnosis:",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q\n%s", want, got)
		}
	}
}

func TestBuildPromptOmitsEmptySections(t *testing.T) {
	got := buildPrompt(Options{Problem: "it broke"})
	for _, unwanted := range []string{"## Project context", "## Observations", "## Already attempted", "## Environment"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("empty section %q should be omitted\n%s", unwanted, got)
		}
	}
	if !strings.Contains(got, "## Problem") {
		t.Fatal("problem section should always be present")
	}
}

func TestRunRequiresProblem(t *testing.T) {
	if _, err := Run(context.Background(), Options{}); err == nil {
		t.Fatal("expected error for empty problem")
	}
	if _, err := Run(context.Background(), Options{Problem: "   "}); err == nil {
		t.Fatal("expected error for blank problem")
	}
}

func TestRunUnknownProvider(t *testing.T) {
	if _, err := Run(context.Background(), Options{Problem: "x", Provider: "nope"}); err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestRunDefaultsToBothProviders(t *testing.T) {
	var got []string
	restore := stubFanOut(func(_ context.Context, _ string, names []string) []dispatch.Result {
		got = names
		return []dispatch.Result{
			{Provider: "gemini", Title: "Gemini", Text: "1. Most likely cause: DNS"},
			{Provider: "chatgpt", Title: "ChatGPT", Text: "1. Most likely cause: pool exhaustion"},
		}
	})
	defer restore()

	out, err := Run(context.Background(), Options{Problem: "timeouts"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Join(got, ",") != "gemini,chatgpt" {
		t.Fatalf("names = %v, want [gemini chatgpt]", got)
	}
	if !strings.Contains(out, "## Gemini") || !strings.Contains(out, "## ChatGPT") {
		t.Fatalf("expected labeled answers, got %q", out)
	}
}

func TestRunSingleProviderHasNoHeader(t *testing.T) {
	var got []string
	restore := stubFanOut(func(_ context.Context, _ string, names []string) []dispatch.Result {
		got = names
		return []dispatch.Result{{Provider: "chatgpt", Title: "ChatGPT", Text: "1. Most likely cause: DNS"}}
	})
	defer restore()

	out, err := Run(context.Background(), Options{Problem: "timeouts", Provider: "chatgpt"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Join(got, ",") != "chatgpt" {
		t.Fatalf("names = %v, want [chatgpt]", got)
	}
	if out != "1. Most likely cause: DNS" {
		t.Fatalf("got %q", out)
	}
}

func TestRunSendsPromptWithAllFields(t *testing.T) {
	var gotPrompt string
	restore := stubFanOut(func(_ context.Context, prompt string, _ []string) []dispatch.Result {
		gotPrompt = prompt
		return []dispatch.Result{{Provider: "gemini", Title: "Gemini", Text: "diagnosis"}}
	})
	defer restore()

	if _, err := Run(context.Background(), Options{
		Problem:      "the-problem-body",
		Observations: "the-observations-body",
		Attempts:     "the-attempts-body",
		Environment:  "the-environment-body",
		Context:      "the-context-body",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, want := range []string{
		"the-problem-body",
		"the-observations-body",
		"the-attempts-body",
		"the-environment-body",
		"the-context-body",
	} {
		if !strings.Contains(gotPrompt, want) {
			t.Fatalf("prompt missing %q", want)
		}
	}
}

func TestRunBestEffortKeepsSurvivors(t *testing.T) {
	restore := stubFanOut(func(_ context.Context, _ string, _ []string) []dispatch.Result {
		return []dispatch.Result{
			{Provider: "gemini", Title: "Gemini", Err: errors.New("no firefox")},
			{Provider: "chatgpt", Title: "ChatGPT", Text: "1. Most likely cause: DNS"},
		}
	})
	defer restore()

	out, err := Run(context.Background(), Options{Problem: "timeouts"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "DNS") || !strings.Contains(out, "_unavailable: no firefox_") {
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

	if _, err := Run(context.Background(), Options{Problem: "timeouts"}); err == nil {
		t.Fatal("expected error when all providers fail")
	}
}

package review

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
		Diff:    "diff --git a/x b/x",
		Context: "a Go CLI",
		Rules:   "never log secrets",
	})
	for _, want := range []string{
		"## Project context",
		"a Go CLI",
		"## Rules the change must follow",
		"never log secrets",
		"## Change under review",
		"diff --git a/x b/x",
		"1. Verdict:",
		"4. Rule violations:",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q\n%s", want, got)
		}
	}
}

func TestBuildPromptOmitsEmptySections(t *testing.T) {
	got := buildPrompt(Options{Diff: "some change"})
	if strings.Contains(got, "## Project context") {
		t.Fatal("empty context should be omitted")
	}
	if strings.Contains(got, "## Rules the change must follow") {
		t.Fatal("empty rules should be omitted")
	}
	if !strings.Contains(got, "## Change under review") {
		t.Fatal("change section should always be present")
	}
}

func TestRunRequiresDiff(t *testing.T) {
	if _, err := Run(context.Background(), Options{}); err == nil {
		t.Fatal("expected error for empty diff")
	}
	if _, err := Run(context.Background(), Options{Diff: "   "}); err == nil {
		t.Fatal("expected error for blank diff")
	}
}

func TestRunUnknownProvider(t *testing.T) {
	if _, err := Run(context.Background(), Options{Diff: "x", Provider: "nope"}); err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestRunDefaultsToBothProviders(t *testing.T) {
	var got []string
	restore := stubFanOut(func(_ context.Context, _ string, names []string) []dispatch.Result {
		got = names
		return []dispatch.Result{
			{Provider: "gemini", Title: "Gemini", Text: "1. Verdict: APPROVE\n"},
			{Provider: "chatgpt", Title: "ChatGPT", Text: "1. Verdict: APPROVE\n"},
		}
	})
	defer restore()

	out, err := Run(context.Background(), Options{Diff: "change"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Join(got, ",") != "gemini,chatgpt" {
		t.Fatalf("names = %v, want [gemini chatgpt]", got)
	}
	if !strings.Contains(out, "## Gemini") || !strings.Contains(out, "## ChatGPT") {
		t.Fatalf("expected labeled verdicts, got %q", out)
	}
}

func TestRunSingleProviderHasNoHeader(t *testing.T) {
	var got []string
	restore := stubFanOut(func(_ context.Context, _ string, names []string) []dispatch.Result {
		got = names
		return []dispatch.Result{{Provider: "chatgpt", Title: "ChatGPT", Text: "1. Verdict: APPROVE"}}
	})
	defer restore()

	out, err := Run(context.Background(), Options{Diff: "x", Provider: "chatgpt"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Join(got, ",") != "chatgpt" {
		t.Fatalf("names = %v, want [chatgpt]", got)
	}
	if out != "1. Verdict: APPROVE" {
		t.Fatalf("got %q", out)
	}
}

func TestRunSendsPromptWithDiffContextAndRules(t *testing.T) {
	var gotPrompt string
	restore := stubFanOut(func(_ context.Context, prompt string, _ []string) []dispatch.Result {
		gotPrompt = prompt
		return []dispatch.Result{{Provider: "gemini", Title: "Gemini", Text: "review"}}
	})
	defer restore()

	if _, err := Run(context.Background(), Options{
		Diff:    "the-diff-body",
		Context: "the-context-body",
		Rules:   "the-rules-body",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, want := range []string{"the-diff-body", "the-context-body", "the-rules-body"} {
		if !strings.Contains(gotPrompt, want) {
			t.Fatalf("prompt missing %q", want)
		}
	}
}

func TestRunBestEffortKeepsSurvivors(t *testing.T) {
	restore := stubFanOut(func(_ context.Context, _ string, _ []string) []dispatch.Result {
		return []dispatch.Result{
			{Provider: "gemini", Title: "Gemini", Err: errors.New("no firefox")},
			{Provider: "chatgpt", Title: "ChatGPT", Text: "1. Verdict: APPROVE"},
		}
	})
	defer restore()

	out, err := Run(context.Background(), Options{Diff: "x"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "1. Verdict: APPROVE") || !strings.Contains(out, "_unavailable: no firefox_") {
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

	if _, err := Run(context.Background(), Options{Diff: "x"}); err == nil {
		t.Fatal("expected error when all providers fail")
	}
}

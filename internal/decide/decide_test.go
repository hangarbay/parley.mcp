package decide

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
		Question:    "which datastore",
		Options:     "postgres\nsqlite",
		Criteria:    "operational burden",
		Constraints: "must run in one container",
		Context:     "a small Go service",
	})
	for _, want := range []string{
		"## Project context",
		"a small Go service",
		"## Decision to make",
		"which datastore",
		"## Options to choose between",
		"postgres",
		"sqlite",
		"## Criteria",
		"operational burden",
		"## Constraints",
		"must run in one container",
		"1. Recommendation:",
		"2. Rationale:",
		"3. Evaluation:",
		"4. Risks:",
		"5. What would change the decision:",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q\n%s", want, got)
		}
	}
}

func TestBuildPromptOmitsEmptySections(t *testing.T) {
	got := buildPrompt(Options{Question: "q", Options: "a\nb"})
	for _, unwanted := range []string{"## Project context", "## Criteria", "## Constraints"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("empty section %q should be omitted\n%s", unwanted, got)
		}
	}
	for _, want := range []string{"## Decision to make", "## Options to choose between"} {
		if !strings.Contains(got, want) {
			t.Fatalf("required section %q missing", want)
		}
	}
}

func TestRunRequiresQuestion(t *testing.T) {
	if _, err := Run(context.Background(), Options{Options: "a\nb"}); err == nil {
		t.Fatal("expected error for empty question")
	}
	if _, err := Run(context.Background(), Options{Question: "   ", Options: "a\nb"}); err == nil {
		t.Fatal("expected error for blank question")
	}
}

func TestRunRequiresOptions(t *testing.T) {
	if _, err := Run(context.Background(), Options{Question: "q"}); err == nil {
		t.Fatal("expected error for empty options")
	}
	if _, err := Run(context.Background(), Options{Question: "q", Options: "   "}); err == nil {
		t.Fatal("expected error for blank options")
	}
}

func TestRunUnknownProvider(t *testing.T) {
	if _, err := Run(context.Background(), Options{Question: "q", Options: "a", Provider: "nope"}); err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestRunDefaultsToBothProviders(t *testing.T) {
	var got []string
	restore := stubFanOut(func(_ context.Context, _ string, names []string) []dispatch.Result {
		got = names
		return []dispatch.Result{
			{Provider: "gemini", Title: "Gemini", Text: "1. Recommendation: postgres"},
			{Provider: "chatgpt", Title: "ChatGPT", Text: "1. Recommendation: sqlite"},
		}
	})
	defer restore()

	out, err := Run(context.Background(), Options{Question: "q", Options: "a\nb"})
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
		return []dispatch.Result{{Provider: "chatgpt", Title: "ChatGPT", Text: "1. Recommendation: postgres"}}
	})
	defer restore()

	out, err := Run(context.Background(), Options{Question: "q", Options: "a\nb", Provider: "chatgpt"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Join(got, ",") != "chatgpt" {
		t.Fatalf("names = %v, want [chatgpt]", got)
	}
	if out != "1. Recommendation: postgres" {
		t.Fatalf("got %q", out)
	}
}

func TestRunSendsPromptWithAllFields(t *testing.T) {
	var gotPrompt string
	restore := stubFanOut(func(_ context.Context, prompt string, _ []string) []dispatch.Result {
		gotPrompt = prompt
		return []dispatch.Result{{Provider: "gemini", Title: "Gemini", Text: "decision"}}
	})
	defer restore()

	if _, err := Run(context.Background(), Options{
		Question:    "the-question-body",
		Options:     "the-options-body",
		Criteria:    "the-criteria-body",
		Constraints: "the-constraints-body",
		Context:     "the-context-body",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, want := range []string{
		"the-question-body",
		"the-options-body",
		"the-criteria-body",
		"the-constraints-body",
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
			{Provider: "chatgpt", Title: "ChatGPT", Text: "1. Recommendation: postgres"},
		}
	})
	defer restore()

	out, err := Run(context.Background(), Options{Question: "q", Options: "a\nb"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "postgres") || !strings.Contains(out, "_unavailable: no firefox_") {
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

	if _, err := Run(context.Background(), Options{Question: "q", Options: "a\nb"}); err == nil {
		t.Fatal("expected error when all providers fail")
	}
}

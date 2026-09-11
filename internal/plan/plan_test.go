package plan

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
		Task:    "migrate the CLI to cobra",
		Context: "a Go CLI",
	})
	for _, want := range []string{
		"## Project context",
		"a Go CLI",
		"## Task to plan",
		"migrate the CLI to cobra",
		"1. Goal:",
		"2. Phases:",
		"3. Risks:",
		"4. Open questions:",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q\n%s", want, got)
		}
	}
}

func TestBuildPromptOmitsEmptyContext(t *testing.T) {
	got := buildPrompt(Options{Task: "some task"})
	if strings.Contains(got, "## Project context") {
		t.Fatal("empty context should be omitted")
	}
	if !strings.Contains(got, "## Task to plan") {
		t.Fatal("task section should always be present")
	}
}

func TestRunRequiresTask(t *testing.T) {
	if _, err := Run(context.Background(), Options{}); err == nil {
		t.Fatal("expected error for empty task")
	}
	if _, err := Run(context.Background(), Options{Task: "   "}); err == nil {
		t.Fatal("expected error for blank task")
	}
}

func TestRunUnknownProvider(t *testing.T) {
	if _, err := Run(context.Background(), Options{Task: "x", Provider: "nope"}); err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestRunDefaultsToBothProviders(t *testing.T) {
	var got []string
	restore := stubFanOut(func(_ context.Context, _ string, names []string) []dispatch.Result {
		got = names
		return []dispatch.Result{
			{Provider: "gemini", Title: "Gemini", Text: "2. Phases:\n"},
			{Provider: "chatgpt", Title: "ChatGPT", Text: "2. Phases:\n"},
		}
	})
	defer restore()

	out, err := Run(context.Background(), Options{Task: "task"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Join(got, ",") != "gemini,chatgpt" {
		t.Fatalf("names = %v, want [gemini chatgpt]", got)
	}
	if !strings.Contains(out, "## Gemini") || !strings.Contains(out, "## ChatGPT") {
		t.Fatalf("expected labeled plans, got %q", out)
	}
}

func TestRunSingleProviderHasNoHeader(t *testing.T) {
	var got []string
	restore := stubFanOut(func(_ context.Context, _ string, names []string) []dispatch.Result {
		got = names
		return []dispatch.Result{{Provider: "chatgpt", Title: "ChatGPT", Text: "2. Phases:\n- step one"}}
	})
	defer restore()

	out, err := Run(context.Background(), Options{Task: "x", Provider: "chatgpt"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Join(got, ",") != "chatgpt" {
		t.Fatalf("names = %v, want [chatgpt]", got)
	}
	if out != "2. Phases:\n- step one" {
		t.Fatalf("got %q", out)
	}
}

func TestRunSendsPromptWithTaskAndContext(t *testing.T) {
	var gotPrompt string
	restore := stubFanOut(func(_ context.Context, prompt string, _ []string) []dispatch.Result {
		gotPrompt = prompt
		return []dispatch.Result{{Provider: "gemini", Title: "Gemini", Text: "plan"}}
	})
	defer restore()

	if _, err := Run(context.Background(), Options{
		Task:    "the-task-body",
		Context: "the-context-body",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, want := range []string{"the-task-body", "the-context-body"} {
		if !strings.Contains(gotPrompt, want) {
			t.Fatalf("prompt missing %q", want)
		}
	}
}

func TestRunBestEffortKeepsSurvivors(t *testing.T) {
	restore := stubFanOut(func(_ context.Context, _ string, _ []string) []dispatch.Result {
		return []dispatch.Result{
			{Provider: "gemini", Title: "Gemini", Err: errors.New("no firefox")},
			{Provider: "chatgpt", Title: "ChatGPT", Text: "2. Phases:\n- step one"},
		}
	})
	defer restore()

	out, err := Run(context.Background(), Options{Task: "x"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "2. Phases:\n- step one" {
		t.Fatalf("got %q, want the surviving provider's answer", out)
	}
}

func TestRunAllProvidersFailedReturnsEmpty(t *testing.T) {
	restore := stubFanOut(func(_ context.Context, _ string, _ []string) []dispatch.Result {
		return []dispatch.Result{
			{Provider: "gemini", Title: "Gemini", Err: errors.New("no firefox")},
			{Provider: "chatgpt", Title: "ChatGPT", Err: errors.New("throttled")},
		}
	})
	defer restore()

	out, err := Run(context.Background(), Options{Task: "x"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "" {
		t.Fatalf("got %q, want empty", out)
	}
}

package dispatch

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func stubRegistry(ps ...provider) func() {
	orig := registry
	registry = ps
	return func() { registry = orig }
}

func fakeProvider(name, title string, ask AskFunc) provider {
	return provider{name: name, title: title, ask: ask}
}

func TestNames(t *testing.T) {
	got := Names()
	want := []string{"gemini", "chatgpt"}
	if len(got) != len(want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Names() = %v, want %v", got, want)
		}
	}
}

func TestResolve(t *testing.T) {
	restore := stubRegistry(
		fakeProvider("alpha", "Alpha", nil),
		fakeProvider("beta", "Beta", nil),
	)
	defer restore()

	tests := []struct {
		request string
		want    []string
	}{
		{"", []string{"alpha", "beta"}},
		{"both", []string{"alpha", "beta"}},
		{"ALL", []string{"alpha", "beta"}},
		{"alpha", []string{"alpha"}},
		{"Beta", []string{"beta"}},
		{"beta,alpha", []string{"beta", "alpha"}},
		{"alpha,alpha", []string{"alpha"}},
		{" alpha , beta ", []string{"alpha", "beta"}},
	}
	for _, tt := range tests {
		got, err := Resolve(tt.request)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", tt.request, err)
		}
		if strings.Join(got, ",") != strings.Join(tt.want, ",") {
			t.Fatalf("Resolve(%q) = %v, want %v", tt.request, got, tt.want)
		}
	}
}

func TestResolveRejectsUnknown(t *testing.T) {
	restore := stubRegistry(fakeProvider("alpha", "Alpha", nil))
	defer restore()

	if _, err := Resolve("nope"); err == nil {
		t.Fatal("expected error for unknown provider")
	}
	if _, err := Resolve(","); err == nil {
		t.Fatal("expected error for empty provider list")
	}
}

func TestFanOutRunsConcurrentlyAndPreservesOrder(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	blocking := func(name string) AskFunc {
		return func(_ context.Context, prompt string) (string, error) {
			started <- name
			<-release
			return name + ":" + prompt, nil
		}
	}
	restore := stubRegistry(
		fakeProvider("alpha", "Alpha", blocking("alpha")),
		fakeProvider("beta", "Beta", blocking("beta")),
	)
	defer restore()

	done := make(chan []Result, 1)
	go func() { done <- FanOut(context.Background(), "q", []string{"alpha", "beta"}) }()

	// If FanOut were sequential, the first provider would block here waiting for
	// the second to start, and this loop would time out.
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("providers did not run concurrently")
		}
	}
	close(release)

	results := <-done
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].Provider != "alpha" || results[1].Provider != "beta" {
		t.Fatalf("order not preserved: %v", results)
	}
	if results[0].Text != "alpha:q" || results[1].Text != "beta:q" {
		t.Fatalf("unexpected texts: %q, %q", results[0].Text, results[1].Text)
	}
}

func TestFanOutBestEffort(t *testing.T) {
	restore := stubRegistry(
		fakeProvider("alpha", "Alpha", func(context.Context, string) (string, error) {
			return "", errors.New("boom")
		}),
		fakeProvider("beta", "Beta", func(context.Context, string) (string, error) {
			return "ok", nil
		}),
	)
	defer restore()

	results := FanOut(context.Background(), "q", []string{"alpha", "beta"})
	if results[0].Err == nil {
		t.Fatal("expected alpha to fail")
	}
	if results[1].Err != nil || results[1].Text != "ok" {
		t.Fatalf("beta should succeed: %+v", results[1])
	}
}

func TestFormatSingleSuccessHasNoHeader(t *testing.T) {
	got, err := Format([]Result{{Provider: "alpha", Title: "Alpha", Text: "  hello  "}})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if got != "hello" {
		t.Fatalf("got %q, want %q", got, "hello")
	}
}

func TestFormatSingleFailureIsEmpty(t *testing.T) {
	got, err := Format([]Result{{Provider: "alpha", Title: "Alpha", Err: errors.New("boom")}})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestFormatDropsFailedProvider(t *testing.T) {
	got, err := Format([]Result{
		{Provider: "alpha", Title: "Alpha", Text: "answer a"},
		{Provider: "beta", Title: "Beta", Err: errors.New("no firefox")},
	})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(got, "answer a") {
		t.Fatalf("missing alpha answer: %q", got)
	}
	if strings.Contains(got, "Beta") || strings.Contains(got, "_unavailable") {
		t.Fatalf("failed provider leaked into output: %q", got)
	}
}

func TestFormatMultipleSurvivorsLabeled(t *testing.T) {
	got, err := Format([]Result{
		{Provider: "alpha", Title: "Alpha", Text: "a"},
		{Provider: "beta", Title: "Beta", Text: "b"},
		{Provider: "gamma", Title: "Gamma", Err: errors.New("boom")},
	})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if !strings.Contains(got, "## Alpha") || !strings.Contains(got, "## Beta") {
		t.Fatalf("expected labeled sections: %q", got)
	}
	if strings.Contains(got, "Gamma") {
		t.Fatalf("failed provider leaked: %q", got)
	}
}

func TestFormatAllFailedIsEmpty(t *testing.T) {
	got, err := Format([]Result{
		{Provider: "alpha", Title: "Alpha", Err: errors.New("boom")},
		{Provider: "beta", Title: "Beta", Err: errors.New("bang")},
	})
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestFormatEmpty(t *testing.T) {
	if _, err := Format(nil); err == nil {
		t.Fatal("expected error for no results")
	}
}

// Package dispatch runs provider asks concurrently and formats their results.
// It is the shared fan-out layer used by the ask and review tools.
package dispatch

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/hangarbay/parley.mcp/internal/chatgpt"
	"github.com/hangarbay/parley.mcp/internal/gemini"
)

// AskFunc asks one provider a question and returns its answer as plain text.
type AskFunc func(context.Context, string) (string, error)

type provider struct {
	name  string
	title string
	ask   AskFunc
}

// registry is the set of known providers, ordered by canonical output order.
// It is a package variable so tests can substitute fakes.
var registry = []provider{
	{name: "gemini", title: "Gemini", ask: gemini.Ask},
	{name: "chatgpt", title: "ChatGPT", ask: chatgpt.Ask},
}

// Result is the outcome of asking one provider.
type Result struct {
	Provider string
	Title    string
	Text     string
	Err      error
}

// Names returns the known provider names in canonical order.
func Names() []string {
	names := make([]string, len(registry))
	for i, p := range registry {
		names[i] = p.name
	}
	return names
}

func lookup(name string) (provider, bool) {
	for _, p := range registry {
		if p.name == name {
			return p, true
		}
	}
	return provider{}, false
}

// Resolve expands a provider request into provider names. An empty request,
// "both", or "all" selects every known provider. Otherwise request is a
// comma-separated list of names; request order is preserved and duplicates are
// dropped.
func Resolve(request string) ([]string, error) {
	request = strings.TrimSpace(request)
	if request == "" || strings.EqualFold(request, "both") || strings.EqualFold(request, "all") {
		return Names(), nil
	}
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(request, ",") {
		name := strings.ToLower(strings.TrimSpace(part))
		if name == "" {
			continue
		}
		if _, ok := lookup(name); !ok {
			return nil, fmt.Errorf("unknown provider %q (want %s, or both)", part, strings.Join(Names(), ", "))
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	if len(out) == 0 {
		return nil, errors.New("no providers requested")
	}
	return out, nil
}

// FanOut asks every named provider concurrently and returns the results in the
// same order as names. A failure in one provider does not affect the others;
// only cancellation of ctx stops the whole fan-out.
func FanOut(ctx context.Context, prompt string, names []string) []Result {
	results := make([]Result, len(names))
	var wg sync.WaitGroup
	wg.Add(len(names))
	for i, name := range names {
		go func() {
			defer wg.Done()
			p, ok := lookup(name)
			if !ok {
				results[i] = Result{Provider: name, Title: name, Err: fmt.Errorf("unknown provider %q", name)}
				return
			}
			text, err := p.ask(ctx, prompt)
			results[i] = Result{Provider: p.name, Title: p.title, Text: text, Err: err}
		}()
	}
	wg.Wait()
	return results
}

// Format renders the successful results and drops any failed provider. Failures
// are logged to stderr so they stay out of the caller's answer. A single
// surviving provider is returned without a header; multiple survivors are
// labeled. When every provider fails, Format returns an empty string and no
// error, so a total outage reads as an empty answer rather than a failure.
func Format(results []Result) (string, error) {
	if len(results) == 0 {
		return "", errors.New("no providers requested")
	}

	var ok []Result
	for _, r := range results {
		if r.Err != nil {
			log.Printf("parley: provider %s failed: %v", r.Provider, r.Err)
			continue
		}
		ok = append(ok, r)
	}
	if len(ok) == 0 {
		return "", nil
	}
	if len(ok) == 1 {
		return strings.TrimSpace(ok[0].Text), nil
	}

	var b strings.Builder
	for i, r := range ok {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("## ")
		b.WriteString(r.Title)
		b.WriteString("\n\n")
		b.WriteString(strings.TrimSpace(r.Text))
	}
	return b.String(), nil
}

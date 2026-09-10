// Package extract flattens the HTML fragments returned by the provider web
// UIs into readable plain text.
package extract

import (
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

var (
	reMultiWS = regexp.MustCompile(`[ \t]+`)
	reMultiNL = regexp.MustCompile(`\n{3,}`)
)

var blockElements = map[string]bool{
	"p": true, "div": true, "br": true, "hr": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"li": true, "ul": true, "ol": true,
	"tr": true, "table": true,
	"pre": true, "blockquote": true,
	"section": true, "article": true, "header": true, "footer": true,
}

// Options tweaks the flattening for a provider's response shape.
type Options struct {
	// SkipTemplate drops <template> subtrees. The ChatGPT partial-update
	// stream wraps every visible element in <template>, so it must keep them.
	SkipTemplate bool
}

// KeepTemplates is the default flattening behavior. The ChatGPT stream needs
// it, since its content lives inside <template> elements.
var KeepTemplates = Options{}

// SkipTemplates also drops <template> subtrees. Gemini's AI Mode layout uses
// empty scaffolding templates that carry no answer text.
var SkipTemplates = Options{SkipTemplate: true}

// Nodes parses an HTML fragment and returns its text content with block
// elements separated by newlines and runs of whitespace collapsed.
func Nodes(fragment string, opts Options) string {
	doc, err := html.Parse(strings.NewReader(fragment))
	if err != nil {
		return strings.TrimSpace(fragment)
	}
	var buf strings.Builder
	walk(doc, &buf, opts)
	raw := buf.String()
	raw = reMultiWS.ReplaceAllString(raw, " ")
	raw = reMultiNL.ReplaceAllString(raw, "\n\n")
	return strings.TrimSpace(raw)
}

// Strip removes every match of the given patterns and trims the result.
func Strip(text string, patterns ...*regexp.Regexp) string {
	for _, re := range patterns {
		text = re.ReplaceAllString(text, "")
	}
	return strings.TrimSpace(text)
}

// DedupeLines removes consecutive duplicate lines. Streaming UIs resend the
// same paragraph in both the pending and committed frames.
func DedupeLines(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		if len(out) > 0 && strings.TrimSpace(ln) == strings.TrimSpace(out[len(out)-1]) {
			continue
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

func walk(n *html.Node, buf *strings.Builder, opts Options) {
	if n.Type == html.ElementNode {
		switch n.Data {
		case "script", "style", "noscript":
			return
		case "template":
			if opts.SkipTemplate {
				return
			}
		case "br":
			buf.WriteByte('\n')
			return
		}
	}

	if n.Type == html.TextNode {
		if text := strings.TrimSpace(n.Data); text != "" {
			buf.WriteString(text)
			buf.WriteByte(' ')
		}
	}

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, buf, opts)
	}

	if n.Type == html.ElementNode && blockElements[n.Data] {
		buf.WriteByte('\n')
	}
}

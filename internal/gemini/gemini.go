// Package gemini provides anonymous questions to Google Gemini (AI Mode)
// through the free web UI, with no API key. It is a provider consumed by the
// ask and review tools; it registers no MCP tools itself.
package gemini

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sync"
	"time"

	"github.com/hangarbay/parley.mcp/internal/extract"
	"github.com/hangarbay/parley.mcp/internal/httpx"
)

const (
	firefoxUA      = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:154.0) Gecko/20100101 Firefox/154.0"
	requestTimeout = 60 * time.Second

	// maxRequestURL is the longest request URI Google accepts before it answers
	// 400 Bad Request. The prompt is carried in the query string, so it and the
	// rest of the URL-encoded query must stay under this bound.
	maxRequestURL = 16384
)

// checkRequestURL rejects a request whose URI exceeds Google's limit. Without
// it an oversized prompt surfaces as a misleading "missing required tokens"
// error (Google replies 400 with an error page) or a bare "write: broken pipe"
// once the request line is large enough for the connection to be reset.
func checkRequestURL(reqURL string) error {
	if len(reqURL) >= maxRequestURL {
		return fmt.Errorf("prompt too large for the Gemini web UI: request URL is %d bytes and Google rejects URLs of %d bytes or more; shorten the prompt or use another provider", len(reqURL), maxRequestURL)
	}
	return nil
}

// searchStatusError explains a non-200 search response. Google answers
// anonymous automated traffic with 429 and a CAPTCHA page, which the caller
// would otherwise surface as the misleading "cookies may be expired".
func searchStatusError(status int, body string) error {
	if status == http.StatusOK {
		return nil
	}
	if status == http.StatusTooManyRequests {
		return errors.New("google returned 429 (anonymous traffic rate-limited or challenged with a CAPTCHA); retry later or use another provider")
	}
	return fmt.Errorf("search page returned status %d: %s", status, body[:min(len(body), 200)])
}

// Ask sends prompt to Gemini and returns its answer as plain text.
func Ask(ctx context.Context, prompt string) (string, error) {
	return sharedClient.ask(ctx, prompt)
}

type geminiClient struct {
	mu     sync.Mutex
	cookie string
	http   *http.Client
}

func newGeminiClient() *geminiClient {
	return &geminiClient{
		http: &http.Client{Timeout: requestTimeout},
	}
}

var sharedClient = newGeminiClient()

func (c *geminiClient) ask(ctx context.Context, prompt string) (string, error) {
	if err := c.ensureSession(ctx); err != nil {
		return "", fmt.Errorf("session: %w", err)
	}

	tokens, err := c.fetchTokens(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("fetch tokens: %w", err)
	}

	fragment, err := c.fetchAnswer(ctx, tokens, prompt)
	if err != nil {
		return "", fmt.Errorf("fetch answer: %w", err)
	}

	text := extractText(fragment)
	if text == "" {
		return "", errors.New("empty response from Gemini")
	}
	return text, nil
}

func (c *geminiClient) fetchTokens(ctx context.Context, prompt string) (map[string]string, error) {
	params := url.Values{}
	params.Set("q", prompt)
	params.Set("udm", "50")
	params.Set("hl", "en")
	params.Set("gl", "ca")
	params.Set("dpr", "2")
	params.Set("biw", "1073")
	params.Set("bih", "883")
	reqURL := "https://www.google.com/search?" + params.Encode()
	if err := checkRequestURL(reqURL); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", firefoxUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	req.Header.Set("Referer", "https://www.google.com/")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := httpx.ReadBody(resp)
	if err != nil {
		return nil, err
	}
	page := string(body)

	if err := searchStatusError(resp.StatusCode, page); err != nil {
		return nil, err
	}

	tokens := make(map[string]string)
	for _, m := range reDataTokens.FindAllStringSubmatch(page, -1) {
		if len(m) == 3 {
			tokens[m[1]] = m[2]
		}
	}
	for _, t := range []struct {
		name  string
		regex *regexp.Regexp
	}{
		{"vet", reVet},
		{"ved", reVed},
		{"ei", reEI},
		{"sca_esv", reScaEsv},
		{"stkp", reStkp},
	} {
		if m := t.regex.FindStringSubmatch(page); m != nil {
			tokens[t.name] = m[1]
		}
	}

	if _, ok := tokens["ei"]; !ok {
		return nil, fmt.Errorf("missing AI Mode tokens in search page (got %d); Google may have served a consent or reduced page", len(tokens))
	}
	return tokens, nil
}

var (
	reDataTokens = regexp.MustCompile(`data-(ei|garc|srtst|lro-token|lro-signature|xsrf-folwr-token)="([^"]*)"`)
	reVet        = regexp.MustCompile(`data-vet="([^"]*)"`)
	reVed        = regexp.MustCompile(`data-ved="([^"]*)"`)
	reEI         = regexp.MustCompile(`"kEI:'([^']+)'`)
	reScaEsv     = regexp.MustCompile(`sca_esv=([a-f0-9]+)`)
	reStkp       = regexp.MustCompile(`data-stkp="([^"]*)"`)
)

func (c *geminiClient) fetchAnswer(ctx context.Context, tokens map[string]string, prompt string) (string, error) {
	params := url.Values{}
	params.Set("srtst", tokens["srtst"])
	params.Set("garc", tokens["garc"])
	params.Set("mlro", tokens["lro-token"])
	params.Set("mlros", tokens["lro-signature"])
	params.Set("ei", tokens["ei"])
	params.Set("q", prompt)
	params.Set("yv", "3")
	params.Set("aep", "1")
	params.Set("cs", "1")
	params.Set("udm", "50")

	if v, ok := tokens["sca_esv"]; ok && v != "" {
		params.Set("sca_esv", v)
	}
	if v, ok := tokens["vet"]; ok && v != "" {
		params.Set("vet", v)
	}
	if v, ok := tokens["ved"]; ok && v != "" {
		params.Set("ved", v)
	}
	if v, ok := tokens["stkp"]; ok && v != "" {
		params.Set("stkp", v)
	}

	asyncVal := "_fmt:adl"
	if v, ok := tokens["xsrf-folwr-token"]; ok && v != "" {
		asyncVal += ",_xsrf:" + v
	}
	params.Set("async", asyncVal)

	reqURL := "https://www.google.com/async/folwr?" + params.Encode()
	if err := checkRequestURL(reqURL); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", firefoxUA)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	req.Header.Set("Referer", "https://www.google.com/")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := httpx.ReadBody(resp)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("folwr returned status %d: %s", resp.StatusCode, string(body[:min(len(body), 500)]))
	}

	return string(body), nil
}

var geminiBoilerplate = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^AI Mode reply for .+?\n`),
	regexp.MustCompile(`\n*Shared\s*\n\d+ files\s*\n`),
	regexp.MustCompile(`\n*Copy\s*\nShare public link\s*\n[\s\S]*$`),
	regexp.MustCompile(`\n*Good response\s*\nBad response\s*\n[\s\S]*$`),
	regexp.MustCompile(`\n*Show less\s*\nShow all\s*\n*$`),
	// The clipboard widget that trails every reply. Matched as a specific pair
	// so an answer that merely mentions "Copied to clipboard" survives.
	regexp.MustCompile(`\n*Copied to clipboard\s+Failed to copy to clipboard\. Try again later\.[\s\S]*$`),
}

func extractText(fragment string) string {
	if fragment == "" {
		return ""
	}
	return extract.Strip(extract.Nodes(fragment, extract.Static), geminiBoilerplate...)
}

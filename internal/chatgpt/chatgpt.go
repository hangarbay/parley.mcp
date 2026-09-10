// Package chatgpt implements the ask_chatgpt tool: anonymous questions to
// ChatGPT through the free chatgpt.com web UI, with no API key, account, or
// browser.
package chatgpt

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/hangarbay/parley.mcp/internal/extract"
	"github.com/hangarbay/parley.mcp/internal/httpx"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	firefoxUA      = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:155.0) Gecko/20100101 Firefox/155.0"
	requestTimeout = 120 * time.Second
	chatgptHome    = "https://chatgpt.com/"
)

// Register adds the ask_chatgpt tool and prompt to server.
func Register(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "ask_chatgpt",
		Description: "Ask ChatGPT a question using the free anonymous web UI. " +
			"No API key or browser required; session cookies are obtained automatically via the web flow.",
	}, askHandler)
	server.AddPrompt(&mcp.Prompt{
		Name:        "ask_chatgpt",
		Description: "Ask ChatGPT a question. No API key required.",
		Arguments: []*mcp.PromptArgument{
			{Name: "prompt", Description: "The question or prompt to send to ChatGPT", Required: true},
		},
	}, askPrompt)
}

type askArgs struct {
	Prompt string `json:"prompt" jsonschema:"the question or prompt to send to ChatGPT"`
}

func askHandler(ctx context.Context, _ *mcp.CallToolRequest, args askArgs) (*mcp.CallToolResult, any, error) {
	prompt := strings.TrimSpace(args.Prompt)
	if prompt == "" {
		return nil, nil, errors.New("prompt is required")
	}

	text, err := Ask(ctx, prompt)
	if err != nil {
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}, nil, nil
}

func askPrompt(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	prompt := ""
	if req.Params.Arguments != nil {
		prompt = req.Params.Arguments["prompt"]
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, errors.New("prompt is required")
	}

	text, err := Ask(ctx, prompt)
	if err != nil {
		return nil, err
	}

	return &mcp.GetPromptResult{
		Description: "ChatGPT: " + prompt,
		Messages: []*mcp.PromptMessage{
			{Content: &mcp.TextContent{Text: text}, Role: mcp.Role("user")},
		},
	}, nil
}

// Ask sends prompt to ChatGPT and returns its answer as plain text.
func Ask(ctx context.Context, prompt string) (string, error) {
	return newClient().ask(ctx, prompt)
}

// Probe walks the anonymous session one step at a time and prints what it
// learns, for diagnosing protocol drift.
func Probe(ctx context.Context) error {
	return newClient().probe(ctx)
}

type client struct {
	mu       sync.Mutex
	deviceID string
	http     *http.Client
	home     *homeState
}

type homeState struct {
	workerVersion string
	buildID       string
	affinity      string
	affinityExp   int64
	oaiSessionID  string
}

func newClient() *client {
	jar, _ := cookiejar.New(nil)
	return &client{
		http: &http.Client{
			Timeout: requestTimeout,
			Jar:     jar,
		},
	}
}

func (c *client) ask(ctx context.Context, prompt string) (string, error) {
	if err := c.ensureSession(ctx); err != nil {
		return "", fmt.Errorf("session: %w", err)
	}

	traceID := newUUID()
	prep := &preparedTurn{
		operationID:   newUUID(),
		oaiSessionID:  c.home.oaiSessionID,
		workerVersion: c.home.workerVersion,
		affinity:      c.home.affinity,
		deviceID:      c.deviceID,
	}

	// The sentinel cycle is independent of the conversation prepare (it never
	// validates the affinity/session), so run both chains concurrently.
	var wg sync.WaitGroup
	var prepErr, sentErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		var p *preparedTurn
		if p, prepErr = c.prepare(ctx, traceID); prepErr == nil {
			prep.conduitToken = p.conduitToken
		}
	}()
	go func() {
		defer wg.Done()
		sentErr = c.sentinelCycle(ctx, prep)
	}()
	wg.Wait()
	if prepErr != nil {
		return "", fmt.Errorf("prepare: %w", prepErr)
	}
	if sentErr != nil {
		return "", fmt.Errorf("sentinel: %w", sentErr)
	}

	fragment, err := c.fetchAnswer(ctx, traceID, prep, prompt)
	if err != nil {
		return "", fmt.Errorf("fetch answer: %w", err)
	}

	text := extractText(fragment)
	if text == "" {
		return "", errors.New("empty response from ChatGPT")
	}
	c.saveSessionCache()
	return text, nil
}

func (c *client) ensureSession(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.home != nil {
		return nil
	}

	// Reuse a previously bootstrapped session from disk when still valid.
	if c.loadSessionCache() {
		return nil
	}

	// Load the home page to obtain oai-did, worker version and affinity/session
	// metadata that later requests must echo back. The page sometimes serves a
	// truncated variant, so retry a few times.
	var lastErr error
	for i := 0; i < 5; i++ {
		lastErr = c.fetchHome(ctx)
		if lastErr == nil {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if lastErr != nil {
		return lastErr
	}
	c.deviceID = newUUID()
	return nil
}

func (c *client) fetchHome(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", chatgptHome, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", firefoxUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	req.Header.Set("Referer", "https://www.google.com/")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "cross-site")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := httpx.ReadBody(resp)
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("home returned status %d", resp.StatusCode)
	}

	home := &homeState{}
	if v := resp.Header.Get("x-worker-version"); v != "" {
		home.workerVersion = v
	}
	page := string(body)
	if m := reAffinity.FindStringSubmatch(page); len(m) == 2 {
		home.affinity = m[1]
	}
	if home.affinity == "" {
		if m := reAffinityJS.FindStringSubmatch(page); len(m) == 2 {
			home.affinity = m[1]
		}
	}
	if m := reBuildID.FindStringSubmatch(page); len(m) == 2 {
		home.buildID = m[1]
	}
	// The affinity JWT's "s" claim is the authoritative session id.
	if home.affinity != "" {
		claims := jwtClaims(home.affinity)
		home.oaiSessionID = claims.S
		home.affinityExp = claims.E
	}
	c.home = home
	if home.workerVersion == "" {
		return errors.New("could not determine worker version from home page")
	}
	if home.affinity == "" || home.oaiSessionID == "" {
		return errors.New("could not determine conversation document affinity from home page")
	}
	if home.buildID == "" {
		return errors.New("could not determine build id from home page")
	}
	return nil
}

type affinityClaims struct {
	S string `json:"s"`
	E int64  `json:"e"`
}

func jwtClaims(token string) affinityClaims {
	var out affinityClaims
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return out
	}
	// Two-part format: payload.signature. Three-part (header.payload.signature) fallback.
	payload := parts[0]
	if len(payload) > 200 && strings.HasPrefix(payload, "eyJ") {
		// already the payload
	} else {
		payload = parts[1]
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		p := payload + strings.Repeat("=", (4-len(payload)%4)%4)
		raw, err = base64.URLEncoding.DecodeString(p)
		if err != nil {
			return out
		}
	}
	_ = json.Unmarshal(raw, &out)
	return out
}

var (
	reAffinity   = regexp.MustCompile(`data-conversation-document-affinity="(eyJ[A-Za-z0-9_-]{100,}\.[A-Za-z0-9_-]{30,})"`)
	reAffinityJS = regexp.MustCompile(`(eyJ[A-Za-z0-9_-]{100,}\.[A-Za-z0-9_-]{30,})`)
	reBuildID    = regexp.MustCompile(`data-build="([^"]+)"`)
)

type preparedTurn struct {
	operationID         string
	conduitToken        string
	chatRequirementsTok string
	proofToken          string
	oaiSessionID        string
	workerVersion       string
	affinity            string
	deviceID            string
}

func (c *client) prepare(ctx context.Context, traceID string) (*preparedTurn, error) {
	form := url.Values{}
	form.Set("conversationRetryOwner", `{"mode":"anonymous","sessionEpoch":null}`)
	form.Set("conversationState", `{"messages":[],"parentMessageId":"client-created-root","userMessageCount":0}`)
	form.Set("clientContextualInfo", clientContextualInfoJSON())
	name, off := time.Now().Zone()
	if name == "" {
		name = "UTC"
		off = 0
	}
	form.Set("timezone", name)
	form.Set("timezoneOffsetMinutes", fmt.Sprintf("%d", off/60))

	reqURL := "https://chatgpt.com/unauth-mweb/conversation/prepare?lightweight_authenticated=0"
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", firefoxUA)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	req.Header.Set("Referer", "https://chatgpt.com/")
	req.Header.Set("X-Web-Mobile-Conversation-Renderer", "octane")
	req.Header.Set("x-oai-turn-trace-id", traceID)
	req.Header.Set("x-web-mobile-prepare-state", "none")
	if c.home != nil && c.home.workerVersion != "" {
		req.Header.Set("X-Web-Mobile-Document-Worker-Version", c.home.workerVersion)
		req.Header.Set("Cloudflare-Workers-Version-Overrides", `web-mobile-prod="`+c.home.workerVersion+`"`)
	}
	if c.home != nil && c.home.affinity != "" {
		req.Header.Set("X-Web-Mobile-Conversation-Document-Affinity", c.home.affinity)
	}
	if c.home != nil && c.home.oaiSessionID != "" {
		req.Header.Set("OAI-Session-Id", c.home.oaiSessionID)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")
	req.Header.Set("Origin", "https://chatgpt.com")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := httpx.ReadBody(resp)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("prepare returned status %d: %s", resp.StatusCode, truncate(string(body), 500))
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("prepare returned non-JSON: %s", truncate(string(body), 500))
	}

	prep := &preparedTurn{
		operationID:   newUUID(),
		conduitToken:  firstString(raw, "conduitToken", "conduit_token"),
		oaiSessionID:  c.home.oaiSessionID,
		workerVersion: c.home.workerVersion,
		affinity:      c.home.affinity,
		deviceID:      c.deviceID,
	}
	if prep.conduitToken == "" {
		return nil, fmt.Errorf("prepare response missing conduit_token: %s", truncate(string(body), 500))
	}
	return prep, nil
}

// sentinelCycle runs the sentinel chat-requirements prepare, proof-of-work and
// finalize sequence to obtain the chatRequirementsToken and proofToken
// required by the conversation/updates endpoint.
func (c *client) sentinelCycle(ctx context.Context, prep *preparedTurn) error {
	reqTok := requirementsToken(prep.deviceID, firefoxUA, c.home.buildID)

	prepareBody, err := json.Marshal(map[string]string{"p": reqTok})
	if err != nil {
		return err
	}

	var sp struct {
		Persona      string `json:"persona"`
		PrepareToken string `json:"prepare_token"`
		Proof        struct {
			Required   bool   `json:"required"`
			Seed       string `json:"seed"`
			Difficulty string `json:"difficulty"`
		} `json:"proofofwork"`
		ForceLogin bool `json:"force_login"`
	}
	status, body, err := c.sentinelPost(ctx, prep, "/sentinel/chat-requirements/prepare", prepareBody)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("prepare returned status %d: %s", status, truncate(string(body), 500))
	}
	if err := json.Unmarshal(body, &sp); err != nil {
		return fmt.Errorf("sentinel prepare non-JSON: %s", truncate(string(body), 500))
	}
	if sp.ForceLogin {
		return errors.New("chatgpt requires login (force_login)")
	}
	if sp.PrepareToken == "" {
		return fmt.Errorf("sentinel prepare missing prepare_token: %s", truncate(string(body), 500))
	}

	var proofToken string
	if sp.Proof.Required {
		proofToken = solveProofOfWork(sp.Proof.Seed, sp.Proof.Difficulty, prep.deviceID, firefoxUA, c.home.buildID)
		if proofToken == "" {
			return errors.New("failed to solve sentinel proof-of-work")
		}
	}

	finBody, err := json.Marshal(map[string]string{
		"prepare_token": sp.PrepareToken,
		"proofofwork":   proofToken,
	})
	if err != nil {
		return err
	}
	var fin struct {
		Token string `json:"token"`
	}
	status, body, err = c.sentinelPost(ctx, prep, "/sentinel/chat-requirements/finalize", finBody)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("finalize returned status %d: %s", status, truncate(string(body), 500))
	}
	if err := json.Unmarshal(body, &fin); err != nil {
		return fmt.Errorf("sentinel finalize non-JSON: %s", truncate(string(body), 500))
	}
	if fin.Token == "" {
		return fmt.Errorf("sentinel finalize missing token: %s", truncate(string(body), 500))
	}

	prep.chatRequirementsTok = fin.Token
	prep.proofToken = proofToken
	return nil
}

func (c *client) sentinelPost(ctx context.Context, prep *preparedTurn, path string, body []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", "https://chatgpt.com/unauth-mweb"+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("User-Agent", firefoxUA)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Referer", "https://chatgpt.com/")
	req.Header.Set("OAI-Session-Id", prep.oaiSessionID)
	req.Header.Set("x-web-mobile-document-renderer", "octane")
	req.Header.Set("X-Web-Mobile-Document-Worker-Version", prep.workerVersion)
	req.Header.Set("X-Web-Mobile-Conversation-Document-Affinity", prep.affinity)
	req.Header.Set("Cloudflare-Workers-Version-Overrides", `web-mobile-prod="`+prep.workerVersion+`"`)
	req.Header.Set("Origin", "https://chatgpt.com")

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	rb, err := httpx.ReadBody(resp)
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, rb, nil
}

func clientContextualInfoJSON() string {
	return `{"app_name":"chatgpt.com","has_web_push_capabilities":true,"is_dark_mode":true,"web_push_notification_permission":"default","page_height":882,"page_width":1073,"pixel_ratio":2,"screen_height":956,"screen_width":1470,"time_since_loaded":0}`
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if t, ok := v.(string); ok && t != "" {
				return t
			}
		}
	}
	return ""
}

func (c *client) fetchAnswer(ctx context.Context, traceID string, prep *preparedTurn, prompt string) (string, error) {
	conversationState := `{"messages":[],"parentMessageId":"client-created-root","safety":{"dismissedInterventionIds":[]},"userMessageCount":0}`

	form := url.Values{}
	form.Set("conversationState", conversationState)
	form.Set("messageMetadata", `{}`)
	form.Set("oai-session-id", prep.oaiSessionID)
	form.Set("imageAttachments", `[]`)
	form.Set("pendingImageUploads", `[]`)
	form.Set("prompt", prompt)
	form.Set("chatRequirementsToken", prep.chatRequirementsTok)
	if prep.proofToken != "" {
		form.Set("proofToken", prep.proofToken)
	}
	form.Set("telemetryToken", `0,1398`)
	form.Set("timingToken", `[1,null]`)
	form.Set("conversationRetryOwner", `{"mode":"anonymous","sessionEpoch":null}`)
	form.Set("assistantMessageId", "pending-"+newUUID())
	form.Set("userMessageId", newUUID())

	params := url.Values{}
	params.Set("lightweight_authenticated", "0")
	params.Set("operationId", prep.operationID)
	reqURL := "https://chatgpt.com/unauth-mweb/conversation/updates?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", firefoxUA)
	req.Header.Set("Accept", "text/vnd.openai.web-mobile-partial+html")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	req.Header.Set("Referer", "https://chatgpt.com/")
	req.Header.Set("x-conduit-token", prep.conduitToken)
	req.Header.Set("x-oai-turn-trace-id", traceID)
	req.Header.Set("x-web-mobile-prepare-state", "success")
	if prep.workerVersion != "" {
		req.Header.Set("X-Web-Mobile-Document-Worker-Version", prep.workerVersion)
		req.Header.Set("Cloudflare-Workers-Version-Overrides", `web-mobile-prod="`+prep.workerVersion+`"`)
	}
	if prep.affinity != "" {
		req.Header.Set("X-Web-Mobile-Conversation-Document-Affinity", prep.affinity)
	}
	req.Header.Set("X-Web-Mobile-Conversation-Renderer", "octane")
	req.Header.Set("X-Web-Mobile-Conversation-Stream-Protocol", "1")
	req.Header.Set("OAI-Session-Id", prep.oaiSessionID)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")
	req.Header.Set("Origin", "https://chatgpt.com")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")

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
		return "", fmt.Errorf("updates returned status %d: %s", resp.StatusCode, truncate(string(body), 500))
	}
	if d := os.Getenv("CHATGPT_DEBUG_DUMP"); d != "" {
		_ = os.WriteFile(d, body, 0644)
	}

	return string(body), nil
}

func (c *client) probe(ctx context.Context) error {
	if err := c.ensureSession(ctx); err != nil {
		return fmt.Errorf("session: %w", err)
	}
	fmt.Println("=== home state ===")
	fmt.Printf("workerVersion: %s\n", c.home.workerVersion)
	fmt.Printf("buildID: %s\n", c.home.buildID)
	fmt.Printf("oaiSessionID: %s\n", c.home.oaiSessionID)
	fmt.Printf("affinity: %s\n", truncate(c.home.affinity, 120))

	traceID := newUUID()
	prep, err := c.prepare(ctx, traceID)
	if err != nil {
		return err
	}
	fmt.Println("=== prepare ===")
	fmt.Printf("operationID: %s\n", prep.operationID)
	fmt.Printf("conduitToken: %s\n", truncate(prep.conduitToken, 120))
	fmt.Printf("oaiSessionID: %s\n", prep.oaiSessionID)

	if err := c.sentinelCycle(ctx, prep); err != nil {
		return err
	}
	fmt.Println("=== sentinel ===")
	fmt.Printf("chatRequirementsToken: %s\n", truncate(prep.chatRequirementsTok, 120))
	fmt.Printf("proofToken: %s\n", truncate(prep.proofToken, 120))

	fragment, err := c.fetchAnswer(ctx, traceID, prep, "Say hi in one short sentence.")
	if err != nil {
		return err
	}
	fmt.Println("=== updates response (first 3000 chars) ===")
	fmt.Println(truncate(fragment, 3000))
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("... [%d more bytes]", len(s)-n)
}

func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

var chatgptBoilerplate = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^ChatGPT\s*\n`),
	regexp.MustCompile(`\n*Copy\s*\n`),
	regexp.MustCompile(`\n*Regenerate\s*\n`),
	regexp.MustCompile(`\n*Share\s*\n`),
	regexp.MustCompile(`\n*Stop generating\s*\n?$`),
	regexp.MustCompile(`\n*ChatGPT can make mistakes. Check important info\.\s*$`),
}

func extractText(fragment string) string {
	if fragment == "" {
		return ""
	}
	return extract.DedupeLines(extract.Strip(extract.Nodes(fragment, extract.KeepTemplates), chatgptBoilerplate...))
}

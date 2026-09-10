# parley.mcp

An MCP server that lets an AI agent consult an external AI for a second
opinion, with no API keys. **parley** exposes two tools:

- **`ask_gemini`** — Google Gemini (AI Mode) through the free `google.com`
  web UI. Uses headless Firefox to bootstrap session cookies.
- **`ask_chatgpt`** — ChatGPT through the free anonymous `chatgpt.com` web
  UI. Plain HTTP, no browser required.

Both providers are self-contained packages under `internal/`; the MCP server
in `cmd/parley-mcp` only wires them together.

## Tools

- **`ask_gemini`** — Ask Gemini a question.
  - `prompt` (string, required): the question or prompt
- **`ask_chatgpt`** — Ask ChatGPT a question.
  - `prompt` (string, required): the question or prompt

## Usage

### MCP client config

```json
{
  "mcpServers": {
    "parley": {
      "command": "parley-mcp",
      "args": ["serve"]
    }
  }
}
```

### Docker

```bash
docker build -t parley.mcp .
docker run --rm -i parley.mcp
```

The Docker image includes `firefox-esr`, so the Gemini session bootstrap works
out of the box.

### Local

```bash
go run ./cmd/parley-mcp serve
```

CLI one-shots:

```bash
go run ./cmd/parley-mcp ask -provider gemini  -prompt "what is the capital of France"
go run ./cmd/parley-mcp ask -provider chatgpt -prompt "what is the capital of France"
go run ./cmd/parley-mcp probe   # diagnose the anonymous ChatGPT session
```

## Providers

### gemini

1. On first use, launches headless Firefox to obtain Google session cookies.
2. Fetches the AI Mode search page (`google.com/search?udm=50`) with those
   cookies.
3. Extracts tokens from the page (`srtst`, `garc`, `lro-token`, ...).
4. Calls Google's internal `async/folwr` endpoint with those tokens.

The session is bootstrapped once and reused for the lifetime of the process.
Requires Firefox; set `FIREFOX_PATH` to point at a specific binary.

### chatgpt

1. **Home page** (`GET /`) — grabs the octane worker version, the signed
   conversation document affinity token (whose `s` claim is the session id),
   and the `data-build` id. The server's `Set-Cookie` headers provide
   `oai-did` / `g_state` / rotating `oai-sc`; no browser needed.
2. **conversation/prepare** — returns the conduit token for this turn.
3. **sentinel/chat-requirements/prepare → solve PoW → finalize** — produces a
   requirements token, solves the server's FNV-1a proof-of-work challenge, and
   exchanges it for the chat requirements token.
4. **conversation/updates** — streams the assistant's answer as
   `text/vnd.openai.web-mobile-partial+html`, which is flattened to text.

The bootstrapped session is cached under the user cache dir
(`parley-mcp/chatgpt-session.json`) so the home page fetch only happens once
per affinity window. Set `CHATGPT_DEBUG_DUMP` to a path to dump the raw
`conversation/updates` response.

## Caveats

Both integrations drive undocumented, free web UIs, so they are inherently
brittle. The sentinel SDK version, octane renderer, token names, and affinity
format all rotate, and rate limiting or abuse detection may kick in under heavy
automated use. Cloudflare Turnstile is intentionally never used.

## Build

```bash
make build    # local binary at ./parley-mcp
make test     # go vet ./... && go test ./...
make docker   # Docker image tagged parley.mcp:local
```

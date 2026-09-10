# parley.mcp

An MCP server that lets an AI agent consult external AIs for a second opinion,
with no API keys. **parley** exposes five tools:

- **`ask`** — ask one or both providers a question. Selecting both fans out
  concurrently and returns labeled answers.
- **`decide`** — choose exactly one option from an explicitly enumerated list
  against explicit criteria, and commit to a recommendation.
- **`diagnose`** — given a failure with observations and attempted fixes, rank
  competing causes and propose tests that distinguish them.
- **`plan`** — turn a project or task into an ordered, phased breakdown.
- **`review`** — send a git diff, plan, or code change to one or both providers
  with project context and rules, and get back structured review verdicts.

The two providers are:

- **gemini** — Google Gemini (AI Mode) through the free `google.com` web UI.
  Uses headless Firefox to bootstrap session cookies.
- **chatgpt** — ChatGPT through the free anonymous `chatgpt.com` web UI. Plain
  HTTP, no browser required.

Providers are self-contained packages under `internal/`; `internal/dispatch`
runs them concurrently, and the tool packages (`internal/ask`, `internal/decide`,
`internal/diagnose`, `internal/plan`, `internal/review`) build prompts and format
results. The server in `cmd/parley-mcp` only wires everything together.

## Tools

- **`ask`** — Ask one or more providers a question.
  - `prompt` (string, required): the question or prompt
  - `provider` (string, optional): `gemini`, `chatgpt`, or `both` (default `both`)
- **`decide`** — Choose exactly one option from an explicitly enumerated list
  against explicit criteria, and commit to it. Use when the alternatives are
  already known.
  - `question` (string, required): the decision to make
  - `options` (string, required): the alternatives to choose between, one per line
  - `criteria` (string, optional): criteria the choice must be judged against
  - `constraints` (string, optional): hard constraints the choice must satisfy
  - `context` (string, optional): project context to ground the decision
  - `provider` (string, optional): `gemini`, `chatgpt`, or `both` (default `both`)
- **`diagnose`** — Given a failure with observations and attempted fixes, rank
  competing causes and propose tests that distinguish them.
  - `problem` (string, required): the failure or unexpected behavior observed
  - `observations` (string, optional): logs, errors, or measurements gathered so far
  - `attempts` (string, optional): fixes already tried and what happened
  - `environment` (string, optional): relevant environment or version details
  - `context` (string, optional): project context to ground the diagnosis
  - `provider` (string, optional): `gemini`, `chatgpt`, or `both` (default `both`)
- **`plan`** — Plan a project, task, or feature and get back a structured
  breakdown (goal, numbered phases with steps and deliverables, risks, open
  questions).
  - `task` (string, required): the project or task to plan
  - `context` (string, optional): project context to ground the plan
  - `provider` (string, optional): `gemini`, `chatgpt`, or `both` (default `both`)
- **`review`** — Review a git diff, plan, or code change and return a verdict
  (`APPROVE` / `REQUEST_CHANGES` / `NEEDS_DISCUSSION`) with specific findings.
  - `diff` (string, required): the unified diff (`git diff`), or the plan text for a plan review
  - `context` (string, optional): project context to ground the review
  - `rules` (string, optional): rules or conventions the change must follow
  - `provider` (string, optional): `gemini`, `chatgpt`, or `both` (default `both`)

When both providers are selected they run concurrently, so the call costs about
as long as the slower one. A failure in one provider does not discard the other:
failed providers are annotated inline and the call only errors if every provider
fails. Asking a single provider returns its answer with no section header.

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
go run ./cmd/parley-mcp ask -prompt "what is the capital of France"         # both providers
go run ./cmd/parley-mcp ask -provider gemini -prompt "..."                 # just one
go run ./cmd/parley-mcp decide -question "which datastore" -options "postgres, sqlite" -criteria "operational burden"
go run ./cmd/parley-mcp diagnose -problem "requests time out under load" -observations "p99 5s, CPU normal"
go run ./cmd/parley-mcp plan -task "migrate the CLI to cobra"              # ordered phases
go run ./cmd/parley-mcp review -diff changes.diff                          # review a diff (both providers)
go run ./cmd/parley-mcp review -provider chatgpt -diff changes.diff        # just one
go run ./cmd/parley-mcp probe   # diagnose the anonymous ChatGPT session
cat changes.diff | go run ./cmd/parley-mcp review                          # or pipe it on stdin
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

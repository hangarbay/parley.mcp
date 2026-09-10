# AGENTS.md

## What this is

`parley.mcp` is a Go MCP (Model Context Protocol) server that exposes five tools
so an AI agent can consult external AIs for a second opinion **without API
keys**: `ask` (fan a question out to one or both providers), `decide` (commit to
one of several known options against explicit criteria), `diagnose` (rank
competing causes of a failure and propose tests that distinguish them), `plan`
(turn a task into a phased breakdown), and `review` (send a diff, plan, or change
to one or both providers for a verdict). The providers are `gemini` (Google
Gemini "AI Mode" via the free web UI, needs headless Firefox) and `chatgpt`
(anonymous chatgpt.com web UI, plain HTTP).

Both integrations drive **undocumented, free web UIs**. They are inherently
brittle: token names, sentinel SDK versions, and affinity formats rotate
without notice, and rate limiting / abuse detection may kick in under heavy
automated use. Cloudflare Turnstile is intentionally never used. When
something breaks, suspect upstream drift before suspecting local bugs.

## Commands

```bash
make build            # go build -o parley-mcp ./cmd/parley-mcp
make test             # go vet ./... && go test ./...
make integ            # integration tests: go test -tags=integration -count=1 -timeout 5m -v ./cmd/parley-mcp/
make docker           # docker build -t parley.mcp:local .

go run ./cmd/parley-mcp serve                 # MCP stdio server (the production entrypoint)
go run ./cmd/parley-mcp ask -prompt "..."                    # both providers (default)
go run ./cmd/parley-mcp ask -provider gemini -prompt "..."   # just one
go run ./cmd/parley-mcp decide -question "..." -options "..."  # choose between known options
go run ./cmd/parley-mcp diagnose -problem "..."              # rank causes of a failure
go run ./cmd/parley-mcp plan -task "..."                     # phased breakdown (or use -context)
go run ./cmd/parley-mcp review -diff changes.diff            # review a diff (or pipe on stdin)
go run ./cmd/parley-mcp probe                 # step-by-step diagnostic of the anonymous ChatGPT session
```

- `make test` runs `go vet` too; unit tests cover `internal/dispatch` (resolve,
  fan-out concurrency, formatting) and every tool package (`internal/ask`,
  `internal/decide`, `internal/diagnose`, `internal/plan`, `internal/review`)
  for prompt composition and validation. The provider packages have focused
  unit tests for prompt-size and rejection handling; their end-to-end coverage
  is the integration suite behind a `//go:build integration` tag.
- `make integ` hits live providers and needs network access; the Gemini case
  also needs Firefox. It drives both providers through the real MCP surface
  (in-memory transport) and asserts a sentinel token comes back in the answer.
- `serve` takes no flags and communicates over stdio. The Docker image's
  entrypoint is `parley-mcp serve`.
- GitHub Actions (`docker.yml`) builds **multi-arch** images
  (linux/amd64 + linux/arm64) on push to `main` and publishes
  `hangarbay/parley.mcp:latest` + `:sha` to Docker Hub. The Docker image
  installs `firefox-esr` so the Gemini bootstrap works in containers.

## Layout and architecture

```
cmd/parley-mcp/     Thin CLI + MCP wiring. main.go only: newServer() registers
                    the tools; commands: ask | decide | diagnose | plan | serve
                    | probe | review.
internal/gemini/    gemini provider (self-contained; exports Ask)
internal/chatgpt/   chatgpt provider (self-contained; exports Ask, Probe)
internal/dispatch/  Shared fan-out layer: provider registry, Resolve, FanOut
                    (one goroutine per provider), Format. No MCP dependencies.
internal/ask/       ask tool: fans a prompt out via dispatch and formats results.
internal/decide/    decide tool: committed recommendation among enumerated options.
internal/diagnose/  diagnose tool: ranked causes of a failure plus discriminating
                    tests.
internal/plan/      plan tool: phased breakdown of a project or task.
internal/review/    review tool: composes a review prompt from a diff, context,
                    and rules, then fans it out via dispatch.
internal/httpx/     ReadBody: reads response body, auto-decompresses gzip.
internal/extract/   HTML fragment -> plain text flattening (shared by both).
```

Control/data flow, per provider:

**Gemini** (`internal/gemini`):
1. `ensureSession` launches headless Firefox once per process against the AI
   Mode page, then reads `NID`, `__Secure-STRP`, `SEARCH_SAMESITE`, `AEC`
   cookies out of the profile's `cookies.sqlite` (pure-Go `modernc.org/sqlite`).
   The cookie string is cached in a **package-level `sharedClient`** for the
   process lifetime — not persisted to disk.
2. `fetchTokens` GETs `google.com/search?udm=50` and regex-extracts tokens
   (`ei`, `garc`, `srtst`, `lro-token`, `lro-signature`, `sca_esv`, `vet`,
   `ved`, `stkp`, `xsrf-folwr-token`).
3. `fetchAnswer` GETs `google.com/async/folwr` echoing those tokens; the HTML
   fragment is flattened via `extract.Nodes(fragment, extract.Static)` and
   scrubbed with `geminiBoilerplate` regexes (clipboard widget, share buttons,
   "Show less/Show all", etc.).

**ChatGPT** (`internal/chatgpt`):
1. `fetchHome` GETs `chatgpt.com/` (with retries — the page is sometimes served
   truncated). Captures `x-worker-version` header, `data-build`, and the
   conversation-document-affinity JWT whose `s` claim is the session id. The
   server's `Set-Cookie` fills `oai-did` / `g_state` / rotating `oai-sc`.
2. `prepare` POSTs `conversation/prepare` for the per-turn `conduitToken`.
3. `sentinelCycle` runs prepare -> proof-of-work -> finalize on
   `/sentinel/chat-requirements/*` to obtain the `chatRequirementsToken`.
4. `fetchAnswer` POSTs `conversation/updates`, which streams
   `text/vnd.openai.web-mobile-partial+html`; flattened with
   `extract.Nodes(fragment, extract.Stream)` (drops live `-pending` frames),
   `chatgptBoilerplate` regexes, and `extract.DedupeLines`.

Important: `prepare` and the sentinel cycle run **concurrently** (they are
independent); the conduit token and requirements token are combined before
`fetchAnswer`.

**Dispatch** (`internal/dispatch`):
- Holds the provider `registry` (name + title + `Ask` func). `Resolve` turns a
  request string (`""`/`both`/`all`, or a comma list) into validated names;
  `FanOut` runs every named provider in its own goroutine and returns results in
  request order; `Format` renders single answers bare and multi answers under
  `## Gemini` / `## ChatGPT` headings, annotating unavailable providers inline.
- `registry` (dispatch) and `fanOut` (every tool package) are package vars so
  tests can substitute fakes without network access.

**Tools** (`internal/ask`, `internal/decide`, `internal/diagnose`,
`internal/plan`, `internal/review`):
- Each `Register` adds a tool and a matching prompt. Every tool follows the same
  shape: a `Run(ctx, Options{...})` that validates the required fields, resolves
  the provider, composes a prompt (role string, optional context/rules sections,
  a fixed numbered response template), fans out, and formats.
- `ask.Run(ctx, prompt, provider)` is the generic case and takes plain args.
  `decide` requires `question` + `options`; `diagnose` requires `problem`;
  `plan` requires `task`; `review` requires `diff`.
- All tools default `provider` to both. They never talk to the web directly:
  they build a prompt and hand it to dispatch.

## Gotchas and non-obvious patterns

- **Tools, not providers, own MCP registration.** Every tool package
  (`internal/ask`, `internal/decide`, `internal/diagnose`, `internal/plan`,
  `internal/review`) registers both a tool and a same-named prompt. Provider
  packages (`gemini`, `chatgpt`) are pure: they export `Ask` (and chatgpt also
  `Probe`) and register nothing. To add a provider, extend `internal/dispatch`'s
  `registry`; the tools pick it up automatically and `both` will include it.
  Adding a tool is likewise mechanical: a new `internal/<name>` package with the
  standard shape, then register it in `newServer()`.
- **Fan-out is best-effort and order-preserving.** `dispatch.FanOut` uses one
  goroutine per provider; a failure in one is captured in that `Result.Err` and
  never cancels the others. Results are indexed by request position, not map
  iteration, so output order is deterministic. `Format` errors only when every
  provider failed, and a single provider's answer is returned without a header.
- **Every provider request goes through `httpx.ReadBody`**, not `io.ReadAll` —
  the servers gzip responses and Go's transport does not auto-decompress.
- **The two providers use different User-Agent constants** (gemini:
  Firefox/154.0, chatgpt: Firefox/155.0). Do not "unify" them; they are
  intentionally versioned to match what each web UI expects.
- **`extract.Options` has two modes**: `Static` (SkipTemplate) for Gemini's
  AI Mode scaffolding, `Stream` (SkipPending) for ChatGPT's live `-pending`
  template frames. Choosing the wrong mode leaks empty scaffolding or stream
  prefixes into answers.
- **Boilerplate scrubbing is per-provider**: each package has its own
  `extractText` and its own `*Boilerplate` regex list. New UI chrome (buttons,
  share widgets, disclaimers) that leaks into answers gets fixed there. Note
  the recent commit "Fix leaked stream prefixes and page chrome in answers" —
  this is an ongoing maintenance area. `make integ` + the sentinel echo test
  is how you verify extraction is clean.
- **Prompt size limits differ per provider.** Gemini carries the prompt in the
  GET query string, so Google answers 400 Bad Request once the request URI
  reaches 16384 bytes; `checkRequestURL` rejects such prompts with a clear
  error, meaning a review diff beyond ~12 KB cannot go through Gemini at all.
  ChatGPT carries the prompt in the POST body but still caps its size: an
  oversized prompt arrives as an HTTP 200 stream whose
  `conversation-partial-control` frame is `failed`. `turnFailure` surfaces the
  assistant message (`Invalid prompt`, `Chat is temporarily unavailable...`)
  instead of the old generic `empty response from ChatGPT`.
- **Sentinel PoW is a faithful Go port of the JS FNV-1a 32-bit hash** with the
  avalanche finalizer, in `internal/chatgpt/sentinel.go`. `fnv1aHash` and
  `imul32` must stay bit-exact with the browser JS. Token formats:
  requirements `gAAAAAC` + base64(json(fingerprint)) + `~S`, proof `gAAAAAB` +
  base64 + `~S`. The fingerprint is a fixed 25-element array with randomized
  navigator/document probes; nonce=1 + `Math.random()` for requirements,
  nonce=attempt + elapsed ms for proof. If PoW stops verifying, the hash or
  fingerprint shape changed upstream.
- **ChatGPT session persistence**: after a successful ask, the session
  (cookies + octane metadata) is saved to
  `$XDG_CACHE_HOME/parley-mcp/chatgpt-session.json` (mode 0600) and reused
  until the affinity JWT expires. A fresh `client` is created per `Ask` call,
  but disk cache makes the home-page fetch a once-per-affinity-window cost.
  Stale/corrupt caches are silently ignored (re-bootstrap). If the cache file
  is missing, nothing breaks. Gemini has **no** disk persistence by design.
- **Debug env vars**: `CHATGPT_DEBUG_DUMP=/path` dumps the raw
  `conversation/updates` response body; `FIREFOX_PATH` overrides Firefox
  discovery (fixed path list incl. `/usr/bin/firefox-esr`, the Docker image's
  binary). `probe` walks the ChatGPT flow step by step and prints what it
  learns — the first tool to reach for when ChatGPT breaks.
- **`ask` CLI fallback**: if `-prompt` is empty, remaining positional args are
  joined as the prompt.
- **Docker build uses `CGO_ENABLED=0`**; this works because sqlite access is
  via `modernc.org/sqlite` (pure Go), not `mattn/go-sqlite3`. Don't add CGO
  dependencies.
- Go 1.27.1, module `github.com/hangarbay/parley.mcp`, MCP via
  `github.com/modelcontextprotocol/go-sdk v1.7.0`. The SDK's API style is
  `mcp.AddTool(server, &mcp.Tool{...}, handler)` with generic-typed handler
  args (see `askArgs` with `jsonschema:` tags).
- Recent commit history shows the project ships fixes for streaming/parsing
  drift and container/runtime issues (leaked stream prefixes, arm64 image
  publishing, server entrypoint in Docker). Keep those concerns in mind when
  touching extraction or the Dockerfile.

// Command parley-mcp exposes external AIs as MCP tools so an agent can consult
// them for a second opinion. It talks to Google Gemini (AI Mode) and to the
// anonymous ChatGPT web UI, neither of which needs an API key.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/hangarbay/parley.mcp/internal/chatgpt"
	"github.com/hangarbay/parley.mcp/internal/gemini"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	os.Exit(run())
}

func run() int {
	args := os.Args[1:]
	cmd := "ask"
	if len(args) > 0 {
		cmd = args[0]
		args = args[1:]
	}
	switch cmd {
	case "ask":
		return askCmd(args)
	case "serve":
		return serveCmd(args)
	case "probe":
		return probeCmd(args)
	case "-h", "--help", "help":
		usage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage()
		return 2
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `parley-mcp - ask external AIs (Gemini, ChatGPT) via their free web UIs

Usage:
  parley-mcp ask -provider gemini|chatgpt [-prompt STRING]   Ask one provider (CLI one-shot)
  parley-mcp serve                                           Run the MCP stdio server (tools: ask_gemini, ask_chatgpt)
  parley-mcp probe                                           Diagnose the anonymous ChatGPT session

Providers:
  gemini    Google Gemini (AI Mode). Bootstraps a session via headless Firefox; set FIREFOX_PATH to override.
  chatgpt   ChatGPT anonymous web UI. No browser required; sessions are cached under the user cache dir.
`)
}

func providers() map[string]func(context.Context, string) (string, error) {
	return map[string]func(context.Context, string) (string, error){
		"gemini":  gemini.Ask,
		"chatgpt": chatgpt.Ask,
	}
}

func askCmd(args []string) int {
	fs := flag.NewFlagSet("ask", flag.ContinueOnError)
	provider := fs.String("provider", "gemini", "which provider to ask: gemini or chatgpt")
	prompt := fs.String("prompt", "", "the question or prompt to send")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *prompt == "" {
		if len(fs.Args()) > 0 {
			*prompt = strings.Join(fs.Args(), " ")
		}
	}
	if *prompt == "" {
		fmt.Fprintln(os.Stderr, "missing -prompt")
		return 2
	}

	ask, ok := providers()[strings.ToLower(*provider)]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown provider %q (want gemini or chatgpt)\n", *provider)
		return 2
	}

	text, err := ask(context.Background(), *prompt)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	fmt.Println(text)
	return 0
}

func serveCmd(args []string) int {
	if len(args) > 0 {
		fmt.Fprintf(os.Stderr, "unknown argument %q (serve takes no flags)\n", args[0])
		return 2
	}
	if err := newServer().Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

// newServer builds the MCP server with every provider registered.
func newServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "parley",
		Title:   "Parley MCP",
		Version: "0.1.0",
	}, nil)
	gemini.Register(server)
	chatgpt.Register(server)
	return server
}

func probeCmd(args []string) int {
	if len(args) > 0 {
		fmt.Fprintf(os.Stderr, "unknown argument %q (probe takes no flags)\n", args[0])
		return 2
	}
	if err := chatgpt.Probe(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

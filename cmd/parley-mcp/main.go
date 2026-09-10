// Command parley-mcp exposes external AIs as MCP tools so an agent can consult
// them for a second opinion. It talks to Google Gemini (AI Mode) and to the
// anonymous ChatGPT web UI, neither of which needs an API key.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/hangarbay/parley.mcp/internal/ask"
	"github.com/hangarbay/parley.mcp/internal/chatgpt"
	"github.com/hangarbay/parley.mcp/internal/plan"
	"github.com/hangarbay/parley.mcp/internal/review"
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
	case "plan":
		return planCmd(args)
	case "review":
		return reviewCmd(args)
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
  parley-mcp ask [-provider gemini|chatgpt|both] [-prompt STRING]  Ask one or both providers (CLI one-shot)
  parley-mcp plan [-provider gemini|chatgpt|both] [-task STRING]   Plan a project or task breakdown
  parley-mcp review [-provider gemini|chatgpt|both] [-diff FILE] Review a diff, plan, or change
  parley-mcp serve                                           Run the MCP stdio server (tools: ask, plan, review)
  parley-mcp probe                                           Diagnose the anonymous ChatGPT session

Providers:
  gemini    Google Gemini (AI Mode). Bootstraps a session via headless Firefox; set FIREFOX_PATH to override.
  chatgpt   ChatGPT anonymous web UI. No browser required; sessions are cached under the user cache dir.
  both      Query both concurrently and label the answers (the default).
`)
}

func askCmd(args []string) int {
	fs := flag.NewFlagSet("ask", flag.ContinueOnError)
	provider := fs.String("provider", "both", "which providers to ask: gemini, chatgpt, or both")
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

	text, err := ask.Run(context.Background(), *prompt, *provider)
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

// newServer builds the MCP server with every tool registered.
func newServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "parley",
		Title:   "Parley MCP",
		Version: "0.1.0",
	}, nil)
	ask.Register(server)
	plan.Register(server)
	review.Register(server)
	return server
}

func planCmd(args []string) int {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	provider := fs.String("provider", "both", "which providers plan the task: gemini, chatgpt, or both")
	contextStr := fs.String("context", "", "project context to ground the plan")
	task := fs.String("task", "", "the project or task to plan")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *task == "" {
		if len(fs.Args()) > 0 {
			*task = strings.Join(fs.Args(), " ")
		}
	}
	if *task == "" {
		fmt.Fprintln(os.Stderr, "missing -task")
		return 2
	}

	text, err := plan.Run(context.Background(), plan.Options{
		Task:     *task,
		Context:  *contextStr,
		Provider: *provider,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	fmt.Println(text)
	return 0
}

func reviewCmd(args []string) int {
	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	provider := fs.String("provider", "both", "which providers review the change: gemini, chatgpt, or both")
	contextStr := fs.String("context", "", "project context to ground the review")
	rules := fs.String("rules", "", "rules or conventions the change must follow")
	diffFile := fs.String("diff", "", "path to a file holding the diff, or - for stdin")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	diff, err := readDiff(*diffFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}

	text, err := review.Run(context.Background(), review.Options{
		Diff:     diff,
		Context:  *contextStr,
		Rules:    *rules,
		Provider: *provider,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	fmt.Println(text)
	return 0
}

// readDiff reads the diff from a file, or from stdin when no path (or "-") is
// given.
func readDiff(path string) (string, error) {
	if path != "" && path != "-" {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", err
	}
	return string(data), nil
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

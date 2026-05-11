// Command claude-go is the Phase 1 CLI entry-point for the claude-code Go
// rewrite.  It accepts a single prompt via the -p flag, runs the query loop
// (which automatically registers the Bash tool), and prints the assistant's
// final reply to stdout.
//
// Usage:
//
//	export ANTHROPIC_API_KEY=sk-ant-...
//	go run . -p "echo hello"
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/learnclaudecode/claude-go/src/phase-01-trajectory/api"
	"github.com/learnclaudecode/claude-go/src/phase-01-trajectory/query"
)

func main() {
	prompt := flag.String("p", "", "Prompt to send to Claude (required)")
	model := flag.String("model", "claude-sonnet-4-6", "Model ID to use")
	flag.Parse()

	if *prompt == "" {
		fmt.Fprintln(os.Stderr, "Usage: claude-go -p <prompt>")
		os.Exit(1)
	}

	// Wire the real API implementation into the query package.
	query.SetCallModel(api.CallModel)

	// Honour Ctrl-C gracefully.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	params := query.QueryParams{
		Messages: []query.Message{
			{
				Role: query.RoleUser,
				Content: []query.ContentBlock{
					query.TextBlock{Type: "text", Text: *prompt},
				},
				UUID: "user-init",
			},
		},
		SystemParts: []query.SystemPart{
			{
				Type: "text",
				Text: "You are a helpful assistant. When the user asks you to run commands, use the Bash tool.",
			},
		},
		MaxTurns:    10,
		Model:       *model,
		QuerySource: "cli",
	}

	msgCh, termCh := query.Query(ctx, params)

	// Stream messages to stdout as they arrive.
	for msg := range msgCh {
		if msg.Role == query.RoleAssistant {
			for _, block := range msg.Content {
				switch b := block.(type) {
				case query.TextBlock:
					fmt.Print(b.Text)
				case query.ToolUseBlock:
					fmt.Printf("[tool_use] %s(%v)\n", b.Name, b.Input)
				}
			}
		} else if msg.Role == query.RoleUser {
			for _, block := range msg.Content {
				if tr, ok := block.(query.ToolResultBlock); ok {
					content, _ := tr.Content.(string)
					if tr.IsError {
						fmt.Printf("[tool_error] %s\n", content)
					} else {
						fmt.Printf("[tool_result] %s", content)
					}
				}
			}
		}
	}

	term := <-termCh
	fmt.Println()

	switch term.Reason {
	case query.TerminalCompleted:
		// Success — nothing extra to print.
	case query.TerminalAborted:
		fmt.Fprintln(os.Stderr, "[aborted]")
		os.Exit(1)
	case query.TerminalMaxTurns:
		fmt.Fprintln(os.Stderr, "[max turns reached]")
		os.Exit(1)
	case query.TerminalError:
		fmt.Fprintf(os.Stderr, "[error] %v\n", term.Error)
		os.Exit(1)
	}
}

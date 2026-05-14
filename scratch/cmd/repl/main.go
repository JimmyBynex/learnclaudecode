package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"scratch/api"
	"scratch/query"
	"strings"
	"syscall"

	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load()
	query.SetCallModel(api.CallModel)

	var messages []query.Message
	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		messages = append(messages, query.Message{
			Role:    query.RoleUser,
			Content: []query.ContentBlock{query.TextBlock{Type: "text", Text: input}},
		})

		queryCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		msgCh, termCh := query.Query(queryCtx, query.QueryParams{
			Messages: messages,
			SystemParts: []query.SystemPart{
				{Type: "text", Text: "You are a helpful assistant."},
			},
			MaxTurns: 10,
			Model:    "claude-sonnet-4-6",
		})

		for msg := range msgCh {
			for _, block := range msg.Content {
				switch b := block.(type) {
				case query.TextBlock:
					fmt.Print(b.Text)
				case query.ToolUseBlock:
					fmt.Printf("\n[tool] %s: %v\n", b.Name, b.Input)
				case query.ToolResultBlock:
					content, _ := b.Content.(string)
					if b.IsError {
						fmt.Printf("[error] %s\n", content)
					} else {
						fmt.Printf("[result] %s\n", content)
					}
				}
			}
		}

		term := <-termCh
		stop()
		fmt.Println()

		switch term.Reason {
		case query.TerminalCompleted:
			messages = term.Messages
		case query.TerminalAborted:
			fmt.Fprintln(os.Stderr, "[aborted]")
			messages = term.Messages
		case query.TerminalMaxTurns:
			fmt.Fprintln(os.Stderr, "[max turns]")
			messages = term.Messages
		case query.TerminalError:
			fmt.Fprintf(os.Stderr, "[error] %v\n", term.Error)
			messages = term.Messages
		}
	}
}

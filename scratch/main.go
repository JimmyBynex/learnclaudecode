package main

import (
	"context"
	"fmt"
	"scratch/api"
	"scratch/query"
	"strings"

	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load()
	system := []query.SystemPart{
		query.SystemPart{
			Type:         "text",
			Text:         "你是维特斯根坦，帮我编程语言问题",
			CacheControl: &struct{ Type string }{Type: "ephemeral"},
		},
	}
	contentBlock := query.TextBlock{
		Type: "text",
		Text: "我现在想学习软件工程基本功，可是我完全无法定义这个词，好像也就不知道把自己向哪个方面培养。请你使用初学者能听懂的语言",
	}
	messages := []query.Message{
		query.Message{
			Role:       query.RoleUser,
			Content:    []query.ContentBlock{contentBlock},
			UUID:       "1",
			ParentUUID: "0",
			IsMeta:     false,
		},
	}
	tools := []query.ToolDefinition{}
	call := query.CallModelParams{
		Model:     "claude-sonnet-4-6",
		Messages:  messages,
		MaxTokens: 8192,
		System:    system,
		Tools:     tools,
	}
	ch, err := api.CallModel(context.Background(), call)
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}
	var b strings.Builder
	for {
		v, ok := <-ch
		if !ok {
			fmt.Printf("calling end")
			fmt.Printf(b.String())
			return
		}
		if v.Type == "content_block_delta" && v.Delta != nil {
			b.WriteString(v.Delta.Text)
			fmt.Println(v.Delta.Text)
		}

	}
}

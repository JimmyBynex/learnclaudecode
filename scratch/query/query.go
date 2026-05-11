package query

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
)

type contentAccumulator struct {
	block []*accumBlock
}

type accumBlock struct {
	blockType string
	id        string
	name      string
	text      []byte
	thinking  []byte
	inputJSON []byte
}

func (ca *contentAccumulator) ensureIndex(index int) {
	for index >= len(ca.block) {
		ca.block = append(ca.block, nil)
	}
}
func (ca *contentAccumulator) startBlock(blockType string, index int, id, name string) {
	ca.ensureIndex(index)
	//id和name字段只有toolUse才有，其他thinking和text只有type
	ca.block[index] = &accumBlock{
		blockType: blockType,
		id:        id,
		name:      name}
}

func (ca *contentAccumulator) applyDelta(deltaType string, index int, text, thinking, partialJSON string) {
	ab := ca.block[index]
	switch deltaType {
	case "text_delta":
		ab.appendText(text)
	case "thinking_delta":
		ab.appendThinking(thinking)
	case "input_json_delta":
		ab.appendInput(partialJSON)
	}
}

func (ca *contentAccumulator) build() ([]ContentBlock, error) {
	result := make([]ContentBlock, 0, len(ca.block))
	for _, ab := range ca.block {
		if ab == nil {
			continue
		}
		switch ab.blockType {
		case "text":
			result = append(result, TextBlock{Type: "text", Text: string(ab.text)})
		case "thinking":
			result = append(result, ThinkingBlock{Type: "thinking", Thinking: string(ab.thinking)})
		case "tool_use":
			var input map[string]any
			if len(ab.inputJSON) > 0 {
				if err := json.Unmarshal(ab.inputJSON, &input); err != nil {
					return nil, fmt.Errorf("parse tool_use input JSON for %q: %w", ab.name, err)
				}
			}
			result = append(result, ToolUseBlock{
				Type:  "tool_use",
				ID:    ab.id,
				Name:  ab.name,
				Input: input,
			})
		}
	}
	return result, nil
}

// 为什么不直接使用+=，也就是直接使用string类型，原因是string反复内存分配
func (b *accumBlock) appendText(s string)     { b.text = append(b.text, s...) }
func (b *accumBlock) appendInput(s string)    { b.inputJSON = append(b.inputJSON, s...) }
func (b *accumBlock) appendThinking(s string) { b.thinking = append(b.thinking, s...) }

// CallModel is a function used by queryLoop to call Anthropic API
var CallModel func(context.Context, CallModelParams) (<-chan StreamEvent, error)

func SetCallModel(fn func(context.Context, CallModelParams) (<-chan StreamEvent, error)) {
	CallModel = fn
}

// newUUID returns a fresh UUID string using crypto/rand.
func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return hex.EncodeToString(b[:4]) + "-" + hex.EncodeToString(b[4:6]) + "-" + hex.EncodeToString(b[6:8]) + "-" + hex.EncodeToString(b[8:10]) + "-" + hex.EncodeToString(b[10:])
}

// 返回值分别是message，abort，error
func collectAssistantMessage(ctx context.Context, evCh <-chan StreamEvent) (Message, bool, error) {
	ca := &contentAccumulator{}
	for {
		select {
		case <-ctx.Done():
			content, _ := ca.build()
			return Message{
				Role:    RoleAssistant,
				Content: content,
				UUID:    newUUID(),
			}, true, nil
		case ev, ok := <-evCh:
			//正常结束
			if !ok {
				content, _ := ca.build()
				return Message{
					Role:    RoleAssistant,
					Content: content,
					UUID:    newUUID(),
				}, false, nil
			}
			//有事件
			switch ev.Type {
			case "error":
				return Message{}, false, fmt.Errorf("stream error event received from API")
			case "content_block_start":
				ca.startBlock(ev.BlockMeta.Type, ev.Index, ev.BlockMeta.ID, ev.BlockMeta.Name)
			case "content_block_delta":
				ca.applyDelta(ev.Delta.Type, ev.Index, ev.Delta.Text, ev.Delta.Thinking, ev.Delta.PartialJSON)
			//可以提前返回
			case "message_stop":
				content, err := ca.build()
				if err != nil {
					return Message{}, false, err
				}
				return Message{
					Role:    RoleAssistant,
					Content: content,
					UUID:    newUUID(),
				}, false, nil
			}

		}
	}
}

func collectToolUseBlocks(msg Message) []ToolUseBlock {
	result := []ToolUseBlock{}
	for _, b := range msg.Content {
		if tu, ok := b.(ToolUseBlock); ok {
			result = append(result, tu)
		}
	}
	return result
}

// executeTools runs all tool_use blocks and constructs the user Message that
// carries all tool results.  Returns false if the context was cancelled.
func executeTools(ctx context.Context, toolUseBlocks []ToolUseBlock, assistantMsg Message) (Message, bool) {
	resultBlocks := make([]ContentBlock, 0, len(toolUseBlocks))
	var output string
	var isError bool
	for i, tu := range toolUseBlocks {
		//处理关闭情况
		select {
		case <-ctx.Done():
			for _, remaining := range toolUseBlocks[i:] {
				resultBlocks = append(resultBlocks, ToolResultBlock{
					Type:      "tool_result",
					ToolUseID: remaining.ID,
					Content:   "error",
				})
			}
			return Message{
				Role:       RoleUser,
				Content:    resultBlocks,
				UUID:       newUUID(),
				ParentUUID: assistantMsg.UUID,
			}, false
		default:
		}
		//正常
		switch tu.Name {
		case "Bash":
			cmd, _ := tu.Input["command"].(string)
			output, isError = executeBash(cmd)
		default:
			output = fmt.Sprintf("unknown tool: %s", tu.Name)
			isError = true
		}
		resultBlocks = append(resultBlocks, ToolResultBlock{
			Type:      "tool_result",
			ToolUseID: tu.ID,
			Content:   output,
			IsError:   isError,
		})
	}
	return Message{
		Role:       RoleUser,
		Content:    resultBlocks,
		UUID:       newUUID(),
		ParentUUID: assistantMsg.UUID,
	}, true
}

func executeBash(command string) (string, bool) {
	//方便执行串联复杂命令，同时不需要担心权限
	cmd := exec.Command("bash", "-c", command)
	//和并错误和输出
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), true
	}
	return string(out), false
}

func queryLoop(ctx context.Context, p QueryParams, msgCh chan<- Message, termCh chan<- Terminal) {

	messages := make([]Message, len(p.Messages))
	copy(messages, p.Messages)

	tools := []ToolDefinition{}
	if p.ToolCtx != nil {
		tools = p.ToolCtx.Tools
	}

	// Always register the Bash tool.
	hasBash := false
	for _, t := range tools {
		if t.Name == "Bash" {
			hasBash = true
			break
		}
	}
	if !hasBash {
		tools = append(tools, ToolDefinition{
			Name:        "Bash",
			Description: "Run a bash command and return its output.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": "The bash command to run.",
					},
				},
				"required": []string{"command"},
			},
		})
	}

	for turn := 0; turn < p.MaxTurns; turn++ {
		//0.决定是否开启这轮
		select {
		case <-ctx.Done():
			termCh <- Terminal{
				Reason:   TerminalAborted,
				Messages: messages,
				Error:    nil,
			}
			return
		default:
		}

		//为什么有两种err，第一是请求的时候出现的问题，第二个是流式推送事件产生的
		//1.发送请求
		evCh, err := CallModel(ctx, CallModelParams{
			Model:     p.Model,
			Messages:  messages,
			MaxTokens: 8192,
			Tools:     tools,
			System:    p.SystemParts,
		})

		//请求过程中出现错误，不添加消息
		if err != nil {
			termCh <- Terminal{
				Reason:   TerminalError,
				Messages: messages,
				Error:    err,
			}
			return
		}

		//收集assistant消息
		assistantMsg, aborted, err := collectAssistantMessage(ctx, evCh)

		//收集流或转换消息的过程中，出现错误，不添加消息
		if err != nil {
			termCh <- Terminal{
				Reason:   TerminalError,
				Messages: messages,
				Error:    err,
			}
			return
		}
		//
		if aborted {
			termCh <- Terminal{
				Reason:   TerminalAborted,
				Messages: messages,
				Error:    nil,
			}
			return
		}
		messages = append(messages, assistantMsg)
		toolUse := collectToolUseBlocks(assistantMsg)
		if len(toolUse) == 0 {
			termCh <- Terminal{
				Reason:   TerminalCompleted,
				Messages: messages,
				Error:    nil,
			}
			msgCh <- assistantMsg
			return

		} else {
			msgCh <- assistantMsg
		}
		toolResult, cancel := executeTools(ctx, toolUse, assistantMsg)
		messages = append(messages, toolResult)
		if !cancel {
			termCh <- Terminal{
				Reason:   TerminalAborted,
				Messages: messages,
				Error:    nil,
			}
			msgCh <- toolResult
			return
		} else {
			msgCh <- toolResult
		}

	}
}

func RepairTrajectory(messages []Message) []Message {
	return nil
}

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
	id        string //toolUseID
	name      string //toolName
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

func (ca *contentAccumulator) buildAt(index int) (ContentBlock, error) {
	if index >= len(ca.block) || index < 0 || ca.block[index] == nil {
		return nil, nil
	}
	switch ca.block[index].blockType {
	case "text":
		return TextBlock{Type: "text", Text: string(ca.block[index].text)}, nil

	case "thinking":
		return ThinkingBlock{Type: "thinking", Thinking: string(ca.block[index].thinking)}, nil
	case "input_json":
		var input map[string]any
		if err := json.Unmarshal(ca.block[index].inputJSON, &input); err != nil {
			return nil, err
		}
		return ToolUseBlock{Type: "tool_use", ID: ca.block[index].id, Name: ca.block[index].name, Input: input}, nil
	}
	return nil, nil
}

func Query(ctx context.Context, q QueryParams) (<-chan Message, <-chan Terminal) {
	msgCh := make(chan Message, 64)
	termCh := make(chan Terminal, 1)
	go func() {
		queryLoop(ctx, q, msgCh, termCh)
	}()
	return msgCh, termCh
}

func queryLoop(ctx context.Context, p QueryParams, msgCh chan<- Message, termCh chan<- Terminal) {

	messages := make([]Message, len(p.Messages))
	copy(messages, p.Messages)
	//1.先是检查
	model := p.Model
	if model == "" {
		model = "claude-sonnet-4-6"
	}
	maxTurns := p.MaxTurns
	if maxTurns == 0 {
		maxTurns = 10
	}
	tools := []ToolDefinition{}
	if p.ToolCtx != nil {
		tools = p.ToolCtx.Tools
	}
	hasBash := false
	for _, tool := range tools {
		if tool.Name == "bash" {
			hasBash = true
		}
	}

	if !hasBash {
		tools = append(tools, ToolDefinition{
			Name:        "bash",
			Description: "Run a bash command and return its output.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": "the command to run",
					},
				},
				"required": []string{"command"},
			},
		})
	}

	//2.进入调用循环
	for turn := 0; turn < maxTurns; turn++ {
		select {
		//2.1 轮次开始前可以取消
		case <-ctx.Done():
			messages = RepairTrajectory(messages)
			termCh <- Terminal{
				Reason:   TerminalAborted,
				Messages: messages,
			}
			return
		default:
		}
		//2.2 进入调用
		evCh, err := CallModel(ctx, CallModelParams{
			Model:     model,
			Messages:  messages,
			MaxTokens: 8196,
			Tools:     tools,
			System:    p.SystemParts,
		})
		//2.3 调用api或者转换格式出错，sse传输前
		if err != nil {
			messages = RepairTrajectory(messages)
			termCh <- Terminal{
				Reason:   TerminalError,
				Messages: messages,
				Error:    fmt.Errorf("CallModel: %w", err),
			}
			return
		}

		ca := &contentAccumulator{}
		assistantUUID := newUUID()
		toolResultUUID := newUUID()
		parentUUID := ""
		if len(messages) > 0 {
			parentUUID = messages[len(messages)-1].UUID
		}
		var aborted bool
		var streamErr error
		var commited bool
		var assistantBlocks []ContentBlock
		var pendingResultBlocks []ContentBlock
		var toolUsed bool

		//2.4 进入sse传输
	eventLoop:
		for {
			//一开始我写的只是select{case <-ctx.done}
			//原来我的理解是即使ev,ok := <-evCh放在select外
			//但是问题是evCh有可能还在阻塞中，就是还没有消息的时候，如果此时ctx.done，监听ctx部分早就跳过了
			//导致这个空窗期什么都干不了
			select {
			//2.4.1 流式子接收消息过程中可以打断
			case <-ctx.Done():
				aborted = true
				break eventLoop
			//2.4.2 处理流式消息
			case ev, ok := <-evCh:
				//！ok有三种情况，一种是读到err传error事件后关闭，第二种是ctx取消关闭，第三种是ev关闭（网络中断或者是正常message结束）
				//第一种会先被error事件处理，第二种是一开始就被捕获，第三种如果是正常message结束，下面也捕获处理了，唯独网络中断或者没收到message_stop没处理
				//当作流错误处理
				if !ok {
					streamErr = fmt.Errorf("stream closed without message_stop")
					break eventLoop
				}

				//分别收集
				switch ev.Type {
				case "content_block_start":
					ca.startBlock(ev.BlockMeta.Type, ev.Index, ev.BlockMeta.ID, ev.BlockMeta.Name)

				case "content_block_delta":
					ca.applyDelta(ev.Delta.Type, ev.Index, ev.Delta.Text, ev.Delta.Thinking, ev.Delta.PartialJSON)

				case "content_block_stop":
					block, err := ca.buildAt(ev.Index)
					if err != nil {
						streamErr = fmt.Errorf("build block: %w", err)
						break eventLoop
					}
					assistantBlocks = append(assistantBlocks, block)

					//先是直接推送给显示
					partMsg := Message{
						Role:       RoleAssistant,
						Content:    []ContentBlock{block},
						UUID:       assistantUUID,
						ParentUUID: parentUUID,
					}
					select {
					case <-ctx.Done():
						aborted = true
						break eventLoop
					case msgCh <- partMsg:
					}

					//再是查看当前是否需要toolUse
					if tu, ok := block.(ToolUseBlock); ok {
						tr := executeOneTool(ctx, tu)
						toolUsed = true
						pendingResultBlocks = append(pendingResultBlocks, tr)

						//修复ctx无法上传的问题，当然还可以使得executeOneTool返回多一个bool的interrupt
						select {
						case <-ctx.Done():
							aborted = true
							break eventLoop
						default:
						}

						toolResultMsg := Message{
							Role:       RoleUser,
							Content:    []ContentBlock{tr},
							UUID:       toolResultUUID,
							ParentUUID: assistantUUID,
						}
						select {
						case <-ctx.Done():
							aborted = true
							break eventLoop
						case msgCh <- toolResultMsg:
						}
					}

				case "message_stop":

					assistantMsg := Message{
						Role:       RoleAssistant,
						Content:    assistantBlocks,
						UUID:       assistantUUID,
						ParentUUID: parentUUID,
					}
					messages = append(messages, assistantMsg)
					commited = true
					if toolUsed {
						toolUseMsg := Message{
							Role:       RoleUser,
							Content:    pendingResultBlocks,
							UUID:       toolResultUUID,
							ParentUUID: assistantUUID,
						}
						messages = append(messages, toolUseMsg)
					}

					//如果没有工具调用，直接结束了
					if !toolUsed {
						termCh <- Terminal{
							Reason:   TerminalCompleted,
							Messages: messages,
							Error:    nil,
						}
						return
					}
					//否则只是打断event收取流程，进入新一轮循环调用
					break eventLoop

				case "error":
					streamErr = fmt.Errorf("stream error event")
					break eventLoop
				}
			}

		}

		//进入处理修复阶段
		if !commited {
			messages = append(messages, Message{
				Role:       RoleAssistant,
				Content:    assistantBlocks,
				UUID:       assistantUUID,
				ParentUUID: parentUUID,
			})
			if toolUsed {
				messages = append(messages, Message{
					Role:       RoleUser,
					Content:    pendingResultBlocks,
					UUID:       toolResultUUID,
					ParentUUID: assistantUUID,
				})
			}
		}
		if streamErr != nil {
			repaired := RepairTrajectory(messages)
			termCh <- Terminal{
				Reason:   TerminalError,
				Messages: repaired,
				Error:    streamErr,
			}
			return
		}
		if aborted {
			repaired := RepairTrajectory(messages)
			termCh <- Terminal{
				Reason:   TerminalAborted,
				Messages: repaired,
				Error:    nil,
			}
			return
		}
	}

	//达到最大循环次数
	termCh <- Terminal{
		Reason:   TerminalMaxTurns,
		Messages: messages,
		Error:    nil,
	}
	return
}

func executeOneTool(ctx context.Context, tu ToolUseBlock) ToolResultBlock {
	select {
	case <-ctx.Done():
		return ToolResultBlock{
			Type:      "tool_use",
			ToolUseID: tu.ID,
			Content:   "Interrupted by user",
			IsError:   true,
		}
	default:
	}
	var isErr bool
	var output string
	switch tu.Name {
	case "bash":
		output, isErr = executeBash(tu.Input["command"].(string))
	default:
		isErr = false
		output = fmt.Sprintf("Unknown tool '%s'", tu.Name)
	}
	return ToolResultBlock{
		Type:      "tool_use",
		ToolUseID: tu.ID,
		Content:   output,
		IsError:   isErr,
	}
}

func executeBash(command string) (string, bool) {
	cmd := exec.Command("bash", "-c", command)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), true
	}
	return string(out), false
}

func RepairTrajectory(messages []Message) []Message {
	resolved := make(map[string]bool)
	for _, m := range messages {
		if m.Role != RoleUser {
			continue
		}
		for _, b := range m.Content {
			if tr, ok := b.(ToolResultBlock); ok {
				resolved[tr.ToolUseID] = true
			}
		}
	}
	result := make([]Message, len(messages))
	copy(result, messages)
	for _, m := range messages {
		if m.Role != RoleAssistant {
			continue
		}
		for _, b := range m.Content {
			tu, ok := b.(ToolUseBlock)
			if !ok {
				continue
			}
			if resolved[tu.ID] {
				continue
			}
			synthetic := Message{
				Role: RoleUser,
				Content: []ContentBlock{
					ToolResultBlock{
						Type:      "tool_result",
						ToolUseID: tu.ID,
						Content:   "Interrupted by user",
						IsError:   true,
					},
				},
				UUID:       newUUID(),
				ParentUUID: m.UUID,
			}
			result = append(result, synthetic)
			resolved[tu.ID] = true
		}
	}
	return result
}

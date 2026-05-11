// Package query implements the agent query loop and trajectory-repair utilities.
package query

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// ── Trajectory repair ─────────────────────────────────────────────────────────

// RepairTrajectory ensures every ToolUseBlock in the message list has a
// corresponding ToolResultBlock.  For any tool_use that is missing its result
// a synthetic error ToolResultBlock is injected as a new user Message.
func RepairTrajectory(messages []Message) []Message {
	// Build the set of tool-use IDs that already have a result.
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
			// Synthesise a tool_result error message.
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

// newUUID returns a fresh UUID string using crypto/rand.
func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return hex.EncodeToString(b[:4]) + "-" + hex.EncodeToString(b[4:6]) + "-" + hex.EncodeToString(b[6:8]) + "-" + hex.EncodeToString(b[8:10]) + "-" + hex.EncodeToString(b[10:])
}

// ── callModel shim ────────────────────────────────────────────────────────────

// CallModel is the function used by the query loop to call the Anthropic API.
// The real implementation lives in src/api/client.go and is wired up from
// main.go to avoid an import cycle (api imports query; query must not import api).
// Tests replace this with a mock via SetCallModel.
var CallModel func(ctx context.Context, p CallModelParams) (<-chan StreamEvent, error)

// SetCallModel wires the API client implementation into the query loop.
// Must be called before the first Query() invocation.
func SetCallModel(fn func(ctx context.Context, p CallModelParams) (<-chan StreamEvent, error)) {
	CallModel = fn
}

// callModelFn is an internal alias used inside the loop so tests can override
// CallModel without data races on the exported var.
var callModelFn func(ctx context.Context, p CallModelParams) (<-chan StreamEvent, error)

// ── Query ─────────────────────────────────────────────────────────────────────

// Query runs the agent query loop for the given params.
// It returns two channels:
//   - msgCh: streams intermediate Messages as they are produced.
//   - termCh: emits exactly one Terminal value when the loop finishes.
func Query(ctx context.Context, p QueryParams) (<-chan Message, <-chan Terminal) {
	msgCh := make(chan Message, 64)
	termCh := make(chan Terminal, 1)

	go func() {
		defer close(msgCh)
		defer close(termCh)
		queryLoop(ctx, p, msgCh, termCh)
	}()

	return msgCh, termCh
}

// queryLoop is the core agentic loop.
func queryLoop(
	ctx context.Context,
	p QueryParams,
	msgCh chan<- Message,
	termCh chan<- Terminal,
) {
	messages := make([]Message, len(p.Messages))
	copy(messages, p.Messages)

	model := p.Model
	if model == "" {
		model = "claude-sonnet-4-6"
	}
	maxTurns := p.MaxTurns
	if maxTurns == 0 {
		maxTurns = 10
	}

	// Collect tool definitions from the registry.
	var tools []ToolDefinition
	if p.ToolCtx != nil && p.ToolCtx.Tools != nil {
		tools = p.ToolCtx.Tools.ToDefinitions()
	}

	for turn := 0; turn < maxTurns; turn++ {
		select {
		case <-ctx.Done():
			repaired := RepairTrajectory(messages)
			termCh <- Terminal{
				Reason:   TerminalAborted,
				Messages: repaired,
			}
			return
		default:
		}

		callFn := callModelFn
		if callFn == nil {
			callFn = CallModel
		}

		evCh, err := callFn(ctx, CallModelParams{
			Messages:  messages,
			System:    p.SystemParts,
			Tools:     tools,
			Model:     model,
			MaxTokens: 8192,
		})
		if err != nil {
			repaired := RepairTrajectory(messages)
			termCh <- Terminal{
				Reason:   TerminalError,
				Messages: repaired,
				Error:    fmt.Errorf("callModel: %w", err),
			}
			return
		}

		ca := &contentAccumulator{}
		var assistantBlocks []ContentBlock
		var pendingToolResults []ContentBlock
		anyBlockReceived := false
		aborted := false
		var streamErr error

		assistantUUID := newUUID()
		var parentUUID string
		if len(messages) > 0 {
			parentUUID = messages[len(messages)-1].UUID
		}

	eventLoop:
		for {
			select {
			case <-ctx.Done():
				aborted = true
				break eventLoop
			case ev, ok := <-evCh:
				if !ok {
					break eventLoop
				}

				switch ev.Type {
				case "content_block_start":
					if ev.BlockMeta != nil {
						ca.startBlock(ev.Index, ev.BlockMeta.Type, ev.BlockMeta.ID, ev.BlockMeta.Name)
					}

				case "content_block_delta":
					if ev.Delta != nil {
						ca.applyDelta(ev.Index, ev.Delta.Type, ev.Delta.Text, ev.Delta.PartialJSON, ev.Delta.Thinking)
					}

				case "content_block_stop":
					block, buildErr := ca.buildBlockAt(ev.Index)
					if buildErr != nil {
						streamErr = buildErr
						break eventLoop
					}
					if block == nil {
						break
					}

					anyBlockReceived = true
					assistantBlocks = append(assistantBlocks, block)

					partialMsg := Message{
						Role:       RoleAssistant,
						Content:    []ContentBlock{block},
						UUID:       assistantUUID,
						ParentUUID: parentUUID,
					}
					select {
					case msgCh <- partialMsg:
					case <-ctx.Done():
						aborted = true
						break eventLoop
					}

					if tu, ok := block.(ToolUseBlock); ok {
						result := executeOneTool(ctx, tu, p.ToolCtx)
						pendingToolResults = append(pendingToolResults, result)

						resultMsg := Message{
							Role:       RoleUser,
							Content:    []ContentBlock{result},
							UUID:       newUUID(),
							ParentUUID: assistantUUID,
						}
						select {
						case msgCh <- resultMsg:
						case <-ctx.Done():
							aborted = true
							break eventLoop
						}
					}

				case "message_stop":
					if len(assistantBlocks) > 0 {
						assistantMsg := Message{
							Role:       RoleAssistant,
							Content:    assistantBlocks,
							UUID:       assistantUUID,
							ParentUUID: parentUUID,
						}
						messages = append(messages, assistantMsg)

						if len(pendingToolResults) > 0 {
							toolResultMsg := Message{
								Role:       RoleUser,
								Content:    pendingToolResults,
								UUID:       newUUID(),
								ParentUUID: assistantUUID,
							}
							messages = append(messages, toolResultMsg)
						}
					}

					hasToolUse := false
					for _, b := range assistantBlocks {
						if _, ok := b.(ToolUseBlock); ok {
							hasToolUse = true
							break
						}
					}

					if !hasToolUse {
						termCh <- Terminal{
							Reason:   TerminalCompleted,
							Messages: messages,
						}
						return
					}

					break eventLoop

				case "error":
					streamErr = fmt.Errorf("stream error event received from API")
					break eventLoop
				}
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
			if !anyBlockReceived {
				repaired := RepairTrajectory(messages)
				termCh <- Terminal{
					Reason:   TerminalAborted,
					Messages: repaired,
				}
				return
			}
			if len(assistantBlocks) > 0 {
				assistantMsg := Message{
					Role:       RoleAssistant,
					Content:    assistantBlocks,
					UUID:       assistantUUID,
					ParentUUID: parentUUID,
				}
				messages = append(messages, assistantMsg)
			}
			repaired := RepairTrajectory(messages)
			termCh <- Terminal{
				Reason:   TerminalAborted,
				Messages: repaired,
			}
			return
		}
	}

	repaired := RepairTrajectory(messages)
	termCh <- Terminal{
		Reason:   TerminalMaxTurns,
		Messages: repaired,
	}
}

// ── accumBlock ────────────────────────────────────────────────────────────────

type accumBlock struct {
	blockType string
	id        string
	name      string
	text      []byte
	inputJSON []byte
	thinking  []byte
}

func (b *accumBlock) appendText(s string)     { b.text = append(b.text, s...) }
func (b *accumBlock) appendInput(s string)    { b.inputJSON = append(b.inputJSON, s...) }
func (b *accumBlock) appendThinking(s string) { b.thinking = append(b.thinking, s...) }

// contentAccumulator assembles content blocks from SSE events.
type contentAccumulator struct {
	blocks []*accumBlock
}

func (ca *contentAccumulator) ensureIndex(index int) {
	for len(ca.blocks) <= index {
		ca.blocks = append(ca.blocks, nil)
	}
}

func (ca *contentAccumulator) startBlock(index int, blockType, id, name string) {
	ca.ensureIndex(index)
	ca.blocks[index] = &accumBlock{
		blockType: blockType,
		id:        id,
		name:      name,
	}
}

func (ca *contentAccumulator) applyDelta(index int, deltaType, text, partialJSON, thinking string) {
	ca.ensureIndex(index)
	b := ca.blocks[index]
	if b == nil {
		return
	}
	switch deltaType {
	case "text_delta":
		b.appendText(text)
	case "input_json_delta":
		b.appendInput(partialJSON)
	case "thinking_delta":
		b.appendThinking(thinking)
	}
}

func (ca *contentAccumulator) buildBlockAt(index int) (ContentBlock, error) {
	if index >= len(ca.blocks) || ca.blocks[index] == nil {
		return nil, nil
	}
	b := ca.blocks[index]
	switch b.blockType {
	case "text":
		return TextBlock{Type: "text", Text: string(b.text)}, nil
	case "thinking":
		return ThinkingBlock{
			Type:     "thinking",
			Thinking: string(b.thinking),
		}, nil
	case "tool_use":
		var input map[string]any
		if len(b.inputJSON) > 0 {
			if err := json.Unmarshal(b.inputJSON, &input); err != nil {
				return nil, fmt.Errorf("parse tool_use input JSON for %q: %w", b.name, err)
			}
		}
		return ToolUseBlock{
			Type:  "tool_use",
			ID:    b.id,
			Name:  b.name,
			Input: input,
		}, nil
	}
	return nil, nil
}

// ── executeOneTool ────────────────────────────────────────────────────────────

// executeOneTool runs a single tool call using the Phase 2 tool registry.
func executeOneTool(ctx context.Context, tu ToolUseBlock, tctx *ToolUseContext) ToolResultBlock {
	select {
	case <-ctx.Done():
		return ToolResultBlock{
			Type:      "tool_result",
			ToolUseID: tu.ID,
			Content:   "Interrupted by user",
			IsError:   true,
		}
	default:
	}

	var output string
	var isError bool

	if tctx != nil && tctx.Tools != nil {
		tool, found := tctx.Tools.FindByName(tu.Name)
		if !found {
			output = fmt.Sprintf("unknown tool: %s", tu.Name)
			isError = true
		} else {
			result, err := ExecuteTool(ctx, tool, tu.Input, tctx)
			if err != nil {
				output = err.Error()
				isError = true
			} else {
				output = result.Content
				isError = result.IsError
			}
		}
	} else {
		output = fmt.Sprintf("unknown tool: %s (no tool registry)", tu.Name)
		isError = true
	}

	return ToolResultBlock{
		Type:      "tool_result",
		ToolUseID: tu.ID,
		Content:   output,
		IsError:   isError,
	}
}

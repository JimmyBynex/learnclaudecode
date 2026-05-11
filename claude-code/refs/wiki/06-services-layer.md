# 6. Services Layer

## 6.4 Context Compaction & Memory

### Compaction Priority Order

1. **Session Memory Compaction**: Structured "Session Memory" block without full re-summarization (cheaper)
2. **Reactive Compaction** (REACTIVE_COMPACT flag): Routes through `reactiveCompactOnPromptTooLong`
3. **Micro-Compaction**: Removes redundant metadata, truncates large tool outputs
4. **Conversation Summarization**: LLM call (`compactConversation`) for concise narrative

### Key Functions

| Entity | Location | Description |
|--------|----------|-------------|
| `compactConversation` | `src/services/compact/compact.ts` | Full LLM-based summarization |
| `microcompactMessages` | `src/services/compact/microCompact.ts` | Prune non-essential data |
| `trySessionMemoryCompaction` | `src/services/compact/sessionMemoryCompact.ts` | Structured session memory update |
| `suppressCompactWarning` | `src/services/compact/compactWarningState.ts` | Reset UI warnings |
| `runPostCompactCleanup` | `src/services/compact/postCompactCleanup.ts` | Invalidate caches |

### Session Memory (`src/services/SessionMemory/`)

- Transient to current conversation (more detailed than MEMORY.md)
- Tracks `lastSummarizedMessageId` to avoid redundant processing
- Cleanup: `runPostCompactCleanup()` invalidates temp state/caches

### AutoDream & MEMORY.md (`src/services/autoDream/`)

Consolidation Cycle:
1. **Orient**: Read existing `MEMORY.md`
2. **Gather**: Scan recent daily logs, session histories
3. **Consolidate**: Update durable memory files with new learnings
4. **Prune**: Remove outdated information

### Memory Scanning (`src/memdir/`)

- Searching through project docs and `MEMORY.md` for relevant context
- Team Memory: Shared knowledge bases in swarm configurations
- `loadMemoryPrompt`: Loads MEMORY.md into system prompt

### Compaction Triggers

- **Auto-Trigger**: Context exceeds threshold → `autoCompact`
- **Manual**: `/compact` command
- **Warning suppression**: `suppressCompactWarning()` clears UI after success

---

## 6.5 API Client & Rate Limiting

### Key Components

| Component | Description | File |
|-----------|-------------|------|
| `Anthropic` | Primary Anthropic API class | `src/services/api/client.ts` |
| `withRetry` | Exponential backoff wrapper | `src/services/api/withRetry.ts` |
| `filesApi` | File upload/management | `src/services/api/filesApi.ts` |
| `usage` | Token consumption tracking | `src/services/api/usage.ts` |

### sendMessage (`claude.ts`)

1. System Prompt Assembly: Merging base + tool definitions
2. Message Formatting: Internal → Anthropic SDK format
3. Streaming: Server-sent events (SSE) for real-time feedback
4. State Capture: Updates `lastAPIRequest`, `lastAPIRequestMessages`

### Retry Logic (`withRetry.ts`)

- **Exponential Backoff**: Increasing delays between attempts
- **Jitter**: Randomizing delays to prevent thundering herd
- **Error Classification**: Retriable (429, timeouts) vs terminal (invalid params)

### Usage Tracking (`usage.ts`)

- Intercepts API responses for `usage` metadata (input/output tokens, cache hits/misses)
- Persisted in `AppStateStore`
- Query via `/usage`, `/cost` commands
- Global metrics: `totalCostUSD`, `totalAPIDuration`, `modelUsage`

### Policy Limits (`policyLimits.ts`)

- **Max Tokens per Session**: Prevent runaway loops
- **Max Tool Calls**: Limit recursive tool use depth

### Remote Managed Settings (`remoteManagedSettings.ts`)

Anthropic can push config updates without package update:
- Default model versions
- Feature flags (enable/disable tools)
- System prompt fragments

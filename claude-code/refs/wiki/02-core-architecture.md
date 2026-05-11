# 2. Core Architecture

## 2.1 CLI Entrypoint & Initialization

Startup pipeline:
1. **Argument Parsing** – Commander.js in `src/main.tsx`
2. **Bootstrap & Initialization** – `src/entrypoints/init.ts`: keychain credentials, MDM settings, telemetry bootstrap, remote settings
3. **REPL Launch** – Interactive React/Ink terminal

Key files:
- `src/main.tsx` - Commander.js, Ink bootstrapping
- `src/entrypoints/init.ts` - environment setup
- `src/cli/exit.ts` - `cliError()`, `cliOk()`
- `src/cli/update.ts` - version checks, installation type detection

---

## 2.2 QueryEngine & Query Loop

### Key Classes and Functions

- **`QueryEngine`**: Stateful class for one conversation session. Holds message history, file cache, usage statistics, configuration.
- **`ask()`**: Primary SDK/headless entry point. Instantiates `QueryEngine`, runs single prompt to completion.
- **`submitMessage()`**: Async generator yielding `Message` objects as produced by LLM or tools.
- **`query()`**: Low-level interface to Anthropic API - streaming, retries, raw response parsing.

### System Prompt Assembly

Before every LLM request:
1. **Base Instructions**: Core behavioral guidelines
2. **Environment Context**: CWD, OS details, shell (`fetchSystemPromptParts`)
3. **Tool Definitions**: XML-formatted schemas for all tools including MCP/skill tools
4. **Memory**: Content from `MEMORY.md` via `loadMemoryPrompt` (`src/memdir/memdir.ts`)
5. **Custom Instructions**: `customSystemPrompt` or `appendSystemPrompt`

### Tool Call Dispatch

| Step | Entity | Action |
|------|--------|--------|
| 1 | QueryEngine | Identifies `tool_use` in API response |
| 2 | `canUseTool` | Checks permissions (Allow/YOLO/Prompt) |
| 3 | `Tool.call()` | Executes logic (BashTool runs shell command) |
| 4 | Result Reporting | Wraps output in `tool_result`, appends to history |

### Context Boundary & Compaction

1. **Compact Boundaries**: Points where history was summarized
2. **Snipping (HISTORY_SNIP)**: Truncation for long headless sessions
3. **Micro-compaction**: Removes redundant metadata, shrinks large tool outputs

### Result Reporting

- Cost tracking: USD based on input/output tokens per model
- Synthetic Outputs: Large tool output represented without bloating history
- Session Persistence: Flushes transcript via `recordTranscript`

---

## 2.3 Application State Management

Hybrid state: singleton `STATE` object + React `AppStateStore` (subscriber pattern).

Key STATE properties:
- `projectRoot`: Stable project root
- `totalCostUSD`, `modelUsage`: Cost/token tracking
- `totalAPIDuration`: API performance
- `kairosActive`: Proactive monitoring flag
- `sessionBypassPermissionsMode`: YOLO mode
- `sessionId`, `parentSessionId`: Session identity (Plan Mode)

`AppStateStore`: reactive wrapper for UI components.

---

## 2.4 User Input Processing

`processUserInput` entry point - transforms raw terminal input:

1. **Slash Command Interception**: Input starting with `/` → `src/commands.ts` registry (80+ commands)
2. **Bash Execution**: Input starting with `!` → direct BashTool, bypasses LLM
3. **Prompt Dispatch**: Standard text → `QueryEngine.submitMessage()`

### Command Categories

| Category | Examples |
|----------|---------|
| Session | `/clear`, `/history`, `/stats`, `/summary` |
| Configuration | `/config`, `/theme`, `/color`, `/vim` |
| Agent Control | `/plan`, `/tasks`, `/branch`, `/agents` |
| System/Utility | `/doctor`, `/version`, `/health`, `/login` |

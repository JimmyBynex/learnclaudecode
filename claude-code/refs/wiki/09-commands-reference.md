# 9. Commands Reference

80+ slash commands. Processed via pipeline: matches against registry in `src/commands.ts` → executes against `QueryEngine` + `AppState`.

## 9.1 Session & Conversation Commands

### /clear

Destructive reset of current conversation:
- Executes SessionEnd hooks (1.5s timeout)
- Kills foreground tasks, preserves background tasks
- Clears message list, resets MCP state, file history
- Regenerates sessionId, clears metadata (titles, tags)

### /compact

Reduces token count to stay within context window:
1. Filters messages after last compact boundary
2. Attempts session memory compaction first (non-LLM)
3. Runs micro-compaction to remove redundant outputs
4. Uses LLM summarization if needed
5. Triggers cleanup processes

### /session

Manages session metadata:
- `list`: Shows recent history
- `rename`: Updates metadata
- Sessions stored with UUID, human-readable title, optional tag

### /context

Token usage breakdown:
- Mirrors QueryEngine pipeline with message filtering
- Returns Markdown table: model info, token %, strategy status, category breakdown, MCP usage

### /memory / /summary

- `/memory`: Displays MEMORY.md content and SessionMemory state
- `/summary`: Generates brief recap of conversation goals

### /rewind

Removes last N messages from conversation history.

### /cost / /stats / /usage / /extra-usage

- Query UsageTracker and AppState
- Per-model cost calculations
- Cache hits/misses breakdown

---

## 9.2 Configuration & Settings Commands

### /config

Primary graphical settings interface. Handles user-configurable and MDM-enforced (read-only) settings.

### /model / /effort

- `/model`: Updates LLM model for current session
- `/effort`: Controls reasoning effort (high/medium/low)

### /theme / /color

- `/theme`: light/dark/system modes
- `/color`: Agent identity color, persists via `saveAgentColor`

### /permissions / /sandbox-toggle

- `/permissions`: Switch between default, bypass (YOLO), restricted
- `/sandbox-toggle`: Enable/disable containerized shell execution

### /hooks

Configures AsyncHookRegistry events (postSamplingHooks, execPromptHook).

---

## 9.3 Agent & Task Commands

### /branch

Creates conversation forks:
- Reads current transcripts
- Assigns `forkedFrom` with original session IDs
- Clones content-replacement entries
- Generates unique names with numeric suffixes

### /brief

Restricts agents to `SendUserMessage` for output:
- Checks feature flag entitlements
- Updates `AppState.isBriefOnly`
- Synchronizes tool visibility
- Injects system reminders

### /btw

**Side questions without polluting main history**:
- Uses `BtwSideQuestion` component
- Implements cache safety via `buildCacheSafeParams`
- Executes via `runSideQuestion`
- Off-topic queries don't affect main conversation history

### /agents / /tasks

- `/agents`: Launches AgentsMenu to spawn sub-agents
- `/tasks`: Tracks task lifecycles by TaskType and TaskStatus

### /plan

Enters Plan Mode: agent focuses on architectural design, restricted tools.

### /add-dir

Expands file system context:
- Validates paths
- Applies permission updates
- Refreshes SandboxManager
- Optionally persists to settings

### /advisor

Secondary model monitors primary outputs. Validates advisor model, tier checking.

### /fast / /effort / /passes

Performance tuning: reasoning budgets and iteration limits.

---

## 9.4 Integration & Tooling Commands

### /bridge / /remote-control

Bidirectional connection to claude.ai web interface. QR code support for mobile handoff.

### /mcp

Manages MCP connections: add, remove, list, serve.

### /plugin

Extension management: install, list, validate, marketplace.

### /doctor

Health checks: authentication, network, permissions, dependencies.

### /login / /logout

OAuth flows for authentication.

### /teleport / /remote-env / /remote-setup

Session state migration and remote environment provisioning.

### /mobile / /voice

- `/mobile`: QR codes for mobile session continuation
- `/voice`: Toggle speech-to-text

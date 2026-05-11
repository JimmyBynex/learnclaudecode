# 3. Tool System

Every interaction between the LLM and the local environment is mediated by the `Tool` interface, which enforces strict permission boundaries, input validation, and result size management.

## Tool Interface

Generic `Tool<Input, Output>` requires:
- **name**: Unique identifier for LLM invocation
- **description**: Natural language usage guidance
- **inputSchema**: Zod or JSON schema for arguments
- **validate()**: Pre-execution input checking
- **call()**: Primary handler returning `ToolResult`

Tools operate within `ToolUseContext` providing app state, file caches, communication channels.

## 3.2 Shell & File Tools

### BashTool (`src/tools/BashTool/`)

Most powerful tool - shell command execution with multi-layered security:

1. **Detecting Dangerous Patterns**: Piped commands, redirects, subshells via bashClassifier
2. **Path Validation**: All paths must be within allowed project boundaries
3. **Sed Validation**: Prevents unauthorized file corruption

Key: `abortController` allows UI to cancel long-running commands.

### FileReadTool

- Reads specific line ranges (critical for large files to prevent context overflow)
- Uses `FileStateCache` for most recent version
- `maxResultSizeChars = Infinity` (never persisted to disk - would create circular loop)

### FileEditTool

- Primarily uses structured "Search and Replace" blocks
- Triggers `StructuredDiff` UI for user review
- Uses `DenialTrackingState` to prevent repeated failed attempts

### FileWriteTool

- Creating new files or completely rewriting small files

### GlobTool

- File system discovery via pattern matching
- Restricted by `.gitignore` rules by default
- Avoids indexing build artifacts / node_modules

### GrepTool

- High-performance text searching
- Truncates large result sets with summaries
- Used for finding function definitions, variable usages

## 3.3 Agent & Orchestration Tools

### AgentTool

Spawns sub-agents internally via `query()`. Key flow:
1. Generates unique `taskId` with `t` prefix (in_process_teammate)
2. Creates initial task state (pending status)
3. Sets up environment vars including `CLAUDE_CODE_TEAMMATE_ID`
4. Executes query loop in sub-process/in-process
5. Returns result to parent agent

### Task Management Tools

- **TaskCreateTool, TaskGetTool, TaskListTool, TaskUpdateTool, TaskStopTool, TaskOutputTool**: Full CRUD for task lifecycle
- **TeamCreateTool, TeamDeleteTool**: Swarm team management
- **SendMessageTool**: Communication between agents

## 3.4 Web, LSP & MCP Tools

### WebSearchTool / WebFetchTool

- `WebFetchTool`: HTML→Markdown conversion, ssrfGuard for security
- `WebSearchTool`: Google search, tracks via `WebSearchProgress`

### LSPTool

- `getDefinition`, `getReferences`, `getHover`
- JSON-RPC communication with language servers

### MCP Tools

- **MCPTool**: Dynamically generated from MCP servers
- **ListMcpResourcesTool, ReadMcpResourceTool**: Resource access

## 3.5 Utility & Workflow Tools

- **EnterPlanModeTool**: Switches to plan mode (read-only focus)
- **EnterWorktreeTool**: Isolates context in git worktrees
- **SkillTool**: Routes LLM to registered skills
- **ScheduleCronTool (CronCreateTool, CronDeleteTool, CronListTool)**: Task scheduling
- **ToolSearchTool**: Keyword search over deferred tools
- **TodoWriteTool**: Structured task list management
- **BriefTool**: Toggles brief-only output mode
- **SleepTool**: Delays execution
- **ConfigTool**: Runtime settings inspection/modification
- **SyntheticOutputTool**: Injects programmatic content

## Security Model

Three permission modes:
- **Default**: Prompts user for destructive actions
- **Bypass/YOLO**: Executes without prompting
- **Auto-Deny**: Rejects requests

`ToolPermissionContext` enforces rules throughout lifecycle.

### Permission Rules

Sources: user, project, global. Types:
- **Always Allow**: Skip permission dialogs
- **Always Deny**: Immediate rejection
- **Always Ask**: Forces user prompts even if classifier would approve

### Denial Tracking

`DenialTrackingState` prevents agent from repeatedly attempting the same denied action.

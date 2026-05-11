# 4. Multi-Agent Swarm System

Leader-Teammate architecture. Primary "Leader" agent spawns and manages multiple sub-agents ("Teammates") for parallel task execution.

## Swarm Backends

| Backend | File | Purpose |
|---------|------|---------|
| InProcess | `src/utils/swarm/InProcessBackend.ts` | Background child processes (default) |
| Tmux | `src/utils/swarm/TmuxBackend.ts` | New tmux panes |
| iTerm2 | `src/utils/swarm/ITermBackend.ts` | New iTerm2 tabs via AppleScript |

## Teammate Lifecycle

1. **Initialization (`teammateInit`)**: Generates unique taskId (`t` prefix), creates `TaskStateBase` (status: pending), sets env vars
2. **Execution (`teammateLayoutManager`)**: Determines UI rendering location
3. **Termination**: `kill` method updates AppState to terminal status

## Permission Synchronization

- **leaderPermissionBridge**: Leader is source of truth for permissions
- Rule Propagation: When leader's PermissionMode changes (e.g., `/yolo`), synchronized to all teammates
- Global Latch: Denied permission in one teammate updates `DenialTrackingState` globally

## Task Types

| Type | Prefix | Description |
|------|--------|-------------|
| local_bash | b | Shell commands |
| local_agent | a | Local sub-agent |
| remote_agent | r | Remote/cloud agent |
| in_process_teammate | t | In-process swarm agent |
| local_workflow | w | Structured workflows |
| monitor_mcp | m | MCP monitoring |
| dream | d | Memory consolidation |

## Task State

`TaskStateBase` fields:
- `id`: Unique ID with type prefix
- `type`: TaskType
- `status`: pending → running → completed/failed/killed
- `description`: Human-readable goal
- `outputFile`: Disk-backed log path (from `getTaskOutputPath`)
- `startTime`: Epoch timestamp
- `outputOffset`: Read position in output file

## Task ID Generation

`generateTaskId(type)`: 8-char suffix from 36-char alphabet (0-9a-z). ~2.8 trillion combinations. Case-insensitive, filesystem-safe.

## Reconnection Logic

- Task states persisted in `AppState`
- On restart: scans for orphaned tasks
- TmuxBackend/ITermBackend: re-detects existing panes by taskId
- `isTerminalTaskStatus()`: Guards against injecting into dead teammates

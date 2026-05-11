# 14. Glossary

## Core System

| Term | Definition |
|------|-----------|
| **Tengu** | Internal codename for Claude Code. Appears in telemetry, env vars. |
| **BUDDY** | Companion system with deterministic species assignment (seeded from user ID) |
| **KAIROS** | Autonomous monitoring subsystem, acts independently on errors |
| **ULTRAPLAN** | Offloads complex planning to remote Opus sessions |
| **Swarm** | Multi-agent orchestration: leader + teammate agents |
| **Dream (autoDream)** | Memory consolidation: gather → prune cycle |
| **Compaction** | Reduces conversation history size; "snip compaction" replaces large blocks with summaries |
| **ToolUseContext** | Configuration object passed to tools: app state, execution resources |

## Bridge & Infrastructure

| Term | Definition |
|------|-----------|
| **Bridge** | Connects local CLI to remote environments via polling |
| **CCR v2** | Updated transport protocol using worker endpoints |
| **Env-less Bridge** | Bypasses Environments API for direct OAuth connections |
| **session_*** | Client-facing session ID prefix |
| **cse_*** | Infrastructure-level session ID prefix |
| **Work Secret** | Base64url-encoded credentials |
| **Trusted Device Token** | Persistent keychain-based auth token |

## Additional Terms

| Term | Definition |
|------|-----------|
| **HistoryPage** | Paginated session event history (100 events/page) |
| **BackoffConfig** | Controls retry delays during network failures |
| **BridgeLogger** | Displays connection status TUI |
| **SpawnMode** | Controls child process spawn strategy |
| **SessionHandle** | Manages child process lifecycle |
| **PermissionMode** | default / bypass / yolo / plan |
| **DenialTrackingState** | Prevents repeated denied permission requests |
| **FileStateCache** | LRU cache for file read state |
| **ContentReplacementState** | Per-thread state for tool result budget |
| **QueryChainTracking** | chainId + depth for tracking nested query calls |

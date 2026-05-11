# 5. Bridge & Remote Control

Connects local Claude Code CLI to remote environments (Claude.ai web, IDE extensions, cloud containers). Enables "Remote Control" from web UI.

## Architecture

Long-polling worker model:
1. CLI registers itself as available environment
2. Enters poll loop for "work" (new sessions/messages)
3. On work received: spawns session runner → manages local REPL
4. Pipes input/output between remote caller and local shell/tools

## Bridge API & Authentication

`BridgeApiClient`:
- **Environment Registration**: `registerBridgeEnvironment` → advertises machine name, directory, git branch
- **Polling**: `pollForWork` long-polling for new tasks
- **Heartbeats**: `heartbeatWork` keeps server informed
- **Authentication Tiers**: OAuth token refresh via `withOAuthRetry`, `X-Trusted-Device-Token` for elevated security
- **Entitlement Gates**: `isBridgeEnabled()`, `isBridgeEnabledBlocking()`

## Session Runner & REPL Bridge

| Component | Role |
|-----------|------|
| `SessionSpawner` | Manages child process lifecycle per remote session |
| `ReplBridge` | Bridges remote transport (SSE/WS) ↔ local `QueryEngine` |
| `ReplBridgeTransport` | Abstract interface for send/receive (JSON, attachments) |
| `FlushGate` | Ensures all pending output sent before closing |

`sessionRunner`: Creates temporary worktrees for clean environments, manages SpawnMode.

## Configuration

- **Capacity**: Default 32 concurrent sessions (`SPAWN_SESSIONS_DEFAULT`)
- **Polling Intervals**: Dynamic adjustment based on error backoff (`BackoffConfig`)
- **Session ID Compatibility**: `toCompatSessionId` bridges `cse_*` ↔ `session_*`

## Main Orchestrator (`bridgeMain.ts`)

`runBridgeLoop`:
- Registers environment
- SIGTERM handling for graceful shutdown
- Maintains `activeSessions` map
- Handles "Env-less" (v2) direct connections via `remoteBridgeCore.ts`

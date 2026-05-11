# 1. Overview

Claude Code (internal codename: **Tengu**) is a high-performance CLI AI agent designed for professional software engineering tasks. Unlike traditional chat interfaces, it operates within developers' terminals with capabilities including shell execution, file management, complex refactoring, and multi-agent coordination.

The system combines a custom React-based terminal engine (Ink) with a sophisticated tool-dispatch loop that allows the agent to interact with the local environment and remote services autonomously.

## Core Architectural Pillars

1. **The Execution Loop (QueryEngine)** - manages conversation state and iterative LLM interaction cycles; handles context management including history compaction. Key: `src/QueryEngine.ts`

2. **Tool System** - over 40 specialized tools spanning from `FileReadTool` and `BashTool` to `AgentTool`. Implemented in `src/Tool.ts` and `src/tools/`

3. **Multi-Agent Swarm** - complex tasks leverage sub-agent spawning through the `Swarm` coordinator in `src/coordinator/`

4. **Terminal UI (Ink)** - React-based CLI engine for rich interface elements. Entrypoint: `src/main.tsx`

## Advanced Systems

- **BUDDY**: Tamagotchi-style companion in `src/buddy/`
- **Dream System**: Background memory consolidation via `autoDream`
- **ULTRAPLAN**: Complex tasks offloaded to remote Opus 4.6 sessions
- **KAIROS**: Always-on proactive assistant

## Key Concepts & Terminology

| Term | Definition |
|------|-----------|
| Tengu | Internal codename for Claude Code |
| BUDDY | Tamagotchi-style terminal companion |
| KAIROS | Always-on proactive assistant |
| ULTRAPLAN | High-effort planning via remote Opus 4.6 |
| Undercover Mode | Blocks leaking internal model codenames |

## Task Types (TaskType)

- `local_bash` (b): Execution of shell commands
- `local_agent` (a): Sub-agent running locally
- `remote_agent` (r): Agent running in remote environment
- `in_process_teammate` (t): Sub-agent in same process (swarm)
- `local_workflow` (w): Structured local workflows
- `monitor_mcp` (m): MCP server monitoring
- `dream` (d): Background maintenance tasks

## Memory & Dream System

autoDream cycle:
1. **Orient**: Reads MEMORY.md in project root
2. **Gather**: Extracts new signals from session logs
3. **Consolidate**: Updates durable memory files
4. **Prune**: Removes redundant/stale information

## Permission Modes

- `default`: Standard interactive prompts for sensitive actions
- `bypass`: (YOLO mode) Automatically allows all tool uses
- `plan`: Focused on architectural design with restricted tools

## Global Application State

Key STATE properties:
- `projectRoot`: Stable root of the project
- `totalCostUSD`: Cumulative API cost
- `kairosActive`: Boolean for proactive monitoring
- `sessionBypassPermissionsMode`: YOLO mode flag

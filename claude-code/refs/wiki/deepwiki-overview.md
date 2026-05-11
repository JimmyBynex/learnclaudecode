# Claude Code (Tengu) - DeepWiki Overview

## What is Claude Code?

Claude Code, internally called **Tengu**, functions as "a high-performance CLI AI agent designed for professional software engineering tasks." Rather than operating through standard chat interfaces, it executes directly within developer terminals, managing shell commands, files, complex refactors, and multi-agent workflows.

## Core Architecture

The system rests on four foundational pillars:

**1. QueryEngine (Execution Loop)**
The orchestration heart managing conversation state and LLM-tool interaction cycles. Located in `src/QueryEngine.ts`, it handles context management and conversation flow.

**2. Tool System**
Over 40 specialized tools enable OS and development environment interaction—from basic filesystem operations (`FileReadTool`, `BashTool`) to higher-level orchestration (`AgentTool`), residing primarily in `src/tools/`.

**3. Multi-Agent Swarm**
The `Swarm` coordinator in `src/coordinator/` manages sub-agent lifecycles for parallelizable or complex tasks, maintaining unified state across distributed work.

**4. Terminal UI (Ink)**
"A React-based engine for CLI layouts" enabling rich terminal elements like progress bars, diffs, and permission dialogs in `src/main.tsx`.

## Notable Advanced Systems

- **BUDDY**: Tamagotchi-style companion with evolution mechanics (`src/buddy/`)
- **Dream System**: Background memory consolidation via daily logs
- **ULTRAPLAN**: Remote deep planning using Opus 4.6
- **KAIROS**: Proactive monitoring assistant

The documentation spans 14 major sections covering everything from installation through security and advanced features.

## Additional Details (from initial fetch)

### Project Identity
Claude Code, internally codenamed **Tengu**, is a high-performance CLI AI agent engineered for professional software development. It differs fundamentally from chat interfaces by operating within the developer's terminal environment with capabilities to execute shell commands, manage files, perform refactoring, and coordinate multi-agent workflows.

### Technology Stack
- **Runtime:** Bun/Node.js
- **Authentication:** Anthropic OAuth
- **UI Framework:** Ink (React-based)
- **Language:** TypeScript

### BUDDY Companion
Tamagotchi-style companion system located in `src/buddy/` featuring species evolution, stat mechanics like CHAOS, and deterministic gacha mechanics.

### Dream System
Background memory consolidation service (autoDream) managing MEMORY.md files and daily logs for persistent context.

### ULTRAPLAN
Remote deep-planning capability leveraging Opus 4.6 sessions for complex architectural problem-solving.

### KAIROS
Always-on proactive assistant monitoring logs and system state for autonomous insights.

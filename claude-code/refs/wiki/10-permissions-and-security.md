# 10. Permissions & Security

"Human-in-the-loop" safety framework. Every tool request evaluated through permission modes, automated classifiers, and programmable hooks.

## 10.1 Permission Modes & Rules

### Permission Modes

- **default**: Explicit user approval for write/execute, guided by allow/deny rules
- **bypass**: Temporary elevated state (no repeated prompting)
- **yolo**: High-autonomy mode via LLM classifiers + dangerous pattern detection
- **plan**: Planning mode with restricted/monitored tools

### Permission Rules (by source: user, project, global)

- **Always Allow**: Tools matching → skip permission dialogs entirely
- **Always Deny**: Tools matching → immediate rejection
- **Always Ask**: Forces user prompts even if classifier would auto-approve

### Bash-specific

System analyzes command **intent** not just string matching. Detects "shadowed" rules where conflicting patterns create security gaps.

### Auto Mode (YOLO) Customization

1. `allow` rules for auto-approval
2. `soft_deny` rules triggering prompts even in auto mode
3. `environment` context for classifier

### Flags

- `shouldAvoidPermissionPrompts`: For background agents unable to show UI
- `awaitAutomatedChecksBeforeDialog`: Ensures checks complete before prompts

### Denial Tracking

Prevents infinite loops of forbidden requests. Provides human-readable explanations via "permission explainer."

---

## 10.2 Bash Classifier & Sandbox

### Classification System

`classifyBashCommand` → result with danger flags, reasoning, confidence.
- Fast-path for known safe commands
- LLM-path for complex scripts

Key components:
- **DANGEROUS_PATTERNS**: Regex detectors for high-risk operations
- **yoloClassifier.ts**: Auto-approval logic in YOLO mode
- **ClassifierResult**: Risk flags, reasoning, confidence levels

### Validation Layers

- **pathValidation**: Blocks directory traversal, system file access
- **sedValidation**: Ensures `sed` targets only local files
- **bashSecurity**: Shell escaping and sanitization

### Sandbox Implementation

`SandboxProvider` interface → Docker, E2B, or local containers.
- Lazily initialized on first use
- Cleaned up when sessions end

---

## 10.3 Hooks System

`AsyncHookRegistry` manages async event dispatching.

### Three Hook Categories

**Agent Hooks (`execAgentHook`)**:
- `pre_tool_use`: Validation before tool execution
- `post_tool_use`: Post-execution checks
- `on_error`: Error handling

**Prompt Hooks (`execPromptHook`)**:
- `pre_prompt`: Context injection
- `post_sampling`: Response modification

**HTTP Hooks (`execHttpHook`)**:
- `ssrfGuard`: Prevents access to localhost/private IPs

### Standard Response Format

`HookResult`:
- `status`: 'continue' | 'stop' | 'error'
- `message`: Optional user-facing text
- `data`: Optional modified data

### Hook Sources

- Skill hooks: Skills react to agent actions
- Frontmatter hooks: Inject environment metadata
- Plugin hooks: Pre-prompt, post-tool-use lifecycle events

### Lifecycle Events

- `SessionStart`, `SessionEnd`: Session boundaries
- `PreToolUse`, `PostToolUse`: Tool execution wrapping
- `postSamplingHooks`: After LLM response
- `pre_compact`, `post_compact`: Compaction events

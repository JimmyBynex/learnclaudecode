# 7. Plugin & Skills System

## Plugin Architecture (`src/utils/plugins/`)

Plugins are distribution containers for third-party extensions. Sourced from: local directories, Git repos, official Marketplace.

### Key Components

- **pluginLoader**: Discovers, initializes plugins from configured sources
- **installedPluginsManager**: Manages local installed plugins/versioning
- **marketplaceManager**: Remote marketplace synchronization
- **dependencyResolver**: Verifies requirements
- **pluginPolicy**: Enforces security constraints
- **mcpPluginIntegration**: Maps MCP servers into core system

### Plugin Discovery Sources

1. Global/user scopes via CLI
2. Project scopes in `.claude-plugin` directories
3. Inline via `--plugin-dir` flag

### Security

- Blocklists for malicious plugins
- Scope restrictions (no cross-project access)
- Content validation for dangerous patterns

### Core Integrations

- **MCP Integration**: Plugins define MCP servers in manifests → custom tools
- **Hook System**: Plugins register for lifecycle events (pre-prompt, post-tool-use) via `AsyncHookRegistry`

### CLI Commands

```
claude plugin install <id>
claude plugin list
claude plugin validate <path>
claude plugin marketplace add
claude plugin update
```

---

## Skills System (`src/skills/`, `src/tools/SkillTool/`)

Skills are discrete functional units - differ from tools by involving complex multi-step logic or specialized LLM prompting.

### Three Types

1. **Bundled Skills**: Core capabilities shipped with binary
2. **MCP Skills**: Dynamically constructed from MCP servers
3. **SkillTool**: The dispatcher interface for LLM access

### Essential Bundled Skills

- `claudeApi`: Direct Claude API access
- `loop`: Iterative task logic
- `remember`: Long-term memory storage
- `scheduleRemoteAgents`: Offloads to remote environments
- `skillify`: Converts successful tool sequences into reusable skills (meta-skill)
- `verify`: Validates completed tasks

### SkillTool Flow

`SkillTool` acts as router: "use_skill" interface → dispatches to registered skills.

### Skills Loading

Skills discovered from:
- Bundled skills in distribution
- Filesystem skills (user-defined, project local)
- MCP skill builders

### REPL Command

`/skills`: View active capabilities in current session

### Slash Command Tool Skills

`getSlashCommandToolSkills(cwd)`: Loads skills as slash commands from project skills directory.

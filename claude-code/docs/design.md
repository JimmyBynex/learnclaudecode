# Claude Code (Go 重写) — 设计文档

> 语言：**Go**
> 原项目：TypeScript (Bun + React/Ink)，内部代号 Tengu
> 目标：理解核心架构——QueryEngine、工具系统、记忆、skill、压缩、hooks、多 agent

---

## 3a. 项目摘要

**一句话描述**：Claude Code 是一个 CLI AI agent，允许 Claude 在终端中直接执行 shell 命令、读写文件、通过递归调用自身协调多个并发子 agent，并通过持久记忆、skill 系统和自动压缩维持长期高效的工作能力。

**核心问题**：如何让 LLM 不只是"说话"，而是真正"行动"——在真实环境里执行工具调用、处理结果、持续循环直到任务完成；同时管理上下文长度、跨会话记忆、和可扩展的 skill 系统。

**关键架构决策**：
1. **工具调用作为核心原语**：一切能力（bash、读文件、spawn subagent、调用 skill）都封装成 Tool，统一进出接口。
2. **`query()` 是无限循环**：`while(true)` 循环：调用 API → 流式接收 → 找到 tool_use → 执行工具 → 把结果塞回消息列表 → 继续循环。不是递归，是迭代状态机。
3. **SubAgent 就是另一个 `query()` 调用**：AgentTool 内部直接调用 `query()`，用独立的消息列表和上下文——多 agent 不是独立系统，是工具调用链的自然延伸。
4. **Memory.md 注入系统 prompt**：`loadMemoryPrompt()` 在每次请求前把 MEMORY.md 内容注入到 system prompt，是跨会话记忆的核心机制。
5. **Skill = 注入 prompt 的 slash command**：Skill 本质上是 markdown 文件，当被 SkillTool 调用时，其内容以 user message 形式注入到对话中，驱动 Claude 按特定方式行动。
6. **Compaction 是 context window 管理策略**：当 token 过多时，按优先级触发：session memory → micro-compaction → LLM 摘要——保留要点，丢弃冗余细节。
7. **Hooks 是横切关注点**：AsyncHookRegistry 在工具调用前后、prompt 前后插入验证/修改/记录逻辑，不污染核心循环。

---

## 3b. 架构图

```
CLI (cobra)
   │
   ▼
QueryEngine.Submit(prompt)
   │
   ├─── loadMemoryPrompt() ──→ MEMORY.md content
   │
   ├─── buildSystemPrompt() ──→ []string
   │     (base instructions + env context + tool defs + memory)
   │
   ▼
query(messages, systemPrompt, tools)
   │
   └── queryLoop() ───────────────────────────────────────────────┐
          │                                                        │
          │ [pre_prompt hooks]                                     │
          ▼                                                        │
       callModel(messages) ── Anthropic API (SSE) ──→ <-chan StreamEvent
          │                                                        │
          ├── type=text       → accumulate AssistantMessage        │
          ├── type=thinking   → accumulate ThinkingBlock           │
          ├── type=tool_use   → collect ToolUseBlock               │
          └── type=stop                                            │
               │                                                   │
               ├── [post_sampling hooks]                           │
               │                                                   │
               ├── needsFollowUp=false → return Terminal           │
               │                                                   │
               └── needsFollowUp=true                             │
                      │                                            │
                      ▼                                            │
                  [checkAutoCompact] ──→ trigger compact if needed │
                      │                                            │
                      ▼                                            │
                  runTools(toolUseBlocks)                          │
                      │                                            │
                      │ [pre_tool_use hooks]                       │
                      │                                            │
                      ├── BashTool.Call()     → exec command       │
                      ├── FileReadTool.Call() → read file          │
                      ├── FileWriteTool.Call()→ write file         │
                      ├── FileEditTool.Call() → replace string     │
                      ├── GlobTool.Call()     → glob match         │
                      ├── GrepTool.Call()     → search content     │
                      ├── SkillTool.Call()    → inject skill prompt │
                      ├── AgentTool.Call()                         │
                      │       └── query() [new context]            │
                      │                                            │
                      │ [post_tool_use hooks]                      │
                      │                                            │
                      ▼                                            │
                  toolResults → applyBudget → append to messages   │
                      │                                            │
                      └───────────────────────────────────────────┘

ToolUseContext (贯穿全局)
   ├── Messages []Message
   ├── Tools    []Tool
   ├── PermissionContext (allow/deny/ask rules, mode)
   ├── AbortController (chan struct{})
   ├── AppState (mutable: permission mode, usage, etc.)
   ├── HookRegistry *AsyncHookRegistry
   └── FileStateCache (LRU: read file states)

Memory System (cross-session)
   ├── ~/.claude/projects/<slug>/memory/MEMORY.md  ← index (200 lines max)
   └── ~/.claude/projects/<slug>/memory/*.md        ← topic files

Session Memory (within-session, richer detail)
   └── held in AppState, cleared on /clear

Compaction Pipeline
   ① trySessionMemoryCompaction  (structured, no LLM)
   ② microcompactMessages         (prune redundant metadata)
   ③ compactConversation          (LLM summarization)
```

---

## 3c. 四大不变量与学习路径

> 这不是功能列表。这是 Claude Code 作为 agent runtime 的四条核心不变量。
> 任何一条被破坏，系统就从 agent 退化成"勉强还能工作的 prompt middleware"。
> 每个 Phase 的目标是理解并实现**守住某条不变量的最小代码**。

### 不变量 I：轨迹拓扑不能破
**规则**：每个 `tool_use` 块必须有唯一对应的 `tool_result`，且顺序不能乱。
API 只接受合法交替结构：`user → assistant → user → ...`。
任何中断（abort、crash、超时）都必须合成补全结构后才能继续。

**违反后果**：API 返回 `400 invalid message structure`；整个会话无法恢复。

**核心源码**：
```
refs/source/src/query.ts              ← State 枚举 + yieldMissingToolResultBlocks + queryLoop
refs/source/src/utils/queryHelpers.ts ← 轨迹修复助手函数
refs/source/src/utils/conversationRecovery.ts ← 中断后的合成续接
refs/source/src/utils/messages.ts     ← 消息构造器（注：源码泄露不完整，无 src/types/message.ts）
refs/source/src/services/api/claude.ts ← SSE 流式客户端
```

### 不变量 II：缓存前缀不能漂移
**规则**：一旦某个消息前缀被 API 见过，其 cache_control 标记的位置就"命运已定"——
移动它等于破坏前缀，导致缓存 miss，每次请求都重新付费。

**违反后果**：token 成本线性增长；长会话不可持续运行。

**核心源码**：
```
refs/source/src/utils/toolResultStorage.ts          ← ContentReplacementState，"命运已定"机制
refs/source/src/services/api/promptCacheBreakDetection.ts ← 前缀漂移根因诊断
refs/source/src/services/compact/microCompact.ts    ← 本地视图 vs API 前缀视图分离
refs/source/src/services/compact/apiMicrocompact.ts ← API 端微压缩（不移动 cache 标记）
refs/source/src/tools/AgentTool/forkSubagent.ts     ← fork 时复制完整 assistant 消息保持前缀
refs/source/src/tools.ts                            ← 工具注册顺序稳定（builtin-first）
```

### 不变量 III：能力面不能一次性敞开
**规则**：不能把所有工具定义塞进初始 system prompt。
工具定义占 token；更重要的是"工具可见 = 工具可用"——暴露没有授权的工具会误导模型。
能力面必须按需、按条件渐进展开。

**违反后果**：context 被工具定义撑爆；模型误用未授权工具；skill hooks 失效。

**核心源码**：
```
refs/source/src/tools/ToolSearchTool/ToolSearchTool.ts ← 延迟工具发现（deferred tool discovery）
refs/source/src/skills/loadSkillsDir.ts                ← skill 作为路径条件能力单元
refs/source/src/tools/SkillTool/SkillTool.ts           ← skill 分发器
refs/source/src/utils/attachments.ts                   ← 上下文路由（动态 skill 加载）
refs/source/src/constants/systemPromptSections.ts      ← prompt section 注册表 + cache 边界
refs/source/src/context.ts                             ← system prompt 组装 + SYSTEM_PROMPT_DYNAMIC_BOUNDARY
```

### 不变量 IV：连续性不能只寄托在 transcript 上
**规则**：transcript 是易失的（压缩会删除它）。
跨轮次的因果链（parentUuid）、跨会话的记忆（MEMORY.md）、
跨压缩边界的上下文（session memory workcard）必须外化存储。

**违反后果**：压缩后模型失去关键背景；任务恢复依赖模型"猜"而非事实；
长任务自然衰退成单轮问答。

**核心源码**：
```
refs/source/src/utils/sessionStorage.ts                ← transcript 因果链（parentUuid、progressBridge）
refs/source/src/utils/conversationRecovery.ts          ← 会话恢复语义（同 Phase 1）
refs/source/src/memdir/memdir.ts                       ← MEMORY.md（200行/25KB上限）
refs/source/src/memdir/findRelevantMemories.ts         ← 记忆召回（侧边 query）
refs/source/src/services/SessionMemory/sessionMemory.ts ← session memory workcard
refs/source/src/services/compact/sessionMemoryCompact.ts ← 无 LLM 的结构化 memory 更新
refs/source/src/services/compact/compact.ts            ← LLM 全量摘要压缩
```

### Skip 列表（与学习核心架构无关）

```
skip:
  - terminal-ui (Ink/React → bubbletea/lipgloss)
    reason: Go 不用 React 组件树；本次 CLI 用 fmt.Print/bufio.Scanner
  - mcp-integration
    reason: Model Context Protocol 是外部插件协议；独立复杂系统可后续扩展
  - bridge / remote-control
    reason: 远程控制依赖完整 OAuth + polling；与核心 agent 循环解耦
  - oauth-auth
    reason: 用 ANTHROPIC_API_KEY 环境变量
  - buddy / kairos / ultraplan / computer-use / voice / teleport
    reason: 特性功能，不在核心架构路径
  - analytics / telemetry / plugins-marketplace
    reason: 遥测和插件市场，依赖外部基础设施
```

---

## 3d. 阶段拆分（8 个不变量中心 Phase）

> 每个 Phase 围绕一个核心问题（"我在守什么？"），而不是功能列表。
> `src:` 字段指向 `refs/source/src/` 下已验证存在的源文件。

```
Phase 1: 轨迹骨架（不变量 I）
  core-question: tool_use → tool_result 闭合约束如何在流式接收、中断、恢复三种场景下保持？
  goal:     可跑通完整 agent loop：
            go run . -p "echo hello" → Claude 调用 BashTool → 拿到结果 → 回复 → 停止
            中途 Ctrl-C 后重新提问，不报 400
  invariant: 每个 tool_use 必有唯一对应的 tool_result；消息交替结构不能破
  src:
    refs/source/src/query.ts                        ← State 枚举、yieldMissingToolResultBlocks、queryLoop 主体
    refs/source/src/utils/messages.ts               ← 消息构造器（源码泄露中无 src/types/message.ts）
    refs/source/src/services/api/claude.ts          ← SSE 流式客户端
    refs/source/src/utils/queryHelpers.ts           ← 轨迹修复助手函数
    refs/source/src/utils/conversationRecovery.ts   ← 中断后合成续接逻辑
  depends-on: none
  test:     TestTrajectoryClose（mock API → abort 中途 → 验证补全结构后可继续）
            TestQueryLoopBasic（单 tool 调用 → tool_result 追加 → stop）

Phase 2: 工具调度面（不变量 I + III 基础）
  core-question: query loop 如何把"模型想调用 X"翻译成"系统安全执行 X 并拿到结果"？
  goal:     Tool interface 完整；BashTool/FileReadTool/FileWriteTool/FileEditTool/GlobTool/GrepTool/WebFetchTool/WebSearchTool
            全部实现；toolExecution 管道（validate → permission check → execute → result wrap）跑通
  invariant: 工具执行是单向管道，不能绕过验证直接执行
  src:
    refs/source/src/Tool.ts                                     ← Tool interface + buildTool() factory
    refs/source/src/services/tools/toolExecution.ts             ← 完整管道（validate→permission→execute→result）
    refs/source/src/tools.ts                                    ← 注册表，builtin-first 稳定排序
    refs/source/src/tools/BashTool/BashTool.tsx                 ← Bash 执行 + 多层安全检查
    refs/source/src/tools/FileReadTool/FileReadTool.ts          ← 读文件 + 行范围 + LRU 缓存
    refs/source/src/tools/FileWriteTool/FileWriteTool.ts        ← 写文件
    refs/source/src/tools/FileEditTool/FileEditTool.ts          ← 精确字符串替换
    refs/source/src/tools/GlobTool/GlobTool.ts                  ← gitignore 感知的文件名匹配
    refs/source/src/tools/GrepTool/GrepTool.ts                  ← 内容搜索 + 大结果截断
    refs/source/src/tools/WebFetchTool/WebFetchTool.ts          ← HTTP 抓取 + HTML→文本转换
    refs/source/src/tools/WebFetchTool/utils.ts                 ← 抓取工具函数
    refs/source/src/tools/WebFetchTool/preapproved.ts           ← 预批准域名列表
    refs/source/src/tools/WebSearchTool/WebSearchTool.ts        ← 搜索引擎查询 + 结果格式化
  depends-on: Phase 1（消息类型、ToolResultBlock）
  test:     TestToolExecutionPipeline（validate fail → 拒绝执行）
            TestBashTool / TestFileReadTool / TestGlobTool（fixture 验证）
            TestWebFetchTool（mock HTTP → HTML 转文本）
            TestWebSearchTool（mock 搜索 API → 结果格式化）

Phase 3: 缓存前缀稳定性（不变量 II）
  core-question: 为什么大工具结果一旦被 API 见过就不能移动？ContentReplacementState 如何实现"命运已定"？
  goal:     大 tool result 替换为 content-ref；压缩后 API 前缀不漂移；
            验证：连续 10 轮对话的 cache_read_tokens 线性增长而非每次重新计费
  invariant: cache_control 标记位置一旦 API 见过，永不移动
  src:
    refs/source/src/utils/toolResultStorage.ts                       ← ContentReplacementState + getPersistenceThreshold + PERSISTED_OUTPUT_TAG
    refs/source/src/services/api/promptCacheBreakDetection.ts        ← 前缀漂移根因诊断
    refs/source/src/services/compact/microCompact.ts                 ← 本地视图与 API 前缀视图分离
    refs/source/src/services/compact/apiMicrocompact.ts              ← API 端微压缩（不移动 cache 标记）
    refs/source/src/tools/AgentTool/forkSubagent.ts                  ← fork 时复制完整父 assistant 消息保持前缀
    refs/source/src/tools.ts                                         ← 工具注册顺序稳定（同 Phase 2）
  depends-on: Phase 2（ToolResult 产生大输出）
  test:     TestContentReplacement（超阈值 result → 替换为 ref → 原内容可查）
            TestCacheBreakDetection（移动标记 → 检测到漂移）

Phase 4: 能力面管理（不变量 III）
  core-question: 工具定义怎么从"一次性全暴露"变成"按需、按条件渐进展开"？
  goal:     ToolSearchTool 工作（延迟发现工具定义）；
            skill 文件加载后作为路径条件能力单元可用；
            system prompt 组装有 SYSTEM_PROMPT_DYNAMIC_BOUNDARY 标记（cache 边界正确）
  invariant: 暴露的工具 ⊆ 已授权的工具；工具定义不占满初始 context
  src:
    refs/source/src/tools/ToolSearchTool/ToolSearchTool.ts      ← 延迟工具发现
    refs/source/src/skills/loadSkillsDir.ts                     ← skill 作为路径条件能力单元
    refs/source/src/tools/SkillTool/SkillTool.ts                ← skill 分发器
    refs/source/src/utils/attachments.ts                        ← 上下文路由（动态 skill 加载）
    refs/source/src/constants/systemPromptSections.ts           ← prompt section 注册表 + cache 边界常量
    refs/source/src/context.ts                                  ← system prompt 组装 + SYSTEM_PROMPT_DYNAMIC_BOUNDARY
  depends-on: Phase 2（Tool interface）; Phase 3（system prompt 前缀稳定）
  test:     TestToolSearch（deferred tool → search → schema 返回）
            TestSystemPromptBoundary（动态 section 不破坏 cache 前缀）

Phase 5: 连续性外化（不变量 IV）
  core-question: 当 transcript 被截断或压缩，哪些因果链必须外化存储？怎么存？
  goal:     sessionStorage 的 parentUuid 链可追溯；MEMORY.md 跨会话读写可用（200行/25KB截断）；
            压缩后 session memory workcard 保留关键背景；/compact 命令手动触发 LLM 摘要
  invariant: 任务因果链不依赖 transcript 完整性；跨会话关键背景在 MEMORY.md 中持久化
  src:
    refs/source/src/utils/sessionStorage.ts                       ← parentUuid + progressBridge + applySnipRemovals + recoverOrphanedParallelToolResults
    refs/source/src/utils/conversationRecovery.ts                 ← 恢复语义（同 Phase 1）
    refs/source/src/memdir/memdir.ts                              ← MEMORY.md（200行/25KB上限）+ loadMemoryPrompt
    refs/source/src/memdir/findRelevantMemories.ts                ← 侧边 query 召回相关记忆
    refs/source/src/services/SessionMemory/sessionMemory.ts       ← session memory workcard
    refs/source/src/services/compact/sessionMemoryCompact.ts      ← 无 LLM 结构化 memory 更新
    refs/source/src/services/compact/compact.ts                   ← LLM 全量摘要压缩
  depends-on: Phase 1（消息结构）; Phase 4（system prompt 注入 memory）
  test:     TestMemoryLoadTruncation（超限 MEMORY.md → 截断后注入）
            TestSessionMemoryCompact（压缩前后 workcard 保留关键字段）

Phase 6: 策略控制面（不变量 I + III）
  core-question: 权限系统的四层裁决如何在不阻塞 agent 循环的前提下给出明确的 allow/deny/ask？
  goal:     default 模式下危险命令触发 human-in-the-loop；yolo 模式下分类器自动批准；
            DenialTrackingState 防止 AI 反复请求被拒操作死循环；
            hook registry 在 tool 生命周期各点可拦截
  invariant: 权限裁决有四层护盾（policy limits → rule sets → tool.checkPermissions → classifier），缺一不可
  src:
    refs/source/src/utils/permissions/permissions.ts              ← 四层裁决主逻辑
    refs/source/src/utils/permissions/denialTracking.ts           ← DenialTrackingState
    refs/source/src/utils/permissions/bashClassifier.ts           ← bash 安全分类器
    refs/source/src/utils/permissions/yoloClassifier.ts           ← YOLO 模式分类器
    refs/source/src/utils/hooks/AsyncHookRegistry.ts              ← hook 注册表
    refs/source/src/utils/hooks/execAgentHook.ts                  ← agent 类型 hook 执行
    refs/source/src/utils/hooks/execPromptHook.ts                 ← prompt 类型 hook 执行
    refs/source/src/utils/hooks/postSamplingHooks.ts              ← post_sampling hook 执行
  depends-on: Phase 2（Tool.checkPermissions）; Phase 4（工具注册表）
  test:     TestPermissionFourLayers（各层 deny 均能阻止执行）
            TestDenialTracking（同一操作被拒 N 次后标记死循环）

Phase 7: 子代理与恢复图（不变量 I + IV）
  core-question: 子 agent 如何成为父 agent 的"工具调用"而非独立系统？in-process teammate 与 fork subagent 有什么不同？
  goal:     AgentTool 可启动子 agent（独立 transcript）完成后结果返回父 agent；
            in-process runner 有独立压缩 + replacement state reset；
            task substrate 提供注册/GC/SDK bookend；
            TaskCreate/TaskUpdate/TaskList/TaskGet/TaskStop 工具可用
  invariant: 子 agent 是轨迹上的一个 tool_result，不是并发进程；
             in-process teammate 的 replacement state 必须与父 agent 隔离
  src:
    refs/source/src/tools/AgentTool/runAgent.ts                   ← agent 执行 + sidechain transcript + agentType metadata
    refs/source/src/tools/AgentTool/forkSubagent.ts               ← cache-safe fork（同 Phase 3）
    refs/source/src/utils/swarm/inProcessRunner.ts                ← in-process actor loop（独立压缩 + replacement state reset）
    refs/source/src/utils/swarm/permissionSync.ts                 ← leader→worker 权限同步
    refs/source/src/utils/task/framework.ts                       ← task 基础设施（registerTask、evict、GC、SDK bookend）
    refs/source/src/utils/task/TaskOutput.ts                      ← task 输出存储
    refs/source/src/utils/task/diskOutput.ts                      ← 磁盘输出持久化
    refs/source/src/tasks/RemoteAgentTask/RemoteAgentTask.tsx      ← remote agent 生命周期（404 vs 可恢复错误）
    refs/source/src/coordinator/coordinatorMode.ts                ← coordinator 模式（task-notification XML 协议）
    refs/source/src/utils/mailbox.ts                              ← mailbox RPC 协议（SendMessage 工具底层）
    refs/source/src/tools/TaskCreateTool/TaskCreateTool.ts        ← 创建子任务
    refs/source/src/tools/TaskUpdateTool/TaskUpdateTool.ts        ← 更新任务状态
    refs/source/src/tools/TaskListTool/TaskListTool.ts            ← 列出所有任务
    refs/source/src/tools/TaskGetTool/TaskGetTool.ts              ← 获取单个任务详情
    refs/source/src/tools/TaskStopTool/TaskStopTool.ts            ← 停止运行中的任务
    refs/source/src/utils/task/outputFormatting.ts               ← 输出格式化工具
  depends-on: Phase 1（query 函数）; Phase 3（cache-safe fork）; Phase 6（权限继承）
  test:     TestAgentToolCall（父→子→结果汇总）
            TestInProcessRunnerIsolation（子 replacement state 不污染父）
            TestTaskCRUD（create→update→list→get→stop 全流程）

Phase 8: 可观测性 + CLI 组装
  core-question: 如何从外部观测四条不变量是否被守住？QueryEngine 如何把前 7 个 Phase 组装成完整 CLI？
  goal:     完整 CLI 可用：
              go build -o claude-go
              ./claude-go -p "list files and explain what you see"
              ./claude-go  ← 交互式 REPL
            queryProfiler 在调试模式下输出各阶段耗时；
            analyzeContext 输出 token 分布（谁吃掉了 context）
  invariant: 可观测性是验证其他七个不变量的唯一外部手段
  src:
    refs/source/src/utils/queryProfiler.ts                        ← query 管道分阶段性能打点
    refs/source/src/services/api/promptCacheBreakDetection.ts     ← 缓存前缀漂移根因（同 Phase 3）
    refs/source/src/utils/analyzeContext.ts                       ← context 构成分析（token 分布）
    refs/source/src/QueryEngine.ts                                ← 完整会话宿主（1296行）
    refs/source/src/main.tsx                                      ← CLI 入口（Commander.js → Go cobra）
    refs/source/src/entrypoints/agentSdkTypes.ts                  ← SDK 类型定义
  depends-on: Phase 1..7: 全部接口
  test:     集成测试：TestFullSession（多轮对话 + 工具调用 + 压缩 + 记忆注入）
            TestContextAnalysis（token 分布与实际消息列表一致）
```

---

## 3e. 接口定义

> 接口按 8 个 Phase 组织。每个 Phase 只定义该阶段新增的核心类型和函数签名。
> 后续 Phase 直接复用前序 Phase 的类型，不重复声明。

### Phase 1 接口（轨迹骨架）

```go
// ── 消息类型 ──
type Role string
const (
    RoleUser      Role = "user"
    RoleAssistant Role = "assistant"
)

type ContentBlock interface{ BlockType() string }

type TextBlock    struct{ Type, Text string }
type ThinkingBlock struct{ Type, Thinking, Signature string }
type ToolUseBlock  struct {
    Type  string
    ID    string
    Name  string
    Input map[string]any
}
type ToolResultBlock struct {
    Type      string // "tool_result"
    ToolUseID string
    Content   any  // string | []ContentBlock
    IsError   bool
}

// 内部消息（带因果元数据，对应 sessionStorage 中的 parentUuid 链）
type Message struct {
    Role       Role
    Content    []ContentBlock
    UUID       string
    ParentUUID string // 不变量 IV：因果链外化
    IsMeta     bool   // system-injected（skill/memory/hooks）
}

// ── 流式 API 客户端 ──
type Usage struct {
    InputTokens         int
    OutputTokens        int
    CacheCreationTokens int
    CacheReadTokens     int
}

type StreamEvent struct {
    Type  string // message_start|content_block_delta|message_stop|...
    Index int
    Delta *struct{ Type, Text, PartialJSON, Thinking string }
    Usage *Usage
}

type SystemPart struct {
    Type         string
    Text         string
    CacheControl *struct{ Type string } // "ephemeral" — 不变量 II 的边界标记
}

type ToolDefinition struct {
    Name        string
    Description string
    InputSchema any // JSON Schema
}

type CallModelParams struct {
    Messages  []Message
    System    []SystemPart
    Tools     []ToolDefinition
    Model     string
    MaxTokens int
}

func CallModel(ctx context.Context, p CallModelParams) (<-chan StreamEvent, error)

// ── query loop（不变量 I：轨迹拓扑闭合）──
type TerminalReason string
const (
    TerminalCompleted  TerminalReason = "completed"
    TerminalAborted    TerminalReason = "aborted"
    TerminalMaxTurns   TerminalReason = "max_turns"
    TerminalError      TerminalReason = "error"
)

type Terminal struct {
    Reason   TerminalReason
    Messages []Message
    Error    error
}

type QueryParams struct {
    Messages    []Message
    SystemParts []SystemPart
    ToolCtx     *ToolUseContext // Phase 2 完善
    MaxTurns    int
    Model       string
    QuerySource string // "repl_main_thread" | "agent:*" | "sdk"
}

// 主循环：流式 yield Message；最终发一次 Terminal
func Query(ctx context.Context, p QueryParams) (<-chan Message, <-chan Terminal)

// 轨迹修复：中断后合成缺失的 tool_result 使结构合法（对应 yieldMissingToolResultBlocks）
func RepairTrajectory(messages []Message) []Message
```

### Phase 2 接口（工具调度面）

```go
// ── Tool interface + 调度管道 ──
type PermissionBehavior string
const (
    PermAllow PermissionBehavior = "allow"
    PermDeny  PermissionBehavior = "deny"
    PermAsk   PermissionBehavior = "ask"
)

type PermissionDecision struct {
    Behavior PermissionBehavior
    Message  string
}

type ValidationResult struct{ OK bool; Message string }

type ToolCallProgress func(update struct{ Type, Content string })

type ToolResult struct {
    Content     string
    IsError     bool
    NewMessages []Message // skill inline 注入用
}

type Tool interface {
    Name() string
    Description(input map[string]any) string
    InputSchema() any
    Call(ctx context.Context, input map[string]any, tctx *ToolUseContext, onProgress ToolCallProgress) (ToolResult, error)
    ValidateInput(input map[string]any, tctx *ToolUseContext) ValidationResult
    CheckPermissions(input map[string]any, tctx *ToolUseContext) PermissionDecision
    IsReadOnly(input map[string]any) bool
}

// 执行管道：validate → permission → execute → result-wrap（对应 toolExecution.ts）
func ExecuteTool(ctx context.Context, t Tool, input map[string]any, tctx *ToolUseContext) (ToolResult, error)

// builtin-first 稳定排序注册表（不变量 II：工具顺序不能漂移）
type ToolRegistry struct{ /* 私有 */ }
func NewToolRegistry(builtins, extras []Tool) *ToolRegistry
func (r *ToolRegistry) FindByName(name string) (Tool, bool)
func (r *ToolRegistry) All() []Tool
func (r *ToolRegistry) ToDefinitions() []ToolDefinition

// ToolUseContext：贯穿 query loop 的运行时上下文
type ToolUseContext struct {
    Tools          *ToolRegistry
    Permissions    *PermissionContext // Phase 6 完善
    Hooks          *AsyncHookRegistry // Phase 6 完善
    FileCache      *FileStateCache
    DenialState    *DenialTrackingState // Phase 6
    ReplacementState *ContentReplacementState // Phase 3
    AbortCh        <-chan struct{}
    GetAppState    func() *AppState
    SetAppState    func(func(*AppState))
    Depth          int // 子 agent 嵌套深度
}

type AppState struct {
    PermissionMode  string
    SessionID       string
    Usage           Usage
    TotalCostUSD    float64
    IsInterrupted   bool
    CompactBoundary *CompactBoundary // Phase 5
    SessionMemory   string           // Phase 5
}
```

### Phase 3 接口（缓存前缀稳定性）

```go
// ContentReplacementState：不变量 II 的核心实现
// 大 tool result 超过阈值后替换为引用，原内容不再出现在 API 请求中
type ContentReplacementState struct{ /* 私有：content-ref → 原始内容 映射 */ }

func NewContentReplacementState() *ContentReplacementState
// 若 content 超过阈值，存储原始内容并返回 PERSISTED_OUTPUT_TAG 引用
func (s *ContentReplacementState) MaybeReplace(toolUseID, content string) string
// 恢复：content-ref → 原始内容（供本地显示用）
func (s *ContentReplacementState) Restore(ref string) (string, bool)

// 缓存前缀漂移检测（对应 promptCacheBreakDetection.ts）
type CacheBreakEvent struct {
    ExpectedCacheReadTokens int
    ActualCacheReadTokens   int
    SuspectedCause          string
}
func DetectCacheBreak(prev, curr Usage) *CacheBreakEvent

// 微压缩：分离本地视图与 API 前缀视图，不移动 cache_control 标记
type MicroCompactResult struct {
    LocalMessages []Message  // 本地显示用（可裁剪）
    APIMessages   []Message  // 发给 API 的前缀（保持稳定）
}
func MicroCompact(messages []Message, state *ContentReplacementState) MicroCompactResult
```

### Phase 4 接口（能力面管理）

```go
// 系统 prompt 组装（不变量 III：cache 边界稳定）
type SystemPromptSection struct {
    Key          string
    Content      string
    IsDynamic    bool // true → 放在 SYSTEM_PROMPT_DYNAMIC_BOUNDARY 之后
    CacheControl *struct{ Type string }
}

// 按 cache 边界排列 sections，确保静态前缀不被动态内容污染
func BuildSystemPrompt(sections []SystemPromptSection) []SystemPart

// 延迟工具发现（对应 ToolSearchTool：deferred tool → fetch schema → register）
type DeferredTool struct {
    Name        string
    Description string // 短描述（search 时展示）
    // Schema 在 FetchSchema() 后才填充
}
func (d *DeferredTool) FetchSchema(ctx context.Context) (ToolDefinition, error)

// Skill 作为路径条件能力单元
type Skill struct {
    Name        string
    Description string
    Content     string // markdown（SKILL.md 内容）
    Source      string // "bundled" | "local"
    ExecMode    string // "inline" | "fork"
    AllowTools  []string
    Model       string
}

type SkillLoader struct{ Dirs []string }
func (l *SkillLoader) LoadAll() ([]Skill, error)
func (l *SkillLoader) FindByName(name string) (*Skill, bool)

// SkillTool 实现 Tool interface
// inline: 返回 ToolResult.NewMessages（注入 skill 内容为 user message）
// fork:   调用 AgentTool 逻辑（Phase 7）
type SkillTool struct{ loader *SkillLoader; queryFn QueryFn }
```

### Phase 5 接口（连续性外化）

```go
// ── MEMORY.md 跨会话记忆（不变量 IV）──
type MemoryConfig struct {
    MemoryDir string
    MaxLines  int // default 200
    MaxBytes  int // default 25000
}

func LoadMemoryPrompt(cfg MemoryConfig) (string, error)
func EnsureMemoryDirExists(dir string) error

type TruncationResult struct {
    Content          string
    WasLineTruncated bool
    WasByteTruncated bool
}
func TruncateContent(raw string, maxLines, maxBytes int) TruncationResult

// 侧边 query 召回相关记忆（对应 findRelevantMemories.ts）
func FindRelevantMemories(ctx context.Context, query string, memoryDir string, callModel func(CallModelParams) (<-chan StreamEvent, error)) ([]string, error)

// ── Session memory workcard（压缩边界跨越时的连续性保障）──
type CompactBoundary struct {
    MessageID string
    Summary   string
}

type CompactConfig struct {
    ContextThreshold float64 // e.g. 0.80
    MaxTokens        int
}

func ShouldAutoCompact(messages []Message, usage Usage, cfg CompactConfig) bool

// 无 LLM 的结构化 session memory 更新（对应 sessionMemoryCompact.ts）
func UpdateSessionMemory(current string, newFacts []string) string

// LLM 全量摘要压缩
func CompactConversation(
    ctx context.Context,
    messages []Message,
    callModel func(CallModelParams) (<-chan StreamEvent, error),
    model string,
) ([]Message, *CompactBoundary, error)
```

### Phase 6 接口（策略控制面）

```go
// ── 四层权限裁决（不变量 I + III）──
type PermissionMode string
const (
    PermModeDefault PermissionMode = "default"
    PermModePlan    PermissionMode = "plan"   // read-only
    PermModeBypass  PermissionMode = "bypass" // allow all
    PermModeYolo    PermissionMode = "yolo"   // classifier auto-approve
)

type PermissionRule struct{ ToolName, Pattern string }

type PermissionContext struct {
    Mode        PermissionMode
    AlwaysAllow []PermissionRule
    AlwaysDeny  []PermissionRule
    AlwaysAsk   []PermissionRule
}

// DenialTrackingState：防止 AI 反复请求被拒操作死循环
type DenialTrackingState struct{ /* 私有 */ }
func (s *DenialTrackingState) Record(toolName, inputHash string)
func (s *DenialTrackingState) IsDeadLoop(toolName, inputHash string) bool

// 四层裁决主函数（policy limits → rule sets → tool.checkPermissions → classifier）
func EvaluatePermission(
    tool Tool,
    input map[string]any,
    tctx *ToolUseContext,
    askUser func(msg string) bool,
) PermissionDecision

// ── Hook registry（横切不变量）──
type HookType string
const (
    HookPreToolUse   HookType = "pre_tool_use"
    HookPostToolUse  HookType = "post_tool_use"
    HookPrePrompt    HookType = "pre_prompt"
    HookPostSampling HookType = "post_sampling"
)

type HookStatus string
const (
    HookContinue HookStatus = "continue"
    HookStop     HookStatus = "stop"
    HookError    HookStatus = "error"
)

type HookEvent struct {
    Type   HookType
    Tool   Tool
    Input  any
    Output any
}

type HookResult struct {
    Status  HookStatus
    Message string
    Data    any
}

type AsyncHookRegistry struct{ /* 私有 */ }
func NewAsyncHookRegistry() *AsyncHookRegistry
func (r *AsyncHookRegistry) Register(t HookType, fn func(context.Context, HookEvent) HookResult)
func (r *AsyncHookRegistry) Execute(ctx context.Context, e HookEvent) HookResult
```

### Phase 7 接口（子代理与恢复图）

```go
// AgentTool 通过函数注入引用 Query（避免循环依赖）
type QueryFn func(ctx context.Context, p QueryParams) (<-chan Message, <-chan Terminal)

type AgentToolInput struct {
    Prompt      string `json:"prompt"`
    Description string `json:"description,omitempty"`
    Model       string `json:"model,omitempty"`
}

// AgentTool 实现 Tool interface；Call 内部：
//   1. fork parent prefix（cache-safe）
//   2. 创建独立 ContentReplacementState
//   3. 调用 queryFn 取得子 transcript
//   4. 返回子 agent 最终文本作为 ToolResult
type AgentTool struct{ queryFn QueryFn; maxDepth int }

// in-process runner：独立压缩 + replacement state（不变量 II + IV 隔离）
type InProcessRunner struct {
    queryFn          QueryFn
    replacementState *ContentReplacementState // 与父隔离
    compactCfg       CompactConfig
}
func NewInProcessRunner(queryFn QueryFn, cfg CompactConfig) *InProcessRunner
func (r *InProcessRunner) Run(ctx context.Context, p QueryParams) (<-chan Message, <-chan Terminal)

// Task substrate：注册/GC/SDK bookend（对应 utils/task/framework.ts）
type TaskID string
type TaskStatus string
const (
    TaskPending  TaskStatus = "pending"
    TaskRunning  TaskStatus = "running"
    TaskDone     TaskStatus = "done"
    TaskEvicted  TaskStatus = "evicted"
)

type TaskRecord struct {
    ID     TaskID
    Type   string
    Status TaskStatus
}

type TaskFramework struct{ /* 私有 */ }
func NewTaskFramework() *TaskFramework
func (f *TaskFramework) Register(id TaskID, taskType string) error
func (f *TaskFramework) Complete(id TaskID)
func (f *TaskFramework) Evict(id TaskID)
func (f *TaskFramework) GC()

// Mailbox RPC（SendMessage 工具底层，显式 RPC 非共享状态）
type Mailbox struct{ /* 私有 */ }
func (m *Mailbox) Send(toAgentID, content string) error
func (m *Mailbox) Receive(agentID string) (string, bool)
```

### Phase 8 接口（可观测性 + CLI 组装）

```go
// ── Query profiler（分阶段性能打点）──
type ProfileStage string
const (
    StageAPICall     ProfileStage = "api_call"
    StageToolExec    ProfileStage = "tool_exec"
    StageCompaction  ProfileStage = "compaction"
    StagePermission  ProfileStage = "permission"
)

type ProfileEntry struct {
    Stage    ProfileStage
    DurationMs int64
    Meta     map[string]any
}

type QueryProfiler struct{ /* 私有 */ }
func NewQueryProfiler() *QueryProfiler
func (p *QueryProfiler) Mark(stage ProfileStage, meta map[string]any)
func (p *QueryProfiler) Report() []ProfileEntry

// ── Context 构成分析（谁吃掉了 token）──
type ContextBreakdown struct {
    SystemPromptTokens  int
    MemoryTokens        int
    ToolDefinitionTokens int
    ConversationTokens  int
    TotalTokens         int
}
func AnalyzeContext(messages []Message, systemParts []SystemPart) ContextBreakdown

// ── Settings（三层优先级：MDM > user > default）──
type Settings struct {
    Model         string         `json:"model,omitempty"`
    MaxTokens     int            `json:"maxTokens,omitempty"`
    Permissions   PermissionMode `json:"permissions,omitempty"`
    AllowedTools  []string       `json:"allowedTools,omitempty"`
    AutoMemory    *bool          `json:"autoMemoryEnabled,omitempty"`
}
func LoadSettings(configDir string) (*Settings, error)
func SaveSettings(configDir string, s *Settings) error
func DefaultSettings() *Settings

// ── QueryEngine（会话宿主，组装前 7 个 Phase）──
type QueryEngine struct{ /* 私有 */ }

type QueryEngineConfig struct {
    Tools      *ToolRegistry
    Model      string
    MaxTurns   int
    Settings   *Settings
    MemoryDir  string
    WorkDir    string
}

func NewQueryEngine(cfg QueryEngineConfig) (*QueryEngine, error)
// SubmitMessage：发用户消息，流式返回；自动处理 slash 命令、压缩、记忆注入
func (e *QueryEngine) SubmitMessage(ctx context.Context, input string) (<-chan Message, <-chan Terminal)

// ── CLI 入口 ──
type CLIConfig struct {
    Print    string // -p: 非交互模式
    Model    string
    MaxTurns int
    WorkDir  string
    Verbose  bool
}

func RunCLI(cfg CLIConfig) error
// print 模式：SubmitMessage → 打印 → 退出
// REPL 模式：循环读取 → SubmitMessage → 流式打印 → 继续
```

---

## 3f. 项目约定

**目录结构**（每个 Phase 独立子目录）

```
claude-code/
├── src/
│   ├── phase-01-trajectory/   # Phase 1: query loop + SSE client + trajectory repair
│   ├── phase-02-tools/        # Phase 2: Tool interface + core tools + web tools
│   ├── phase-03-cache/        # Phase 3: ContentReplacementState + cache break detection
│   ├── phase-04-capability/   # Phase 4: system prompt assembly + deferred tools + skill loader
│   ├── phase-05-memory/       # Phase 5: MEMORY.md + session memory + compaction
│   ├── phase-06-policy/       # Phase 6: permissions + denial tracking + hooks
│   ├── phase-07-swarm/        # Phase 7: in-process runner + task framework + mailbox + task tools
│   ├── phase-08-engine/       # Phase 8: QueryEngine + settings + CLI + profiler
│   └── integration/           # cross-phase integration tests
├── docs/
│   ├── design.md
│   └── phase-*-guide.md
└── refs/
    ├── source/                # 原 TS 源码（参考）
    └── wiki/                  # DeepWiki 文档
```

**包命名**：每目录一包，小写单词（`cache`、`policy`、`swarm`）。

**语言**：Go 1.22+

**错误处理**：返回 `error`，不 panic。`fmt.Errorf("context: %w", err)` 包装。

**并发**：tool 并发执行用 goroutine + channel。不用 mutex 共享可变状态。

**HTTP 流式**：标准 `net/http` + SSE 手动解析（`data: {...}\n\n`）。

**测试**：table-driven tests，`xxx_test.go`。mock 通过接口注入。

**Compaction 触发**：每轮 tool 完成后检查 → 超 80% 触发 micro-compact → 超 95% 触发 LLM 摘要。

**Skill 执行**：默认 inline；`ExecMode: "fork"` 时启动子 agent（Phase 7）。

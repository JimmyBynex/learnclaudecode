# Skill: learnOpenSource

Use this skill when the user wants to learn and implement an open source project from scratch.

## Invocation

Use this skill when:
- User provides a GitHub project URL and asks to learn or implement it
- User says "带我实现这个项目", "从零学习", "learnOpenSource"
- User wants to understand a project by building it themselves

## Prerequisites

Before starting, ALL of the following must be satisfied. If any are missing, STOP and tell the user what is needed:

- [ ] GitHub project URL provided
- [ ] DeepWiki or documentation URL provided

## Language Rules

**Step 1: Identify the original language**

Read the source repo and determine the implementation language.

**Step 2: If the original is Go or Python**

Use the original language. No confirmation needed.

**Step 3: If the original is any other language (Rust, TypeScript, C++, Java, etc.)**

Do not assume. Ask the user:

```
这个项目是用 [language] 写的。建议用以下语言重写：

**Go**（推荐）
- 编译型，性能接近原语言
- 标准库强，CLI 工具生态成熟
- 并发模型简单
- 适合：系统工具、CLI、网络服务

**Python**
- 开发速度快，适合快速验证
- 库生态丰富
- 适合：脚本、数据处理、原型

**[其他合理选项，如果有的话]**
- [理由]

你想用哪种语言？
```

Wait for the user's answer. Do not proceed until confirmed.

**Step 4: Record the chosen language in `docs/design.md` (3f)**

All SubAgents use the language recorded there. No SubAgent may use a different language without escalating to the main agent.

**Always skip:** platform compatibility patches, OS-specific adapters — regardless of language choice.

## Output Structure

All outputs live under a single project root `<project-name>/`:

```
<project-name>/
├── src/
│   ├── phase-01-<name>/     ← SubAgent A output: 业务代码（读者手敲）+ 测试代码（SubAgent A 预写，读者运行验证，不手敲）
│   ├── phase-02-<name>/     ← SubAgent A output: 业务代码（读者手敲）+ 测试代码（SubAgent A 预写，读者运行验证，不手敲）
│   ├── ...
│   └── integration/         ← cross-phase integration tests (updated each phase, not hand-typed)
├── docs/
│   ├── design.md            ← Phase 0 design document (confirmed)
│   ├── phase-01-<name>-guide.md   ← SubAgent B output: learning guide
│   ├── phase-02-<name>-guide.md
│   └── ...
└── refs/
    ├── source/              ← cloned original source repo
    └── wiki/                ← cloned wiki (GitHub wiki) or fetched DeepWiki pages
```

---

## The Process

You are the main agent throughout. You execute Phase 0 yourself. For Phase 1 onward, you dispatch SubAgents and reviewers — you do not implement or review yourself.

---

### Step 1 — Clone source and documentation

```bash
git clone <github-url> <project-name>/refs/source

# GitHub wiki is a separate git repo
git clone <github-url>.wiki.git <project-name>/refs/wiki
```

If the wiki clone fails (project has no GitHub wiki), fetch DeepWiki pages instead and save each page as a `.md` file under `refs/wiki/`. DeepWiki is a website and cannot be cloned.

---

### Step 2 — Read everything

Read all of the following before forming any conclusions:
- All source files in `refs/source/`
- All content in `refs/wiki/`
- README, changelogs, any design documents in the repo

Do not begin the design document until you have read everything.

---

### Step 3 — Produce design document

Save to `docs/design.md`. This document is the single source of truth for all SubAgents. Every interface, every phase boundary, every convention comes from here.

**3a. Project summary**
- One-sentence description
- Core problem it solves
- Key architectural decisions and why they were made

**3b. Architecture diagram**

Draw the full system as ASCII. Show:
- All core modules
- Control flow between modules (who calls whom)
- Data flow between modules (what type enters, what type exits)
- External dependencies (database, network, file system)
- Entry points

```
[CLI] → [Parser] → [Router] → [Handler] → [Store]
                                    ↓
                              [HTTP Client] → [External API]
```

**3c. Feature list**

```
core:
  - <feature name>
    what: [what it does for the user]
    why:  [what problem it solves, why it must exist]
    src:  [exact file paths in refs/source/ that implement this]

skip:
  - <feature name>
    reason: [platform adapter / compatibility patch / out of scope]
```

Every core feature must have `src` pointing to actual files. SubAgents use this to extract relevant source code — vague paths block their work.

**3d. Phase breakdown**

The phase list is the execution plan. Every phase in this list becomes one Phase N cycle (Step 6 below). The list is fixed after user confirmation — phases are not added or removed during execution.

Each phase must:
- Have an independently runnable result the user can verify
- Build directly on the previous phase
- Implement one coherent slice of functionality
- Estimated hand-typing time: 1–3 hours

```
Phase 1: <name>
  goal:       [what the user can run after this phase]
  features:   [feature names from 3c]
  depends-on: [none]

Phase 2: <name>
  goal:       [what the user can run after this phase]
  features:   [feature names from 3c]
  depends-on: [Phase 1: FunctionA, TypeB]

Phase N: ...
```

**3e. Interface definitions**

For every phase, list every exported interface it introduces. These are contracts — no SubAgent may deviate from them without escalating to you.

```
Phase 1 interfaces:
  - FunctionName(param Type, param Type) (ReturnType, error)
  - TypeName struct {
      Field Type
    }

Phase 2 interfaces:
  - ...
```

**3f. Project conventions**
- Directory structure
- Package naming
- Error handling pattern (return error vs panic vs sentinel)
- Code style observed in the source

---

### Step 4 — Confirm design document with user

Present the design document section by section. Different sections require different types of confirmation — do not ask the same question for every section.

**Section 1: Project summary and architecture (3a, 3b)**

Type: needs-based confirmation. The user knows what they want to learn, not how to design it.

Present the summary and architecture diagram, then ask:
- "这个项目的核心功能描述符合你的预期吗？"
- "有没有你特别想学的部分，或者特别不感兴趣可以跳过的？"

Do not ask: "你觉得架构图合理吗？" — the user cannot evaluate architecture they haven't learned yet.

**Section 2: Feature list (3c)**

Type: needs-based confirmation. The user decides what they want to build.

For each core feature, explain in one plain sentence what it lets the user do. Then ask:
- "这些功能你都想实现吗？"
- "有没有你觉得不需要的？或者漏掉了你想要的？"

Do not ask about skip features unless the user asks — explaining what was cut confuses beginners.

**Section 3: Phase breakdown (3d)**

Type: needs-based confirmation. The user confirms the learning progression feels right.

For each phase, describe the goal as "完成这个 Phase 后，你能做到：[具体的可运行结果]". Then ask:
- "这个拆分顺序对你来说清晰吗？"
- "每个阶段的目标你觉得可以理解吗？"

Do not ask: "Phase 划分合理吗？" — the user has no basis to judge ordering.

**Section 4: Interface definitions (3e)**

Type: inform only. The user does not evaluate technical contracts.

Say: "以下是各 Phase 之间的接口约定，我已经根据原项目源码确定了这些设计。你不需要判断是否合理——如果实现过程中发现问题，我们会一起调整。"

Show the interfaces briefly. Do not ask for approval. Move on.

**Section 5: Project conventions (3f)**

Type: inform only. The user does not evaluate coding conventions.

Say: "项目使用以下约定，所有代码会按此风格编写。" Show the conventions. Move on.

---

Only after Sections 1–3 are explicitly confirmed by the user: seal `docs/design.md`. Do not modify it after this point without user approval.

Red flag: "The user seems to understand, I'll just continue" — NO. Get explicit confirmation for Sections 1–3.
Red flag: asking "你觉得接口合理吗？" or "你觉得架构设计对吗？" — these are technical decisions the user cannot evaluate. Do not ask.

---

### Step 5 — Prepare context package for Phase N

Before dispatching any SubAgent, assemble the context package for that phase. SubAgents have no access to conversation history — everything they need must be in the package.

**Context package for Phase N contains:**

```
1. Full docs/design.md

2. Phase N specification extracted from design.md:
   - goal
   - features (from 3c, including src file paths)
   - depends-on
   - interface definitions (from 3e)

3. Relevant source code:
   - Read the files listed in the src field of Phase N's features
   - Include only those files — not the entire source repo

4. Previous phases record (Phase 2 onward):
   For each completed Phase 1..N-1:
     - Phase name and goal
     - Interfaces introduced (exact signatures from 3e)
     - Any interface deviations approved during execution (with reason)
   Do NOT paste full implementation code here —
   SubAgent A reads src/ directly; pasting full code bloats the context.
```

Assemble this package fresh for each phase. Do not reuse a previous phase's package.

---

### Step 6 — Execute Phase N (repeat for every phase in the list from 3d)

This step repeats for N = 1, 2, 3, ... in order. Phase N+1 does not begin until Phase N is fully complete.

```
Assemble context package (Step 5)
        ↓
  Dispatch SubAgent A
        ↓
  SubAgent A self-check fails ──→ SubAgent A fixes ──┐
        │                                             │
        │ self-check passes                           │
        ↓                                             ↓
  Main agent verifies output        SubAgent A retries ←─┘
        │
        │ incomplete or wrong
        ↓
  Main agent tells SubAgent A what is missing → SubAgent A fixes
        │
        │ output verified
        ↓
  Dispatch SubAgent B
        ↓
  SubAgent B self-check fails ──→ SubAgent B fixes ──┐
        │                                             │
        │ self-check passes                           │
        ↓                                             ↓
  Main agent verifies output        SubAgent B retries ←─┘
        │
        │ incomplete or wrong
        ↓
  Main agent tells SubAgent B what is missing → SubAgent B fixes
        │
        │ output verified
        ↓
  Impact assessment (Step 7)
        ↓
  Phase N complete → N = N+1, go to Step 5
```

---

#### SubAgent A: Implementation

Dispatch SubAgent A with the context package. SubAgent A writes to `src/phase-N-<name>/`.

**What SubAgent A must produce:**
- Complete, compilable code in `src/phase-N-<name>/`
- No placeholders (`TODO`, `TBD`, `implement later`, `similar to above`, `...`)
- Every exported function/type matches interface definitions in context package exactly
- Unit tests in `src/phase-N-<name>/` covering normal, boundary, error cases — these are verification tools, NOT for the reader to hand-type
- Updated integration tests in `src/integration/` — also not for hand-typing
- Actual test output attached

Note: test files are pre-written scaffolding. The reader hand-types only business code, then runs `go test ./...` against these pre-written tests to verify correctness.

**How to handle cross-phase calls:**
- Read actual source in `src/phase-*/` before calling any previous phase function
- If a previous phase interface must change: STOP, report the specific conflict to main agent — do not modify previous phase code

**SubAgent A self-check (must pass before reporting back):**
- [ ] `src/phase-N-<name>/` directory exists and contains all required files
- [ ] Code compiles without errors — attach output
- [ ] All unit tests pass — attach actual output
- [ ] All integration tests pass — attach actual output
- [ ] No placeholders anywhere in produced files
- [ ] Every exported interface matches `docs/design.md` (3e) exactly
- [ ] No previous phase files modified

If any item fails: fix it before reporting back. Do not report back with a failing checklist.

**SubAgent A reports back to main agent:**
- List of files created
- Unit test output (actual)
- Integration test output (actual)
- Self-check result: all passed
- Any interface conflicts (if any)

**Main agent responsibilities for SubAgent A:**

Step 1: Dispatch SubAgent A with the context package.

Step 2: Receive SubAgent A's report. Verify:
- `src/phase-N-<name>/` exists and contains all expected files
- Unit test output is attached and shows all passing
- Integration test output is attached and shows all passing
- Self-check result is "all passed"

Step 3: If anything is missing or wrong:
- Tell SubAgent A specifically what is incomplete (e.g. "integration test output is missing", "src/phase-N-<name>/handler.go was not created")
- Dispatch SubAgent A again with the same context package + the list of what is missing
- Repeat until output is complete

Step 4: Once output is verified, proceed to dispatch SubAgent B.

Main agent must NOT edit any file in `src/` directly — even a one-line fix goes through SubAgent A.

---

#### SubAgent B: Guide

Dispatch SubAgent B only after SubAgent A's output is verified. SubAgent B receives: context package + SubAgent A's approved code.

SubAgent B writes `docs/phase-N-<name>-guide.md`.

**The guide must establish a complete mental model before showing any code.**

**Section 1: Global picture (before any code)**

- **What this phase adds** — one paragraph, plain language
- **Where it sits in the architecture** — reproduce relevant portion of architecture diagram from `docs/design.md` (3b), mark this phase's position
- **Control flow** — ASCII diagram required:
  ```
  caller → FunctionA(InputType) → FunctionB → Store.Save()
                  ↓
            FunctionC → ExternalAPI.Post()
  ```
- **Data flow** — concrete types required:
  ```
  entry:       Request{Method, URL, Headers}
  after parse: Command{Action, Args, Flags}
  after exec:  Result{Output string, Err error}
  ```
- **Connection to previous phases** — re-explain each Phase N-1 interface called here; do not say "see Phase N-1"

**Section 2: Implementation walkthrough**

Walk through in beginner-natural order:

1. **Data structures** — why these fields? what alternatives?
2. **Core logic** — step by step
3. **Wiring** — how it connects to previous phases
4. **Error paths** — what fails, where, how handled

For subsections 1–4, follow this exact format:
```
[概念讲解：为什么这样做，有什么替代方案]

【现在手敲】
[完整业务代码块，从 SubAgent A 输出中原样复制]

【验证】
运行：<exact command>
期望输出：
<exact expected output>
```

Every subsection (1–4) must have `【现在手敲】` and `【验证】`. No exceptions.

**Tests subsection — do NOT include 【现在手敲】:**

Tests are pre-written by SubAgent A and placed in `src/phase-N-<name>/`. The reader does not hand-type tests. End Section 2 with a standalone verification block:

```
## 验证你的实现

SubAgent A 已预写好测试，直接运行：

【验证】
运行：go test ./src/phase-N-<name>/...
期望输出：
ok  	<package>	Xs

如果全部 PASS，本 Phase 完成。
```

**SubAgent B self-check (must pass before reporting back):**
- [ ] Section 1 appears before any code
- [ ] Control flow and data flow diagrams present with concrete types
- [ ] Every previous phase interface re-explained inline
- [ ] Subsections 1–4 each have `【现在手敲】` and `【验证】` with exact expected output
- [ ] Section 2 ends with a standalone `## 验证你的实现` block (no 【现在手敲】 — tests are pre-written)
- [ ] Every business code block exactly matches SubAgent A's output — no simplifications
- [ ] No test files appear in any `【现在手敲】` block
- [ ] No "similar to above", "as before", "following the same pattern"

If any item fails: fix it before reporting back.

**SubAgent B reports back to main agent:**
- Guide file path
- Self-check result: all passed
- Any code errors discovered while writing (escalate — do not fix silently)

**Main agent responsibilities for SubAgent B:**

Step 1: Dispatch SubAgent B with context package + SubAgent A's verified code.

Step 2: Receive SubAgent B's report. Verify:
- `docs/phase-N-<name>-guide.md` exists
- Guide contains `【现在手敲】` and `【验证】` markers in every subsection
- Self-check result is "all passed"
- No code errors were escalated

Step 3: If anything is missing or wrong:
- Tell SubAgent B specifically what is incomplete (e.g. "Section 2.3 missing 【验证】", "data flow diagram absent")
- Dispatch SubAgent B again with the same context + list of what is missing
- Repeat until output is complete

Step 4: Once output is verified, proceed to Step 7 (impact assessment).

Main agent must NOT edit guide documents directly — all changes go through SubAgent B.

---

### Step 7 — Impact assessment after any rework

Run this step whenever SubAgent A's code changes after initial approval (due to rework in a later phase or a code error found by SubAgent B).

**Classify the change:**

```
Interface changed (function signature, type definition, package boundary)
  → check docs/design.md (3d) depends-on for all phases M where M > N
  → add every Phase M that depends-on any changed interface to re-review list

Implementation changed, interface unchanged
  → no static impact, proceed to integration tests
```

**Run full integration tests:**

```bash
go test ./src/integration/...
```

```
All pass → re-review list is final (only static impact from above)
Any fail → identify which phases the failing tests exercise
         → add those phases to re-review list
```

**Execute re-review list (in phase order):**

```
List is empty
  → proceed to Phase N+1

List is not empty
  → for each Phase M in the list:
      Dispatch SubAgent A for src/phase-M-<name>/ with the changed interface info
      SubAgent A self-checks and fixes, then reports back
      Main agent verifies output
      Dispatch SubAgent B for docs/phase-M-<name>-guide.md with updated code
      SubAgent B self-checks and fixes, then reports back
      Re-run integration tests after each rework
  → proceed to Phase N+1
```

`depends-on` identifies candidates. Integration tests confirm actual breakage. Both are required.

---

### Step 8 — Final assembly

Once all phases are APPROVED:

1. Build the full project from `src/` root — attach actual build output
2. Run all tests — attach actual output
3. Produce `docs/handbook.md` with the following structure:

```markdown
# [Project Name] 学习手册

## 项目概述
[从 docs/design.md 3a 复制，不改写]

## 架构图
[从 docs/design.md 3b 复制]

## 学习路线
| Phase | 目标 | 指南 |
|-------|------|------|
| Phase 1: <name> | <goal> | [查看](phase-01-<name>-guide.md) |
| Phase 2: <name> | <goal> | [查看](phase-02-<name>-guide.md) |
| ...   |      |      |

## 如何使用本手册
1. 按 Phase 顺序阅读，不要跳跃
2. 每个 Phase 读完 Section 1（心智模型）再开始手敲
3. 每段业务代码手敲完后执行【验证】步骤，通过后再继续
4. 测试代码已预写好，不需要手敲——手敲完业务代码后直接运行 `go test` 验证
5. 遇到问题先查本 Phase 的错误路径章节

## Phase 详细内容
[将每个 phase guide 的完整内容按顺序嵌入，以 --- 分隔]
```

`docs/handbook.md` 是完整内容合并，不是目录索引——读者无需跳转文件即可从头读完。

---

## Prohibited

- Cloning or reading source after Phase 0 is already underway
- Starting Phase N implementation before design document is fully confirmed
- SubAgent A modifying previous phase source files
- SubAgent B starting before SubAgent A is APPROVED
- Guide document code that differs from approved implementation code
- Any placeholder in implementation code
- Cross-phase integration tests that mock previous phase code
- Declaring completion without attaching actual test output
- Main agent summarizing previous phases from memory — always use `docs/design.md` and `src/`
- Main agent directly editing any file in `src/` or `docs/` — all code and guide changes go through SubAgent A or SubAgent B
- Using superpowers:code-reviewer, superpowers:requesting-code-review, or any registered review skill — always use `agents/code-reviewer.md` and `agents/guide-reviewer.md` exclusively

## Completion Criteria

- [ ] Source and wiki cloned to `refs/`
- [ ] Design document confirmed section by section with user
- [ ] Every phase: code in `src/phase-N-*/`, complete and runnable
- [ ] Every phase: tests pass — actual output attached
- [ ] Every phase: guide in `docs/phase-N-*-guide.md`, APPROVED by Guide Reviewer
- [ ] All phases APPROVED by Code Reviewer
- [ ] Full project builds and all tests pass from root
- [ ] `docs/handbook.md` produced

## Common Mistakes

| Mistake | Correct approach |
|---------|-----------------|
| Presenting full design doc for one-shot approval | Confirm section by section, revise as you go |
| Passing full conversation history to SubAgent | Assemble explicit context package |
| SubAgent B starting before SubAgent A approved | Hard dependency — wait for APPROVED |
| Guide says "similar to above" | Write every code block in full |
| Integration tests mock previous phases | Call actual code in `src/phase-*/` |
| SubAgent A silently changes previous phase interface | STOP, escalate to main agent |
| Rework only re-reviews changed phase | Re-review all subsequent phases |

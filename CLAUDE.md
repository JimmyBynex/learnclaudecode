# 项目规则

## Review 规则

代码审查必须使用 `agents/code-reviewer.md` 定义的 Agent。
指南审查必须使用 `agents/guide-reviewer.md` 定义的 Agent。

禁止使用以下任何 skill 或 agent 进行审查：
- superpowers:code-reviewer
- superpowers:requesting-code-review
- superpowers:receiving-code-review
- 任何其他已注册的 review 相关 skill

## 主 Agent 职责边界

主 Agent 只负责编排，不修改任何源代码文件。

禁止：
- 主 Agent 直接编辑 src/ 下的任何文件
- 主 Agent 直接编辑 docs/ 下的指南文档
- Code Reviewer 返回 CHANGES_REQUIRED 后主 Agent 自己动手改

正确做法：
- Code Reviewer 返回 CHANGES_REQUIRED → dispatch SubAgent A
- Guide Reviewer 返回 CHANGES_REQUIRED → dispatch SubAgent B

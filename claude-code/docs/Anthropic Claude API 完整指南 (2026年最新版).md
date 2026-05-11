# Anthropic Claude API 完整指南 (2026年最新版)

本指南旨在详细解析 Anthropic Claude API 的所有核心字段、参数规则、错误处理及高级功能，帮助开发者快速掌握并高效集成 Claude 模型。

---

## 1. 核心端点与请求基础

Claude API 主要通过 **Messages API** 进行交互，支持单轮对话和多轮状态对话。

- **Base URL**: `https://api.anthropic.com/v1`
- **主要端点**: `POST /v1/messages`

### 1.1 必需请求头
| 字段 | 说明 | 示例 |
| :--- | :--- | :--- |
| `x-api-key` | 您的 API 密钥 | `sk-ant-api03-...` |
| `anthropic-version` | API 版本号（目前固定为 `2023-06-01`） | `2023-06-01` |
| `content-type` | 请求内容类型 | `application/json` |

---

## 2. Messages API 请求字段详解 (`POST /v1/messages`)

### 2.1 核心请求参数 (Body Parameters)

| 字段名 | 类型 | 是否必选 | 说明 |
| :--- | :--- | :--- | :--- |
| `model` | `string` | **是** | 指定使用的模型 ID（如 `claude-opus-4-7`, `claude-sonnet-4-6`）。 |
| `messages` | `array` | **是** | 对话消息列表。包含 `role` (user/assistant) 和 `content`。 |
| `max_tokens` | `number` | **是** | 生成的最大 Token 数。注意：不同模型有不同的最大输出限制。 |
| `system` | `string` / `array` | 否 | 系统提示词（System Prompt），用于定义 Claude 的角色和行为。 |
| `stop_sequences` | `array<string>` | 否 | 自定义停止序列。模型遇到这些字符串时会停止生成。 |
| `stream` | `boolean` | 否 | 是否启用流式输出（Server-Sent Events）。默认为 `false`。 |
| `temperature` | `number` | 否 | **(已弃用)** 采样温度，建议使用默认值或通过 Prompt 引导。 |
| `top_p` | `number` | 否 | **(已弃用)** 核采样。 |
| `top_k` | `number` | 否 | **(已弃用)** 仅从前 K 个最可能的 Token 中采样。 |
| `metadata` | `object` | 否 | 包含 `user_id` 等元数据，用于跟踪请求。 |
| `tools` | `array` | 否 | 工具定义列表，用于工具调用（Tool Use）。 |
| `tool_choice` | `object` | 否 | 控制工具调用的模式（`auto`, `any`, `tool`）。 |
| `cache_control` | `object` | 否 | 自动缓存控制，类型通常为 `{"type": "ephemeral"}`。 |
| `thinking` | `object` | 否 | 配置扩展思维（Extended Thinking）功能。 |

### 2.2 消息对象 (`messages` 数组元素)

每个消息对象必须包含：
- **`role`**: 角色，可选 `user` 或 `assistant`。
- **`content`**: 内容，可以是字符串，也可以是内容块数组（Content Blocks）。

#### 内容块类型 (Content Block Types):
1.  **Text Block**: `{"type": "text", "text": "..."}`
2.  **Image Block**: `{"type": "image", "source": {"type": "base64", "media_type": "image/jpeg", "data": "..."}}`
3.  **Document Block**: `{"type": "document", "source": {"type": "base64", "media_type": "application/pdf", "data": "..."}}`
4.  **Tool Use Block**: (仅限响应) `{"type": "tool_use", "id": "...", "name": "...", "input": {...}}`
5.  **Tool Result Block**: `{"type": "tool_result", "tool_use_id": "...", "content": "..."}`

---

## 3. 响应字段详解

### 3.1 成功响应对象 (Message Object)

| 字段名 | 类型 | 说明 |
| :--- | :--- | :--- |
| `id` | `string` | 消息的唯一标识符。 |
| `type` | `string` | 固定为 `"message"`。 |
| `role` | `string` | 固定为 `"assistant"`。 |
| `content` | `array` | 模型生成的内容块数组。 |
| `model` | `string` | 实际使用的模型 ID。 |
| `stop_reason` | `string` | 停止原因：`end_turn`, `max_tokens`, `stop_sequence`, `tool_use`。 |
| `stop_sequence`| `string` | 触发停止的序列（如果有）。 |
| `usage` | `object` | Token 使用统计，包含 `input_tokens`, `output_tokens`, `cache_creation_input_tokens`, `cache_read_input_tokens`。 |

---

## 4. 高级功能与规则

### 4.1 提示词缓存 (Prompt Caching)
Anthropic 提供业界领先的缓存机制，大幅降低重复输入的成本。
- **机制**: 缓存前缀（Prefix Caching）。
- **有效期**: 默认 5 分钟（可付费延长至 1 小时）。
- **费用**: 缓存读取费用仅为基础输入费用的 10%。
- **槽位**: 每个请求最多支持 4 个显式缓存断点。

### 4.2 工具调用 (Tool Use)
- **Client Tools**: 客户端执行，Claude 返回 `tool_use` 块。
- **Server Tools**: Anthropic 服务器执行（如 `web_search`, `code_execution`），直接返回结果。
- **Strict Mode**: 支持 `strict: true` 确保输出严格符合 JSON Schema。

### 4.3 扩展思维 (Extended Thinking)
部分模型（如 Claude 4 系列）支持在输出前进行深度思考。
- **配置**: 通过 `thinking` 参数设置。
- **输出**: 响应中会包含 `thinking` 类型的块。

---

## 5. 速率限制与服务层级 (Rate Limits)

API 限制基于**组织层级**和**模型类别**。

| 层级 | 充值要求 | 月度限额 |
| :--- | :--- | :--- |
| Tier 1 | $5 | $100 |
| Tier 2 | $40 | $500 |
| Tier 3 | $200 | $1,000 |
| Tier 4 | $400 | $200,000 |

> **注意**: 缓存读取的 Token (Cache Read Tokens) 通常**不计入** ITPM (Input Tokens Per Minute) 限制。

---

## 6. 错误处理 (Error Handling)

| HTTP 状态码 | 错误类型 | 处理建议 |
| :--- | :--- | :--- |
| 400 | `invalid_request_error` | 检查请求格式、参数或模型 ID 是否正确。 |
| 401 | `authentication_error` | 检查 API Key 是否有效或过期。 |
| 403 | `permission_error` | 检查是否有权限访问该模型或资源。 |
| 429 | `rate_limit_error` | 触发频率限制，请指数退避重试。 |
| 500 | `api_error` | Anthropic 服务器错误，请稍后重试。 |
| 529 | `overloaded_error` | 服务器负载过高，建议稍后重试。 |

---

## 7. 2026年主流模型概览

| 模型 ID | 上下文窗口 | 最大输出 | 特色 |
| :--- | :--- | :--- | :--- |
| `claude-opus-4-7` | 1M | 128k | 最强推理与代码能力，支持自适应思维。 |
| `claude-sonnet-4-6`| 1M | 64k | 速度与智能的最佳平衡，支持扩展思维。 |
| `claude-haiku-4-5` | 200k | 64k | 极致速度，高性价比，支持扩展思维。 |

---

## 8. 最佳实践建议

1.  **交替对话**: `messages` 数组必须以 `user` 和 `assistant` 角色交替出现。
2.  **预填充 (Prefill)**: 某些旧模型支持在 `messages` 末尾添加一个 `assistant` 角色消息来引导输出（Claude 4 系列部分模型已不再支持此功能，建议使用 `system` 或 `thinking`）。
3.  **使用 SDK**: 官方提供的 Python 和 TypeScript SDK 已内置自动重试、流式处理和类型检查。
4.  **监控消耗**: 始终检查 `usage` 字段以优化 Prompt 结构，特别是利用好 Prompt Caching。

---
*文档更新日期: 2026-04-23*

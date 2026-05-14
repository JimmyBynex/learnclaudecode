# Anthropic Claude API 完整指南 (2026年最新版)

本指南旨在详细解析 Anthropic Claude API 的所有核心字段、参数规则、错误处理及高级功能。特别针对 **Content Block（内容块）** 和 **SSE Stream Events（流式事件）** 进行了深度拆解。

---

## 1. 核心端点与认证基础

Claude API 主要通过 **Messages API** 进行交互，支持单轮和多轮对话。

- **Base URL**: `https://api.anthropic.com/v1`
- **核心端点**: `POST /v1/messages`
- **必需请求头**:
    - `x-api-key`: 您的 API 密钥。
    - `anthropic-version`: 目前固定为 `2023-06-01`。
    - `content-type`: `application/json`。

---

## 2. Content Block (内容块) 深度解析

在 Messages API 中，`messages.content` 和响应中的 `content` 均由 **Content Block** 数组组成。

### 2.1 内容块类型汇总

| 类型 (`type`) | 方向 | 关键字段 | 说明 |
| :--- | :--- | :--- | :--- |
| **`text`** | 输入/输出 | `text` | 纯文本内容。支持缓存。 |
| **`image`** | 输入 | `source` | 图像数据（Base64/URL）。 |
| **`document`** | 输入 | `source` | PDF 或纯文本文档。 |
| **`thinking`** | 输出 | `thinking` | 模型推理过程（Extended Thinking）。 |
| **`tool_use`** | 输出 | `id`, `name`, `input` | 模型请求调用工具。 |
| **`tool_result`** | 输入 | `tool_use_id`, `content` | 客户端返回工具执行结果。 |

---

## 3. SSE 流式事件结构 (`stream: true`)

当开启流式输出时，API 会返回一系列符合 Server-Sent Events (SSE) 标准的事件。每个事件包含 `event` 名称和 `data` (JSON 字符串)。

### 3.1 事件流生命周期
1.  **`message_start`**: 包含初始消息对象（不含内容块）。
2.  **`content_block_start`**: 开始一个新的内容块（如文本或工具调用）。
3.  **`content_block_delta`**: 内容块的增量更新（文本片段、JSON 片段、思考片段）。
4.  **`content_block_stop`**: 内容块结束。
5.  **`message_delta`**: 消息级别的元数据更新（如 `stop_reason` 和 `usage`）。
6.  **`message_stop`**: 整个流结束。

### 3.2 关键事件 JSON 示例

#### 🟦 message_start
```json
event: message_start
data: {
  "type": "message_start",
  "message": {
    "id": "msg_123", "type": "message", "role": "assistant", "model": "claude-opus-4-7",
    "content": [], "stop_reason": null, "stop_sequence": null, "usage": {"input_tokens": 25, "output_tokens": 1}
  }
}
```

#### 🟩 content_block_delta (文本片段)
```json
event: content_block_delta
data: {
  "type": "content_block_delta",
  "index": 0,
  "delta": { "type": "text_delta", "text": "Hello world!" }
}
```

#### 🟨 content_block_delta (工具参数片段)
```json
event: content_block_delta
data: {
  "type": "content_block_delta",
  "index": 1,
  "delta": { "type": "input_json_delta", "partial_json": "{\"loc\"" }
}
```

#### 🧠 content_block_delta (思考过程片段)
```json
event: content_block_delta
data: {
  "type": "content_block_delta",
  "index": 0,
  "delta": { "type": "thinking_delta", "thinking": "Let me think..." }
}
```

#### 🟧 message_delta (结束状态)
```json
event: message_delta
data: {
  "type": "message_delta",
  "delta": { "stop_reason": "end_turn", "stop_sequence": null },
  "usage": { "output_tokens": 150 }
}
```

---

## 4. Messages API 请求参数 (Body)

| 字段名 | 类型 | 说明 |
| :--- | :--- | :--- |
| **`model`** | `string` | 模型 ID（如 `claude-opus-4-7`）。 |
| **`messages`** | `array` | 对话历史，必须以 `user` 和 `assistant` 交替。 |
| **`max_tokens`** | `number` | 最大生成 Token 数。 |
| **`thinking`** | `object` | 配置思维模式。`{"type": "adaptive"}` (推荐)。 |
| **`tools`** | `array` | 工具定义列表。支持 `strict: true` 模式。 |
| **`cache_control`**| `object` | 顶级缓存控制，例如 `{"type": "ephemeral"}`。 |

---

## 5. 2026年核心模型与规则

### 5.1 提示词缓存 (Prompt Caching)
- **槽位**: 每个请求最多 **4 个** 显式缓存断点。
- **计费**: 缓存命中（Cache Hit）的价格仅为正常输入的 **10%**。
- **生命周期**: 默认 **5 分钟**，每次命中自动刷新。

---

## 6. 错误代码处理

| 状态码 | 错误类型 | 处理策略 |
| :--- | :--- | :--- |
| **429** | `rate_limit_error` | 触发频率限制，请指数退避重试。 |
| **529** | `overloaded_error` | 服务器负载过高，建议稍后重试。 |
| **400** | `invalid_request_error` | 参数错误，请检查输入格式。 |

---
*文档更新日期: 2026-04-23*

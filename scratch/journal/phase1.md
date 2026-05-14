# Phase 1 学习复盘

## 整体目标

通过阅读 claude-code 的 Go 实现（phase-01-trajectory），手写一份自己的版本，理解 streaming API 调用 + 多轮工具循环的完整机制。

---

## 开发思路回顾

### 第一步：读懂 queryLoop 的决策结构

最开始的问题是：进入 SSE 流之后，代码到底在做什么决策？

梳理出来的决策层次：
1. **轮次开始前** — 检查 ctx 是否已取消，是则 abort
2. **调用 API** — CallModel 出错直接 error 终止
3. **SSE 事件循环** — 用 labeled for + select 同时监听 ctx 和 evCh
4. **content_block_stop** — 组装 block，推送到 msgCh，若是 tool_use 则立即执行
5. **message_stop** — 提交本轮 messages，决定是结束还是进入下一轮
6. **eventLoop 退出后** — 处理未提交的情况，再根据 aborted/streamErr 决定走哪条路

这个结构一开始看起来绕，是因为两个循环（turn loop + event loop）嵌套，且 commit 和 tool 执行混在 event 处理里。

### 第二步：理解 labeled break 的必要性

Go 里 `break` 默认只跳出最近的 `for/switch/select`。event loop 里的 select 收到退出信号后，要跳出的是外层 for，不是 select 本身，所以必须用 `break eventLoop`。

一开始以为 `break` 会跳到正确的地方，这是认知盲点。

### 第三步：SSE 和 io.Reader 的心智模型

**io.Reader 的本质**：一个游标，每次 Read 从上次停下的地方继续，不会重放。  
`http.Response.Body` 实现了 `io.ReadCloser`，也就是 `io.Reader + io.Closer` 的组合。

**bufio.Scanner**：包装 io.Reader，自动按分隔符（默认 `\n`）切割，每次 `Scan()` 推进游标。Scanner 是有状态的，一旦 EOF 或出错就停止，不能重置。

**SSE 协议格式**：
```
event: content_block_start
data: {...}

event: content_block_delta
data: {...}

```
每个事件之间必有空行。`readSSE` 的 for 循环是累积逻辑：遇到空行且有内容才返回一个事件，否则继续读。

**EOF 的含义**：TCP 连接正常关闭，`scanner.Scan()` 返回 false，`scanner.Err()` 为 nil。这是正常结束，返回 `nil, nil`。  
网络中断时 `scanner.Err()` 非 nil，或者 evCh 在没有 message_stop 的情况下关闭（`!ok`），视为 streamErr。

### 第四步：ctx 传播和 select 的空窗期问题

ctx 取消之后，代码并不会魔法般地停止，必须有地方"检查"它。

最初的困惑：既然 `ev, ok := <-evCh` 会阻塞，把 `case <-ctx.Done()` 放 select 外面也没用——在等待 evCh 的空窗期里，没有任何代码在跑，ctx 取消了也没人理。所以必须把两者放进同一个 select，让 runtime 来仲裁。

结论：**不需要重复检查 ctx 的是那些立即返回的点**（`select { case <-ctx.Done(): ... default: }`），只有在可能长时间阻塞的地方才需要把 ctx.Done() 和真正的 case 并列。

### 第五步：RepairTrajectory 的作用边界

它只做一件事：扫描 messages，找出有 ToolUseBlock 却没有对应 ToolResultBlock 的情况，补一个 "Interrupted by user" 的合成结果。

不修复消息顺序，不修复内容，不做重试。目的是保证发回去的 messages 对 API 来说是合法的（tool_use 必须有对应的 tool_result）。

---

## 犯过的 Bug

### Bug 1：`buildAt` 匹配错误的 blockType

```go
// 写的
case "input_json":

// 应该是
case "tool_use":
```

`contentAccumulator.startBlock` 存的是 API 返回的 `content_block.type`，值是 `"tool_use"`，不是 delta 的 type（`"input_json_delta"`）。把这两个概念混在一起了。

**根因**：没区分清楚"block 的类型"和"delta 的类型"是两个不同字段。

### Bug 2：`executeOneTool` 返回错误的 Type

```go
// 写的
return ToolResultBlock{Type: "tool_use", ...}

// 应该是
return ToolResultBlock{Type: "tool_result", ...}
```

`ToolResultBlock` 发回给 API 时 type 字段必须是 `"tool_result"`，否则 API 拒绝。

**根因**：复制粘贴 ToolUseBlock 的结构时没有改 type 字符串。

### Bug 3：`Query()` 里缺 `defer close`

```go
// 写的
go func() {
    queryLoop(ctx, q, msgCh, termCh)
}()

// 应该是
go func() {
    defer close(msgCh)
    defer close(termCh)
    queryLoop(ctx, q, msgCh, termCh)
}()
```

`for msg := range msgCh` 会永远阻塞，程序没有反应。这是最影响调试的 bug，因为症状是"输入之后完全无响应"，不会报错。

**根因**：channel 的关闭职责没想清楚——生产者负责关闭，消费者只负责读。

### Bug 4：`scanner.Err()` 放在循环内部

```go
// 写的（有问题）
for scanner.Scan() {
    ...
    if err := scanner.Err(); err != nil { // 这里永远是 nil
        return nil, err
    }
}
```

`scanner.Err()` 只在 `Scan()` 返回 false 之后才可能非 nil，放在循环内部没有意义。应该在循环退出后调用。

### Bug 5：CallModel 出错用 `continue` 而不是 `return`

```go
// 写的
if err != nil {
    termCh <- Terminal{Reason: TerminalError, ...}
    continue // 错：还会进入下一轮
}

// 应该是
    return
```

发了 error terminal 之后继续循环，逻辑错乱。

### Bug 6：Ctrl-C 直接退出整个程序

REPL 里最初把 `queryCtx` 派生自程序级 context：

```go
progCtx, progCancel := signal.NotifyContext(...)
queryCtx, stop := context.WithCancel(progCtx)
```

Ctrl-C 取消 `progCtx`，进而取消 `queryCtx`，然后外层判断 `progCtx.Err() != nil` 就 break 退出 REPL。

正确做法：每次 query 给一个独立的 `signal.NotifyContext(context.Background(), ...)`，Ctrl-C 只打断当前 query，不影响 REPL 主循环。

---

## 学到的知识点

| 知识点 | 核心结论 |
|--------|----------|
| labeled break | `break label` 跳出指定的 for/switch，不是跳出最近一层 |
| io.Reader 模型 | 游标语义，不可重放，`resp.Body` 就是一个 ReadCloser |
| bufio.Scanner | 有状态包装器，Scan() 推进游标，Err() 只在 Scan()=false 后有效 |
| SSE 协议 | event+data 对，空行分隔，`[DONE]` 或 EOF 结尾 |
| channel 关闭责任 | 生产者关闭，消费者用 `range` 或 `v, ok` 检测 |
| select 空窗期 | 只有把两个 case 放进同一 select，runtime 才能仲裁 |
| ctx 传播 | ctx 取消不会自动停止代码，必须在阻塞点检查 |
| signal.NotifyContext | 返回的 stop() 必须调用，否则 signal goroutine 泄漏 |
| contentAccumulator | 流式 block 的状态机：startBlock → applyDelta → buildAt |

---

## 好习惯

- 先读懂参考实现的结构，再动手写，而不是边猜边试
- 遇到不理解的地方主动问"为什么"，而不是跳过
- 写完之后自己做 bug 审查，能找出部分问题

## 需要改进的地方

- **类型字段混淆**：block.type 和 delta.type 是两层概念，一开始没区分清楚，导致 Bug 1
- **channel 生命周期**：写 goroutine 时没有第一时间想到 defer close，是心智模型不完整
- **调试方向**：程序无响应时没有第一时间想到 channel 阻塞，走了弯路
- **复制粘贴后没检查**：Bug 2 是典型的复制后忘改字符串，需要养成改完就对照原型检查的习惯
- **错误处理的控制流**：发了 terminal 之后要立刻 return，不能让逻辑继续走，这是固定模式，要记住

---

## 遗留的两个 Fix（phase1 结束前）

1. `query.go` `buildAt`：`case "input_json"` → `case "tool_use"`
2. `query.go` `executeOneTool`：`Type: "tool_use"` → `Type: "tool_result"`

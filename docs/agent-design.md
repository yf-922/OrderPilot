# 订单客服 Agent 设计

## 目标

Agent 将大模型接入已有商品订单业务，而不是让模型直接访问数据库。模型负责理解意图和选择工具，Go 代码负责身份、参数校验、业务规则和副作用。

```text
浏览器 -> Bearer Token -> Agent Controller -> Agent Orchestrator
                                         -> Model Provider (Grok / local demo)
                                         -> Tool Registry
                                            -> Product Service
                                            -> Order Service
                                               -> MySQL / Redis
```

## 工具

| 工具 | 用途 | 副作用 |
| --- | --- | --- |
| `search_products` | 商品搜索和低库存筛选 | 无 |
| `list_my_orders` | 查询当前用户订单 | 无 |
| `get_order_status` | 查询本人订单状态 | 无 |
| `prepare_cancel_order` | 校验订单并生成确认令牌 | 不修改订单 |

真正的取消操作不暴露给模型。用户点击确认后，`POST /agent/confirm` 校验用户、会话、令牌和过期时间，再调用原有订单 Service 完成事务取消与库存恢复。

## 安全设计

- 工具参数使用严格 JSON Schema，禁止额外字段。
- `userID` 不属于工具参数，只能从已验证的 Bearer Token 注入。
- 模型不能执行 SQL、HTTP 请求或任意代码。
- 会话与确认令牌绑定用户；令牌随机生成、5 分钟过期、只使用一次。
- 单轮最多执行 5 次模型推理，单会话最多保留 20 条消息并在 1 小时后清理。
- 上游 Provider 错误只写服务端日志，对客户端返回脱敏错误。
- API Key 只从服务端环境变量读取。

## Provider 与降级

`CompatibleProvider` 使用 OpenAI-compatible Chat Completions 工具调用协议，可通过 `AI_BASE_URL` 和 `AI_MODEL` 切换模型。配置 `XAI_API_KEY` 后默认连接 Grok；无 Key 时使用 `LocalProvider`，使 CI、演示和 Agent 工具测试不依赖外部网络。

本地 Provider 只用于确定性演示和测试，不等同于大模型能力。页面会明确显示“Grok 在线”或“本地演示”。

## 可观测性与评测

每次执行输出结构化 `agent_run` 日志，包含用户、会话、Provider、模型、工具数量、Token 用量和成功状态。HTTP 返回工具名称、参数、结果与耗时，方便面试时展示可解释执行轨迹。

`agent/eval_test.go` 提供意图路由回归集，覆盖订单列表、状态查询、取消确认、商品查询、低库存和提示注入。Service 测试覆盖权限隔离、确认前无副作用、跨用户确认和令牌重放。

## 面试讲解顺序

1. 为什么模型不能直接查库或直接取消订单。
2. Function Calling 的模型请求、工具执行、结果回传和最终回答循环。
3. 用户身份如何从 Token 注入，避免越权查询。
4. Human-in-the-loop 如何保护写操作。
5. 模型不可用时如何降级，以及如何做确定性测试。
6. 如何用轨迹、Token 和离线用例评估正确性、成本与延迟。

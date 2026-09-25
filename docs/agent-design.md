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

`agent/testdata/golden.json` 是固定的 30 题 v1 黄金集：15 条基础题、9 条表达变体、6 条对抗/异常题。每条保留输入、预期工具和参数、最终回答关键词、工具成功与否、是否需人工确认。变更题集时应升级版本号，不能为了提高分数临时改标注。

`TestAgentGoldenEvaluation` 每题重复 5 次，用相同 mock 业务数据重新建立会话，对工具轨迹、参数、身份绑定、结果、确认状态及确认前无副作用自动判分。报告逐次保留通过状态、失败原因、步骤、Token 和耗时，并按难度汇总。CI 强制运行本地确定性回归，失败即阻断。Service 测试另外覆盖跨用户确认和令牌重放。

在仓库根目录运行：

```powershell
go test ./agent -run '^TestAgentGoldenEvaluation$' -count=1 -v
$env:AGENT_EVAL_REPORT_PATH = 'agent-eval-local.json'
go test ./agent -run '^TestAgentGoldenEvaluation$' -count=1 -v
```

配置服务端 Grok Key 后，显式设置 `AGENT_EVAL_LIVE=1`，用**同一题集**运行真实模型评测。它会实际调用付费 API（最多 150 个任务，每个任务可能多轮模型调用）；请先确认预算，并将报告路径设置在仓库外，避免意外提交模型输出。不要将本地 Provider 的 0 Token/极低延迟当作真实模型成本。在线模型答案具有开放性，关键词断言属于保守自动筛查，失败案例应人工复核，再按预先定义的评分点改进标注。当前题集以单工具任务为主，尚未覆盖复杂多工具编排、工具超时注入或线上用户分布，不能据此声称生产效果。

在线冒烟可另外设置 `AGENT_EVAL_CASE=B04` 和 `AGENT_EVAL_REPETITIONS=1`，只执行一题一次；不设置时默认全量 30 题各 5 次。直接运行 `go test` 不会自动读取 `.env`，需要先把 `GROK_API_KEY`、`AI_BASE_URL`、`AI_MODEL` 导入当前终端环境。Docker Compose 则会自动读取根目录 `.env`。第三方兼容网关的模型名和支持的 API 协议应以实际响应验证；本项目当前使用 Chat Completions + Function Calling。

每次在线评测失败不直接改黄金答案，而是先记录到 `agent/testdata/bad_cases.json`，包括来源、失败类别、是否已修复和下一步动作；修复后再把案例固化进黄金集。这样能区分模型随机性、标注问题和产品策略问题。

运行中的 Agent 还提供鉴权接口 `GET /agent/metrics`，返回累计请求成功率、工具调用/失败次数、确认操作数、Token 总量和平均延迟。订单客服页面右侧会刷新这些指标，便于演示和排查；指标保存在进程内，重启后清零，尚未宣称是 Prometheus 持久化监控数据。

## 面试讲解顺序

1. 为什么模型不能直接查库或直接取消订单。
2. Function Calling 的模型请求、工具执行、结果回传和最终回答循环。
3. 用户身份如何从 Token 注入，避免越权查询。
4. Human-in-the-loop 如何保护写操作。
5. 模型不可用时如何降级，以及如何做确定性测试。
6. 如何用轨迹、Token 和离线用例评估正确性、成本与延迟。

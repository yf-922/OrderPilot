# GoWorks Product 面试项目说明

## 一句话介绍

基于 Go + Iris 的商品与订单后端：以 Controller / Service / Repository 分层承载用户、商品和订单业务，使用 MySQL 事务与行级锁保证库存一致性，Redis 通过 Cache-Aside 加速热点商品查询，并用幂等键处理客户端重试。

## 可直接放进简历的 AI 应用岗版本

- 基于 Go + Iris 在商品/订单后端内实现订单客服 Agent；通过 OpenAI-compatible Chat Completions + Function Calling 接入 **Grok 4.5**（配置网关，非声称直连 xAI），模型只负责意图理解和工具选择，业务副作用仍由 Go Service 执行。
- 实现最多 5 轮的多轮工具调用循环：`tool` 结果回传模型后再生成回答；会话按用户隔离，1 小时未更新清理。送模前对工具结果做结构化精简，并按约 32 KB 上下文预算保留最近完整轮次；重复工具调用、连续失败和轮次耗尽会进入结构化人工协助状态。
- 设计 4 个严格 JSON Schema 工具（商品搜索、我的订单、订单状态、取消预检查），`additionalProperties=false`、参数和订单归属在工具层二次校验；一次响应中的多个工具调用按顺序执行，当前关闭并行和流式 Function Calling。
- 对取消订单采用 Human-in-the-loop：模型只能生成绑定用户/会话的 5 分钟单次确认令牌，确认接口再次校验身份、会话、过期时间和订单状态；身份从 Bearer 登录态注入，提示注入和越权不能绕过工具层授权，工具失败会覆盖模型“成功”表述并返回脱敏兜底。
- 建立可回放评测闭环：30 条黄金题每题重复 5 次；30 条 challenge-v3 按类别固定 seed 分层抽样。Grok 4.5 在改动前基线完成 challenge-v3 全部 `30` 题，自动通过 `17/30（56.7%）`，总 `64,906` Token，平均 `2,164` Token/题、平均延迟 `8.77s`、样本 P95 `19.12s`；黄金集单次回归 `20/30`。这些是固定题集的自动评分基线，不冒充改动后效果、线上准确率或 SLO。

## 简历项目展示终稿

**GoWorks Product｜电商订单后端与订单客服 Agent**
**技术栈：** Go、Iris、MySQL、Redis、Function Calling、第三方兼容网关（配置模型标识 `grok-4.5`）

- 面向商品查询、订单查询和取消订单场景，在 Go + Iris 电商后端中实现订单客服 Agent；通过 OpenAI-compatible Chat Completions 接入第三方网关配置的 `grok-4.5` 模型标识，模型负责意图理解和工具选择，业务规则与订单副作用由 Go Service 控制。
- 实现最多 5 轮的 Function Calling 编排；商品工具结果只保留 ID、名称、库存等必要字段与前 20 条，按约 32 KB **字节预算**保留最近完整对话轮次。12 轮、每轮返回 100 个商品的长会话探针中，最大模型输入由约 233 KB 降至约 6.7 KB。
- 设计商品搜索、我的订单、订单状态、取消预检查 4 个严格 JSON Schema 工具；工具层二次校验参数、登录身份和订单归属，检测重复工具调用，区分参数/业务/暂时性错误，避免模型越权或死循环。
- 为取消订单引入 Human-in-the-loop：模型只能生成绑定用户与会话、5 分钟有效的单次确认令牌；确认接口再次校验后才执行取消和库存回滚。用户明确要求人工、投诉，或工具重复/超时/达到轮次上限时返回结构化人工协助信息。
- 用固定 seed 的 30 条 challenge-v3 做同版本迭代，自动通过 **17/30 → 22/30（+5）**；改动后工具路径正确 **27/30**，在选对路径且需调用工具的题目中参数正确 **14/16**。平均延迟 **8.77s → 6.99s**，但样本 P95 **19.12s → 21.62s**，未宣称尾延迟优化；失败仍集中在歧义、多意图和非法参数。

在订单后端使用 MySQL 事务/行级锁防止超卖、`Idempotency-Key` 处理重试、Redis Cache-Aside 缓存商品；提供 Docker Compose、OpenAPI、CI、chaos case、逐题评测报告和 Agent 前端执行轨迹。

**指标口径：** 上述结果均为固定题集单次离线运行。安全题严格自动评分 2/5；另外 3 题因拒绝语句引用禁词被误判，人工确认没有越权工具调用，不能宣称“提示注入拦截率 100%”。网关返回的模型标识不能证明底层是 xAI 官方公开型号。

## 可直接放进简历的后端岗版本

- 使用 Go + Iris 完成用户、商品、订单分层后端，覆盖认证、分页搜索、订单状态机和库存回滚；MySQL 事务内 `SELECT ... FOR UPDATE` 保证并发下单不超卖。
- 订单创建支持与用户、请求参数绑定的 `Idempotency-Key`；同 Key 重试返回原订单，参数冲突返回 `409 Conflict`，避免网络重试重复扣库存。
- Redis Cache-Aside 加速热点商品查询，缓存不可用自动回源 MySQL；加入 Request ID、结构化日志、健康/就绪探针、请求体上限和安全响应头。
- 用单元测试、`-race`、真实 MySQL 并发测试、HTTP 冒烟和 chaos case 建立质量门禁；提供 OpenAPI、Docker Compose、GitHub Actions，并接入订单客服 Agent 作为业务应用层。

## 当前可验证证据

| Gate | 当前结果 |
| --- | --- |
| `go test ./...` | 通过 |
| `go test -race ./...` | 通过 |
| `go vet ./...` / `go build ./...` | 通过 |
| `staticcheck ./...` | 通过 |
| `govulncheck ./...` | 0 个可达漏洞 |
| 真实 MySQL 并发防超卖 | 通过 |
| 幂等键冲突与同 Key 去重 | 通过 |
| HTTP API 冒烟 | 8/8 通过 |
| Docker MySQL / Redis / API 健康检查 | 通过 |

以上是本地验证结果，不等同于线上吞吐或可用性指标。若简历需要写 P95、QPS 或错误率，应额外运行压测并记录命令、并发数、数据规模和环境。

Agent 面试时可展示 `agent/testdata/golden.json`、`agent/testdata/challenge.json` 与逐题报告。解释“固定题集 -> 分层挑战抽样 -> 预定义断言 -> bad case 回流 -> 同题回归”的闭环。已有 Grok 小规模回归，但样本量不足以代表全量准确率；本地规则 Provider 只是回归基线，不写未经验证的提升或消融百分比。

## 面试演示顺序

1. `docker compose up -d --build --wait`，打开 `/health`、`/ready` 和网页入口。
2. 展示商品查询的 Redis Cache-Aside 路径及缓存失效。
3. 并发执行订单测试，解释行级锁、事务回滚和库存不变量。
4. 使用相同 `Idempotency-Key` 重试订单，再使用不同数量复用该 Key，展示成功去重和 `409` 冲突。
5. 打开 [`docs/openapi.yaml`](openapi.yaml) 和 [`docs/architecture.md`](architecture.md) 说明边界与取舍。

## 诚实边界与后续演进

- 当前鉴权为 HMAC Bearer Token，生产环境应接入 OIDC/JWT、密钥轮换和集中式密钥管理。
- 当前迁移由应用启动时兼容补齐；更大规模部署可替换为 Goose/Atlas 等版本化迁移流水线。
- 当前指标以请求日志和健康检查为主，下一步可接 Prometheus/OpenTelemetry、限流和连接池指标。
- Agent 页面现在提供累计运行面板，通过鉴权的 `/agent/metrics` 展示请求成功率、工具失败、Token、平均延迟和确认操作数；进程重启后指标清零。
- 当前本地挑战基线为 `11/30`，用于暴露规则 Provider 对错别字和口语的识别边界；真实 Grok 评测需显式设置 `AGENT_CHALLENGE_LIVE=1`，在线模式默认分层抽样 12 题并受 Token 上限约束。

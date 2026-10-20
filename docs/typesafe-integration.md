# TypeSafe 官方 Jev 接入

> 本页的网络与本机配置记录发生于 2026-10-17，不代表其他部署环境。当前仓库示例默认关闭外部 Jev；三组真实对照尚未证明外部模型的额外效率收益，详见 [评测结论](typesafe-efficiency-v1/findings.md)。

## 最新验证：Clash Verge 专用代理已连通

2026-10-17 显式经 `http://127.0.0.1:7897` 再次测试，官方返回 `jev-1.13.0`，来源 typesafe，耗时 1243ms，输入 863 Token、输出 188 Token，无重试、无主模型调用。中文多意图用例正确返回 search_products 和 search_knowledge 两个工具，确认决策为 NONE。按官方公开单价，输入费用约 0.000036246 美元；实际费用以账单为准。

成功记录：[typesafe-live-smoke.json](typesafe-live-smoke.json)。当时本机验证启用了外部调用，并设置 `TYPESAFE_PROXY_URL=http://127.0.0.1:7897`；代理仅影响 TypeSafe 客户端，Clash 需保持运行。启动时须先将 `.env` 加载到进程环境再启动或重启后端，直接 `go run` 不会自动读取 `.env`。部署到其他主机时需重新配置可用代理或直连网络，不能沿用本机回环地址。个人 `.env` 不随仓库发布。

此前 IP 复查仍遇一次 451，未返回用量，额外预留 25000 Token；本次成功返回 1051 Token，保守累计预算占用更新为 1808566 Token。以上是预算预留合计，不是实际账单用量。下文保留了此前地区限制过程及默认关闭配置，不能将单次成功当作准确率或延迟 P95。

本实现使用原生 `POST https://api.typesafe.ai/v1/systemone`，不是 OpenAI 聊天接口，也不复用主模型的 Key。先前 strict-core4 系列消融仍只代表确定性路由，不能算作官方 Jev 效果。

## 配置

在项目根目录已被 Git 忽略的 `.env` 中设置：

```dotenv
TYPESAFE_API_KEY=填写你自己的Key
TYPESAFE_BASE_URL=https://api.typesafe.ai/v1
TYPESAFE_PROXY_URL=
JEV_MODEL=jev-1.13.0
AGENT_JEV_ENABLED=true
AGENT_JEV_SHADOW=false
JEV_EXTERNAL_ENABLED=false
JEV_TIMEOUT_MS=2000
JEV_MIN_CONFIDENCE=0.7
```

确认部署地区可以正常访问官方服务后，将 `JEV_EXTERNAL_ENABLED` 改为 `true` 并重启服务。未配置独立 Key 或外部开关关闭时仍使用本地规则。不能把模型名改成 `jev-*` 后继续发送到主模型网关。

## 请求与决策

确定性规则先处理明确确认、放弃、越权拒止、已知终态和明确投诉。普通明确查询不新增外部请求。指代、多意图等疑难输入才调用 Jev。

一次请求包含以下独立问题：

- Choice：继续主流程还是转人工。
- Choice：主要意图。
- 五个 Noul：分别判断五项工具是否相关，组合为工具并集。
- 有已验证订单时增加 Choice：从最多 64 个本人订单候选和 NONE 中解析指代。

状态采用字段白名单，含最近两轮简报与滚动摘要；不发送原始确认令牌。模型只返回分类与概率，不执行工具，也不生成取消授权。Go 验证答案类型、候选集合、概率范围和分布；Choice 默认要求置信度至少 0.7。Noul 大于等于 0.8 时纳入工具，处于 0.2 到 0.8 之间则回退，避免误删必要工具。阈值是保守初始配置，尚非中文数据校准结果。

已解析的订单候选必须再次属于当前会话已验证订单，随后进入状态锚点与主模型上下文；取消服务仍执行所有权、状态与二次确认检查。模型不能直接核销令牌。

超时、网络、HTTP 错误、缺失答案、非法概率或低置信度均回退主模型及完整工具候选集，不自动重试。失败和成功分别记录，Provider 返回的实际模型、输入输出 Token 单独记录在 `jev_typesafe_usage` 日志，不冒充主模型用量。

## 2026-10-17 早期失败记录（随后已连通）

独立凭据存在，未打印或写入报告。只向官方地址发出一次原生请求，不调用主模型。收到 HTTP 451，耗时 1109ms，未返回模型答案或 Token usage，因此不能宣称接入已在线验证成功，也不能把该请求消耗认定为零。为该次未知用量保守预留 25000 Token；与此前预算占用 1757515 合计 1782515 Token，远低于既定 1000 万红线。

当时外部开关保持关闭；随后经单独代理连通，成功记录见页首。账号与部署地区可用性需按官方服务要求确认；不要以换模型网关方式冒充官方接入。中间运行日志未纳入正式评测证据。

## 验证命令

本次原生客户端七类离线用例、凭据隔离，以及订单权限、取消二次确认、运行时错误恢复和会话状态的定向回归通过；`go test ./... -run ...` 也完成了全项目编译。另一次较宽的测试正则包含了旧消融全量测试，在 90 秒上限处仍运行 Python Tokenizer 后被超时终止，未完成全量消融，不能称作 `go test ./agent` 全量通过。

离线测试不读取项目 `.env`，也不进行付费调用：

```powershell
go test ./agent -run '^TestJevExternalGateway$|^TestJevCredentialsIsolation$' -count=1
```

明确配置官方凭据和外部开关后，单次收费冒烟：

```powershell
$env:JEV_EXTERNAL_ENABLED='true'
$env:JEV_LIVE_SMOKE='1'
go test -v ./agent -run '^TestJevExternalLiveSmoke$' -count=1 -timeout 20s
```

该命令只运行一项中文多意图用例，不能用来计算准确率或 P95；不会自动重新跑历史消融。运行前需在进程环境加载独立 TYPESAFE 配置。真正的模型评测应在地区访问恢复后另建版本与 Checkpoint。

官方资料：https://docs.typesafe.ai/introduction/quickstart 、https://docs.typesafe.ai/api 、https://docs.typesafe.ai/models 。截至核查日，官方公布每百万输入 Token 0.042 美元，输出免费；实际费用以账号账单为准。

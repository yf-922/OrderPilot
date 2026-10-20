# 测试说明：重点客服 Agent

所有命令在仓库根目录执行。结果索引见 [test_report.md](test_report.md)，指标、分母和原始证据见 [客服性能报告](../docs/customer-service-performance.md)。

## 无付费模型的本地验证

先安装 Go（版本见 `go.mod`）、Python 与 Tokenizer 依赖。在一个终端运行：

```powershell
python -m pip install -r tests/requirements-context.txt
python tokenizer/server.py
```

在另一个干净的 PowerShell 终端运行，不导入含真实 Key 的 `.env`：

```powershell
$env:AI_PROVIDER='local'
$env:JEV_EXTERNAL_ENABLED='false'
$env:AGENT_TOKENIZER_URL='http://127.0.0.1:8091'
$env:AGENT_ABLATION_REPORT_PATH='../docs/local-verification-ablation.md'
go test ./... -count=1 -timeout 5m
go vet ./...
go build ./...
go test -race ./... -count=1 -timeout 5m
```

Windows 的 race 检查需要支持的 C 编译器和 CGO；没有相应环境时不能把未执行当成通过。自动测试不会自动读取 `.env`，但会继承当前终端已有环境。

## 客服测试分类

| 分类 | 入口 | 验证内容 |
| --- | --- | --- |
| 编排与安全回归 | `agent/*_test.go` | 会话隔离、工具参数校验、取消确认、重复操作、业务错误、提示注入 |
| 上下文压缩 | `agent/context_engine_test.go` | 状态、脱水、切分边界和 Token 预算回归 |
| 离线因子消融 | `TestAgentContextAndJevAblation` | 相同 fixture 的策略对照；不代表线上质量 |
| 官方 Jev 客户端 | `agent/jev_external_test.go` | 本地 HTTP 假服务下的协议与回退；真实冒烟需显式开关 |
| 既有线上证据审计 | `TestTypeSafeEfficiencyEvidenceOffline` | 仅读本地 Checkpoint，核验使用量、轨迹与可比性 |
| 本地 HTTP 压测 | `TestAgentLocalHTTPLoad` | 真实 HTTP、鉴权、控制器与本地 Agent，6 万次单轮请求 |

```powershell
go test ./agent -run '^TestTypeSafeEfficiencyEvidenceOffline$' -count=1 -v

$env:AGENT_LOCAL_LOAD='1'
go test ./backend/web/controllers -run '^TestAgentLocalHTTPLoad$' -count=1 -v -timeout 3m
```

压测默认写 `docs/agent-local-load.json`，可用 `AGENT_LOCAL_LOAD_REPORT` 换路径。压测不构造远程模型客户端，不涉及真实订单事务，不代表线上容量。

## 真实依赖集成与页面测试

```powershell
docker compose up -d --build --wait
# 真实 MySQL 并发防超卖和幂等；需核对连接参数。
$env:INTEGRATION_TEST='1'
$env:MYSQL_HOST='127.0.0.1'
$env:MYSQL_DATABASE='product'
go test ./tests -count=1 -v

python tests/api_smoke_test.py
npm ci
npx playwright install chromium
npm run test:e2e
```

HTTP 冒烟覆盖鉴权、商品搜索分页、下单、取消返库及幂等冲突；浏览器测试覆盖页面与客服确认交互。默认后端 `http://localhost:8080`，可通过 `BASE_URL` 调整。启动后端或 Compose 不等于这些测试已执行。CI 入口为 [.github/workflows/api-smoke.yml](../.github/workflows/api-smoke.yml)。

## 付费评测必须显式启用

以下开关可能触发真实模型调用；运行全量测试前应确保未启用：

`AGENT_EVAL_LIVE`、`AGENT_CHALLENGE_LIVE`、`AGENT_CONTEXT_PAIRED_LIVE`、`AGENT_CONTEXT_LIVE`、`AGENT_REDUCED_LIVE`、`AGENT_STRICT_LIVE`、`AGENT_STRICT_PREFLIGHT`、`AGENT_STRICT_RESUME`、`TYPESAFE_EFFICIENCY_LIVE`、`JEV_LIVE_SMOKE`。

线上测试须先确定数据集、Provider、并发、Token 上限和 Checkpoint 版本，不能为了更新 README 重跑。完整历史与压缩组必须使用同源样本；任务完成、工具执行成功、工具选择准确分别统计；包含所有实际调用及回退用量。不要将本地 fixture 输出标记为真实模型实测。

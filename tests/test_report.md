# 测试结果索引

更新日期：2026-10-20。正式性能与质量口径统一见 [客服评测与性能报告](../docs/customer-service-performance.md)。

| 验证 | 本次结果 | 证据 / 说明 |
| --- | --- | --- |
| `go test ./... -count=1 -timeout 5m` | 通过 | 本地 Provider；常驻 Tokenizer；真实 MySQL 集成条件跳过 |
| `go vet ./...` | 通过 | 全项目静态检查 |
| `go build ./...` | 通过 | 全项目构建 |
| `go test -race ./... -count=1 -timeout 5m` | 通过 | 全项目竞态检查，付费测试关闭 |
| 本地 HTTP 并发 | 60,000 次统计请求，0 失败 | [JSON](../docs/agent-local-load.json)，无远程模型、数据库、Redis |
| 历史上下文线上消融 | 保留原始记录，未重复付费调用 | [报告](../docs/strict-core4-v3-resumed/final-resumed-report.md) |
| 官方 Jev 三组实验 | 保留原始记录，未重复付费调用 | [报告](../docs/typesafe-efficiency-v1/findings.md) |

离线消融运行器的统计用于 fixture 回归，不等于真实 Provider 的性能。历史离线输出中曾使用绕过率作为冗余工具收益代理，此口径不用于 README 或简历。生成新的离线报告时请设置 `AGENT_ABLATION_REPORT_PATH` 为独立路径，避免覆盖此索引。

执行方式、付费开关与测试边界见 [测试说明](README.md)。

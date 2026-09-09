# GoWorks Product 面试项目说明

## 一句话介绍

基于 Go + Iris 的商品与订单后端：以 Controller / Service / Repository 分层承载用户、商品和订单业务，使用 MySQL 事务与行级锁保证库存一致性，Redis 通过 Cache-Aside 加速热点商品查询，并用幂等键处理客户端重试。

## 可直接放进简历的项目描述

- 设计并实现 Go + Iris 分层后端，覆盖用户认证、商品分页搜索、订单状态机和库存回滚；通过 MySQL 事务内 `SELECT ... FOR UPDATE` 保证并发下单不会超卖。
- 为订单创建引入 `Idempotency-Key`，将请求参数与用户绑定；重复请求返回同一订单，不同参数复用同一 Key 返回 `409 Conflict`，避免重试造成重复扣库存。
- 使用 Redis Cache-Aside 缓存商品列表，缓存不可用时自动回源 MySQL；补充 Request ID、结构化访问日志、健康/就绪探针、请求体上限和安全响应头。
- 建立从单元测试、竞态检测、真实 MySQL 并发测试到 HTTP 冒烟的质量门禁，并提供 OpenAPI 契约、Docker Compose 和 GitHub Actions。

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

# Backend roadmap

当前仓库已经是一套可运行的商品库存与订单后端，主链路为 `Iris Controller -> Service -> Repository -> MySQL`，商品查询使用 Redis Cache-Aside。

## 已完成

- 用户注册、登录、bcrypt 密码哈希和过期 Bearer Token
- 用户资源归属校验，禁止越权读取和修改
- 商品创建、查询、搜索、分页、更新、删除和 Redis 缓存
- MySQL 事务下单、外键校验、行级锁扣库存和库存非负约束
- `Idempotency-Key` 幂等重试及不同参数的 409 冲突保护
- 订单分页、归属校验、状态流转和取消返库存
- 健康检查、Request ID、结构化日志、安全响应头和请求体大小限制
- Docker Compose、非 root API 容器、数据库兼容迁移和 GitHub Actions CI
- Go 单元/竞态/并发集成测试、Python/PowerShell HTTP 测试、OpenAPI 和架构文档

## 后续可选增强

这些是面向更大生产规模的演进方向，不是当前项目的运行缺陷：

1. 用 Goose、Atlas 或 Flyway 替换启动迁移，建立独立版本化迁移流水线。
2. 接入 JWT/OIDC、密钥轮换、Prometheus、OpenTelemetry 和集中式日志。
3. 为高流量部署增加网关限流、登录失败限流、熔断和连接池指标。
4. 增加库存审计表、订单领域事件、异步消息和 Repository/Controller contract tests。

## 面试演示

运行 `docker compose up -d --build --wait` 后执行 `make integration`，重点讲解订单事务、`SELECT ... FOR UPDATE`、唯一幂等索引、Redis 降级和并发测试设计。

# goworks-product

一个可运行的 Go + Iris 商品订单管理后端示例，包含用户、商品、订单模块、MySQL 持久化、Redis 缓存、HMAC Bearer 鉴权、统一响应、Docker Compose 环境和自动化测试。

## 技术栈

- 后端：Go、Iris
- 数据库：MySQL
- 缓存：Redis
- 测试：Go 单元/集成测试、竞态检测、Python 接口冒烟测试
- CI：GitHub Actions
- 工程化：Docker Compose、健康检查、HMAC Bearer Token、事务设计

## 项目结构

```text
backend/        Web 启动入口、控制器、页面模板和静态资源
common/         MySQL、Redis、请求参数解析和统一返回结构
datamodels/     用户、商品、订单数据模型
repositories/   数据访问层
services/       业务逻辑层
tests/          接口测试用例、冒烟测试脚本和测试报告
.github/        GitHub Actions CI 配置
```

## 已实现功能

- 用户模块：登录校验、新增用户、查询用户、更新用户、删除用户。
- 商品模块：商品列表查询、商品创建、商品更新、Redis 缓存和缓存失效。
- 订单模块：事务下单、行级锁扣库存、幂等请求、库存不足回滚、归属校验、状态机与取消返库。
- 查询能力：商品分页/关键词搜索、用户维度订单分页查询。
- 测试：参数校验、鉴权、幂等、状态流转、取消返库和并发防超卖。
- 可观测性：请求 ID、JSON 访问日志、存活/就绪探针和安全响应头。
- 安全边界：请求体大小限制、受校验的 Request ID、幂等键参数冲突检测和统一 JSON 响应封装。
- Web 演示：登录态贯通的用户、商品、订单管理页面，支持实际下单、确认与取消操作。

## 运行方式

推荐使用 Docker Compose 启动完整依赖：

```bash
docker compose up --build
```

服务启动后访问 `GET /health`，演示账号为 `demo / demo123456`。也可以先复制 `.env.example`，再使用本机 Go 运行。

浏览器演示入口为 [用户页](http://localhost:8080/user)、[商品页](http://localhost:8080/product/all)、[订单页](http://localhost:8080/order)。先在用户页登录，随后可在同一浏览器会话中管理商品和订单。

Windows 本地验证：

```powershell
$env:Path = "C:\Users\admin\Desktop\work\toolchains\go\bin;$env:Path"
$env:GOPROXY = "https://goproxy.cn,direct"
go test ./...
go test -race ./...
go vet ./...
go build ./...
docker compose up -d --build
powershell -ExecutionPolicy Bypass -File tests/integration_test.ps1
```

也可以使用统一入口：

```powershell
make ci
make up
make integration
```

覆盖率文件输出为 `coverage.out`（已加入 `.gitignore`），并发/数据库集成测试默认只在显式设置 `INTEGRATION_TEST=1` 时执行。

项目默认从环境变量读取 MySQL 和 Redis 配置；数据库结构和演示数据位于 `db/schema.sql`、`db/seed.sql`。

```bash
go run ./backend
```

常用环境变量：

```text
MYSQL_HOST
MYSQL_PORT
MYSQL_USER
MYSQL_PASSWORD
MYSQL_DATABASE
REDIS_HOST
REDIS_PORT
```

## 接口自动化测试

启动后端服务后运行：

```bash
python tests/api_smoke_test.py
```

当前测试重点覆盖不依赖固定数据库种子数据的场景：

- 用户登录缺少用户名或密码
- 用户创建缺少必要字段
- 商品更新缺少 ID 或库存格式错误
- 订单创建缺少用户 ID 或商品 ID

详细用例和测试报告见：

- `tests/api_test_cases.md`
- `tests/test_report.md`

## CI 说明

`.github/workflows/api-smoke.yml` 会在 push / pull request 时执行：

- `go mod download`
- `go build ./...`
- `go test ./...`、`go test -race ./...`、`go vet ./...`
- `staticcheck`、`govulncheck`、Go 格式检查和 Python 测试脚本语法检查
- Docker Compose 启动完整环境
- 真实 MySQL 并发测试和 HTTP API 冒烟测试

CI 会执行 Go 构建、单元测试、竞态检查、静态检查、依赖漏洞扫描和 Python 冒烟测试。真实接口测试在隔离的 Docker Compose 环境中执行。

## 设计亮点

- Controller / Service / Repository 分层，业务错误在 service 层收敛。
- 商品列表采用 Redis Cache-Aside，写操作主动失效缓存，Redis 不可用时自动降级到 MySQL。
- 登录返回 HMAC 签名 Bearer Token；生产环境通过 `AUTH_SECRET` 注入密钥。
- 下单在 MySQL 事务内使用 `SELECT ... FOR UPDATE` 锁定商品，原子扣减库存并写入订单；唯一幂等键保证并发重试只创建一次。
- 订单只能由 Token 对应用户查询和操作；取消待处理订单会在事务内恢复库存，已终态订单不能再次流转。
- API 容器使用非 root 用户运行；数据库启动使用指数退避重试，MySQL/Redis/API 均配置健康检查。
- OpenAPI 契约见 `docs/openapi.yaml`，架构与设计取舍见 `docs/architecture.md`。
- 已有数据库升级说明见 `db/migrations/README.md`；应用启动时会自动补齐订单数量和幂等字段。

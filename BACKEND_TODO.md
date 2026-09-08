# 后端待实现清单

这份清单是按你当前项目实际代码整理出来的，目的是帮助你继续做“纯后端学习”。

## 已经实现的后端能力

- 商品列表查询
- 商品更新
- 订单列表查询
- 订单与商品信息关联查询
- 用户登录校验
- 用户新增
- MySQL 配置读取和连接封装

## 已补齐（本次工程化升级）

- `db/schema.sql` 与 `db/seed.sql`：可重复初始化的 MySQL 结构和演示数据
- `docker-compose.yml` 与 `Dockerfile`：API、MySQL、Redis 一键启动
- `GET /health`：容器健康检查
- 登录返回带过期时间的 HMAC Bearer Token
- 商品详情/删除、订单详情/更新/删除 HTTP 入口
- 商品库存、用户账号、订单数量和状态的基础参数校验
- Go 单元测试、`go test -race`、`go vet` CI 门禁

## 还没真正实现完整的商品功能

### 已有底层能力但没有 HTTP 入口

这些方法在 service / repository 已经存在，但前端页面和 controller 没有完整接出来：

- 商品按 ID 查询
- 商品新增
- 商品删除

对应现状：

- `services/product_service.go` 里有 `GetProductById`、`InsertProduct`、`DeleteProductById`
- `repositories/product_repository.go` 里也有对应实现
- 但 `backend/web/controllers/product_controller.go` 目前只暴露了列表和更新

## 还没真正实现完整的订单功能

### 已有底层能力但没有 HTTP 入口

- 订单按 ID 查询
- 订单新增
- 订单更新
- 订单删除

对应现状：

- `services/order_service.go` 和 `repositories/order_repository.go` 已有实现
- `backend/web/controllers/order_controller.go` 目前只有列表展示

这说明订单模块现在更像“只读演示页”，还不是完整 CRUD。

## 用户模块后端还缺的能力

### 缺少用户管理能力

- 用户列表查询
- 用户详情查询
- 用户更新
- 用户删除

当前用户模块只有：

- 登录校验
- 新增用户

如果你想把用户模块练完整，下一步建议优先补：

1. `GET /user/all`
2. `GET /user/{id}`
3. `POST /user/update`
4. `POST /user/delete`

## 通用后端能力还缺什么

### 1. 参数校验不完整

目前很多参数只是简单读取，没有系统校验：

- 商品名是否为空
- 库存是否允许负数
- 用户名长度是否合法
- 密码强度是否合法
- 订单状态是否只允许 0/1/2

### 2. 错误处理不统一

当前大多是直接返回字符串或简单 JSON，缺少统一错误响应结构，例如：

- 错误码
- 错误分类
- 统一 message 格式

### 3. 缺少认证与会话

当前“登录成功”只是返回 JSON，并没有真正建立登录态：

- 没有 session
- 没有 token
- 没有权限校验
- 没有接口鉴权中间件

这意味着现在任何人都可以直接访问业务页面和接口。

### 4. 缺少事务处理

当前 repository 主要是单表或简单查询，后续如果出现“下单 + 扣库存”这类场景，就需要事务。

### 5. 缺少分页、筛选、搜索

当前列表接口都是全量查询：

- 商品列表无分页
- 订单列表无分页
- 无按条件筛选
- 无关键词搜索

这在真实项目里通常不够用。

### 6. 缺少自动化测试

目前没有：

- 单元测试
- repository 测试
- service 测试
- controller 测试

这也是你继续练后端时非常值得补的一块。

### 7. 缺少数据库初始化文档和迁移方案

目前你已经发现用户表缺失，这说明项目还缺：

- 完整建表 SQL
- 初始化数据 SQL
- 迁移脚本或 schema 文档

### 8. 缺少日志与可观测性

目前只有少量错误日志，没有系统化日志内容，例如：

- 请求日志
- SQL 错误上下文
- 业务操作日志

## 最适合你继续练纯后端的实现顺序

如果你现在主要想练后端，我建议按下面顺序补：

1. 先补数据库 schema 文档和初始化 SQL
2. 给商品模块补完整 CRUD 接口
3. 给订单模块补完整 CRUD 接口
4. 给用户模块补列表、详情、删除、更新
5. 给所有写接口补参数校验
6. 加统一错误响应结构
7. 加测试
8. 最后再考虑登录态、权限和分页

## 当前最明显的结构问题

项目里存在一套未接入运行的 `fronted/` 目录，这容易干扰理解。

学习时请以这套真实运行链路为准：

- `backend/main.go`
- `backend/web/controllers`
- `services`
- `repositories`
- `datamodels`
Git practice line
Git add by file practice
Branch practice line

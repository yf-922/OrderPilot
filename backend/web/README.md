# Web layer

`backend/web` 是项目的 Iris Web 层，包含页面模板、静态资源和 Controller。API 与页面共用同一套 Service/Repository，不会绕过业务规则直接访问数据库。

## 运行时目录

- `views/`：服务端 HTML 模板
- `asserts/`：CSS、JavaScript 和图片
- `controllers/`：HTTP 参数解析、鉴权上下文和状态码映射

## 页面入口

| 页面 | 路径 | 能力 |
| --- | --- | --- |
| 用户页 | `GET /user` | 登录、注册、当前用户资料编辑 |
| 商品页 | `GET /product/all` | 商品查看、新增、编辑、删除 |
| 订单页 | `GET /order` | 当前用户订单查询、创建、确认、取消 |

页面登录成功后将 Bearer Token 仅保存到浏览器 `sessionStorage`。商品和订单页通过该 Token 调用受保护 API；页面本身可以匿名打开，订单及其他用户数据不会在 HTML 中预渲染。

## API 入口

- 用户：`POST /user/login`、`POST /user/create`、`GET /user/{id}`、`POST /user/update`、`POST /user/delete`
- 商品：`GET /product/list`、`GET /product/{id}`、`POST /product/create`、`POST /product/update`、`POST /product/delete`
- 订单：`GET /order/list`、`GET /order/{id}`、`POST /order/create`、`POST /order/update`、`POST /order/delete`

完整参数、响应和错误状态码见 [`docs/openapi.yaml`](../../docs/openapi.yaml)。

## 阅读调用链

阅读任意功能时，按下面顺序追踪：

1. `backend/main.go` 的路由和中间件
2. `controllers/*.go` 的请求校验与响应
3. `services/*.go` 的业务规则
4. `repositories/*.go` 的参数化 SQL 和事务
5. `datamodels/*.go` 的数据结构

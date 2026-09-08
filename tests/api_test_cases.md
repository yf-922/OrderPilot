# goworks-product 接口测试用例

## 测试目标

验证当前 Go 后端项目中用户、商品、订单模块的基础路由、参数校验和错误响应是否符合预期，减少后续改动造成的接口回归问题。

## 测试环境

| 项目 | 内容 |
| --- | --- |
| 服务地址 | `http://localhost:8080` |
| 后端框架 | Go + Iris |
| 数据库 | MySQL |
| 缓存 / 连接组件 | Redis |
| 测试方式 | Go 单元/并发测试 + Python/PowerShell 自动化集成测试 |

## 自动化冒烟用例

| 编号 | 模块 | 接口 | 场景 | 输入 | 预期结果 |
| --- | --- | --- | --- | --- | --- |
| API-001 | 用户 | `POST /user/login` | 缺少用户名 | `password=123456` | HTTP 400，返回 `userName is required` |
| API-002 | 用户 | `POST /user/login` | 缺少密码 | `userName=test_user` | HTTP 400，返回 `password is required` |
| API-003 | 用户 | `POST /user/create` | 缺少昵称 | `userName=test_user,password=123456` | HTTP 400，返回 `nickName is required` |
| API-004 | 用户 | `POST /user/create` | 缺少用户名 | `nickName=test user,password=123456` | HTTP 400，返回 `userName is required` |
| API-005 | 商品 | `POST /product/update` | 缺少商品 ID | `productName=test product,productImage=x.png,productUrl=http://example.com,productNum=10` | HTTP 400，返回 `id is required` |
| API-006 | 商品 | `POST /product/update` | 库存格式错误 | `id=1,productName=test product,productImage=x.png,productUrl=http://example.com,productNum=abc` | HTTP 400，返回 `productNum must be int64` |
| API-007 | 订单 | `POST /order/create` | 缺少用户 ID | `productID=1,orderStatus=0` | HTTP 400，返回 `userID is required` |
| API-008 | 订单 | `POST /order/create` | 缺少商品 ID | `userID=1,orderStatus=0` | HTTP 400，返回 `productID is required` |

## 已覆盖的正向流程

| 编号 | 模块 | 场景 | 前置条件 | 预期结果 |
| --- | --- | --- | --- | --- |
| API-009 | 用户 | 登录成功 | Docker seed 中存在 demo 用户 | 返回 `success=true`、用户 ID 和 bearer token |
| API-010 | 商品 | 搜索分页 | Docker seed 中存在商品 | 返回商品列表和 total |
| API-011 | 订单 | 幂等创建与重试 | demo 用户和商品存在 | 两次请求返回同一订单 ID，库存只扣一次 |
| API-012 | 订单 | 取消返库存 | 存在待处理订单 | 状态变为失败，库存恢复，重复取消返回 409 |

## 风险记录

1. 生产环境建议将应用启动迁移替换为独立 migration pipeline，并在发布前备份数据库。
2. 当前项目使用 HMAC bearer token，生产环境必须注入随机高熵 `AUTH_SECRET` 并通过密钥管理系统托管。

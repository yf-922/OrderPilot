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
| 测试方式 | Python 脚本冒烟测试 + 人工正向流程验证 |

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

## 正向流程待补充用例

以下用例依赖稳定的初始化数据，当前先记录为后续补充项：

| 编号 | 模块 | 场景 | 前置条件 | 预期结果 |
| --- | --- | --- | --- | --- |
| API-009 | 用户 | 登录成功 | 数据库存在指定用户名和密码 | 返回 `success=true` 和用户 ID |
| API-010 | 用户 | 新增用户成功 | 用户名未存在 | 返回 `success=true` 和新用户 ID |
| API-011 | 商品 | 商品更新成功 | 数据库存在指定商品 ID | 返回 `success=true` 和更新后的商品信息 |
| API-012 | 订单 | 订单创建成功 | 用户 ID 和商品 ID 均存在 | 返回新订单 ID |

## 风险记录

1. 项目尚未提供完整数据库初始化 SQL，正向流程测试依赖本地数据库状态。
2. 用户登录目前只返回 JSON，暂未形成 session / token 鉴权链路，因此暂不验证鉴权流程。
3. 商品、订单部分 CRUD 已有 service / repository 能力，但 HTTP 接口还不完整，后续可以继续补齐。

# 接口自动化测试说明

本目录用于补充 `goworks-product` 后端项目的轻量级接口自动化测试实践，重点覆盖用户、商品、订单三个模块中已经暴露的 HTTP 接口。

当前项目通过 Docker Compose 自动准备 MySQL、Redis、用户和商品种子数据。测试分成单元、并发集成和 HTTP 集成三类：

1. Go 单元测试：密码、鉴权、请求参数和服务层规则。
2. Go 集成测试：真实 MySQL 事务、并发防超卖和幂等重试。
3. HTTP 集成测试：健康检查、鉴权、搜索分页、订单取消返库和幂等冲突。
4. Agent 测试：工具路由、会话隔离、人工确认、提示注入回归和浏览器端取消返库。

## 运行前提

1. 启动后端服务：

```bash
go run ./backend
```

2. 默认服务地址：

```text
http://localhost:8080
```

3. 执行接口冒烟测试：

```bash
python tests/api_smoke_test.py
npm run test:e2e
```

如需指定服务地址：

```bash
BASE_URL=http://localhost:8080 python tests/api_smoke_test.py
```

## 覆盖范围

当前自动化脚本覆盖以下类型：

- 用户登录缺少用户名或密码
- 用户创建缺少必要字段
- 商品更新缺少 ID 或库存格式错误
- 订单创建缺少用户 ID 或商品 ID

脚本会区分以下结果类型：

- `PASSED`：状态码和响应内容均符合预期。
- `PARAM_ERROR_UNEXPECTED`：接口返回参数错误，但不符合当前用例预期。
- `AUTH_ERROR_UNEXPECTED`：出现未预期的鉴权错误。
- `SERVER_ERROR_UNEXPECTED`：出现服务端错误。
- `ENV_UNAVAILABLE`：服务未启动或网络不可达。
- `ASSERTION_FAILED`：状态码或响应内容断言失败。

## CI 说明

仓库的 `.github/workflows/api-smoke.yml` 会构建完整 Docker Compose 环境，执行真实 MySQL 并发测试和 HTTP 冒烟测试；本地可用 `make integration` 复现。

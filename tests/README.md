# 接口自动化测试说明

本目录用于补充 `goworks-product` 后端项目的轻量级接口自动化测试实践，重点覆盖用户、商品、订单三个模块中已经暴露的 HTTP 接口。

当前项目依赖 MySQL 和 Redis，正向业务流程还需要提前准备用户、商品、订单等测试数据。因此测试分成两类：

1. 自动化冒烟测试：优先覆盖参数校验、错误响应、基础路由可用性，尽量不依赖数据库种子数据。
2. 人工或半自动测试用例：记录需要数据库初始化数据支持的正向流程，例如用户登录成功、商品更新成功、订单创建成功。

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

仓库补充了 `.github/workflows/api-smoke.yml`。由于当前项目缺少可复现的 MySQL/Redis 初始化脚本，CI 阶段先做两件事：

1. 编译 Go 项目，避免基础代码无法构建。
2. 检查 Python 冒烟测试脚本语法，保证测试资产本身可维护。

后续补齐 `schema.sql`、`seed.sql` 和容器化数据库后，可以在 CI 中启动完整服务并运行 `python tests/api_smoke_test.py`。

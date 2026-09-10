# 页面到后端的映射表

这份文件专门帮助你把“前端操作”和“后端代码入口”快速对应起来。

## 商品模块

### 商品列表页

- 页面文件：`backend/web/views/product/view.html`
- 控制器：`backend/web/controllers/product_controller.go`
- Service：`services/product_service.go`
- Repository：`repositories/product_repository.go`

### 实际调用链

- 访问页面：`GET /product/all`
- 控制器方法：`ProductController.GetAll`
- Service 方法：`ProductService.GetProductAll`
- Repository 方法：`ProductManager.SelectAll`

### 商品修改

- 前端脚本：`backend/web/asserts/js/product.js`
- 接口路径：`POST /product/update`
- 控制器方法：`ProductController.PostUpdate`
- Service 方法：`ProductService.UpdateProduct`
- Repository 方法：`ProductManager.Update`

## 订单模块

### 订单列表页

- 页面文件：`backend/web/views/order/view.html`
- 控制器：`backend/web/controllers/order_controller.go`
- Service：`services/order_service.go`
- Repository：`repositories/order_repository.go`

### 实际调用链

- 访问页面：`GET /order`
- 控制器方法：`OrderController.Get`
- Service 方法：`OrderService.ListOrdersForUser`
- Repository 方法：`OrderManagerRepository.ListByUser`

## 用户模块

### 用户页

- 页面文件：`backend/web/views/user/view.html`
- 前端脚本：`backend/web/asserts/js/user.js`
- 控制器：`backend/web/controllers/user_controller.go`
- Service：`services/user_service.go`
- Repository：`repositories/user_repository.go`

### 登录校验

- 接口路径：`POST /user/login`
- 控制器方法：`UserController.PostLogin`
- Service 方法：`UserService.IsPwdSuccess`
- Repository 方法：`UserManagerRepository.Select`

### 新增用户

- 接口路径：`POST /user/create`
- 控制器方法：`UserController.PostCreate`
- Service 方法：`UserService.AddUser`
- Repository 方法：`UserManagerRepository.Insert`

## AI 客服 Agent

- 页面文件：`backend/web/views/agent/view.html`
- 前端脚本：`backend/web/asserts/js/agent.js`
- 控制器：`backend/web/controllers/agent_controller.go`
- 编排层：`agent/service.go`
- 模型适配器：`agent/compatible_provider.go`

### 对话与工具调用

- 对话入口：`POST /agent/chat`
- Agent 根据意图调用商品或订单 Service，用户 ID 由 Bearer Token 注入
- 工具结果返回模型生成最终回答，同时在页面展示调用轨迹

### 取消确认

- 模型只调用 `prepare_cancel_order` 生成短期确认令牌，不直接修改订单
- 用户点击确认后请求 `POST /agent/confirm`
- 后端校验令牌、用户和会话，再调用 `OrderService.CancelOrder` 取消订单并返还库存

## 学后端时的阅读建议

当你想学习某个功能时，不要先盯页面样式。

建议固定按这个顺序读：

1. 路由入口：`backend/main.go`
2. controller：请求参数怎么进来、返回什么
3. service：业务规则放在哪里
4. repository：SQL 是怎么写的
5. datamodel：数据结构长什么样

这条线看熟了，你对“一个后端需求怎么从 HTTP 走到数据库”会更清楚。

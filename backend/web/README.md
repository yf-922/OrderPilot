# 前端结构说明

这个项目的前端是为了配合后端学习准备的“最小可用界面”，重点不是做复杂交互，而是帮你快速验证后端接口。

## 运行时真正生效的前端目录

- `backend/web/views`
- `backend/web/asserts`

`backend/main.go` 里实际注册的就是这两个目录：

- 模板目录：`./backend/web/views`
- 静态资源目录：`./backend/web/asserts`

所以你学习后端时，真正需要关注的前端文件只在这里。

## 建议你怎么理解这套前端

把它当成三层：

1. `views/*.html`
   负责把后端传过来的数据渲染成页面。
2. `asserts/js/*.js`
   负责拦截表单提交、发 AJAX 请求、显示提示信息。
3. `asserts/css/product.css`
   只负责样式，不影响后端业务。

如果你主要学纯后端，优先看下面这些文件：

- `backend/main.go`
- `backend/web/controllers/*.go`
- `services/*.go`
- `repositories/*.go`

前端你只需要知道“表单往哪个接口发请求、接口返回什么 JSON、页面怎么把结果显示出来”。

## 页面与接口对应关系

### 商品页

- 页面：`backend/web/views/product/view.html`
- 脚本：`backend/web/asserts/js/product.js`
- 页面访问路径：`GET /product/all`
- 修改接口：`POST /product/update`

这个页面的作用：

- 展示商品列表
- 点击“编辑”把当前商品信息回填到右侧表单
- 提交表单后异步调用 `/product/update`

### 订单页

- 页面：`backend/web/views/order/view.html`
- 页面访问路径：`GET /order`

这个页面当前只做展示：

- 展示订单基础信息
- 展示订单关联的商品名称、图片和链接

当前没有订单编辑脚本，也没有订单新增/删除页面操作。

### 用户页

- 页面：`backend/web/views/user/view.html`
- 脚本：`backend/web/asserts/js/user.js`
- 页面访问路径：`GET /user`
- 登录接口：`POST /user/login`
- 新增用户接口：`POST /user/create`

这个页面的作用：

- 校验用户名密码
- 新增用户

## 你在学后端时应该重点关心什么

看到一个按钮或表单时，按这个顺序追：

1. HTML 表单的 `action`
2. 对应 controller 方法
3. controller 调用的 service
4. service 调用的 repository
5. repository 最终执行的 SQL

举例：

- 商品修改按钮最终会走到 `/product/update`
- 对应 `ProductController.PostUpdate`
- 再进入 `ProductService.UpdateProduct`
- 再进入 `ProductManager.Update`
- 最后执行 `UPDATE product ...`

## 当前前端设计目标

当前这套前端不是为了“做复杂前端工程”，而是为了：

- 给后端接口一个可视化入口
- 快速手工验证 CRUD 和登录逻辑
- 降低你学习后端时的干扰

如果你以后继续做纯后端学习，这套前端保持“能用、易懂、可验证”就够了，不建议优先投入大量精力重构样式或交互。

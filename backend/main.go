package main

import (
	"context"
	"log"
	"os"
	"product/agent"
	"product/backend/web/controllers"
	"product/common"
	"product/repositories"
	"product/services"
	"time"

	"github.com/kataras/iris/v12"
	"github.com/kataras/iris/v12/mvc"
	"github.com/mediocregopher/radix/v3"
)

// main 是整个 Web 项目的启动入口。
// 主要职责：
// 1. 初始化 Iris 应用和模板系统。
// 2. 注册静态资源与统一错误页。
// 3. 建立 MySQL 连接。
// 4. 初始化 repository、service、controller，并挂载各业务模块路由。
// 5. 在 localhost:8080 启动 HTTP 服务。
func main() {
	app := iris.New()
	app.UseRouter(common.RequestBodyLimit, common.RequestLogger, common.SecurityHeaders)
	app.Get("/health", func(ctx iris.Context) {
		_ = ctx.JSON(common.NewSuccessResult("ok", iris.Map{"service": "product-api"}))
	})
	app.Logger().SetLevel("debug")

	// 注册 HTML 模板目录，并指定共享布局文件。
	// Reload(true) 便于开发阶段修改模板后即时生效。
	template := iris.HTML("./backend/web/views", ".html").Layout("shared/layout.html").Reload(true)
	app.RegisterView(template)

	// 将前端静态资源目录映射到 /assets。
	app.HandleDir("/assets", "./backend/web/asserts")

	// 注册统一错误页处理逻辑。
	// 当出现 404、500 等错误时，页面会渲染 shared/error.html。
	app.OnAnyErrorCode(func(ctx iris.Context) {
		ctx.ViewData("message", ctx.Values().GetStringDefault("message", "page error"))
		ctx.ViewLayout("")
		ctx.View("shared/error.html")
	})

	// 初始化数据库连接。
	startupCtx, startupCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer startupCancel()
	db, err := common.GetMySQLConnWithRetry(startupCtx)
	if err != nil {

		log.Fatal(err)
	}
	if err := common.EnsureSchema(startupCtx, db); err != nil {
		log.Fatal("database migration failed:", err)
	}
	defer db.Close()
	app.Get("/ready", func(ctx iris.Context) {
		if err := db.PingContext(ctx.Request().Context()); err != nil {
			ctx.StatusCode(iris.StatusServiceUnavailable)
			_ = ctx.JSON(common.NewFailResult("database unavailable"))
			return
		}
		_ = ctx.JSON(common.NewSuccessResult("ready", nil))
	})

	// 初始化redis连接
	redisPool, err := common.GetRedisPool()
	if err != nil {
		log.Println("redis unavailable, continuing without cache:", err)
	}
	if redisPool != nil {
		log.Println("redis connected")
		var pingResult string
		if err = redisPool.Do(radix.Cmd(&pingResult, "PING")); err != nil {
			log.Println("redis ping failed, continuing without cache:", err)
		}
		log.Println("redis ping:", pingResult)
		defer redisPool.Close()
	}

	// 创建应用级上下文，后续可用于依赖注入或资源释放控制。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 注册商品模块：
	// repository 负责数据库访问，service 负责业务封装，controller 负责 HTTP 请求处理。
	productRepository := repositories.NewProductManager("product", db)
	productService := services.NewProductService(productRepository, redisPool)
	productParty := app.Party("/product")
	productParty.Use(common.RequireAuth)
	product := mvc.New(productParty)
	product.Register(ctx, productService)
	product.Handle(new(controllers.ProductController))

	// 注册用户模块。
	userRepository := repositories.NewUserRepository("User", db)
	userService := services.NewUserService(userRepository)
	userParty := app.Party("/user")
	user := mvc.New(userParty)
	user.Register(ctx, userService)
	user.Handle(new(controllers.UserController))

	// 注册订单模块。
	orderRepository := repositories.NewOrderManagerRepository("order", db)
	orderService := services.NewOrderService(orderRepository, productService)
	orderParty := app.Party("/order")
	orderParty.Use(common.RequireAuth)
	order := mvc.New(orderParty)
	order.Register(ctx, orderService)
	order.Handle(new(controllers.OrderController))

	// 注册订单客服 Agent。模型仅能调用受控业务工具，写操作需要用户二次确认。
	agentService := agent.NewService(agent.NewProviderFromEnv(), orderService, productService)
	agentParty := app.Party("/agent")
	agentParty.Use(common.RequireAuth)
	agentMVC := mvc.New(agentParty)
	agentMVC.Register(agentService)
	agentMVC.Handle(new(controllers.AgentController))

	// 启动 Web 服务。
	addr := os.Getenv("APP_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	app.Run(iris.Addr(addr))
}

package common

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/kataras/iris/v12"
	"github.com/kataras/iris/v12/httptest"
)

func TestConfigDefaultsAndOverrides(t *testing.T) {
	t.Setenv("MYSQL_HOST", "db.internal")
	t.Setenv("MYSQL_PORT", "3307")
	t.Setenv("MYSQL_USER", "app")
	t.Setenv("MYSQL_PASSWORD", "secret")
	t.Setenv("MYSQL_DATABASE", "product_test")
	config := NewMySQLConfig()
	if config.Host != "db.internal" || config.Port != 3307 || config.Database != "product_test" {
		t.Fatalf("unexpected MySQL config: %+v", config)
	}
	wantDSN := "app:secret@tcp(db.internal:3307)/product_test?charset=utf8mb4&parseTime=true&loc=Local"
	if got := config.DSN(); got != wantDSN {
		t.Fatalf("DSN=%q want=%q", got, wantDSN)
	}
	t.Setenv("REDIS_HOST", "cache.internal")
	t.Setenv("REDIS_PORT", "6380")
	redis := NewRedisConfig()
	if redis.Addr() != "cache.internal:6380" {
		t.Fatalf("Redis address=%q", redis.Addr())
	}
}

func TestMappingHelpers(t *testing.T) {
	product := MapToProduct(map[string]any{"ID": []byte("2"), "productName": "Go", "productNum": int64(8), "productImage": "img", "productUrl": "url"})
	if product.ID != 2 || product.ProductNum != 8 || product.ProductName != "Go" {
		t.Fatalf("unexpected product: %+v", product)
	}
	order := MapToOrder(map[string]any{"ID": "3", "userID": 4, "productId": float64(5), "quantity": int32(2), "idempotencyKey": []byte("key"), "orderStatus": 0})
	if order.ID != 3 || order.UserID != 4 || order.ProductId != 5 || order.Quantity != 2 {
		t.Fatalf("unexpected order: %+v", order)
	}
	user := MapToUser(map[string]any{"ID": 6, "NickName": "Demo", "Username": "demo", "HashPassword": "hash"})
	if user.ID != 6 || user.Username != "demo" {
		t.Fatalf("unexpected user: %+v", user)
	}
	if got := normalizeDBValue([]byte("value")); got != "value" {
		t.Fatalf("normalizeDBValue=%v", got)
	}
}

func TestRequestMiddlewareAndAuth(t *testing.T) {
	app := iris.New()
	app.Use(RequestBodyLimit, RequestLogger, SecurityHeaders)
	app.Get("/public", func(ctx iris.Context) { _ = ctx.JSON(NewSuccessResult("ok", nil)) })
	app.Get("/private", RequireAuth, func(ctx iris.Context) {
		id, _ := AuthenticatedUserID(ctx)
		_ = ctx.JSON(NewSuccessResult("ok", iris.Map{"userId": id}))
	})
	e := httptest.New(t, app)
	e.GET("/public").WithHeader("X-Request-ID", "trace-123").Expect().Status(iris.StatusOK).Header("X-Request-ID").IsEqual("trace-123")
	e.GET("/private").Expect().Status(iris.StatusUnauthorized)
	e.GET("/private").WithHeader("Authorization", "Bearer "+IssueToken(42)).Expect().Status(iris.StatusOK).Body().Contains("\"userId\":42")
}

func TestFormBuilders(t *testing.T) {
	app := iris.New()
	app.Post("/product", func(ctx iris.Context) {
		product, err := BuildProductForCreateFromContext(ctx)
		if err != nil {
			ctx.StatusCode(iris.StatusBadRequest)
			_ = ctx.JSON(NewFailResult(err.Error()))
			return
		}
		_ = ctx.JSON(NewSuccessResult("ok", product))
	})
	app.Post("/user", func(ctx iris.Context) {
		user, err := BuildUserForUpdateFromContext(ctx)
		if err != nil {
			ctx.StatusCode(iris.StatusBadRequest)
			_ = ctx.JSON(NewFailResult(err.Error()))
			return
		}
		_ = ctx.JSON(NewSuccessResult("ok", user))
	})
	app.Post("/order", func(ctx iris.Context) {
		order, err := BuildOrderForCreateFromContext(ctx)
		if err != nil {
			ctx.StatusCode(iris.StatusBadRequest)
			_ = ctx.JSON(NewFailResult(err.Error()))
			return
		}
		_ = ctx.JSON(NewSuccessResult("ok", order))
	})
	e := httptest.New(t, app)
	e.POST("/product").WithFormField("productName", "Go").WithFormField("productImage", "img").WithFormField("productUrl", "url").WithFormField("productNum", "5").Expect().Status(iris.StatusOK).Body().Contains("\"productNum\":5")
	e.POST("/product").WithFormField("productName", "Go").Expect().Status(iris.StatusBadRequest).Body().Contains("productImage is required")
	e.POST("/user").WithFormField("id", "1").WithFormField("nickName", "Demo").WithFormField("userName", "demo").WithFormField("password", "secret123").Expect().Status(iris.StatusOK).Body().Contains("\"userName\":\"demo\"")
	e.POST("/user").WithFormField("id", "1").WithFormField("nickName", "Demo").WithFormField("userName", "demo").WithFormField("password", string(make([]byte, 73))).Expect().Status(iris.StatusBadRequest).Body().Contains("password must be at most 72 bytes")
	e.POST("/order").WithFormField("userID", "1").WithFormField("productID", "2").WithFormField("quantity", "3").WithFormField("orderStatus", "0").Expect().Status(iris.StatusOK).Body().Contains("\"quantity\":3")
	e.POST("/order").WithFormField("userID", "1").WithFormField("productID", "2").WithFormField("quantity", "0").WithFormField("orderStatus", "0").Expect().Status(iris.StatusBadRequest).Body().Contains("quantity must be positive")
}

func TestEnsureSchemaOnCurrentSchema(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT DATABASE()")).WillReturnRows(sqlmock.NewRows([]string{"database"}).AddRow("product"))
	for _, table := range []string{"User", "product", "order"} {
		mock.ExpectQuery("information_schema\\.tables").WithArgs("product", table).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	}
	mock.ExpectQuery("information_schema\\.columns").WithArgs("product", "User", "HashPassword").WillReturnRows(sqlmock.NewRows([]string{"character_maximum_length"}).AddRow(64))
	mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE `User` MODIFY COLUMN `HashPassword` VARCHAR(255) NOT NULL")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("information_schema\\.columns").WithArgs("product", "order", "quantity").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("information_schema\\.columns").WithArgs("product", "order", "idempotencyKey").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectExec("UPDATE `order` SET").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE `order` MODIFY COLUMN `idempotencyKey` VARCHAR(128) NOT NULL")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("information_schema\\.statistics").WithArgs("product", "order", "uk_order_idempotency").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	if err := EnsureSchema(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

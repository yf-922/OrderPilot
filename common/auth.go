package common

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/kataras/iris/v12"
)

// IssueToken creates a small HMAC-signed bearer token without adding a JWT dependency.
// The payload is userID.expiry; it is intentionally easy to inspect during interviews.
func IssueToken(userID int64) string {
	exp := time.Now().Add(24 * time.Hour).Unix()
	payload := fmt.Sprintf("%d.%d", userID, exp)
	mac := hmac.New(sha256.New, []byte(authSecret()))
	mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + sig
}

func UserIDFromToken(token string) (int64, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return 0, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return 0, false
	}
	mac := hmac.New(sha256.New, []byte(authSecret()))
	mac.Write(payload)
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(parts[1])) {
		return 0, false
	}
	values := strings.Split(string(payload), ".")
	if len(values) != 2 {
		return 0, false
	}
	id, err1 := strconv.ParseInt(values[0], 10, 64)
	exp, err2 := strconv.ParseInt(values[1], 10, 64)
	return id, err1 == nil && err2 == nil && id > 0 && time.Now().Unix() < exp
}

func AuthenticatedUserID(ctx iris.Context) (int64, bool) {
	header := ctx.GetHeader("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return 0, false
	}
	return UserIDFromToken(strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")))
}

// RequireAuth protects a route group using the Bearer token issued at login.
func RequireAuth(ctx iris.Context) {
	// 页面入口允许匿名打开，页面加载后由前端使用登录态调用受保护 API。
	// 业务 JSON 接口仍然必须携带 Bearer Token，避免把页面可访问误变成数据可访问。
	if ctx.Method() == "GET" && strings.Contains(ctx.GetHeader("Accept"), "text/html") {
		path := ctx.Path()
		if path == "/product/all" || path == "/order" {
			ctx.Next()
			return
		}
	}
	if _, ok := AuthenticatedUserID(ctx); !ok {
		ctx.StatusCode(iris.StatusUnauthorized)
		_ = ctx.JSON(NewFailResult("authorization required"))
		return
	}
	ctx.Next()
}

func authSecret() string {
	if value := os.Getenv("AUTH_SECRET"); value != "" {
		return value
	}
	return "development-only-secret-change-me"
}

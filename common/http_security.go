package common

import (
	"os"
	"strconv"

	"github.com/kataras/iris/v12"
)

const defaultMaxRequestBodyBytes int64 = 1 << 20

// RequestBodyLimit protects form endpoints from oversized request bodies.
func RequestBodyLimit(ctx iris.Context) {
	limit := defaultMaxRequestBodyBytes
	if value := os.Getenv("MAX_REQUEST_BODY_BYTES"); value != "" {
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	ctx.SetMaxRequestBodySize(limit)
	ctx.Next()
}

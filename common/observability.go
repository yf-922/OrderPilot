package common

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"time"

	"github.com/kataras/iris/v12"
)

// RequestLogger emits one structured JSON record per request and propagates a request ID.
func RequestLogger(ctx iris.Context) {
	requestID := ctx.GetHeader("X-Request-ID")
	if requestID == "" {
		requestID = newRequestID()
	}
	ctx.Header("X-Request-ID", requestID)
	ctx.Values().Set("request_id", requestID)
	started := time.Now()
	ctx.Next()
	record := map[string]any{
		"event": "http_request", "request_id": requestID,
		"method": ctx.Method(), "path": ctx.Path(),
		"status": ctx.GetStatusCode(), "latency_ms": time.Since(started).Milliseconds(),
	}
	if userID, ok := AuthenticatedUserID(ctx); ok {
		record["user_id"] = userID
	}
	encoded, _ := json.Marshal(record)
	log.Print(string(encoded))
}

func SecurityHeaders(ctx iris.Context) {
	ctx.Header("X-Content-Type-Options", "nosniff")
	ctx.Header("X-Frame-Options", "DENY")
	ctx.Header("Referrer-Policy", "no-referrer")
	ctx.Next()
}

func newRequestID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return time.Now().UTC().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(buf)
}

# Verification report

Verified locally on Windows on 2026-09-09 with Go 1.27.1 and Docker Engine 29.6.2.

| Gate | Result |
|---|---|
| `go test ./...` | Passed |
| `go test -race ./...` | Passed |
| `go vet ./...` | Passed |
| `go build ./...` | Passed |
| Docker multi-stage build | Passed |
| MySQL / Redis / API health checks | Passed |
| HTTP authentication and ownership checks | Passed |
| Product search and pagination | Passed |
| Order idempotent retry | Passed |
| Order cancellation and stock restore | Passed |
| Concurrent oversell prevention | Passed |
| Concurrent same-key deduplication | Passed |
| Python API suite | 8/8 passed |
| `staticcheck ./...` | Passed |
| `govulncheck ./...` | 0 reachable vulnerabilities |
| Agent intent evaluation | 9/9 passed, including prompt-injection regression |
| Agent user/conversation isolation | Passed |
| Agent cancellation confirmation and replay protection | Passed |
| Playwright desktop/mobile workflow | Passed |

The concurrency tests run against a real MySQL 8 container. They verify that two 60-unit purchases against stock 100 yield exactly one success and final stock 40, and that simultaneous requests sharing one idempotency key return one order ID and deduct stock once.

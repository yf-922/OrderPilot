SHELL := pwsh.exe
GO := go

.PHONY: fmt test race vet build security integration e2e up down ci coverage coverage-html ablation local-load

fmt:
	$(GO)fmt -w agent backend common datamodels repositories services tests

test:
	$(GO) test ./... -coverprofile=coverage.out

coverage: test
	$(GO) tool cover -func=coverage.out

coverage-html: test
	$(GO) tool cover -html=coverage.out -o coverage.html

ablation:
	$$env:AGENT_ABLATION_REPORT_PATH='../docs/local-verification-ablation.md'; $(GO) test ./agent -run TestAgentContextAndJevAblation -count=1 -v

local-load:
	$$env:AGENT_LOCAL_LOAD='1'; $(GO) test ./backend/web/controllers -run '^TestAgentLocalHTTPLoad$$' -count=1 -v -timeout 3m

race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

build:
	$(GO) build ./...

security:
	staticcheck ./...
	govulncheck ./...

up:
	docker compose up -d --build --wait

integration:
	$$env:INTEGRATION_TEST='1'; $$env:MYSQL_HOST='127.0.0.1'; $$env:MYSQL_DATABASE='product'; $(GO) test ./tests -v
	powershell -ExecutionPolicy Bypass -File tests/integration_test.ps1
	python tests/api_smoke_test.py

e2e:
	npm run test:e2e

down:
	docker compose down -v

ci: fmt test race vet build security

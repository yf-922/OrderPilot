SHELL := pwsh.exe
GO := go

.PHONY: fmt test race vet build security integration up down ci

fmt:
	$(GO)fmt -w backend common datamodels repositories services tests

test:
	$(GO) test ./... -coverprofile=coverage.out

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

down:
	docker compose down -v

ci: fmt test race vet build security

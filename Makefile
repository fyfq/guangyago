.PHONY: build run import vet tidy clean web lint staticcheck gosec cover web-lint smoke

GO ?= go
BIN_DIR := $(shell $(GO) env GOPATH)/bin

# staticcheck / gosec 首次运行前用 `make tools` 一次性安装
tools:
	$(GO) install honnef.co/go/tools/cmd/staticcheck@latest
	$(GO) install github.com/securego/gosec/v2/cmd/gosec@latest

build:
	$(GO) build -o bin/guangya ./cmd/server

run: build
	./bin/guangya -config config.yaml

# 冒烟：临时目录起隔离实例，跑通安装→登录→门户/SEO→后台读取，全程不出网
smoke: build
	./scripts/smoke.sh

# 导入光鸭批量分享记录或单条分享文本
import: build
	./bin/guangya -config config.yaml -import "$(FILE)"

vet:
	$(GO) vet ./...

staticcheck:
	$(BIN_DIR)/staticcheck ./...

# gosec 默认会对 _test.go 报噪音，只在生产代码上跑
gosec:
	$(BIN_DIR)/gosec -exclude=G104,G304 ./...

# lint 是 CI 的门禁入口：任一失败即退出非 0
lint: vet staticcheck gosec web-lint

web-lint:
	cd web && npm ci && npm run type-check

tidy:
	$(GO) mod tidy

clean:
	rm -rf bin

# 构建前端并同步到 embed 目录；三行各自独立执行，避免 cd 之后相对路径失效
web:
	cd web && npm run build
	rm -rf internal/web/dist
	cp -r web/dist internal/web/dist
	@touch internal/web/dist/.gitkeep

# 覆盖率报告，只看整体百分比即可
cover:
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out | tail -1

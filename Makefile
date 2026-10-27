.PHONY: build run import vet tidy clean web

GO ?= go

build:
	$(GO) build -o bin/guangya ./cmd/server

run: build
	./bin/guangya -config config.yaml

# 导入光鸭批量分享记录或单条分享文本
import: build
	./bin/guangya -config config.yaml -import "$(FILE)"

vet:
	$(GO) vet ./...

tidy:
	$(GO) mod tidy

clean:
	rm -rf bin

# 构建前端并同步到 embed 目录；三行各自独立执行，避免 cd 之后相对路径失效
web:
	cd web && npm run build
	rm -rf internal/web/dist
	cp -r web/dist internal/web/dist

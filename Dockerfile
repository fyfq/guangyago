# 不要加 `# syntax=docker/dockerfile:1`：那行会逼 BuildKit 先去 Docker Hub 拉 docker/dockerfile 前端镜像，
# 弱网/墙内服务器上直接卡死。内置前端已支持本文件用到的多阶段、ARG 默认值、COPY --from 和预置 TARGETARCH。

# 构建期可选参数（网络差时在 .env 里覆盖，见 docker-compose.yml 的 build.args）：
#   BUILD_PROXY   → 转成 HTTP(S)_PROXY 给 npm 和 go 用，必须是 HTTP 代理端口（socks5 端口它们不认）
#   NPM_REGISTRY  → 国内源 https://registry.npmmirror.com
#   GOPROXY       → 国内源 https://goproxy.cn,direct（它会一并代跑 sumdb，GOSUMDB 保持默认即可）
#
# HTTP(S)_PROXY 不用在这里声明：BuildKit 认得这类预置构建参数，
# 只要 compose 的 build.args（或 --build-arg）传进来，各阶段的 RUN 就自动带上这组环境变量。

# 前端：构建产物随后被 go:embed 编进二进制
FROM node:22-alpine AS web
ARG NPM_REGISTRY=https://registry.npmjs.org
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund --registry=${NPM_REGISTRY}
COPY web/ ./
RUN npm run build

# 后端：modernc.org/sqlite 是纯 Go，CGO 关掉即可产出静态二进制
FROM golang:1.26-alpine AS server
ARG GOPROXY=https://proxy.golang.org,direct
ARG GOSUMDB=sum.golang.org
ARG TARGETARCH=amd64
ENV GOPROXY=${GOPROXY} GOSUMDB=${GOSUMDB}
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./internal/web/dist
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/guangya ./cmd/server

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=server /out/guangya /app/guangya
EXPOSE 8090
USER 1000:1000
# 配置与数据都落在 /app/data（宿主 bind mount）。-addr 固定，宿主改端口只动 compose 的 ports。
ENTRYPOINT ["/app/guangya", "-config", "/app/data/config.yaml", "-addr", ":8090"]

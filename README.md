<div align="center">

# 🦆 光鸭 GO

**光鸭云盘分享链接 → TMDB 刮削 → Telegram 频道推送**，一个二进制跑完整条链路。

[![CI](https://img.shields.io/github/actions/workflow/status/fyfq/guangyago/ci.yml?branch=main&label=CI&logo=githubactions&logoColor=white)](https://github.com/fyfq/guangyago/actions/workflows/ci.yml)
![Go 1.26](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)
![Vue%203](https://img.shields.io/badge/Vue-3-42b883?logo=vuedotjs&logoColor=white)
![SQLite%20纯Go驱动](https://img.shields.io/badge/SQLite-%E7%BA%AFGo%E9%A9%B1%E5%8A%A8-003B57?logo=sqlite&logoColor=white)
![Arch amd64/arm64](https://img.shields.io/badge/image-amd64%20%7C%20arm64-2496ED?logo=docker&logoColor=white)
![Single binary](https://img.shields.io/badge/%E4%BA%A7%E7%89%A9-%E5%8D%95%E4%B8%AA%E9%9D%99%E6%80%81%E4%BA%8C%E8%BF%9B%E5%88%B6-10B981)

**一个进程 · 一个端口 · 一个 `data/` 目录 · 零外部依赖**

</div>

---

录入链接 → 取影视信息 → TMDB 刮削 → 推送 Telegram 频道，同时在服务端长出一个**公开资源门户**：Google 爬虫能直接读到标题、简介、海报与结构化数据。

## ✨ 功能一览

| | 能力 | 说明 |
| --- | --- | --- |
| 📥 | **批量录入** | 粘贴分享文本或上传 CSV，按 `shareId` 去重落库；Telegram Bot 也能直接收消息录入 |
| 🎬 | **TMDB 刮削** | 电影/剧集自动判别、中英文标题模糊匹配、海报本地缓存、TMDB 429 按 `Retry-After` 退让 |
| 📤 | **频道推送** | 图文卡片推送到 Telegram 频道，海报不可用时自动回退纯文本；发送节奏走官方建议的限流下限 |
| 🌐 | **公开门户** | `/` 展示已收录资源：搜索、类型筛选、排序、详情深链，全部服务端渲染 SEO 标签 |
| 🔍 | **Google 收录** | `robots.txt` / `sitemap.xml`（超 5000 自动分片）/ Schema.org JSON-LD / Search Console 验证与自检 |
| 🛠 | **后台管理** | 录入 / 刮削 / 推送 / 资源库 / 设置 / 用户管理 / SEO 七个页签，进度实时轮询 |
| 🔐 | **安全默认** | PBKDF2 口令、服务端可撤销会话、双源 CSRF、CSP、登录锁定、SSRF 与外链白名单 |
| 🩺 | **运行日志** | 结构化访问日志 + 刮削/推送关键事件，按大小轮转写在 `data/logs/`，设置页一键下载 |
| 🚚 | **三种投递** | Docker Compose 镜像 / 离线 `docker save` / 裸机单二进制 + systemd，全部只依赖一个数据目录 |

## 🚀 快速开始

```bash
cp config.example.yaml config.yaml   # 填入 TMDB Key / Bot Token / 频道 ID
make build
./bin/guangya -import ~/Downloads/批量分享记录_xxx.csv   # 导入 CSV
./bin/guangya                                            # 启动，示例配置里监听 :8090
```

打开 `http://localhost:8090/` 是公开门户，`/admin` 是后台。用户表为空时访问任意页面都会跳到安装向导 `/setup`。

> 端口有三个来源，别记混：代码内置兜底 `:8080`、`config.example.yaml` 写 `:8090`、镜像 ENTRYPOINT 用 `-addr :8090` 固定。**示例配置与容器统一走 8090**，README 与文档里的地址都按 8090 写。

导入单条分享文本同理，把文件内容换成「光鸭云盘用户给你分享了…」那一段即可。

改前端后必须 `make web` 再 `make build`：`cmd/server` 通过 `//go:embed` 把 `internal/web/dist` 编进二进制，只跑 `npm run build` 不会更新那份拷贝，页面上看不到任何改动。

## 🐳 部署

### Docker（推荐）

一个容器、一个目录。所有状态都落在宿主机的 `./data`：SQLite 库、海报缓存、站点素材、运行日志，以及后台「设置」页写回的 `config.yaml`。

```bash
# 服务器上，源码目录里
mkdir -p data && sudo chown 1000:1000 data   # 容器以 uid 1000 运行，要能写 data/
docker compose up -d --build
curl -s localhost:8090/api/health
```

`http://服务器IP:8090/` 是公开门户，`/admin` 是后台。想在无头环境初始化管理员，设 `ADMIN_USERNAME` / `ADMIN_PASSWORD` 环境变量，服务启动时会直接建号（仅在用户表为空时生效）。

本机已有的 `data/`（`guangya.db` + `posters/`）整个拷过去就是无缝迁移，只有 `config.yaml` 的位置从项目根换成 `data/config.yaml`。

#### 构建期网络（服务器出网差时）

`docker compose build` 要下三样东西：基础镜像、npm 包、Go 模块。后两样走 compose 的 `build.args`，在 `.env` 里覆盖即可：

| 变量 | 管什么 | 网络差时填 |
| --- | --- | --- |
| `NPM_REGISTRY` | npm 源 | `https://registry.npmmirror.com` |
| `GOPROXY` | Go 模块代理 | `https://goproxy.cn,direct` |
| `BUILD_PROXY` | 构建期出境代理 | `http://192.168.1.10:7890` |

`GOPROXY=https://goproxy.cn,direct` 时 `GOSUMDB` 保持默认（goproxy.cn 会代跑校验），别顺手关掉。

用 `BUILD_PROXY` 有两个坑：只能给 **HTTP 端口**，给 socks5 端口 npm 和 go 都不认（运行期 `PROXY_ADDR` 那份 socks5 配置是另一回事，别混用）；构建沙箱在 bridge 网络里，`127.0.0.1` 不是宿主机，要么写宿主的局域网 IP，要么放开 compose 里 `build.network: host` 那行再写 `http://127.0.0.1:7890`。

基础镜像（`node` / `golang` / `alpine`）不吃 build args，得配 Docker 守护进程：`/etc/docker/daemon.json` 加 `"registry-mirrors": ["https://<加速地址>"]`，或给 dockerd 加 systemd 代理 drop-in（`HTTP_PROXY`/`HTTPS_PROXY`），改完 `systemctl daemon-reload && systemctl restart docker`。

Dockerfile 首行**不要**写 `# syntax=docker/dockerfile:1`：那会让 BuildKit 在解析阶段先去 Docker Hub 拉 `docker/dockerfile` 前端镜像，构建连基础镜像都还没开始就卡死。本项目的 Dockerfile 特意不带这行，内置前端已覆盖用到的多阶段、`ARG` 默认值、`COPY --from` 与预置 `TARGETARCH`。

#### 离线投递

镜像构建要出网（npm + Go modules）。服务器不方便出网时，在能出网的机器上建好再传：

```bash
docker build --platform linux/amd64 -t guangya:latest .
docker save guangya:latest | gzip > guangya.tar.gz
scp guangya.tar.gz server:/tmp && ssh server 'gunzip -c /tmp/guangya.tar.gz | docker load'
```

之后服务器上 `docker compose up -d` 即可（`image: guangya:latest` 已就位，`build: .` 用不上）。

### 裸机：一个二进制，服务器完全不出网

Docker Hub 连不上的时候，最省事的是不装 Docker。前端被 `go:embed` 进了二进制，`modernc.org/sqlite` 又是纯 Go，所以产物是**无任何依赖的静态可执行文件**（约 26MB，前后端都在里面）。在有网的机器上交叉编译好拷过去：

```bash
# 本机（或 CI）构建：前端产物会被一起编进二进制
make web
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o guangya ./cmd/server
```

```bash
# 服务器上：tar 包里含 guangya 与 guangya.service（DynamicUser，免建账号、免 chown）
tar xzf guangya-bare-*-linux-amd64.tar.gz -C /tmp/gy
sudo install -m 755 /tmp/gy/guangya /usr/local/bin/guangya
sudo cp /tmp/gy/guangya.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now guangya
curl -s localhost:8090/api/health
```

数据落在 unit 里 `StateDirectory` 指的 `/var/lib/guangya/data/`（库、海报、日志、`config.yaml`），备份迁移同样是 tar 这一个目录。要换端口改 unit 里的 `-addr`，再 `daemon-reload` + `restart`。这条路径的密钥只在本机与服务器之间走一次文件拷贝，不过网。

### 密钥与配置

两种写法可以混用，环境变量优先：

- 后台「设置」页填写，程序写回 `data/config.yaml`，重启不丢（保存时勾上「写回配置文件」，否则密钥只活在内存里，重启即丢）；
- 与 compose 同级建 `.env`，写 `TMDB_API_KEY` / `TG_BOT_TOKEN` / `GUANGYA_COOKIE` / `PROXY_ADDR` / `ADMIN_USERNAME` / `ADMIN_PASSWORD`（`ADMIN_*` 只在用户表为空时生效）。

容器内监听地址被 `-addr :8090` 固定，换对外端口只改 compose 里 `ports` 左侧。

### 代理

代理跑在宿主机时 `PROXY_ADDR` 写 `host.docker.internal:7890`（compose 已配 `host-gateway`）；写 `127.0.0.1:7890` 指向的是容器自己，连不通。

### 迁移与备份

```bash
docker compose stop                          # 干净关闭会把 WAL 合并回主库
tar czf guangya-data.tgz data/
# 新机先备好源码目录（含 docker-compose.yml），再：
scp guangya-data.tgz new-server:/opt/guangya/ && ssh new-server 'tar xzf guangya-data.tgz'
docker compose up -d
```

`data/` 里只有 `guangya.db*`、`posters/`、`branding/`、`session.key`、`config.yaml` 是状态；`logs/` 是历史运行记录，迁移时可以不拷。

> ⚠️ `session.key` 决定所有会话令牌摘要。**换掉这台文件，已登录的会话全部失效**，用户需要重新登录。

### 公网接入

服务本身按明文 HTTP 提供，`Secure` Cookie 依赖 HTTPS。公网部署请挂在 HTTPS 反向代理后（`/api/health` 可做健康检查）；登录失败锁定按真实 TCP 地址统计，经反代后会全部记到代理 IP 上——这是有意为之，不信任 `X-Forwarded-For`，避免伪造来源绕过锁定。

sitemap 里的地址要用公网域名，记得把 `seo.base_url` 固定成 `https://<你的域名>`，否则会写成内网地址。

## 🚪 访问入口与账号

| 路径 | 说明 |
| --- | --- |
| `/` | 公开资源门户，只展示已收录的记录：按片名搜索、按电影/剧集筛选、按最新或评分排序，点开可复制分享链接与提取码 |
| `/admin` | 后台管理，录入 / 刮削 / 推送 / 资源库 / 设置 / 用户管理 / SEO |
| `/login` | 后台登录；未登录访问 `/admin` 会自动跳转并带回跳地址 |
| `/setup` | 安装向导；用户表为空时访问任意页面都会跳到这里 |
| `/branding/:name` | 站点 Logo / 图标原图，只有与配置里记录的文件名精确相等才回源 |

角色只有两种：**管理员**（含密钥、代理、SEO、站点品牌、用户管理、日志下载）与**运营**（录入、刮削、推送、资源库）。运营连管理员入口都看不到，接口也调不通。

## 🖥️ 页面职能

后台导航按数据流向排列，每个页面只负责一件事：

| 页面 | 承载的状态 | 职能 |
| --- | --- | --- |
| **录入解析** | 入库瞬间 | 粘贴分享文本 / 上传 CSV，解析去重落库；成功后可「立即刮削这 N 条」，这是最顺手的刮削触发点 |
| **刮削队列** | `pending` `review` `scraping` `unmatched` `failed` | **刮削的唯一触发场所**：按筛选批量刮削、刮削所选、重试失败与未匹配；未匹配的记录可手动补 TMDB ID 后重刮 |
| **推送频道** | `scraped` `push_failed` | 待推送工作台：预览实际卡片文案（与推送内容一致，可复制），推送全部待推送 / 重试推送失败 |
| **资源库** | `scraped` `pushed` `push_failed` | 刮削完成的资源都在这里：检索、海报墙、详情抽屉、单条重推、导出 CSV，按推送状态回看 |
| **设置** | — | TMDB Key / Bot Token / 频道 ID / 代理，逐项连通性测试；底部「运行日志」直接下载当前日志文件 |
| **用户管理** | — | 仅管理员可见：创建账号、切换角色、停用/启用、重置密码、删除 |
| **SEO** | — | 仅管理员可见：Google 站点验证码、站点根地址、站点标题与 Logo / 图标上传、sitemap / robots / 结构化数据自检与提交指引 |

刮削成功后记录自动从「刮削队列」流出，推送完成后可在「资源库」按已推送回看；重刮失败会退回队列，不会留在资源库里充数。

## 🔐 安全默认值

- 密钥、代理、站点品牌、用户管理与日志下载接口仅管理员可用，运营账号既看不到入口也调不通。
- 密码用 PBKDF2-HMAC-SHA256（210,000 轮 / 随机盐）存储；会话是 32 字节随机令牌下发为 HttpOnly Cookie（SameSite=Lax，7 天），数据库只存 HMAC-SHA256 摘要。停用账号、重置或修改密码都会让旧会话立即失效。
- 登录失败按真实 TCP 地址与用户名两个维度限流（连续 5 次锁 5 分钟，状态落库、重启不丢）；服务端不信任 `X-Forwarded-For`。
- 账号不存在与密码错误返回同一提示，并做等时校验，避免通过耗时枚举账号。
- 所有状态变更接口做 Origin/Referer 同源校验，与 `SameSite=Lax` 构成双保险。
- 页面带 `X-Content-Type-Options`、`X-Frame-Options: DENY`、`Referrer-Policy`、`Permissions-Policy` 与 CSP（`script-src 'self'`，所以前端不能引 CDN、不能写内联脚本）；API 响应 `Cache-Control: no-store`；HSTS 只在直连 TLS 时下发，反代场景交给边缘。
- 普通接口请求体上限 1MB，导入接口 32MB；CSV 导出带 BOM 并给公式前缀加 `'`，防止表格公式注入。
- 门户只输出已收录记录的展示字段，链接仅允许 http/https，查询用占位符与排序白名单，LIKE 关键词统一转义。
- TMDB 图片域名走白名单防 SSRF；非 http(s) 的海报地址不会被发给 Telegram。
- 运行日志写在 `data/logs/`（目录 0750、文件 0600），单文件 5 MB、轮转保留 3 份；访问日志只记路由模式与状态码，不落查询串里的提取码。下载入口仅管理员可用，且不接受客户端指定文件名。

部署到公网时请放在 HTTPS 之后，并把 `config.yaml` 与 `data/guangya.db` 权限收紧到 600。

## 📤 推送卡片格式

```
片名

2008

简介（最多 200 字，超出截断并补省略号）

光鸭资源链接          ← 超链接指向分享地址

提取码：xxxx

TMDB: https://www.themoviedb.org/tv/20851
```

排版规则：**每一项各占一行，项与项之间都空一行**。整段不超过 Telegram 的 1024 字上限，简介会按剩余额度再压缩。
界面上的预览直接调 `/api/links/:id/caption`，与实际推送同源，不会出现两处格式漂移。

## 🧩 解析规则

- 链接：`https://www.guangyapan.com/s/{shareId}_{token}?code={提取码}`，`shareId` 作为去重唯一键
- 文件名：`标题 (年份) {tmdb-ID}` → 拆出片名、年份、TMDB ID
- 分享文本中的截断标题（如 `…-156138}`）同样能抠出 TMDB ID
- 年份旁边写了 `tmdb-ID` 的记录，刮削时直接按该 ID 取详情，不再按标题相似度打回待核实

## 🌐 收录与 SEO

`/robots.txt`、`/sitemap.xml`、Schema.org 结构化数据都由服务端动态生成。要进 Google：后台「SEO」页填 Search Console「HTML 标签」给出的验证码，服务端会把它注入首页原始 HTML（爬虫读不到前端脚本补的标签），验证通过后在 Console 里提交 `https://<你的域名>/sitemap.xml`。

同一页的「站点标识」可以改站点标题、上传 Logo 与浏览器图标：标题由服务端替换进首页原始 HTML 的 `title` / `og:` / `twitter:`，图片落在 `<data_dir>/branding/`，只接受 PNG / JPEG / GIF / WebP（SVG 可内嵌脚本，不收录）。详细步骤与配置项见 [docs/dev-guide.md](docs/dev-guide.md#8-seo-与-google-录入)。

## ⚙️ 配置与代理

密钥建议走环境变量：`TMDB_API_KEY`、`TG_BOT_TOKEN`、`GUANGYA_COOKIE`、`SEO_BASE_URL`，代理用 `PROXY_ADDR`（+ `PROXY_USERNAME` / `PROXY_PASSWORD`）。首次启动的初始管理员用安装向导 `/setup` 创建，或在无头环境用 `ADMIN_USERNAME` / `ADMIN_PASSWORD`。

TMDB 与 Telegram 的出网请求共用一套代理配置：

```yaml
proxy:
    enable: true
    addr: 127.0.0.1:7890        # 写 host:port 默认按 socks5 处理
    username: ""                # 也可写 socks5://host:port、http://host:port
    password: ""
```

界面上启用后点「测试」会经代理访问 `api.telegram.org` 并回显出口 IP；改完保存立即生效，Bot 会用新代理重启。

关于 TMDB 密钥，两种形态都支持，程序按形态自动选择鉴权方式：

- 32 位 API Key → `?api_key=xxx`
- `eyJ` 开头的 Read Access Token → `Authorization: Bearer xxx`

## 🩺 连通性测试与排查

设置页每个密钥旁都有「测试」按钮，不写回任何配置，只探测：

| 目标 | 做了什么 |
| --- | --- |
| 🎬 TMDB | 调 `/configuration`，区分「Key 无效（401）」与「网络/代理不通」 |
| 📨 Telegram | `getMe` 拿 Bot 用户名 → `getChat` 拿频道标题 → 校验 Bot 是否为管理员 |
| 🌐 代理 | 经代理访问 Telegram，返回耗时与出口 IP |

**出问题先看日志。** 服务端把结构化日志同时写到标准输出与 `data/logs/app.log`，容器里 `docker logs` 看到的内容和文件完全一致；设置页底部「运行日志」可以一键下载当前文件（`GET /api/logs/download`，仅管理员）。排查用的关键字段：

| 现象 | 看哪条日志 |
| --- | --- |
| 接口慢或报错 | `HTTP 请求` 的 `path` / `status` / `latency_ms`；5xx 与慢请求会升到 Error/Warn |
| 刮削不匹配或判错类型 | `刮削未匹配` / `刮削结果待人工确认`（带 `tmdb_id`、`score`） |
| 推送发不出去 | `推送失败`（带 `poster_url`）、`海报发送失败，回退纯文本`、`推送跳过：未配置 Telegram Bot Token` |
| 任务一直排队 | `批次结束` 的 `total` / `done` / `failed` / `elapsed_ms`，以及 `已取消排队任务` |
| TMDB 被限流 | `TMDB 限流，退让后重试` 的 `retry_after_ms` |

> 同一 Telegram Token 只能有一个 getUpdates 消费者，本地再起一个实例会看到 `Conflict: terminated by other getUpdates request`——不是 Bug，停掉多余实例即可。

## 📡 接口

逐条参数、请求体与响应字段见 **[docs/api.md](docs/api.md)**。要点：

- 公开：`/api/health`、`/api/portal/links`、`/api/portal/stats`、`/api/setup/status`、`/api/setup`、`/api/auth/login`、`/robots.txt`、`/sitemap.xml`、`/api/structured-data[/:id]`。
- 其余接口都要会话 Cookie（`gy_session`，HttpOnly + SameSite=Lax）；密钥、代理、站点品牌、SEO、用户管理与 `GET /api/logs/download` 只对管理员开放。
- 所有状态变更接口做同源校验（Origin/Referer），跨站 POST 会拿到 403。
- 错误统一 `{"error":"中文提示"}`；导入返回 `{"total":94,"inserted":94,"skipped":0,"failed":[],"ids":[...]}`。

## 📚 文档地图

| 文档 | 面向 | 内容 |
| --- | --- | --- |
| [docs/api.md](docs/api.md) | 调用方 / 前端 | 每个接口的参数、请求体、响应字段与错误码 |
| [docs/dev-guide.md](docs/dev-guide.md) | **改代码前先看** | 运行时拓扑、启动序列、包职责、状态机、安全、配置、构建与维护清单 |
| [docs/design.md](docs/design.md) | 需求 | 目标、功能范围与原型 |
| [docs/design-system.md](docs/design-system.md) | 界面 | 设计 token 与组件规范 |
| [docs/tech-selection.md](docs/tech-selection.md) | 选型 | 为什么是 Go + SQLite + Vue |
| [AGENTS.md](AGENTS.md) | AI 协作 | 术语表、包边界、代码风格、提交边界与 reviewer 清单 |

## 🤝 参与

改代码前请先读 [AGENTS.md](AGENTS.md)（术语、依赖方向、提交边界都写在里面）与 [docs/dev-guide.md](docs/dev-guide.md)。约定：单分支 `main` 始终可部署，`feat/<名>` / `fix/<名>` 只在超过 300 行或涉及 schema 时才开；接口或配置有变动时同步更新 `docs/api.md` 与维护清单。

```bash
make lint      # go vet + staticcheck + gosec + 前端 type-check
go test ./...  # 全部离线，不出网
make smoke     # 起隔离实例跑「安装 → 登录 → 门户/SEO → 登出」，约 1 秒
```

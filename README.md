# 光鸭 GO

光鸭云盘分享链接管理工具：录入链接 → 取影视信息 → TMDB 刮削 → 推送 Telegram 频道。

设计文档见 `docs/design.md`，技术选型见 `docs/tech-selection.md`，界面规范见 `docs/design-system.md`。
**改代码前先看**：[docs/dev-guide.md](docs/dev-guide.md)（架构、包职责、状态机、安全、构建与维护清单）、[docs/api.md](docs/api.md)（逐个接口的参数与响应）。

## 当前进度：M1 ~ M3 已完成，M4 页面已完成

已完成：项目骨架、SQLite 存储、光鸭链接解析（CSV 批量 + 分享文本）、TMDB 刮削、Telegram 频道推送与 Bot 录入、Web 后台（安装向导 / 资源库 / 录入 / 刮削 / 推送 / 设置 / 用户管理 / SEO）与公开资源门户、搜索引擎收录支持、Docker 部署。

## 快速开始

```bash
cp config.example.yaml config.yaml   # 填入 TMDB Key / Bot Token / 频道 ID
make build
./bin/guangya -import ~/Downloads/批量分享记录_xxx.csv   # 导入 CSV
./bin/guangya                                            # 启动服务，默认 :8080
```

导入单条分享文本同理，把文件内容换成「光鸭云盘用户给你分享了…」那一段即可。

改前端后必须走 `make web` 再 `make build`：`cmd/server` 通过 `//go:embed` 把 `internal/web/dist`
编进二进制，只跑 `npm run build` 不会更新那份拷贝，页面上看不到任何改动。

## Docker 部署（x86 服务器）

一个容器、一个目录。所有状态都落在宿主机的 `./data`：SQLite 库、海报缓存、以及后台「设置」页写回的 `config.yaml`。

```bash
# 服务器上，源码目录里
mkdir -p data && sudo chown 1000:1000 data   # 容器以 uid 1000 运行，要能写 data/
docker compose up -d --build
curl -s localhost:8090/api/health
```

`http://服务器IP:8090/` 是公开门户，`/admin` 是后台。用户表为空时，首次访问会自动跳到安装向导 `/setup`，在页面里创建管理员账号（可选顺手填 TMDB Key）。想在无头环境初始化，就设 `ADMIN_USERNAME` / `ADMIN_PASSWORD` 环境变量，服务启动时会直接建号。

本机已有的 `data/`（如 `guangya.db` + `posters/`）直接整个拷过去就是无缝迁移，只有 `config.yaml` 的位置从项目根换成 `data/config.yaml`。

### 构建期网络（服务器出网差时）

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

### 离线投递

镜像构建要出网（npm + Go modules）。服务器不方便出网时，在能出网的机器上建好再传：

```bash
docker build --platform linux/amd64 -t guangya:latest .
docker save guangya:latest | gzip > guangya.tar.gz
scp guangya.tar.gz server:/tmp && ssh server 'gunzip -c /tmp/guangya.tar.gz | docker load'
```

之后服务器上 `docker compose up -d` 即可（`image: guangya:latest` 已就位，`build: .` 用不上）。

### 裸机跑：一个二进制，服务器完全不出网

Docker Hub 连不上的时候，最省事的是不装 Docker。`cmd/server` 把前端 `go:embed` 进了二进制，`modernc.org/sqlite` 又是纯 Go，所以产物是**无任何依赖的静态可执行文件**（约 26MB，前后端都在里面）。在有网的机器上交叉编译好拷过去：

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

数据落在 unit 里 `StateDirectory` 指的 `/var/lib/guangya/data/`（库、海报、`config.yaml`），备份迁移同样是 tar 这一个目录。要换端口改 unit 里的 `-addr`，再 `daemon-reload` + `restart`。这条路径的密钥只在本机与服务器之间走一次文件拷贝，不过网。

### 密钥与配置

两种写法可以混用，环境变量优先：

- 后台「设置」页填写，程序写回 `data/config.yaml`，重启不丢（保存时勾上「写回配置文件」，否则密钥只活在内存里，重启即丢）；
- 与 compose 同级建 `.env`，写 `TMDB_API_KEY` / `TG_BOT_TOKEN` / `GUANGYA_COOKIE` / `PROXY_ADDR` / `ADMIN_USERNAME` / `ADMIN_PASSWORD`（`ADMIN_*` 只在用户表为空时生效）。

容器内监听地址被 `-addr :8090` 固定，换对外端口只改 compose 里 `ports` 的左侧。

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

### 公网接入

服务本身按明文 HTTP 提供，`Secure` Cookie 依赖 HTTPS。公网部署请挂在 HTTPS 反向代理后（`/api/health` 可做健康检查）；登录失败锁定按真实 TCP 地址统计，经反代后会全部记到代理 IP 上——这是有意为之，不信任 `X-Forwarded-For`，避免伪造来源绕过锁定。

## 访问入口与账号

| 路径 | 说明 |
| --- | --- |
| `/` | 公开资源门户，只展示已推送的记录：按片名搜索、按电影/剧集筛选、按最新或评分排序，点开可复制分享链接与提取码 |
| `/admin` | 后台管理，录入 / 刮削 / 推送 / 资源库 / 设置 / 用户管理 / SEO |
| `/login` | 后台登录；未登录访问 `/admin` 会自动跳转并带回跳地址 |
| `/setup` | 安装向导；用户表为空时访问任意页面都会跳到这里 |

用户表为空时不会自动建号，请走 `/setup`；也可用 `ADMIN_USERNAME` / `ADMIN_PASSWORD` 在无头环境直接初始化（仅在账号表为空时生效）。登录后请尽快在「用户管理」里确认密码强度。

- 角色：**管理员**可管理用户；**运营**只能使用录入、刮削、推送与资源库。
- 密码以 PBKDF2-HMAC-SHA256（随机盐）存储；会话是服务端可撤销的 HttpOnly Cookie（7 天），停用账号、重置或修改密码都会让旧会话立即失效。
- 同一 IP 连续 5 次登录失败会锁定 5 分钟。

## 安全默认值

- 密钥、代理与用户管理接口仅管理员可用，运营账号既看不到入口也调不通。
- 会话令牌是 32 字节随机数，数据库只存 SHA-256 摘要；Cookie 为 HttpOnly + SameSite=Lax，HTTPS 下自动加 Secure。
- 登录失败按真实 TCP 地址限流；服务端不信任 `X-Forwarded-For`，避免伪造来源绕过锁定（若部署在反向代理后，限流会按代理地址统计）。
- 账号不存在与密码错误返回同一提示，并做等时校验，避免通过耗时枚举账号。
- 页面带 `X-Content-Type-Options`、`X-Frame-Options: DENY`、`Referrer-Policy` 与 CSP；API 响应 `Cache-Control: no-store`。
- 普通接口请求体上限 1MB，导入接口 32MB；CSV 导出会给公式前缀加 `'`，防止表格公式注入。
- 门户只输出已推送记录的展示字段，链接仅允许 http/https，查询用占位符与排序白名单。

部署到公网时请放在 HTTPS 之后，并把 `config.yaml` 与 `data/guangya.db` 权限收紧到 600。

## 页面职能

后台导航按数据流向排列，每个页面只负责一件事：

| 页面 | 承载的状态 | 职能 |
| --- | --- | --- |
| **录入解析** | 入库瞬间 | 粘贴分享文本 / 上传 CSV，解析去重落库；成功后可「立即刮削这 N 条」，这是最顺手的刮削触发点 |
| **刮削队列** | `pending` `review` `scraping` `unmatched` `failed` | **刮削的唯一触发场所**：按筛选批量刮削、刮削所选、重试失败与未匹配；未匹配的记录可手动补 TMDB ID 后重刮 |
| **推送频道** | `scraped` `push_failed` | 待推送工作台：预览实际卡片文案（与推送内容一致，可复制），推送全部待推送 / 重试推送失败 |
| **资源库** | `scraped` `pushed` `push_failed` | 刮削完成的资源都在这里：检索、海报墙、详情抽屉、单条重推、导出 CSV，按推送状态回看 |
| **设置** | — | TMDB Key / Bot Token / 频道 ID / 代理，逐项连通性测试 |
| **用户管理** | — | 仅管理员可见：创建账号、切换角色、停用/启用、重置密码、删除 |
| **SEO** | — | 仅管理员可见：Google 站点验证码、站点根地址、sitemap / robots / 结构化数据自检与提交指引 |

刮削成功后记录自动从「刮削队列」流出，推送完成后可在「资源库」按已推送回看；重刮失败会退回队列，不会留在资源库里充数。

## 推送卡片格式

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

## 接口

逐条参数、请求体与响应字段见 **[docs/api.md](docs/api.md)**。要点：

- 公开：`/api/health`、`/api/portal/links`、`/api/portal/stats`、`/api/setup/status`、`/api/setup`、`/api/auth/login`、`/robots.txt`、`/sitemap.xml`、`/api/structured-data[/:id]`。
- 其余接口都要会话 Cookie（`gy_session`，HttpOnly + SameSite=Lax）；密钥、代理、SEO 与用户管理只对管理员开放。
- 所有状态变更接口做同源校验（Origin/Referer），跨站 POST 会拿到 403。
- 错误统一 `{"error":"中文提示"}`；导入返回 `{"total":94,"inserted":94,"skipped":0,"failed":[],"ids":[...]}`。


## 解析规则

- 链接：`https://www.guangyapan.com/s/{shareId}_{token}?code={提取码}`，`shareId` 作为去重唯一键
- 文件名：`标题 (年份) {tmdb-ID}` → 拆出片名、年份、TMDB ID
- 分享文本中的截断标题（如 `…-156138}`）同样能抠出 TMDB ID

## 配置

密钥建议走环境变量：`TMDB_API_KEY`、`TG_BOT_TOKEN`、`GUANGYA_COOKIE`，
代理用 `PROXY_ADDR`（+ `PROXY_USERNAME` / `PROXY_PASSWORD`）。
首次启动的初始管理员用安装向导 `/setup` 创建，或在无头环境用 `ADMIN_USERNAME` / `ADMIN_PASSWORD`（仅在账号表为空时生效）。
站点根地址用 `SEO_BASE_URL`（反向代理后 sitemap 需要它才能写成公网域名）。

### 收录与 SEO

`/robots.txt`、`/sitemap.xml`、Schema.org 结构化数据都由服务端动态生成。要进 Google：后台「SEO」页填 Search Console「HTML 标签」给出的验证码，服务端会把它注入首页原始 HTML（爬虫读不到前端脚本补的标签），验证通过后在 Console 里提交 `https://<你的域名>/sitemap.xml`。同一页的「站点标识」可以改站点标题、上传 Logo 与浏览器图标：标题由服务端替换进首页原始 HTML 的 `title`/`og:`/`twitter:`，图片落在 `<data_dir>/branding/`，只接受 PNG / JPEG / GIF / WebP（SVG 可内嵌脚本，不收录）。详细步骤与配置项见 [docs/dev-guide.md](docs/dev-guide.md#8-seo-与-google-录入)。

### 代理

TMDB 与 Telegram 的出网请求共用一套代理配置：

```yaml
proxy:
    enable: true
    addr: 127.0.0.1:7890        # 写 host:port 默认按 socks5 处理
    username: ""                # 也可写 socks5://host:port、http://host:port
    password: ""
```

界面上启用后点「测试」会经代理访问 `api.telegram.org` 并回显出口 IP；
改完保存立即生效，Bot 会用新代理重启。

### 关于 TMDB 密钥

两种都支持，程序按形态自动选择鉴权方式：

- 32 位 API Key → `?api_key=xxx`
- `eyJ` 开头的 Read Access Token → `Authorization: Bearer xxx`

## 连通性测试

设置页每个密钥旁都有「测试」按钮，不写回任何配置，只探测：

| 目标 | 做了什么 |
| --- | --- |
| TMDB | 调 `/configuration`，区分「Key 无效（401）」与「网络/代理不通」 |
| Telegram | `getMe` 拿 Bot 用户名 → `getChat` 拿频道标题 → 校验 Bot 是否为管理员 |
| 代理 | 经代理访问 Telegram，返回耗时与出口 IP |

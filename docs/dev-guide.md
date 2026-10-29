# 光鸭 GO 开发文档

面向维护者。描述当前代码的真实结构，与 `docs/design.md`（需求与原型）、`docs/tech-selection.md`（选型）、`docs/design-system.md`（界面规范）互补。接口逐条说明见 [docs/api.md](api.md)。

- 最后核对：2026-10-29，对应提交 `14cfbd0`
- 技术栈：Go + gin + SQLite（纯 Go 驱动）/ Vue 3 + TS + Tailwind v4，前端产物 `go:embed` 进二进制

---

## 1. 运行时拓扑

一个进程、一个端口（默认 `:8090`）同时提供三样东西：公开资源门户、后台管理界面、JSON API。SQLite 单文件库 + 海报目录 + `config.yaml` 全部落在 `data/` 下，因此备份 = tar 一个目录。

```
浏览器 ──HTTP──> cmd/server
                   ├─ internal/api      JSON API / robots / sitemap / 首页注入
                   ├─ internal/pipeline 刮削与推送 worker 池
                   ├─ internal/scraper  ──> TMDB API
                   ├─ internal/notifier ──> Telegram Bot API（推送 + long-polling 录入）
                   ├─ internal/store    ──> SQLite（WAL）
                   └─ internal/web      ──> go:embed 的前端 dist
```

出网请求（TMDB / Telegram / 光鸭探测）共用 `proxy` 配置，经 `internal/netutil` 构造客户端。

## 2. 启动序列

`cmd/server/main.go`：

1. 解析 flag：`-config`（默认 `config.yaml`）、`-addr`、`-import FILE`、`-no-bot`、`-v`。
2. `config.Load` → `store.Open`（迁移在建库后立即执行）。
3. `auth.LoadOrCreateSessionKey(dataDir)`：读或生成 `data/session.key`（32 字节，0600）。拿不到则退回裸 SHA-256 摘要并告警——**换了这台文件，所有已登录会话都会失效**。
4. `-import` 非空：解析 CSV/文本落库后打印统计并退出，不启服务。
5. `ensureAdmin`：仅当用户表为空且设置了 `ADMIN_PASSWORD` 时创建初始管理员；否则只在日志提示走安装向导（`/setup`）。
6. `build(cfg)`：按配置构造 scraper / notifier，首次创建 pipeline 并 `Start(workers)`，按需启动 Bot。这个闭包同时是配置热更新回调。
7. gin：`ReleaseMode`、`SetTrustedProxies(nil)`（**故意不信任 `X-Forwarded-For`**，登录锁定按真实 TCP 地址统计）、中间件 `Logger / Recovery / securityHeaders / limitBody`，`Register` 里再加全局 `requireSameOrigin`。
8. `NoRoute`：`/api/*` 回 404 JSON；命中静态文件直接 `FileFromFS`；其余一律 `srv.WriteIndex` 回落首页（为了注入 SEO 标签，见 §8）。
9. 收 SIGINT/SIGTERM：`pipe.Stop()` → 5 秒超时的 `http.Server.Shutdown`。

## 3. 包职责

| 包 | 职责 | 关键符号 |
| --- | --- | --- |
| `cmd/server` | 入口、装配、优雅关闭、安全响应头 | `main`、`ensureAdmin`、`runImport`、`securityHeaders`、`limitBody` |
| `internal/api` | HTTP 层。`Server` 持有 store / pipeline / cfg | `New`、`WithRuntime`、`Register`、`Save`、`WriteIndex`、`loginGuard` |
| `internal/pipeline` | 刮削与推送的 worker 池与进度 | `Pipeline`、`Enqueue`、`Cancel`、`Status`/`StatusOf`、`UpdateComponents`、`SetGuangyaLimit` |
| `internal/store` | SQLite 访问层，建表与迁移 | `Open`、`List`/`Get`/`Create`、`ListPublic`/`PublicStats`、`users.go`、`login.go` |
| `internal/model` | 数据结构与状态常量 | `Link`、`Status*`、`User`、`PublicLink`/`PublicFilter`/`PublicStats`、状态集合 |
| `internal/auth` | 密码哈希、会话令牌、会话密钥文件 | `HashPassword`、`VerifyPassword(OrDummy)`、`NewSessionToken`、`HashToken`、`LoadOrCreateSessionKey` |
| `internal/config` | YAML 配置 + 环境变量覆盖 + 写回 | `Config`、`Default`、`Load`、`Save`、`Effective*`、`ChatID.Any` |
| `internal/scraper` | TMDB 客户端与海报缓存 | `New`、`Fetch`、`LookupByID`/`LookupByTitle`、`SavePoster`、`TestConnection`、`LowConfidence` |
| `internal/notifier` | Telegram 推送与 Bot 录入 | `New`、`Send`、`Caption`、`RunBot`、`TestTelegram` |
| `internal/parser` | 分享文本 / CSV 解析 | `ParseText`、`ParseCSV`、`SplitTitle`、`CleanTitle` |
| `internal/netutil` | 代理 URL 与出网客户端、代理探测 | `ProxyURL`、`HTTPClient`、`TestProxy` |
| `internal/ratelimit` | 令牌桶与信号量 | `Limiter`、`Semaphore` |
| `internal/web` | 前端产物嵌入 | `FS()`（无产物返回 nil）、`HTTPFS()` |

## 4. 数据模型

`links` 24 列，`share_id` UNIQUE（去重键）。状态机：

```
                    ┌──────────────┐
   导入 / Bot ─────>│ pending      │ 分享状态异常则直接进 review
                    └──────┬───────┘
                    scraping│
              ┌────────────┼─────────────┐
              ▼            ▼             ▼
          scraped      unmatched       failed
              │        （低可信→review）
        push   │
     ┌─────────┴──────────┐
     ▼                    ▼
  pushed             push_failed
```

- 界面按「状态集合」分组，定义在 `internal/model/link.go:66-73`：`PendingScrape`（刮削队列）、`ScrapedOnly`（资源库）、`PushQueue`（推送页）。新增状态时这三处和 `web/src/lib/api.ts` 的 `statusMeta` 必须同步。
- 门户收录口径是「刮削成功即入库」：`internal/store/public.go` 的 `publicWhere` = `status IN ('scraped','pushed','push_failed')`，与 `ScrapedOnly` 同集合（SQL 里是字面量，改状态时两处一起改）。推送只是发布到频道，不再决定门户可见性；`ListPublic`/`PublicStats`/`GetPublic` 都走这个常量，`sitemap.xml` 与 `/?id` 注入因此自动跟着变。门户只暴露 `model.PublicLink` 的最小字段集（不含内部状态与错误）。
- `users` / `sessions` / `login_attempts` / `schema_meta`：会话外键 `ON DELETE CASCADE`，删账号即清会话。

## 5. 流水线

- 任务粒度是单条记录：`job{kind, linkID, batch}` 投进 `jobs`（缓冲 1024）。
- 每个 kind 一份 `Progress`；`Enqueue` 时若不运行则自增批次号，`feed` goroutine 发现批次号变了就停止投喂——这是 `Cancel` 与重复点击的实现方式。在途任务靠 `stale()` 判定，结果会被丢弃（不计入进度）。
- 单任务上下文超时 60 秒。
- **没有重试**：`config.Pipeline.MaxRetry` / `RetryDelay` 当前没有任何引用，失败即落到 `failed` / `push_failed`，由界面上的「重试」按钮人工重投。改这里前先确认是要自动重试还是继续手动。
- 限流分布在服务层：TMDB 令牌桶在 `scraper.New` 内按 `rate_limit.tmdb` 构造；Telegram 发送间隔在 `notifier.New` 内按 `rate_limit.telegram` 构造；光鸭探测用 `pipeline` 里的信号量（`SetGuangyaLimit` 热更新）。
- 配置保存后 `main.build` 会重建 scraper/notifier 并 `UpdateComponents` + 重启 Bot。

## 6. 鉴权与安全

- 密码：PBKDF2-HMAC-SHA256，210,000 轮、16 字节盐、32 字节派生密钥，串成 `pbkdf2$iter$salt$key`（base64url）。校验走常量时间比较；账号不存在时用固定假哈希做**等时校验**，避免按耗时枚举账号。
- 口令策略：8–128 字节、`weakPasswords` 黑名单、大小写/数字/符号至少两类（`internal/api/auth.go:354-403`）。用户名 `^[a-zA-Z0-9_.-]{3,32}$`。
- 会话：32 字节随机令牌下发为 Cookie `gy_session`（HttpOnly、SameSite=Lax、Path=/、7 天），库里只存 HMAC-SHA256 摘要（密钥来自 `data/session.key`）。登出、改密、被管理员重置密码、账号停用都会立即让旧会话失效。
- `Secure` 标记：默认按 TLS / `X-Forwarded-Proto` 判定，可用 `server.secure_cookie` 强制 true/false（纯 HTTP 内网部署写 `false`）。
- 登录锁定：按「真实 IP」和「用户名（小写）」两个维度，连续 5 次失败锁 5 分钟，状态落 `login_attempts` 表（重启不丢），进程内 30 秒缓存，命中回 `429`。
- CSRF：全局 `requireSameOrigin` 拦截所有 POST/PUT/PATCH/DELETE 的跨站请求（无 Origin 且无 Referer 时放行，兼容 curl）。
- 角色：`admin` 独占 `/api/settings*`、`/api/users*`；`operator` 连入口都看不到（`adminOnly` 标签按 `me.role` 过滤）。最后一个启用的管理员不允许降级或删除（`store.ErrLastAdmin`）。
- 响应头：`X-Content-Type-Options: nosniff`、`X-Frame-Options: DENY`、`Referrer-Policy: no-referrer`、`Permissions-Policy`、CSP（`script-src 'self'`、`connect-src 'self'`，所以前端不能引 CDN、不能写内联脚本）、仅 TLS 直连时下发 HSTS、`/api/*` 一律 `Cache-Control: no-store`。
- 请求体：普通接口 1MB，导入接口 32MB。
- SQL：全部占位符；LIKE 关键词经 `likeEscape` 转义并配 `ESCAPE '\'`；门户排序走 `store/public.go` 的 `publicOrder` 白名单，后台列表 `store.List` 固定 `id DESC`。
- CSV 导出：带 BOM，公式前缀加 `'` 防表格公式注入；门户链接经 `safeLinkURL` 只允许 http/https；TMDB 图片域名走白名单防 SSRF。

## 7. 配置与密钥

`config.Load` = `Default()` 叠加 YAML（文件不存在不报错），再叠加环境变量。优先级：**env > YAML**。

| 段 | 关键项 |
| --- | --- |
| `server` | `addr`、`base_path`、`secure_cookie`（`*bool` 三态） |
| `storage` | `dsn`、`data_dir`（会话密钥也在这）、`poster_dir` |
| `tmdb` | `api_key`（32 位 Key 或 `eyJ` 开头的 Read Token 自适应）、`language`、`image_base`、`poster_size`、`min_score`、`cache_poster` |
| `telegram` | `token`、`chat_id`（`ChatID` 同时接受数字与 `@频道名`）、`enable_bot`、`silent` |
| `netdisk` | `guangya_hosts`、`user_agent`、`cookie`、`timeout` |
| `pipeline` | `workers`、`auto_push`、（`max_retry`/`retry_delay` 目前是死配置） |
| `proxy` | `enable`、`addr`（裸 `host:port` 按 socks5 处理）、`username`、`password` |
| `seo` | `google_verification`、`base_url`、`site_name` |
| `rate_limit` | `tmdb.per_second/burst/shared_credentials`、`telegram.interval_ms/channel_only/shared_credentials/paid_broadcast`、`guangya.concurrency`；`Effective*` 会夹到官方下限（群组 3000ms、会话 1000ms、TMDB ≤40/s） |

环境变量：`TMDB_API_KEY`、`TG_BOT_TOKEN`、`TG_CHAT_ID`、`GUANGYA_COOKIE`、`PROXY_ADDR`、`PROXY_USERNAME`、`PROXY_PASSWORD`、`SEO_BASE_URL`；`ADMIN_USERNAME`/`ADMIN_PASSWORD` 只在用户表为空时被 `ensureAdmin` 使用。

后台「设置」页写配置走 `config.Save`：**整份 YAML 重新序列化**（0600），手工加进去的注释和自定义排版会丢失——改默认值请改 `config.Default()`，别指望注释留在 `config.yaml` 里。保存后调用 `reload` 回调即时生效并重启 Bot。

`config.yaml` 与 `data/` 都在 `.gitignore` 里，含真实密钥，**不要提交**。

## 8. SEO 与 Google 录入

- `robots.txt` / `sitemap.xml` / `GET /api/structured-data[/:id]` 由服务端动态生成（`internal/api/seo.go`）。三者的可见范围都走 `store.publicWhere`（刮削成功即收录），不另判推送状态。
- sitemap 数据来自 `store.Sitemap(offset, limit)`（只取 `id` + `updated_at`，比 `ListPublic` 轻）。单文件上限 `maxSitemapURLs = 5000`：超出后 `/sitemap.xml` 返回 `sitemapindex`，分片回到同一 handler 的 `?part=<n>`，**不会再静默丢掉最老的记录**。`lastmod` 用 `updated_at`（重刮/改状态都会刷新），首页条目的 `lastmod` 用全站 `MAX(updated_at)`。`robots.txt` 与 `sitemap.xml` 都带 `Cache-Control: public, max-age=3600`。
- `updated_at` 是 DATETIME 列，SQL 里一律 `CAST(... AS TEXT)` 再扫进 `string`，避免驱动按时间类型解析时踩格式差异。
- 域名解析顺序：`seo.base_url` 优先，否则按请求 TLS 与 `Host` 推断。**放在反向代理或多域名环境里必须固定 `base_url`**，否则 sitemap 会写成内网地址。
- `WriteIndex` 在返回首页 HTML 时把 `<meta name="google-site-verification">` 和 `<link rel="canonical">` 注入 `<head>`；请求带 `?id=<n>` 且记录已收录（刮削成功）时，再注入该资源自己的 `title` / `description` / `og:` / `twitter:` / JSON-LD，并删掉模板里的站点级同类标签，避免同名标签重复。必须走服务端注入：CSP 拦内联脚本，爬虫也读不到前端 JS 补的标签。`/admin*`、`/login`、`/setup` 不生成 canonical。
- 门户把页面状态绑到查询串（`?id`/`?q`/`?media_type`/`?year`/`?sort`），`PortalView` 挂载时 `applyURL()` 回填、`popstate` 时重新回填：打开详情用 `pushState`（返回键先关详情），改筛选只 `replaceState`（不塞历史）。所以 sitemap 里的 `/?id=<n>` 是能被爬虫打开的真实页面，而不是一串回落到空壳首页的 URL。
- 验证码只允许 `^[A-Za-z0-9_-]{10,120}$`，不合法直接 400——这是防 HTML 注入的硬约束，放宽前想清楚。
- 后台 SEO 页（`web/src/views/SEOView.vue`）：填验证码、复制 meta 标签、拉一次首页原始 HTML 自检标签是否生效、给出 Search Console 深链与提交步骤。
- 录入流程：Search Console「添加资源」→ 选**网址前缀**（选「网域」只能走 DNS TXT，meta 无效）→ 复制 HTML 标签里的 content → 填进本页保存 → 回 Console 点验证 → 提交 `https://<域名>/sitemap.xml`。

## 9. 前端

- **没有 vue-router**：`App.vue` 按 `location.pathname` 选页面（`/setup`、`/login`、`/admin*`、其余走门户）；后台内部页签是 `AdminShell` 的 `ref<Tab>` + `v-if`。新增页面要同时改 `App.vue` 分支、`AdminShell` 的 `Tab` 联合类型、`allTabs`、模板里的 `v-else-if` 四处。
- `navSeq` 每次跳转换号，作为 `:key` 强制重挂载列表页，用来重置筛选并重新拉数据——去掉它会让「再点一次回到默认筛选」失效。
- 轮询：`ScrapeView` / `PushView` 用 1000ms `setInterval` 拉 `/api/tasks?kind=`，`running=false` 自动停；任务发起点必须重启轮询。
- 会话：401 时 `location.replace('/login?next=...')`，回跳目标只允许 `/admin*`。Cookie 认证，前端不存令牌。
- `web/src/lib/api.ts` 是唯一的取数入口，接口类型与后端 JSON tag 一一对应；改后端字段记得同步。
- 样式约定：只在 `style.css` 的 `@theme` 定义一次 token，组件里用 `text-[var(--color-*)]` 引用，不写裸色值；字体统一系统默认（`-apple-system / PingFang SC`）。
- `web/src/lib/utils.ts` 的 `safeExternalUrl` 是所有外链/头像 URL 的协议白名单，别绕过它。

## 10. 构建、调试、测试

```bash
make web                      # npm run build → 拷进 internal/web/dist
go build -o guangya-go ./cmd/server   # 必须再编一次，否则前端改动看不见
./guangya-go -config config.yaml      # 监听 8090
```

- 日常联调：`cd web && npm run dev`（5173，代理 `/api` 到 127.0.0.1:8090），同时跑着后端二进制即可，不用反复 `make web`。
- 只有 `make build` 会把产物写成 `bin/guangya`，裸二进制在 `./guangya-go`。
- 类型检查没有进构建脚本（`npm run build` 不跑 `vue-tsc`），改动 TS 后手动 `cd web && npx vue-tsc --noEmit`。
- 测试：`go test ./...`，目前仅 `internal/parser` 与 `internal/scraper` 有测试；`internal/api`、`internal/store`、`internal/pipeline` 无测试，改这些地方请用真实的隔离实例验证。
- 打接口：`curl -c jar -X POST localhost:8090/api/auth/login -d '{"username":"...","password":"..."}'`，后续 `-b jar`。POST 必须同源（Origin/Referer）否则 403。
- 端口占用：`lsof -ti:8090 | xargs kill -9`。
- Telegram Bot 单实例：同一 Token 只能有一个 getUpdates 消费者，本地再起一个实例会看到 `Conflict: terminated by other getUpdates request`。
- 干净退出才会把 WAL 合并回主库；热拷贝 `guangya.db` 前先停服务，或连 `-wal`/`-shm` 一起拷。

## 11. 发布与部署

- `Makefile`：`build` / `run` / `import FILE=` / `vet` / `tidy` / `clean` / `web`。
- `Dockerfile` 三阶段：`node:22-alpine` 出前端 → `golang:1.26-alpine` 交叉编译（`CGO_ENABLED=0`、`-trimpath -s -w`）→ `alpine:3.22` 运行（`ca-certificates`/`tzdata`、`USER 1000`、`EXPOSE 8090`、配置在 `/app/data/config.yaml`）。构建参数 `BUILD_PROXY`（只认 HTTP 端口）、`NPM_REGISTRY`、`GOPROXY`；首行刻意不写 `# syntax=`，避免 BuildKit 先去 Docker Hub 拉前端镜像。
- `docker-compose.yml`：单服务 `app`，`pull_policy: always` + `image: ghcr.io/fyfq/guangyago:latest`，`8090:8090`，`./data:/app/data`，`host-gateway` 映射 `host.docker.internal`，`/api/health` 健康检查。
- `.github/workflows/docker-image.yml`（唯一工作流）：push main / `v*` tag / 手动触发 → QEMU+Buildx 多架构（amd64+arm64）推 GHCR。
- 裸机：交叉编译出的单文件无任何依赖（纯 Go SQLite + 嵌入前端），配 `guangya.service`（`DynamicUser`，状态目录 `/var/lib/guangya/data/`）。
- 公网部署务必挂 HTTPS 反向代理；`PROXY_ADDR` 指向宿主机代理时写 `host.docker.internal:7890`，写 `127.0.0.1` 指向的是容器自己。

## 12. 维护清单

改这些东西时对应的连带修改点：

| 动它 | 顺手检查 |
| --- | --- |
| 新增/改名 `links.status` | `model/link.go` 三个状态集合、`api.applyStatusFilter`、`export.go` 的 `stats` 顺序、`web/src/lib/api.ts` 的 `statusMeta`、各视图的状态筛选 |
| 新增配置项 | `config.Default()`、`config.example.yaml`、`api.settings` 响应、`settingsRequest`/`updateSettings`、`web/src/lib/api.ts` 的 `Settings`/`SettingsInput`、`SettingsView.vue` |
| 新增接口 | `api.Register` 的分组（是否需要 `requireAdmin`）、`web/src/lib/api.ts` 方法、`docs/api.md` |
| 新增前端页面 | `App.vue` 路由分支、`AdminShell` 的 `Tab`/`allTabs`/模板、`docs/api.md` 若涉及新接口 |
| 改 schema | `store.migrateSQL` 幂等（`IF NOT EXISTS`）、`schemaVersion` 自增、存量库需数据迁移时要写显式迁移而不是只改建表串 |
| 改推送文案 | `notifier.Caption` 与 `GET /api/links/:id/caption` 同源，界面预览不用改 |
| 改 SEO 注入 | `seo.go` 的 `headTags`/`WriteIndex`，注意 CSP 与 `base_url` |

已知的历史坑（别重复踩）：

- 「设置」页曾把脱敏后的代理地址回填再保存，导致配置被掩码覆盖。现在 `addr`/`username` 一律明文返回（`has_password` 只表示有没有设密码）。**新增敏感字段时沿用这个策略，不要给可回写的字段加脱敏**。
- 前端改动不生效通常是忘了 `make web` 之后还要 `go build`。
- `web/index.html` 里放静态 `<link rel="canonical" href="/">` 会让 Vite 构建报 `EISDIR`；canonical 现在由服务端注入，别加回去。
- 推送后一直显示「排队中」的问题尚未定位（怀疑 worker 未消费 / notifier 为 nil / 限流阻塞），排查从 `pipeline.worker`、`Progress.Current` 与 `pip.log` 的 `未配置 Telegram Bot Token` 告警入手。
- 本仓库可能同时被多个会话编辑；提交前 `git status` 确认工作区内容，`git diff` 检查有没有别人的改动被一起带进来。

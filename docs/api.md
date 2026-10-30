# 光鸭 GO 接口文档

覆盖 `internal/api` 当前注册的全部路由。最后核对 2026-10-30（新增同源海报接口）。改接口请同步更新本文（维护清单见 [docs/dev-guide.md](dev-guide.md#12-维护清单)）。

## 约定

- **基址**：所有 JSON 接口都在 `/api` 下，前端 `api.ts` 会自动补前缀。示例里的 `localhost:8090` 是默认监听地址。
- **认证**：会话 Cookie `gy_session`（HttpOnly / SameSite=Lax / Secure 视 `server.secure_cookie` 而定 / 7 天），由 `POST /api/auth/login` 下发。除下表标注「公开」的接口外，一律需要登录；标注「管理员」的还需要 `role=admin`。
  - `server.secure_cookie` 省略时按当次请求是否 TLS 自动判定；反代在边缘终止 TLS 时请显式设为 `true`，纯本地 http 调试可设 `false`。
  - 未登录 → `401 {"error":"未登录"}`；Cookie 有效但会话过期/被撤销 → `401 {"error":"登录已失效"}`；角色不足 → `403 {"error":"需要管理员权限"}`。
- **CSRF**：所有 `POST/PUT/PATCH/DELETE` 都做了同源校验。浏览器跨源 XHR 会带 `Origin`，不同源直接 `403 {"error":"跨站请求已被拒绝"}`；`curl` 不带 Origin/Referer 时放行。
- **请求体**：普通接口上限 1MB，`/api/links/import` 32MB。`/api/branding` 不设通用上限，由 handler 按素材类型各自限流（Logo 512KB / 站点图标 256KB），否则「图片过大」会以传输错误呈现。JSON 接口 `Content-Type: application/json`。
- **响应**：一律 `Cache-Control: no-store`。错误统一 `{"error": "中文提示"}`，HTTP 状态码见各接口。服务端内部错误只回 `500 {"error":"服务器内部错误"}`，细节进日志。
- **状态码**：`400` 参数/校验不合法 · `401` 未登录 · `403` 权限或 CSRF · `404` 记录不存在（或 `NoRoute` 下的 `/api/*`）· `409` 用户名冲突 · `429` 登录锁定 · `503` 依赖组件未就绪（管道/TMDB Key/配置未加载）。

### 数据结构

`Link`（后台完整记录）

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | int64 | 主键 |
| `share_id` | string | 分享唯一键，去重依据 |
| `url` / `code` / `source` | string | 分享链接、提取码、来源（默认 `guangya`） |
| `raw_title` | string | 原始文件名 |
| `title` / `original_title` | string | 刮削后片名 / TMDB 原名 |
| `year` | int | 年份 |
| `media_type` | string | `movie` \| `tv` \| 空 |
| `tmdb_id` | int64 | TMDB 条目 ID |
| `overview` / `genres` | string | 简介 / 逗号分隔类型 |
| `vote_average` | float | 评分 |
| `runtime` | int | 分钟 |
| `poster_url` / `poster_path` | string | 海报地址与本地缓存路径 |
| `shared_at` / `share_status` | string | 分享时间 / 光鸭分享状态原文 |
| `status` | string | `pending` `review` `scraping` `scraped` `unmatched` `pushed` `push_failed` `failed` |
| `error` | string | 最近一次失败原因 |
| `tg_message_id` | int64 | 推送后的 Telegram 消息 ID |
| `created_at` / `updated_at` | RFC3339 | 时间戳 |

`PublicLink`（门户最小字段集）：`id, title, original_title, year, media_type, tmdb_id, overview, genres, vote_average, runtime, poster_url, url, code, shared_at, link_count, links`。同一个 TMDB 条目（`media_type + tmdb_id`）下的多条分享在门户里合并成一条资源：`link_count` 是链接数，`links` 是全部 `{url, code, shared_at}`（最近分享在前），顶层 `id/url/code/shared_at` 取自最近分享的那条。未刮削出 TMDB ID 的记录按自身 ID 独立成组。`poster_url` 优先返回本地缓存海报的同源路径（`/posters/movie-129.jpg`），缓存不存在时才回退刮削时记录的 TMDB 远程地址；两者都是浏览器可直接加载的地址。

`User`：`id, username, display_name, role(admin|operator), enabled, last_login_at, created_at`（不含密码字段）。

`Branding`：`site_name, logo_url, icon_url`。两个素材地址是同源相对路径（`/branding/logo.png?v=1730246400`），未配置时为空串；`?v=` 取文件修改时间，换图后自动换址绕过浏览器缓存。

`TaskProgress`：`running, kind(scrape|push), total, done, failed, current, error`。

`ImportResult`：`total, inserted, skipped, failed[{line,raw,err}], ids[]`。

---

## 1. 公开接口

### `GET /api/health`
健康检查。→ `{"status":"ok"}`。可用作反向代理与容器探活。

### `GET /api/setup/status`
安装状态（用户表是否为空）。

```json
{ "installed": false }
```

### `POST /api/setup`
首次安装：创建管理员，可同时写入 TMDB Key 与对外访问地址。**用户表非空时固定拒绝**，无自恢复入口，请在页面提示下走「用户管理」。

| 字段 | 必填 | 约束 |
| --- | --- | --- |
| `username` | 是 | `^[a-zA-Z0-9_.-]{3,32}$` |
| `password` | 是 | 8–128 字节、非常见弱口令、字符类别≥2 |
| `display_name` | 否 | ≤40 字节，默认「管理员」 |
| `public_url` | 否 | 对外访问根地址，写入 `server.public_url`；非空时必须以 `http://` 或 `https://` 开头并带主机名，尾部 `/` 会被去掉。向导默认回填 `location.origin`，反代/隧道场景务必保留 |
| `tmdb_api_key` | 否 | 非空则写入 `config.yaml` 并热加载 |

→ `200 {"ok":true,"user_id":1}`；已安装 → `403 {"error":"系统已安装，请勿重复操作"}`；校验失败 → `400 {"error":"对外访问地址需要以 http:// 或 https:// 开头"}` 等。

### `POST /api/auth/login`

```json
{ "username": "admin", "password": "..." }
```

→ `200` 返回 `User`，并 `Set-Cookie: gy_session=...`。
账号不存在、被停用、密码错误一律 `401 {"error":"用户名或密码错误"}`（等时校验，不泄露账号是否存在）。
同一 IP 或同一用户名连续 5 次失败 → `429 {"error":"尝试次数过多，请 5 分钟后再试"}`。

### `GET /api/portal/links`
公开门户列表。收录口径是「刮削成功即入库」：`status IN ('scraped','pushed','push_failed')`，推送只是发布到 Telegram 频道，不影响门户可见性。同一 `media_type + tmdb_id` 的多条分享合并为一条，`total` 是合并后的资源数；排序按组内最近分享的那条计算。

| 查询参数 | 默认 | 说明 |
| --- | --- | --- |
| `q` | — | 关键词，超 64 字符按字符截断 |
| `media_type` | — | 仅 `movie` / `tv`，其他值 → `400 {"error":"参数不正确"}` |
| `year` | 全部 | 非法值当作「全部年份」，不报错 |
| `sort` | `new` | `new`（最新分享）\| `release`（上映时间）\| `rating` \| `year`，其余回落 `new`。`release` 按 TMDB 上映/首播日期倒序，存量记录只有年份时退到当年 1 月 1 日参与同一套顺序，两者都缺的排最后 |
| `limit` | 24 | 1–60，越界回落 24 |
| `offset` | 0 | 负数归 0，上限 10000 |

→ `{"total": 123, "items": [PublicLink...]}`

### `GET /api/portal/stats`
筛选栏计数：分类数忽略分类条件、年份数忽略年份条件，`library_total` 不随筛选变化。计数单位与列表一致，是 TMDB 合并后的资源数。参数同上（除 `sort/limit/offset`）。

```json
{ "total": 42, "movies": 30, "tv": 12, "library_total": 318,
  "years": [ { "year": 2024, "count": 7 } ] }
```

### `GET /api/portal/link/:id`
单条已收录资源（`PublicLink`），供门户 `/?id=<n>` 深链直接打开详情。传组内任意一条分享的 `id` 都返回整组资源与全部链接。未收录（还没刮削成功）→ `404 {"error":"资源不存在"}`；`id` 非法 → `400`。

```json
{ "id": 42, "title": "千与千寻", "original_title": "千と千尋の神隠し", "year": 2001,
  "media_type": "movie", "tmdb_id": 129, "overview": "…", "genres": "动画 / 奇幻",
  "vote_average": 8.5, "runtime": 125, "poster_url": "/posters/movie-129.jpg",
  "url": "https://www.guangyapan.com/s/…", "code": "abcd", "shared_at": "2026-10-27 16:37:34",
  "link_count": 2,
  "links": [
    { "url": "https://www.guangyapan.com/s/…", "code": "abcd", "shared_at": "2026-10-27 16:37:34" },
    { "url": "https://www.guangyapan.com/s/…", "code": "efgh", "shared_at": "2026-09-01 08:00:00" }
  ] }
```

> 有本地缓存时 `poster_url` 是 `/posters/movie-129.jpg`；缓存缺失时才回退 `https://image.tmdb.org/…`。

### `GET /robots.txt`
纯文本（`Cache-Control: public, max-age=3600`）。放行 `/`，屏蔽 `/admin`、`/login`、`/setup`、`/api/`，并声明 Sitemap 地址。

```
User-agent: *
Allow: /
Disallow: /admin
Disallow: /login
Disallow: /setup
Disallow: /api/

Sitemap: https://gy.example.com/sitemap.xml
```

### `GET /sitemap.xml`
XML（`Content-Type: application/xml`，`Cache-Control: public, max-age=3600`）。收录首页与全部已收录记录（`/?id=<n>`，即刮削成功的记录）。单文件上限 5000 条：超出后 `GET /sitemap.xml` 改吐 `sitemapindex`，分片地址为 `/sitemap.xml?part=<n>`（part 从 0 起，首页条目只在 part 0 里）。域名取 `seo.base_url`，未配置则按请求头推断（`X-Forwarded-Proto=https` 或 TLS 为 https）。

`lastmod` 取 `links.updated_at`（重新刮削、改状态都会刷新）；首页的 `lastmod` 取全站最近更新时间。

```xml
<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://gy.example.com/</loc><lastmod>2026-10-02</lastmod><changefreq>daily</changefreq><priority>1</priority></url>
  <url><loc>https://gy.example.com/?id=42</loc><lastmod>2026-10-01</lastmod><changefreq>monthly</changefreq><priority>0.8</priority></url>
</urlset>
```

### `GET /api/structured-data`
站点级 Schema.org（`WebSite` + `SearchAction`），由门户注入 `<head>`。`name` 取 `seo.site_name`，缺省「光鸭 GO」。

```json
{
  "@context": "https://schema.org",
  "@type": "WebSite",
  "name": "光鸭 GO",
  "url": "https://gy.example.com",
  "description": "光鸭 GO 资源库 - 网盘资源分享平台",
  "potentialAction": {
    "@type": "SearchAction",
    "target": "https://gy.example.com/?q={search_term_string}",
    "query-input": "required name=search_term_string"
  }
}
```

### `GET /api/structured-data/:id`
单条资源的 Schema.org（`media_type=tv` → `TVSeries`，否则 `Movie`）。仅已收录（刮削成功）记录，记录不存在 → `404 {"error":"资源不存在"}`；`id` 非数字 → `400 {"error":"非法 ID"}`。

```json
{
  "@context": "https://schema.org",
  "@type": "TVSeries",
  "name": "致命女人",
  "url": "https://gy.example.com/?id=42",
  "datePublished": "2019-08-15",
  "alternateName": "Why Women Kill",
  "description": "……",
  "image": "https://image.tmdb.org/t/p/w500/xxx.jpg",
  "year": 2019,
  "aggregateRating": { "@type": "AggregateRating", "ratingValue": 7.8, "bestRating": 10, "worstRating": 0 },
  "duration": "PT50M",
  "genre": ["犯罪", "剧情"],
  "sameAs": "https://www.themoviedb.org/tv/20851"
}
```

> 可选字段按数据有无出现：`alternateName` 仅在原名与片名不同时；`description` / `image` / `year` / `aggregateRating` / `duration` / `genre` / `sameAs` 在对应值 >0 或非空时才输出。`datePublished` 由 `shared_at` 归一成 ISO 日期（`2006-01-02`），`shared_at` 为空时整个字段省略。

### `GET /?id=<n>`（门户深链）
首页 HTML 由服务端渲染，`?id=` 命中已收录记录（刮削成功，含待推送与推送失败）时，`</head>` 前注入该资源自己的标签，并删除模板里的站点级 `title` / `description` / `og:` / `twitter:`，保证同名标签全局唯一：

```html
<link rel="canonical" href="https://gy.example.com/?id=42" />
<title>千与千寻（2001） · 光鸭 GO</title>
<meta name="description" content="…" />
<meta property="og:type" content="video.movie" />
<meta property="og:title" content="千与千寻（2001）" />
<meta property="og:image" content="https://image.tmdb.org/t/p/w500/xxx.jpg" />
<meta name="twitter:card" content="summary_large_image" />
<script id="structured-data" data-item-id="42" type="application/ld+json">{ "@type": "Movie", … }</script>
```

- `id` 缺失、非法或记录还没刮削成功时，退回站点级标签，页面仍是可用的列表页。
- 同一 TMDB 的多条分享在详情里合并展示，组内任意一条分享的 `id` 都会渲染同一部资源。
- 标签值全部经 `html.EscapeString` 转义，JSON-LD 由 `json.Marshal` 转义尖括号，不引入内联脚本。
- 门户 URL 与页面状态双向绑定：`?id` 详情、`?q` 关键词、`?media_type`、`?year`、`?sort`（打开详情用 `pushState`，改筛选只 `replaceState`）。前端挂载时按 URL 回填，`data-item-id` 命中当前资源时不再重复请求 `/api/structured-data/:id`。
- `canonical` 对带 `id` 的请求只保留 `id`，避免 `?id=42&q=x` 这类组合被规范到列表页；`sitemap.xml` 的 `lastmod` 与 `datePublished` 用同一个 `schemaDate`。

---

### `GET /api/branding`
站点品牌信息，**公开**（门户/后台/登录页的头部与浏览器标签都要用）。

```json
{ "site_name": "光鸭影视库", "logo_url": "/branding/logo.png?v=1730246400", "icon_url": "" }
```

- `site_name` 来自 `seo.site_name`，留空回落到默认值 `光鸭 GO`。
- 首页原始 HTML 的 `title` / `description` / `og:` / `twitter:` 由服务端把模板里的 `{{SITE_NAME}}` 占位符替换成站点名（先 HTML 转义再拼，防止闭合 `<title>` 往 head 里塞标签）。
- 配了站点图标时额外注入 `<link rel="icon" href="/branding/icon.png?v=…" />`；没配就不注入，交给浏览器默认。

---

## 2. 账号（需登录）

### `GET /api/auth/me`
→ 当前 `User`。前端用它判断角色与是否跳登录。

### `POST /api/auth/logout`
删除会话并清 Cookie。→ `{"ok":true}`

### `POST /api/auth/password`

```json
{ "old_password": "...", "new_password": "..." }
```

→ `{"ok":true}`，同时**作废该账号全部会话**并清 Cookie。
`400`：`请求格式不正确` / 新密码不合规则给出具体原因 / `当前密码不正确`。

---

## 3. 资源管理（需登录）

### `GET /api/links`

| 参数 | 说明 |
| --- | --- |
| `status` | 单值、逗号分隔多值，或语义集合 `scraped_set`（资源库）/ `pending_set`（刮削队列）/ `push_set`（推送页）；空或 `all` 不过滤 |
| `q` | 匹配片名、原名、原始文件名、链接、错误信息（LIKE 转义） |
| `limit` | 默认 50，上限 200，越界回落 50 |
| `offset` | 默认 0 |

→ `{"total":N,"items":[Link...]}`，按 `id DESC`（等价于入库顺序倒序）。

### `GET /api/links/:id`
→ `Link`；不存在 `404 {"error":"记录不存在"}`；`id` 非数字 `400 {"error":"非法 ID"}`。

### `DELETE /api/links/:id`
→ `{"deleted":<id>}`

### `POST /api/links/batch-delete`
`{"ids":[1,2,3]}` → `{"deleted":3}`（成功删除的条数）。`ids` 为空 → `400 {"error":"ids 不能为空"}`。

### `GET /api/stats`
后台总览。固定输出全部 8 个状态，计数为 0 的也在；另给 `total`（库内记录总数，含未列出的异常状态），前端不必自己把各状态相加。

```json
{ "total": 319,
  "pending":4, "review":0, "scraping":0, "scraped":12,
  "unmatched":1, "pushed":300, "push_failed":2, "failed":0 }
```

### `POST /api/links/text`
提交光鸭分享文本（`{"text":"光鸭云盘用户给你分享了..."}`），解析后落库。`text` 为空 → `400 {"error":"text 不能为空"}`。→ `ImportResult`。

### `POST /api/links/import`
两种用法：multipart 上传字段 `file`（按 CSV 解析）；或直接提交请求体文本，服务端按首行是否含「链接」/逗号数判定 CSV 还是分享文本。缺文件且空体 → `400 {"error":"缺少 file 或请求体"}`。→ `ImportResult`（`ids` 为本次新增记录，供「立即刮削这 N 条」）。

去重规则：`share_id` 已存在则计入 `skipped`。入库状态：分享状态非空且不为「成功」→ `review`，否则 `pending`。

### `GET /api/links/export`
CSV 下载（带 BOM，公式前缀加 `'`），支持 `status` 与 `q`，最多 10000 行。`Content-Disposition: attachment; filename="guangya-links.csv"`。列：ID、文件名、片名、年份、TMDB ID、链接、提取码、分享时间、分享状态、处理状态、推送消息ID。

### `GET /api/links/:id/caption`
推送卡片文案预览，与实际推送同源。→ `{"html":"...","text":"..."}`（`html` 是 Telegram 的 `<b>`/`<a>` 富文本标记，`text` 是去标签纯文本）。

### `POST /api/links/:id/tmdb`
手动补 TMDB ID。

```json
{ "tmdb_id": 20851, "media_type": "tv", "scrape": true }
```

`media_type` 非 `tv` 一律按 `movie`；`tmdb_id <= 0` → `400 {"error":"需要有效的 tmdb_id"}`。写入后状态重置为待刮削，`scrape=true` 时顺带入队。→ `{"ok":true}`

---

## 4. 流水线（需登录）

### `POST /api/scrape`
入队刮削。三种选择方式，优先级从左到右：

```json
{ "ids": [1,2] }                       // 指定记录
{ "all": true }                        // 全部 pending
{ "all": true, "statuses": ["failed","unmatched"] }  // 按状态重试
```

未匹配到记录 → `400 {"error":"没有符合状态的记录"}`；管道未启动 → `503`。→ `{"queued":12}`

### `POST /api/push`
同 `scrape`，字段换成 `{"ids":[]}` / `{"scraped":true}`（全部待推送）/ `{"statuses":["push_failed"]}`（重推失败项）。→ `{"queued":N}`；无记录 → `400 {"error":"没有待推送的记录"}`。

### `POST /api/links/:id/scrape` · `POST /api/links/:id/push`
单条入队。→ `{"queued":1}`

### `GET /api/tasks?kind=scrape|push`
任务进度。带 `kind` 返回该类型的 `TaskProgress`；不带则返回最近一次活动批次（两类都空闲时回最后写入的那份）。管道未启动返回全零对象。

前端以 1 秒间隔轮询，`running=false` 时停止——`current` 是界面上的「排队中/正在处理…」文案来源。

### `POST /api/tasks/cancel`
`{"kind":"scrape"}`，`kind` 留空表示两类都取消。→ `{"canceled":7,"kind":"scrape"}`

语义：只让**排队中**的任务不再执行（自增批次号使旧 feed 失效）；已在途的那条会跑完，但结果被丢弃、不计入进度。

### `GET|POST /api/tmdb/lookup`
TMDB 预览，**只读不写库**，用于补 ID 时先核对。参数可放 JSON 体或查询串：

| 输入 | 行为 |
| --- | --- |
| `{"tmdb_id":20851,"media_type":"tv"}` | 精确查 |
| `{"query":"致命女人","year":2019}` | 按片名搜，最多 8 条候选 |

→ `{"mode":"id","item":Candidate,"items":[Candidate]}` 或 `{"mode":"search","items":[Candidate]}`。
`Candidate`：`media_type, tmdb_id, title, original_title, year, overview, genres, vote_average, runtime, poster_url, site_url, media_label`。
缺 Key → `400 {"error":"还没有配置 TMDB API Key，无法查询"}`；两者都给空 → `400 {"error":"需要提供 tmdb_id 或 query"}`。

---

## 5. 设置与用户（管理员）

非管理员访问本节全部接口 → `403 {"error":"需要管理员权限"}`。

### `GET /api/settings`
返回配置**就绪状态**，不回密钥明文；代理地址与用户名回明文（界面需要回填再保存，脱敏会把配置写坏），仅口令以 `has_password` 表示。

```json
{
  "tmdb_configured": true, "telegram_configured": true,
  "chat_id": "@realguangya", "auto_push": true, "workers": 2,
  "public_url": "https://gy.example.com",
  "proxy": { "enable": true, "addr": "host:7890", "username": "", "has_password": false },
  "rate_limit": {
    "tmdb":     { "enable": true, "per_second": 4, "burst": 4, "shared_credentials": false },
    "telegram": { "enable": true, "interval_ms": 3100, "channel_only": false, "shared_credentials": false, "paid_broadcast": false },
    "guangya":  { "enable": true, "concurrency": 2 }
  },
  "seo": { "google_verification": "...", "base_url": "https://gy.example.com", "site_name": "光鸭 GO" }
}
```

### `POST /api/settings`
部分更新，未出现的段/字段保持原值。

```json
{
  "tmdb_api_key": "...",        // 空串忽略
  "tg_token": "...",            // 空串忽略
  "chat_id": "@channel",        // 空串忽略；支持数字 ID 或 @频道名
  "auto_push": true,            // 省略则不改；刮削成功后自动推送，已推送记录重刮不重复推送
  "public_url": "https://gy.example.com",  // 省略则不改；空串清空（回到按请求 Host 判断）
  "save_to_file": true,         // 是否写回 config.yaml（否则只活到本次进程）
  "proxy": { "enable": true, "addr": "host:7890", "username": "", "password": "" },
  "rate_limit": {
    "tmdb":     { "enable": true, "per_second": 4, "burst": 4, "shared_credentials": false },
    "telegram": { "enable": true, "interval_ms": 3100, "channel_only": false, "shared_credentials": false, "paid_broadcast": false },
    "guangya":  { "enable": true, "concurrency": 2 }
  },
  "seo": { "google_verification": "", "base_url": "", "site_name": "" }
}
```

- `proxy.*` 与 `rate_limit.*` 都是可选子对象，缺字段不改动；`burst` 夹在 1–100。
- **`seo` 用指针区分「不改」和「清空」**：传 `google_verification: ""` 会撤掉首页验证标签；`base_url` 非空时必须以 `http://` 或 `https://` 开头，且尾部 `/` 会被去掉。
- **`public_url` 同理是顶层指针**：省略不改，传 `""` 清空并回到按请求 `Host` 推断；非空时必须带 `http://`/`https://`，尾部 `/` 会被去掉。它是同源校验与站点地图（`seo.base_url` 缺省时）的基准地址，反代/Cloudflare Tunnel 下必填。
- 校验失败：`400 {"error":"Google 站点验证码格式不正确"}` / `{"error":"站点根地址需要以 http:// 或 https:// 开头"}` / `{"error":"对外访问地址需要以 http:// 或 https:// 开头"}`；`save_to_file` 写盘失败 → `500`。
- 成功 → `{"ok":true}`，随后进程内配置热更新（重建 TMDB/Telegram 客户端并重启 Bot）。

### `POST /api/settings/test`
连通性探测，**不写任何配置**。`target` 取 `tmdb` | `telegram` | `proxy`；其余字段可覆盖当次探测用的密钥/代理（留空则用当前配置）。

→ `{"ok":bool,"message":"...","detail":"...","elapsed_ms":123}`

- `tmdb`：调 `/configuration`，区分「Key 无效（401）」与网络不通。
- `telegram`：`getMe` → `getChat` → 校验 Bot 是否为频道管理员。
- `proxy`：经代理访问 Telegram，附带出口 IP（拿不到 IP 不影响结论）。

### `GET /api/logs/download`
下载当前滚动日志文件（管理员）。**非 JSON**：成功直接回文件流 + `Content-Disposition: attachment`，文件名形如 `guangya-20261030-014500.log`，所以设置页用普通 `<a href>` 就能触发下载。

- 日志只写标准输出、文件没建成（数据目录不可写）→ `404 {"error":"日志文件未启用"}`
- 启动时建好了、下载时文件已被删除 → `404 {"error":"日志文件不存在"}`

路径由进程启动时通过 `Server.WithLogFile` 注入，接口不接受任何客户端参数，因此没有路径穿越面；轮转出的 `app.log.1`…`app.log.3` 不在本接口范围内。GET 不触发同源校验。下载动作本身会记一条 `管理员下载日志`（带 `username` 与 `size`）。

### `POST /api/branding`
`multipart/form-data` 上传站点素材：字段 `logo` / `icon` 至少带一个，`save_to_file=true` 时把文件名写回 `config.yaml`（省略则只活到本次进程）。

→ `Branding`（回包就是最新状态，前端直接覆盖全站品牌信息，不必再拉一次）

- 类型按**文件头嗅探**判定，只认 PNG / JPEG / GIF / WebP；不看客户端的 `filename` 与 `Content-Type`，**SVG 一律拒绝**（同源 SVG 可内嵌脚本）。
- 大小上限：Logo 512KB、站点图标 256KB。
- 落盘文件名固定为 `<kind><嗅探扩展名>`（`logo.png`、`icon.jpg`…），换格式时删掉旧文件；目录 `<storage.data_dir>/branding`（0750），文件 0600。先写 `.tmp` 再改名，避免留下半张图。
- `400`：`缺少 logo 或 icon 文件` / `只支持 PNG / JPEG / GIF / WebP 图片` / `图片超过 512 KB，请压缩后再上传` / `图片内容为空` / `读取上传文件失败`。
- `503 配置未加载`；未配置 `storage.data_dir` 或写盘失败 → `500`。
- 站点标题不在这里改，走 `POST /api/settings` 的 `seo.site_name`（留空即回到默认「光鸭 GO」）。

### `DELETE /api/branding?kind=logo|icon&save_to_file=true`
删除一类素材文件并清空配置里的引用。→ `Branding`
`kind` 缺失或不是 `logo` / `icon` → `400 {"error":"kind 只支持 logo / icon"}`。

### `GET /api/users`
→ `{"total":N,"items":[User...]}`

### `POST /api/users`

```json
{ "username": "op1", "password": "...", "display_name": "小李", "role": "operator" }
```

`role` 省略默认 `operator`；用户名重复 → `409 {"error":"用户名已存在"}`；其余校验同安装向导（`400`）。→ `200 User`

### `PATCH /api/users/:id`
字段全部可选：`{"display_name":"...","role":"admin","enabled":false}`

- 不能停用自己或改自己的角色 → `400 {"error":"不能停用自己或修改自己的角色"}`
- 会动到最后一个启用的管理员（停用或降权）→ `400`（`store.ErrLastAdmin`）
- 只有 `enabled=false` 会连带删掉该账号的全部会话；单纯改显示名或角色不会踢人。→ `200 User`

### `POST /api/users/:id/password`
`{"password":"..."}` 管理员重置他人密码，口令规则同上；成功后该账号全部会话作废。→ `{"ok":true}`

### `DELETE /api/users/:id`
删除账号（级联清会话）。不能删自己 → `400 {"error":"不能删除当前登录的账号"}`；删最后一个启用管理员 → `400`。→ `{"deleted":<id>}`

---

## 6. 页面型路由（非 JSON）

| 路径 | 说明 |
| --- | --- |
| `/` | 公开门户（SPA）。服务端注入 `<link rel="canonical">`、`<meta name="google-site-verification">`、站点图标 `<link rel="icon">`，并把模板里的 `{{SITE_NAME}}` 换成配置里的站点名 |
| `/?id=<n>` | 门户详情，canonical 会带上 `?id=` |
| `/setup` | 安装向导，`installed=true` 时自动离开 |
| `/login` | 登录页，未安装时自动跳 `/setup`；`?next=` 只接受 `/admin*` |
| `/admin` | 后台（`/admin/<任意后缀>` 都回落到同一个 SPA） |
| `/branding/:name` | 站点素材原图（PNG / JPEG / GIF / WebP）。**只有与配置里 `seo.logo_file` / `seo.icon_file` 精确相等的名字才回源**，其余一律 `404`，既不会路径穿越也暴露不出 `data` 目录里的其它文件；响应带 `Cache-Control: public, max-age=86400` |
| `/posters/:name` | 刮削缓存的海报原图，文件名为 `<media_type>-<tmdb_id>.jpg`（如 `movie-129.jpg`），只从 `storage.poster_dir` 下读取。名字先过白名单正则 `^(movie|tv)-\d{1,12}\.jpg$`，其余一律 `404`；`storage.serve_posters: false` 时整条路由固定 `404`。响应带 `Cache-Control: public, max-age=86400`。门户与 OG/JSON-LD 都优先用这个同源地址，避免访客网络打不开 `image.tmdb.org` 时海报空白 |

后台页签（资源库 / 录入 / 刮削 / 推送 / 设置 / 用户管理 / SEO）不是真实 URL，切换靠前端状态，不产生路由记录。

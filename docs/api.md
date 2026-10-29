# 光鸭 GO 接口文档

覆盖 `internal/api` 当前注册的全部路由。最后核对 2026-10-29，对应提交 `81655f8`。改接口请同步更新本文（维护清单见 [docs/dev-guide.md](dev-guide.md#12-维护清单)）。

## 约定

- **基址**：所有 JSON 接口都在 `/api` 下，前端 `api.ts` 会自动补前缀。示例里的 `localhost:8090` 是默认监听地址。
- **认证**：会话 Cookie `gy_session`（HttpOnly / SameSite=Lax / 7 天），由 `POST /api/auth/login` 下发。除下表标注「公开」的接口外，一律需要登录；标注「管理员」的还需要 `role=admin`。
  - 未登录 → `401 {"error":"未登录"}`；Cookie 有效但会话过期/被撤销 → `401 {"error":"登录已失效"}`；角色不足 → `403 {"error":"需要管理员权限"}`。
- **CSRF**：所有 `POST/PUT/PATCH/DELETE` 都做了同源校验。浏览器跨源 XHR 会带 `Origin`，不同源直接 `403 {"error":"跨站请求已被拒绝"}`；`curl` 不带 Origin/Referer 时放行。
- **请求体**：普通接口上限 1MB，`/api/links/import` 32MB。JSON 接口 `Content-Type: application/json`。
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

`PublicLink`（门户最小字段集）：`id, title, original_title, year, media_type, tmdb_id, overview, genres, vote_average, runtime, poster_url, url, code, shared_at`。

`User`：`id, username, display_name, role(admin|operator), enabled, last_login_at, created_at`（不含密码字段）。

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
首次安装：创建管理员，可同时写入 TMDB Key。**用户表非空时固定拒绝**，无自恢复入口，请在页面提示下走「用户管理」。

| 字段 | 必填 | 约束 |
| --- | --- | --- |
| `username` | 是 | `^[a-zA-Z0-9_.-]{3,32}$` |
| `password` | 是 | 8–128 字节、非常见弱口令、字符类别≥2 |
| `display_name` | 否 | ≤40 字节，默认「管理员」 |
| `tmdb_api_key` | 否 | 非空则写入 `config.yaml` 并热加载 |

→ `200 {"ok":true,"user_id":1}`；已安装 → `403 {"error":"系统已安装，请勿重复操作"}`；校验失败 → `400`。

### `POST /api/auth/login`

```json
{ "username": "admin", "password": "..." }
```

→ `200` 返回 `User`，并 `Set-Cookie: gy_session=...`。
账号不存在、被停用、密码错误一律 `401 {"error":"用户名或密码错误"}`（等时校验，不泄露账号是否存在）。
同一 IP 或同一用户名连续 5 次失败 → `429 {"error":"尝试次数过多，请 5 分钟后再试"}`。

### `GET /api/portal/links`
公开门户列表，只含 `status='pushed'`。

| 查询参数 | 默认 | 说明 |
| --- | --- | --- |
| `q` | — | 关键词，超 64 字符按字符截断 |
| `media_type` | — | 仅 `movie` / `tv`，其他值 → `400 {"error":"参数不正确"}` |
| `year` | 全部 | 非法值当作「全部年份」，不报错 |
| `sort` | `new` | `new` \| `rating` \| `year`，其余回落 `new` |
| `limit` | 24 | 1–60，越界回落 24 |
| `offset` | 0 | 负数归 0，上限 10000 |

→ `{"total": 123, "items": [PublicLink...]}`

### `GET /api/portal/stats`
筛选栏计数：分类数忽略分类条件、年份数忽略年份条件，`library_total` 不随筛选变化。参数同上（除 `sort/limit/offset`）。

```json
{ "total": 42, "movies": 30, "tv": 12, "library_total": 318,
  "years": [ { "year": 2024, "count": 7 } ] }
```

### `GET /robots.txt`
纯文本。放行 `/`，屏蔽 `/admin`、`/login`、`/setup`、`/api/`，并声明 Sitemap 地址。

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
XML（`Content-Type: application/xml`）。收录首页与全部已推送记录（`/?id=<n>`，上限 5000 条）。域名取 `seo.base_url`，未配置则按请求头推断（`X-Forwarded-Proto=https` 或 TLS 为 https）。

```xml
<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://gy.example.com/</loc><changefreq>daily</changefreq><priority>1</priority></url>
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
单条资源的 Schema.org（`media_type=tv` → `TVSeries`，否则 `Movie`）。仅 `pushed` 记录，记录不存在 → `404 {"error":"资源不存在"}`；`id` 非数字 → `400 {"error":"非法 ID"}`。

```json
{
  "@context": "https://schema.org",
  "@type": "TVSeries",
  "name": "致命女人",
  "url": "https://gy.example.com/?id=42",
  "datePublished": "2019-08-15 10:00:00",
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

> 可选字段按数据有无出现：`alternateName` 仅在原名与片名不同时；`description` / `image` / `year` / `aggregateRating` / `duration` / `genre` / `sameAs` 在对应值 >0 或非空时才输出。

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
后台总览。固定输出全部 8 个状态，计数为 0 的也在。

```json
{ "pending":4, "review":0, "scraping":0, "scraped":12,
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
  "auto_push": true,            // 省略则不改
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
- 校验失败：`400 {"error":"Google 站点验证码格式不正确"}` / `{"error":"站点根地址需要以 http:// 或 https:// 开头"}`；`save_to_file` 写盘失败 → `500`。
- 成功 → `{"ok":true}`，随后进程内配置热更新（重建 TMDB/Telegram 客户端并重启 Bot）。

### `POST /api/settings/test`
连通性探测，**不写任何配置**。`target` 取 `tmdb` | `telegram` | `proxy`；其余字段可覆盖当次探测用的密钥/代理（留空则用当前配置）。

→ `{"ok":bool,"message":"...","detail":"...","elapsed_ms":123}`

- `tmdb`：调 `/configuration`，区分「Key 无效（401）」与网络不通。
- `telegram`：`getMe` → `getChat` → 校验 Bot 是否为频道管理员。
- `proxy`：经代理访问 Telegram，附带出口 IP（拿不到 IP 不影响结论）。

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
| `/` | 公开门户（SPA）。响应由服务端注入 `<link rel="canonical">` 与 `<meta name="google-site-verification">` |
| `/?id=<n>` | 门户详情，canonical 会带上 `?id=` |
| `/setup` | 安装向导，`installed=true` 时自动离开 |
| `/login` | 登录页，未安装时自动跳 `/setup`；`?next=` 只接受 `/admin*` |
| `/admin` | 后台（`/admin/<任意后缀>` 都回落到同一个 SPA） |

后台页签（资源库 / 录入 / 刮削 / 推送 / 设置 / 用户管理 / SEO）不是真实 URL，切换靠前端状态，不产生路由记录。

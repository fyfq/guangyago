# 光鸭 GO · AI 协作约定

这份文件是给 AI（Qoder/Claude/Cursor 等）看的**项目宪法**。写代码前先读这一页，能省掉 80% 的返工。
人类 reviewer 也照这份对。

## 一、术语表（禁止混用）

| 术语 | 类型 | 含义 | 出现位置 |
|---|---|---|---|
| `Link` | `model.Link` | **后台**资源记录，一条网盘分享 | `internal/model`, `internal/store/store.go`, `internal/api/*.go`（鉴权路由下） |
| `PublicLink` | `model.PublicLink` | **门户**展示的资源，是 Link 的对外投影 | `internal/store/public.go`, `internal/api/portal.go`, `internal/api/seo.go` |
| `Scrape` | 动词 | 用 TMDB ID 抓元数据（标题/海报/年份…） | `internal/scraper/tmdb.go` |
| `Push` | 动词 | 把 Link 推送到 Telegram 频道 | `internal/notifier/telegram.go` |
| `Pipeline` | 名词 | 后台 worker 队列，串 Scrape→Push | `internal/pipeline/pipeline.go` |
| `ShareID` | 字段 | 光鸭分享链接的唯一标识，业务主键 | `Link.ShareID`，DB UNIQUE |
| `TMDBID` | 字段 | 刮削时人工/AI 校准后的 TMDB 条目 ID | `Link.TMDBID` |

**不要**：`resource` / `item` / `entry` / `movie` / `link` 混着叫。同一个概念在 Go 里、TS 里、DB 里、UI 文案里必须是同一个词。

## 二、包边界（依赖方向）

```
cmd/server ──▶ api ──▶ pipeline ──▶ { scraper, notifier, store, parser, ratelimit, netutil }
                            │
                            └──▶ config ──▶ (leaf)

model / auth / web / ratelimit / netutil / parser ─ 叶子包，禁止 import 项目内其它包（除 model）
store ──▶ model（仅此一个内部依赖）
scraper ──▶ { config, netutil, ratelimit }
notifier ──▶ { config, model, netutil, ratelimit }
api ──▶ 所有其它内部包
```

**硬性规则**（违反 = PR 打回）：

1. `internal/model` 不 import 任何内部包，只依赖 stdlib + `time`。
2. `internal/store` 只 import `internal/model`。SQL、事务、schema 都在这里，**API 层禁止裸写 `db.Exec`**。
3. `internal/scraper`、`internal/notifier` **禁止** import `internal/store`。它们的输出交给 pipeline 决定要不要落库。
4. `internal/pipeline` 是**唯一**能同时看到 store/scraper/notifier 的地方；跨阶段编排都写这里。
5. `internal/api` 处理 HTTP 语义（鉴权、限流、请求/响应格式），**不要**在 handler 里塞业务循环。
6. `cmd/server/main.go` 只做装配（依赖注入），不写逻辑。

## 三、参考实现（照这些抄，别自造）

**Store 事务写方法** → [internal/store/users.go](file:///Users/odin/Documents/Qoder/光鸭 GO/internal/store/users.go) 里 `UpdateUserGuarded` / `DeleteUserGuarded`  
要点：`tx.Begin()` → `defer tx.Rollback()` → 校验业务不变式（如最后一个管理员）→ 失败返回 sentinel error（如 `ErrLastAdmin`）→ `tx.Commit()`。

**限流 HTTP 客户端** → [internal/scraper/tmdb.go](file:///Users/odin/Documents/Qoder/光鸭 GO/internal/scraper/tmdb.go) 里 `get()`  
要点：`ratelimit.Limiter.Wait` → `http.NewRequestWithContext` → `io.LimitReader(resp.Body, maxAPIResponseBytes)` → 状态码分类。

**API handler 错误契约** → [internal/api/auth.go](file:///Users/odin/Documents/Qoder/光鸭 GO/internal/api/auth.go)  
要点：请求校验失败 → `400 + 中文 error`；鉴权失败 → `401`；权限不足 → `403`；资源不存在 → `404`；业务不变式违反（如 `ErrLastAdmin`）→ `400` + 具体中文；DB/内部错误 → `serverError(c, err)` 统一 500 + 日志。

**服务端注入 HTML head** → [internal/api/seo.go](file:///Users/odin/Documents/Qoder/光鸭 GO/internal/api/seo.go) 里 `headTags` / `injectHeadTags`  
要点：CSP 禁内联脚本，任何需要出现在首屏源码的 meta/link 都走服务端字符串注入；用户可控部分**必须**先经过白名单字符过滤（参考 `sanitizeCanonicalPath` / `sanitizeCanonicalQuery`）。

**前端 URL 绑定** → [web/src/lib/utils.ts](file:///Users/odin/Documents/Qoder/光鸭 GO/web/src/lib/utils.ts) 里 `safeExternalUrl`  
要点：`:href` 绑定用户/CSV 输入时**必须**过一层协议白名单（http/https），杜绝 `javascript:` / `data:` XSS。

## 四、代码风格约定

### Go
- 错误消息**中文**，短、动作化。"至少要保留一个启用的管理员"，而不是 "at least one enabled admin required"。
- 日志用 `slog.Info/Warn/Error(msg, "key", value, ...)`，msg 中文，key/value 保持小写下划线。
- **禁止** `_ = someErr`。要么处理，要么 `slog.Error("...", "err", err)` 记下来。参考 pipeline 里 `updateStatus`。
- 上限常量命名 `max<X>`（如 `maxAPIResponseBytes`、`maxJSONBody`、`maxCSVBytes`），单位后缀（Bytes / MS / Count）。
- LIKE 查询必须走 `store.likeEscape` + `ESCAPE '\'`。
- 所有 `http.Response.Body` 读取都要 `io.LimitReader` 包一层，防止上游返回超大响应打爆内存。

### Vue/TypeScript
- 全站**统一系统默认字体**，禁止在 CSS 里声明 `font-family` 覆盖。
- `target="_blank"` 必须带 `rel="noopener noreferrer"`。
- **禁止** `v-html`。文案渲染走白名单解析器（参考 [web/src/views/PushView.vue](file:///Users/odin/Documents/Qoder/光鸭 GO/web/src/views/PushView.vue) 里的 `parseCaptionSegments`）。
- 用户可控数据绑到 `:href`/`:src` 前，走 `safeExternalUrl`。
- 组件内 `import` 顺序：Vue → lucide → 本地 UI → lib → 相对。

## 五、安全默认（不接受例外）

- 密码：`auth.HashPassword` 用 PBKDF2-HMAC-SHA256，`validPassword` 至少 8 位 + 2 类字符。
- 会话 token：`auth.HashToken` 用 HMAC-SHA256，密钥文件 `data/session.key`（0600）持久化。
- Cookie：`SameSite=Lax`；生产 TLS 下 `Secure`（`cfg.Server.SecureCookie` 覆盖）。
- CSRF：`requireSameOrigin` 中间件校验 Origin/Referer host。
- CSP：`script-src 'self'`，无 `'unsafe-inline'`（style-src 例外，Tailwind 需要）。
- 登录限流：`login_attempts` 表 + 内存缓存双层，IP + 用户名分别计数。
- SSRF：TMDB `imageBase` 只允许 `image.tmdb.org` / `themoviedb.org` / `api.themoviedb.org`。
- HSTS：只在**直连 TLS** 时下发，反代场景交给边缘。

## 六、变更纪律（AI 特别容易踩）

1. **加东西之前先问能不能改**。看到"要新建一个 helper"，先在文件里搜 5 分钟。
2. **不做无关清理**。修 bug 就修 bug，不要顺手 rename 周边变量、加注释、拆导入。
3. **不加防御性分支**。`if x == nil { return }` 除非 x 真的能来自外部空值，否则删。
4. **不写文档/README/planning.md**，除非用户明确要求。
5. **不引入新依赖**，除非用户同意或 stdlib 明显没有对应能力。引入前先问一句。
6. **一个 PR ≤ 300 行 diff**。超过就拆——AI 一次生成 800 行时人的注意力会崩，评审只是走过场。
7. **每次改 `web/` 都必须 `make web && go build ./...`**（见 [auto memory: frontend-embed-rebuild]），否则 embed 里的前端和后端会不一致。

## 七、提交边界（哪些提交、哪些不提交）

### 必须提交

| 类别 | 文件 |
|---|---|
| 源码 | `cmd/`、`internal/`（除 `internal/web/dist/` 产物）、`web/src/` |
| 构建与部署 | `Makefile`、`Dockerfile`、`docker-compose.yml`、`.dockerignore`、`.github/` |
| 依赖锁定 | `go.mod`、`go.sum`、`web/package.json`、`web/package-lock.json` |
| 配置模板 | `config.example.yaml`、`.env.example` |
| 文档 | `README.md`、`docs/`、`AGENTS.md` |
| 占位文件 | `internal/web/dist/.gitkeep` —— 保证空目录下 `//go:embed all:dist` 仍可编译 |

### 绝不提交（`.gitignore` 已覆盖）

| 类别 | 文件 |
|---|---|
| 密钥与运行配置 | `config.yaml`、`.env`、`.env.*`（`!` 例外：`.env.example`） |
| 运行数据 | `data/`（含 `session.key`、数据库、海报）、`backups/`、`.workbuddy/` |
| 构建产物 | `bin/`、`web/dist/`、`internal/web/dist/*`（`!` 例外：`.gitkeep`）、`guangya-go`、`*.zip`、`*.tar.gz` |
| 依赖目录 | `node_modules/`、`web/node_modules/` |
| 杂项 | `.DS_Store`、`*.log`、`*.db`、`*.db-wal`、`*.db-shm` |

### 提交前须确认

- 新增依赖（`go.mod` / `package.json`）：先问是否必要。
- 数据库 schema 变更：`schema_meta.version` 必须 +1，并说明迁移路径。
- 示例里的真实域名、Bot Token、频道 ID、提取码必须替换为占位值。

### 提交前自检

```bash
git status --short          # 确认没有把 data/ config.yaml 之类带进来
make lint                   # vet + staticcheck + gosec + 前端 type-check
git diff --cached --stat    # 单次 diff 控制在 300 行内
```

## 八、Reviewer 快速清单

- [ ] 有无 `_ = err`？
- [ ] 有无 `http.Response.Body` 未包 `io.LimitReader`？
- [ ] 有无新的 SQL 未走 `store`？
- [ ] 有无 LIKE 拼字符串未走 `likeEscape`？
- [ ] 新 handler 的错误响应是不是**中文 + 4xx/5xx 分类正确**？
- [ ] 用户可控数据是不是过了 `safeExternalUrl` / `googleTokenRe` 之类白名单？
- [ ] `target="_blank"` 是不是带 `rel="noopener noreferrer"`？
- [ ] 有没有 5 行以上复制粘贴？
- [ ] 单元测试覆盖新逻辑的关键分支（成功/失败/边界各一条）？

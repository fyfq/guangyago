# 光鸭 GO 最终方案

一句话：粘贴或导入光鸭云盘分享链接 → 工具自己取影视信息 → TMDB 刮削 → Bot 推送到 Telegram 频道。

技术栈选型见 `docs/tech-selection.md`，本文档聚焦流程、数据模型与实现规则。

## 1. 整体流程

| 步骤 | 输入 | 动作 | 输出 |
| --- | --- | --- | --- |
| 1 录入 | 分享文本 / CSV / Bot 消息 | 解析出链接、提取码、标题、TMDB ID；按 shareId 去重 | 链接记录（pending） |
| 2 刮削 | TMDB ID 或标题 | `/movie/{id}`，404 则 `/tv/{id}`；取详情与海报 | 完整元数据（scraped） |
| 3 渲染 | 元数据 + 链接 + 提取码 | 生成海报卡片文案 | 待推送内容 |
| 4 推送 | 卡片 | Bot 以管理员身份发到频道 | message_id（pushed） |
| 5 回看 | 记录列表 | Web 管理页查看、搜索、重推、纠错 | — |

三条录入入口（Web 粘贴、批量 CSV、Bot 发送）共用同一条管道。

## 2. 数据模型（SQLite）

表 `links`：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| id | INTEGER PK | 自增 |
| share_id | TEXT UNIQUE | 去重唯一键，从 `/s/{shareId}_{token}` 提取 |
| url | TEXT | 原始链接 |
| code | TEXT | 提取码 |
| source | TEXT | `guangya`，预留夸克 / 阿里 |
| raw_title | TEXT | 文件名或分享文本原始标题 |
| title / original_title | TEXT | 刮削后片名 / 原名 |
| year | INTEGER | 年份 |
| media_type | TEXT | movie / tv |
| tmdb_id | INTEGER | TMDB ID，可为空 |
| overview | TEXT | 简介 |
| genres | TEXT | 类型，逗号分隔 |
| vote_average | REAL | 评分 |
| runtime | INTEGER | 时长或单集时长 |
| poster_url / poster_path | TEXT | 远端海报 URL / 本地缓存路径 |
| shared_at / status / error | TEXT | 分享时间 / 状态 / 错误信息 |
| tg_message_id | INTEGER | 推送后回写，支持后续编辑或删除 |
| created_at / updated_at | TEXT | 时间戳 |

索引：`share_id` 唯一、`status`、`tmdb_id`。

## 3. 解析规则

### 3.1 CSV 批量导入（光鸭导出模板）

列：`文件名, 链接, 提取码, 分享时间, 分享状态`（首行含 BOM，需剥离）

- 文件名：`标题 (年份) {tmdb-ID}` → 正则 `^(?P<title>.+?)\s*\((?P<year>\d{4})\)\s*\{tmdb-(?P<id>\d+)\}$`
- 链接：`https://www.guangyapan.com/s/(?P<share>[^_]+)_(?P<token>[^?]+)\?code=(?P<code>\w+)`，代码中 query 缺省时回退到「提取码」列
- 分享状态非「成功」→ 标记 `待核查`，仍入库不推送
- 导入返回：总数 / 新增 / 重复跳过 / 解析失败

### 3.2 单条分享文本

```
光鸭云盘用户给你分享了：「Benjamin Fra…-156138}」，点击链接...
链接：https://www.guangyapan.com/s/1962202450833190960_asE77mMgZ9YxZr9Z?code=exuz
提取码：exuz
```

- 链接行：`链接：` 后的 URL，同 3.1 正则
- 提取码：`提取码：` 后字段，或 URL 的 `?code=`
- 标题：「…」内文本，被截断（`…-156138}`）→ 只要尾部能抠出 `tmdb-\d+` 就直取 ID；否则清洗后走标题搜索
- 清洗规则：去掉 `-数字}` 尾部哈希、`【】[]()` 内水印、分辨率（1080p/2160p/4K）、编码（x264/HEVC/HDR）、字幕组标记

## 4. TMDB 刮削

1. 有 ID：请求 `/movie/{id}`，404 或类型不符则请求 `/tv/{id}`，语言 `zh-CN`，`append_to_response=images`
2. 无 ID：`/search/multi` 按清洗后的标题 + 年份，取首个年份差值 ≤1 的候选
3. 置信度低于阈值 → 状态 `unmatched`，不阻塞入库，Web 与 Bot 均可手动指定 ID 重刮
4. 海报下载到本地 `data/posters/{tmdb_id}.jpg`，推送用本地文件或 TMDB 图片 URL
5. 请求失败按指数退避重试 3 次；TMDB 限流（约 50 req/s）用令牌桶兜底

## 5. Telegram 推送

- Bot 加入目标频道并设为管理员，chat_id 用 `-100...` 或 `@channelname`
- 发送 `sendPhoto`（海报图 + HTML caption），caption 字段：片名(年份) / 原名 / 评分·类型·时长 / 简介 / 来源·提取码 / 原链接
- 推送成功回写 `tg_message_id`；失败保留 `pushed_failed` 可一键重推
- 批量推送做间隔（约 1.5s/条）避免触发限流
- Bot 同时以 long-polling 接收私聊消息：收到链接即录入，收到 `/id xxx` 可纠正 TMDB ID

## 6. 状态机

```
pending → scraping → scraped → pushed
   │          │          │
   └→ failed  └→ unmatched（可手动指定 ID 重刮）
```

`待核查`（分享状态异常）与 `failed` 均支持手动重试。

## 7. 目录结构

```
cmd/server          入口，装配配置/存储/管道/Bot/HTTP
internal/config     配置加载（YAML + 环境变量）
internal/model      数据结构与状态常量
internal/store      SQLite 连接、迁移、CRUD
internal/parser     分享文本与 CSV 解析、网盘适配器接口
internal/scraper    TMDB 客户端与标题清洗
internal/notifier   Telegram 推送
internal/pipeline   任务队列与 Worker
internal/api        Gin 路由与处理器
internal/web        前端构建产物（go:embed）
web/                Vue 3 + Vite + shadcn-vue 源码
data/               SQLite 库与海报缓存
```

## 8. 里程碑

| 阶段 | 内容 | 验收 |
| --- | --- | --- |
| M1 | 骨架 + SQLite + CSV/文本解析 + 导入 API | 导入 93 条 CSV，落库去重正确 |
| M2 | TMDB 刮削（ID 直取 + 回退搜索）+ 海报缓存 | 93 条全部刮到元数据与海报 |
| M3 | Telegram 频道推送 + Bot 录入 | 频道收到完整卡片，私聊发链接可入库 |
| M4 | Web 管理页（shadcn-vue）+ Docker | NAS 上一键部署，页面可查可重推 |

## 9. 需要的配置

| 项 | 来源 | 必填 |
| --- | --- | --- |
| TMDB API Key (v3) | TMDB 账号后台 | 是 |
| Telegram Bot Token | @BotFather | 是 |
| 频道 ID | Bot 加入频道后获取 | 是 |
| 光鸭 Cookie | 登录态（当前方案暂不需要） | 否 |

## 10. 风险与对策

| 风险 | 对策 |
| --- | --- |
| 光鸭分享页为 SPA，直连接口被网关拒绝 | 已定方案不抓页面，只用分享文本与 CSV；后续可加浏览器渲染适配器 |
| 标题截断导致无 ID 时匹配不准 | 优先抠 ID；无 ID 时走搜索 + 人工确认兜底 |
| TMDB 中文元数据缺失 | 中文取不到时回退 `en-US` |
| Telegram 限流 | 推送间隔 + 失败重试队列 |

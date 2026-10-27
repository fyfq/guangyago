# 光鸭 GO

光鸭云盘分享链接管理工具：录入链接 → 取影视信息 → TMDB 刮削 → 推送 Telegram 频道。

设计文档见 `docs/design.md`，技术选型见 `docs/tech-selection.md`。

## 当前进度：M1 ~ M3 已完成，M4 页面已完成

已完成：项目骨架、SQLite 存储、光鸭链接解析（CSV 批量 + 分享文本）、TMDB 刮削、Telegram 频道推送与 Bot 录入、Web 管理页（资源库 / 录入 / 刮削 / 推送 / 设置）。

未完成：Docker 部署打包。

## 快速开始

```bash
cp config.example.yaml config.yaml
make build
./bin/guangya -import ~/Downloads/批量分享记录_xxx.csv   # 导入 CSV
./bin/guangya                                            # 启动服务，默认 :8080
```

导入单条分享文本同理，把文件内容换成「光鸭云盘用户给你分享了…」那一段即可。

## 页面职能

导航按数据流向排列，每个页面只负责一件事：

| 页面 | 承载的状态 | 职能 |
| --- | --- | --- |
| **录入解析** | 入库瞬间 | 粘贴分享文本 / 上传 CSV，解析去重落库；成功后可「立即刮削这 N 条」，这是最顺手的刮削触发点 |
| **刮削队列** | `pending` `review` `scraping` `unmatched` `failed` | **刮削的唯一触发场所**：按筛选批量刮削、刮削所选、重试失败与未匹配；未匹配的记录可手动补 TMDB ID 后重刮 |
| **推送频道** | `scraped` `push_failed` | 待推送工作台：预览实际卡片文案（与推送内容一致，可复制），推送全部待推送 / 重试推送失败 |
| **资源库** | `scraped` `pushed` `push_failed` | 刮削完成的资源都在这里：检索、海报墙、详情抽屉、单条重推、导出 CSV，按推送状态回看 |
| **设置** | — | TMDB Key / Bot Token / 频道 ID / 代理，逐项连通性测试 |

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

排版规则：标题区（片名 + 年份）、简介区、链接区之间各用一个空行分隔；链接 / 提取码 / TMDB 三行连续不加空行。
整段不超过 Telegram 的 1024 字上限，简介会按剩余额度再压缩。
界面上的预览直接调 `/api/links/:id/caption`，与实际推送同源，不会出现两处格式漂移。

## 接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/health` | 健康检查 |
| GET | `/api/links?status=&q=&limit=&offset=` | 链接列表；`status` 支持逗号分隔多值，也支持 `scraped_set` / `pending_set` 两个语义集合 |
| GET | `/api/links/:id` | 单条详情 |
| DELETE | `/api/links/:id` | 删除 |
| POST | `/api/links/text` | 提交 `{"text": "分享文本"}` |
| POST | `/api/links/import` | 上传 CSV（multipart `file`）或直接提交 CSV/文本 |
| GET | `/api/links/export` | 导出 CSV（带 BOM），支持 `status` / `q` |
| GET | `/api/stats` | 各状态数量 |
| DELETE | `/api/links/:id` | 删除单条 |
| POST | `/api/links/batch-delete` | 批量删除 `{"ids":[1,2]}` |
| POST | `/api/scrape` | 刮削 `{"ids":[]}` / `{"all":true}`（默认 pending）/ `{"statuses":["failed","unmatched"]}` 指定状态重试 |
| POST | `/api/links/:id/scrape` | 单条重新刮削 |
| POST | `/api/links/:id/tmdb` | 手动指定 TMDB ID：`{"tmdb_id":20851,"media_type":"tv","scrape":true}` |
| GET | `/api/links/:id/caption` | 卡片文案预览，返回 `{"html":...,"text":...}` |
| POST | `/api/push` | 推送 `{"ids":[]}` / `{"scraped":true}` / `{"statuses":["push_failed"]}` 重推失败项 |
| POST | `/api/links/:id/push` | 单条推送 |
| GET | `/api/tasks` | 当前批次进度 |
| GET | `/api/settings` | 配置就绪状态（含代理，不返回密钥明文） |
| POST | `/api/settings` | 更新密钥/代理，可写回配置文件 |
| POST | `/api/settings/test` | 连通性测试：`{"target":"tmdb"\|"telegram"\|"proxy"}` |

导入返回：`{"total":94,"inserted":94,"skipped":0,"failed":[],"ids":[...]}`

## 解析规则

- 链接：`https://www.guangyapan.com/s/{shareId}_{token}?code={提取码}`，`shareId` 作为去重唯一键
- 文件名：`标题 (年份) {tmdb-ID}` → 拆出片名、年份、TMDB ID
- 分享文本中的截断标题（如 `…-156138}`）同样能抠出 TMDB ID

## 配置

密钥建议走环境变量：`TMDB_API_KEY`、`TG_BOT_TOKEN`、`GUANGYA_COOKIE`，
代理用 `PROXY_ADDR`（+ `PROXY_USERNAME` / `PROXY_PASSWORD`）。

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

## 变更说明

<!-- 一句话说明为什么改（bug 现象 / 需求来源），而不是改了什么。 -->

关联 issue：

## 影响面

- [ ] 改了 `internal/store/` → 需要评估迁移路径（`schema_meta.version` 是否 +1）
- [ ] 改了 `internal/api/` 的路由或鉴权 → 前端 `web/src/lib/api.ts` 需同步
- [ ] 改了 `web/` → **本地跑过 `make web && go build ./...`**（embed 一致性）
- [ ] 改了配置结构 → `config.example.yaml` 已更新，且 `Load()` 支持 env override
- [ ] 改了限流 / 队列 / 并发 → 关闭路径有 `ctx.Done()` 兜底

## Reviewer 快速清单（对照 AGENTS.md §七）

- [ ] 无 `_ = err` 吞错
- [ ] `http.Response.Body` 读取都包了 `io.LimitReader`
- [ ] 新 SQL 都走 `internal/store`，API 层无裸 `db.Exec`
- [ ] LIKE 拼接走 `store.likeEscape` + `ESCAPE '\'`
- [ ] 错误响应是中文 + 正确的 4xx/5xx 分类
- [ ] 用户可控数据过了白名单（`safeExternalUrl` / 正则）
- [ ] `target="_blank"` 带 `rel="noopener noreferrer"`
- [ ] 无 5 行以上复制粘贴
- [ ] diff ≤ 300 行（超过请拆分或说明理由）

## 测试

```
make lint     # vet + staticcheck + gosec + web type-check
make cover    # 整体覆盖率
```

- [ ] 已本地跑 `make lint` 无新增告警
- [ ] 新增/修改逻辑有对应单测（成功 / 失败 / 边界各一条）
- [ ] 手工验证过的场景：<填写：登录、推送、门户访问、后台列表 中的哪些>

## 部署

<!-- 单人运维项目请写清楚：是否需要停服 / 是否自动迁移 / 回滚方式 -->

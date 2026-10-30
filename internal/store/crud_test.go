package store

import (
	"errors"
	"testing"
	"time"

	"github.com/odin/guangya-go/internal/model"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mustCreateLink(t *testing.T, s *Store, l *model.Link) {
	t.Helper()
	ok, err := s.Create(l)
	if err != nil {
		t.Fatalf("Create(%q): %v", l.ShareID, err)
	}
	if !ok {
		t.Fatalf("Create(%q) 未插入", l.ShareID)
	}
}

func TestCreate_InsertAndDedupe(t *testing.T) {
	s := newTestStore(t)
	l := &model.Link{
		ShareID: "abc123", URL: "https://guangyapan.com/s/abc123_x",
		Code: "k1", Source: "guangya", RawTitle: "流浪地球2 (2023)",
		Title: "流浪地球2", Year: 2023, TMDBID: 842675,
		SharedAt: "2026-10-01", ShareStatus: "成功", Status: model.StatusPending,
	}
	mustCreateLink(t, s, l)
	if l.ID == 0 {
		t.Fatal("插入后 ID 未回填")
	}

	got, err := s.Get(l.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get 返回 nil")
	}
	if got.ShareID != "abc123" || got.Title != "流浪地球2" || got.Year != 2023 || got.TMDBID != 842675 {
		t.Errorf("字段回读不一致: %+v", got)
	}
	if got.Status != model.StatusPending {
		t.Errorf("status = %q, 期望 pending", got.Status)
	}

	// 同一 share_id 再次插入应跳过
	dup := &model.Link{ShareID: "abc123", URL: "https://other", Status: model.StatusPending}
	ok, err := s.Create(dup)
	if err != nil {
		t.Fatalf("重复 Create 报错: %v", err)
	}
	if ok {
		t.Error("重复 share_id 应返回未插入")
	}

	if n, err := s.CountByStatus(); err != nil || n[model.StatusPending] != 1 {
		t.Errorf("pending 数量 = %d, err=%v", n[model.StatusPending], err)
	}
}

func TestGet_Missing(t *testing.T) {
	s := newTestStore(t)
	got, err := s.Get(999)
	if err != nil {
		t.Fatalf("Get 缺失记录不应报错: %v", err)
	}
	if got != nil {
		t.Errorf("缺失记录应返回 nil，实际 %+v", got)
	}
}

func TestList_Filters(t *testing.T) {
	s := newTestStore(t)
	seed := []*model.Link{
		{ShareID: "s1", URL: "u1", Title: "Alpha", Year: 2020, Status: model.StatusPending},
		{ShareID: "s2", URL: "u2", Title: "Beta 100% pure", Status: model.StatusScraped},
		{ShareID: "s3", URL: "u3", Title: "Gamma", Status: model.StatusPushed},
		{ShareID: "s4", URL: "u4", Title: "Alpha 2", Status: model.StatusScraped},
	}
	for _, l := range seed {
		mustCreateLink(t, s, l)
	}

	cases := []struct {
		name      string
		f         model.ListFilter
		wantLen   int
		wantTotal int
	}{
		{"单状态", model.ListFilter{Status: model.StatusScraped}, 2, 2},
		{"多状态", model.ListFilter{Statuses: model.ScrapedOnly}, 3, 3},
		{"关键字", model.ListFilter{Keyword: "Alpha"}, 2, 2},
		{"默认 limit", model.ListFilter{}, 4, 4},
		{"分页", model.ListFilter{Limit: 1, Offset: 0}, 1, 4},
	}
	for _, c := range cases {
		links, total, err := s.List(c.f)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if len(links) != c.wantLen || total != c.wantTotal {
			t.Errorf("%s: len=%d total=%d 期望 len=%d total=%d", c.name, len(links), total, c.wantLen, c.wantTotal)
		}
	}

	// % 必须被转义成字面量，否则会匹配所有记录
	links, _, err := s.List(model.ListFilter{Keyword: "100%"})
	if err != nil {
		t.Fatalf("关键字含 %%: %v", err)
	}
	if len(links) != 1 || links[0].ShareID != "s2" {
		t.Errorf("%% 未被转义，命中 %d 条", len(links))
	}

	// 排序按 id DESC
	links, _, err = s.List(model.ListFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if links[0].ShareID != "s4" {
		t.Errorf("应按 id 倒序，首条为 %q", links[0].ShareID)
	}
}

func TestIDsByStatuses(t *testing.T) {
	s := newTestStore(t)
	mustCreateLink(t, s, &model.Link{ShareID: "a", URL: "u", Status: model.StatusPending})
	mustCreateLink(t, s, &model.Link{ShareID: "b", URL: "u", Status: model.StatusScraped})

	ids, err := s.IDsByStatuses(model.PendingScrape)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 {
		t.Fatalf("期望 1 条 pending，实际 %d", len(ids))
	}

	if ids, err := s.IDsByStatuses(nil); err != nil || ids != nil {
		t.Errorf("空状态集合应返回 nil,nil，实际 %v,%v", ids, err)
	}
}

func TestUpdates(t *testing.T) {
	s := newTestStore(t)
	l := &model.Link{ShareID: "u1", URL: "https://x", Status: model.StatusPending}
	mustCreateLink(t, s, l)

	if err := s.UpdateStatus(l.ID, model.StatusFailed, "boom"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(l.ID)
	if got.Status != model.StatusFailed || got.Error != "boom" {
		t.Errorf("UpdateStatus 未生效: %+v", got)
	}

	scraped := &model.Link{
		Title: "T", OriginalTitle: "OT", Year: 2021, MediaType: model.MediaTV,
		TMDBID: 42, Overview: "ov", Genres: "g", VoteAverage: 8.5, Runtime: 100,
		PosterURL: "https://img", PosterPath: "p.jpg",
	}
	if err := s.UpdateScraped(l.ID, scraped); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(l.ID)
	if got.Status != model.StatusScraped || got.Title != "T" || got.MediaType != model.MediaTV || got.TMDBID != 42 {
		t.Errorf("UpdateScraped 未生效: %+v", got)
	}

	if err := s.UpdatePushed(l.ID, 777); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(l.ID)
	if got.Status != model.StatusPushed || got.TGMessageID != 777 {
		t.Errorf("UpdatePushed 未生效: %+v", got)
	}

	if err := s.SetTMDBID(l.ID, 99, model.MediaMovie); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(l.ID)
	if got.Status != model.StatusPending || got.TMDBID != 99 || got.MediaType != model.MediaMovie {
		t.Errorf("SetTMDBID 未生效: %+v", got)
	}
}

func TestDeleteMany(t *testing.T) {
	s := newTestStore(t)
	a := &model.Link{ShareID: "a", URL: "u", Status: model.StatusPending}
	b := &model.Link{ShareID: "b", URL: "u", Status: model.StatusPending}
	mustCreateLink(t, s, a)
	mustCreateLink(t, s, b)

	n, err := s.DeleteMany([]int64{a.ID, b.ID, 999})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("DeleteMany 删除 %d 条，期望 2", n)
	}
	if n, err := s.DeleteMany(nil); err != nil || n != 0 {
		t.Errorf("空 ID 列表应返回 0,nil，实际 %d,%v", n, err)
	}

	mustCreateLink(t, s, &model.Link{ShareID: "c", URL: "u", Status: model.StatusPending})
	var id int64
	if err := s.db.QueryRow(`SELECT id FROM links WHERE share_id='c'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(id); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(id); got != nil {
		t.Error("Delete 后仍能读到记录")
	}
}

func TestUsers_CRUD(t *testing.T) {
	s := newTestStore(t)
	if n, err := s.CountUsers(); err != nil || n != 0 {
		t.Fatalf("初始用户数 = %d, err=%v", n, err)
	}

	u := &model.User{Username: "admin", DisplayName: "管理员", Role: model.RoleAdmin, Enabled: true}
	if err := s.CreateUser(u, "hash1"); err != nil {
		t.Fatal(err)
	}
	if u.ID == 0 {
		t.Fatal("CreateUser 未回填 ID")
	}
	if err := s.CreateUser(&model.User{Username: "admin", Role: model.RoleOperator, Enabled: true}, "h"); err == nil {
		t.Error("重复用户名应报错")
	}

	got, hash, err := s.UserByUsername("admin")
	if err != nil || got == nil || hash != "hash1" {
		t.Fatalf("UserByUsername: %+v hash=%q err=%v", got, hash, err)
	}
	if got.DisplayName != "管理员" || !got.Enabled {
		t.Errorf("用户字段不一致: %+v", got)
	}

	if got, hash, err := s.UserByUsername("nobody"); err != nil || got != nil || hash != "" {
		t.Errorf("不存在的用户应返回 nil,\"\",nil，实际 %+v %q %v", got, hash, err)
	}

	if err := s.TouchLastLogin(u.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = s.UserByID(u.ID)
	if got.LastLoginAt == nil {
		t.Error("TouchLastLogin 后 last_login_at 应非空")
	}

	if err := s.UpdateUserPassword(u.ID, "hash2"); err != nil {
		t.Fatal(err)
	}
	if h, err := s.UserPasswordHash(u.ID); err != nil || h != "hash2" {
		t.Errorf("UserPasswordHash = %q, err=%v", h, err)
	}
	if h, err := s.UserPasswordHash(999); err != nil || h != "" {
		t.Errorf("缺失用户密码哈希应返回空串，实际 %q %v", h, err)
	}

	if err := s.UpdateUser(u.ID, "新名字", model.RoleOperator, false); err != nil {
		t.Fatal(err)
	}
	got, _ = s.UserByID(u.ID)
	if got.DisplayName != "新名字" || got.Role != model.RoleOperator || got.Enabled {
		t.Errorf("UpdateUser 未生效: %+v", got)
	}

	list, err := s.ListUsers()
	if err != nil || len(list) != 1 {
		t.Errorf("ListUsers = %d, err=%v", len(list), err)
	}
}

func TestUsers_LastAdminGuards(t *testing.T) {
	s := newTestStore(t)
	admin := &model.User{Username: "a1", Role: model.RoleAdmin, Enabled: true}
	if err := s.CreateUser(admin, "h"); err != nil {
		t.Fatal(err)
	}

	// 降级唯一管理员应被拒绝
	err := s.UpdateUserGuarded(admin.ID, "", model.RoleOperator, true, true)
	if !errors.Is(err, ErrLastAdmin) {
		t.Errorf("降级最后管理员应返回 ErrLastAdmin，实际 %v", err)
	}
	// 停用唯一管理员同样拒绝
	err = s.UpdateUserGuarded(admin.ID, "", model.RoleAdmin, false, true)
	if !errors.Is(err, ErrLastAdmin) {
		t.Errorf("停用最后管理员应返回 ErrLastAdmin，实际 %v", err)
	}
	// 不保底时允许
	if err := s.UpdateUserGuarded(admin.ID, "", model.RoleOperator, true, false); err != nil {
		t.Errorf("不保底更新应成功: %v", err)
	}

	// 恢复管理员后再加一个管理员，删除其一应允许
	_ = s.UpdateUserGuarded(admin.ID, "", model.RoleAdmin, true, false)
	admin2 := &model.User{Username: "a2", Role: model.RoleAdmin, Enabled: true}
	if err := s.CreateUser(admin2, "h"); err != nil {
		t.Fatal(err)
	}
	if n, err := s.EnabledAdminCount(admin.ID); err != nil || n != 1 {
		t.Errorf("EnabledAdminCount = %d, err=%v", n, err)
	}
	if err := s.DeleteUserGuarded(admin.ID, true); err != nil {
		t.Errorf("还有另一个管理员时应允许删除: %v", err)
	}

	// 删除不存在的账号在保底模式下应静默成功
	if err := s.DeleteUserGuarded(999, true); err != nil {
		t.Errorf("删除缺失账号应返回 nil: %v", err)
	}
	if err := s.UpdateUserGuarded(999, "", model.RoleAdmin, true, true); err != nil {
		t.Errorf("更新缺失账号应返回 nil: %v", err)
	}

	// 现在只剩一个管理员，删除应被拒绝
	if err := s.DeleteUserGuarded(admin2.ID, true); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("删除最后管理员应返回 ErrLastAdmin，实际 %v", err)
	}

	// 非管理员账号不受保底限制
	op := &model.User{Username: "op", Role: model.RoleOperator, Enabled: true}
	if err := s.CreateUser(op, "h"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUserGuarded(op.ID, true); err != nil {
		t.Errorf("删除普通账号应允许: %v", err)
	}
}

func TestSessions(t *testing.T) {
	s := newTestStore(t)
	u := &model.User{Username: "u", Role: model.RoleAdmin, Enabled: true}
	if err := s.CreateUser(u, "h"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()

	if err := s.CreateSession(u.ID, "hash", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err := s.SessionUser("hash", now)
	if err != nil || got == nil || got.Username != "u" {
		t.Fatalf("SessionUser: %+v, err=%v", got, err)
	}

	// 过期会话无效
	if got, err := s.SessionUser("hash", now.Add(2*time.Hour)); err != nil || got != nil {
		t.Errorf("过期会话应无效，实际 %+v, err=%v", got, err)
	}

	// 停用账号后会话无效
	if err := s.UpdateUser(u.ID, "", model.RoleAdmin, false); err != nil {
		t.Fatal(err)
	}
	if got, err := s.SessionUser("hash", now); err != nil || got != nil {
		t.Errorf("停用账号的会话应无效，实际 %+v, err=%v", got, err)
	}

	if err := s.DeleteSession("hash"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUserSessions(u.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteExpiredSessions(now); err != nil {
		t.Fatal(err)
	}

	// 创建新会话，停用用户时事务应连带清理
	if err := s.CreateSession(u.ID, "hash2", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateUserGuarded(u.ID, "", model.RoleAdmin, false, false); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id=?`, u.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("停用账号后应清空会话，实际剩 %d", n)
	}
}

func TestDeleteUser_CleansSessions(t *testing.T) {
	s := newTestStore(t)
	u := &model.User{Username: "u", Role: model.RoleOperator, Enabled: true}
	if err := s.CreateUser(u, "h"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession(u.ID, "h", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(u.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id=?`, u.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("删除账号后应清空会话，实际剩 %d", n)
	}
}

func TestPublicQueries(t *testing.T) {
	s := newTestStore(t)
	// media_type / vote_average 由 UpdateScraped 写入，门户收录刮削成功的记录，
	// 所以这里走 Create → UpdateScraped（→ UpdatePushed）的真实链路。
	mk := func(share, title string, year int, mt model.MediaType, rating float64, pushed bool, sharedAt string, tmdbID int64) *model.Link {
		l := &model.Link{
			ShareID: share, URL: "https://guangyapan.com/s/" + share + "_x",
			Status: model.StatusPending, SharedAt: sharedAt,
		}
		mustCreateLink(t, s, l)
		scraped := &model.Link{
			Title: title, Year: year, MediaType: mt, VoteAverage: rating,
			TMDBID: tmdbID,
		}
		if err := s.UpdateScraped(l.ID, scraped); err != nil {
			t.Fatal(err)
		}
		if pushed {
			if err := s.UpdatePushed(l.ID, 1); err != nil {
				t.Fatal(err)
			}
		}
		return l
	}
	p1 := mk("p1", "电影A", 2020, model.MediaMovie, 7.0, true, "2026-01-01", 101)
	mk("p2", "剧集B", 2021, model.MediaTV, 9.0, true, "2026-02-01", 102)
	mk("p3", "电影C", 2020, model.MediaMovie, 5.0, true, "", 103)
	p4 := mk("p4", "待推送D", 2022, model.MediaMovie, 8.0, false, "2026-03-01", 104)
	// 脏数据：javascript: 链接必须被过滤为空
	bad := mk("p5", "脏链接E", 2019, model.MediaMovie, 6.0, true, "2026-04-01", 105)
	if _, err := s.db.Exec(`UPDATE links SET url='javascript:alert(1)' WHERE id=?`, bad.ID); err != nil {
		t.Fatal(err)
	}
	// 同一个 TMDB 的第二条分享：门户合并成一张卡，代表行取最近分享的那条
	same := mk("p7", "电影A", 2020, model.MediaMovie, 7.0, true, "2026-06-01", 101)
	// 还没刮削的记录不应出现在门户里
	pending := &model.Link{
		ShareID: "p6", URL: "https://guangyapan.com/s/p6_x",
		Status: model.StatusPending, SharedAt: "2026-05-01",
	}
	mustCreateLink(t, s, pending)

	items, total, err := s.ListPublic(model.PublicFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || len(items) != 5 {
		t.Fatalf("门户应按 TMDB 合并为 5 条（含待推送），实际 total=%d len=%d", total, len(items))
	}
	// 同一 TMDB 的两条分享只出现一次，链接数如实统计
	merged := 0
	for _, it := range items {
		if it.TMDBID == 101 {
			merged++
			if it.ID != same.ID {
				t.Errorf("合并后的代表行应取最近分享的 %d，实际 %d", same.ID, it.ID)
			}
			if it.LinkCount != 2 || len(it.Links) != 2 {
				t.Errorf("同 TMDB 应合并 2 条链接，实际 link_count=%d links=%d", it.LinkCount, len(it.Links))
			}
			if len(it.Links) == 2 && it.Links[0].SharedAt != "2026-06-01" {
				t.Errorf("链接应最近分享在前，实际 %+v", it.Links)
			}
		}
		if it.ID == bad.ID && it.URL != "" {
			t.Errorf("javascript: 链接未被过滤: %q", it.URL)
		}
	}
	if merged != 1 {
		t.Errorf("TMDB 101 应只出现 1 次，实际 %d", merged)
	}

	// 分类筛选
	items, _, err = s.ListPublic(model.PublicFilter{MediaType: model.MediaMovie})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 4 {
		t.Errorf("电影筛选应命中 4 条，实际 %d", len(items))
	}

	// 关键词
	items, _, err = s.ListPublic(model.PublicFilter{Keyword: "剧集B"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Errorf("关键词应命中 1 条，实际 %d", len(items))
	}

	// 年份
	items, _, err = s.ListPublic(model.PublicFilter{Year: 2020})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Errorf("2020 年应命中 2 条，实际 %d", len(items))
	}

	// 排序：评分降序
	items, _, err = s.ListPublic(model.PublicFilter{Sort: "rating", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) < 2 || items[0].VoteAverage < items[1].VoteAverage {
		t.Errorf("rating 排序异常: %+v", items)
	}

	// 默认排序把空 shared_at 放最后
	items, _, err = s.ListPublic(model.PublicFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if items[len(items)-1].SharedAt != "" {
		t.Errorf("空 shared_at 应排最后，实际最后一条 %q", items[len(items)-1].SharedAt)
	}

	// 未知排序回退默认
	if _, _, err := s.ListPublic(model.PublicFilter{Sort: "bogus"}); err != nil {
		t.Errorf("未知排序不应报错: %v", err)
	}

	// 统计
	st, err := s.PublicStats(model.PublicFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if st.Total != 5 || st.Movies != 4 || st.TV != 1 || st.LibraryTotal != 5 {
		t.Errorf("统计异常: %+v", st)
	}
	if len(st.Years) != 4 {
		t.Errorf("年份选项应 4 组（2019/2020/2021/2022），实际 %d: %+v", len(st.Years), st.Years)
	}
	for _, y := range st.Years {
		if y.Year == 0 {
			t.Error("年份选项不应包含 0")
		}
	}

	// 站点地图与列表共用分组口径：同 TMDB 只出现一次，最近分享的链接排最前
	sm, err := s.Sitemap(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if sm.Total != 5 || len(sm.Entries) != 5 {
		t.Errorf("sitemap 应按 TMDB 合并为 5 条，实际 total=%d len=%d", sm.Total, len(sm.Entries))
	}
	if len(sm.Entries) == 5 && sm.Entries[0].ID != same.ID {
		t.Errorf("sitemap 首条应为最近分享的 %d，实际 %d", same.ID, sm.Entries[0].ID)
	}

	// GetPublic：任一分享 ID 都能取到整组；未刮削的读不到
	for _, id := range []int64{p1.ID, same.ID} {
		p, err := s.GetPublic(id)
		if err != nil || p == nil {
			t.Fatalf("同组分享 %d 应可读: %+v err=%v", id, p, err)
		}
		if p.ID != same.ID || p.LinkCount != 2 || len(p.Links) != 2 {
			t.Errorf("GetPublic(%d) 应返回整组并取最近分享为代表: %+v", id, p)
		}
	}
	if p, err := s.GetPublic(bad.ID); err != nil || p == nil {
		t.Errorf("已推送记录应可读: %+v err=%v", p, err)
	}
	if p, err := s.GetPublic(p4.ID); err != nil || p == nil {
		t.Errorf("待推送记录应可读: %+v err=%v", p, err)
	}
	if p, err := s.GetPublic(pending.ID); err == nil || p != nil {
		t.Errorf("未刮削记录应返回错误，实际 %+v err=%v", p, err)
	}
}

func TestSafeLinkURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://a.com/x", "https://a.com/x"},
		{"http://a.com", "http://a.com"},
		{"javascript:alert(1)", ""},
		{"data:text/html,x", ""},
		{"ftp://a.com", ""},
		{"not a url", ""},
		{"", ""},
		{"//a.com", ""},
	}
	for _, c := range cases {
		if got := safeLinkURL(c.in); got != c.want {
			t.Errorf("safeLinkURL(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}

func TestLoginAttempts(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()

	a, err := s.LoginAttemptGet("ip:1.2.3.4")
	if err != nil || a.Fails != 0 || !a.Until.IsZero() {
		t.Fatalf("初始状态异常: %+v err=%v", a, err)
	}

	// 未达阈值只是累计失败次数
	if err := s.LoginAttemptFail("ip:1.2.3.4", now, 3, time.Minute); err != nil {
		t.Fatal(err)
	}
	a, _ = s.LoginAttemptGet("ip:1.2.3.4")
	if a.Fails != 1 || !a.Until.IsZero() {
		t.Errorf("第一次失败后: %+v", a)
	}

	if err := s.LoginAttemptFail("ip:1.2.3.4", now, 3, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := s.LoginAttemptFail("ip:1.2.3.4", now, 3, time.Minute); err != nil {
		t.Fatal(err)
	}
	a, _ = s.LoginAttemptGet("ip:1.2.3.4")
	if a.Fails != 0 || a.Until.IsZero() {
		t.Errorf("达到阈值应锁定并清零计数: %+v", a)
	}

	// 锁定过期后重新计数
	later := now.Add(2 * time.Minute)
	if err := s.LoginAttemptFail("ip:1.2.3.4", later, 3, time.Minute); err != nil {
		t.Fatal(err)
	}
	a, _ = s.LoginAttemptGet("ip:1.2.3.4")
	if a.Fails != 1 || !a.Until.IsZero() {
		t.Errorf("锁定过期后应重新计数: %+v", a)
	}

	// 重置
	if err := s.LoginAttemptReset("ip:1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	a, _ = s.LoginAttemptGet("ip:1.2.3.4")
	if a.Fails != 0 || !a.Until.IsZero() {
		t.Errorf("重置后应为空: %+v", a)
	}

	// 清理：到期的清零记录被删除
	if err := s.LoginAttemptFail("u:bob", now, 1, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := s.LoginAttemptCleanup(now.Add(2 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM login_attempts WHERE key='u:bob'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("到期记录应被清理，实际剩 %d", n)
	}
}

#!/usr/bin/env bash
# 冒烟测试：在临时目录起一个隔离实例，跑通「安装 → 登录 → 门户/SEO → 后台读取 → 退出」。
# 全程不访问外网、不碰仓库里的 config.yaml 与 data/，任何一步失败即退出非 0。
#
# 用法：make smoke（需要先 make web 生成嵌入前端，再 make build）
set -uo pipefail

BIN="${BIN:-bin/guangya}"
PORT="${PORT:-18099}"
BASE="http://127.0.0.1:${PORT}"
WORK="$(mktemp -d)"
PID=""
FAIL=0

cleanup() {
  if [[ -n "$PID" ]]; then
    kill "$PID" 2>/dev/null
    wait "$PID" 2>/dev/null
  fi
  rm -rf "$WORK"
}
trap cleanup EXIT

pass() { printf '  ✓ %s\n' "$1"; }
fail() {
  printf '  ✗ %s\n' "$1"
  FAIL=1
}

# expect_contains <说明> <实际> <期望子串>
expect_contains() {
  if [[ "$2" == *"$3"* ]]; then
    pass "$1"
  else
    printf '  ✗ %s：期望包含 %s，实际 %s\n' "$1" "$3" "${2:0:200}"
    FAIL=1
  fi
}

# expect_code <说明> <实际状态码> <期望状态码>
expect_code() {
  if [[ "$2" == "$3" ]]; then
    pass "$1（$2）"
  else
    printf '  ✗ %s：期望 HTTP %s，实际 %s\n' "$1" "$3" "$2"
    FAIL=1
  fi
}

get() { curl -sS -m 10 "$BASE$1" 2>/dev/null; }
get_code() { curl -sS -m 10 -o /dev/null -w '%{http_code}' "$BASE$1" 2>/dev/null; }
# post <路径> <JSON>；同源头是 CSRF 校验的前提，缺失会被 403
post() {
  curl -sS -m 10 -X POST \
    -H 'Content-Type: application/json' -H "Origin: $BASE" \
    -d "$2" "$BASE$1" 2>/dev/null
}
post_code() {
  curl -sS -m 10 -o /dev/null -w '%{http_code}' -X POST \
    -H 'Content-Type: application/json' -H "Origin: $BASE" \
    -d "$2" "$BASE$1" 2>/dev/null
}

if [[ ! -x "$BIN" ]]; then
  echo "找不到可执行文件 $BIN，请先 make build"
  exit 1
fi
if [[ ! -f internal/web/dist/index.html ]]; then
  echo "嵌入前端缺失，请先 make web && make build"
  exit 1
fi

cat >"$WORK/config.yaml" <<YAML
server:
  addr: "127.0.0.1:${PORT}"
  secure_cookie: false
storage:
  dsn: ${WORK}/data/guangya.db
  data_dir: ${WORK}/data
  poster_dir: ${WORK}/data/posters
tmdb:
  api_key: ""
telegram:
  token: ""
proxy:
  enable: false
YAML

"$BIN" -config "$WORK/config.yaml" >"$WORK/server.log" 2>&1 &
PID=$!

# 等健康检查通过，最多 5 秒
ready=""
for _ in $(seq 1 50); do
  if [[ "$(get_code /api/health)" == "200" ]]; then
    ready=1
    break
  fi
  if ! kill -0 "$PID" 2>/dev/null; then
    echo "服务进程已退出："
    tail -20 "$WORK/server.log"
    exit 1
  fi
  sleep 0.1
done
if [[ -z "$ready" ]]; then
  echo "服务在 5 秒内没有就绪："
  tail -20 "$WORK/server.log"
  exit 1
fi

echo '安装向导'
expect_contains '健康检查' "$(get /api/health)" '"status":"ok"'
expect_contains '初始状态未安装' "$(get /api/setup/status)" '"installed":false'
# 带跨站 Origin 的写请求必须被 CSRF 中间件挡掉（不带 Origin 的命令行客户端是放行的）
cross_site_code="$(curl -sS -m 10 -o /dev/null -w '%{http_code}' -X POST \
  -H 'Content-Type: application/json' -H 'Origin: http://evil.invalid' \
  -d '{"username":"smoke-admin","password":"Smoke#12345"}' "$BASE/api/setup" 2>/dev/null)"
expect_code '跨站写请求被拒' "$cross_site_code" "403"
expect_contains '创建管理员' "$(post /api/setup '{"username":"smoke-admin","password":"Smoke#12345"}')" '"ok":true'
expect_contains '安装后状态翻转' "$(get /api/setup/status)" '"installed":true'
expect_code '禁止重复安装' "$(post_code /api/setup '{"username":"smoke-admin","password":"Smoke#12345"}')" "403"

echo '登录与鉴权'
expect_code '未登录访问后台接口' "$(get_code /api/links)" "401"
expect_code '密码错误' "$(post_code /api/auth/login '{"username":"smoke-admin","password":"Wrong#12345"}')" "401"
login_code="$(curl -sS -m 10 -c "$WORK/ck" -o /dev/null -w '%{http_code}' -X POST \
  -H 'Content-Type: application/json' -H "Origin: $BASE" \
  -d '{"username":"smoke-admin","password":"Smoke#12345"}' "$BASE/api/auth/login" 2>/dev/null)"
expect_code '登录成功' "$login_code" "200"
expect_contains '统计接口带 total' "$(curl -sS -m 10 -b "$WORK/ck" "$BASE/api/stats" 2>/dev/null)" '"total"'
expect_contains '列表接口带 total/items' \
  "$(curl -sS -m 10 -b "$WORK/ck" "$BASE/api/links?limit=5" 2>/dev/null)" '"total":0'

echo '门户与 SEO'
index_html="$(get /)"
expect_contains '首页注入 canonical' "$index_html" '<link rel="canonical"'
expect_contains '首页是 SPA 壳' "$index_html" '<div id="app">'
expect_contains '后台路由回落到 SPA' "$(get /admin/links)" '<div id="app">'
expect_contains 'robots.txt 声明 sitemap' "$(get /robots.txt)" 'Sitemap: '"$BASE"'/sitemap.xml'
sitemap="$(get /sitemap.xml)"
expect_contains 'sitemap 是 urlset' "$sitemap" '<urlset'
# 空库时只有首页一条；深链与 lastmod 的分支由 internal/api/seo_test.go 覆盖
expect_contains 'sitemap 收录首页' "$sitemap" "<loc>${BASE}/</loc>"
expect_code 'sitemap 带缓存头' "$(curl -sS -m 10 -o /dev/null -w '%{http_code}' -D "$WORK/sitemap.hdr" "$BASE/sitemap.xml" 2>/dev/null)" "200"
expect_contains 'sitemap 响应有 Cache-Control' "$(cat "$WORK/sitemap.hdr")" 'Cache-Control: public, max-age=3600'
expect_contains '结构化数据是 WebSite' "$(get /api/structured-data)" '"WebSite"'
expect_code '不存在的资源深链 404' "$(get_code '/api/portal/link/999999')" "404"

echo '退出登录'
expect_code '登出' "$(curl -sS -m 10 -b "$WORK/ck" -o /dev/null -w '%{http_code}' -X POST \
  -H "Origin: $BASE" "$BASE/api/auth/logout" 2>/dev/null)" "200"
expect_code '登出后后台接口失效' "$(get_code /api/stats)" "401"

if [[ "$FAIL" != "0" ]]; then
  echo
  echo '冒烟失败，服务日志尾部：'
  tail -30 "$WORK/server.log"
  exit 1
fi
echo
echo 'SMOKE PASS'

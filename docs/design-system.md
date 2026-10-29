# 光鸭 GO 界面设计规范

面向 `web/` 的视觉与交互约定，取色与气质对齐 qoder.com，落地以现有实现为准。
产品流程见 `docs/design.md`，选型见 `docs/tech-selection.md`；本文只管"长什么样、怎么摆、怎么反馈"。

> 更新于 2026-10-29。改 UI 时先查本规范，新增 token 或组件必须先写进这里。

---

## 1. 设计原则

1. **内容优先**：海报、片名、数字是主角。界面只用 1px 细线、浅灰底和留白分组，不用大面积色块和重阴影。
2. **只有一种品牌色**：可点击的主操作、当前筛选、焦点环统一用品牌绿；ok / warn / bad / info 仅用于表达状态。
3. **克制圆角**：控件 6px、卡片 8px，只有筛选标签、徽标、头像类元素用全圆角。全站不出现 12px 以上的大圆角（登录页 Logo 底 12px 为唯一例外）。
4. **宋体标题 + 无衬线正文**：标题负责气质，正文负责密度与可读性。
5. **反馈即时、破坏可逆**：轻操作给 toast，破坏性操作先确认，任何列表页都有加载 / 空 / 错三态。

---

## 2. Design Token

全部定义在 `web/src/style.css` 的 `@theme` 块里，Tailwind 直接以 `--color-*` 变量消费。

### 2.1 颜色

| Token | 值 | 用途 | 白底对比度 |
| --- | --- | --- | --- |
| `--color-canvas` | `#f2f4f2` | 页面底色（body / html） | — |
| `--color-surface` | `#ffffff` | 卡片、表头、弹层、输入框底 | — |
| `--color-surface-sunken` | `#f3f3f3` | 分段控件槽、统计格、代码块、提取码块 | — |
| `--color-surface-hover` | `#eeeeeb` | 悬停底、海报占位、进度条槽 | — |
| `--color-line` | `#e6e6e6` | 卡片边框、表格行分隔、页头分隔 | — |
| `--color-line-strong` | `#dddddd` | 输入框、次级按钮、筛选下拉的描边 | — |
| `--color-ink` | `#141414` | 标题与正文主色 | 18.4:1 |
| `--color-muted` | `#636261` | 说明文字、次要信息、未选中项 | 6.1:1 |
| `--color-faint` | `#7b7b76` | 占位符、图标、页脚、极次要提示 | 4.3:1 |
| `--color-brand` | `#2f7452` | 主按钮、当前导航/筛选、链接、焦点环、进度 | 5.6:1 |
| `--color-brand-hover` | `#245c41` | 主按钮悬停 | 7.8:1 |
| `--color-brand-soft` | `#eaf7e7` | 选中项浅底、品牌色提示块 | — |
| `--color-brand-line` | `#cdecc8` | 品牌色描边、输入焦点外环、选中块边界 | — |
| `--color-ok` / `--color-ok-soft` | `#35734a` / `#eefaf1` | 成功、已推送、已刮削 | 5.3:1 |
| `--color-warn` / `--color-warn-soft` | `#8a5a00` / `#fefbe8` | 待核查、未匹配、剧集标识 | 5.7:1 |
| `--color-bad` / `--color-bad-soft` | `#bf3a32` / `#fdf2f0` | 失败、错误提示、危险按钮 | 5.0:1 |
| `--color-info` / `--color-info-soft` | `#3563c4` / `#e9f6fe` | 进行中、电影标识 | 5.1:1 |

写法约定：

```html
<!-- 统一用变量，不在组件里写裸十六进制 -->
<span class="text-[var(--color-muted)]">…</span>
<div class="border border-[var(--color-line)] bg-[var(--color-surface)]">…</div>
```

使用红线：

- **`faint` 不承载正文**（白底 4.25:1，浅底更低），只能用于占位符、图标和页脚。
- 状态色必须成对使用 `bg-*-soft` + `text-*`，不要用纯色底铺大面积。
- 同一屏内品牌绿只出现在"当前选中 + 主操作 + 链接"三类元素上。

### 2.2 圆角、描边、阴影

| 层级 | 配方 | 用于 |
| --- | --- | --- |
| 控件 | `rounded-[var(--radius-control)]` = 6px | 按钮、输入框、下拉、分段控件槽 |
| 卡片 | `rounded-[var(--radius-card)]` = 8px | 卡片、弹窗、海报、toast |
| 标签 | `rounded-full` | 筛选胶囊、状态徽标、分段按钮 |
| 唯一例外 | `rounded-xl` = 12px | 登录页 Logo 底座 |

| 层级 | 阴影 | 用于 |
| --- | --- | --- |
| 静止 | `shadow-[0_1px_3px_rgba(20,20,20,0.05)]` | 常规卡片 |
| 浮起 | `shadow-[0_6px_20px_rgba(20,20,20,0.12)]` | toast |
| 弹层 | `shadow-[0_12px_40px_rgba(20,20,20,0.18)]` | 确认框（小面板） |
| 媒体弹窗 | `shadow-xl` | 资源详情这类大面积弹窗 |
| 分组选中 | `shadow-sm` | 分段控件里被选中的那一段 |

遮罩：轻量确认用 `bg-black/30`；媒体类弹层用 `bg-black/40 backdrop-blur-sm`。

### 2.3 字体与字号

字体栈（`web/src/style.css`）：

```css
/* 正文：拉丁优先 Instrument Sans，中文回退苹方 / 雅黑 */
'Instrument Sans', -apple-system, BlinkMacSystemFont, 'PingFang SC', 'Microsoft YaHei', 'Segoe UI', sans-serif

/* 标题 h1–h3：往宋体回退，未装思源宋体时落到 Songti SC */
'Instrument Sans', 'Source Han Serif SC', 'Noto Serif CJK SC', 'Songti SC', 'SimSun', serif
```

> 现状：`Instrument Sans` 未自托管，也没有外链字体，实际渲染以系统字体为准。
> 若后续引入，放 `web/public/fonts/` 并写 `@font-face`，**不要**依赖 Google Fonts（部署环境是内网）。

| 角色 | 字号 / 行高 | 字重 | 字体 | 示例 |
| --- | --- | --- | --- | --- |
| 门户主标题 | 30px（sm 起 36px） | 400 | 宋体栈 | 光鸭片库 |
| 弹窗 / 登录标题 | 20px `text-xl` | 400 | 宋体栈 | 片名、光鸭 GO |
| 卡片标题 | 15px | 500 | 宋体栈 | 资源库、录入解析 |
| 统计数字 | 24px `text-2xl` | 500 | 无衬线 | 待刮削 12 |
| 正文 / 控件 | 14px，行高 1.6 | 400 | 无衬线 | 按钮、输入框、表格 |
| 主要 UI 文本 | 13px | 400 / 500 | 无衬线 | 说明、筛选、表格内容、toast |
| 辅助文本 | 12px `text-xs` | 400 | 无衬线 | 海报副标题、徽标 |
| 极小标识 | 10–11px | 500 | 无衬线 | 步骤号、评分角标 |

规则：

- 正文最小 13px；10–11px 只用于步骤号、评分角标这类短标识。
- 数字列表、计数、进度统一加 `tabular-nums`，避免跳动。
- 段落的可读行高用 `leading-relaxed`，标题用 `leading-snug`，主标题用 `tracking-tight`。

### 2.4 间距与尺寸

以 4px 为基准，只取 `2 / 4 / 6 / 8 / 10 / 12 / 14 / 16 / 20 / 24 / 28 / 32 / 36`。

| 场景 | 值 |
| --- | --- |
| 页面左右留白 | `px-6`（24px），所有页面一致 |
| 后台内容区上下留白 | `py-6`（24px） |
| 门户 hero 上 / 下留白 | `pt-9` / `pb-6`（sm 起 `pb-7`） |
| 卡片内边距 | `p-5`（20px），卡头下间距 `mb-4` |
| 卡片间距 | 纵向 `mb-5`，网格 `gap-4` ~ `gap-y-6` |
| 控件组的间距 | `gap-1`（同组）、`gap-2`（跨组） |
| 页头高度 | 门户 `py-3.5`，后台 `py-4` |

控件高度：

| 控件 | 高度 |
| --- | --- |
| 小按钮 `size="sm"` | 32px `h-8` |
| 默认按钮 `size="md"` | 36px `h-9` |
| 大按钮 `size="lg"` | 40px `h-10` |
| 输入框 / 筛选下拉 | 36px `h-9` / `h-8` |
| 门户搜索框 | 44px `h-11` |
| 纯图标按钮 | 32px × 32px，无边框 |

### 2.5 动效

| 场景 | 时长 / 曲线 |
| --- | --- |
| 颜色、描边、底色变化 | `transition-colors`，150ms |
| 海报悬停放大 | `scale-[1.03]`，300ms |
| 进度条增长 | `transition-all`，300ms |
| toast 进出 | `translate-y-1 + opacity`，150ms |

不引入回弹、弹性或超过 300ms 的装饰性动画。

### 2.6 层级

| 层 | z-index | 说明 |
| --- | --- | --- |
| 页头 / 内容 | 默认 | 不吸顶，不悬浮 |
| 弹窗、确认框、toast | `z-50` | 同一层级，互不叠加使用 |

---

## 3. 布局

### 3.1 容器与断点

| 容器 | 宽度 | 用于 |
| --- | --- | --- |
| 内容主容器 | `max-w-7xl`（1280px） | 后台全部页面、门户页头与列表 |
| 门户 hero | `max-w-2xl`（672px） | 标题、搜索、筛选栏（三者同宽） |
| 登录卡 | `max-w-sm`（384px） | 登录页、确认框 |
| 详情弹窗 | `max-w-3xl`（768px） | 资源详情 |

断点沿用 Tailwind 默认：`sm 640` / `md 768` / `lg 1024` / `xl 1280`。

### 3.2 门户页（`PortalView.vue`）

```
┌───────────────────────────────────────────────┐
│ [logo] 光鸭 GO / 资源库            已收录 239 部 │  页头：白底 + 1px 下边线
├───────────────────────────────────────────────┤
│                   光鸭片库                      │  hero 居中，max-w-2xl
│      已发布到频道的资源，搜索片名就能找到分享链接     │
│   ┌───────────────────────────────────────┐   │
│   │ 🔍 搜索片名 / 原名 / 类型             ✕ │   │  搜索框 h-11 全宽
│   └───────────────────────────────────────┘   │
│   全部 239  电影 61  剧集 178    [最新|评分] [年份▾] ↺ │  左分类 / 右排序+年份
├───────────────────────────────────────────────┤
│  海报网格（2 / 3 / 4 / 5 / 6 列）                │  max-w-7xl，px-6 py-7
│  …                                            │
│            已显示 24 / 239 部                  │
│               [ 加载更多 ]                     │
├───────────────────────────────────────────────┤
│        共 239 部资源 · 数据来自光鸭网盘与 TMDB    │
└───────────────────────────────────────────────┘
```

规则：

- **搜索框与筛选栏必须等宽**（都撑满 hero 容器）；不要出现"搜索窄、筛选宽"的错位。
- 筛选栏分两簇：左侧分类（全部 / 电影 / 剧集），右侧排序分段控件 + 年份下拉 +（有筛选时）重置图标。
  桌面端 `justify-between` 顶两端；`< sm` 时上下两行各自居中。
- 分类胶囊：`rounded-full px-3 py-1.5 text-[13px]`，选中 `bg-brand-soft text-brand font-medium`，未选中 `text-muted hover:bg-surface-hover`。
- 排序用**一个** `bg-surface-sunken rounded-full p-0.5` 分段控件，选中段是 `bg-surface shadow-sm`；不要做成两个独立胶囊。
- 年份下拉：`h-8 appearance-none pl-3 pr-8 rounded-full border`，外部叠 `ChevronDown h-3.5`；选中年份整颗变品牌色。
- 有任意筛选条件时，在年份右侧出现 32px 的图标式"重置筛选"按钮（`RotateCcw`），无筛选时不留空位。
- 海报卡：`aspect-[2/3]`、`rounded-lg`、1px 边框；悬停时图片 `scale-[1.03]`。评分角标固定在右上（`bg-black/65 text-white text-[11px]`），电影/剧集徽标固定在左下。
- 网格：`grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6`，间距 `gap-x-4 gap-y-6`。
- 列表底部固定"已显示 x / y 部" + `加载更多`（首屏不分页页码）。
- 门户**不暴露原始分享 URL**，只给"复制链接（含提取码）"和"打开光鸭"两个动作。

### 3.3 后台页（`AdminShell.vue`）

```
┌───────────────────────────────────────────────┐
│ [logo] 光鸭 GO   后台 · 录入→刮削→推送  用户 角色 前台 退出 │
│ [① 录入解析] [② 刮削队列] [③ 推送频道] [▤ 资源库] [⚙ 设置] │  导航胶囊
├───────────────────────────────────────────────┤
│ ┌ 待刮削 12 ┐┌ 未匹配 1 ┐┌ 待推送 8 ┐┌ 已推送 239 ┐ │  概览统计格（可点击跳转）
│ ┌───────────────────────────────────────────┐ │
│ │ 卡片：标题 + 计数 + 视图切换                   │ │
│ │ 工具栏：搜索 / 状态筛选 / 批量操作 ← 右对齐      │ │
│ │ 表格或海报墙 + 分页                            │ │
│ └───────────────────────────────────────────┘ │
└───────────────────────────────────────────────┘
```

规则：

- 后台使用**顶部导航**而非侧栏：主容器 `max-w-7xl px-6`，页头白底 + 下边线。
- 导航胶囊 `rounded-lg px-3 py-1.5 text-sm`，选中 `bg-brand-soft text-brand font-medium`；前三步显示 4px 圆圈步骤号，其余显示 `h-4` 图标。
- 概览统计格 `grid-cols-2 sm:grid-cols-4`，每格 `bg-surface-sunken rounded-lg p-3`，上标签 13px，下数字 24px，整格可点击。
- 每个功能区包一张 `Card`（8px 圆角 + 1px 边 + 极小阴影），卡头左侧标题 15px + 同行 13px 说明，右侧放视图切换等次要动作。
- 工具栏顺序固定：搜索框（`max-w-xs`，内嵌 `Search` 图标）→ 状态胶囊组 → 右侧批量操作（`ml-auto`）。
- 表格 `text-[13px]`，表头 `text-muted font-medium`，行分隔用 `border-b border-line`，单元格 `py-2 pr-4`；操作列右对齐、保持 24px 宽。
- 总数写在卡片标题右侧（`共 n 条 · 说明`），分页只显示"第 x / y 页" + 上一页/下一页（outline sm），不用页码列表。

### 3.4 登录页

- 整体 `min-h-screen` 居中，卡宽 `max-w-sm`，上下留 40px。
- 顶部 44px 品牌方块（`rounded-xl`，内含 24px `Cloud` 图标）+ 20px 标题 + 13px 说明。
- 表单卡与 `Card` 同配方 `p-5`；字段标签 13px `font-medium`，字段间距 16px。
- 错误块：`bg-bad-soft text-bad rounded-lg px-3 py-2 text-[13px]`，图标 16px 顶对齐。
- 主按钮占满宽度 `mt-5 w-full`，提交中显示 16px 旋转图标并禁用。

### 3.5 弹层

| 类型 | 规格 |
| --- | --- |
| 详情弹窗 | `max-w-3xl`，`max-h-[90vh]` 可滚动，`p-5 shadow-xl`，遮罩 `bg-black/40 backdrop-blur-sm`；海报 190px 列 + 右内容，`sm` 以下上下堆叠 |
| 确认框 | `max-w-sm p-5` + `0 12px 40px` 阴影，左 32px 圆形图标（危险 `bg-bad-soft text-bad`，普通 `bg-brand-soft text-brand`）+ 标题 15px + 说明 13px，底部右对齐两个 32px 按钮 |
| toast | 右下角 `bottom-5 right-5`，宽 `min(360px, 100vw-40px)`，`p-3 py-2.5`，13px 文案 |

三者共同的硬性要求：

- `role="dialog" / "alertdialog"` + `aria-modal="true"` + 可读的 `aria-label`。
- 打开时锁 `body` 滚动，关闭时恢复；`Esc` 关闭；点遮罩关闭（确认框同样允许）。
- 打开后焦点移入弹层，弹层本身 `tabindex="-1"`（确认框已实现；门户详情弹窗尚未接管焦点，待补）。
- 关闭按钮 16px 图标，放右上角 `right-3 top-3`，必须有 `aria-label="关闭"`。

---

## 4. 组件规范

### 4.1 Button（`components/ui/Button.vue`）

| 变体 | 样式 | 用途 |
| --- | --- | --- |
| `default` | 品牌绿底 + 白字，悬停 `brand-hover` | 页面主操作，每屏最多一个 |
| `outline` | 白底 + `line-strong` 边，悬停 `surface-hover` | 次要操作、取消、重试 |
| `ghost` | 无底无边，悬停浅灰 | 退出、清空筛选等低权重动作 |
| `danger` | `bad` 底 + 白字，悬停 `brightness-90` | 删除等破坏性操作 |

- 尺寸：`sm h-8 px-3` / `md h-9 px-4` / `lg h-10 px-5 text-[15px]`。
- 图标 16px，与文字间距 `gap-1.5`；纯图标按钮不加文字但必须有 `aria-label`。
- 焦点态：`focus-visible:outline-2 outline-offset-2 outline-brand`。
- 禁用态：`opacity-50 pointer-events-none`，不要改成灰色实底。

### 4.2 Input / Textarea / Select

- 输入框：`h-9 rounded-[6px] border-line-strong px-3 text-sm`；聚焦时边框变 `brand`，外加 `ring-2 ring-brand-line`。
- 搜索框内嵌 16px `Search` 图标（左 12px），输入内容时右侧出现清空按钮。
- 多行输入（粘贴分享文本）：`font-mono text-[13px] leading-relaxed resize-y`，默认 10 行。
- 原生 `<select>` 若保留箭头，统一 `appearance-none` + 自绘 `ChevronDown`，避免各浏览器外观不一致。

### 4.3 Card

```html
<section class="rounded-[8px] border border-line bg-surface p-5 shadow-[0_1px_3px_rgba(20,20,20,0.05)]">
  <header class="mb-4">…标题 15px / 说明 13px…</header>
  …内容
</section>
```

标题与说明同卡头纵向排列，说明 `mt-0.5 text-[13px] text-muted`；表单类页面每张卡只放一组相关设置。

### 4.4 Badge 与状态语义

`Badge` 固定 `rounded-full px-2.5 py-0.5 text-xs font-medium`，颜色由语义表指定，**不要在页面里临时拼状态色**。

| 状态 | 文案 | 配色 |
| --- | --- | --- |
| `pending` | 待处理 | `surface-hover` / `muted` |
| `review` | 待核查 | `warn-soft` / `warn` |
| `scraping` | 刮削中 | `info-soft` / `info` |
| `scraped` | 已刮削 | `ok-soft` / `ok` |
| `unmatched` | 未匹配 | `warn-soft` / `warn` |
| `pushed` | 已推送 | `ok-soft` / `ok` |
| `push_failed` | 推送失败 | `bad-soft` / `bad` |
| `failed` | 失败 | `bad-soft` / `bad` |
| `movie` | 电影 | `info-soft` / `info` |
| `tv` | 剧集 | `warn-soft` / `warn` |

定义在 `web/src/lib/api.ts` 的 `statusMeta` / `mediaMeta`，新增状态两处同时补。

### 4.5 筛选胶囊与分段控件

- 筛选胶囊（分类、状态）：全圆角、13px、选中品牌浅底；同一组内 `gap-1`，不加分隔线。
- 分段控件（排序、表格/海报墙切换）：外层 `bg-surface-sunken rounded-[6px] / rounded-full p-0.5`，内层选中段 `bg-surface shadow-sm`。
- 两者不要混用：**筛选**表达"看哪一类"，**分段**表达"换一种看法"。

### 4.6 表格

- 13px；表头 12–13px `text-muted`，不加底色。
- 行高由 `py-2` 控制，行间只用 1px 分隔线，无斑马纹。
- 数字列右对齐或定宽，状态列固定 96px，操作列 96px。
- 空结果在表体中央给 13px `muted` 说明，不要把表头留着当骨架。
- 横向溢出交给 `overflow-x-auto`，不要缩字号。

### 4.7 任务进度（`TaskProgress.vue`）

- 容器：`rounded-lg border border-line p-3`，不放进 Card 里再套一层。
- 首行：16px 状态图标 + 当前条目（可截断）+ `done / total` + 可选"停止"（outline sm）。
- 进度条：`h-1.5 rounded-full bg-surface-hover`，填充 `brand`，完成后切 `ok`。
- 下方统计：成功（ok）、失败（>0 时 bad，否则 faint）、进行中/已停止徽标、最近错误（bad，`line-clamp-2`）。

### 4.8 Toast 与确认

- 成功 / 信息 / 错误：16px 图标 + 13px 文案 + 关闭按钮；成功与信息 3s 自动消失，错误停留 5s；同屏最多 4 条。
- 同一操作不要既弹 toast 又弹 confirm；确认成功后再 toast。
- 批量操作必须在确认文案里带数量，例如"删除所选 12 条记录？"。

### 4.9 空 / 加载 / 错误三态

| 态 | 表现 |
| --- | --- |
| 加载 | 居中 20px `Loader2 animate-spin` + `text-muted`，不显示骨架屏 |
| 空 | 32px `Inbox` 图标 + 13px 说明（说清为什么空）+ 可选一个恢复动作 |
| 错 | `bg-bad-soft text-bad rounded-lg px-3 py-2.5`，16px 图标 + "加载失败：原因" + 右侧"重试" outline sm |

筛选后为空时，空态必须给"清空筛选"，而不是只写"没有数据"。

---

## 5. 交互约定

- **输入防抖**：关键词 300ms，筛选切换 150ms 或立即，切换标签页立即。
- **请求竞态**：所有列表请求带自增序号，只让最后一次结果落地（`loadSeq` / `statsSeq` 模式）。
- **筛选联动**：分类数字跟随搜索变化；年份选项忽略年份本身、其余条件照常生效；统计值不改变已收录总数。
- **键盘**：`Enter` 触发行/卡片主操作，`Esc` 关闭弹层，`Tab` 走浏览器顺序，不写自定义焦点陷阱。
- **焦点**：所有可聚焦控件保留可见焦点环；弹层打开后焦点进入弹层。
- **复制**：优先 `navigator.clipboard`，失败回退隐藏 textarea；局域网 http 下必须能复制，成功 toast"链接已复制"。
- **破坏性操作**：删除、批量删除先 `confirmAction`；确认按钮文案写动词（"删除"/"清空"），默认焦点给取消。
- **不阻断**：单个请求失败不清空用户已看到的内容，除首屏加载外。

---

## 6. 文案

- 中文、短句、动词开头：按钮写"导出 CSV"不写"CSV"；状态写"已推送"不写"PUSHED"。
- 数量与单位之间留空格：`共 239 条`、`24 / 239 部`。
- 错误文案结构：`加载失败：<原因>`，原因来自后端 message，不吞掉。
- 空态文案说清楚"为什么"和"下一步"，例如"录入的链接要先在『刮削队列』完成 TMDB 刮削才会进库"。
- 不在前台暴露实现细节（接口名、share id、原始 URL）；后台可以显示 TMDB ID 等排障信息。
- 不用感叹号、emoji、颜文字；不使用"亲""哦"等口吻。

---

## 7. 可访问性

- 纯图标按钮必须有 `aria-label`，悬停提示用 `title` 补充。
- 图片 `alt` 用片名；纯装饰图标 `aria-hidden`。
- 页面只有一个 `h1`；卡片标题用 `h2`，不要跳级。
- 正文对比度 ≥ 4.5:1；`faint` 不用于正文（见 2.1）。
- 可点击区域不小于 32×32px；卡片整卡可点时保留键盘 `Enter` 支持。
- toast 容器 `aria-live="polite"`；弹层用 `aria-modal` 并锁滚动。

---

## 8. 新页面检查清单

- [ ] 用 `max-w-7xl px-6`（门户 hero 用 `max-w-2xl`）作容器，左右 24px 留白一致。
- [ ] 只使用本节 token，不新增十六进制颜色、不新增阴影写法。
- [ ] 标题走宋体栈、正文 ≥13px、数字加 `tabular-nums`。
- [ ] 主操作只有 1 个品牌绿按钮；其余用 outline / ghost。
- [ ] 列表具备加载、空、错三态，空态给恢复动作。
- [ ] 破坏性操作有确认弹窗，确认后给 toast。
- [ ] 弹层支持 `Esc`、遮罩关闭、焦点进入、`body` 锁滚动。
- [ ] 请求有竞态保护，筛选变化不叠加旧结果。
- [ ] 移动端 375px 宽度下无横向滚动，筛选栏能换行居中。
- [ ] 图标按钮有 `aria-label`，图片有 `alt`。

---

## 9. 禁止事项

- 引入第二主色、渐变、玻璃拟态、彩色阴影。
- 直接写 `text-gray-500` 这类原生调色板，或绕过 `@theme` 新增颜色。
- 在同一个区块里混用 6px / 8px / 12px 圆角。
- 用 `faint` 承载正文，或用纯色大面积铺状态色。
- 两个以上阴影同时出现在一张卡上。
- 为动画而动画：超过 300ms 的装饰动效、回弹曲线、自动轮播。
- 在门户页展示原始链接、接口字段或后台操作入口。

---

## 附：常用配方速查

```html
<!-- 页面容器 -->
<div class="mx-auto max-w-7xl px-6">

<!-- 卡片 -->
<section class="rounded-[var(--radius-card)] border border-[var(--color-line)] bg-[var(--color-surface)] p-5 shadow-[0_1px_3px_rgba(20,20,20,0.05)]">

<!-- 筛选胶囊（选中） -->
<button class="rounded-full px-3 py-1.5 text-[13px] bg-[var(--color-brand-soft)] font-medium text-[var(--color-brand)]">

<!-- 分段控件 -->
<div class="flex items-center gap-0.5 rounded-full bg-[var(--color-surface-sunken)] p-0.5">
  <button class="rounded-full px-2.5 py-1 text-[13px] bg-[var(--color-surface)] font-medium shadow-sm">

<!-- 错误条 -->
<div class="rounded-lg bg-[var(--color-bad-soft)] px-3 py-2.5 text-[13px] text-[var(--color-bad)]">

<!-- 次要说明 -->
<p class="text-[13px] text-[var(--color-muted)]">
```

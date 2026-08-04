# sub2api-account-monitor 实施计划

> 供应商余额 / 收益核算 / 稳定性监控 —— 架构与前端体系完全参考 [sub2api](F:\code\2api\sub2api)。
> 交互原型：早期静态原型稿已移除（正式前端见 `frontend/`）。

## 1. 项目背景与需求

用户运营基于 sub2api 的 API 网关，上游 key 购自多家供应商，**供应商基本也是 sub2api 站点**。sub2api 账号命名约定 `【供应商】描述`（如 `【walk】gpt pro`）。本项目为独立监控端，需求：

| # | 需求 | 实现方式 |
|---|---|---|
| 1 | 供应商余额监控 | 录入供应商站点地址+登录邮箱+密码 → **模拟登录供应商 sub2api 站点**，取余额 + 仪表盘信息（今日消费/请求数/Token/RPM·TPM），定时快照 |
| 2 | 分组倍率变化、今日收益/成本/利润 | 只读本站线上 Postgres：轮询 `groups/accounts.rate_multiplier` 记变化历史；`usage_logs` 聚合收益成本利润 |
| 3 | 稳定性监控（耗时/首字） | 双口径：主动探测（对上游 key 发最小流式请求测 TTFT/总耗时/成功率）+ 被动统计（usage_logs 的 duration_ms/first_token_ms 分位数） |
| 4 | 供应商快捷录入 | 扫描 accounts.name 的【】前缀去重 → 勾选一键创建；子账号按前缀**动态聚合**（不存静态关联） |
| 5 | 登录 | config.yaml 配置账密 + 自签 JWT |

## 2. 已核实的关键事实（源码依据）

### 2.1 供应商侧 sub2api 用户 API（模拟登录用）

- 登录：`POST {base_url}/api/v1/auth/login`，body `{email, password}`
  - 依据 `backend/internal/handler/auth_handler.go:73`（LoginRequest）、路由 `internal/server/routes/auth.go:34`（限流 20 次/分钟）
  - 响应 `{access_token, refresh_token, expires_in, token_type, user}`，**`user.balance` 即余额**（`handler/dto/types.go:16`）
  - 站点开 Turnstile（需 turnstile_token）或 2FA（走 /login/2fa）时无法自动登录 → 记错误，供应商可转手动记录
- 仪表盘：`GET {base_url}/api/v1/usage/dashboard/stats`（Bearer token）
  - 返回 `UserDashboardStats`（`internal/pkg/usagestats/usage_log_types.go:215`）：
    `today_requests / today_tokens / today_cost / today_actual_cost`、`total_requests / total_tokens / total_actual_cost`、`rpm / tpm / average_duration_ms`、`total_api_keys / active_api_keys`
- 响应 envelope 统一 `{code:0, message, data}`；`GET /api/v1/auth/me` 可随时取 user（含 balance）

### 2.2 本站线上 Postgres（只读接入）

连接：`<PG_HOST>:5432 / sub2api`（主机与凭据均在 config.yaml，不入库不入 git）。

- `accounts`：`name`（【walk】格式）、`platform`（anthropic/openai/gemini/...）、`type`（api_key/oauth/cookie）、`credentials` JSONB **明文**（`credentials->>'api_key'`）、`extra->>'base_url'`、`rate_multiplier`（**账号成本倍率**）、`status`、`schedulable`、`deleted_at`（软删除）
- `groups`：`rate_multiplier`（**用户扣费倍率**）、`platform`、`status`、`deleted_at`；`account_groups` 多对多
- `usage_logs`（append-only，仅成功请求）：`account_id`、`group_id`、`model`、tokens、**`total_cost`（官方价成本）**、**`actual_cost`（用户实扣）**、`rate_multiplier`、`account_rate_multiplier`（账号成本倍率快照）、`stream`、**`duration_ms`**、**`first_token_ms`**、`created_at`
  - 索引：(account_id,created_at)、(group_id,created_at)、(created_at)；金额单位 USD
- ~~**口径**：收益 = Σ`actual_cost`；成本 = Σ(`total_cost` × COALESCE(`account_rate_multiplier`,1))；利润 = 收益 − 成本~~
  > ⚠️ **该成本口径已废弃。** 本站 `usage_logs` 推算出的只是**官价**，不等于真实付出。实际成本改为从上游供应商站点按 key 拉取 `actual_cost`（倍率折后实扣）。收益口径不变。现行口径见 [README.md](./README.md#口径)。

### 2.3 sub2api 可复用范式

- 后端：gin + viper(yaml) + robfig/cron/v3 + golang-jwt/v5；三层 handler/service/repository；响应封装 `backend/internal/pkg/response/response.go`（`{code,message,data}` + `PaginatedData`）；前端嵌入 `internal/web/embed_on.go`（`//go:embed all:dist` + build tag `embed`）
- 前端：Vue3 + Vite5 + Pinia + vue-router4 + 纯 Tailwind 自研组件 + chart.js/vue-chartjs + vue-i18n + axios
- 复制资产清单见 §6.1；**复制的通用组件内部依赖 vue-i18n（$t），故保留 vue-i18n 仅带 zh 单语言**

## 3. 架构决策

| 决策点 | 结论 | 理由 |
|---|---|---|
| 后端框架 | Go + gin + viper + robfig/cron + golang-jwt/v5，手工构造函数注入 | 与 sub2api 同族；wire/ent 对小项目过重（KISS/YAGNI） |
| sub2api 数据 | pgx/v5 stdlib，DSN 强制 `default_transaction_read_only=on` | 只读双保险，杜绝误写线上库 |
| 自有数据 | 本地 SQLite（modernc.org/sqlite 纯 Go 免 CGO），WAL + MaxOpenConns(1)，内嵌 SQL migration | 不碰线上库；单用户低写入量足够 |
| 供应商↔子账号 | 动态前缀匹配 `ParseProviderName`，正则 `^【([^】]+)】`，运行时聚合 | 账号改名自动跟随，无关联表可维护性最高（DRY/单一事实源） |
| 余额获取 | `balance_type`: **sub2api**（主打）/ manual / none | 供应商基本是 sub2api；manual 兜底非标站点 |
| token 管理 | 供应商站点 access_token 缓存（列存），过期或 401 重登一次 | 30m 采集间隔远低于 20/min 登录限流 |
| 稳定性 | 被动（PG 分位数）+ 主动（流式探测），只探 `type='api_key'`，**探测默认关闭** | oauth/cookie 无法直接探测；探测烧真 token 须显式开启 |
| 登录 | config 账密（明文 constant-time 比较；`$2a$/$2b$` 前缀自动走 bcrypt）+ HS256 JWT 24h，无 refresh | 单管理员内网工具（KISS） |
| 时区 | config `timezone` 默认 Asia/Shanghai，须与 sub2api 一致；「今日」由 Go 算边界传绝对时间 | 对账口径一致 |
| 端口 | 后端 9090；前端 dev 3001（代理 /api → 9090）；生产 `go build -tags embed` 单二进制 | 避开 sub2api 的 8080/3000 |

## 4. 后端设计

### 4.1 目录结构

```
backend/
├── go.mod                          module sub2api-account-monitor
├── config.example.yaml             模板（config.yaml 含真实凭据，gitignore）
├── cmd/server/main.go              手工装配、启动 cron+gin、信号优雅退出
└── internal/
    ├── config/config.go            viper 加载+默认值+校验（~150 行）
    ├── server/
    │   ├── router.go               gin.Engine、路由注册、/health、embed 挂载
    │   └── middleware/auth.go      Bearer JWT 校验
    ├── handler/                    auth / dashboard / provider / stats / stability
    ├── service/
    │   ├── auth_service.go         密码比对 + 签发 JWT
    │   ├── provider_service.go     CRUD、ParseProviderName、scan/import、子账号聚合
    │   ├── sub2api_client.go       供应商站点客户端：登录、auth/me、dashboard/stats
    │   ├── balance_service.go      采集编排：token 缓存 → 拉数 → 落快照
    │   ├── stats_service.go        收益/成本/利润（PG 聚合 → Go 按前缀归并）
    │   ├── rate_service.go         倍率 diff → 写历史
    │   ├── probe_service.go        按 platform 构造最小流式请求，测 TTFT/总耗时
    │   └── scheduler.go            cron 装配 4 个 job
    ├── repository/
    │   ├── pg.go pg_accounts.go pg_usage.go        线上库只读查询
    │   ├── sqlite.go               打开+PRAGMA+迁移执行器（embed FS）
    │   ├── migrations/001_init.sql
    │   └── provider_repo.go balance_repo.go rate_repo.go probe_repo.go
    ├── pkg/
    │   ├── response/response.go    自 sub2api 复制裁剪（envelope + 分页）
    │   └── jwtutil/jwt.go          HS256 签发+解析（~60 行）
    └── web/embed_on.go embed_off.go dist/          简化 SPA 嵌入
```

依赖：`gin-gonic/gin`、`spf13/viper`、`robfig/cron/v3`、`golang-jwt/jwt/v5`、`jackc/pgx/v5`、`modernc.org/sqlite`、`golang.org/x/crypto`（bcrypt）。

### 4.2 SQLite DDL（migrations/001_init.sql）

```sql
CREATE TABLE providers (
  id                    INTEGER PRIMARY KEY AUTOINCREMENT,
  name                  TEXT NOT NULL UNIQUE,        -- 前缀名（不含【】），如 walk
  note                  TEXT NOT NULL DEFAULT '',
  balance_type          TEXT NOT NULL DEFAULT 'none',-- sub2api | manual | none
  base_url              TEXT NOT NULL DEFAULT '',    -- 供应商站点地址
  login_email           TEXT NOT NULL DEFAULT '',
  login_password        TEXT NOT NULL DEFAULT '',    -- 明文存本地（API 永不回显）
  access_token          TEXT NOT NULL DEFAULT '',    -- 供应商站点 JWT 缓存
  token_expires_at      TEXT,
  low_balance_threshold REAL NOT NULL DEFAULT 0,     -- 低于此值仪表盘标红（0=不判断）
  probe_enabled         INTEGER NOT NULL DEFAULT 0,
  probe_model           TEXT,                        -- NULL=用 config 平台默认模型
  last_balance          REAL,                        -- 冗余缓存，列表页免 join
  last_balance_at       TEXT,
  last_balance_error    TEXT,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);

CREATE TABLE balance_snapshots (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  provider_id INTEGER NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
  balance     REAL,                        -- NULL=本次抓取失败
  currency    TEXT NOT NULL DEFAULT 'USD',
  source      TEXT NOT NULL DEFAULT 'auto',-- auto | manual
  metrics     TEXT,                        -- JSON：dashboard/stats 摘要（today_requests/
                                           -- today_tokens/today_actual_cost/rpm/tpm/
                                           -- average_duration_ms/total_actual_cost）
  error       TEXT,                        -- 失败原因（截 500 字符）
  created_at  TEXT NOT NULL
);
CREATE INDEX idx_balance_snap_provider_time ON balance_snapshots(provider_id, created_at);

-- 当前倍率状态（diff 基准；首次观察只入此表，不写 history，避免首轮刷屏）
CREATE TABLE rate_state (
  entity_type TEXT NOT NULL,               -- group | account
  entity_id   INTEGER NOT NULL,            -- sub2api PG 中的 id
  entity_name TEXT NOT NULL,
  rate        REAL NOT NULL,
  updated_at  TEXT NOT NULL,
  PRIMARY KEY (entity_type, entity_id)
);

CREATE TABLE rate_change_history (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  entity_type TEXT NOT NULL,
  entity_id   INTEGER NOT NULL,
  entity_name TEXT NOT NULL,               -- 变化时点名称快照
  old_rate    REAL NOT NULL,
  new_rate    REAL NOT NULL,
  observed_at TEXT NOT NULL
);
CREATE INDEX idx_rate_hist_entity ON rate_change_history(entity_type, entity_id, observed_at);
CREATE INDEX idx_rate_hist_time   ON rate_change_history(observed_at);

CREATE TABLE probe_results (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  provider_id  INTEGER,                    -- 账号无前缀/供应商未录入时 NULL
  account_id   INTEGER NOT NULL,           -- PG accounts.id
  account_name TEXT NOT NULL,              -- 快照
  platform     TEXT NOT NULL,
  model        TEXT NOT NULL,
  base_url     TEXT NOT NULL DEFAULT '',   -- 实际探测目标（不含 key）
  source       TEXT NOT NULL DEFAULT 'schedule', -- schedule | manual
  success      INTEGER NOT NULL,           -- 2xx 且收到首个数据块
  status_code  INTEGER,                    -- 网络层失败为 NULL
  ttft_ms      INTEGER,                    -- 首个非空 SSE 行到达耗时
  total_ms     INTEGER,                    -- 流读完/失败时刻总耗时
  error        TEXT,                       -- 错误摘要（redact key，截 500 字符）
  created_at   TEXT NOT NULL
);
CREATE INDEX idx_probe_account_time  ON probe_results(account_id, created_at);
CREATE INDEX idx_probe_provider_time ON probe_results(provider_id, created_at);
CREATE INDEX idx_probe_time          ON probe_results(created_at);
```

时间戳统一 TEXT RFC3339 UTC，Go 层转换。`schema_migrations(filename, applied_at)` 由迁移执行器自建。

### 4.3 REST API（前缀 /api/v1，sub2api 同款 envelope，除 login、/health 外过 JWT）

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | /auth/login | `{username,password}` → `{token,expires_at,username}` |
| GET | /auth/me | 当前用户 |
| GET | /dashboard/summary?date= | 今日收益/成本/利润/请求数/供应商数/账号数 |
| GET | /dashboard/trend?days=7\|30 | 按日收益/成本/利润/请求趋势 |
| GET | /providers | 列表（含 account_count、last_balance 等；**login_password/access_token 永不回显**） |
| POST | /providers | 新建（name/note/balance_type/base_url/login_email/login_password/low_balance_threshold/probe_enabled/probe_model） |
| PUT | /providers/:id | 编辑（密码字段留空 = 不修改） |
| DELETE | /providers/:id | 删除（级联删快照；probe_results 留存 provider_id 置 NULL） |
| POST | /providers/test-connection | `{base_url,email,password}` → 即时登录返回 `{balance,...}`（弹窗「测试连接」） |
| GET | /providers/scan | 扫描 accounts.name 前缀 → `[{prefix,account_count,exists}]` |
| POST | /providers/import | `{names[]}` → `{created,skipped}` |
| GET | /providers/:id/accounts | 子账号（name/platform/type/status/rate_multiplier/groups；**key 绝不返回**） |
| POST | /providers/:id/balance/refresh | 同步采集一次，返回最新 snapshot |
| PUT | /providers/:id/balance | `{balance}` 手动记账（source=manual） |
| GET | /providers/:id/balance/history?days= | 快照历史 |
| GET | /stats/providers?start=&end= | 按供应商收益/成本/利润（含子账号明细 + 未归属桶） |
| GET | /stats/groups?start=&end= | 按分组（带当前倍率） |
| GET | /rates/history?entity_type=&entity_id=&page= | 倍率变化历史（分页） |
| GET | /stability/passive?hours= | 被动：按账号 p50/p95 总耗时与首字 |
| GET | /stability/probes?account_id=&page= | 探测明细（分页） |
| GET | /stability/probes/summary?hours= | 探测汇总（成功率/均值/最近状态） |
| GET | /stability/probes/trend?account_id=&hours= | 单账号探测点列 |
| POST | /stability/probe/run | `{account_id}` 同步返回 / `{provider_id}` 异步入队 |
| GET | /health | 免鉴权，`{status,pg,sqlite}` |

### 4.4 供应商采集流程（balance_service + sub2api_client）

1. token 缺失/过期 → `POST {base_url}/api/v1/auth/login`（email/password）→ 缓存 access_token + expires_at；**登录响应 user.balance 直接可用**
2. `GET {base_url}/api/v1/usage/dashboard/stats`（Bearer）→ 组装 metrics JSON
3. 落 balance_snapshot + 更新 providers 冗余列
4. 失败：401 → 清 token 重登一次；仍失败 → snapshot 记 error（balance NULL）、providers.last_balance_error 供 UI 展示；Turnstile/2FA 站点表现为登录 4xx，错误原样展示

### 4.5 核心 SQL

> ⚠️ 下列 SQL 中的 `cost` 表达式（`total_cost × account_rate_multiplier`）在实现中已改名为 **`official_cost`（官价对照）**，不再作为成本参与利润。真实成本来自上游 per-key `actual_cost`，存本地 SQLite，与这些 PG 聚合结果在 service 层按账号合并。见 [README.md](./README.md#口径)。

```sql
-- 收益/成本/利润：PG 按 account_id 聚合，Go 按【】前缀归并到供应商
SELECT ul.account_id, COALESCE(a.name,'') AS account_name, COUNT(*) AS requests,
       COALESCE(SUM(ul.actual_cost),0)                                          AS revenue,
       COALESCE(SUM(ul.total_cost * COALESCE(ul.account_rate_multiplier,1)),0)  AS cost
FROM usage_logs ul LEFT JOIN accounts a ON a.id = ul.account_id   -- 不滤 deleted_at：已删账号历史流量仍归属
WHERE ul.created_at >= $1 AND ul.created_at < $2
GROUP BY 1, 2;

-- 按分组（带当前倍率）
SELECT ul.group_id, COALESCE(g.name,'(无分组)'), g.rate_multiplier, COUNT(*),
       COALESCE(SUM(ul.actual_cost),0),
       COALESCE(SUM(ul.total_cost * COALESCE(ul.account_rate_multiplier,1)),0)
FROM usage_logs ul LEFT JOIN groups g ON g.id = ul.group_id
WHERE ul.created_at >= $1 AND ul.created_at < $2
GROUP BY 1, 2, 3;

-- 日趋势（$1 = 'Asia/Shanghai'）
SELECT (ul.created_at AT TIME ZONE $1)::date AS day, COUNT(*),
       COALESCE(SUM(ul.actual_cost),0),
       COALESCE(SUM(ul.total_cost * COALESCE(ul.account_rate_multiplier,1)),0)
FROM usage_logs ul WHERE ul.created_at >= $2 AND ul.created_at < $3
GROUP BY 1 ORDER BY 1;

-- 被动稳定性（近 N 小时）
SELECT ul.account_id, COALESCE(a.name,''), COALESCE(a.platform,''), COUNT(*),
       percentile_cont(0.5)  WITHIN GROUP (ORDER BY ul.duration_ms)    FILTER (WHERE ul.duration_ms    IS NOT NULL),
       percentile_cont(0.95) WITHIN GROUP (ORDER BY ul.duration_ms)    FILTER (WHERE ul.duration_ms    IS NOT NULL),
       percentile_cont(0.5)  WITHIN GROUP (ORDER BY ul.first_token_ms) FILTER (WHERE ul.first_token_ms IS NOT NULL),
       percentile_cont(0.95) WITHIN GROUP (ORDER BY ul.first_token_ms) FILTER (WHERE ul.first_token_ms IS NOT NULL)
FROM usage_logs ul LEFT JOIN accounts a ON a.id = ul.account_id
WHERE ul.created_at >= $1 GROUP BY 1, 2, 3;

-- 倍率轮询（Go diff，epsilon 1e-9）
SELECT id, name, rate_multiplier FROM groups   WHERE deleted_at IS NULL;
SELECT id, name, rate_multiplier FROM accounts WHERE deleted_at IS NULL;

-- 探测候选
SELECT id, name, platform, credentials->>'api_key' AS api_key,
       COALESCE(extra->>'base_url','') AS base_url
FROM accounts WHERE deleted_at IS NULL AND type = 'api_key' AND status = 'active';
```

规约：所有 usage_logs 查询必带 created_at 范围谓词（索引/分区裁剪）。`user_group_rate_multipliers` 无需触碰——`actual_cost` 已是含用户覆盖倍率的实扣快照。

### 4.6 调度器（scheduler.go，单 cron 实例 + SkipIfStillRunning + Recover）

| Job | 默认间隔 | 逻辑 | 并发/超时 |
|---|---|---|---|
| balance | 30m | balance_type='sub2api' 的 providers 逐个采集 | 信号量 3，单个 15s |
| probe | 15m | probe_enabled 供应商的 api_key 账号流式探测 | 信号量 2，单个 30s |
| rate | 5m | 倍率 diff | 整体 10s |
| cleanup | 每日 03:30 | 按 retention_days 清 probe_results/balance_snapshots/rate_change_history | - |

启动时序：SQLite migrate → PG Ping（失败仅告警不退出，相关接口返回 503）→ 同步跑一次 rate（建基线）+ balance（UI 立即有数）→ probe 不自动跑 → cron.Start()。退出：cron.Stop() → srv.Shutdown。手动触发接口直接复用 service 方法。

探测请求构造（按 platform，缺 base_url 回退官方端点）：
- anthropic：POST `/v1/messages`，`x-api-key` + `anthropic-version: 2023-06-01`，`{model, max_tokens:16, stream:true, messages:[{role:"user",content:"ping"}]}`
- openai：POST `/v1/chat/completions`，`Authorization: Bearer`，`{model, stream:true, max_tokens:16, messages:[...]}`
- gemini：POST `/v1beta/models/{model}:streamGenerateContent?alt=sse`，`x-goog-api-key`
- 其余 platform（oauth/cookie）：跳过，UI 标注「不支持探测」

## 5. config.yaml

```yaml
server:
  host: 0.0.0.0
  port: 9090

timezone: Asia/Shanghai        # 须与 sub2api 的 timezone 一致（对账口径）

auth:
  username: admin
  password: "change-me"        # 明文；填 $2a$/$2b$ 开头则按 bcrypt 校验
  jwt_secret: ""               # 必填，32 字节以上随机串
  token_ttl_hours: 24

sub2api_db:                    # 线上库（只读；DSN 强制 default_transaction_read_only=on）
  host: "<线上 PG 地址>"
  port: 5432
  user: "<只读账号>"
  password: "<见实际 config.yaml>"
  dbname: sub2api
  sslmode: disable

sqlite:
  path: data/monitor.db

balance:
  interval_minutes: 30
  timeout_seconds: 15
  concurrency: 3
  retention_days: 90

probe:
  interval_minutes: 15
  timeout_seconds: 30
  concurrency: 2
  retention_days: 30
  default_models:
    anthropic: claude-3-5-haiku-20241022
    openai: gpt-4o-mini
    gemini: gemini-2.5-flash

rates:
  interval_minutes: 5
  retention_days: 365

log:
  level: info
```

环境变量覆盖：viper `MONITOR_` 前缀（如 `MONITOR_SUB2API_DB_PASSWORD`）。

## 6. 前端设计

### 6.1 从 sub2api frontend 复制清单（[C]=原样 [C-]=裁剪）

- 脚手架：`vite.config.ts`[C-]（端口 3001、代理 → 9090、outDir=../backend/internal/web/dist、删 injectPublicSettings）、`tailwind.config.js`[C]、`postcss.config.js`[C]、`tsconfig*.json`[C]、`index.html`[C-]
- 样式：`src/style.css`[C]⭐（766 行设计系统原样）
- 布局：`components/layout/{AppLayout,AppHeader,AppSidebar,AuthLayout,TablePageLayout}.vue`[C-]（Sidebar 裁成 4 项菜单：仪表盘/供应商/收益统计/稳定性）
- 通用组件[C]：DataTable、Pagination、BaseDialog、ConfirmDialog、Toast、Select、Input、TextArea、SearchInput、Toggle、StatCard、StatusBadge、EmptyState、LoadingSpinner、Skeleton、DateRangePicker、HelpTooltip、PlatformIcon、AutoRefreshButton + `icons/Icon.vue`
- API/状态：`api/client.ts`[C-]（删 refresh 队列，401 → 清 token 跳 /login）、`stores/app.ts`[C-]（theme/sidebar/toast）、`stores/auth.ts`[N ~80 行]
- composables[C]：useTableLoader、useAutoRefresh、usePersistedPageSize
- i18n：`i18n/index.ts`[C-]（固定 zh）+ `locales/zh.ts`[N]（从原 zh.ts 摘 common/auth 段起步）
- `views/auth/LoginView.vue`[C-]（裁掉 OAuth/注册/Turnstile）、`NotFoundView.vue`[C]

依赖：`vue vue-router pinia axios chart.js vue-chartjs vue-i18n @vueuse/core` + `vite @vitejs/plugin-vue typescript vue-tsc tailwindcss autoprefixer postcss`。

### 6.2 页面（按原型实现）

- **DashboardView**：4×StatCard（今日收益/成本/利润/请求）→ 供应商余额卡片区（低余额红标/登录失败黄标/更新时间）→ TrendChart（收益成本利润 7/30 天）+ 按供应商收益构成条
- **ProvidersView**（范式=AccountsView）：搜索 + 快捷录入 + 新增；表格列：供应商(含站点)/余额(供应商侧)/今日消费(供应商侧)/子账号数/今日成本(本站口径)/余额刷新方式/探测/状态/操作（刷新/历史/编辑/删除）
  - ProviderFormDialog：名称/备注/余额获取方式（sub2api 登录｜手动｜不监控）/站点地址+邮箱+密码/**测试连接**/低余额阈值/探测开关
  - ProviderScanDialog：扫描前缀勾选导入（已存在的置灰）
  - ProviderAccountsDialog：子账号表（平台/类型/成本倍率/分组/今日成本/状态）
  - BalanceHistoryDialog：快照表+折线
- **StatsView**：Tab（按供应商/按分组）+ 日期快捷选择；表格（请求/Tokens/收益/成本/利润/利润率，负利润红标，合计行）+ 子账号明细弹窗；下方倍率变化历史表（类型筛选，旧→新 带涨跌色）
- **StabilityView**：上半主动探测汇总（成功率/平均首字/平均总耗时/最近状态/立即探测/趋势弹窗 LatencyChart）+ 自动刷新；下半被动真实流量表（请求数/首字 P50/P95/总耗时 P50/P95），文案注明「被动口径无成功率」

## 7. 实施步骤（每批独立验证）

1. **后端骨架**：go.mod、config、response、jwtutil、auth 全链、router、/health、main → `go build ./... && go vet ./...`；curl login/me
2. **PG 只读接入**：pg*.go、stats_service、dashboard/stats handler → 连线上库跑聚合对账
3. **SQLite+供应商**：迁移、provider CRUD/scan/import/accounts；ParseProviderName 单测（`【walk】gpt pro`→walk、无前缀→!ok、`【a】【b】x`→a）
4. **供应商采集**：sub2api_client（login/dashboard stats）、test-connection、balance job、rate job、cleanup
5. **主动探测**：三个 platform builder、手动触发、probe job
6. **前端骨架**：初始化+复制裁剪+登录页+空四页 → dev 3001 验证登录/暗色/导航
7. **四页面**：Dashboard → Providers → Stats → Stability（对接 2-5 的接口）
8. **embed 联调**：pnpm build → `go build -tags embed` → 单二进制 :9090 全流程回归；Makefile 收口
9. **文档**：README（部署/config 说明/curl 手册/只读账号建议）

## 8. 验证方案

- 静态：`go build ./...`、`go vet ./...`、`go test ./...`（ParseProviderName/jwt/migration/rate diff）；`vue-tsc --noEmit`、`vite build`
- 线上库对账：项目连接跑今日聚合 SQL，与 sub2api 后台数字比对；验证连接为只读（尝试写须失败）
- 供应商采集：对至少一家真实供应商站点跑 test-connection / refresh（须用户提供或自测）
- curl 全链路：login → providers CRUD → scan/import → balance refresh → stats → stability
- 调度：dev 配 interval=1m 观察 10 分钟（无重叠、行数增长、无 SQLITE_BUSY）

## 9. 风险与规避

| 风险 | 规避 |
|---|---|
| 供应商站点凭据需可逆存储 | 存本地 SQLite（仅本机）；API 永不回显密码/token；日志 redact；README 提示文件权限 |
| Turnstile / 2FA 站点无法自动登录 | 登录 4xx 错误可视化展示，可转 manual 手动记录 |
| 供应商登录限流 20/min | 30m 采集间隔 + 401 仅重登一次，远低于阈值 |
| 上游 key 明文二次暴露 | 所有接口/日志/probe error 全部 redact，key 永不出后端 |
| 线上 PG 误写 | 会话级 `default_transaction_read_only=on` + README 提供只读账号 GRANT |
| 时区口径不一致 | monitor 与 sub2api timezone 必须一致（默认均 Asia/Shanghai），README 显著提示 |
| 探测烧钱/触发风控 | 默认关闭、max_tokens=16、便宜模型、并发 2、15m 间隔 |
| usage_logs 仅记成功请求 | 被动口径只有量+延迟分位（UI 文案写明）；成功率仅来自主动探测 |
| usage_logs 可能分区 | 对父表 SELECT 透明；必带 created_at 谓词即可裁剪 |
| 倍率轮询窗口内多次变化 | 5m 粒度合并中间态只记 old→new（可接受，YAGNI 不做 CDC） |

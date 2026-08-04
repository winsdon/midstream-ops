<div align="center">

# midstream-ops

[![Go](https://img.shields.io/badge/Go-1.25-00ADD8.svg)](https://golang.org/)
[![Vue](https://img.shields.io/badge/Vue-3.4+-4FC08D.svg)](https://vuejs.org/)
[![SQLite](https://img.shields.io/badge/SQLite-WAL-003B57.svg)](https://www.sqlite.org/)
[![Docker](https://img.shields.io/badge/Docker-Ready-2496ED.svg)](https://www.docker.com/)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

**AI API 中转站运营监控端 — 余额 / 成本 / 收益核算 / 稳定性盯盘**

</div>

## ⚠️ 重要提醒

- **📖 用途声明**：本项目是 [sub2api](https://github.com/Wei-Shaw/sub2api) 的**第三方独立监控端**，与 sub2api 官方无隶属关系，不修改也不代理其业务流量。
- **🔒 只读接入**：线上 Postgres 以只读账号 + DSN 强制 `default_transaction_read_only=on` 双保险接入，不写业务库。唯一的写操作是通过 admin API 调整本站分组倍率（调价映射功能，可不启用）。
- **🔑 凭据风险**：本系统存储上游供应商站点的登录凭据与客户 KYC 实名资料。**生产部署必须配置 `MONITOR_CREDENTIALS_KEY`**，否则密码与身份证号明文落库。
- **⚖️ 免责声明**：本项目仅供技术学习与内部运营使用，作者不对因使用本项目导致的账户封禁、数据丢失或其他任何直接或间接损失承担责任。

## 项目概述

midstream-ops（代码内部标识 `sub2api-account-monitor`）是面向 AI API 中转站运营方的**独立监控端**：只读接入线上 sub2api 的 Postgres 拉取收益数据，模拟登录各上游供应商站点采集余额与实扣成本，两边对账算出真实利润，同时盯住上游链路的延迟与可用性。

它解决的是运营方最基本却最难回答的三个问题：

| 问题 | 本项目的回答 |
|------|------|
| **我到底赚不赚钱？** | 收益取本站实扣，成本取**上游 per-key 真实实扣**（不是推算），逐供应商 / 逐分组算利润 |
| **上游还剩多少钱？** | 多平台多认证模式采集余额，阶梯退避防撞 WAF，低余额分级告警 |
| **哪条链路在劣化？** | 真实流量被动统计 + 主动探测双口径，首字延迟为主视觉，六状态健康机 |

单二进制交付（前端 embed），本地 SQLite 存监控数据，不额外依赖数据库容器。

## 核心功能

- **仪表盘** — 今日收益 / 实扣成本 / 利润 / 请求数总览，近 N 天趋势图，成本同步状态与完整性告警
- **供应商管理** — 显式关联账号；多平台（sub2api / new-api）多认证模式；余额采集与历史曲线；上游 per-key 成本明细（实扣 / 官价 / 折扣节省）；自营站标记与运营成本记账
- **收益统计** — 按供应商 / 按分组两维度聚合收益·成本·利润·请求数，可展开子账号明细，支持自定义日期范围
- **分组倍率** — 变更驱动快照追踪上游与本站分组倍率，展示涨跌幅与生效时长，含变更时间线
- **调价映射** — 上游倍率 → 本站倍率联动（`目标 = 上游 × 系数 + 偏移`），支持自动调价、人工修改冲突检测、审计留痕
- **稳定性盯盘** — 被动统计（真实流量分位数）+ 主动探测（TTFT / 成功率）双口径，实时窗口下探到 5 分钟，六状态健康机
- **授信台账** — 客户垫付应收的人工台账，只追加分录、记错走冲正，敞口分级告警；KYC 实名资料加密落库，支持客户自助填报 + 审核流
- **系统设置** — per-provider 错峰调度（热更新免重启）、余额与倍率预警、钉钉 / 飞书 / Telegram 通知渠道

<details>
<summary><b>展开各模块完整能力清单</b></summary>

| 模块 | 说明 |
|------|------|
| **仪表盘** | 今日收益 / 实扣成本 / 利润 / 请求数 / 供应商数 / 账号数，近 N 天收益·成本·利润趋势图；成本同步状态与完整性告警条 |
| **供应商** | **显式关联账号**（按站点地址归组多选，支持按账号名 / 平台 / 站点地址 / 归属站点 / ID 搜索与「全选本组」；或按账号名【】前缀批量建站顺带关联）；CRUD；多平台（sub2api / new-api）多认证模式（账号密码 / Access+Refresh Token / 系统令牌+用户 ID）；按平台 / 状态 / 余额类型三维筛选，按今日消费·余额·名称排序（默认今日消费降序，**「不监控」站点恒排最后**）；采集健康点（绿/黄/红）与登录冷却提示；站点级忽略余额告警开关；手动录入余额；余额历史曲线；可用分组按平台（Anthropic / OpenAI / Gemini …）分节展示；💰 上游成本明细（per-key 实扣 / 官价 / 折扣节省，手动同步与回补）；**自营站标记**与运营成本记账（买号 / 订阅 / 服务器） |
| **收益统计** | 按供应商 / 按分组两个维度聚合收益·实扣成本·利润·请求数，均可展开子账号明细（未匹配上游 key 的账号显式标记）；Top 15 三系列并排柱状图；支持日期范围（今日 / 7 天 / 30 天 / 自定义） |
| **分组倍率** | 变更驱动快照追踪上游站点与本站的分组倍率：当前倍率 / 上次倍率 / 涨跌幅（持续展示到下一次变化）/ 生效时长；每实体变更历史时间线 |
| **调价映射** | 上游分组倍率 → 本站分组倍率联动：`目标 = 上游 × 系数 + 偏移`（夹紧上下限）；手动一键应用或自动调价；人工修改冲突检测（检测到即停止覆盖，须人工确认）；调价审计留痕 |
| **上游成本同步** | 随供应商统一 sync 拉取各供应商 per-key `actual_cost` 落本地库，首次回补 90 天历史；只存 key 的 sha256 指纹 |
| **稳定性** | 默认被动统计（真实流量 duration / first_token 分位数）+ 主动探测（对上游 key 发最小流式请求测 TTFT / 总耗时 / 成功率）；按归属供应商与健康状态两维度筛选；实时窗口档位（5 分钟 / 30 分钟 / 1 小时 / 5 小时 / 24 小时）；**首字延迟为主视觉指标**（分档着色，P50/P95 合一格）+ 行首综合评级色点；六状态健康状态机（正常/降权/熔断/观察/恢复/停用）+ 状态迁移时间线；失败退避与每日探测预算。**详见 [稳定性可视化](docs/DESIGN_NOTES.md#稳定性可视化)** |
| **授信台账** | 客户授信额度与垫付应收账的**人工台账**：建档时从 sub2api 用户列表直接选人（已建档的禁选）；垫付 / 回款只追加分录，记错走冲正；敞口 = Σ垫付 − Σ回款，每次写入在同一事务内全量重算；80% / 100% 分级告警（边沿触发，闩锁落库）。KYC 实名资料加密落库，支持管理端代录与客户自助填报 + 审核流。**详见 [授信台账](docs/DESIGN_NOTES.md#授信台账人工记账)** |
| **系统设置** | 数据刷新频率（per-provider 错峰调度，热更新免重启）；余额预警（充值倍率折 CNY，1h 冷却，可按站点单独静音）；倍率变更预警；钉钉 / 飞书 / Telegram 机器人通知渠道（支持加签与测试发送） |

</details>

> **📐 每个数字背后的口径、取舍与实现约束，见 [docs/DESIGN_NOTES.md](docs/DESIGN_NOTES.md)** —— 成本为什么取上游实扣而非本站推算、分组利润合计为什么偏高、稳定性色点为什么取最差而非平均，都在那里。

## 技术栈

| 组件 | 技术 |
|------|------|
| 后端 | Go 1.25, gin, viper, robfig/cron, pgx/v5 |
| 前端 | Vue 3.4+, Vite 5, Pinia, Tailwind, chart.js, vue-i18n |
| 本地存储 | SQLite（modernc.org/sqlite，WAL，内嵌 migration） |
| 数据源 | sub2api 线上 PostgreSQL（**只读**） |

---

## 部署方式

### 方式一：Docker（推荐用于线上）

镜像已内嵌前端与时区库，单容器即可运行 —— 本项目自身只用 SQLite，Postgres 是**只读连接已有的 sub2api 库**，不需要额外起数据库容器。

```bash
docker pull winsdon8/midstream-ops:latest
```

部署文件在 `deploy/`：

```bash
cd deploy
cp .env.example .env      # 按注释填写，至少 3 个密钥 + DB 只读账号
docker compose up -d
curl http://127.0.0.1:9090/health
```

**三个部署前必须注意的点**（详见 [deploy/DOCKER.md](deploy/DOCKER.md)）：

1. **网络名要先查再填**。容器靠接入 sub2api 已有的 docker 网络来访问它的 Postgres，而 compose 会给网络名加项目名前缀。先跑 `docker network ls | grep sub2api` 拿到真实名字填进 `.env` 的 `SUB2API_NETWORK`。
2. **`MONITOR_CREDENTIALS_KEY` 首次部署就要配**，否则供应商密码与 KYC 的 PII 明文落库；且换密钥会让旧密文永久无法解密。
3. **数据卷必须挂目录**（`monitor_data:/app/data`）而非单个 `.db` 文件 —— SQLite 开了 WAL，需要 `-wal` / `-shm` 与主库同目录。

配置全部走 `MONITOR_` 前缀的环境变量，命名规则是配置键的 `.` 换 `_` 再大写（`sub2api_db.host` → `MONITOR_SUB2API_DB_HOST`），无需挂载 `config.yaml`。

端口默认只绑 `127.0.0.1`。本站含成本、利润、客户授信与 KYC 数据，对外访问请在宿主机用 Nginx / Caddy 反代并配置 TLS 与访问控制，**不要直接裸露公网**。

#### 自行构建镜像

```bash
docker build -t midstream-ops:dev .
```

或用封装脚本（自动识别本机的 docker / podman）：

```powershell
.\deploy\build.ps1                                    # 本地 dev 镜像
.\deploy\build.ps1 -Version 0.1.0 -User <用户名> -Push  # 构建并推送
```

---

### 方式二：源码编译（单二进制）

需要 Go 1.25+、Node 20+、pnpm 10+。日常开发请直接看下方「[本地开发](#本地开发免编译不产出-exe)」，无需编译。

**1. 配置**

```bash
cp backend/config.example.yaml backend/config.yaml
```

编辑 `backend/config.yaml`：

| 配置项 | 说明 |
|------|------|
| `auth.username` / `auth.password` | 监控端登录账密 |
| `auth.jwt_secret` | **必填**，≥32 字节随机串 |
| `sub2api_db.*` | 线上 sub2api Postgres（只读账号即可，DSN 会强制 `default_transaction_read_only=on`） |
| `cost.interval_minutes` | 上游成本同步间隔，默认 10 分钟 |
| `cost.retention_days` | 成本明细保留天数，默认 180 |
| `timezone` | 与 sub2api 主站一致（影响「今日」口径） |

> 供应商站点的登录邮箱与密码不在配置文件里，而是在「供应商」页面录入后存本地 SQLite（接口只回显 `has_password` 布尔）。

> ⚠️ `config.yaml` 含真实凭据，已在 `.gitignore` 中，**勿提交**。

**2. 编译**

```bash
cd frontend && pnpm build && cd ../backend && go build -tags embed -o server.exe ./cmd/server
```

产物：`backend/server.exe`（内嵌前端）。等价的 make 封装为 `make build-embed`（需已安装 GNU make）。

> **注意：** `-tags embed` 会把前端嵌入二进制。不带此参数编译的程序不含前端界面。

**3. 运行**

```bash
cd backend && ./server.exe
```

访问 `http://localhost:9090`，用 `config.yaml` 中的账密登录。

---

## 本地开发（免编译，不产出 exe）

两种方式，按场景选：

| | 方式 A：双终端 | 方式 B：单进程 |
|---|---|---|
| **适用** | 日常开发、调前端 | 验证 embed 部署形态 |
| **前端热更新** | ✅ HMR 毫秒生效 | ❌ 须重新 `pnpm build` + 重启 |
| **访问** | `:3001` | `:9090` |
| **进程** | 2 个终端 | 1 条命令 |

首次需安装依赖：`cd backend && go mod tidy` + `cd frontend && pnpm install`。

### 方式 A：双终端（推荐，日常用这个）

终端 1 — 后端（`go run` 直接跑源码，无 embed）：

```bash
cd backend && go run ./cmd/server
```

终端 2 — 前端（vite dev server，代理 `/api` → :9090）：

```bash
cd frontend && pnpm dev
```

访问 `http://localhost:3001`。改前端 HMR 自动生效；改后端 `Ctrl+C` 后重跑 `go run`（构建缓存命中约 2-5 秒）。

### 方式 B：单进程（后端托管前端，一条命令）

```bash
cd frontend && pnpm build && cd ../backend && go run -tags embed ./cmd/server
```

访问 `http://localhost:9090`。同样不产出 `server.exe`。

- ⚠️ **没有前端热更新**：每改一个 Vue 文件都要重跑整条命令（`pnpm build` 实测约 4 秒 + 后端重启约 5 秒）。调前端请用方式 A。
- `dist/` 已被 `backend/.gitignore` 忽略，**首次必须先 `pnpm build`**，否则 `//go:embed all:dist` 会因目录不存在而编译失败。
- 价值在于能覆盖方式 A 测不到的部署态问题：CSP、SPA 路由回退、静态资源路径。

### 排障

> 端口可通过 `frontend/.env` 的 `VITE_DEV_PORT` / `VITE_DEV_PROXY_TARGET` 覆盖。
>
> 若后端报 `bind: Only one usage of each socket address`，说明 :9090 已被占用——通常是之前构建的 `backend/server.exe` 仍在后台常驻。查杀：
> ```powershell
> Get-NetTCPConnection -LocalPort 9090 -State Listen | Select-Object OwningProcess
> Stop-Process -Id <PID>
> ```

> Makefile 里的 `dev-backend` / `dev-frontend` / `tidy` 是上述命令的等价封装，**需本机已安装 GNU make**（Windows 默认没有）。没装 make 就直接用裸命令。

### 测试

```bash
# 后端（用 -race 而非 -cover：本机 Go 工具链缺 covdata 时 -cover 会假报错）
cd backend && go vet ./... && go test -race ./...

# 前端纯函数单测（vitest，复用 vite.config.ts 的 @ alias）
cd frontend && pnpm test && pnpm typecheck
```

前端只测纯函数（`src/utils/*.spec.ts`）—— 排序与筛选、延迟分档、行级评级、关联搜索这类有边界条件的逻辑都刻意抽成了不依赖 Vue 的纯函数，组件层留给端到端手测。

---

## 项目结构

```
midstream-ops/
├── backend/                Go 1.25 + gin + viper + robfig/cron + pgx/v5 + modernc.org/sqlite
│   ├── cmd/server/         入口（装配 + 优雅退出）
│   └── internal/
│       ├── config/         配置加载校验（PG DSN 强制只读）
│       ├── handler/        HTTP 处理器
│       ├── service/        业务逻辑（余额采集 / 探测 / 倍率 / 统计 / 调度 / 授信台账）
│       ├── repository/     PG 只读查询 + SQLite 存储（WAL，内嵌 migration）
│       ├── pkg/            response envelope / jwt / secretbox（凭据与 PII 加密）
│       ├── server/         路由 + 中间件（管理员 JWT / 嵌入会话 / CSP frame-ancestors）
│       └── web/            前端 embed（-tags embed）
│
├── frontend/               Vue3 + Vite5 + Pinia + vue-router4 + Tailwind + chart.js + vue-i18n
│
├── deploy/                 部署文件
│   ├── docker-compose.yml  容器编排
│   ├── .env.example        环境变量模板
│   ├── build.ps1           镜像构建脚本
│   └── DOCKER.md           Docker 部署说明
│
└── docs/
    └── DESIGN_NOTES.md     口径与设计说明
```

## API 概览

所有管理端接口需 `Authorization: Bearer <token>`（`POST /api/v1/auth/login` 获取）。统一 envelope `{code, message, data}`，分页为 `PaginatedData{items,total,page,page_size,pages}`。`/api/v1/embed/*` 是例外，走独立的嵌入会话体系（见文末）。

<details>
<summary><b>展开完整接口列表</b></summary>

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/v1/auth/login` | 登录 |
| GET | `/api/v1/auth/me` | 当前用户 |
| GET | `/api/v1/dashboard/summary` | 今日汇总 |
| GET | `/api/v1/dashboard/trend?days=` | 趋势 |
| GET/POST | `/api/v1/providers` | 供应商列表 / 新建 |
| PUT/DELETE | `/api/v1/providers/:id` | 更新 / 删除 |
| POST | `/api/v1/providers/test-connection` | 测试站点登录 |
| GET | `/api/v1/providers/scan` | 按账号名【】前缀给出建站建议（含该前缀下的账号 id） |
| GET | `/api/v1/providers/scan-urls` | 按账号 base_url 归组（不依赖命名习惯） |
| POST | `/api/v1/providers/import` | 批量建站并顺带写入账号关联 |
| GET/PUT | `/api/v1/providers/:id/links` | 该站关联的账号 / 全量替换关联集合 |
| GET | `/api/v1/providers/:id/accounts` | 子账号聚合 |
| POST | `/api/v1/providers/:id/balance/refresh` | 刷新余额 |
| PUT | `/api/v1/providers/:id/balance` | 手动录入余额 |
| GET | `/api/v1/providers/:id/balance/history` | 余额历史 |
| GET | `/api/v1/providers/:id/costs?start=&end=` | 上游 per-key 成本明细（实扣 / 官价 / 匹配状态） |
| POST | `/api/v1/providers/:id/costs/sync` | 手动同步上游成本（`?backfill=true` 回补 90 天历史） |
| GET | `/api/v1/providers/:id/operating-costs?start=&end=` | 自营站运营成本明细与合计（区间缺省为本月至今） |
| POST | `/api/v1/providers/:id/operating-costs` | 记一笔运营成本（**仅自营站**，否则 400） |
| DELETE | `/api/v1/operating-costs/:id` | 删除一笔运营成本 |
| GET | `/api/v1/stats/providers?start=&end=` | 按供应商统计（含子账号明细、成本完整性标记、同步状态） |
| GET | `/api/v1/stats/groups?start=&end=` | 按分组统计（结构与供应商维度同构；成本为按用量占比分摊值） |
| GET | `/api/v1/rates/history?scope=&provider_id=&changes_only=` | 倍率变化历史（变更驱动快照，LAG 推导涨跌） |
| GET | `/api/v1/rates/current?scope=local\|upstream` | 当前生效倍率列表（含上次倍率与生效时长） |
| GET/PUT | `/api/v1/pricing/self` | 本站连接（调价用 admin 凭据，保存即验证） |
| GET | `/api/v1/pricing/local-groups` | 本站分组下拉选项 |
| GET | `/api/v1/pricing/mappings/preview` | 映射预览（上游倍率 → 目标倍率 → 是否待应用） |
| POST/PUT/DELETE | `/api/v1/pricing/mappings[/:id]` | 映射 CRUD |
| POST | `/api/v1/pricing/mappings/:id/apply` | 手动应用（GET+PUT-merge + 人工冲突检测） |
| POST | `/api/v1/pricing/mappings/:id/resolve-conflict` | 确认冲突，恢复自动调价资格 |
| GET | `/api/v1/pricing/mappings/:id/actions` | 调价审计历史 |
| GET | `/api/v1/stability/passive?minutes=` | 被动稳定性（含 `provider_id` / `provider_name` 归属） |
| GET | `/api/v1/stability/probes` | 探测记录（分页） |
| GET | `/api/v1/stability/probes/summary?minutes=` | 探测汇总（含归属；`last_success` 限窗口内） |
| GET | `/api/v1/stability/probes/trend?account_id=&minutes=` | 单账号探测趋势 |
| POST | `/api/v1/stability/probe/run` | 手动探测（`{account_id}` 同步 / `{provider_id}` 异步） |
| GET | `/api/v1/stability/health` | 账号健康状态（六状态 + 当日探测预算） |
| GET | `/api/v1/stability/health/events?account_id=` | 状态迁移时间线 |
| PUT | `/api/v1/stability/health/:id/disabled` | 人工启停账号探测 |
| GET/PUT | `/api/v1/settings/strategy` | 自动化与策略（刷新频率 / 预警开关，热更新） |
| GET/PUT | `/api/v1/settings/notify` | 通知渠道（钉钉 / 飞书 / Telegram，凭据脱敏回显） |
| POST | `/api/v1/settings/notify/test` | 渠道测试发送 |
| GET | `/api/v1/credit/summary` | 授信总览（客户数 / 授信总额 / 敞口合计 / 超额与预警客户数） |
| POST | `/api/v1/credit/recalc` | 全量重算敞口与告警闩锁（幂等兜底） |
| GET | `/api/v1/credit/sub2api-users` | 建档下拉数据源（读线上 users 表，标记已建档；PG 不可用时 503，前端降级为手填） |
| GET/POST | `/api/v1/credit/customers` | 客户列表（支持 `sort`/`order`，白名单列） / 新建（重复建档返回 409） |
| GET/PUT/DELETE | `/api/v1/credit/customers/:id` | 详情 / 更新 / 归档（软删，台账保留） |
| POST | `/api/v1/credit/customers/:id/recalc` | 单客户重算 |
| GET/POST | `/api/v1/credit/customers/:id/ledger` | 台账分录列表 / 记一笔（垫付或回款） |
| POST | `/api/v1/credit/ledger/:id/reverse` | 冲正（写等额反向分录，原分录保留） |
| GET/PUT | `/api/v1/credit/customers/:id/kyc` | KYC 资料读取 / 保存（PII 加密落库） |
| POST | `/api/v1/credit/customers/:id/kyc/review` | 审核（通过 / 驳回，驳回须填意见） |

嵌入页接口（sub2api iframe，**独立于管理员 JWT**，身份来自 sub2api 透传 token 换取的短期会话）：

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/v1/embed/plaza/session` | 换会话（免鉴权） |
| GET | `/api/v1/embed/plaza/models` | 模型广场数据 |
| POST | `/api/v1/embed/kyc/session` | 换会话（免鉴权） |
| GET/PUT | `/api/v1/embed/kyc/profile` | 客户自助读取 / 提交 KYC（身份取自会话，请求体无 `user_id`） |

</details>

## 安全说明

- 线上 PG 只读双保险：只读账号 + DSN 强制 `default_transaction_read_only=on`
- **写自己站点只走 admin API**（调价映射）：GET + PUT 完整合并回写，绝不写 DB、绝不部分覆盖；检测到人工修改立即停止自动覆盖
- 上游 key 永不出后端：探测请求的 key 仅用于单次请求，接口 / 日志 / 错误信息全部脱敏
- 供应商登录密码、token 不在接口回显（仅 `has_password` 等布尔）；配置 `MONITOR_CREDENTIALS_KEY` 后 AES-256-GCM 加密落库
- 供应商站点前置 WAF 拦截非浏览器 UA：后端已内置浏览器 User-Agent 与连接重试；登录被拒自动进入阶梯冷却（30min → 2h → 6h），防止反复撞接口被拉黑 IP
- 通知渠道加签密钥 / Bot Token 脱敏回显（仅 `has_secret` 布尔），保存时留空表示不修改
- **授信模块永不写上游**：充值由人在 sub2api 后台手动执行，系统只记本地台账。sub2api 的会话绑定（JWT `bnd` = sha256(IP+UA)）会让服务端直连必然失败，并撤销该用户整个会话家族
- KYC 的 PII 加密落库，客户身份只取自会话上下文（请求体无 `user_id`）；嵌入端纯 Bearer 头鉴权、不用 Cookie；日志永不打 token 明文 / 证件号 / 联系方式

## 相关文档

| 文档 | 内容 |
|------|------|
| [docs/DESIGN_NOTES.md](docs/DESIGN_NOTES.md) | 口径与设计说明 —— 收益成本利润口径、稳定性可视化取舍、授信台账约束 |
| [deploy/DOCKER.md](deploy/DOCKER.md) | Docker 部署详解（网络、只读账号、密钥、持久化） |
| [PLAN.md](PLAN.md) | 实施计划与架构决策记录 |

---

## 许可证

本项目基于 [MIT 许可证](LICENSE) 授权。

Copyright (c) 2026 winsdon

---

<div align="center">

**如果觉得有用，请给个 Star 支持一下！**

</div>

# 从 0.1.x（SQLite）升级到 PostgreSQL

`0.1.2` 及更早版本把自身数据存在容器内的 SQLite 单文件（`monitor_data` 卷）里。
本版改为外部 PostgreSQL 的 monitor 库，容器无状态。

**不能直接 `docker compose pull`** —— 新镜像启动时会连 `MONITOR_STORE_DB_*`，
找不到旧卷里的数据，看起来像「数据丢了」。按下面步骤一次性搬完再切。

回滚坐标：`winsdon8/midstream-ops:0.1.2` + 旧 compose，只要没删
`monitor_data` 卷，SQLite 数据原封不动。

---

## 0. 准备

需要：

- 能访问旧容器 / 旧 volume 的环境
- 一台可连目标 PostgreSQL 的机器（装 Python 3.10+、`psycopg[binary]`）
- 约等于旧库体积的磁盘空闲（通常几十 MB）

```bash
pip install 'psycopg[binary]'
```

## 1. 停服务并备份 SQLite

**必须先停**，否则 WAL 里未 checkpoint 的写入会丢。

```bash
cd /opt/midstream-ops   # 或你的部署目录
docker compose stop monitor
```

从 volume 导出一致快照（推荐 `VACUUM INTO`，单文件、无 `-wal`/`-shm`）：

```bash
# 查 volume 挂载点
VOL=$(docker volume inspect midstream-ops_monitor_data --format '{{.Mountpoint}}')
ls -la "$VOL"

# 用临时容器做一致性导出
docker run --rm \
  -v midstream-ops_monitor_data:/data \
  -v /tmp:/out \
  alpine sh -c '
    apk add --no-cache sqlite >/dev/null
    rm -f /out/monitor.db
    sqlite3 /data/monitor.db "VACUUM INTO \"/out/monitor.db\""
    ls -la /out/monitor.db
  '
```

把 `/tmp/monitor.db` 拷到跑迁移脚本的机器上，并再拷一份异地备份。

## 2. 建 monitor 库与账号

在目标 PostgreSQL 上执行一次：

```sql
CREATE DATABASE monitor;
CREATE USER monitor WITH PASSWORD '<强密码>';
GRANT ALL PRIVILEGES ON DATABASE monitor TO monitor;
\c monitor
GRANT ALL ON SCHEMA public TO monitor;
```

⚠️ 最后一行不能省：PostgreSQL 15+ 的 public schema 默认不再对 PUBLIC 开放
CREATE 权限，漏了会在首次建表时报 `permission denied for schema public`。

与 sub2api 可以是同一个 PG 实例上的不同 database，也可以是独立实例。

## 3. 先让新版进程建好表结构

迁移脚本往**已存在的表**里 COPY 数据，不会建表。所以要先起一次新版（或用
同等配置的二进制）让它跑完 `migrations_pg`，再停掉。

方式 A —— 临时起容器（推荐，与线上一致）：

```bash
# 新版 .env 至少填齐 MONITOR_STORE_DB_* / 密钥 / 只读账号
# 此时还不要指望业务可用，只为建表
docker compose up -d
curl -s localhost:9090/health
# 期望 store_pg:"up"
docker compose stop monitor
```

方式 B —— 本地二进制：

```bash
# config.yaml 配好 store_db.* 后
cd backend && go run ./cmd/server
# 看到「本地 PG 就绪」后 Ctrl+C
```

## 4. 空跑迁移（只报告，不写入）

在仓库根目录：

```bash
python deploy/migrate-sqlite/migrate.py \
  --src /path/to/monitor.db \
  --dsn 'postgresql://monitor:<密码>@<host>:5432/monitor'
```

确认：

- 各表行数符合预期
- 没有「无法解析为时间 / 日期」类问题

有问题先修源库或联系维护者，**不要**加 `--execute`。

## 5. 正式写入

```bash
python deploy/migrate-sqlite/migrate.py \
  --src /path/to/monitor.db \
  --dsn 'postgresql://monitor:<密码>@<host>:5432/monitor' \
  --execute
```

脚本会：

1. 按父表 → 子表顺序 COPY
2. 把 TEXT 时刻 / 日期 / 0·1 布尔转成 TIMESTAMPTZ / DATE / BOOLEAN
3. 保留原 `id`，并 `setval` 重置序列

加密列（密码、token、KYC PII）按密文原样搬，**不需要**也不应持有
`MONITOR_CREDENTIALS_KEY`。切到新版后必须继续用**同一个**密钥。

## 6. 校验

```bash
python deploy/migrate-sqlite/verify.py \
  --src /path/to/monitor.db \
  --dsn 'postgresql://monitor:<密码>@<host>:5432/monitor'
```

校验层（任一层失败都以非零码退出）：

1. 行数
2. id 极值（发现少搬尾部）
3. 数值求和（发现类型/精度问题，尤其 `media_tasks.cost_ticks`）
4. 时间抽样（发现全变 `0001-01-01`）

全部通过后再往下走。

## 7. 切换 compose 与配置

1. 用新版 `deploy/docker-compose.yml`（**无** `volumes` / `monitor_data`）
2. `.env` 追加（或确认已有）六项：

| 变量 | 说明 |
|------|------|
| `MONITOR_STORE_DB_HOST` | monitor 库地址 |
| `MONITOR_STORE_DB_PORT` | 默认 `5432` |
| `MONITOR_STORE_DB_DBNAME` | 默认 `monitor` |
| `MONITOR_STORE_DB_USER` | 可写账号 |
| `MONITOR_STORE_DB_PASSWORD` | 可写密码 |
| `MONITOR_STORE_DB_SSLMODE` | 同宿主可 `disable`，远程建议 `require` |

3. **不要**再设 `MONITOR_SQLITE_PATH`
4. `MONITOR_CREDENTIALS_KEY` 必须与旧部署相同

```bash
docker compose pull
docker compose up -d
curl -s localhost:9090/health
# 期望 {"status":"ok","upstream_pg":"up","store_pg":"up"}
```

登录管理端，抽查：

- 上游管理站点数 / 余额
- 收益统计有数
- 授信台账与 KYC 能打开
- 系统设置与通知渠道仍在

## 8. 清理（确认稳定后再做）

旧 `monitor_data` 卷建议保留至少一周，再：

```bash
docker volume rm midstream-ops_monitor_data   # 名字以实际为准
```

备份策略从「拷 `.db`」改为 `pg_dump`：

```bash
docker exec <pg容器> pg_dump -U monitor -d monitor \
  --format=custom --no-owner --no-privileges -f /tmp/monitor.dump
```

---

## 排障

| 现象 | 可能原因 | 处理 |
|------|----------|------|
| 启动报 `store_db.host 必填` | 未配 `MONITOR_STORE_DB_*` | 按第 7 步补全 |
| 建表 `permission denied for schema public` | 漏了 schema GRANT | 重跑第 2 步最后一行 |
| `verify.py` 行数对不上 | 空跑时未发现的表被跳过 / 正式跑前有脏数据 | 清空 monitor 库表后重跑 3→6 |
| 登录后供应商密码解不开 | 换了 `MONITOR_CREDENTIALS_KEY` | 恢复旧密钥 |
| `/health` 里 `store_pg:"down"` | 连错库 / 密码错 / 网络不通 | 用同 DSN 在容器外 `psql` 验证 |

更细的部署背景见 [DOCKER.md](./DOCKER.md)。

# midstream-ops

sub2api 上游账号的余额 / 成本 / 稳定性监控端。单容器部署，只读连接已有的
sub2api Postgres，自身数据存 SQLite。

```bash
docker pull winsdon8/midstream-ops:latest
```

- 镜像体积约 43 MB（Alpine + 静态编译单二进制，前端已 embed）
- 架构：`linux/amd64`
- 端口：`9090`
- 数据目录：`/app/data`（必须挂载，见下）

## 快速开始

```bash
mkdir sub2api-monitor && cd sub2api-monitor
# 下载 docker-compose.yml 与 .env.example（见项目仓库 deploy/ 目录）
cp .env.example .env
```

编辑 `.env`，至少填这几项：

| 变量 | 说明 | 生成方式 |
|---|---|---|
| `MONITOR_AUTH_PASSWORD` | 监控端登录密码 | 自定 |
| `MONITOR_AUTH_JWT_SECRET` | JWT 签名密钥，须 ≥32 字节 | `openssl rand -hex 32` |
| `MONITOR_CREDENTIALS_KEY` | 凭据/PII 加密密钥，base64 的 32 字节 | `openssl rand -base64 32` |
| `MONITOR_SUB2API_DB_USER` / `_PASSWORD` | sub2api 库的只读账号 | 见下方 SQL |
| `SUB2API_NETWORK` | sub2api 的 docker 网络真实名 | `docker network ls \| grep sub2api` |

然后：

```bash
docker compose up -d
curl http://127.0.0.1:9090/health
```

期望输出 `{"pg":"up","sqlite":"up","status":"ok"}`。

## 部署前必读的三件事

### 1. 网络名要先查再填

本容器通过接入 sub2api 已有的 docker 网络来访问它的 Postgres。compose 会给
网络名加项目名前缀，`sub2api-network` 实际通常叫 `sub2api_sub2api-network`
（取决于 sub2api 的部署目录名）。**先查真实名字**：

```bash
docker network ls | grep sub2api
```

把结果填进 `.env` 的 `SUB2API_NETWORK`。填错会报
`network xxx declared as external, but could not be found`。

如果 monitor 与 sub2api 不在同一宿主，改为把 `MONITOR_SUB2API_DB_HOST` 填成
远程地址、`MONITOR_SUB2API_DB_SSLMODE` 设为 `require`，并删掉 compose 里的
`networks` 段。

### 2. 建 Postgres 只读账号

程序侧的 DSN 已强制 `default_transaction_read_only=on`，再配一个只读账号构成
双保险：

```sql
CREATE USER monitor_ro WITH PASSWORD '<强密码>';
GRANT CONNECT ON DATABASE sub2api TO monitor_ro;
GRANT USAGE ON SCHEMA public TO monitor_ro;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO monitor_ro;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO monitor_ro;
```

### 3. 加密密钥要一开始就配

`MONITOR_CREDENTIALS_KEY` 未配置时，供应商站点的登录密码、KYC 的身份证号等
PII 会**明文**存进 SQLite（程序只在启动日志告警，不阻止启动）。

- 后期补配不会炸：代码按 `enc:v1:` 前缀自动兼容新旧数据
- 但已有明文不会自动加密，会形成新旧混存
- **换密钥会让旧密文永久无法解密** —— 请妥善备份

## 数据持久化

```yaml
volumes:
  - monitor_data:/app/data
```

必须挂**目录**而非单个 `.db` 文件：SQLite 开了 WAL，运行时会在同目录产生
`monitor.db-wal` 与 `monitor.db-shm`。挂单文件会导致这两个文件落在容器可写层，
容器重建即丢失未 checkpoint 的写入。

⚠️ `docker compose down -v` 会删除 volume，监控数据全丢。日常停服务请用不带
`-v` 的 `docker compose down`。

## 安全建议

端口默认只绑 `127.0.0.1`。本站含成本、利润、客户授信与 KYC 等敏感数据，
**不要直接裸露公网** —— 对外访问请在宿主机用 Nginx/Caddy 反代并配置 TLS 与
访问控制。

## 配置参考

全部配置项走 `MONITOR_` 前缀的环境变量，命名规则是配置键的 `.` 换成 `_` 再大写，
例如 `sub2api_db.host` → `MONITOR_SUB2API_DB_HOST`。完整清单见 `.env.example`。

常用可选项：

| 变量 | 默认 | 说明 |
|---|---|---|
| `TZ` / `MONITOR_TIMEZONE` | `Asia/Shanghai` | 须与 sub2api 主站一致，影响「今日」口径 |
| `MONITOR_BALANCE_INTERVAL_MINUTES` | `30` | 余额采集间隔 |
| `MONITOR_COST_INTERVAL_MINUTES` | `10` | 上游成本同步间隔 |
| `MONITOR_COST_RETENTION_DAYS` | `180` | 成本明细保留天数 |
| `MONITOR_PROBE_INTERVAL_MINUTES` | `15` | 探测间隔 |
| `MONITOR_PLAZA_ENABLED` | `false` | 模型广场 / KYC 嵌入页总开关 |

## 健康检查

`/health` 免鉴权，返回 `{status, pg, sqlite}`。

注意：上游 PG 不可用时它**仍返回 200**（只是 `pg: "down"`），这是有意的降级
设计 —— 上游库挂掉不代表本容器不健康，相关接口会返回 503 并在后台持续重试。

## 许可证

MIT

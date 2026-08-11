#!/usr/bin/env python3
"""把本地 monitor.db（SQLite）的全量数据搬到 PostgreSQL。

一次性工具，用完即弃。设计要点：

1. 类型转换在 Python 侧做，不交给 PG 隐式转换 —— PG 遇到格式异常的行会整批
   失败且不告诉你是哪一行。这里能精确报「表 X 的 id=N 的 created_at 无法解析」。
   这不是假想问题：Go 侧 provider_repo.go 的 parseTime 是 `t, _ := time.Parse(...)`，
   吞掉错误返回零值，历史上任何解析失败都静默变成了 0001-01-01。

2. 加密列（*_enc / login_password / access_token / refresh_token / session_cookie）
   按 TEXT 原样搬，不解密不重新加密。本脚本因此不需要也不应该持有
   MONITOR_CREDENTIALS_KEY。

3. 显式保留原 id，搬完 setval 重置序列 —— provider_id / customer_id 等跨表引用
   必须继续有效。

4. 两批搬运：父表 → 子表，满足外键。

用法：
    python migrate.py --src monitor.db --dsn 'postgresql://...' [--execute]
默认空跑（只报告，不写入）。
"""
import argparse
import sqlite3
import sys
from datetime import datetime, date, timezone

import psycopg


# 每张表声明：需要转成 timestamptz 的列、需要转成 date 的列、需要转成 bool 的列。
# 未列出的列按原样搬（TEXT/INTEGER/REAL 直通）。
SCHEMA = {
    # ---- 第一批：父表（无外键依赖，或只依赖同批内已建的表） ----
    "providers": {
        "ts": ["token_expires_at", "last_balance_at", "login_cooldown_until",
               "created_at", "updated_at"],
        "bool": ["probe_enabled", "ignore_balance_alert", "self_operated"],
    },
    "customers": {
        "ts": ["alert_at", "last_entry_at", "created_at", "updated_at"],
    },
    "settings": {"ts": ["updated_at"]},
    "collector_state": {
        "ts": ["last_run_at", "last_success_at", "next_eligible_at"],
    },
    "probe_budget": {"date": ["day"]},
    "probe_results": {"ts": ["created_at"], "bool": ["success"]},
    "health_states": {
        "ts": ["cooldown_until", "observing_until", "last_probe_at", "updated_at"],
    },
    "health_events": {"ts": ["created_at"]},
    "rate_snapshots": {
        "ts": ["first_seen_at", "last_seen_at"],
        "bool": ["deleted"],
    },
    "local_group_pricing": {
        "ts": ["created_at", "updated_at"],
        "bool": ["auto_enabled", "conflict"],
    },
    "pricing_sources": {},
    "rate_actions": {"ts": ["created_at"]},
    "upstream_connections": {"ts": ["created_at", "updated_at"]},
    "media_tasks": {"ts": ["created_at", "updated_at"]},
    "credit_ledger": {"ts": ["occurred_at", "created_at"]},

    # ---- 第二批：子表（外键指向第一批） ----
    "balance_snapshots": {"ts": ["created_at"]},
    "upstream_key_costs": {"ts": ["synced_at"], "date": ["usage_date"]},
    "upstream_key_map": {"ts": ["updated_at"]},
    "cost_sync_state": {"ts": ["last_synced_at", "backfilled_at", "updated_at"]},
    "provider_accounts": {"ts": ["created_at"]},
    "provider_operating_costs": {"ts": ["created_at"], "date": ["occurred_on"]},
    "customer_kyc": {
        "ts": ["submitted_at", "reviewed_at", "created_at", "updated_at"],
    },
}

# 搬运顺序：父表在前。credit_ledger.customer_id 与 pricing_sources.pricing_id
# 在 SQLite 侧就没建外键（跨库引用的刻意设计），PG 侧同样没建，顺序无所谓，
# 但仍按逻辑归组以便阅读。
ORDER = [
    "providers", "customers", "settings", "collector_state", "probe_budget",
    "probe_results", "health_states", "health_events", "rate_snapshots",
    "local_group_pricing", "pricing_sources", "rate_actions",
    "upstream_connections", "media_tasks", "credit_ledger",
    # 子表
    "balance_snapshots", "upstream_key_costs", "upstream_key_map",
    "cost_sync_state", "provider_accounts", "provider_operating_costs",
    "customer_kyc",
]

# 时间解析容忍的格式。第一个是全库约定的 RFC3339，其余是历史遗留的可能形态。
TS_FORMATS = [
    "%Y-%m-%dT%H:%M:%SZ",
    "%Y-%m-%dT%H:%M:%S%z",
    "%Y-%m-%d %H:%M:%S",
    "%Y-%m-%dT%H:%M:%S.%fZ",
]


class Problem:
    """一处转换异常。收集而非立即抛出，让空跑能一次报全。"""
    def __init__(self, table, rowid, col, raw, why):
        self.table, self.rowid, self.col, self.raw, self.why = table, rowid, col, raw, why

    def __str__(self):
        return f"  {self.table}[{self.rowid}].{self.col} = {self.raw!r} -> {self.why}"


def parse_ts(v, table, rowid, col, problems):
    """TEXT(RFC3339) -> datetime。空/NULL 按 NULL（沿用 parseTimePtr 的语义）。"""
    if v is None or v == "":
        return None
    if isinstance(v, (int, float)):
        problems.append(Problem(table, rowid, col, v, "数值型时间，非预期"))
        return None
    s = str(v).strip()
    for fmt in TS_FORMATS:
        try:
            dt = datetime.strptime(s, fmt)
            return dt.replace(tzinfo=timezone.utc) if dt.tzinfo is None else dt
        except ValueError:
            continue
    problems.append(Problem(table, rowid, col, s, "无法解析为时间"))
    return None


def parse_date(v, table, rowid, col, problems):
    """TEXT(YYYY-MM-DD) -> date。"""
    if v is None or v == "":
        return None
    s = str(v).strip()
    try:
        return datetime.strptime(s, "%Y-%m-%d").date()
    except ValueError:
        pass
    # 容忍带时刻的形态，取日期部分
    dt = parse_ts(s, table, rowid, col, [])
    if dt:
        return dt.date()
    problems.append(Problem(table, rowid, col, s, "无法解析为日期"))
    return None


def to_bool(v):
    """INTEGER -> BOOLEAN。不假设只有 0/1。"""
    if v is None:
        return None
    return bool(v)


def migrate_table(sq, pg, table, spec, execute, problems):
    cur = sq.execute(f'SELECT * FROM "{table}"')
    cols = [d[0] for d in cur.description]
    rows = cur.fetchall()
    if not rows:
        return 0, 0

    ts_cols = set(spec.get("ts", []))
    date_cols = set(spec.get("date", []))
    bool_cols = set(spec.get("bool", []))
    pk = "id" if "id" in cols else cols[0]
    pk_idx = cols.index(pk)

    converted = []
    for r in rows:
        rowid = r[pk_idx]
        out = []
        for i, c in enumerate(cols):
            v = r[i]
            if c in ts_cols:
                v = parse_ts(v, table, rowid, c, problems)
            elif c in date_cols:
                v = parse_date(v, table, rowid, c, problems)
            elif c in bool_cols:
                v = to_bool(v)
            out.append(v)
        converted.append(tuple(out))

    if not execute:
        return len(rows), 0

    collist = ", ".join(f'"{c}"' for c in cols)
    with pg.cursor() as pc:
        with pc.copy(f'COPY "{table}" ({collist}) FROM STDIN') as cp:
            for row in converted:
                cp.write_row(row)
    return len(rows), len(converted)


def reset_sequences(pg):
    """遍历 information_schema 自动发现序列并重置。

    不硬编码序列名：名字由 PG 生成，表名超长会被截断。
    不硬编码表清单：漏一张表的后果是启动后第一次 INSERT 撞主键冲突。
    """
    with pg.cursor() as pc:
        pc.execute("""
            SELECT table_name, column_name
            FROM information_schema.columns
            WHERE table_schema = 'public' AND column_default LIKE 'nextval(%'
            ORDER BY table_name
        """)
        targets = pc.fetchall()
        done = []
        for tbl, col in targets:
            pc.execute(f"""
                SELECT setval(
                    pg_get_serial_sequence('{tbl}', '{col}'),
                    COALESCE((SELECT MAX("{col}") FROM "{tbl}"), 0) + 1,
                    false)
            """)
            done.append((tbl, col, pc.fetchone()[0]))
    return done


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--src", required=True)
    ap.add_argument("--dsn", required=True)
    ap.add_argument("--execute", action="store_true",
                    help="实际写入。不加则空跑，只报告转换问题。")
    args = ap.parse_args()

    sq = sqlite3.connect(args.src)
    problems = []

    print(f"{'表':<28} {'读取':>8} {'写入':>8}")
    print("-" * 48)

    with psycopg.connect(args.dsn) as pg:
        total_read = total_written = 0
        for table in ORDER:
            spec = SCHEMA[table]
            try:
                read, written = migrate_table(sq, pg, table, spec, args.execute, problems)
            except Exception as e:
                print(f"{table:<28} {'ERROR':>8}  {e}")
                pg.rollback()
                sys.exit(1)
            total_read += read
            total_written += written
            print(f"{table:<28} {read:>8} {written:>8}")
        print("-" * 48)
        print(f"{'合计':<28} {total_read:>8} {total_written:>8}")

        if args.execute:
            pg.commit()
            print("\n=== 重置序列 ===")
            for tbl, col, val in reset_sequences(pg):
                print(f"  {tbl}.{col} -> {val}")
            pg.commit()

    if problems:
        print(f"\n=== 转换问题 {len(problems)} 处 ===")
        for p in problems[:60]:
            print(p)
        if len(problems) > 60:
            print(f"  ... 另有 {len(problems) - 60} 处")
    else:
        print("\n转换问题: 无")

    if not args.execute:
        print("\n[空跑] 未写入任何数据。加 --execute 执行实际搬运。")


if __name__ == "__main__":
    main()

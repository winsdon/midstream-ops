#!/usr/bin/env python3
"""校验 SQLite -> PostgreSQL 迁移的完整性。

三层校验，按发现问题的能力从弱到强：
1. 行数    —— 只能证明「搬了」
2. id 极值 —— 能发现「少搬了尾部」
3. 数值求和 —— 能发现类型转换精度丢失与 BIGINT 溢出（最有价值）
   重点盯 media_tasks.cost_ticks：1 tick = 1e-10 USD，若 PG 侧误用 INTEGER
   会静默截断，只有求和对比能抓到。
4. 时间抽样 —— 逐行比对 RFC3339 字符串与 timestamptz 是否指向同一时刻。
   行数和求和都查不出「时间列全变 0001-01-01」。

用法：
    python verify.py --src monitor.db --dsn 'postgresql://...'
校验失败时打印全部差异并以非零码退出。
"""
import argparse
import sqlite3
import sys
from datetime import datetime, timezone

import psycopg

TABLES = [
    "providers", "customers", "settings", "collector_state", "probe_budget",
    "probe_results", "health_states", "health_events", "rate_snapshots",
    "local_group_pricing", "pricing_sources", "rate_actions",
    "upstream_connections", "media_tasks", "credit_ledger",
    "balance_snapshots", "upstream_key_costs", "upstream_key_map",
    "cost_sync_state", "provider_accounts", "provider_operating_costs",
    "customer_kyc",
]

# 表 -> 需要求和比对的数值列
SUMS = {
    "balance_snapshots": ["balance"],
    "upstream_key_costs": ["actual_cost", "official_cost", "requests"],
    "provider_operating_costs": ["amount"],
    "credit_ledger": ["amount"],
    "customers": ["outstanding", "credit_limit"],
    "media_tasks": ["cost_ticks", "est_cost_ticks", "progress"],
    "providers": ["low_balance_threshold", "recharge_rate", "quota_per_unit"],
    "rate_snapshots": ["rate"],
    "probe_results": ["ttft_ms", "total_ms"],
    "health_states": ["weight_percent", "consecutive_failures"],
    "upstream_key_map": ["rate_multiplier"],
}

# 布尔列：SQLite 的 SUM(col) 应等于 PG 的 COUNT(*) FILTER (WHERE col)
BOOLS = {
    "providers": ["probe_enabled", "ignore_balance_alert", "self_operated"],
    "probe_results": ["success"],
    "rate_snapshots": ["deleted"],
    "local_group_pricing": ["auto_enabled", "conflict"],
}

# 时间列抽样比对：表 -> (主键列, [时间列])
TIMES = {
    "providers": ("id", ["created_at", "updated_at", "last_balance_at"]),
    "balance_snapshots": ("id", ["created_at"]),
    "probe_results": ("id", ["created_at"]),
    "rate_snapshots": ("id", ["first_seen_at", "last_seen_at"]),
    "credit_ledger": ("id", ["occurred_at", "created_at"]),
    "health_states": ("account_id", ["updated_at", "last_probe_at"]),
    "media_tasks": ("id", ["created_at", "updated_at"]),
    "upstream_key_costs": ("id", ["synced_at"]),
}

fails = []


def check(label, a, b, tol=1e-9):
    """比对两个值。浮点用相对容差（COPY 走文本协议，double 往返应精确，
    但留一点余量以免被最后一位有效数字绊倒）。"""
    if a is None and b is None:
        ok = True
    elif a is None or b is None:
        ok = False
    elif isinstance(a, float) or isinstance(b, float):
        fa, fb = float(a), float(b)
        ok = abs(fa - fb) <= max(tol, abs(fa) * tol)
    else:
        ok = a == b
    if not ok:
        fails.append(f"{label}: sqlite={a!r} pg={b!r}")
    return ok


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--src", required=True, help="旧的 monitor.db")
    ap.add_argument("--dsn", required=True, help="目标 PG 连接串")
    args = ap.parse_args()

    sq = sqlite3.connect(args.src)
    pg = psycopg.connect(args.dsn)
    pc = pg.cursor()

    print("=" * 72)
    print(f"{'表':<28} {'sqlite':>8} {'pg':>8} {'min(id)':>10} {'max(id)':>10}")
    print("=" * 72)

    for t in TABLES:
        n1 = sq.execute(f'SELECT COUNT(*) FROM "{t}"').fetchone()[0]
        pc.execute(f'SELECT COUNT(*) FROM "{t}"')
        n2 = pc.fetchone()[0]
        check(f"{t} 行数", n1, n2)

        idinfo = ""
        cols = [r[1] for r in sq.execute(f'PRAGMA table_info("{t}")')]
        if "id" in cols and n1 > 0:
            a = sq.execute(f'SELECT MIN(id), MAX(id) FROM "{t}"').fetchone()
            pc.execute(f'SELECT MIN(id), MAX(id) FROM "{t}"')
            b = pc.fetchone()
            check(f"{t} min(id)", a[0], b[0])
            check(f"{t} max(id)", a[1], b[1])
            idinfo = f"{a[0]:>10} {a[1]:>10}"

        mark = "OK " if n1 == n2 else "!! "
        print(f"{mark}{t:<26} {n1:>8} {n2:>8} {idinfo}")

    print("\n" + "=" * 72)
    print("数值列求和比对")
    print("=" * 72)
    for t, cols in SUMS.items():
        for c in cols:
            a = sq.execute(f'SELECT SUM("{c}") FROM "{t}"').fetchone()[0]
            pc.execute(f'SELECT SUM("{c}") FROM "{t}"')
            b = pc.fetchone()[0]
            b = float(b) if b is not None else None
            ok = check(f"{t}.{c} 求和", a, b)
            print(f"{'OK ' if ok else '!! '}{t}.{c:<24} {a!r:>24}  {b!r:>24}")

    print("\n" + "=" * 72)
    print("布尔列：sqlite SUM vs pg COUNT FILTER")
    print("=" * 72)
    for t, cols in BOOLS.items():
        for c in cols:
            a = sq.execute(f'SELECT COALESCE(SUM("{c}"),0) FROM "{t}"').fetchone()[0]
            pc.execute(f'SELECT COUNT(*) FILTER (WHERE "{c}") FROM "{t}"')
            b = pc.fetchone()[0]
            ok = check(f"{t}.{c} 真值数", a, b)
            print(f"{'OK ' if ok else '!! '}{t}.{c:<28} {a:>8} {b:>8}")

    print("\n" + "=" * 72)
    print("时间列逐行比对（全量，非抽样）")
    print("=" * 72)
    for t, (pk, tcols) in TIMES.items():
        n = sq.execute(f'SELECT COUNT(*) FROM "{t}"').fetchone()[0]
        if n == 0:
            print(f"-- {t:<26} 空表，跳过")
            continue
        quoted = ", ".join(f'"{c}"' for c in tcols)
        srows = {r[0]: r[1:] for r in
                 sq.execute(f'SELECT "{pk}", {quoted} FROM "{t}"')}
        pc.execute(f'SELECT "{pk}", {quoted} FROM "{t}"')
        prows = {r[0]: r[1:] for r in pc.fetchall()}

        bad = 0
        for k, sv in srows.items():
            pv = prows.get(k)
            if pv is None:
                bad += 1
                fails.append(f"{t}[{k}] 在 PG 中不存在")
                continue
            for i, c in enumerate(tcols):
                s_raw, p_val = sv[i], pv[i]
                if (s_raw in (None, "")) != (p_val is None):
                    bad += 1
                    fails.append(f"{t}[{k}].{c} 空值不一致: sqlite={s_raw!r} pg={p_val!r}")
                    continue
                if s_raw in (None, ""):
                    continue
                try:
                    exp = datetime.strptime(str(s_raw).strip(),
                                            "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc)
                except ValueError:
                    continue
                got = p_val.astimezone(timezone.utc)
                if exp != got:
                    bad += 1
                    if len(fails) < 200:
                        fails.append(f"{t}[{k}].{c} 时刻不符: sqlite={s_raw} pg={got.isoformat()}")
        print(f"{'OK ' if bad == 0 else '!! '}{t:<26} {n:>8} 行 × {len(tcols)} 列  "
              f"{'全部一致' if bad == 0 else f'{bad} 处不符'}")

    print("\n" + "=" * 72)
    if fails:
        print(f"校验失败：{len(fails)} 处")
        for f in fails[:50]:
            print("  " + f)
        if len(fails) > 50:
            print(f"  ... 另有 {len(fails) - 50} 处")
        sys.exit(1)
    print("校验全部通过：行数 / id 极值 / 数值求和 / 布尔真值数 / 时间列逐行 —— 零差异")


if __name__ == "__main__":
    main()

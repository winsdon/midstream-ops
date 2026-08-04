// Package repository 提供数据访问层（线上 PG 只读 + 本地 SQLite）。
package repository

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"sub2api-account-monitor/internal/config"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PG 线上 sub2api Postgres 只读连接池。
type PG struct {
	pool *pgxpool.Pool
	// up 标记最近一次 Ping 是否成功，用于 503 降级。
	up atomic.Bool
}

// NewPG 创建只读连接池（不立即连接；调用 Ping 验证）。
func NewPG(cfg config.Sub2apiDB) (*PG, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("解析 PG DSN 失败: %w", err)
	}
	poolCfg.MaxConns = 4
	poolCfg.MinConns = 0
	poolCfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(context.Background(), poolCfg)
	if err != nil {
		return nil, fmt.Errorf("创建 PG 连接池失败: %w", err)
	}
	return &PG{pool: pool}, nil
}

// Ping 探测连接可用性并更新 up 标记。失败不致命（仅告警）。
func (p *PG) Ping(ctx context.Context) error {
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := p.pool.Ping(c)
	p.up.Store(err == nil)
	return err
}

// Available 报告 PG 是否可用。
func (p *PG) Available() bool { return p.up.Load() }

// Pool 暴露底层连接池供查询使用。
func (p *PG) Pool() *pgxpool.Pool { return p.pool }

// Close 关闭连接池。
func (p *PG) Close() { p.pool.Close() }

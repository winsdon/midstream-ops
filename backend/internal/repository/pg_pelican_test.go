package repository

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
)

func TestPelicanVisibleGroups(t *testing.T) {
	dsn := os.Getenv("MONITOR_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("MONITOR_TEST_PG_DSN is required")
	}
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, err = pool.Exec(ctx, `
 CREATE TEMP TABLE groups(id bigint,name text,platform text,status text,deleted_at timestamptz,allow_image_generation boolean,is_exclusive boolean);
 INSERT INTO groups VALUES(10,'ordinary','anthropic','active',NULL,false,false),(11,'exclusive','openai','active',NULL,false,true),(12,'disabled','openai','disabled',NULL,false,false);
 `)
	if err != nil {
		t.Fatal(err)
	}
	pg := &PG{pool: pool}
	visible, err := pg.PelicanVisibleGroups(ctx)
	if err != nil || len(visible) != 1 || visible[0] != 10 {
		t.Fatalf("visibility %v %v", visible, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE groups SET is_exclusive=true WHERE id=10`); err != nil {
		t.Fatal(err)
	}
	visible, err = pg.PelicanVisibleGroups(ctx)
	if err != nil || len(visible) != 0 {
		t.Fatal("newly exclusive group remains visible")
	}
}

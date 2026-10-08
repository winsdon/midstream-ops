package repository

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
)

func TestPelicanKeysAndVisibility(t *testing.T) {
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
 CREATE TEMP TABLE users(id bigint,status text,deleted_at timestamptz);
 CREATE TEMP TABLE groups(id bigint,name text,platform text,status text,deleted_at timestamptz,allow_image_generation boolean,is_exclusive boolean);
 CREATE TEMP TABLE accounts(id bigint,platform text,status text,deleted_at timestamptz,schedulable boolean,credentials jsonb);
 CREATE TEMP TABLE account_groups(account_id bigint,group_id bigint);
 CREATE TEMP TABLE api_keys(id bigint,user_id bigint,name text,key text,group_id bigint,status text,deleted_at timestamptz,expires_at timestamptz);
 INSERT INTO users VALUES(1,'active',NULL),(2,'active',NULL);
 INSERT INTO groups VALUES(10,'ordinary','anthropic','active',NULL,false,false),(11,'exclusive','openai','active',NULL,false,true),(12,'disabled','openai','disabled',NULL,false,false);
 INSERT INTO api_keys VALUES(1,1,'valid','one',10,'active',NULL,NULL),(2,2,'other','two',10,'active',NULL,NULL),(3,1,'expired','three',10,'active',NULL,now()-interval '1 day'),(4,1,'disabled','four',10,'disabled',NULL,NULL),(5,1,'disabled group','five',12,'active',NULL,NULL),(6,1,'exclusive','six',11,'active',NULL,NULL);
 `)
	if err != nil {
		t.Fatal(err)
	}
	pg := &PG{pool: pool}
	keys, err := pg.ListPelicanKeys(ctx, "1")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0].ID != 1 || keys[1].ID != 6 {
		t.Fatalf("key isolation failed %+v", keys)
	}
	visible, err := pg.PelicanVisibleGroups(ctx)
	if err != nil || len(visible) != 1 || visible[0] != 10 {
		t.Fatalf("visibility %v %v", visible, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET status='disabled' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	keys, err = pg.ListPelicanKeys(ctx, "1")
	if err != nil || len(keys) != 0 {
		t.Fatal("disabled test user can still charge")
	}
	if _, err := pool.Exec(ctx, `UPDATE groups SET is_exclusive=true WHERE id=10`); err != nil {
		t.Fatal(err)
	}
	visible, err = pg.PelicanVisibleGroups(ctx)
	if err != nil || len(visible) != 0 {
		t.Fatal("newly exclusive group remains visible")
	}
}

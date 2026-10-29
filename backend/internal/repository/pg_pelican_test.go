package repository

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
)

func pelicanTestPool(t *testing.T) (context.Context, *PG, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("MONITOR_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("MONITOR_TEST_PG_DSN is required")
	}
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	// 临时表按会话存在：连接池只能有 1 条连接，否则建表和查询不在同一会话。
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return ctx, &PG{pool: pool}, pool
}

func TestPelicanVisibleGroups(t *testing.T) {
	ctx, pg, pool := pelicanTestPool(t)
	_, err := pool.Exec(ctx, `
 CREATE TEMP TABLE groups(id bigint,name text,platform text,status text,deleted_at timestamptz,allow_image_generation boolean,is_exclusive boolean);
 INSERT INTO groups VALUES(10,'ordinary','anthropic','active',NULL,false,false),(11,'exclusive','openai','active',NULL,false,true),(12,'disabled','openai','disabled',NULL,false,false);
 `)
	if err != nil {
		t.Fatal(err)
	}
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

// 账号归属分组的口径：账号真挂在 account_groups 上、分组启用未删除、平台匹配
// （composite 通吃）。刻意不看账号状态、调度开关与专属标记 —— 鹈鹕测试直连账号自己的地址，
// 不经 sub2api 调度，停用、报错、停调的账号都要能列出来看状态，专属分组也一直能当作品归属。本测试把这条
// 口径钉住，免得日后「对齐分组可用模型」时把能测的账号一起过滤掉。
func TestListAccountGroupIDs(t *testing.T) {
	ctx, pg, pool := pelicanTestPool(t)
	_, err := pool.Exec(ctx, `
 CREATE TEMP TABLE groups(id bigint,name text,platform text,status text,deleted_at timestamptz,allow_image_generation boolean,is_exclusive boolean);
 CREATE TEMP TABLE accounts(id bigint,name text,platform text,type text,status text,deleted_at timestamptz,schedulable boolean,credentials jsonb,extra jsonb);
 CREATE TEMP TABLE account_groups(account_id bigint,group_id bigint);
 INSERT INTO groups VALUES(10,'anthropic','anthropic','active',NULL,false,false),(11,'openai','openai','active',NULL,false,false),(12,'disabled','anthropic','disabled',NULL,false,false),(13,'composite','composite','active',NULL,false,false),(14,'deleted','anthropic','active',now(),false,false),(15,'exclusive','anthropic','active',NULL,false,true);
 INSERT INTO accounts VALUES
   (1,'claude','anthropic','api_key','active',NULL,true,'{}','{}'),
   (2,'gpt','openai','api_key','active',NULL,true,'{}','{}'),
   (3,'off','anthropic','api_key','disabled',NULL,true,'{}','{}'),
   (4,'oauth','anthropic','oauth','active',NULL,true,'{}','{}'),
   (5,'gone','anthropic','api_key','active',now(),true,'{}','{}'),
   (6,'paused','anthropic','api_key','active',NULL,false,'{}','{}');
 INSERT INTO account_groups VALUES(1,10),(1,11),(1,12),(1,13),(1,14),(2,11),(2,12),(2,13),(3,10),(4,10),(5,10),(6,15);
 `)
	if err != nil {
		t.Fatal(err)
	}
	groups, err := pg.ListAccountGroupIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := groups[1]; len(got) != 2 || got[0] != 10 || got[1] != 13 {
		t.Fatalf("anthropic account groups %v", got)
	}
	if got := groups[2]; len(got) != 2 || got[0] != 11 || got[1] != 13 {
		t.Fatalf("openai account groups %v", got)
	}
	// 停调（schedulable=false）的账号与专属分组仍然算归属：本页直连凭据，不经过调度。
	if got := groups[6]; len(got) != 1 || got[0] != 15 {
		t.Fatalf("paused account on an exclusive group %v", got)
	}
	// 停用账号也列出来（选择列表里显示状态，由管理员决定测不测）。
	if got := groups[3]; len(got) != 1 || got[0] != 10 {
		t.Fatalf("disabled account groups %v", got)
	}
	if len(groups) != 4 {
		t.Fatalf("deleted or non api_key accounts must not be listed: %v", groups)
	}

	if _, err := pool.Exec(ctx, `ALTER TABLE accounts ADD COLUMN rate_multiplier numeric, ADD COLUMN error_message text,
 ADD COLUMN rate_limit_reset_at timestamptz, ADD COLUMN overload_until timestamptz, ADD COLUMN temp_unschedulable_until timestamptz;
 UPDATE accounts SET error_message='401 invalid key' WHERE id=3;
 UPDATE accounts SET rate_limit_reset_at=now()+interval '1 hour' WHERE id=6;`); err != nil {
		t.Fatal(err)
	}
	accounts, err := pg.ListPelicanAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[int64]PGPelicanAccount{}
	for _, a := range accounts {
		byID[a.ID] = a
	}
	if len(byID) != 4 || byID[3].Status != "disabled" || byID[3].ErrorMessage != "401 invalid key" {
		t.Fatalf("pelican accounts must include disabled ones with error message: %+v", accounts)
	}
	if byID[6].RateLimitResetAt == nil || byID[6].Schedulable || byID[1].RateLimitResetAt != nil {
		t.Fatalf("runtime status fields not scanned: %+v", byID[6])
	}
}

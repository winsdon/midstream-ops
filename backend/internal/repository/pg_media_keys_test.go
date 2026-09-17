package repository

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Exercise both upstream schema variants without modifying any persistent tables.
func TestListUserKeysOptionalModelsListConfig(t *testing.T) {
	dsn := os.Getenv("MONITOR_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("MONITOR_TEST_PG_DSN is required")
	}
	for _, variant := range []string{"absent", "null", "disabled", "enabled"} {
		t.Run(variant, func(t *testing.T) {
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
    CREATE TEMP TABLE groups (id bigint, name text, platform text, status text, deleted_at timestamptz, allow_image_generation boolean);
    CREATE TEMP TABLE accounts (id bigint, platform text, status text, deleted_at timestamptz, schedulable boolean, credentials jsonb);
    CREATE TEMP TABLE account_groups (account_id bigint, group_id bigint);
    CREATE TEMP TABLE api_keys (id bigint, user_id bigint, name text, key text, group_id bigint, status text, deleted_at timestamptz);
    INSERT INTO groups VALUES (10,'test','openai','active',NULL,true);
    INSERT INTO accounts VALUES (20,'openai','active',NULL,true,'{"model_mapping":{"gpt-image-1":"gpt-image-1","gpt-image-2":"gpt-image-2","*":"*"}}');
    INSERT INTO account_groups VALUES (20,10);
    INSERT INTO api_keys VALUES (30,1,'own','test-key-own',10,'active',NULL),(31,2,'other','test-key-other',10,'active',NULL),(32,1,'disabled','test-key-disabled',10,'disabled',NULL);
   `)
			if err != nil {
				t.Fatal(err)
			}
			if variant != "absent" {
				if _, err = pool.Exec(ctx, `ALTER TABLE groups ADD COLUMN models_list_config jsonb`); err != nil {
					t.Fatal(err)
				}
			}
			if variant == "enabled" || variant == "disabled" {
				_, err = pool.Exec(ctx, `UPDATE groups SET models_list_config = jsonb_build_object('enabled', $1::boolean, 'models', jsonb_build_array('gpt-image-2'))`, variant == "enabled")
				if err != nil {
					t.Fatal(err)
				}
			}
			pg := &PG{pool: pool}
			keys, err := pg.ListUserKeys(ctx, "1")
			if err != nil {
				t.Fatalf("ListUserKeys with %s models_list_config: %v", variant, err)
			}
			if len(keys) != 1 || keys[0].ID != 30 {
				t.Fatalf("expected only user's active key; got %d entries", len(keys))
			}
			want := 2
			if variant == "enabled" {
				want = 1
			}
			if len(keys[0].Models) != want {
				t.Fatalf("expected %d models, got %v", want, keys[0].Models)
			}
			if variant == "enabled" && keys[0].Models[0] != "gpt-image-2" {
				t.Fatalf("model allowlist ignored: %v", keys[0].Models)
			}
		})
	}
}

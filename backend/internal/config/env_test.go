package config

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeMinimalConfig 写一份仅含必填项的 config.yaml，让 Validate 能通过，
// 从而把测试焦点放在「环境变量是否真的覆盖了配置」上。
func writeMinimalConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// 两组 DB 的 host/user 也是必填：本地库连不上程序必然起不来，在启动瞬间报
	// 「store_db.host 必填」远好于让 pgx 几秒后抛连接超时。
	body := "auth:\n  jwt_secret: \"" + strings.Repeat("a", 32) + "\"\n" +
		"sub2api_db:\n  host: up-pg\n  user: ro\n" +
		"store_db:\n  host: store-pg\n  user: rw\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o600); err != nil {
		t.Fatalf("写入临时 config.yaml 失败: %v", err)
	}
	return dir
}

// TestEnvOverridesEveryKey 逐个断言 MONITOR_* 能覆盖对应配置项。
//
// 回归目标：viper 的 Unmarshal 只遍历 AllKeys()，没有默认值的键（jwt_secret、
// sub2api_db.host/user/password 等）曾经静默忽略环境变量，导致纯环境变量部署
// （Docker）拿到空配置。bindEnvs 修复后本测试守住该行为。
func TestEnvOverridesEveryKey(t *testing.T) {
	dir := writeMinimalConfig(t)

	env := map[string]string{
		"MONITOR_SERVER_HOST":               "127.0.0.1",
		"MONITOR_SERVER_PORT":               "8080",
		"MONITOR_TIMEZONE":                  "UTC",
		"MONITOR_AUTH_USERNAME":             "ops",
		"MONITOR_AUTH_PASSWORD":             "s3cret",
		"MONITOR_AUTH_TOKEN_TTL_HOURS":      "12",
		"MONITOR_SUB2API_DB_HOST":           "sub2api-postgres",
		"MONITOR_SUB2API_DB_PORT":           "6543",
		"MONITOR_SUB2API_DB_USER":           "monitor_ro",
		"MONITOR_SUB2API_DB_PASSWORD":       "ro-pass",
		"MONITOR_SUB2API_DB_DBNAME":         "sub2api_prod",
		"MONITOR_SUB2API_DB_SSLMODE":        "require",
		"MONITOR_STORE_DB_HOST":             "monitor-postgres",
		"MONITOR_STORE_DB_PORT":             "6544",
		"MONITOR_STORE_DB_USER":             "monitor_rw",
		"MONITOR_STORE_DB_PASSWORD":         "rw-pass",
		"MONITOR_STORE_DB_DBNAME":           "monitor_prod",
		"MONITOR_STORE_DB_SSLMODE":          "require",
		"MONITOR_BALANCE_INTERVAL_MINUTES":  "45",
		"MONITOR_COST_INTERVAL_MINUTES":     "20",
		"MONITOR_COST_RETENTION_DAYS":       "365",
		"MONITOR_PROBE_INTERVAL_MINUTES":    "25",
		"MONITOR_PROBE_CONCURRENCY":         "5",
		"MONITOR_RATES_INTERVAL_MINUTES":    "7",
		"MONITOR_PLAZA_ENABLED":             "true",
		"MONITOR_PLAZA_SUB2API_BASE_URL":    "https://sub2api.example.com",
		"MONITOR_PLAZA_SUB2API_JWT_SECRET":  "shared-secret",
		"MONITOR_PLAZA_SESSION_TTL_MINUTES": "60",
		"MONITOR_LOG_LEVEL":                 "debug",
	}
	for k, val := range env {
		t.Setenv(k, val)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"server.host", cfg.Server.Host, "127.0.0.1"},
		{"server.port", cfg.Server.Port, 8080},
		{"timezone", cfg.Timezone, "UTC"},
		{"auth.username", cfg.Auth.Username, "ops"},
		{"auth.password", cfg.Auth.Password, "s3cret"},
		{"auth.token_ttl_hours", cfg.Auth.TokenTTLHours, 12},
		{"sub2api_db.host", cfg.Sub2api.Host, "sub2api-postgres"},
		{"sub2api_db.port", cfg.Sub2api.Port, 6543},
		{"sub2api_db.user", cfg.Sub2api.User, "monitor_ro"},
		{"sub2api_db.password", cfg.Sub2api.Password, "ro-pass"},
		{"sub2api_db.dbname", cfg.Sub2api.DBName, "sub2api_prod"},
		{"sub2api_db.sslmode", cfg.Sub2api.SSLMode, "require"},
		{"store_db.host", cfg.Store.Host, "monitor-postgres"},
		{"store_db.port", cfg.Store.Port, 6544},
		{"store_db.user", cfg.Store.User, "monitor_rw"},
		{"store_db.password", cfg.Store.Password, "rw-pass"},
		{"store_db.dbname", cfg.Store.DBName, "monitor_prod"},
		{"store_db.sslmode", cfg.Store.SSLMode, "require"},
		{"balance.interval_minutes", cfg.Balance.IntervalMinutes, 45},
		{"cost.interval_minutes", cfg.Cost.IntervalMinutes, 20},
		{"cost.retention_days", cfg.Cost.RetentionDays, 365},
		{"probe.interval_minutes", cfg.Probe.IntervalMinutes, 25},
		{"probe.concurrency", cfg.Probe.Concurrency, 5},
		{"rates.interval_minutes", cfg.Rates.IntervalMinutes, 7},
		{"plaza.enabled", cfg.Plaza.Enabled, true},
		{"plaza.sub2api_base_url", cfg.Plaza.Sub2apiBaseURL, "https://sub2api.example.com"},
		{"plaza.sub2api_jwt_secret", cfg.Plaza.Sub2apiJWTSecret, "shared-secret"},
		{"plaza.session_ttl_minutes", cfg.Plaza.SessionTTLMinutes, 60},
		{"log.level", cfg.Log.Level, "debug"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s 未被环境变量覆盖: got %v, want %v", c.name, c.got, c.want)
		}
	}
}

// TestEnvJWTSecretAloneSatisfiesValidate 验证「无配置文件、纯环境变量」也能启动。
// 这是 Docker 部署的实际形态：镜像里没有 config.yaml，全靠 MONITOR_* 注入。
func TestEnvJWTSecretAloneSatisfiesValidate(t *testing.T) {
	t.Setenv("MONITOR_AUTH_JWT_SECRET", strings.Repeat("k", 32))
	// 两组 DB 的 host/user 也是必填项，纯环境变量部署必须一并提供。
	t.Setenv("MONITOR_SUB2API_DB_HOST", "up-pg")
	t.Setenv("MONITOR_SUB2API_DB_USER", "ro")
	t.Setenv("MONITOR_STORE_DB_HOST", "store-pg")

	// 切到空目录，确保搜索路径 "." 与 "./.." 都找不到 config.yaml
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("纯环境变量加载失败（Docker 部署会起不来）: %v", err)
	}
	if cfg.Auth.JWTSecret != strings.Repeat("k", 32) {
		t.Errorf("jwt_secret 未生效: got %q", cfg.Auth.JWTSecret)
	}
}

// TestDSNStaysReadOnly 守住只读双保险：DSN 必须始终带
// default_transaction_read_only=on，环境变量覆盖连接参数也不能把它弄丢。
func TestDSNStaysReadOnly(t *testing.T) {
	dir := writeMinimalConfig(t)
	t.Setenv("MONITOR_SUB2API_DB_HOST", "sub2api-postgres")
	t.Setenv("MONITOR_SUB2API_DB_USER", "monitor_ro")
	t.Setenv("MONITOR_SUB2API_DB_PASSWORD", "ro-pass")
	t.Setenv("MONITOR_SUB2API_DB_SSLMODE", "require")

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}

	dsn := cfg.Sub2api.DSN()
	if !strings.Contains(dsn, "default_transaction_read_only=on") {
		t.Errorf("DSN 丢失只读保障: %s", dsn)
	}
	if !strings.Contains(dsn, "sslmode=require") {
		t.Errorf("DSN 未采用环境变量的 sslmode: %s", dsn)
	}
	if !strings.Contains(dsn, "sub2api-postgres") {
		t.Errorf("DSN 未采用环境变量的 host: %s", dsn)
	}
}

// TestStoreDSNIsNotReadOnly 是 TestDSNStaysReadOnly 的反向守卫：可写库的 DSN
// 绝不能带只读参数。
//
// pgx 会把未知 DSN 键当 RuntimeParam 静默透传给服务端，误加
// default_transaction_read_only=on 的表现是所有写入报「cannot execute INSERT
// in a read-only transaction」—— 而这在启动与 /health 都看不出来，直到第一次
// 采集才炸。类型分离（StoreDB ≠ Sub2apiDB）是防线，本测试是断言。
func TestStoreDSNIsNotReadOnly(t *testing.T) {
	dir := writeMinimalConfig(t)
	t.Setenv("MONITOR_STORE_DB_HOST", "monitor-postgres")
	t.Setenv("MONITOR_STORE_DB_USER", "monitor_rw")
	t.Setenv("MONITOR_STORE_DB_PASSWORD", "rw-pass")

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}

	dsn := cfg.Store.DSN()
	if strings.Contains(dsn, "read_only") {
		t.Errorf("可写库 DSN 混入了只读参数，所有写入都会被服务端拒绝: %s", dsn)
	}
	if !strings.Contains(dsn, "monitor-postgres") {
		t.Errorf("DSN 未采用环境变量的 host: %s", dsn)
	}
}

// TestDSNEscapesSpecialCharsInPassword 密码含 @ / # 时 DSN 仍须可被解析。
//
// 原实现直接拼接密码，含 @ 的密码会让 URL 解析把它当成 host 分隔符，
// 报的错还是「连接失败」而非「密码格式问题」，极难排查。
func TestDSNEscapesSpecialCharsInPassword(t *testing.T) {
	dir := writeMinimalConfig(t)
	t.Setenv("MONITOR_STORE_DB_HOST", "monitor-postgres")
	t.Setenv("MONITOR_STORE_DB_USER", "monitor_rw")
	t.Setenv("MONITOR_STORE_DB_PASSWORD", "p@ss/w#rd")

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	u, err := url.Parse(cfg.Store.DSN())
	if err != nil {
		t.Fatalf("含特殊字符的密码破坏了 DSN 解析: %v", err)
	}
	if u.Host != "monitor-postgres:5432" {
		t.Errorf("host 解析错误: %s（密码里的 @ 被当成了分隔符？）", u.Host)
	}
	pwd, _ := u.User.Password()
	if pwd != "p@ss/w#rd" {
		t.Errorf("密码往返不一致: got %q", pwd)
	}
}

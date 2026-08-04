// Package config 负责加载与校验应用配置（config.yaml + MONITOR_ 环境变量覆盖）。
package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config 应用配置根结构。
type Config struct {
	Server   ServerConfig   `mapstructure:"server"`
	Timezone string         `mapstructure:"timezone"`
	Auth     AuthConfig     `mapstructure:"auth"`
	Sub2api  Sub2apiDB      `mapstructure:"sub2api_db"`
	SQLite   SQLiteConfig   `mapstructure:"sqlite"`
	Balance  BalanceConfig  `mapstructure:"balance"`
	Cost     CostConfig     `mapstructure:"cost"`
	Probe    ProbeConfig    `mapstructure:"probe"`
	Rates    RatesConfig    `mapstructure:"rates"`
	Plaza    PlazaConfig    `mapstructure:"plaza"`
	Log      LogConfig      `mapstructure:"log"`

	// Location 由 Timezone 解析得到，用于「今日」边界计算。
	Location *time.Location `mapstructure:"-"`
}

type ServerConfig struct {
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
}

type AuthConfig struct {
	Username      string `mapstructure:"username"`
	Password      string `mapstructure:"password"` // 明文；$2a$/$2b$ 前缀按 bcrypt 校验
	JWTSecret     string `mapstructure:"jwt_secret"`
	TokenTTLHours int    `mapstructure:"token_ttl_hours"`
}

type Sub2apiDB struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	User     string `mapstructure:"user"`
	Password string `mapstructure:"password"`
	DBName   string `mapstructure:"dbname"`
	SSLMode  string `mapstructure:"sslmode"`
}

// DSN 返回强制只读的 Postgres DSN。
func (d Sub2apiDB) DSN() string {
	ssl := d.SSLMode
	if ssl == "" {
		ssl = "disable"
	}
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s&default_transaction_read_only=on",
		d.User, d.Password, d.Host, d.Port, d.DBName, ssl,
	)
}

type SQLiteConfig struct {
	Path string `mapstructure:"path"`
}

type BalanceConfig struct {
	IntervalMinutes int `mapstructure:"interval_minutes"`
	TimeoutSeconds  int `mapstructure:"timeout_seconds"`
	Concurrency     int `mapstructure:"concurrency"`
	RetentionDays   int `mapstructure:"retention_days"`
}

// CostConfig 上游真实成本同步配置。
// 成本口径为上游 actual_cost（倍率折后实扣），定时拉取落本地库，查询只读本地库。
type CostConfig struct {
	IntervalMinutes int `mapstructure:"interval_minutes"`
	TimeoutSeconds  int `mapstructure:"timeout_seconds"`
	Concurrency     int `mapstructure:"concurrency"`
	RetentionDays   int `mapstructure:"retention_days"`
}

type ProbeConfig struct {
	IntervalMinutes int               `mapstructure:"interval_minutes"`
	TimeoutSeconds  int               `mapstructure:"timeout_seconds"`
	Concurrency     int               `mapstructure:"concurrency"`
	RetentionDays   int               `mapstructure:"retention_days"`
	DefaultModels   map[string]string `mapstructure:"default_models"`
}

type RatesConfig struct {
	IntervalMinutes int `mapstructure:"interval_minutes"`
	RetentionDays   int `mapstructure:"retention_days"`
}

// PlazaConfig 模型广场嵌入页配置。
//
// 页面通过 sub2api 的自定义菜单以 iframe 嵌入，鉴权靠 sub2api 透传的用户 token：
// monitor 用共享密钥本地验签，解出用户身份后签发自己的短期会话。
//
// 为何不回调 sub2api 的 /auth/me 校验：线上 sub2api 开启了会话绑定（JWT 的 bnd
// claim = sha256(客户端IP + UA)），服务端直连的 IP/UA 必然与浏览器不同 → 校验失败，
// 且 sub2api 在指纹不匹配时会撤销该用户整个会话家族，把用户从浏览器踢下线。
type PlazaConfig struct {
	Enabled bool `mapstructure:"enabled"`
	// Sub2apiBaseURL 是可信的 sub2api 站点 origin，用作 CSP frame-ancestors
	// （只允许该站点嵌入本页）。
	Sub2apiBaseURL string `mapstructure:"sub2api_base_url"`
	// Sub2apiJWTSecret 是 sub2api 的 jwt.secret，用于本地 HS256 验签。
	Sub2apiJWTSecret  string `mapstructure:"sub2api_jwt_secret"`
	SessionTTLMinutes int    `mapstructure:"session_ttl_minutes"`
	CacheSeconds      int    `mapstructure:"cache_seconds"`
	MetricsHours      int    `mapstructure:"metrics_hours"`

	// DevMode 开启本地调试端点 /api/v1/embed/_dev/token（自签任意用户身份的 token）。
	//
	// 【生产必须为 false】该端点能签出任意 user_id 的合法身份，等同于「无密码登录成任何人」。
	// 之所以敢做这个开关，是因为本站对 sub2api token 是本地 HMAC 验签（见 PlazaConfig 注释），
	// 密钥已在本进程内——开关只是把「已有的签名能力」暴露成 HTTP 接口，不引入新的密钥风险，
	// 但暴露面天差地别。默认 false，开启时启动日志会持续告警。
	DevMode bool `mapstructure:"dev_mode"`
}

type LogConfig struct {
	Level string `mapstructure:"level"`
}

// Load 从 path（目录或文件）加载配置，并填充默认值、执行校验。
func Load(path string) (*Config, error) {
	v := viper.New()
	setDefaults(v)

	v.SetConfigName("config")
	v.SetConfigType("yaml")
	if path != "" {
		// 允许传入具体文件或目录
		if strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml") {
			v.SetConfigFile(path)
		} else {
			v.AddConfigPath(path)
		}
	}
	v.AddConfigPath(".")
	v.AddConfigPath("./..")

	v.SetEnvPrefix("MONITOR")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	bindEnvs(v)

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			return nil, fmt.Errorf("读取配置失败: %w", err)
		}
		// 未找到配置文件时继续使用默认+环境变量
	}

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.port", 9090)
	v.SetDefault("timezone", "Asia/Shanghai")

	v.SetDefault("auth.username", "admin")
	v.SetDefault("auth.password", "change-me")
	v.SetDefault("auth.token_ttl_hours", 24)

	v.SetDefault("sub2api_db.port", 5432)
	v.SetDefault("sub2api_db.dbname", "sub2api")
	v.SetDefault("sub2api_db.sslmode", "disable")

	v.SetDefault("sqlite.path", "data/monitor.db")

	v.SetDefault("balance.interval_minutes", 30)
	v.SetDefault("balance.timeout_seconds", 15)
	v.SetDefault("balance.concurrency", 3)
	v.SetDefault("balance.retention_days", 90)

	v.SetDefault("cost.interval_minutes", 10)
	v.SetDefault("cost.timeout_seconds", 30)
	v.SetDefault("cost.concurrency", 2)
	v.SetDefault("cost.retention_days", 180)

	v.SetDefault("probe.interval_minutes", 15)
	v.SetDefault("probe.timeout_seconds", 30)
	v.SetDefault("probe.concurrency", 2)
	v.SetDefault("probe.retention_days", 30)
	v.SetDefault("probe.default_models", map[string]string{
		"anthropic": "claude-3-5-haiku-20241022",
		"openai":    "gpt-4o-mini",
		"gemini":    "gemini-2.5-flash",
	})

	v.SetDefault("rates.interval_minutes", 5)
	v.SetDefault("rates.retention_days", 365)

	v.SetDefault("plaza.enabled", false)
	v.SetDefault("plaza.session_ttl_minutes", 30)
	v.SetDefault("plaza.cache_seconds", 60)
	v.SetDefault("plaza.metrics_hours", 24)

	v.SetDefault("log.level", "info")
}

// bindEnvs 显式声明所有可被 MONITOR_* 环境变量覆盖的键。
//
// 为什么单靠 AutomaticEnv 不够：viper 的 Unmarshal 只遍历 AllKeys()，而该集合
// 来自 SetDefault 的键 + 配置文件里实际出现的键。没有默认值、又不在配置文件里的
// 键（如 auth.jwt_secret、sub2api_db.host/user/password）根本不会被 Unmarshal
// 访问到，环境变量因此**静默失效**——纯环境变量部署（Docker）会拿到空配置。
//
// 不用 SetDefault("", ...) 补齐：空默认值会把空串灌进 AllKeys()，让「未配置」与
// 「显式配空」不可区分，还会干扰配置文件的嵌套合并。BindEnv 只声明「此键接受
// 环境变量」，不伪造值。
func bindEnvs(v *viper.Viper) {
	keys := []string{
		"server.host", "server.port",
		"timezone",

		"auth.username", "auth.password", "auth.jwt_secret", "auth.token_ttl_hours",

		"sub2api_db.host", "sub2api_db.port", "sub2api_db.user",
		"sub2api_db.password", "sub2api_db.dbname", "sub2api_db.sslmode",

		"sqlite.path",

		"balance.interval_minutes", "balance.timeout_seconds",
		"balance.concurrency", "balance.retention_days",

		"cost.interval_minutes", "cost.timeout_seconds",
		"cost.concurrency", "cost.retention_days",

		"probe.interval_minutes", "probe.timeout_seconds",
		"probe.concurrency", "probe.retention_days",

		"rates.interval_minutes", "rates.retention_days",

		"plaza.enabled", "plaza.sub2api_base_url", "plaza.sub2api_jwt_secret",
		"plaza.session_ttl_minutes", "plaza.cache_seconds", "plaza.metrics_hours",
		"plaza.dev_mode",

		"log.level",
	}
	for _, k := range keys {
		// 单参数 BindEnv 依赖 EnvPrefix + EnvKeyReplacer 推导变量名，
		// 如 sub2api_db.host → MONITOR_SUB2API_DB_HOST。
		_ = v.BindEnv(k)
	}
}

// Validate 校验必填项并解析时区。
func (c *Config) Validate() error {
	if c.Auth.JWTSecret == "" || len(c.Auth.JWTSecret) < 32 {
		return errors.New("auth.jwt_secret 必填且长度须 ≥ 32 字节")
	}
	if c.Auth.Username == "" {
		return errors.New("auth.username 不能为空")
	}
	if c.Auth.Password == "" {
		return errors.New("auth.password 不能为空")
	}
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return fmt.Errorf("timezone 无效: %w", err)
	}
	c.Location = loc
	if err := c.Plaza.validate(); err != nil {
		return err
	}
	return nil
}

// validate 校验模型广场配置。
// 启用时 sub2api_base_url 必填且须为绝对 http(s) 地址（作为 CSP frame-ancestors 的值，
// 格式错会导致嵌入静默失败）；sub2api_jwt_secret 必填（本地验签用户 token）。
func (p *PlazaConfig) validate() error {
	if !p.Enabled {
		return nil
	}
	raw := strings.TrimSpace(p.Sub2apiBaseURL)
	if raw == "" {
		return errors.New("plaza.enabled 为 true 时 plaza.sub2api_base_url 必填")
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("plaza.sub2api_base_url 须为绝对 http(s) 地址，如 https://your-sub2api.com")
	}
	// 归一化为 origin：去掉路径/查询/片段与尾部斜杠，直接可用作 CSP 值。
	u.Path, u.RawQuery, u.Fragment = "", "", ""
	p.Sub2apiBaseURL = strings.TrimRight(u.String(), "/")

	if strings.TrimSpace(p.Sub2apiJWTSecret) == "" {
		return errors.New("plaza.enabled 为 true 时 plaza.sub2api_jwt_secret 必填（须与 sub2api 的 jwt.secret 一致）")
	}
	return nil
}

// TodayRange 返回当前时区「今日」的起止时间（UTC，左闭右开）。
func (c *Config) TodayRange() (start, end time.Time) {
	now := time.Now().In(c.Location)
	y, m, d := now.Date()
	start = time.Date(y, m, d, 0, 0, 0, 0, c.Location).UTC()
	end = start.Add(24 * time.Hour)
	return start, end
}

// DayRange 返回指定日期（当前时区）的起止时间（UTC，左闭右开）。
func (c *Config) DayRange(t time.Time) (start, end time.Time) {
	t = t.In(c.Location)
	y, m, d := t.Date()
	start = time.Date(y, m, d, 0, 0, 0, 0, c.Location).UTC()
	end = start.Add(24 * time.Hour)
	return start, end
}

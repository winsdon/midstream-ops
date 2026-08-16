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
	Server   ServerConfig  `mapstructure:"server"`
	Timezone string        `mapstructure:"timezone"`
	Auth     AuthConfig    `mapstructure:"auth"`
	Sub2api  Sub2apiDB     `mapstructure:"sub2api_db"`
	Store    StoreDB       `mapstructure:"store_db"`
	Balance  BalanceConfig `mapstructure:"balance"`
	Cost     CostConfig    `mapstructure:"cost"`
	Probe    ProbeConfig   `mapstructure:"probe"`
	Rates    RatesConfig   `mapstructure:"rates"`
	Plaza    PlazaConfig   `mapstructure:"plaza"`
	Media    MediaConfig   `mapstructure:"media"`
	Log      LogConfig     `mapstructure:"log"`

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

// PGConn 一个 Postgres 连接的通用参数。上游只读库与本地可写库共用此结构。
//
// 【为什么不把只读标志做成本结构的字段】只读是「这个库是什么」的属性，不是
// 「这次连接想怎么用」的选项。做成字段就意味着它可被配置文件/环境变量覆盖，
// 而 TestDSNStaysReadOnly 守的正是「任何外部输入都不能把只读弄丢」。只读性
// 因此上移到两个独立的 DSN 方法里各自硬编码，不接受输入。
type PGConn struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	User     string `mapstructure:"user"`
	Password string `mapstructure:"password"`
	DBName   string `mapstructure:"dbname"`
	SSLMode  string `mapstructure:"sslmode"`
}

// baseDSN 返回不含任何事务模式参数的基础 DSN。
//
// 【不导出】导出会让调用方绕过下面两个语义明确的包装，上游库就可能拿到一个
// 丢了只读保障的 DSN。
//
// 用户名与密码走 url.QueryEscape：密码含 @ / # 等字符会破坏 URL 解析。
func (p PGConn) baseDSN() string {
	ssl := p.SSLMode
	if ssl == "" {
		ssl = "disable"
	}
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		url.QueryEscape(p.User), url.QueryEscape(p.Password),
		p.Host, p.Port, p.DBName, ssl)
}

// validate 校验连接必填项。prefix 用于错误文案指明是哪一组配置。
//
// 不校验 password：本地开发常用 trust/peer 认证或 ~/.pgpass，强制非空会挡住
// 合法用法。
func (p PGConn) validate(prefix string) error {
	if strings.TrimSpace(p.Host) == "" {
		return fmt.Errorf("%s.host 必填", prefix)
	}
	if p.Port <= 0 || p.Port > 65535 {
		return fmt.Errorf("%s.port 无效: %d", prefix, p.Port)
	}
	if strings.TrimSpace(p.User) == "" {
		return fmt.Errorf("%s.user 必填", prefix)
	}
	if strings.TrimSpace(p.DBName) == "" {
		return fmt.Errorf("%s.dbname 必填", prefix)
	}
	return nil
}

// Sub2apiDB 上游 sub2api 库（只读）。
type Sub2apiDB struct {
	PGConn `mapstructure:",squash"`
}

// DSN 返回强制只读的 Postgres DSN。只读性硬编码在此，不受任何配置项影响。
func (d Sub2apiDB) DSN() string {
	return d.baseDSN() + "&default_transaction_read_only=on"
}

// StoreDB 本地 monitor 库（可写），存本项目自己的数据。
//
// 与上游库一样是【外部 PG】：本项目不负责起数据库，可以是同一个 PG 实例上的
// 另一个库，也可以是完全独立的实例。
type StoreDB struct {
	PGConn `mapstructure:",squash"`
}

// DSN 返回可写 DSN。
//
// 【绝不能带 default_transaction_read_only】pgx 会把未知 DSN 键当 RuntimeParam
// 透传给服务端并静默生效，误加会让所有写入报「cannot execute INSERT in a
// read-only transaction」——而这在启动与 /health 都看不出来，直到第一次采集
// 才炸。类型分离（StoreDB ≠ Sub2apiDB）是防止误用的手段，
// TestStoreDSNIsNotReadOnly 是守卫。
func (s StoreDB) DSN() string { return s.baseDSN() }

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

// MediaConfig 生图 / 生视频嵌入页配置。
//
// 复用 plaza 的嵌入身份体系（同一份 sub2api_jwt_secret 与会话存储），
// 但功能开关独立：生图会真实花用户的钱，运营方可能想在开着广场的同时关掉它。
//
// 【GatewayBaseURL 为何不复用 plaza.sub2api_base_url】后者被归一化成纯 origin
// 专供 CSP frame-ancestors 使用，语义是「谁能嵌入本站」；这里是「本站去调谁的
// API」。线上二者可能同域，但网关独立部署时就会分叉——混用会踩坑。
type MediaConfig struct {
	Enabled bool `mapstructure:"enabled"`
	// GatewayBaseURL 生图 / 生视频 API 的基址，如 https://api.example.com。
	GatewayBaseURL string `mapstructure:"gateway_base_url"`
	// MaxPendingVideos 单用户同时进行中的视频任务上限。
	// 视频提交即扣费，上限是防「狂点按钮把余额刷空」的最后一道闸。
	MaxPendingVideos int `mapstructure:"max_pending_videos"`
	// TaskRetentionDays 任务记录保留天数，由每日清理任务消费。
	TaskRetentionDays int `mapstructure:"task_retention_days"`
	// R2 产物转存到 Cloudflare R2 的配置。
	R2 MediaR2Config `mapstructure:"r2"`
}

// MediaR2Config 产物对象存储（Cloudflare R2）配置。
//
// 【为什么需要它】图片走 b64 只在提交响应里返回一次，视频只有一个带认证的临时
// 端点且上游有保留期。两者都无法让用户在刷新页面后继续查看自己付费生成的东西。
// 转存到 R2 后，前端拿到的是不需要认证、不会过期的普通 URL。
//
// 关闭时功能整体退化到「图片 inline、视频经后端代理」，不影响生成本身。
type MediaR2Config struct {
	Enabled         bool   `mapstructure:"enabled"`
	AccountID       string `mapstructure:"account_id"`
	Bucket          string `mapstructure:"bucket"`
	AccessKeyID     string `mapstructure:"access_key_id"`
	SecretAccessKey string `mapstructure:"secret_access_key"`
	// PublicBaseURL 桶绑定的公开访问域名，如 https://media.example.com。
	//
	// 【必须用自定义域】R2 自带的 *.r2.dev 域有严格速率限制，生产用它会被限流。
	PublicBaseURL string `mapstructure:"public_base_url"`
}

// validate 校验 R2 配置。启用时五项必填，公开域名须为绝对 http(s) 地址。
func (r *MediaR2Config) validate() error {
	if !r.Enabled {
		return nil
	}
	required := map[string]string{
		"media.r2.account_id":        r.AccountID,
		"media.r2.bucket":            r.Bucket,
		"media.r2.access_key_id":     r.AccessKeyID,
		"media.r2.secret_access_key": r.SecretAccessKey,
	}
	for name, v := range required {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("media.r2.enabled 为 true 时 %s 必填", name)
		}
	}
	raw := strings.TrimSpace(r.PublicBaseURL)
	if raw == "" {
		return errors.New("media.r2.enabled 为 true 时 media.r2.public_base_url 必填")
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("media.r2.public_base_url 须为绝对 http(s) 地址，如 https://media.example.com")
	}
	r.PublicBaseURL = strings.TrimRight(raw, "/")
	return nil
}

// validate 校验生图配置。启用时网关基址必填且须为绝对 http(s) 地址。
func (m *MediaConfig) validate() error {
	if !m.Enabled {
		return nil
	}
	raw := strings.TrimSpace(m.GatewayBaseURL)
	if raw == "" {
		return errors.New("media.enabled 为 true 时 media.gateway_base_url 必填")
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("media.gateway_base_url 须为绝对 http(s) 地址，如 https://api.your-sub2api.com")
	}
	m.GatewayBaseURL = strings.TrimRight(raw, "/")
	return m.R2.validate()
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

	v.SetDefault("store_db.port", 5432)
	v.SetDefault("store_db.dbname", "monitor")
	v.SetDefault("store_db.user", "monitor")
	v.SetDefault("store_db.sslmode", "disable")

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

	v.SetDefault("media.enabled", false)
	v.SetDefault("media.max_pending_videos", 3)
	v.SetDefault("media.task_retention_days", 30)
	v.SetDefault("media.r2.enabled", false)

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

		"store_db.host", "store_db.port", "store_db.user",
		"store_db.password", "store_db.dbname", "store_db.sslmode",

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

		"media.enabled", "media.gateway_base_url",
		"media.max_pending_videos", "media.task_retention_days",
		// R2 凭据几乎只会走环境变量注入（不该写进配置文件），
		// 漏登记会让纯环境变量部署静默拿到空值并在启动校验时报「必填」。
		"media.r2.enabled", "media.r2.account_id", "media.r2.bucket",
		"media.r2.access_key_id", "media.r2.secret_access_key",
		"media.r2.public_base_url",

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
	if err := c.Sub2api.validate("sub2api_db"); err != nil {
		return err
	}
	if err := c.Store.validate("store_db"); err != nil {
		return err
	}
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return fmt.Errorf("timezone 无效: %w", err)
	}
	c.Location = loc
	if err := c.Plaza.validate(); err != nil {
		return err
	}
	if err := c.Media.validate(); err != nil {
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

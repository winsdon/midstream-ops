// sub2api-account-monitor 服务端入口。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	// 内嵌 IANA 时区库。config.Validate() 会 time.LoadLocation(cfg.Timezone)
	// （默认 Asia/Shanghai），失败即 log.Fatalf 退出。alpine / scratch 等精简镜像
	// 不含 /usr/share/zoneinfo，不内嵌则容器必然起不来。时区正确性是本程序的
	// 正确性前提（「今日」边界、对账口径全依赖它），不该外移成镜像的隐式契约。
	_ "time/tzdata"

	"sub2api-account-monitor/internal/config"
	"sub2api-account-monitor/internal/handler"
	"sub2api-account-monitor/internal/notify"
	"sub2api-account-monitor/internal/pkg/health"
	"sub2api-account-monitor/internal/pkg/jwtutil"
	"sub2api-account-monitor/internal/pkg/secretbox"
	"sub2api-account-monitor/internal/repository"
	"sub2api-account-monitor/internal/server"
	"sub2api-account-monitor/internal/service"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "", "配置文件路径（目录或 config.yaml）")
	flag.Parse()

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// SQLite（本地存储）
	sqliteDB, err := repository.NewSQLite(cfg.SQLite.Path)
	if err != nil {
		log.Fatalf("初始化 SQLite 失败: %v", err)
	}
	defer func() { _ = sqliteDB.Close() }()
	log.Printf("SQLite 就绪: %s", cfg.SQLite.Path)

	// PG（线上只读，失败仅告警不退出）
	pg, err := repository.NewPG(cfg.Sub2api)
	if err != nil {
		log.Fatalf("创建 PG 连接池失败: %v", err)
	}
	defer pg.Close()
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 8*time.Second)
	if err := pg.Ping(pingCtx); err != nil {
		log.Printf("[警告] 线上 PG 暂不可用: %v（相关接口将返回 503，后台将持续重试）", err)
	} else {
		log.Printf("线上 PG 已连接: %s:%d/%s", cfg.Sub2api.Host, cfg.Sub2api.Port, cfg.Sub2api.DBName)
	}
	pingCancel()

	// Repos（box 为凭据加解密器：MONITOR_CREDENTIALS_KEY 未配置时明文直通并告警）
	box := secretbox.FromEnv()
	providerRepo := repository.NewProviderRepo(sqliteDB, box)
	balanceRepo := repository.NewBalanceRepo(sqliteDB)
	rateRepo := repository.NewRateRepo(sqliteDB)
	probeRepo := repository.NewProbeRepo(sqliteDB)
	costRepo := repository.NewUpstreamCostRepo(sqliteDB)
	// 自营站运营成本（买号/订阅/服务器）：自营站上游实扣不计入成本，改由本表记账
	opCostRepo := repository.NewOperatingCostRepo(sqliteDB)
	settingsRepo := repository.NewSettingsRepo(sqliteDB)
	collectorRepo := repository.NewCollectorStateRepo(sqliteDB)
	healthRepo := repository.NewHealthRepo(sqliteDB)
	// 供应商 ↔ 本站账号的显式关联（归属唯一真相，取代账号名【】前缀匹配）
	linkRepo := repository.NewProviderAccountRepo(sqliteDB)

	// Services
	jwtMgr := jwtutil.New(cfg.Auth.JWTSecret, time.Duration(cfg.Auth.TokenTTLHours)*time.Hour)
	authSvc := service.NewAuthService(&cfg.Auth, jwtMgr)
	providerSvc := service.NewProviderService(providerRepo, linkRepo, pg)
	statsSvc := service.NewStatsService(pg, costRepo, opCostRepo, linkRepo, providerRepo, cfg)
	opCostSvc := service.NewOperatingCostService(opCostRepo, providerRepo, cfg)
	balanceSvc := service.NewBalanceService(providerRepo, balanceRepo, cfg)
	rateSvc := service.NewRateService(rateRepo, pg)
	probeSvc := service.NewProbeService(probeRepo, providerRepo, linkRepo, healthRepo, pg, cfg)
	costSvc := service.NewCostSyncService(providerRepo, costRepo, pg, cfg)
	syncSvc := service.NewProviderSyncService(providerRepo, collectorRepo, balanceSvc, costSvc, rateSvc, pg)
	pricingRepo := repository.NewPricingRepo(sqliteDB)
	pricingSvc := service.NewPricingService(providerRepo, pricingRepo, rateRepo, balanceSvc, pg)
	connRepo := repository.NewConnectionRepo(sqliteDB)
	provisionSvc := service.NewProvisionService(providerRepo, connRepo, balanceSvc)

	// 系统设置（策略/通知，SQLite 持久化 + 热更新）
	settingsSvc, err := service.NewSettingsService(settingsRepo)
	if err != nil {
		log.Fatalf("加载系统设置失败: %v", err)
	}
	notifier := notify.NewManager(settingsSvc.Channels())
	alertSvc := service.NewAlertService(settingsSvc, notifier)

	// 授信台账（本地记账 + 人工执行；系统永不写上游，见 012_credit_kyc.sql）
	// box 同时用于 KYC 的 PII 加解密 —— 未配置密钥时身份证号等会明文落库。
	creditRepo := repository.NewCreditRepo(sqliteDB, box)
	creditSvc := service.NewCreditService(creditRepo, alertSvc)

	// AfterSync 钩子：余额预警（采集服务不感知通知渠道，全部在装配层挂接）
	syncSvc.AfterSync = alertSvc.HandleSyncOutcome
	// 倍率变更钩子：倍率预警
	rateSvc.OnRateChanged = alertSvc.HandleRateChanges
	// 上游倍率变化 → 自动调价（异步，不阻塞 sync 链路）
	syncSvc.OnUpstreamRateChanged = func(providerID int64, events []service.RateChangeEvent) {
		go pricingSvc.HandleUpstreamRateChanges(providerID, events)
	}
	// 健康状态转移 → 机器人通知（仅 suspended 与恢复 healthy 两类关键转移）
	probeSvc.OnStateChanged = func(accountName string, from, to health.State, detail string) {
		if to != health.StateSuspended && !(to == health.StateHealthy && from != health.StateDegraded) {
			return
		}
		title := "账号恢复"
		text := fmt.Sprintf("账号 **%s** 已恢复正常（%s → %s）", accountName, from, to)
		if to == health.StateSuspended {
			title = "账号熔断"
			text = fmt.Sprintf("账号 **%s** 探测异常已熔断（%s → %s）\n\n%s", accountName, from, to, detail)
		}
		go func() {
			nctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			notifier.Broadcast(nctx, notify.Message{Title: title, Text: text})
		}()
	}

	// 供应商 sync 调度器（per-provider timer；设置热更新时重建）
	syncScheduler := service.NewSyncScheduler(providerRepo, collectorRepo, syncSvc)
	settingsSvc.OnStrategyChanged = func(st service.StrategySettings) {
		syncScheduler.Configure(st.RefreshEnabled, time.Duration(st.RefreshIntervalSeconds)*time.Second)
	}
	settingsSvc.OnNotifyChanged = notifier.Reload

	// 全局 cron（rate 轮询 / probe / 每日清理）
	scheduler := service.NewScheduler(cfg, pg, rateSvc, probeSvc, costSvc, balanceRepo, rateRepo, probeRepo, healthRepo)

	// sub2api iframe 嵌入页（模型广场 + KYC 自助）。未启用时两个 handler 均为 nil，
	// 对应路由整段不注册。
	//
	// 两个页面共用 cfg.Plaza 下的 sub2api_jwt_secret / sub2api_base_url 与同一份
	// 会话存储 —— 身份来源是同一个 sub2api 站点，拆成两套配置只会多一份漂移风险。
	// 因此 plaza.enabled 实际是「嵌入功能总开关」，名字是历史遗留。
	var plazaHandler *handler.PlazaHandler
	var embedKycHandler *handler.EmbedKycHandler
	var embedSessions *service.EmbedSessionStore
	var embedDevHandler *handler.EmbedDevHandler
	if cfg.Plaza.Enabled {
		embedSessions = service.NewEmbedSessionStore(time.Duration(cfg.Plaza.SessionTTLMinutes) * time.Minute)
		defer embedSessions.Close()
		verifier := service.NewSub2apiTokenVerifier(cfg.Plaza.Sub2apiJWTSecret)
		plazaSvc := service.NewPlazaService(
			pg, probeRepo,
			cfg.Plaza.MetricsHours,
			time.Duration(cfg.Plaza.CacheSeconds)*time.Second,
		)
		plazaHandler = handler.NewPlazaHandler(plazaSvc, verifier, embedSessions, pg)
		embedKycHandler = handler.NewEmbedKycHandler(creditSvc, verifier, embedSessions)

		// 本地联调开关：暴露 token 自签端点，等同于「可登录成任意客户」。
		// 每次启动都告警，避免误开后无声无息地带到线上。
		if cfg.Plaza.DevMode {
			log.Printf("[embed] 警告：plaza.dev_mode 已开启，/api/v1/embed/_dev/token 可签发任意用户身份，生产环境务必关闭")
			embedDevHandler = handler.NewEmbedDevHandler(cfg.Plaza.Sub2apiJWTSecret)
		}
	}

	// 装配处理器
	handlers := &server.Handlers{
		Auth:             handler.NewAuthHandler(authSvc),
		Dashboard:        handler.NewDashboardHandler(statsSvc, providerSvc, cfg, pg),
		Provider:         handler.NewProviderHandler(providerSvc, balanceSvc, costSvc, syncSvc, syncScheduler, rateRepo, cfg, pg),
		OpCost:           handler.NewOperatingCostHandler(opCostSvc, cfg),
		Stats:            handler.NewStatsHandler(statsSvc, cfg, pg),
		Rate:             handler.NewRateHandler(rateSvc),
		Stability:        handler.NewStabilityHandler(probeSvc, pg, cfg),
		Settings:         handler.NewSettingsHandler(settingsSvc, notifier),
		Pricing:          handler.NewPricingHandler(pricingSvc, rateRepo, pg),
		Provision:        handler.NewProvisionHandler(provisionSvc),
		Plaza:            plazaHandler,
		Credit:           handler.NewCreditHandler(creditSvc, pg),
		EmbedKyc:         embedKycHandler,
		EmbedDev:         embedDevHandler,
		EmbedSessions:    embedSessions,
		EmbedFrameOrigin: cfg.Plaza.Sub2apiBaseURL,
		PGAvailable:      pg.Available,
	}

	router := server.NewRouter(cfg, authSvc, handlers)

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// 启动时序：同步跑 rate 基线（UI 立即有数），probe 不自动跑
	if pg.Available() {
		bootCtx, bootCancel := context.WithTimeout(context.Background(), 60*time.Second)
		rateSvc.PollOnce(bootCtx)
		bootCancel()
	}

	if err := scheduler.Start(); err != nil {
		log.Fatalf("启动调度器失败: %v", err)
	}
	// 供应商 sync 调度：按持久化的策略设置启动（默认关闭）
	st := settingsSvc.Strategy()
	syncScheduler.Configure(st.RefreshEnabled, time.Duration(st.RefreshIntervalSeconds)*time.Second)

	go func() {
		log.Printf("服务启动于 http://%s (timezone=%s)", addr, cfg.Timezone)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("服务启动失败: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("正在关闭服务...")

	syncScheduler.Stop()
	scheduler.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("服务关闭异常: %v", err)
	}
	log.Println("服务已退出")
}

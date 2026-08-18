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
	"sub2api-account-monitor/internal/pkg/objectstore"
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

	// 本地 monitor 库（PG，可写；存本项目自己的数据）
	store, err := repository.NewStore(cfg.Store)
	if err != nil {
		log.Fatalf("初始化本地 PG 失败: %v", err)
	}
	defer func() { _ = store.Close() }()
	log.Printf("本地 PG 就绪: %s:%d/%s", cfg.Store.Host, cfg.Store.Port, cfg.Store.DBName)

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
	providerRepo := repository.NewProviderRepo(store, box)
	balanceRepo := repository.NewBalanceRepo(store)
	rateRepo := repository.NewRateRepo(store)
	probeRepo := repository.NewProbeRepo(store)
	costRepo := repository.NewUpstreamCostRepo(store)
	// 自营站运营成本（买号/订阅/服务器）：自营站上游实扣不计入成本，改由本表记账
	opCostRepo := repository.NewOperatingCostRepo(store)
	settingsRepo := repository.NewSettingsRepo(store)
	collectorRepo := repository.NewCollectorStateRepo(store)
	healthRepo := repository.NewHealthRepo(store)
	// 供应商 ↔ 本站账号的显式关联（归属唯一真相，取代账号名【】前缀匹配）
	linkRepo := repository.NewProviderAccountRepo(store)

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
	pricingRepo := repository.NewPricingRepo(store)
	pricingSvc := service.NewPricingService(providerRepo, pricingRepo, rateRepo, balanceSvc, pg)
	connRepo := repository.NewConnectionRepo(store)
	provisionSvc := service.NewProvisionService(providerRepo, connRepo, linkRepo, pg, balanceSvc)

	// 系统设置（策略/通知，monitor 库持久化 + 热更新）
	settingsSvc, err := service.NewSettingsService(settingsRepo)
	if err != nil {
		log.Fatalf("加载系统设置失败: %v", err)
	}
	notifier := notify.NewManager(settingsSvc.Channels())
	alertSvc := service.NewAlertService(settingsSvc, notifier)

	// 授信台账（本地记账 + 人工执行；系统永不写上游，见 migrations_pg/001_init_pg.sql）
	// box 同时用于 KYC 的 PII 加解密 —— 未配置密钥时身份证号等会明文落库。
	creditRepo := repository.NewCreditRepo(store, box)
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
	var embedMediaHandler *handler.EmbedMediaHandler
	var mediaSvc *service.MediaService
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

		// 生图 / 生视频页有独立开关：它会真实花用户的钱（视频提交即扣费且不退款），
		// 运营方可能想在开着广场的同时关掉它。
		if cfg.Media.Enabled {
			// 对象存储未配置时 uploader 为 nil：产物不转存，退化到
			// 「图片 inline、视频经后端代理」的行为，生成本身不受影响。
			var uploader objectstore.Uploader
			if cfg.Media.R2.Enabled {
				r2 := objectstore.NewR2(objectstore.R2Config{
					AccountID:       cfg.Media.R2.AccountID,
					Bucket:          cfg.Media.R2.Bucket,
					AccessKeyID:     cfg.Media.R2.AccessKeyID,
					SecretAccessKey: cfg.Media.R2.SecretAccessKey,
					PublicBaseURL:   cfg.Media.R2.PublicBaseURL,
				})
				uploader = r2
				// 浏览器直传参考图依赖桶 CORS；失败不挡启动，只记日志。
				if err := r2.EnsureBrowserPutCORS(context.Background()); err != nil {
					log.Printf("[media] 配置对象存储 CORS 失败（前端直传可能被浏览器拦截）: %v", err)
				}
				log.Printf("[media] 产物转存已启用，公开域名 %s", cfg.Media.R2.PublicBaseURL)
			} else {
				log.Printf("[media] 产物转存未启用：图片刷新后不可见，视频受上游保留期限制")
			}

			mediaSvc = service.NewMediaService(
				pg,
				repository.NewMediaTaskRepo(store),
				repository.NewMediaArtifactRepo(store),
				service.NewMediaGateway(cfg.Media.GatewayBaseURL),
				uploader,
				cfg.Media.MaxPendingVideos,
			)
			embedMediaHandler = handler.NewEmbedMediaHandler(mediaSvc, verifier, embedSessions, pg)

			// 补投上次运行遗留的在途转存：进程在转存途中重启会留下一批
			// storage_status='pending' 的孤儿，它们既不自愈也不会被用户操作触发。
			// 放在后台跑，不阻塞启动。
			go mediaSvc.ResumePendingArchives(context.Background())
			defer mediaSvc.WaitArchives()
		}

		// 本地联调开关：暴露 token 自签端点，等同于「可登录成任意客户」。
		// 每次启动都告警，避免误开后无声无息地带到线上。
		if cfg.Plaza.DevMode {
			log.Printf("[embed] 警告：plaza.dev_mode 已开启，/api/v1/embed/_dev/token 可签发任意用户身份，生产环境务必关闭")
			embedDevHandler = handler.NewEmbedDevHandler(cfg.Plaza.Sub2apiJWTSecret)
		}
	}

	// 生图任务记录纳入每日清理（未启用时 mediaSvc 为 nil，清理项自动跳过）
	scheduler.SetMediaService(mediaSvc)

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
		EmbedMedia:       embedMediaHandler,
		EmbedDev:         embedDevHandler,
		EmbedSessions:    embedSessions,
		EmbedFrameOrigin: cfg.Plaza.Sub2apiBaseURL,
		StorePing:        store.Ping,
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

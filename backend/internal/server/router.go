// Package server 负责装配 gin 引擎与路由。
package server

import (
	"sub2api-account-monitor/internal/config"
	"sub2api-account-monitor/internal/handler"
	"sub2api-account-monitor/internal/pkg/response"
	"sub2api-account-monitor/internal/server/middleware"
	"sub2api-account-monitor/internal/service"
	"sub2api-account-monitor/internal/web"

	"github.com/gin-gonic/gin"
)

// Handlers 聚合所有处理器（随功能扩展逐步填充）。
type Handlers struct {
	Auth      *handler.AuthHandler
	Dashboard *handler.DashboardHandler
	Provider  *handler.ProviderHandler
	OpCost    *handler.OperatingCostHandler
	Stats     *handler.StatsHandler
	Rate      *handler.RateHandler
	Stability *handler.StabilityHandler
	Settings  *handler.SettingsHandler
	Pricing   *handler.PricingHandler
	Provision *handler.ProvisionHandler
	Plaza     *handler.PlazaHandler
	Credit    *handler.CreditHandler
	EmbedKyc  *handler.EmbedKycHandler
	// EmbedDev 是本地联调用的 token 签发器，仅在 plaza.dev_mode 开启时非 nil。
	EmbedDev *handler.EmbedDevHandler

	// EmbedSessions 是 iframe 嵌入会话存储（Plaza / EmbedKyc 非空时必填）。
	EmbedSessions *service.EmbedSessionStore
	// EmbedFrameOrigin 是允许嵌入本站页面的 sub2api origin（用于 CSP frame-ancestors）。
	EmbedFrameOrigin string

	// PGAvailable 报告线上库是否可用（用于 /health 与 503 降级）。
	PGAvailable func() bool
}

// NewRouter 创建并配置 gin.Engine。
func NewRouter(cfg *config.Config, authSvc *service.AuthService, h *Handlers) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	// 嵌入页的 CSP frame-ancestors（仅作用于 middleware.embedPagePaths 白名单内的路径）。
	//
	// 判据与下方嵌入路由的注册条件保持一致：嵌入页全部关闭时不挂载，否则会对未注册的
	// /embed/* 路径下发 frame-ancestors 'none'，让「功能没开」看起来像「白名单没配」。
	if h.Plaza != nil || h.EmbedKyc != nil {
		r.Use(middleware.EmbedFrameHeaders(h.EmbedFrameOrigin))
	}

	// 健康检查（免鉴权）
	r.GET("/health", func(c *gin.Context) {
		pg := "down"
		if h.PGAvailable != nil && h.PGAvailable() {
			pg = "up"
		}
		c.JSON(200, gin.H{
			"status": "ok",
			"pg":     pg,
			"sqlite": "up",
		})
	})

	v1 := r.Group("/api/v1")
	{
		// 登录（免鉴权）
		v1.POST("/auth/login", h.Auth.Login)

		// 模型广场（sub2api iframe 嵌入，独立于管理员 JWT 鉴权体系）。
		// 换会话端点必须保持免鉴权：此时用户还没有 monitor 会话，
		// 身份靠 sub2api 透传的 token 回调校验换取。
		if h.Plaza != nil {
			plaza := v1.Group("/embed/plaza")
			{
				plaza.POST("/session", h.Plaza.CreateSession)

				authed := plaza.Group("")
				authed.Use(middleware.EmbedSession(h.EmbedSessions))
				authed.GET("/models", h.Plaza.Models)
			}
		}

		// KYC 自助（同上的嵌入身份体系）。
		// 写接口的客户身份只从会话上下文取，请求体里没有 user_id 字段。
		if h.EmbedKyc != nil {
			kyc := v1.Group("/embed/kyc")
			{
				kyc.POST("/session", h.EmbedKyc.CreateSession)

				authed := kyc.Group("")
				authed.Use(middleware.EmbedSession(h.EmbedSessions))
				authed.GET("/profile", h.EmbedKyc.GetProfile)
				authed.PUT("/profile", h.EmbedKyc.SaveProfile)
			}
		}

		// 本地联调：自签 sub2api 用户 token，供 /embed/_dev 调试页拼装嵌入 URL。
		// 仅在 plaza.dev_mode 开启时装配（生产为 nil，整段不注册）。
		if h.EmbedDev != nil {
			v1.GET("/embed/_dev/token", h.EmbedDev.IssueToken)
		}

		// 需鉴权路由
		auth := v1.Group("")
		auth.Use(middleware.Auth(authSvc))
		{
			auth.GET("/auth/me", h.Auth.Me)

			if h.Dashboard != nil {
				auth.GET("/dashboard/summary", h.Dashboard.Summary)
				auth.GET("/dashboard/trend", h.Dashboard.Trend)
			}

			if h.Provider != nil {
				auth.GET("/providers", h.Provider.List)
				auth.POST("/providers", h.Provider.Create)
				auth.PUT("/providers/:id", h.Provider.Update)
				auth.DELETE("/providers/:id", h.Provider.Delete)
				auth.POST("/providers/test-connection", h.Provider.TestConnection)
				auth.GET("/providers/scan", h.Provider.Scan)
				auth.GET("/providers/scan-urls", h.Provider.ScanURLs)
				auth.POST("/providers/import", h.Provider.Import)
				auth.GET("/providers/:id/accounts", h.Provider.Accounts)
				auth.GET("/providers/:id/links", h.Provider.ListLinks)
				auth.PUT("/providers/:id/links", h.Provider.SaveLinks)
				auth.POST("/providers/balance/refresh-all", h.Provider.RefreshAllBalance)
				auth.POST("/providers/:id/balance/refresh", h.Provider.RefreshBalance)
				auth.PUT("/providers/:id/balance", h.Provider.ManualBalance)
				auth.GET("/providers/:id/balance/history", h.Provider.BalanceHistory)
				auth.GET("/providers/:id/costs", h.Provider.KeyCosts)
				auth.POST("/providers/:id/costs/sync", h.Provider.SyncCost)
			}

			// 自营站运营成本（买号/订阅/服务器等手工记账）
			if h.OpCost != nil {
				auth.GET("/providers/:id/operating-costs", h.OpCost.List)
				auth.POST("/providers/:id/operating-costs", h.OpCost.Create)
				auth.DELETE("/operating-costs/:id", h.OpCost.Delete)
			}

			if h.Stats != nil {
				auth.GET("/stats/providers", h.Stats.ByProvider)
				auth.GET("/stats/groups", h.Stats.ByGroup)
			}

			if h.Rate != nil {
				auth.GET("/rates/history", h.Rate.History)
				auth.GET("/rates/current", h.Rate.Current)
			}

			if h.Stability != nil {
				auth.GET("/stability/passive", h.Stability.Passive)
				auth.GET("/stability/probes", h.Stability.Probes)
				auth.GET("/stability/probes/summary", h.Stability.ProbeSummary)
				auth.GET("/stability/probes/trend", h.Stability.ProbeTrend)
				auth.POST("/stability/probe/run", h.Stability.RunProbe)
				auth.GET("/stability/health", h.Stability.HealthStates)
				auth.GET("/stability/health/events", h.Stability.HealthEvents)
				auth.PUT("/stability/health/:id/disabled", h.Stability.SetHealthDisabled)
			}

			if h.Settings != nil {
				auth.GET("/settings/strategy", h.Settings.GetStrategy)
				auth.PUT("/settings/strategy", h.Settings.SaveStrategy)
				auth.GET("/settings/notify", h.Settings.GetChannels)
				auth.PUT("/settings/notify", h.Settings.SaveChannels)
				auth.POST("/settings/notify/test", h.Settings.TestChannel)
			}

			if h.Pricing != nil {
				auth.GET("/pricing/self", h.Pricing.GetSelf)
				auth.PUT("/pricing/self", h.Pricing.SaveSelf)
				auth.GET("/pricing/local-groups", h.Pricing.LocalGroups)
				auth.GET("/pricing/rules", h.Pricing.Preview)
				auth.POST("/pricing/rules", h.Pricing.SaveRule)
				auth.DELETE("/pricing/rules/:id", h.Pricing.DeleteRule)
				auth.POST("/pricing/rules/:id/apply", h.Pricing.ApplyRule)
				auth.POST("/pricing/rules/:id/resolve-conflict", h.Pricing.ResolveConflict)
				auth.GET("/pricing/rules/:id/actions", h.Pricing.Actions)
				auth.GET("/pricing/mapped", h.Pricing.MappedUpstreams)
			}

			if h.Provision != nil {
				auth.GET("/provision/connections", h.Provision.List)
				auth.GET("/provision/connected", h.Provision.Connected)
				auth.POST("/provision/connect", h.Provision.Connect)
				auth.POST("/provision/bind", h.Provision.Bind)
				auth.DELETE("/provision/connections/:id", h.Provision.Disconnect)
			}

			// 授信台账：本地记账 + 人工执行，系统永不写上游（见 012_credit_kyc.sql）
			if h.Credit != nil {
				// 建档下拉：读线上 users 表（PG 不可用时 503，前端降级为手填）
				auth.GET("/credit/sub2api-users", h.Credit.Sub2apiUsers)
				auth.GET("/credit/summary", h.Credit.Summary)
				auth.POST("/credit/recalc", h.Credit.RecalcAll)
				auth.GET("/credit/customers", h.Credit.ListCustomers)
				auth.POST("/credit/customers", h.Credit.CreateCustomer)
				auth.GET("/credit/customers/:id", h.Credit.GetCustomer)
				auth.PUT("/credit/customers/:id", h.Credit.UpdateCustomer)
				auth.DELETE("/credit/customers/:id", h.Credit.ArchiveCustomer)
				auth.POST("/credit/customers/:id/recalc", h.Credit.Recalc)
				auth.GET("/credit/customers/:id/ledger", h.Credit.ListLedger)
				auth.POST("/credit/customers/:id/ledger", h.Credit.AppendEntry)
				auth.POST("/credit/ledger/:id/reverse", h.Credit.ReverseEntry)
				// KYC：身份资料录入与审核（PII 加密落库，见 credit_kyc.go）
				auth.GET("/credit/customers/:id/kyc", h.Credit.GetKYC)
				auth.PUT("/credit/customers/:id/kyc", h.Credit.SaveKYC)
				auth.POST("/credit/customers/:id/kyc/review", h.Credit.ReviewKYC)
			}
		}
	}

	// 嵌入前端（embed 构建时生效）
	r.Use(web.Middleware())

	// 未匹配 API 返回 404 JSON
	r.NoRoute(func(c *gin.Context) {
		if len(c.Request.URL.Path) >= 5 && c.Request.URL.Path[:5] == "/api/" {
			response.NotFound(c, "接口不存在")
			return
		}
		response.NotFound(c, "资源不存在")
	})

	return r
}

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"sub2api-account-monitor/internal/pkg/keyidentity"
	"sub2api-account-monitor/internal/repository"
)

// 面向调用方的错误。handler 层据此选择 HTTP 状态码与 i18n key。
var (
	ErrMediaKeyNotFound   = errors.New("media: key not found")
	ErrMediaTooManyActive = errors.New("media: too many active video tasks")
	ErrMediaRateLimited   = errors.New("media: rate limited")
	ErrMediaNotReady      = errors.New("media: task not ready")
)

// MediaService 生图 / 生视频编排。
//
// 【职责边界】校验 → 取 key → 落 pending → 打上游 → 更新终态。
// 明文 key 只在本类型的方法栈内存在，绝不返回给 handler。
type MediaService struct {
	pg      *repository.PG
	tasks   *repository.MediaTaskRepo
	gateway *MediaGateway

	maxPendingVideos int

	// limiter 提交频率限制。单二进制单实例，内存计数足够，不引 Redis
	// （与 EmbedSessionStore 同一取舍）。
	limiter *mediaRateLimiter
}

// NewMediaService 创建 MediaService。maxPendingVideos <= 0 时回退 3。
func NewMediaService(pg *repository.PG, tasks *repository.MediaTaskRepo,
	gateway *MediaGateway, maxPendingVideos int) *MediaService {
	if maxPendingVideos <= 0 {
		maxPendingVideos = 3
	}
	return &MediaService{
		pg:               pg,
		tasks:            tasks,
		gateway:          gateway,
		maxPendingVideos: maxPendingVideos,
		limiter:          newMediaRateLimiter(),
	}
}

// MediaKeyView 一把 key 的客户侧视图。
//
// 【绝无明文 key 字段】掩码在 service 层就完成，让「handler 忘了掩码」
// 这种失误在类型层面不可能发生。
type MediaKeyView struct {
	ID           int64              `json:"id"`
	Name         string             `json:"name"`
	MaskedKey    string             `json:"masked_key"`
	GroupName    string             `json:"group_name"`
	Platform     string             `json:"platform"`
	ImageModels  []MediaModelOption `json:"image_models"`
	VideoModels  []MediaModelOption `json:"video_models"`
	VideoPricing map[string]int64   `json:"video_pricing"` // 分辨率 → 每秒 ticks
}

// ListKeys 返回用户可用的 key 及每把 key 的生成能力。
func (s *MediaService) ListKeys(ctx context.Context, userID string) ([]MediaKeyView, error) {
	keys, err := s.pg.ListUserKeys(ctx, userID)
	if err != nil {
		return nil, err
	}

	out := make([]MediaKeyView, 0, len(keys))
	for _, k := range keys {
		opts := ClassifyModels(k.Platform, k.AllowImage, k.Models)
		view := MediaKeyView{
			ID:           k.ID,
			Name:         k.Name,
			MaskedKey:    maskAPIKey(k.Key),
			GroupName:    k.GroupName,
			Platform:     k.Platform,
			ImageModels:  make([]MediaModelOption, 0, len(opts)),
			VideoModels:  make([]MediaModelOption, 0, 1),
			VideoPricing: videoPricePerSecondTicks,
		}
		for _, o := range opts {
			if o.Capability == MediaCapVideo {
				view.VideoModels = append(view.VideoModels, o)
			} else {
				view.ImageModels = append(view.ImageModels, o)
			}
		}
		// 没有任何生成能力的 key 不展示：选中后只会收到 403/404
		if len(view.ImageModels) == 0 && len(view.VideoModels) == 0 {
			continue
		}
		out = append(out, view)
	}
	return out, nil
}

// MediaSubmitRequest 一次生成提交。
//
// 【没有 UserID 字段】用户身份只从嵌入会话取。请求体里连字段都不定义，
// 让「传了也没用」在类型层面成立（与 embed_kyc_handler 同一纪律）。
type MediaSubmitRequest struct {
	KeyID           int64
	Params          MediaGenerateParams
	ClientRequestID string
	Files           []MediaUploadFile // 仅图生图
}

// MediaSubmitResult 一次提交的结果。
//
// InlineImages 与 Task 分开返回，是因为二者生命周期不同：任务元数据落库长期可查，
// 而图片字节只在本次响应里存在一次（用户选择的「即用即弃」）。把图片塞进
// MediaTask 会让「这个字段有时有值有时没有」变成隐式约定。
type MediaSubmitResult struct {
	Task *repository.MediaTask
	// InlineImages 是 data URI 列表，前端直接塞进 <img src> 渲染。
	//
	// 【为什么不落库也不走代理】图片走 b64 从网关取回（xAI CDN 直链国内不可达），
	// 拿到的是字节而非链接。按「只存元数据」的取舍，字节随本次响应回给前端后即丢弃，
	// 刷新页面后历史图片不可见 —— 这是明确接受的代价。
	InlineImages []string
}

// Submit 提交一次生成。图片同步完成并随响应返回产物，视频落 pending 后由轮询推进。
func (s *MediaService) Submit(ctx context.Context, userID string, req MediaSubmitRequest) (*MediaSubmitResult, error) {
	if err := ValidateGenerateParams(req.Params); err != nil {
		return nil, err
	}
	if req.ClientRequestID == "" {
		return nil, fmt.Errorf("缺少幂等键")
	}
	if !s.limiter.allow(userID) {
		return nil, ErrMediaRateLimited
	}

	key, err := s.findKey(ctx, userID, req.KeyID)
	if err != nil {
		return nil, err
	}

	isVideo := req.Params.Kind == MediaKindText2Video || req.Params.Kind == MediaKindImage2Video
	if isVideo {
		n, err := s.tasks.CountPendingVideos(ctx, userID)
		if err != nil {
			return nil, err
		}
		if n >= s.maxPendingVideos {
			return nil, ErrMediaTooManyActive
		}
	}

	paramsJSON, _ := json.Marshal(req.Params)

	// 【先落库再打上游】视频提交成功即扣费。若顺序反过来，落库失败就产生了
	// 一笔查无实据的支出。反过来只是多一条 failed 记录，可对账。
	task, reused, err := s.tasks.Create(ctx, repository.MediaTaskParams{
		Sub2apiUserID:   userID,
		APIKeyID:        key.ID,
		KeyFingerprint:  keyidentity.Fingerprint(key.Key),
		GroupID:         key.GroupID,
		TaskKind:        req.Params.Kind,
		Model:           req.Params.Model,
		Prompt:          req.Params.Prompt,
		ParamsJSON:      string(paramsJSON),
		EstCostTicks:    EstimateCostTicks(req.Params),
		ClientRequestID: req.ClientRequestID,
	})
	if err != nil {
		return nil, err
	}
	// 幂等命中：直接返回既有任务，绝不重复调用上游（会二次扣费）。
	// 图片字节是即用即弃的，重复提交拿不回上一次的图 —— 但也不该为此再扣一次钱。
	if reused {
		return &MediaSubmitResult{Task: task}, nil
	}

	if isVideo {
		return s.submitVideo(ctx, key.Key, task, req.Params)
	}
	return s.submitImage(ctx, key.Key, task, req)
}

// submitImage 同步生成图片并落终态，产物字节随本次响应返回。
func (s *MediaService) submitImage(ctx context.Context, apiKey string,
	task *repository.MediaTask, req MediaSubmitRequest) (*MediaSubmitResult, error) {
	var (
		results []ImageResult
		err     error
	)
	if req.Params.Kind == MediaKindImage2Image {
		results, err = s.gateway.EditImage(ctx, apiKey, req.Params, req.Files)
	} else {
		results, err = s.gateway.GenerateImage(ctx, apiKey, req.Params)
	}
	if err != nil {
		s.failTask(ctx, task.ID, err)
		return nil, err
	}

	// 【不落产物，只落元数据】图片字节随本次响应回给前端后即丢弃。
	// result_url 留空：上游 CDN 直链国内不可达，存了也打不开，存空值让
	// has_content 恒为 false，前端不会给出一个必然 404 的预览入口。
	if err := s.tasks.MarkSucceeded(ctx, task.ID, "", results[0].CostTicks); err != nil {
		return nil, err
	}
	saved, err := s.tasks.GetByID(ctx, task.ID)
	if err != nil {
		return nil, err
	}

	images := make([]string, 0, len(results))
	for _, r := range results {
		if r.B64 == "" {
			continue
		}
		images = append(images, "data:"+r.MimeType+";base64,"+r.B64)
	}
	return &MediaSubmitResult{Task: saved, InlineImages: images}, nil
}

// submitVideo 提交视频任务并记录 request_id。
func (s *MediaService) submitVideo(ctx context.Context, apiKey string,
	task *repository.MediaTask, p MediaGenerateParams) (*MediaSubmitResult, error) {
	requestID, err := s.gateway.SubmitVideo(ctx, apiKey, p)
	if err != nil {
		s.failTask(ctx, task.ID, err)
		return nil, err
	}
	// 【必须立刻落库】钱已经扣了，request_id 是找回产物的唯一凭据。
	// 这一步失败要大声告警：任务已计费但无法追踪。
	if err := s.tasks.SetUpstreamRequestID(ctx, task.ID, requestID); err != nil {
		log.Printf("[media] 严重：任务 %d 已提交上游（已计费）但 request_id 落库失败: %v", task.ID, err)
		return nil, err
	}
	saved, err := s.tasks.GetByID(ctx, task.ID)
	if err != nil {
		return nil, err
	}
	return &MediaSubmitResult{Task: saved}, nil
}

// ListTasks 返回用户任务列表，并顺手刷新进行中的视频任务。
//
// 【为什么用被动刷新而非后台轮询】后台 goroutine 需要常驻、需要重启恢复、
// 需要处理「用户已经不看了但任务还在跑」的资源浪费。而任务状态只有用户在看
// 列表时才有意义——把刷新挂在列表查询上，零常驻成本，且用户关页面期间的
// 状态变化会在下次打开时自然补齐。
//
// 单次最多刷新 maxRefreshPerList 个，避免一个用户堆积大量任务把列表请求拖垮。
func (s *MediaService) ListTasks(ctx context.Context, userID string, limit int) ([]repository.MediaTask, error) {
	list, err := s.tasks.ListByUser(ctx, userID, limit)
	if err != nil {
		return nil, err
	}

	const maxRefreshPerList = 5
	refreshed := 0
	for i := range list {
		if refreshed >= maxRefreshPerList {
			break
		}
		t := &list[i]
		if t.Status != repository.MediaStatusPending || t.UpstreamRequestID == "" {
			continue
		}
		refreshed++
		if updated := s.refreshVideoTask(ctx, userID, t); updated != nil {
			list[i] = *updated
		}
	}
	return list, nil
}

// refreshVideoTask 查询单个视频任务的上游状态并落库。失败时返回 nil（保留原状态）。
func (s *MediaService) refreshVideoTask(ctx context.Context, userID string,
	t *repository.MediaTask) *repository.MediaTask {
	key, err := s.findKey(ctx, userID, t.APIKeyID)
	if err != nil {
		return nil
	}

	st, err := s.gateway.QueryVideo(ctx, key.Key, t.UpstreamRequestID)
	if err != nil {
		// 【400 是终态失败，不是可重试错误】上游内容审核拒绝会返回 400，
		// 此时任务永远不会成功，继续轮询只是浪费。而且费用已经扣了。
		var ge *MediaGatewayError
		if errors.As(err, &ge) && ge.StatusCode >= 400 && ge.StatusCode < 500 {
			s.failTask(ctx, t.ID, err)
			if updated, e := s.tasks.GetByID(ctx, t.ID); e == nil {
				return updated
			}
		}
		// 5xx / 网络错误是暂时的，保持 pending 等下次刷新
		return nil
	}

	if st.Done {
		// 视频产物不存 URL：上游返回的是相对路径且需要认证，
		// 统一走本站代理端点，用 upstream_request_id 定位即可。
		if err := s.tasks.MarkSucceeded(ctx, t.ID, "", st.CostTicks); err != nil {
			return nil
		}
	} else if st.Progress != t.Progress {
		if err := s.tasks.UpdateProgress(ctx, t.ID, st.Progress); err != nil {
			return nil
		}
	} else {
		return nil // 无变化，省一次回读
	}

	updated, err := s.tasks.GetByID(ctx, t.ID)
	if err != nil {
		return nil
	}
	return updated
}

// MediaContent 一份待转发的产物流。
type MediaContent struct {
	Body        io.ReadCloser
	ContentType string
}

// OpenContent 打开任务产物流，调用方负责 Close。
//
// 【只服务视频】图片走 b64 随提交响应一次性返回，不落库也不代理
// （xAI CDN 直链国内不可达，代理进程同样在国内，拉不到）。因此只有
// 视频任务的 has_content 会为 true。
//
// 归属校验在 repo 层的 SQL 里完成（GetOwned），越权访问返回 ErrNotFound。
func (s *MediaService) OpenContent(ctx context.Context, userID string, taskID int64) (*MediaContent, error) {
	task, err := s.tasks.GetOwned(ctx, taskID, userID)
	if err != nil {
		return nil, err
	}
	if task.Status != repository.MediaStatusSucceeded || task.UpstreamRequestID == "" {
		return nil, ErrMediaNotReady
	}

	key, err := s.findKey(ctx, userID, task.APIKeyID)
	if err != nil {
		return nil, err
	}

	// 视频产物端点强制要求 Authorization 头，浏览器的 <video src> 带不了，
	// 只能由后端带 key 取流再转发。实测该端点走网关（1.8s 可达），不是 CDN。
	body, ct, err := s.gateway.OpenVideoContent(ctx, key.Key, task.UpstreamRequestID)
	if err != nil {
		return nil, err
	}
	return &MediaContent{Body: body, ContentType: ct}, nil
}

// findKey 按 keyID 取该用户的 key（含明文，仅供内部使用）。
//
// 每次调用都重新查 PG 而不缓存：key 可能被用户随时禁用或删除，
// 缓存会让已撤销的 key 继续可用。查询走 api_keys 主键索引，成本可接受。
func (s *MediaService) findKey(ctx context.Context, userID string, keyID int64) (*repository.PGUserKey, error) {
	keys, err := s.pg.ListUserKeys(ctx, userID)
	if err != nil {
		return nil, err
	}
	for i := range keys {
		if keys[i].ID == keyID {
			return &keys[i], nil
		}
	}
	return nil, ErrMediaKeyNotFound
}

// failTask 标记任务失败，错误信息已由 gateway 层脱敏。
func (s *MediaService) failTask(ctx context.Context, id int64, cause error) {
	msg := cause.Error()
	if len(msg) > 500 {
		msg = msg[:500]
	}
	if err := s.tasks.MarkFailed(ctx, id, msg); err != nil {
		log.Printf("[media] 任务 %d 标记失败时出错: %v", id, err)
	}
}

// Cleanup 按保留天数清理历史任务（由每日调度调用）。
func (s *MediaService) Cleanup(ctx context.Context, retentionDays int) {
	if retentionDays <= 0 {
		return
	}
	before := time.Now().UTC().AddDate(0, 0, -retentionDays).Format("2006-01-02T15:04:05Z")
	n, err := s.tasks.Cleanup(ctx, before)
	if err != nil {
		log.Printf("[cleanup] media_tasks 清理失败: %v", err)
		return
	}
	if n > 0 {
		log.Printf("[cleanup] media_tasks 删除 %d 行", n)
	}
}

// maskAPIKey 掩码展示：保留 sk- 前缀与末 4 位。
//
// 掩码而非完全隐藏：用户有多把 key 时需要靠尾号分辨是哪一把。
func maskAPIKey(key string) string {
	if len(key) <= 7 {
		return "sk-****"
	}
	return "sk-..." + key[len(key)-4:]
}

// mediaSubmitInterval 同一用户两次提交的最小间隔。
//
// 防的是「狂点按钮」而非恶意刷量——幂等键已经挡住了重复提交同一请求，
// 这里挡的是快速提交多个不同请求。取 2 秒：正常人填完表单不会比这更快。
const mediaSubmitInterval = 2 * time.Second

// mediaRateLimiter 按用户的最小提交间隔限制。
type mediaRateLimiter struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func newMediaRateLimiter() *mediaRateLimiter {
	return &mediaRateLimiter{last: make(map[string]time.Time)}
}

// allow 判断是否放行，并在放行时记录本次时间。
//
// map 不做清理：嵌入页用户量级有限，且每个条目只有一个 time.Time。
// 真正的增长风险来自伪造 user_id，但那需要先通过 JWT 验签。
func (l *mediaRateLimiter) allow(userID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if prev, ok := l.last[userID]; ok && now.Sub(prev) < mediaSubmitInterval {
		return false
	}
	l.last[userID] = now
	return true
}

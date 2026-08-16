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
	"sub2api-account-monitor/internal/pkg/objectstore"
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
// 【职责边界】校验 → 取 key → 落 pending → 打上游 → 更新终态 → 转存产物。
// 明文 key 只在本类型的方法栈内存在，绝不返回给 handler。
type MediaService struct {
	pg        *repository.PG
	tasks     *repository.MediaTaskRepo
	artifacts *repository.MediaArtifactRepo
	gateway   *MediaGateway
	archiver  *mediaArchiver

	maxPendingVideos int

	// limiter 提交频率限制。单二进制单实例，内存计数足够，不引 Redis
	// （与 EmbedSessionStore 同一取舍）。
	limiter *mediaRateLimiter
}

// NewMediaService 创建 MediaService。maxPendingVideos <= 0 时回退 3。
//
// uploader 为 nil 表示未配置对象存储：产物不转存，退化到「图片 inline、
// 视频经后端代理」的行为，生成本身不受影响。
func NewMediaService(pg *repository.PG, tasks *repository.MediaTaskRepo,
	artifacts *repository.MediaArtifactRepo, gateway *MediaGateway,
	uploader objectstore.Uploader, maxPendingVideos int) *MediaService {
	if maxPendingVideos <= 0 {
		maxPendingVideos = 3
	}
	return &MediaService{
		pg:               pg,
		tasks:            tasks,
		artifacts:        artifacts,
		gateway:          gateway,
		archiver:         newMediaArchiver(tasks, artifacts, gateway, uploader),
		maxPendingVideos: maxPendingVideos,
		limiter:          newMediaRateLimiter(),
	}
}

// ResumePendingArchives 补投进程重启时遗留的在途转存（由启动流程调用）。
func (s *MediaService) ResumePendingArchives(ctx context.Context) {
	s.archiver.ResumePending(ctx, func(t repository.MediaTask) string {
		key, err := s.findKey(ctx, t.Sub2apiUserID, t.APIKeyID)
		if err != nil {
			return ""
		}
		return key.Key
	})
}

// WaitArchives 等待在途转存结束（优雅关闭用）。
func (s *MediaService) WaitArchives() { s.archiver.Wait() }

// MediaKeyView 一把 key 的客户侧视图。
//
// 【绝无明文 key 字段】掩码在 service 层就完成，让「handler 忘了掩码」
// 这种失误在类型层面不可能发生。
type MediaKeyView struct {
	ID          int64              `json:"id"`
	Name        string             `json:"name"`
	MaskedKey   string             `json:"masked_key"`
	GroupName   string             `json:"group_name"`
	Platform    string             `json:"platform"`
	ImageModels []MediaModelOption `json:"image_models"`
	VideoModels []MediaModelOption `json:"video_models"`
	// PricingKnown 为 false 表示本站拿不到该分组的定价参数，
	// 页面上的预估只能标注「以账单为准」而不能当成承诺。
	PricingKnown bool `json:"pricing_known"`
}

// ListKeys 返回用户可用的 key 及每把 key 的生成能力。
//
// 每把 key 都会查一次所属分组的计费参数，把**折算后的最终单价**（含分组自定义
// 单价与倍率）下发给前端。前端只做「单价 × 数量」，定价的全部复杂度留在这里——
// 页面报价算错过一次，根因正是倍率与分组自定义价散落在两端各算一半。
func (s *MediaService) ListKeys(ctx context.Context, userID string) ([]MediaKeyView, error) {
	keys, err := s.pg.ListUserKeys(ctx, userID)
	if err != nil {
		return nil, err
	}

	// 同一分组的多把 key 复用一次查询：用户常有多把 key 绑同一个分组。
	pricingCache := make(map[int64]*repository.MediaPricing, len(keys))

	out := make([]MediaKeyView, 0, len(keys))
	for _, k := range keys {
		pricing, cached := pricingCache[k.GroupID]
		if !cached {
			// 查不到定价不该让整个页面空白：退化到标准价（不含倍率），
			// 并由 PricingKnown=false 让 UI 标注这是参考值。
			pricing, err = s.pg.GetMediaPricing(ctx, k.GroupID, userID)
			if err != nil {
				log.Printf("[media] 分组 %d 定价查询失败，退化到标准价: %v", k.GroupID, err)
				pricing = nil
			}
			pricingCache[k.GroupID] = pricing
		}

		opts := ClassifyModels(k.Platform, k.AllowImage, k.Models, pricing)
		view := MediaKeyView{
			ID:           k.ID,
			Name:         k.Name,
			MaskedKey:    maskAPIKey(k.Key),
			GroupName:    k.GroupName,
			Platform:     k.Platform,
			ImageModels:  make([]MediaModelOption, 0, len(opts)),
			VideoModels:  make([]MediaModelOption, 0, 2),
			PricingKnown: pricing != nil,
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
// 产物与 Task 分开返回，是因为二者生命周期不同：任务元数据落库长期可查，
// 而产物的呈现方式取决于转存是否成功。把它们塞进 MediaTask 会让「这个字段
// 有时有值有时没有」变成隐式约定。
type MediaSubmitResult struct {
	Task *repository.MediaTask
	// Artifacts 已转存的产物（对象存储 URL，长期有效）。
	Artifacts []repository.MediaArtifact
	// InlineImages 仅在转存未启用或失败时有值：data URI 列表，
	// 前端直接塞进 <img src> 渲染，刷新页面即丢失。
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

	// 预估必须与 ListKeys 下发给前端的口径同源，否则列表里的 est_cost
	// 会和表单里刚看到的数字对不上。查询失败退化到标准价而非中断提交——
	// 预估只是展示，不该因为一次读库失败而挡住用户的生成。
	pricing, err := s.pg.GetMediaPricing(ctx, key.GroupID, userID)
	if err != nil {
		log.Printf("[media] 分组 %d 定价查询失败，预估退化到标准价: %v", key.GroupID, err)
		pricing = nil
	}

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
		EstCostTicks:    EstimateCostTicks(req.Params, pricing),
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

// submitImage 同步生成图片并落终态，产物转存到对象存储。
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

	resultURL := ""
	for _, result := range results {
		if result.URL != "" {
			resultURL = result.URL
			break
		}
	}
	if err := s.tasks.MarkSucceeded(ctx, task.ID, resultURL, results[0].CostTicks); err != nil {
		return nil, err
	}

	// 【转存放在标记成功之后】转存失败不该让一次已扣费、产物已在手的生成
	// 变成失败记录。全部成功时前端用 R2 URL（刷新后仍可见），否则仍旧
	// inline 返回字节，本次会话内照常显示。
	archived := s.archiver.ArchiveImages(ctx, task.ID, results)

	saved, err := s.tasks.GetByID(ctx, task.ID)
	if err != nil {
		return nil, err
	}

	res := &MediaSubmitResult{Task: saved}
	if !archived {
		images := make([]string, 0, len(results))
		for _, r := range results {
			if r.B64 == "" {
				continue
			}
			images = append(images, "data:"+r.MimeType+";base64,"+r.B64)
		}
		res.InlineImages = images
	} else if arts, e := s.artifacts.ListByTasks(ctx, []int64{task.ID}); e == nil {
		res.Artifacts = arts[task.ID]
	}
	return res, nil
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

// MediaTaskWithArtifacts 一条任务及其已转存的产物。
type MediaTaskWithArtifacts struct {
	Task      repository.MediaTask
	Artifacts []repository.MediaArtifact
}

// ListTasks 返回用户任务列表，并顺手刷新进行中的视频任务。
//
// 【为什么用被动刷新而非后台轮询】后台 goroutine 需要常驻、需要重启恢复、
// 需要处理「用户已经不看了但任务还在跑」的资源浪费。而任务状态只有用户在看
// 列表时才有意义——把刷新挂在列表查询上，零常驻成本，且用户关页面期间的
// 状态变化会在下次打开时自然补齐。
//
// 单次最多刷新 maxRefreshPerList 个，避免一个用户堆积大量任务把列表请求拖垮。
// 注意视频产物的转存是异步的：这里只负责发现「已完成」并投递，不等它传完——
// 几十 MB 的上传会把一次本该几十毫秒的列表查询拖成几十秒。
func (s *MediaService) ListTasks(ctx context.Context, userID string, limit int) ([]MediaTaskWithArtifacts, error) {
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

	// 批量查产物，避免 N+1：列表每 5 秒被前端轮询一次。
	ids := make([]int64, 0, len(list))
	for i := range list {
		ids = append(ids, list[i].ID)
	}
	byTask, err := s.artifacts.ListByTasks(ctx, ids)
	if err != nil {
		// 产物查询失败不该让整个列表 500：任务元数据本身仍然有价值，
		// 前端会退化到 inline / 代理路径。
		log.Printf("[media] 产物列表查询失败，本次不返回产物: %v", err)
		byTask = nil
	}

	out := make([]MediaTaskWithArtifacts, 0, len(list))
	for i := range list {
		out = append(out, MediaTaskWithArtifacts{
			Task:      list[i],
			Artifacts: byTask[list[i].ID],
		})
	}
	return out, nil
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
		if err := s.tasks.MarkSucceeded(ctx, t.ID, st.ResultURL, st.CostTicks); err != nil {
			return nil
		}
		// 【先置 pending 再投递】置状态失败就不投递：否则转存完成时会去更新
		// 一条状态为 '' 的记录，前端永远看不到「转存中」这个中间态，
		// 进程重启后的补扫也捞不到它。
		if s.archiver.enabled() {
			if err := s.tasks.SetStorageStatus(ctx, t.ID, repository.MediaStoragePending); err == nil {
				s.archiver.Enqueue(t.ID, key.Key, t.UpstreamRequestID)
			} else {
				log.Printf("[media] 任务 %d 转存状态置 pending 失败，跳过转存: %v", t.ID, err)
			}
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

// DeleteTask 删除当前用户自己的生成记录。
func (s *MediaService) DeleteTask(ctx context.Context, userID string, taskID int64) error {
	return s.tasks.DeleteOwned(ctx, taskID, userID)
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

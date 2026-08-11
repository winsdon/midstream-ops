package repository

import (
	"context"
	"testing"
)

// newMediaTestDB 建临时 monitor 测试库并返回任务仓储。
func newMediaTestDB(t *testing.T) *MediaTaskRepo {
	t.Helper()
	s := newTestStore(t)
	return NewMediaTaskRepo(s)
}

func mediaParams(userID, clientReqID, kind string) MediaTaskParams {
	return MediaTaskParams{
		Sub2apiUserID:   userID,
		APIKeyID:        7,
		KeyFingerprint:  "fp-abc",
		GroupID:         3,
		TaskKind:        kind,
		Model:           "grok-imagine-image",
		Prompt:          "一只小熊猫",
		ParamsJSON:      `{"n":1}`,
		EstCostTicks:    200000000,
		ClientRequestID: clientReqID,
	}
}

// 同一用户重复提交同一个 client_request_id 必须复用既有任务，而不是产生第二笔支出。
// 视频提交即扣费且不退款，重复落库等于重复计账。
func TestCreateIsIdempotentPerUser(t *testing.T) {
	r := newMediaTestDB(t)
	ctx := context.Background()

	first, reused, err := r.Create(ctx, mediaParams("u1", "req-1", MediaKindText2Video))
	if err != nil {
		t.Fatalf("首次创建失败: %v", err)
	}
	if reused {
		t.Fatal("首次创建不应判为复用")
	}

	second, reused, err := r.Create(ctx, mediaParams("u1", "req-1", MediaKindText2Video))
	if err != nil {
		t.Fatalf("重复创建失败: %v", err)
	}
	if !reused {
		t.Fatal("重复提交应判为复用")
	}
	if second.ID != first.ID {
		t.Fatalf("重复提交产生了新任务: %d != %d", second.ID, first.ID)
	}
}

// 幂等键按用户隔离：不同用户各自生成的随机 ID 相撞时，
// 后来者不能拿到别人的任务。
func TestCreateIdempotencyIsScopedToUser(t *testing.T) {
	r := newMediaTestDB(t)
	ctx := context.Background()

	a, _, err := r.Create(ctx, mediaParams("u1", "same-id", MediaKindText2Image))
	if err != nil {
		t.Fatalf("用户 u1 创建失败: %v", err)
	}
	b, reused, err := r.Create(ctx, mediaParams("u2", "same-id", MediaKindText2Image))
	if err != nil {
		t.Fatalf("用户 u2 创建失败: %v", err)
	}
	if reused {
		t.Fatal("不同用户的同名幂等键不应判为复用")
	}
	if a.ID == b.ID {
		t.Fatal("不同用户拿到了同一条任务")
	}
}

// 产物代理必须按归属查询，查不到统一 ErrNotFound —— 不区分「不存在」与
// 「不属于你」，避免通过响应差异枚举他人任务 ID。
func TestGetOwnedRejectsOtherUsers(t *testing.T) {
	r := newMediaTestDB(t)
	ctx := context.Background()

	task, _, err := r.Create(ctx, mediaParams("owner", "req-1", MediaKindText2Image))
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}

	if _, err := r.GetOwned(ctx, task.ID, "owner"); err != nil {
		t.Fatalf("属主应能读取自己的任务: %v", err)
	}
	if _, err := r.GetOwned(ctx, task.ID, "intruder"); err != ErrNotFound {
		t.Fatalf("越权读取应返回 ErrNotFound，实得: %v", err)
	}
}

// 新任务恒为 pending：终态只能由 MarkSucceeded / MarkFailed 写入。
func TestCreateStartsPending(t *testing.T) {
	r := newMediaTestDB(t)
	task, _, err := r.Create(context.Background(), mediaParams("u1", "req-1", MediaKindText2Video))
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if task.Status != MediaStatusPending {
		t.Fatalf("新任务状态应为 pending，实得 %q", task.Status)
	}
	if task.EstCostTicks != 200000000 {
		t.Fatalf("预估费用未落库: %d", task.EstCostTicks)
	}
}

// 状态流转与费用记录。
func TestMarkSucceededAndFailed(t *testing.T) {
	r := newMediaTestDB(t)
	ctx := context.Background()

	ok, _, err := r.Create(ctx, mediaParams("u1", "req-ok", MediaKindText2Image))
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if err := r.MarkSucceeded(ctx, ok.ID, "https://imgen.x.ai/x.jpeg", 200000000); err != nil {
		t.Fatalf("标记成功失败: %v", err)
	}
	got, err := r.GetByID(ctx, ok.ID)
	if err != nil {
		t.Fatalf("回读失败: %v", err)
	}
	if got.Status != MediaStatusSucceeded || got.Progress != 100 {
		t.Fatalf("终态不正确: status=%q progress=%d", got.Status, got.Progress)
	}
	if got.CostTicks != 200000000 || got.ResultURL == "" {
		t.Fatalf("实扣或产物链接未落库: cost=%d url=%q", got.CostTicks, got.ResultURL)
	}

	bad, _, err := r.Create(ctx, mediaParams("u1", "req-bad", MediaKindText2Video))
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if err := r.MarkFailed(ctx, bad.ID, "内容审核拒绝"); err != nil {
		t.Fatalf("标记失败失败: %v", err)
	}
	got, err = r.GetByID(ctx, bad.ID)
	if err != nil {
		t.Fatalf("回读失败: %v", err)
	}
	if got.Status != MediaStatusFailed || got.ErrorMessage == "" {
		t.Fatalf("失败态不正确: status=%q msg=%q", got.Status, got.ErrorMessage)
	}
}

// 并发上限只数视频：图片是同步调用，落库即终态，不存在「进行中」。
func TestCountPendingVideosIgnoresImages(t *testing.T) {
	r := newMediaTestDB(t)
	ctx := context.Background()

	if _, _, err := r.Create(ctx, mediaParams("u1", "img-1", MediaKindText2Image)); err != nil {
		t.Fatalf("创建图片任务失败: %v", err)
	}
	if _, _, err := r.Create(ctx, mediaParams("u1", "vid-1", MediaKindText2Video)); err != nil {
		t.Fatalf("创建视频任务失败: %v", err)
	}
	if _, _, err := r.Create(ctx, mediaParams("u1", "vid-2", MediaKindImage2Video)); err != nil {
		t.Fatalf("创建图生视频任务失败: %v", err)
	}
	// 其他用户的视频任务不该计入本用户配额
	if _, _, err := r.Create(ctx, mediaParams("u2", "vid-3", MediaKindText2Video)); err != nil {
		t.Fatalf("创建他人任务失败: %v", err)
	}

	n, err := r.CountPendingVideos(ctx, "u1")
	if err != nil {
		t.Fatalf("统计失败: %v", err)
	}
	if n != 2 {
		t.Fatalf("进行中视频任务数应为 2，实得 %d", n)
	}
}

// 任务列表按用户隔离。
func TestListByUserIsScoped(t *testing.T) {
	r := newMediaTestDB(t)
	ctx := context.Background()

	for _, id := range []string{"a", "b"} {
		if _, _, err := r.Create(ctx, mediaParams("u1", "req-"+id, MediaKindText2Image)); err != nil {
			t.Fatalf("创建失败: %v", err)
		}
	}
	if _, _, err := r.Create(ctx, mediaParams("u2", "req-c", MediaKindText2Image)); err != nil {
		t.Fatalf("创建失败: %v", err)
	}

	list, err := r.ListByUser(ctx, "u1", 0)
	if err != nil {
		t.Fatalf("列表查询失败: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("应只返回本用户的 2 条任务，实得 %d", len(list))
	}
	for _, task := range list {
		if task.Sub2apiUserID != "u1" {
			t.Fatalf("列表混入了他人任务: %s", task.Sub2apiUserID)
		}
	}
}

// SetUpstreamRequestID 是花钱后的关键落库动作：request_id 丢了就找不回产物。
func TestSetUpstreamRequestID(t *testing.T) {
	r := newMediaTestDB(t)
	ctx := context.Background()

	task, _, err := r.Create(ctx, mediaParams("u1", "req-1", MediaKindText2Video))
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if err := r.SetUpstreamRequestID(ctx, task.ID, "892b47b9-155b"); err != nil {
		t.Fatalf("写入 request_id 失败: %v", err)
	}
	if err := r.UpdateProgress(ctx, task.ID, 87); err != nil {
		t.Fatalf("更新进度失败: %v", err)
	}

	got, err := r.GetByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("回读失败: %v", err)
	}
	if got.UpstreamRequestID != "892b47b9-155b" || got.Progress != 87 {
		t.Fatalf("落库不正确: rid=%q progress=%d", got.UpstreamRequestID, got.Progress)
	}
}

// 更新不存在的任务须返回 ErrNotFound，让上层能回 404 而非静默成功。
func TestUpdateMissingTaskReturnsNotFound(t *testing.T) {
	r := newMediaTestDB(t)
	if err := r.MarkSucceeded(context.Background(), 99999, "", 0); err != ErrNotFound {
		t.Fatalf("应返回 ErrNotFound，实得 %v", err)
	}
}

// 分组自定义模型白名单：启用时只暴露白名单内的模型。
func TestApplyGroupModelsList(t *testing.T) {
	models := []string{"gpt-image-2", "grok-imagine-image", "grok-imagine-video"}

	cases := []struct {
		name string
		raw  string
		want int
	}{
		{"未启用时不过滤", `{"enabled":false,"models":["gpt-image-2"]}`, 3},
		{"白名单为空时不过滤", `{"enabled":true,"models":[]}`, 3},
		{"启用时按白名单过滤", `{"enabled":true,"models":["gpt-image-2","grok-imagine-video"]}`, 2},
		{"配置解析失败时退化为不过滤", `not-json`, 3},
		{"空配置不过滤", `{}`, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := applyGroupModelsList(models, []byte(tc.raw))
			if len(got) != tc.want {
				t.Fatalf("过滤结果数应为 %d，实得 %d (%v)", tc.want, len(got), got)
			}
		})
	}
}

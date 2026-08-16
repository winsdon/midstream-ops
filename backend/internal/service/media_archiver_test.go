package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"sub2api-account-monitor/internal/repository"
)

// fakeUploader 记录上传调用，可注入失败与阻塞。
type fakeUploader struct {
	mu    sync.Mutex
	calls []fakeUpload
	// failKeys 指定要失败的对象键（子串匹配）。
	failKeys []string
	// maxSeen 观测到的最大并发，用于验证信号量生效。
	inFlight int32
	maxSeen  int32
	// block 非 nil 时上传阻塞直到该 channel 关闭。
	block chan struct{}
}

type fakeUpload struct {
	Key         string
	ContentType string
	Size        int64
	Data        string
}

func (f *fakeUploader) Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) (string, error) {
	cur := atomic.AddInt32(&f.inFlight, 1)
	for {
		old := atomic.LoadInt32(&f.maxSeen)
		if cur <= old || atomic.CompareAndSwapInt32(&f.maxSeen, old, cur) {
			break
		}
	}
	defer atomic.AddInt32(&f.inFlight, -1)

	if f.block != nil {
		<-f.block
	}
	for _, bad := range f.failKeys {
		if strings.Contains(key, bad) {
			return "", fmt.Errorf("模拟上传失败")
		}
	}

	data, _ := io.ReadAll(body)
	f.mu.Lock()
	f.calls = append(f.calls, fakeUpload{Key: key, ContentType: contentType, Size: size, Data: string(data)})
	f.mu.Unlock()
	return "https://cdn.example.com/" + key, nil
}

func (f *fakeUploader) snapshot() []fakeUpload {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeUpload(nil), f.calls...)
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// newArchiveFixture 建一个连着真实测试库的转存器与一条已成功的任务。
//
// gateway 指向一个总是返回固定字节的假上游：视频转存路径要用它取产物流。
func newArchiveFixture(t *testing.T, up *fakeUploader) (*mediaArchiver, *repository.MediaTaskRepo,
	*repository.MediaArtifactRepo, int64) {
	t.Helper()
	store := newTestStore(t)
	tasks := repository.NewMediaTaskRepo(store)
	artifacts := repository.NewMediaArtifactRepo(store)

	ctx := context.Background()
	task, _, err := tasks.Create(ctx, repository.MediaTaskParams{
		Sub2apiUserID: "u1", APIKeyID: 1, GroupID: 1,
		TaskKind: repository.MediaKindText2Image, Model: "grok-imagine-image",
		Prompt: "小熊猫", ParamsJSON: "{}", ClientRequestID: "req-" + t.Name(),
	})
	if err != nil {
		t.Fatalf("建任务失败: %v", err)
	}
	if err := tasks.MarkSucceeded(ctx, task.ID, "", 200_000_000); err != nil {
		t.Fatalf("标记成功失败: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = io.WriteString(w, "MP4BYTES")
	}))
	t.Cleanup(srv.Close)

	a := newMediaArchiver(tasks, artifacts, NewMediaGateway(srv.URL), up)
	return a, tasks, artifacts, task.ID
}

// 对象键必须按月分片并带任务 ID 与序号 —— 平铺在一个前缀下时，
// 桶的生命周期规则只能全删或全留。
func TestMediaObjectKeyLayout(t *testing.T) {
	key := mediaObjectKey(42, 3, ".png")
	if !strings.HasPrefix(key, "media/") {
		t.Fatalf("对象键应在 media/ 前缀下: %s", key)
	}
	if !strings.HasSuffix(key, "/42/3.png") {
		t.Fatalf("对象键应以 /{taskID}/{index}{ext} 结尾: %s", key)
	}
	// media/2026/08/42/3.png —— 五段
	if n := len(strings.Split(key, "/")); n != 5 {
		t.Fatalf("对象键应为 media/yyyy/mm/task/index 五段，实得 %d 段: %s", n, key)
	}
}

// 扩展名影响用户下载后的体验：没有扩展名的文件在 Windows 上双击打不开。
func TestExtensionFor(t *testing.T) {
	cases := []struct {
		mime, fallback, want string
	}{
		{"image/jpeg", ".jpg", ".jpg"},
		{"image/png", ".jpg", ".png"},
		{"image/webp", ".jpg", ".webp"},
		{"video/mp4", ".mp4", ".mp4"},
		// 带参数的 Content-Type 必须能正确解析
		{"video/mp4; codecs=avc1", ".mp4", ".mp4"},
		{"IMAGE/PNG", ".jpg", ".png"},
		{"application/octet-stream", ".bin", ".bin"},
		{"", ".jpg", ".jpg"},
	}
	for _, tc := range cases {
		if got := extensionFor(tc.mime, tc.fallback); got != tc.want {
			t.Fatalf("%q 应得 %s，实得 %s", tc.mime, tc.want, got)
		}
	}
}

// countB64Images 是「是否全部转存成功」的分母，算错会让部分失败被当成全成功。
func TestCountB64Images(t *testing.T) {
	results := []ImageResult{
		{B64: b64("a")},
		{B64: ""}, // 上游只给了 URL，没有字节
		{B64: b64("c")},
	}
	if got := countB64Images(results); got != 2 {
		t.Fatalf("应为 2，实得 %d", got)
	}
	if got := countB64Images(nil); got != 0 {
		t.Fatalf("空列表应为 0，实得 %d", got)
	}
}

// 未配置对象存储时转存器整体禁用，不该有任何副作用。
//
// 【依赖不全也必须算「禁用」而不是崩溃】转存跑在后台 goroutine 里，
// 那里的 nil 解引用会带走整个进程——没有请求边界的 recover 能接住它。
func TestArchiverDisabledWithoutDependencies(t *testing.T) {
	cases := []struct {
		name string
		a    *mediaArchiver
	}{
		{"无 uploader", newMediaArchiver(nil, nil, nil, nil)},
		{"有 uploader 但无 repo", newMediaArchiver(nil, nil, nil, &fakeUploader{})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.a.enabled() {
				t.Fatal("依赖不全时应报告禁用")
			}
			if tc.a.ArchiveImages(context.Background(), 1, []ImageResult{{B64: b64("x")}}) {
				t.Fatal("禁用时 ArchiveImages 应返回 false")
			}
			// 【关键】投递不该 panic —— 这正是把 enabled() 从只查 uploader
			// 扩到查全部依赖的原因
			tc.a.Enqueue(1, "sk-x", "req-1")
			tc.a.Wait()
		})
	}
}

// 依赖齐备但缺 gateway 时，图片转存仍可用、视频转存禁用。
func TestArchiverVideoNeedsGateway(t *testing.T) {
	store := newTestStore(t)
	a := newMediaArchiver(repository.NewMediaTaskRepo(store),
		repository.NewMediaArtifactRepo(store), nil, &fakeUploader{})

	if !a.enabled() {
		t.Fatal("图片转存的依赖已齐备，应报告启用")
	}
	if a.videoArchivable() {
		t.Fatal("缺 gateway 时视频转存应禁用")
	}
	a.Enqueue(1, "sk-x", "req-1") // 不该 panic
	a.Wait()
}

// 同一任务不得被重复投递。
//
// 【为什么这条重要】任务列表每 5 秒被前端轮询一次，而 storage_status 在转存
// 完成前一直是 pending。不去重的话同一个几十 MB 的视频会被重复下载上传好几遍，
// 既浪费带宽也可能把并发槽占满。
func TestArchiverDeduplicatesInflight(t *testing.T) {
	up := &fakeUploader{block: make(chan struct{})}
	a, _, _, taskID := newArchiveFixture(t, up)

	for i := 0; i < 5; i++ {
		a.Enqueue(taskID, "sk-x", "req-1")
	}

	var inflight int
	a.inflight.Range(func(_, _ any) bool { inflight++; return true })
	if inflight != 1 {
		t.Fatalf("同一任务应只有 1 条在途记录，实得 %d", inflight)
	}

	close(up.block)
	a.Wait()

	// 完成后在途记录必须清空，否则该任务永远无法被重试
	inflight = 0
	a.inflight.Range(func(_, _ any) bool { inflight++; return true })
	if inflight != 0 {
		t.Fatalf("转存结束后应清空在途记录，实得 %d", inflight)
	}
}

// 视频转存端到端：取上游流 → 传对象存储 → 落产物 → 置 stored。
func TestArchiveVideoEndToEnd(t *testing.T) {
	up := &fakeUploader{}
	a, tasks, artifacts, taskID := newArchiveFixture(t, up)
	ctx := context.Background()

	if err := tasks.SetStorageStatus(ctx, taskID, repository.MediaStoragePending); err != nil {
		t.Fatalf("置 pending 失败: %v", err)
	}
	a.Enqueue(taskID, "sk-test", "req-video")
	a.Wait()

	calls := up.snapshot()
	if len(calls) != 1 {
		t.Fatalf("应上传 1 个视频，实得 %d", len(calls))
	}
	if calls[0].Data != "MP4BYTES" {
		t.Fatalf("上传内容错误: %q", calls[0].Data)
	}
	if !strings.HasSuffix(calls[0].Key, "/0.mp4") {
		t.Fatalf("视频扩展名应随 Content-Type: %s", calls[0].Key)
	}
	if calls[0].ContentType != "video/mp4" {
		t.Fatalf("Content-Type 未透传: %s", calls[0].ContentType)
	}

	task, _ := tasks.GetByID(ctx, taskID)
	if task.StorageStatus != repository.MediaStorageStored {
		t.Fatalf("转存成功后应置 stored，实得 %q", task.StorageStatus)
	}
	saved, _ := artifacts.ListByTasks(ctx, []int64{taskID})
	if len(saved[taskID]) != 1 {
		t.Fatalf("应落 1 条产物记录，实得 %d", len(saved[taskID]))
	}
}

// 视频转存失败时状态置 failed —— 前端据此回退到代理端点，而不是干等一个
// 永远不会变的 pending。
func TestArchiveVideoFailureMarksFailed(t *testing.T) {
	up := &fakeUploader{failKeys: []string{"media/"}}
	a, tasks, _, taskID := newArchiveFixture(t, up)
	ctx := context.Background()

	_ = tasks.SetStorageStatus(ctx, taskID, repository.MediaStoragePending)
	a.Enqueue(taskID, "sk-test", "req-video")
	a.Wait()

	task, _ := tasks.GetByID(ctx, taskID)
	if task.StorageStatus != repository.MediaStorageFailed {
		t.Fatalf("转存失败应置 failed，实得 %q", task.StorageStatus)
	}
}

// 并发上限必须生效：不限并发时十个视频同时完成就是二十条长连接。
//
// 【用真实任务而不是随手编的 ID】编的 ID 会在落产物时被外键拒绝，转存在上传
// 之后就失败退出——峰值并发照样能测出来，但日志里全是外键报错，掩盖了这条
// 用例真正要看的东西，也让将来读日志的人以为实现有问题。
func TestArchiverRespectsConcurrencyLimit(t *testing.T) {
	up := &fakeUploader{block: make(chan struct{})}
	a, tasks, _, firstID := newArchiveFixture(t, up)
	ctx := context.Background()

	ids := []int64{firstID}
	for i := 2; i <= 8; i++ {
		task, _, err := tasks.Create(ctx, repository.MediaTaskParams{
			Sub2apiUserID: "u1", APIKeyID: 1, GroupID: 1,
			TaskKind: repository.MediaKindText2Video, Model: "grok-imagine-video",
			Prompt: "x", ParamsJSON: "{}", ClientRequestID: fmt.Sprintf("req-conc-%d", i),
		})
		if err != nil {
			t.Fatalf("建任务失败: %v", err)
		}
		if err := tasks.MarkSucceeded(ctx, task.ID, "", 0); err != nil {
			t.Fatalf("标记成功失败: %v", err)
		}
		ids = append(ids, task.ID)
	}

	for _, id := range ids {
		a.Enqueue(id, "sk-x", "req")
	}
	close(up.block) // 放行所有在途上传
	a.Wait()

	if int(up.maxSeen) > mediaArchiveConcurrency {
		t.Fatalf("并发数应不超过 %d，实测峰值 %d", mediaArchiveConcurrency, up.maxSeen)
	}
	if len(up.snapshot()) != len(ids) {
		t.Fatalf("应全部上传成功（共 %d 个），实得 %d", len(ids), len(up.snapshot()))
	}
}

// 空 request_id 不得投递：那样只会去请求一个必然 404 的端点。
func TestArchiverSkipsEmptyRequestID(t *testing.T) {
	up := &fakeUploader{}
	a, _, _, taskID := newArchiveFixture(t, up)
	a.Enqueue(taskID, "sk-x", "")
	a.Wait()
	if len(up.snapshot()) != 0 {
		t.Fatal("空 request_id 不应发起转存")
	}
}

// 图片全部转存成功：产物落库、状态置 stored，调用方据此不再返回 inline 字节。
func TestArchiveImagesAllSucceed(t *testing.T) {
	up := &fakeUploader{}
	a, tasks, artifacts, taskID := newArchiveFixture(t, up)
	ctx := context.Background()

	ok := a.ArchiveImages(ctx, taskID, []ImageResult{
		{B64: b64("IMG-A"), MimeType: "image/png"},
		{B64: b64("IMG-B"), MimeType: "image/jpeg"},
	})
	if !ok {
		t.Fatal("全部成功时应返回 true")
	}

	calls := up.snapshot()
	if len(calls) != 2 {
		t.Fatalf("应上传 2 张，实得 %d", len(calls))
	}
	if !strings.HasSuffix(calls[0].Key, "/0.png") || !strings.HasSuffix(calls[1].Key, "/1.jpg") {
		t.Fatalf("扩展名应随 MIME 类型: %s / %s", calls[0].Key, calls[1].Key)
	}
	if calls[0].Data != "IMG-A" {
		t.Fatalf("上传的应是解码后的字节，实得 %q", calls[0].Data)
	}
	// 【必须是确定长度】S3 兼容接口不接受 chunked 传输
	if calls[0].Size != int64(len("IMG-A")) {
		t.Fatalf("必须传确定长度，实得 %d", calls[0].Size)
	}

	saved, err := artifacts.ListByTasks(ctx, []int64{taskID})
	if err != nil {
		t.Fatalf("查产物失败: %v", err)
	}
	if len(saved[taskID]) != 2 {
		t.Fatalf("应落 2 条产物记录，实得 %d", len(saved[taskID]))
	}
	if saved[taskID][0].Index != 0 || saved[taskID][1].Index != 1 {
		t.Fatalf("产物应按 idx 升序返回: %+v", saved[taskID])
	}
	if !strings.HasPrefix(saved[taskID][0].URL, "https://cdn.example.com/") {
		t.Fatalf("落库的应是公开 URL: %s", saved[taskID][0].URL)
	}

	task, _ := tasks.GetByID(ctx, taskID)
	if task.StorageStatus != repository.MediaStorageStored {
		t.Fatalf("转存状态应为 stored，实得 %q", task.StorageStatus)
	}
}

// 部分失败也标 failed：前端据此保留 inline 兜底，让缺的那几张仍然可见。
//
// 【为什么不标 stored】标 stored 会让前端只显示已转存的那部分，
// 用户以为自己少生成了几张——而钱是按全部张数扣的。
func TestArchiveImagesPartialFailureKeepsInlineFallback(t *testing.T) {
	up := &fakeUploader{failKeys: []string{"/1."}} // 第二张失败
	a, tasks, artifacts, taskID := newArchiveFixture(t, up)
	ctx := context.Background()

	ok := a.ArchiveImages(ctx, taskID, []ImageResult{
		{B64: b64("IMG-A"), MimeType: "image/png"},
		{B64: b64("IMG-B"), MimeType: "image/png"},
	})
	if ok {
		t.Fatal("部分失败时应返回 false，让调用方保留 inline 兜底")
	}

	task, _ := tasks.GetByID(ctx, taskID)
	if task.StorageStatus != repository.MediaStorageFailed {
		t.Fatalf("部分失败应标 failed，实得 %q", task.StorageStatus)
	}
	// 成功的那张仍然落库——不该因为同伴失败就丢掉已经传上去的对象
	saved, _ := artifacts.ListByTasks(ctx, []int64{taskID})
	if len(saved[taskID]) != 1 {
		t.Fatalf("成功的那张应保留记录，实得 %d 条", len(saved[taskID]))
	}
}

// 全部失败：状态 failed，无产物记录。
func TestArchiveImagesAllFail(t *testing.T) {
	up := &fakeUploader{failKeys: []string{"media/"}}
	a, tasks, artifacts, taskID := newArchiveFixture(t, up)
	ctx := context.Background()

	if a.ArchiveImages(ctx, taskID, []ImageResult{{B64: b64("X"), MimeType: "image/png"}}) {
		t.Fatal("全部失败时应返回 false")
	}
	task, _ := tasks.GetByID(ctx, taskID)
	if task.StorageStatus != repository.MediaStorageFailed {
		t.Fatalf("应标 failed，实得 %q", task.StorageStatus)
	}
	saved, _ := artifacts.ListByTasks(ctx, []int64{taskID})
	if len(saved[taskID]) != 0 {
		t.Fatalf("失败时不应有产物记录，实得 %d", len(saved[taskID]))
	}
}

// 产物记录必须幂等：转存重试（重启补投、重复触发）会对同一 (task,idx) 再写一次，
// 撞唯一索引报错会让整次转存前功尽弃——而对象其实已经传上去了。
func TestArtifactSaveIsIdempotent(t *testing.T) {
	up := &fakeUploader{}
	a, _, artifacts, taskID := newArchiveFixture(t, up)
	ctx := context.Background()

	results := []ImageResult{{B64: b64("V1"), MimeType: "image/png"}}
	if !a.ArchiveImages(ctx, taskID, results) {
		t.Fatal("首次转存应成功")
	}
	// 再来一次（模拟重试）
	if !a.ArchiveImages(ctx, taskID, []ImageResult{{B64: b64("V2"), MimeType: "image/png"}}) {
		t.Fatal("重试转存应成功而不是撞唯一索引")
	}

	saved, _ := artifacts.ListByTasks(ctx, []int64{taskID})
	if len(saved[taskID]) != 1 {
		t.Fatalf("同一 (task,idx) 应只有一条记录，实得 %d", len(saved[taskID]))
	}
}

// 批量查产物：一次查询覆盖多个任务，避免列表接口的 N+1。
func TestListByTasksBatches(t *testing.T) {
	up := &fakeUploader{}
	a, tasks, artifacts, firstID := newArchiveFixture(t, up)
	ctx := context.Background()

	second, _, err := tasks.Create(ctx, repository.MediaTaskParams{
		Sub2apiUserID: "u1", APIKeyID: 1, GroupID: 1,
		TaskKind: repository.MediaKindText2Image, Model: "grok-imagine-image",
		Prompt: "x", ParamsJSON: "{}", ClientRequestID: "req-second",
	})
	if err != nil {
		t.Fatalf("建第二条任务失败: %v", err)
	}
	_ = tasks.MarkSucceeded(ctx, second.ID, "", 0)

	a.ArchiveImages(ctx, firstID, []ImageResult{{B64: b64("A"), MimeType: "image/png"}})
	a.ArchiveImages(ctx, second.ID, []ImageResult{
		{B64: b64("B"), MimeType: "image/png"},
		{B64: b64("C"), MimeType: "image/png"},
	})

	got, err := artifacts.ListByTasks(ctx, []int64{firstID, second.ID})
	if err != nil {
		t.Fatalf("批量查询失败: %v", err)
	}
	if len(got[firstID]) != 1 || len(got[second.ID]) != 2 {
		t.Fatalf("批量查询结果错误: %d / %d", len(got[firstID]), len(got[second.ID]))
	}

	// 空列表不该发查询也不该报错
	empty, err := artifacts.ListByTasks(ctx, nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("空 ID 列表应返回空 map，实得 %v / %v", empty, err)
	}
}

// 删任务时产物记录随外键级联清理——否则会留下指向已删任务的孤儿行。
func TestDeleteTaskCascadesArtifacts(t *testing.T) {
	up := &fakeUploader{}
	a, tasks, artifacts, taskID := newArchiveFixture(t, up)
	ctx := context.Background()

	a.ArchiveImages(ctx, taskID, []ImageResult{{B64: b64("X"), MimeType: "image/png"}})
	if err := tasks.DeleteOwned(ctx, taskID, "u1"); err != nil {
		t.Fatalf("删任务失败: %v", err)
	}

	got, err := artifacts.ListByTasks(ctx, []int64{taskID})
	if err != nil {
		t.Fatalf("查产物失败: %v", err)
	}
	if len(got[taskID]) != 0 {
		t.Fatalf("任务删除后产物记录应级联清理，实得 %d 条", len(got[taskID]))
	}
}

// 补扫只捞「已成功且转存未完成」的任务：其余状态捞出来也无事可做。
func TestListPendingStorageOnlyPicksStuckTasks(t *testing.T) {
	store := newTestStore(t)
	tasks := repository.NewMediaTaskRepo(store)
	ctx := context.Background()

	mk := func(reqID, status string, succeeded bool) int64 {
		task, _, err := tasks.Create(ctx, repository.MediaTaskParams{
			Sub2apiUserID: "u1", APIKeyID: 1, GroupID: 1,
			TaskKind: repository.MediaKindText2Video, Model: "grok-imagine-video",
			Prompt: "x", ParamsJSON: "{}", ClientRequestID: reqID,
		})
		if err != nil {
			t.Fatalf("建任务失败: %v", err)
		}
		if succeeded {
			_ = tasks.MarkSucceeded(ctx, task.ID, "", 0)
		}
		if status != "" {
			_ = tasks.SetStorageStatus(ctx, task.ID, status)
		}
		return task.ID
	}

	stuck := mk("r-stuck", repository.MediaStoragePending, true)
	mk("r-stored", repository.MediaStorageStored, true)
	mk("r-failed", repository.MediaStorageFailed, true)
	mk("r-none", "", true)
	// pending 但任务还没成功：产物根本不存在，捞出来只会白跑一趟
	mk("r-running", repository.MediaStoragePending, false)

	list, err := tasks.ListPendingStorage(ctx, 50)
	if err != nil {
		t.Fatalf("补扫失败: %v", err)
	}
	if len(list) != 1 || list[0].ID != stuck {
		ids := make([]int64, len(list))
		for i, t2 := range list {
			ids[i] = t2.ID
		}
		t.Fatalf("应只捞到 %d 一条，实得 %v", stuck, ids)
	}
}

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"sub2api-account-monitor/internal/repository"
)

// 上游只回 URL（没有 b64_json）且产物未转存时，inline 兜底必须把这个 URL
// 交回前端——否则用户看到的是「任务成功但页面空白」。
//
// 【线上事故回归】2026-10-20 任务 551：上游只回产物直链，旧实现 inline 兜底
// 只认 b64，结果 Submit 响应里 images 为空、artifacts 也为空，前端只能显示
// 「本次图片未能转存」。图明明就在直链上，本可以让用户当场看到。
func TestSubmitImageInlineFallbackKeepsUpstreamURL(t *testing.T) {
	store := newTestStore(t)
	tasks := repository.NewMediaTaskRepo(store)
	artifacts := repository.NewMediaArtifactRepo(store)

	const upstreamURL = "https://media.example.com/out.png"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"url": upstreamURL}},
		})
	}))
	defer srv.Close()

	// uploader 为 nil 表示未配置对象存储：转存整体禁用，走 inline 兜底。
	svc := NewMediaService(nil, tasks, artifacts, NewMediaGateway(srv.URL), nil, 0)

	ctx := context.Background()
	task, _, err := tasks.Create(ctx, repository.MediaTaskParams{
		Sub2apiUserID: "u1", APIKeyID: 1, GroupID: 1,
		TaskKind: repository.MediaKindText2Image, Model: "gpt-image-2",
		Prompt: "把机器摆正", ParamsJSON: "{}", ClientRequestID: "req-inline-url",
	})
	if err != nil {
		t.Fatalf("建任务失败: %v", err)
	}

	// 文生图与图生图共用 submitImage，产物处理逻辑同一条路径。
	res, err := svc.submitImage(ctx, "sk-test", task, MediaSubmitRequest{
		Params: MediaGenerateParams{
			Kind: repository.MediaKindText2Image, Model: "gpt-image-2",
			Prompt: "把机器摆正", N: 1, Size: "1024x1024", Quality: "high",
		},
	})
	if err != nil {
		t.Fatalf("提交失败: %v", err)
	}
	if len(res.InlineImages) != 1 || res.InlineImages[0] != upstreamURL {
		t.Fatalf("inline 兜底应返回上游直链，实得 %#v", res.InlineImages)
	}
	if len(res.Artifacts) != 0 {
		t.Fatalf("未配置对象存储时不该有产物，实得 %#v", res.Artifacts)
	}
	if res.Task.Status != repository.MediaStatusSucceeded {
		t.Fatalf("任务应标记成功，实得 %q", res.Task.Status)
	}
	if res.Task.ResultURL != upstreamURL {
		t.Fatalf("result_url 应落库，实得 %q", res.Task.ResultURL)
	}
}

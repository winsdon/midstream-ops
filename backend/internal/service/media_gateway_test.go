package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testAPIKey = "sk-test-secret-value"

// 图片生成：请求头、请求体与响应解析。
func TestGenerateImageRequestAndResponse(t *testing.T) {
	var gotAuth, gotUA, gotPath string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotUA = r.Header.Get("User-Agent")
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"b64_json":"SU1H","mime_type":"image/jpeg"}],
			"usage":{"cost_in_usd_ticks":200000000}}`)
	}))
	defer srv.Close()

	g := NewMediaGateway(srv.URL)
	got, err := g.GenerateImage(context.Background(), testAPIKey, MediaGenerateParams{
		Model: "grok-imagine-image", Prompt: "小熊猫", N: 1,
	})
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}

	if gotPath != "/v1/images/generations" {
		t.Fatalf("请求路径错误: %s", gotPath)
	}
	if gotAuth != "Bearer "+testAPIKey {
		t.Fatalf("认证头错误: %s", gotAuth)
	}
	// 供应商 sub2api 站点前置 WAF 会拦截非浏览器 UA
	if !strings.Contains(gotUA, "Mozilla") {
		t.Fatalf("必须携带浏览器 UA，实得: %s", gotUA)
	}
	if gotBody["model"] != "grok-imagine-image" || gotBody["prompt"] != "小熊猫" {
		t.Fatalf("请求体错误: %v", gotBody)
	}
	if len(got) != 1 || got[0].B64 != "SU1H" {
		t.Fatalf("响应解析错误: %v", got)
	}
	if got[0].CostTicks != 200000000 {
		t.Fatalf("实扣未解析: %d", got[0].CostTicks)
	}
}

// Grok 模型不传 size；OpenAI 格式模型传 size。
func TestGenerateImageOmitsEmptySize(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = io.WriteString(w, `{"data":[{"b64_json":"SU1H"}]}`)
	}))
	defer srv.Close()

	g := NewMediaGateway(srv.URL)

	if _, err := g.GenerateImage(context.Background(), testAPIKey, MediaGenerateParams{
		Model: "grok-imagine-image", Prompt: "x", N: 1,
	}); err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	if _, ok := gotBody["size"]; ok {
		t.Fatal("未指定尺寸时不应发送 size 字段")
	}

	if _, err := g.GenerateImage(context.Background(), testAPIKey, MediaGenerateParams{
		Model: "gpt-image-2", Prompt: "x", N: 1, Size: "2048x1152", Quality: "high",
	}); err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	if gotBody["size"] != "2048x1152" || gotBody["quality"] != "high" {
		t.Fatalf("尺寸与质量未透传: %v", gotBody)
	}
}

// 视频提交：返回 request_id。
func TestSubmitVideo(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/videos/generations" {
			t.Errorf("路径错误: %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = io.WriteString(w, `{"request_id":"892b47b9-155b-97e3"}`)
	}))
	defer srv.Close()

	g := NewMediaGateway(srv.URL)
	id, err := g.SubmitVideo(context.Background(), testAPIKey, MediaGenerateParams{
		Model: "grok-imagine-video", Prompt: "海浪", Resolution: "720p", Duration: 8,
	})
	if err != nil {
		t.Fatalf("提交失败: %v", err)
	}
	if id != "892b47b9-155b-97e3" {
		t.Fatalf("request_id 错误: %s", id)
	}
	if gotBody["resolution"] != "720p" || gotBody["duration"] != float64(8) {
		t.Fatalf("视频参数未透传: %v", gotBody)
	}
	// 未指定参考图时不应发送 image 字段
	if _, ok := gotBody["image"]; ok {
		t.Fatal("文生视频不应发送 image")
	}
}

// 图生视频用 JSON 的嵌套 image.url，绝不能走 multipart（上游会 415）。
//
// 【字段形状是嵌套对象，不是顶层字符串】实测：顶层 image_url 会被上游静默丢弃 ——
// HTTP 200、照常扣费，但产物完全不参考图片。对照实验（同模型同图，只改字段形状）：
//
//	{"image_url": "…"}        → 产出与参考图毫无关系
//	{"image": {"url": "…"}}   → 产出忠实还原参考图
//
// 供应商文档（kaola-doc grok-media.md）曾写作顶层 image_url，与 xAI 官方
// docs.x.ai/developers/model-capabilities/video/image-to-video 不符，已同步订正。
func TestSubmitImage2VideoUsesNestedImageObject(t *testing.T) {
	var gotContentType string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = io.WriteString(w, `{"request_id":"rid"}`)
	}))
	defer srv.Close()

	g := NewMediaGateway(srv.URL)
	if _, err := g.SubmitVideo(context.Background(), testAPIKey, MediaGenerateParams{
		Model: "grok-imagine-video", Prompt: "苹果旋转",
		Resolution: "480p", Duration: 8, ImageURL: "https://example.com/a.jpg",
	}); err != nil {
		t.Fatalf("提交失败: %v", err)
	}
	if !strings.HasPrefix(gotContentType, "application/json") {
		t.Fatalf("图生视频必须用 JSON，实得 %s", gotContentType)
	}
	img, ok := gotBody["image"].(map[string]any)
	if !ok {
		t.Fatalf("image 必须是嵌套对象: %v", gotBody)
	}
	if img["url"] != "https://example.com/a.jpg" {
		t.Fatalf("参考图未透传: %v", gotBody)
	}
	// 顶层 image_url 会被上游静默丢弃，绝不能再发
	if _, bad := gotBody["image_url"]; bad {
		t.Fatalf("不应发送顶层 image_url（上游静默丢弃）: %v", gotBody)
	}
}

// 视频状态三态：202 进行中 / 200 完成 / 400 审核拒绝（终态失败且已扣费）。
func TestQueryVideoThreeStates(t *testing.T) {
	t.Run("202 进行中", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"status":"pending","progress":87}`)
		}))
		defer srv.Close()

		st, err := NewMediaGateway(srv.URL).QueryVideo(context.Background(), testAPIKey, "rid")
		if err != nil {
			t.Fatalf("查询失败: %v", err)
		}
		if st.Done {
			t.Fatal("202 不应判为完成")
		}
		if st.Progress != 87 {
			t.Fatalf("进度错误: %d", st.Progress)
		}
	})

	t.Run("200 完成", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{"model":"grok-imagine-video","status":"done","progress":100,
				"usage":{"cost_in_usd_ticks":5600000000},
				"video":{"duration":8,"url":"/v1/videos/rid/content"}}`)
		}))
		defer srv.Close()

		st, err := NewMediaGateway(srv.URL).QueryVideo(context.Background(), testAPIKey, "rid")
		if err != nil {
			t.Fatalf("查询失败: %v", err)
		}
		if !st.Done || st.Progress != 100 {
			t.Fatalf("完成态错误: %+v", st)
		}
		if st.CostTicks != 5600000000 {
			t.Fatalf("实扣错误: %d", st.CostTicks)
		}
	})

	t.Run("400 审核拒绝是终态失败", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"type":"invalid_request_error",
				"message":"Generated video rejected by content moderation."}}`)
		}))
		defer srv.Close()

		_, err := NewMediaGateway(srv.URL).QueryVideo(context.Background(), testAPIKey, "rid")
		if err == nil {
			t.Fatal("400 应返回错误")
		}
		var ge *MediaGatewayError
		if !asGatewayError(err, &ge) {
			t.Fatalf("应为 MediaGatewayError，实得 %T", err)
		}
		if ge.StatusCode != http.StatusBadRequest {
			t.Fatalf("状态码错误: %d", ge.StatusCode)
		}
		if !strings.Contains(ge.Message, "content moderation") {
			t.Fatalf("错误信息未解析: %s", ge.Message)
		}
	})
}

// 产物流式转发：必须带 Authorization 头，且返回未读取的流。
func TestOpenVideoContentStreams(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("fake-mp4-bytes"))
	}))
	defer srv.Close()

	body, ct, err := NewMediaGateway(srv.URL).OpenVideoContent(context.Background(), testAPIKey, "rid")
	if err != nil {
		t.Fatalf("打开产物流失败: %v", err)
	}
	defer body.Close()

	if gotAuth != "Bearer "+testAPIKey {
		t.Fatalf("产物端点必须带认证头，实得 %q", gotAuth)
	}
	if ct != "video/mp4" {
		t.Fatalf("Content-Type 未透传: %s", ct)
	}
	data, _ := io.ReadAll(body)
	if string(data) != "fake-mp4-bytes" {
		t.Fatalf("产物内容错误: %s", data)
	}
}

// 图生图走 multipart，参考图字段名必须是 image。
func TestEditImageSendsMultipart(t *testing.T) {
	var gotContentType, gotModel, gotFilename string
	var gotFileBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Errorf("解析 multipart 失败: %v", err)
			return
		}
		gotModel = r.FormValue("model")
		file, hdr, err := r.FormFile("image")
		if err != nil {
			t.Errorf("缺少 image 字段: %v", err)
			return
		}
		defer file.Close()
		gotFilename = hdr.Filename
		gotFileBody, _ = io.ReadAll(file)

		_, _ = io.WriteString(w, `{"data":[{"b64_json":"RURJVA=="}]}`)
	}))
	defer srv.Close()

	got, err := NewMediaGateway(srv.URL).EditImage(context.Background(), testAPIKey,
		MediaGenerateParams{Model: "grok-imagine-image", Prompt: "赛博朋克", N: 1},
		[]MediaUploadFile{{Filename: "input.jpg", Reader: strings.NewReader("image-bytes")}})
	if err != nil {
		t.Fatalf("编辑失败: %v", err)
	}

	if !strings.HasPrefix(gotContentType, "multipart/form-data") {
		t.Fatalf("图生图必须用 multipart，实得 %s", gotContentType)
	}
	if gotModel != "grok-imagine-image" {
		t.Fatalf("model 未透传: %s", gotModel)
	}
	if gotFilename != "input.jpg" || string(gotFileBody) != "image-bytes" {
		t.Fatalf("参考图未正确转发: %s / %s", gotFilename, gotFileBody)
	}
	if len(got) != 1 {
		t.Fatalf("响应解析错误: %v", got)
	}
}

// 【安全红线】任何错误信息都不得包含明文 API key。
func TestErrorsNeverLeakAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		// 上游把 key 回显在错误里——这种情况真实存在，必须被脱敏拦住
		_, _ = io.WriteString(w, `{"error":{"message":"invalid key: `+testAPIKey+`"}}`)
	}))
	defer srv.Close()

	g := NewMediaGateway(srv.URL)

	_, err := g.GenerateImage(context.Background(), testAPIKey, MediaGenerateParams{
		Model: "grok-imagine-image", Prompt: "x", N: 1,
	})
	if err == nil {
		t.Fatal("401 应返回错误")
	}
	if strings.Contains(err.Error(), testAPIKey) {
		t.Fatalf("错误信息泄露了明文 key: %s", err.Error())
	}
	// 去掉 Bearer 前缀的裸 key 同样不能出现
	if strings.Contains(err.Error(), strings.TrimPrefix(testAPIKey, "sk-")) &&
		strings.Contains(err.Error(), "test-secret-value") {
		t.Fatalf("错误信息泄露了裸 key: %s", err.Error())
	}
}

// 【回归测试】必须请求 b64_json，绝不能用 url。
//
// 曾经的线上缺陷：请求 response_format=url 时，上游返回 xAI 自有 CDN
// （imgen.x.ai）直链，该域名在国内 DNS 被污染、TCP 连接超时 —— 后端和浏览器
// 都拉不到，用户看到破图，但钱已经扣了。
// 见 kaola-doc/docs/guide/grok-media.md 的「国内网络建议使用 b64_json」。
func TestImageRequestsBase64NotURL(t *testing.T) {
	t.Run("文生图", func(t *testing.T) {
		var got map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&got)
			_, _ = io.WriteString(w, `{"data":[{"b64_json":"AAAA","mime_type":"image/jpeg"}]}`)
		}))
		defer srv.Close()

		if _, err := NewMediaGateway(srv.URL).GenerateImage(context.Background(), testAPIKey,
			MediaGenerateParams{Model: "grok-imagine-image", Prompt: "x", N: 1}); err != nil {
			t.Fatalf("生成失败: %v", err)
		}
		if got["response_format"] != "b64_json" {
			t.Fatalf("必须请求 b64_json（url 直链国内不可达），实得 %v", got["response_format"])
		}
	})

	t.Run("图生图", func(t *testing.T) {
		var gotFormat string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("解析 multipart 失败: %v", err)
				return
			}
			gotFormat = r.FormValue("response_format")
			_, _ = io.WriteString(w, `{"data":[{"b64_json":"AAAA"}]}`)
		}))
		defer srv.Close()

		if _, err := NewMediaGateway(srv.URL).EditImage(context.Background(), testAPIKey,
			MediaGenerateParams{Model: "grok-imagine-image", Prompt: "x", N: 1},
			[]MediaUploadFile{{Filename: "a.jpg", Reader: strings.NewReader("bytes")}}); err != nil {
			t.Fatalf("编辑失败: %v", err)
		}
		if gotFormat != "b64_json" {
			t.Fatalf("图生图同样必须请求 b64_json，实得 %q", gotFormat)
		}
	})
}

// b64 与 mime 正确解析成可拼 data URI 的字段；mime 缺省回退 image/jpeg。
func TestParseImagesResponseCarriesBase64(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":[
			{"b64_json":"QUJD","mime_type":"image/png"},
			{"b64_json":"REVG"}
		]}`)
	}))
	defer srv.Close()

	got, err := NewMediaGateway(srv.URL).GenerateImage(context.Background(), testAPIKey,
		MediaGenerateParams{Model: "grok-imagine-image", Prompt: "x", N: 2})
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("应返回 2 张图，实得 %d", len(got))
	}
	if got[0].B64 != "QUJD" || got[0].MimeType != "image/png" {
		t.Fatalf("首张解析错误: %+v", got[0])
	}
	if got[1].MimeType != "image/jpeg" {
		t.Fatalf("mime 缺省应回退 image/jpeg，实得 %q", got[1].MimeType)
	}
}

// 上游给出空壳响应（既无 b64 也无 url）时必须报错。
// 钱已经扣了却拿不到图，不能留一条「成功但打不开」的记录 —— 那就是破图的由来。
func TestParseImagesResponseRejectsEmptyContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"mime_type":"image/jpeg"}]}`)
	}))
	defer srv.Close()

	if _, err := NewMediaGateway(srv.URL).GenerateImage(context.Background(), testAPIKey,
		MediaGenerateParams{Model: "grok-imagine-image", Prompt: "x", N: 1}); err == nil {
		t.Fatal("既无 b64 也无 url 的响应应报错")
	}
}

// 上游未返回图片数据时应报错而非返回空切片，避免上层把空结果当成功落库。
func TestGenerateImageRejectsEmptyData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer srv.Close()

	_, err := NewMediaGateway(srv.URL).GenerateImage(context.Background(), testAPIKey,
		MediaGenerateParams{Model: "grok-imagine-image", Prompt: "x", N: 1})
	if err == nil {
		t.Fatal("空数据应报错")
	}
}

// 提交视频时上游未返回 request_id 必须报错：
// 拿不到 request_id 就等于花了钱却永远查不到产物。
func TestSubmitVideoRequiresRequestID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	_, err := NewMediaGateway(srv.URL).SubmitVideo(context.Background(), testAPIKey,
		MediaGenerateParams{Model: "grok-imagine-video", Prompt: "x", Resolution: "480p", Duration: 8})
	if err == nil {
		t.Fatal("缺少 request_id 应报错")
	}
}

// baseURL 尾斜杠归一化，避免拼出 //v1/... 这种路径。
func TestBaseURLNormalization(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = io.WriteString(w, `{"request_id":"x"}`)
	}))
	defer srv.Close()

	g := NewMediaGateway(srv.URL + "/")
	if _, err := g.SubmitVideo(context.Background(), testAPIKey, MediaGenerateParams{
		Model: "grok-imagine-video", Prompt: "x", Resolution: "480p", Duration: 8,
	}); err != nil {
		t.Fatalf("提交失败: %v", err)
	}
	if gotPath != "/v1/videos/generations" {
		t.Fatalf("路径未归一化: %s", gotPath)
	}
}

// asGatewayError 是 errors.As 的轻量替代，避免为一个断言引入额外导入。
func asGatewayError(err error, target **MediaGatewayError) bool {
	ge, ok := err.(*MediaGatewayError)
	if ok {
		*target = ge
	}
	return ok
}

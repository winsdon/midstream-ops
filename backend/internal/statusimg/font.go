package statusimg

import (
	_ "embed"
	"fmt"
	"os"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
)

// 【字体从哪来】看板必须画出中文，而运行镜像里不保证装有任何中文字体，
// 所以把一份 OFL 授权的 Noto Sans SC 直接 go:embed 进二进制：
// 部署方什么都不用配，出图也不会因为宿主环境换机器而变成豆腐块。
//
// 字体已用 deploy/scripts/subset-font.py 裁到「GB2312 全集 + ASCII + 常用标点」
// （7543 字符，7.95MB -> 1.62MB）。OFL 全文见同目录 fonts/OFL.txt。
//
// embed 路径必须是本包目录内的相对路径（跨包目录 embed 不允许），
// 这正是字体文件放在 internal/statusimg/fonts/ 而不是仓库根 assets/ 的原因。
//
//go:embed fonts/NotoSansSC-Regular.otf
var embeddedFontData []byte

// embeddedFontKey 是内嵌字体在缓存里的标识，仅用于判断「缓存是否还对得上」。
const embeddedFontKey = "embedded:fonts/NotoSansSC-Regular.otf"

var (
	// fontMu 保护下面两个字段：SetFontPath 可能在运行期被配置热更新调用，
	// 而 Render 可能同时在别的 goroutine 里跑（定时推送 + 手工预览）。
	fontMu       sync.RWMutex
	overrideKey  string
	overrideData []byte
)

// SetFontPath 用外部字体文件覆盖内嵌字体，对应配置项 status_push.font_path。
//
// path 为空字符串时恢复使用内嵌字体 —— 配置被清空时就该回到默认，这是调用方
// 最不需要额外分支的语义。文件读得到但解析不了会报错，不会让 Render 在出图的
// 那一刻才失败。
func SetFontPath(path string) error {
	if path == "" {
		fontMu.Lock()
		overrideKey, overrideData = "", nil
		fontMu.Unlock()
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("statusimg: 读取外部字体 %q 失败: %w", path, err)
	}
	// 立即解析一次做校验：配置写错要在启动/保存配置时报，而不是等推送失败。
	if _, err := opentype.Parse(data); err != nil {
		return fmt.Errorf("statusimg: 解析外部字体 %q 失败（需要 TTF/OTF）: %w", path, err)
	}

	fontMu.Lock()
	overrideKey, overrideData = path, data
	fontMu.Unlock()
	return nil
}

// fontSource 返回当前生效的字体标识与字节。返回的字节是只读的（不做拷贝）。
func fontSource() (key string, data []byte) {
	fontMu.RLock()
	defer fontMu.RUnlock()
	if overrideData != nil {
		return overrideKey, overrideData
	}
	return embeddedFontKey, embeddedFontData
}

// fontCache 缓存解析后的字体。
//
// 【为什么可以全局缓存】*sfnt.Font 自身的方法在「每次调用带不同的 Buffer」时
// 是并发安全的；而 opentype.Face 内部复用一个 buffer，**不能**并发使用。
// 所以这里只缓存字体本身，face 每次渲染现建（NewFace 只是分配一个结构体，
// 代价可以忽略），既避免共享 face 的数据竞争，也避免每个字号常驻一份缓冲。
var fontCache struct {
	sync.Mutex
	key  string
	font *sfnt.Font
}

// loadFont 返回当前生效字体（外部覆盖优先），并做进程内缓存。
//
// 解析结果按「字体标识」缓存：SetFontPath 换来一个新路径时自动失效，
// 不需要额外的失效通知。
func loadFont() (*sfnt.Font, error) {
	key, data := fontSource()

	fontCache.Lock()
	defer fontCache.Unlock()
	if fontCache.font != nil && fontCache.key == key {
		return fontCache.font, nil
	}

	parsed, err := opentype.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("statusimg: 解析字体 %q 失败: %w", key, err)
	}
	fontCache.key, fontCache.font = key, parsed
	return parsed, nil
}

// faces 是一张图要用到的全部字号。
//
// 【为什么按字号建 face 而不是按需创建】gg 的 SetFontFace 拿到 face 后就从
// Metrics() 取行高，字号必须在「绘制前」就确定；集中建好还能让 Render 在
// 真正开始画之前就发现字体问题，不会画到一半才报错。
type faces struct {
	title   font.Face // 左上大标题
	summary font.Face // 摘要第一行
	updated font.Face // 摘要第二行（更新时间）
	name    font.Face // 卡片名称
	rate    font.Face // 卡片倍率
	status  font.Face // 右上角状态
	meta    font.Face // 底部成功率/缓存率与备注
}

// newFaces 为一次渲染创建一套 face。
//
// 字体由调用方传入（Render 已经取过一次，缺字形检测也要用同一份），避免重复
// 走 loadFont 的缓存查找。
//
// 字号用 font.HintingNone：本包固定 2× 渲染，字形不受像素对齐影响，
// 关掉 hinting 可以让不同平台（Windows/Linux）产出完全一致的像素与宽度测量。
func newFaces(f *sfnt.Font) (*faces, error) {
	if f == nil {
		return nil, fmt.Errorf("statusimg: 字体为空")
	}

	fs := &faces{}
	for _, spec := range []struct {
		dst  *font.Face
		size float64 // 逻辑字号，实际按 scale 放大
	}{
		{&fs.title, fsTitle},
		{&fs.summary, fsSummary},
		{&fs.updated, fsUpdated},
		{&fs.name, fsName},
		{&fs.rate, fsRate},
		{&fs.status, fsStatus},
		{&fs.meta, fsMeta},
	} {
		// Size 是「点」，DPI=72 时点=像素，于是 Size 直接等于输出像素字号。
		face, err := opentype.NewFace(f, &opentype.FaceOptions{
			Size:    scale * spec.size,
			DPI:     72,
			Hinting: font.HintingNone,
		})
		if err != nil {
			return nil, fmt.Errorf("statusimg: 创建 %g 号字体失败: %w", spec.size, err)
		}
		*spec.dst = face
	}
	return fs, nil
}

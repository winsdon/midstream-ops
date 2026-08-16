package repository

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// MediaPricing 一个分组的图片 / 视频计费参数。
//
// 【为什么本站要复刻上游的定价规则】页面在用户按下「生成」之前必须给出预估费用，
// 而视频提交即扣费且不退款。上游不提供「试算」接口，只能按同一套规则本地算一遍。
// 字段与 sub2api 的 groups 表逐列对应，任何一列漏读都会让报价与账单对不上。
//
// 价格字段是指针：NULL 表示该分组没配自定义价，应回落到模型的标准价。
// 用 0 表示「没配」是错的——0 是合法的自定义价（免费档位）。
type MediaPricing struct {
	ImagePrice1K *float64
	ImagePrice2K *float64
	ImagePrice4K *float64

	VideoPrice480P  *float64
	VideoPrice720P  *float64
	VideoPrice1080P *float64

	// ImageRateIndependent 为 true 时图片用自己的倍率，否则跟随分组倍率。
	ImageRateIndependent bool
	ImageRateMultiplier  float64
	VideoRateIndependent bool
	VideoRateMultiplier  float64

	// GroupRateMultiplier 该用户在该分组的有效倍率：
	// 优先取 user_group_rate_multipliers 的专属值，无则取 groups.rate_multiplier。
	GroupRateMultiplier float64
}

// ImagePrice 返回指定计费档的自定义单价，未配置返回 nil。
func (m *MediaPricing) ImagePrice(tier string) *float64 {
	if m == nil {
		return nil
	}
	switch strings.ToUpper(strings.TrimSpace(tier)) {
	case "1K":
		return m.ImagePrice1K
	case "2K":
		return m.ImagePrice2K
	case "4K":
		return m.ImagePrice4K
	default:
		// 与 sub2api Group.GetImagePrice 一致：未知档位按 2K 处理。
		return m.ImagePrice2K
	}
}

// VideoPrice 返回指定分辨率的自定义单价（USD/秒），未配置返回 nil。
func (m *MediaPricing) VideoPrice(resolution string) *float64 {
	if m == nil {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(resolution)) {
	case "480p":
		return m.VideoPrice480P
	case "720p":
		return m.VideoPrice720P
	case "1080p":
		return m.VideoPrice1080P
	default:
		// 与 sub2api Group.GetVideoPrice 一致：未知分辨率按 480p 处理。
		return m.VideoPrice480P
	}
}

// EffectiveImageMultiplier 图片计费倍率，口径同 sub2api resolveImageRateMultiplier。
//
// 负值收敛到 0：上游对负倍率的处理就是当 0（免费），不是报错。
func (m *MediaPricing) EffectiveImageMultiplier() float64 {
	if m == nil {
		return 1
	}
	if m.ImageRateIndependent {
		return clampNonNegative(m.ImageRateMultiplier)
	}
	return clampNonNegative(m.GroupRateMultiplier)
}

// EffectiveVideoMultiplier 视频计费倍率，口径同 sub2api resolveVideoRateMultiplier。
func (m *MediaPricing) EffectiveVideoMultiplier() float64 {
	if m == nil {
		return 1
	}
	if m.VideoRateIndependent {
		return clampNonNegative(m.VideoRateMultiplier)
	}
	return clampNonNegative(m.GroupRateMultiplier)
}

func clampNonNegative(v float64) float64 {
	if v < 0 {
		return 0
	}
	return v
}

// GetMediaPricing 查询某分组对某用户的媒体计费参数。
//
// userID 是 sub2api 的用户 ID。本站上下游都以字符串携带它（嵌入会话的 claims
// 里是字符串），而 user_group_rate_multipliers.user_id 是 BIGINT——
// 【必须先在 Go 侧确认它是数字再进 SQL】直接写 $2::bigint 时，非数字串会让整条
// 查询报 invalid input syntax 而不是「查不到」，把「这个用户没有专属倍率」这种
// 正常情况变成 500。
//
// 分组不存在时返回 nil, nil：调用方据此退化到标准价。这不是错误——key 可能
// 没绑分组（LEFT JOIN 出来 group_id=0）。
func (p *PG) GetMediaPricing(ctx context.Context, groupID int64, userID string) (*MediaPricing, error) {
	if groupID <= 0 {
		return nil, nil
	}

	// 专属倍率只在 userID 是合法数字时才查。
	var userIDNum int64 = -1
	if n, err := strconv.ParseInt(strings.TrimSpace(userID), 10, 64); err == nil && n > 0 {
		userIDNum = n
	}

	var m MediaPricing
	err := p.pool.QueryRow(ctx, `
		SELECT g.image_price_1k, g.image_price_2k, g.image_price_4k,
		       g.video_price_480p, g.video_price_720p, g.video_price_1080p,
		       COALESCE(g.image_rate_independent,false), COALESCE(g.image_rate_multiplier,1),
		       COALESCE(g.video_rate_independent,false), COALESCE(g.video_rate_multiplier,1),
		       -- 专属倍率优先于分组默认倍率，与 sub2api userGroupRateResolver 同口径
		       COALESCE(ugr.rate_multiplier, g.rate_multiplier, 1)
		FROM groups g
		LEFT JOIN user_group_rate_multipliers ugr
		       ON ugr.group_id = g.id AND ugr.user_id = $2
		WHERE g.id = $1 AND g.deleted_at IS NULL`,
		groupID, userIDNum).Scan(
		&m.ImagePrice1K, &m.ImagePrice2K, &m.ImagePrice4K,
		&m.VideoPrice480P, &m.VideoPrice720P, &m.VideoPrice1080P,
		&m.ImageRateIndependent, &m.ImageRateMultiplier,
		&m.VideoRateIndependent, &m.VideoRateMultiplier,
		&m.GroupRateMultiplier)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

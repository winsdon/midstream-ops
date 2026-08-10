package repository

import (
	"context"
	"encoding/json"
)

// PGUserKey 用户的一把 API Key 及其分组能力。
//
// 【Key 是明文，禁止外泄】它只在「后端 ↔ 网关」这一段的内存里流转：service 层
// 拿它去打生图 / 生视频接口，handler 层转 DTO 时必须掩码。绝不进库、不进日志、
// 不进响应。与 PGAccount.APIKey 同一纪律，但风险更高——那是运营方自己的上游
// 凭据，这是终端用户的凭据，泄露等于替用户把钱包交出去。
type PGUserKey struct {
	ID        int64
	Name      string
	Key       string // 明文，禁止外泄
	GroupID   int64
	GroupName string
	// Platform 决定视频能力：sub2api 的视频接口只在 grok / composite 平台放行，
	// 其余平台直接返回 404（见 sub2api routes/gateway.go 的 videoGenerationHandler）。
	Platform string
	// AllowImage 对应 groups.allow_image_generation。与视频不同，图片能力是显式布尔开关。
	AllowImage bool
	// Models 是该分组实际可用的模型名。为空表示分组下账号都没配 model_mapping，
	// 调用方应回落平台默认模型表（见 service/platform_models.go）。
	Models []string
}

// ListUserKeys 返回某 sub2api 用户可用的 key 及其分组能力。
//
// 【与 ListGroupAvailableModels 的关键区别：不排除专属分组】那个方法服务模型广场，
// 刻意剔除 is_exclusive 分组——专属分组是管理员授权给特定用户的，其分组名与倍率
// 属于内部定价策略，不该出现在面向全体用户的价格目录里。但这里是「用户查自己的
// key」，而用户的 key 恰恰常常就绑在专属分组上，沿用那份过滤会让页面直接空白。
//
// 模型口径与 ListGroupAvailableModels 保持一致：分组下所有可调度账号
// credentials->'model_mapping' 的 KEY 并集。KEY 是用户请求名（VALUE 才是上游真实名），
// 与 /v1/models 的返回同源。
//
// 账号过滤只看持久状态（未删除 / active / schedulable），忽略限流、过载等瞬时运行态：
// 模型下拉是能力清单，不该因某个账号临时限流而让选项消失。
//
// 带 '*' 的通配映射被剔除：它是「透传任意模型名」的占位符，不是可选模型。
func (p *PG) ListUserKeys(ctx context.Context, userID string) ([]PGUserKey, error) {
	rows, err := p.pool.Query(ctx, `
		WITH grp_accounts AS (
		    SELECT g.id AS group_id, a.credentials
		    FROM groups g
		    JOIN account_groups ag ON ag.group_id = g.id
		    JOIN accounts a        ON a.id = ag.account_id
		    WHERE g.deleted_at IS NULL AND g.status = 'active'
		      -- composite 分组聚合全部平台的账号，其余按平台匹配
		      AND (g.platform = 'composite' OR a.platform = g.platform)
		      AND a.deleted_at IS NULL AND a.status = 'active' AND a.schedulable = TRUE
		),
		account_models AS (
		    SELECT ga.group_id, jsonb_object_keys(ga.credentials -> 'model_mapping') AS model
		    FROM grp_accounts ga
		    WHERE jsonb_typeof(ga.credentials -> 'model_mapping') = 'object'
		      AND ga.credentials -> 'model_mapping' <> '{}'::jsonb
		),
		group_models AS (
		    SELECT group_id,
		           ARRAY_AGG(DISTINCT model ORDER BY model)
		               FILTER (WHERE model NOT LIKE '%*') AS models
		    FROM account_models
		    GROUP BY group_id
		)
		SELECT k.id, COALESCE(k.name,''), k.key,
		       COALESCE(g.id,0), COALESCE(g.name,''), COALESCE(g.platform,''),
		       COALESCE(g.allow_image_generation,false),
		       COALESCE(gm.models, '{}'),
		       COALESCE(g.models_list_config,'{}'::jsonb)
		FROM api_keys k
		LEFT JOIN groups g      ON g.id = k.group_id AND g.deleted_at IS NULL AND g.status = 'active'
		LEFT JOIN group_models gm ON gm.group_id = g.id
		WHERE k.user_id = $1 AND k.deleted_at IS NULL AND k.status = 'active'
		ORDER BY k.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PGUserKey
	for rows.Next() {
		var k PGUserKey
		var listCfg []byte
		if err := rows.Scan(&k.ID, &k.Name, &k.Key, &k.GroupID, &k.GroupName,
			&k.Platform, &k.AllowImage, &k.Models, &listCfg); err != nil {
			return nil, err
		}
		k.Models = applyGroupModelsList(k.Models, listCfg)
		out = append(out, k)
	}
	return out, rows.Err()
}

// groupModelsListConfig 对应 sub2api 的 groups.models_list_config。
// 启用时该分组只暴露白名单内的模型（sub2api 侧 filterModelsByCustomList 的口径）。
type groupModelsListConfig struct {
	Enabled bool     `json:"enabled"`
	Models  []string `json:"models"`
}

// applyGroupModelsList 按分组自定义模型白名单过滤。
//
// 配置未启用或白名单为空时原样返回——这与 sub2api 的 CustomModelsListEnabled()
// 判定一致：两个条件缺一，自定义列表都不生效。
//
// 解析失败时同样原样返回而非报错：配置格式异常不该让整个 key 列表 500，
// 退化成「不过滤」是安全的一侧（最多多显示几个模型，调用时上游会拒）。
func applyGroupModelsList(models []string, raw []byte) []string {
	if len(models) == 0 || len(raw) == 0 {
		return models
	}
	var cfg groupModelsListConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return models
	}
	if !cfg.Enabled || len(cfg.Models) == 0 {
		return models
	}
	allow := make(map[string]struct{}, len(cfg.Models))
	for _, m := range cfg.Models {
		allow[m] = struct{}{}
	}
	out := make([]string, 0, len(models))
	for _, m := range models {
		if _, ok := allow[m]; ok {
			out = append(out, m)
		}
	}
	return out
}

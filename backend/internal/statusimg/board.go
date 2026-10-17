// Package statusimg 把「渠道状态」看板的纯视图模型渲染成 PNG。
//
// 【为什么独立成包】本包只认识 Board 这一组字符串化的视图模型，不 import
// repository / service / config：画图是纯函数，输入是已经算好的数字与文案。
// 这样渲染能在没有数据库、没有业务配置的进程里跑（单测、预览接口都受益），
// 也让「算得对不对」与「画得好不好看」成为两件可分别验证的事。
//
// 色块分数、状态判定、样本门槛、倍率格式化等口径全部在 service 层完成；
// 本包不做任何业务判断，只负责排版与上色。
package statusimg

// Board 一张「渠道状态」看板的完整视图模型。
//
// 字段全部是成品字符串 —— 排版细节（小数位、百分号、时间格式）由 service 层
// 决定，本包只按固定位置画出来。
type Board struct {
	// Title 左上角大标题，如「渠道状态」。
	Title string
	// Summary 标题右侧第一行摘要，如「11 个渠道 · 比率近90分钟」。
	Summary string
	// UpdatedAt 标题右侧第二行，如「更新 10-12 15:30」。空字符串则第二行不画。
	UpdatedAt string
	// Note 底部小字备注（口径说明）。空字符串则不画。
	Note string
	// Truncated 卡片被裁剪时的说明，单独占图底部一整行。空字符串则不画。
	//
	// 【为什么不塞进 Summary】Summary 只占画布右半边（约 624px），「共 70 个，
	// 图仅显示前 64」这类文案会被截断到看不见关键数字 —— 那恰好让「悄悄少发
	// 分组」在客户可见面上依然成立。单独一行有整幅宽度（1232px），信息完整。
	Truncated string
	// Cards 按展示顺序排列的卡片（service 层已排好序）。
	Cards []Card
}

// Card 一个分组的一张卡片。
type Card struct {
	// Name 分组名（已按显示宽度截断）。
	Name string
	// Rate 倍率显示文本，如「0.08x」。
	Rate string
	// Status 右上角状态文本：正常 / 延迟 / 异常 / 无流量。
	Status string
	// StatusLevel 状态配色档位，"ok" | "warn" | "bad" | "none"。
	StatusLevel string
	// Blocks 八个色块，从左（较早）到右（较新），固定 8 个。
	Blocks []Block
	// SuccessText 底部成功率显示文本，如「99.2%」或「-」。
	SuccessText string
	// CacheText 底部缓存率显示文本，如「63.1%」或「-」。
	CacheText string
}

// Block 一个时间块的色块。
type Block struct {
	// Score 健康分 0–100；HasData 为 false 时忽略。
	Score int
	// HasData 该块是否有足够样本（样本不足与无流量都算 false，画灰块）。
	HasData bool
}

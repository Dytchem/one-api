package billing

// 自用模式：计费已移除。
// 所有函数保留签名（避免改动调用点），内部均为 no-op。
// 日志中的 token 统计与渠道性能指标由 relay/controller/helper.go 单独记录。

import (
	"context"
)

func ReturnPreConsumedQuota(ctx context.Context, preConsumedQuota int64, tokenId int) {
}

func PostConsumeQuota(ctx context.Context, tokenId int, quotaDelta int64, totalQuota int64, userId int, channelId int, modelRatio float64, groupRatio float64, modelName string, tokenName string) {
}

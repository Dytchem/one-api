package model

import "testing"

// dyt-104: isFailContent 与 isFailContent 收紧后的 HTTP 口径

func TestIsFailContent(t *testing.T) {
	failed := []string{
		"[原始请求] 探测失败 | 渠道：x(#1)",
		"回复为空，渠道：x(#1)，模型：a→b",
		"渠道尝试 | 状态：失败 | 错误：x",
		"渠道尝试 | 状态: 失败 | 错误：x",
		"请求失败: connection reset",
		"SSE首token超时(HTTP 502, 0行/0字节, 120s内无content)",
		"探测失败 HTTP 200 空body",
	}
	for _, content := range failed {
		if !isFailContent(content) {
			t.Errorf("isFailContent(%q) = false, want true", content)
		}
	}

	ok := []string{
		"回复完成，回复内容：你好",
		"渠道测试成功",
		"使用 HTTPS 访问上游",   // 无 "HTTP 数字" 结构
		"HTTP header 异常提示",   // 宽松 LIKE '%HTTP %' 会误伤，正则口径不会
		"HTTP 600 超出正则范围",  // [1-5]\d\d 不匹配 6xx
	}
	for _, content := range ok {
		if isFailContent(content) {
			t.Errorf("isFailContent(%q) = true, want false", content)
		}
	}
}

func TestMarkLogFailedOnlyAppliesToExpectedTypes(t *testing.T) {
	// 消费日志：按内容判定
	consume := &Log{Type: LogTypeConsume, Content: "探测失败 | 渠道：x"}
	markLogFailed(consume)
	if !consume.IsFailed {
		t.Errorf("consume log with fail content should be marked failed")
	}

	consumeOK := &Log{Type: LogTypeConsume, Content: "回复完成，回复内容：hello"}
	markLogFailed(consumeOK)
	if consumeOK.IsFailed {
		t.Errorf("normal consume log should not be marked failed")
	}

	// 系统日志：仅 "渠道尝试" 前缀参与判定
	sys := &Log{Type: LogTypeSystem, Content: "渠道尝试 | 状态：失败"}
	markLogFailed(sys)
	if !sys.IsFailed {
		t.Errorf("channel attempt system log with fail content should be marked failed")
	}

	sysOther := &Log{Type: LogTypeSystem, Content: "系统启动完成 探测失败计数清零"}
	markLogFailed(sysOther)
	if sysOther.IsFailed {
		t.Errorf("non-attempt system log should not be marked failed")
	}

	// 其他类型不参与判定
	manage := &Log{Type: LogTypeManage, Content: "探测失败"}
	markLogFailed(manage)
	if manage.IsFailed {
		t.Errorf("manage log should not be marked failed")
	}
}

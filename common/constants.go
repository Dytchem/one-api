package common

import (
	"time"

	"github.com/songquanpeng/one-api/common/config"
)

var StartTime = time.Now().Unix() // unit: second
var Version = "v0.0.0"            // this hard coding will be replaced automatically when building, no need to manually change

// StreamScannerMaxBufferBytes 流式 SSE 单行缓冲上限（字节），供各 Scanner.Buffer() 使用
var StreamScannerMaxBufferBytes = config.StreamScannerMaxBufferMB * 1024 * 1024

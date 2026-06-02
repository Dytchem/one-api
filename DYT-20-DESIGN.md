# dyt-20 设计方案：失败请求/响应完整保留 + UI 失败日志页

> 状态：草稿，等主人审阅
> 范围：后端（~200 行）+ 前端（~300 行）+ 数据库新表

---

## 一、数据库迁移

### 新表 `log_payloads`

```sql
CREATE TABLE IF NOT EXISTS log_payloads (
  log_id     BIGINT PRIMARY KEY,       -- 一对一关联 logs.id
  request    LONGTEXT,                  -- 完整请求 JSON（已序列化）
  response   LONGTEXT,                  -- 完整响应（SSE 拼接 / 错误体）
  error      LONGTEXT,                  -- 错误字符串（如 empty_response）
  created_at BIGINT,                    -- 同 logs.created_at
  INDEX idx_created (created_at)
);
```

### 在 `model/main.go` 的自动迁移里加：

```go
// 新增 model.LogPayload
if err := DB.AutoMigrate(&LogPayload{}); err != nil { ... }
```

---

## 二、后端改动

### 文件 1: `model/log.go`（+50 行）

```go
// 新结构
type LogPayload struct {
    LogId     int64  `gorm:"primaryKey"`
    Request   string `gorm:"type:longtext"`
    Response  string `gorm:"type:longtext"`
    Error     string `gorm:"type:longtext"`
    CreatedAt int64
}

// 同步写（成功用，失败可降级为异步）
func RecordLogPayload(payload *LogPayload) error {
    if DB == nil { return nil }
    return DB.Create(payload).Error
}

// 异步写（失败用，避免阻塞请求）
var payloadQueue = make(chan *LogPayload, 1024)

func init() {
    for i := 0; i < 2; i++ {
        go func() {
            for p := range payloadQueue {
                _ = RecordLogPayload(p)  // 失败也无所谓
            }
        }()
    }
}

func RecordLogPayloadAsync(payload *LogPayload) {
    select {
    case payloadQueue <- payload:
    default:
        // 队列满则丢弃（避免阻塞）
    }
}
```

### 文件 2: `relay/controller/text.go`（+40 行）

**关键改动**：
1. 在 `RelayTextHelper` 入口处 **保存 `textRequest` 的 JSON 副本**（`c.Set("raw_request", body)`）
2. 在写失败日志时**同时写 payload**
3. 流式响应的 body 拼接：**把 scanner 扫到的所有行拼起来**

```go
// 入口处捕获请求 body
rawBody, _ := c.GetRawData()
c.Request.Body = io.NopCloser(bytes.NewBuffer(rawBody))  // 重置 body
c.Set("raw_request", string(rawBody))  // 给后续路径用

// 失败日志写入点（dyt-19 的 classifyEmptyResponse 后面）
if !success {
    // ... existing failed log write ...
    
    // dyt-20: 异步写 payload
    if textRequest != nil {
        reqJSON, _ := json.Marshal(textRequest)
        respBody := collectResponseBody()  // 流式拼接或非流式 body
        dbmodel.RecordLogPayloadAsync(&dbmodel.LogPayload{
            LogId:     logId,  // 失败日志刚拿到的 id
            Request:   truncate(string(reqJSON), 100*1024),
            Response:  truncate(respBody, 100*1024),
            Error:     combinedReason,
            CreatedAt: meta.StartTime,
        })
    }
}
```

### 文件 3: `controller/log.go`（+80 行）

**新增 3 个 API**：

| API | 方法 | 用途 |
|-----|------|------|
| `/api/log/fail/list` | GET | 失败日志分页列表（带筛选） |
| `/api/log/fail/:id` | GET | 单条失败日志详情（含 payload） |
| `/api/log/fail/:id` | DELETE | 删除单条（**谨慎**） |

```go
// GET /api/log/fail/list?page=1&page_size=50&channel=27&model=jarvis&since=1h
// 响应：
{
  "success": true,
  "data": {
    "total": 88,
    "items": [
      {
        "id": 55063,
        "time": 1780366504,
        "channel": {"id": 27, "name": "MiniMax Frontier"},
        "model": "jarvis",
        "error": "连接断/异常结束 (HTTP 200, 6行/1886字节, ...)[2013:参数错误]",
        "has_payload": true,  // ← 点击行时按需拉详情
        "preview": "回到one api fork项目上 [tools×0]"
      }
    ]
  }
}

// GET /api/log/fail/55063
// 响应：
{
  "success": true,
  "data": {
    "id": 55063,
    "request":  "{...}",   // 完整 JSON 字符串
    "response": "{...}",   // 完整 JSON 字符串
    "error":    "...",
    "channel":  {...},
    "model":    "jarvis"
  }
}
```

---

## 三、前端改动（default 主题）

### 新文件

| 文件 | 作用 |
|------|------|
| `web/default/src/components/FailLogs/index.js` | 页面容器 |
| `web/default/src/components/FailLogs/FailLogList.js` | 列表（虚拟滚动） |
| `web/default/src/components/FailLogs/FailLogDetail.js` | 详情面板 |
| `web/default/src/components/FailLogs/PayloadView.js` | JSON 格式化 + 高亮 |
| `web/default/src/components/FailLogs/failLogsApi.js` | API 客户端 |

### 改动文件

| 文件 | 改动 |
|------|------|
| `web/default/src/App.js` | 注册 `/fail-logs` 路由 |
| `web/default/src/components/SiderBar.js` | 加 Sidebar 入口 |
| `web/default/src/i18n/locales/*.json` | 加中英文 label |

### UI 草图（与 LogsTable 风格一致）

```
┌──────────────────────────────────────────────────────────────────┐
│ 失败日志浏览器       渠道:[全部▾] 模型:[全部▾] 状态:[全部▾]  [🔄] │
├──────────────────────────────────────────────────────────────────┤
│ 时间:[最近 1h▾]  关键词:[____________] [搜索]    共 88 条         │
├──────────────────────────────────────────────────────────────────┤
│ ┌────────────┬──────────┬────────┬─────────┬──────────────────┐ │
│ │ 时间       │ 渠道     │ 模型   │ 状态码  │ 预览             │ │
│ ├────────────┼──────────┼────────┼─────────┼──────────────────┤ │
│ │ 10:15:04  │ Frt M3   │ jarvis │ [2013] │ 连接断...        │ │
│ │ 08:33:36  │ Frt M3   │ jarvis │ [空]   │ 探测失败...      │ │
│ │ 04:01:35  │ Frt M3   │ jarvis │ [超时] │ ...              │ │
│ └────────────┴──────────┴────────┴─────────┴──────────────────┘ │
├──────────────────────────────────────────────────────────────────┤
│ 详情面板（点行展开，固定在底部 40% 高度）                          │
│ ┌─ Request ──────────────────────────────────────────────────┐  │
│ │ {                                                          │  │
│ │   "model": "jarvis",                                       │  │
│ │   "messages": [                                            │  │
│ │     {"role": "system", "content": "..."},                  │  │
│ │     ...                                                    │  │
│ │   ],                                                       │  │
│ │   "stream": true                                           │  │
│ │ }                                                          │  │
│ │                                              [📋 复制]     │  │
│ ├─ Response ─────────────────────────────────────────────────┤  │
│ │ {                                                          │  │
│ │   "id": "066d6f...",                                       │  │
│ │   "choices": null,                                         │  │
│ │   "usage": null,                                           │  │
│ │   "base_resp": {"status_code": 2013, "status_msg": "..."} │  │
│ │ }                                                          │  │
│ │                                              [📋 复制]     │  │
│ ├─ Error ────────────────────────────────────────────────────┤  │
│ │ 连接断/异常结束 (HTTP 200, 6行/1886字节) ... [2013:参数错误]│  │
│ └────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────┘
```

**风格统一**：
- 暗色主题自动适配（`@douyinfe/semi-ui` 已支持）
- 颜色：失败红、警告橙、状态码徽章灰底
- 字体：与 LogsTable 一致（`Menlo, Monaco, monospace`）
- 表格分页：每页 50 条，可调
- 详情：**懒加载**（点行才拉 payload）

---

## 四、风险评估

| 风险 | 缓解 |
|------|------|
| `log_payloads` 写失败拖慢请求 | **异步队列** + 队列满丢弃 |
| 长 body 拖慢 DB | **100KB 截断** |
| UI 渲染超大 JSON 卡 | **分段渲染**（每 1000 行一批） |
| 旧失败日志没 payload | **允许 has_payload=false** |

---

## 五、版本与流程

- **VERSION**：`v0.6.11-dyt-20`
- **流程**：改代码 → go build → 主人审阅 diff → 主人批准 → commit + push + tag → 等 CI → 拉 3099 测 → 报告 → 主人批准 → 部署 3000 + 3001

---

**等主人审阅后开干 ✋**

# Release Notes

本 Fork 自 [songquanpeng/one-api](https://github.com/songquanpeng/one-api)，从 dyt-N 后开始的所有变更记录。

---

## v0.6.11-dyt-23（2026-06-02）

### 修复
- **探测重试 body 完全一致**：之前 `doProbe` 内部走 `getRequestBody` → `adaptor.ConvertRequest`，因 `GeneralOpenAIRequest` 字段带 `omitempty` 二次序列化会丢字段（如 `*bool` 类的 `Store`/`LogitBias`），导致两次发出去的 body 字节不一致。第二次触发 MiniMax M3 2013 `missing required parameter expr_path=messages` 错误，而非真实的流断流错误。
  - 修复：探测阶段直接 `json.Marshal(textRequest)` 缓存 bytes，两次 `doProbe` 用 `bytes.NewReader(bodyBytes)` 发给上游，**跳过** `getRequestBody`/`adaptor.ConvertRequest`
  - 效果：探测和重试发送的 HTTP body 字节完全一致 → 探测错误就是真实错误，不会被序列化丢字段掩盖

---

## v0.6.11-dyt-22（2026-06-02）

### 改进
- **日志格式统一**：`模型：jarvis→MiniMax-M3`（用户原始 → 实际路由）替代 `请求模型：MiniMax Frontier/MiniMax-M3` 模糊展示
  - 探测失败日志
  - type=4 渠道尝试日志
- **去重**：`empty_response` 场景下不再写重复日志（探测失败 + 渠道尝试两条变一条）
  - `RecordChannelAttemptLog` 在 `empty_response` 时跳过
  - 失败日志现在**只显示一条**

---

## v0.6.11-dyt-21（2026-06-02）

### 修复
- **log_payloads 表自动迁移**：之前 `InitLogDB` 在未设 `LOG_SQL_DSN`（使用共享 DB）时**不迁移** `log_payloads` 表，导致 dyt-20 的 payload 写入失败
  - 修复：让 payload 表迁移逻辑独立于 `LOG_SQL_DSN` 设置
  - 效果：dyt-20 升级后无需手动 `CREATE TABLE`

---

## v0.6.11-dyt-20（2026-06-02）

### 新功能
- **失败请求/响应完整保留**：所有失败场景下 `request body` / `response body` / `error` 完整存入新增的 `log_payloads` 表（request 字段 longtext，可存 700KB+）
  - 失败日志新增 `request_payload_id` 字段，关联到 `log_payloads` 表
- **流异常检测**：自动判定 `finish_reason: tool_calls` 但响应截断的流式失败，标记为流异常
- **UI 失败日志浏览器** `/fail-logs`：
  - 按时间倒序显示所有失败日志
  - 列表项展示：时间、渠道、模型、错误类型徽章、错误摘要
  - 点击行展开 → 完整 request/response/error JSON
  - 渠道筛选、模型筛选
  - payload 缺失时显示"未捕获"提示
- **API 端点**：
  - `GET /api/log/fail/list`（AdminAuth）— 失败日志列表
  - `GET /api/log/fail/:id`（AdminAuth）— 单条 payload 详情

### 改进
- 失败日志预览 200 字节末行 + status_code 徽章 + tools 摘要（dyt-19 ABC 改进）

### 数据库迁移
新增表 `log_payloads`：
```sql
CREATE TABLE log_payloads (
  log_id bigint AUTO_INCREMENT PRIMARY KEY,
  request longtext,
  response longtext,
  error longtext,
  created_at bigint
);
```

`logs` 表新增字段 `request_payload_id`（外键关联 `log_payloads.log_id`）。

### ⚠️ 升级注意
- dyt-20 之前的历史失败日志 `request_payload_id` 为 NULL（payload 已丢失）
- dyt-20 之后的新失败才完整保存

---

## v0.6.11-dyt-19（2026-06-02）

### 改进
- 失败日志末行预览从 60 → 200 字节
- 失败日志加 `status_code` 徽章（如 `[2013:invalid params]`）
- 请求预览附 `tools` 摘要（如 `[tools×47:agents_list,browser,cron,...]`）

---

## v0.6.11-dyt-18（2026-06-02）

### 新功能
- **探测失败记录上游细节**：失败日志附 `| 上游：连接断/异常结束 (HTTP 200, 6行/1887字节, 末行: "...tool_calls[0].id=...")`
- **empty response 同渠道重试 1 次**：探测阶段如果空响应，自动用同渠道重试 1 次

---

## v0.6.11-dyt-17 及更早

请查看 [GitHub Releases](https://github.com/Dytchem/one-api/releases) 历史记录。

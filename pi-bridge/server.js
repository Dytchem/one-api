// pi-bridge: 将 pi agent 通过 HTTP/SSE 暴露给 one-api 前端
// 模型表(models.json)动态同步自 one-api /v1/models；工具 = one-api 管理接口
import http from 'node:http';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  createAgentSession,
  defineTool,
  DefaultResourceLoader,
  SettingsManager,
  SessionManager,
  ModelRegistry,
  AuthStorage,
} from '@earendil-works/pi-coding-agent';
import { Type } from 'typebox';

const PORT = parseInt(process.env.PORT || '3005', 10);
const ONEAPI_BASE = process.env.ONEAPI_BASE || 'http://127.0.0.1:3000';
// dyt-92: 管理员 token 必须显式配置；缺失时仅保留本地工具能力并警告（模型表不自动同步）。
// 该 token 用于 /v1/models 同步，泄露会导致模型枚举，故不提供硬编码兜底。
const ONEAPI_ADMIN_TOKEN = process.env.ONEAPI_ADMIN_TOKEN || '';
// dyt-96/97: 与 one-api 的共享密钥（AGENT_BRIDGE_SECRET 同值），加固项而非强制项：
// - 已配置：严格校验 X-Bridge-Token，同机其他进程无密钥无法调用
// - 未配置：兼容模式（保持旧版行为，仅监听 127.0.0.1，不校验），启动时打印警告
// 强烈建议生产环境成对配置（bridge 的 BRIDGE_SECRET = one-api 的 AGENT_BRIDGE_SECRET）。
const BRIDGE_SECRET = process.env.BRIDGE_SECRET || '';
function requireAuth(req, res) {
  if (!BRIDGE_SECRET) {
    // 兼容模式：未配置密钥不校验（v94 及更早版本行为），功能不受影响
    return true;
  }
  const token = req.headers['x-bridge-token'];
  if (typeof token !== 'string' || token !== BRIDGE_SECRET) {
    res.writeHead(401, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ error: 'unauthorized' }));
    return false;
  }
  return true;
}
const AGENT_DIR = path.join(path.dirname(fileURLToPath(import.meta.url)), 'agent');
const MODELS_PATH = path.join(AGENT_DIR, 'models.json');
const AUTH_PATH = path.join(AGENT_DIR, 'auth.json');
const AGENT_EXEC_TIMEOUT_MS = 300_000;
const MAX_EVENTS_PER_SESSION = 2000;
const MAX_RESULT_LENGTH = 20000;
const MAX_SEARCH_LENGTH = 12000;
const MAX_RESPONSE_LENGTH = 12000;

const SYSTEM_PROMPT = `You are an operations assistant for a one-api gateway (OpenAI-compatible API management platform).
You can manage channels, tokens, users and view logs through the provided tools.
Rules:
- Always verify facts with tools before answering; never invent data.
- Keep answers concise and in Chinese unless the user asks otherwise.
- For channel operations, pass full channel objects returned by get_channel/list_channels when updating.
- When a tool reports an error, report the error message to the user.`;

fs.mkdirSync(AGENT_DIR, { recursive: true });

let lastSync = 0;

// thinking 模型：按 pi 原生机制标注 reasoning + thinkingFormat，
// pi 会在思考关闭(off)时发送 enable_thinking:false（qwen）或 reasoning.enabled:false（deepseek），
// 模型随即返回标准 tool_calls JSON
const MODEL_OVERRIDES = {
  'qwen3.7-plus': {
    reasoning: true,
    compat: { thinkingFormat: 'qwen' },
  },
  'mimo-v2.5': {
    reasoning: true,
    compat: { thinkingFormat: 'qwen' },
  },
  'mimo-v2.5-pro': {
    reasoning: true,
    compat: { thinkingFormat: 'qwen' },
  },
  'deepseek-v4-flash': {
    reasoning: true,
    compat: { thinkingFormat: 'deepseek' },
  },
  'deepseek-v4-pro': {
    reasoning: true,
    compat: { thinkingFormat: 'deepseek' },
  },
};

async function syncModels(token) {
  const t = token || ONEAPI_ADMIN_TOKEN;
  if (!t) return 0;
  try {
    const resp = await fetch(`${ONEAPI_BASE}/v1/models`, {
      headers: { Authorization: `Bearer ${t}` },
      signal: AbortSignal.timeout(10000),
    });
    if (!resp.ok) throw new Error(`http ${resp.status}`);
    const json = await resp.json();
    const models = (json.data || []).map((m) => ({
      id: m.id,
      ...(MODEL_OVERRIDES[m.id] || {}),
    }));
    // 与现有表合并（union）：多用户/多分组模型的可见性互不覆盖
    let merged = models;
    try {
      const oldCfg = JSON.parse(fs.readFileSync(MODELS_PATH, 'utf8'));
      const oldModels = oldCfg.providers?.oneapi?.models || [];
      const seen = new Set(models.map((m) => m.id));
      merged = [...oldModels.filter((m) => !seen.has(m.id)), ...models];
    } catch (_) { /* 首次同步无需合并 */ }
    const cfg = {
      providers: {
        oneapi: {
          baseUrl: `${ONEAPI_BASE}/v1`,
          api: 'openai-completions',
          apiKey: 'oneapi-bridge-placeholder',
          models: merged,
          compat: { supportsDeveloperRole: false },
        },
      },
    };
    // 原子写入：临时文件 + rename，避免写一半损坏模型表
    fs.writeFileSync(MODELS_PATH + '.tmp', JSON.stringify(cfg, null, 2));
    fs.renameSync(MODELS_PATH + '.tmp', MODELS_PATH);
    lastSync = Date.now();
    console.log(`[models] synced ${models.length} models`);
    return models.length;
  } catch (e) {
    console.error('[models] sync failed:', e.message);
    return 0;
  }
}

async function ensureModels(force) {
  if (fs.existsSync(MODELS_PATH) && !force && Date.now() - lastSync < 60_000) return;
  await syncModels();
}

// ---- 网络搜索（AnySearch MCP，匿名调用）----
const ANYSEARCH_MCP = 'https://api.anysearch.com/mcp';
async function anysearchCall(toolName, args) {
  try {
    const resp = await fetch(ANYSEARCH_MCP, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Accept: 'application/json, text/event-stream',
        'X-Anysearch-Client': 'mcp/1.0.0',
      },
      body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name: toolName, arguments: args } }),
      signal: AbortSignal.timeout(90000),
    });
    const ct = resp.headers.get('content-type') || '';
    let data = null;
    if (ct.includes('text/event-stream')) {
      const text = await resp.text();
      const line = text.split('\n').find((l) => l.startsWith('data:'));
      if (line) data = JSON.parse(line.slice(5));
    } else {
      data = await resp.json();
    }
    const content = data?.result?.content || [];
    const texts = content.map((c) => (c.type === 'text' ? c.text : JSON.stringify(c))).join('\n').slice(0, 12000);
    return { content: [{ type: 'text', text: texts || '（无结果）' }], details: {} };
  } catch (e) {
    return { content: [{ type: 'text', text: '网络搜索失败，请稍后重试' }], details: {} };
  }
}

// ---- one-api 管理工具 ----
function makeTools({ getToken }) {
  const call = async (p, opts = {}) => {
    try {
      const token = typeof getToken === 'function' ? getToken() : '';
      const resp = await fetch(`${ONEAPI_BASE}${p}`, {
        method: opts.method || 'GET',
        body: opts.body ? JSON.stringify(opts.body) : undefined,
        headers: {
          'Content-Type': 'application/json',
          Authorization: `Bearer ${token}`,
        },
        signal: AbortSignal.timeout(30000),
      });
      const text = await resp.text();
      return {
        content: [{ type: 'text', text: `HTTP ${resp.status}: ${text.slice(0, MAX_RESPONSE_LENGTH)}` }],
        details: { status: resp.status },
      };
    } catch (e) {
      return { content: [{ type: 'text', text: '请求 one-api 失败，请稍后重试' }], details: {} };
    }
  };

  const tool = (name, label, description, params, executor) =>
    defineTool({
      name,
      label,
      description,
      parameters: params,
      execute: async (_callId, args) => executor(args),
    });

  return [
    tool('get_current_time', 'Current Time', '获取当前时间', Type.Object({}), async () => ({
      content: [{ type: 'text', text: new Date().toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai' }) }],
      details: {},
    })),
    tool('get_status', 'System Status', '获取 one-api 系统状态', Type.Object({}), async () => call('/api/status')),
    tool('list_available_models', 'Available Models', '列出当前用户可用的模型', Type.Object({}), async () => call('/api/models')),
    tool('list_all_models', 'All Models (admin)', '列出全部模型（管理员）', Type.Object({}), async () => call('/api/channel/models')),
    tool('get_self', 'My Account', '获取当前用户信息', Type.Object({}), async () => call('/api/user/self')),
    tool('list_tokens', 'My Tokens', '列出我的 API 令牌', Type.Object({}), async () => call('/api/token/?p=0&size=100')),
    tool('list_channels', 'Channels (admin)', '列出所有渠道（管理员）', Type.Object({}), async () => call('/api/channel/?p=0&size=100')),
    tool('get_channel', 'Channel Detail (admin)', '获取渠道详情（管理员）', Type.Object({ id: Type.Number({ description: '渠道 ID' }) }), async (a) => call(`/api/channel/${a.id}`)),
    tool('test_channel', 'Test Channel (admin)', '通过 one-api 管理 API 测试渠道连通性（管理员），会记录测试日志', Type.Object({ id: Type.Number({ description: '渠道 ID' }) }), async (a) => {
      const r = await call(`/api/channel/test/${a.id}`);
      const raw = r.content?.[0]?.text || '';
      try {
        const m = raw.match(/\{.*\}$/s);
        const j = JSON.parse(m ? m[0] : '{}');
        if (j.success === true) {
          return { content: [{ type: 'text', text: `渠道 #${a.id} 测试成功：${j.message || ''}（耗时 ${j.time ?? '?'} 秒）` }], details: r.details };
        }
        return { content: [{ type: 'text', text: `渠道 #${a.id} 测试失败：${j.message || raw}` }], details: r.details };
      } catch (e) {
        return r;
      }
    }),
    tool('add_channel', 'Add Channel (admin)', '新增渠道（管理员），channel 对象包含 name/type/key/models/group/base_url 等字段', Type.Object({ channel: Type.Record(Type.String(), Type.Any(), { description: '渠道配置对象' }) }), async (a) => call('/api/channel/', { method: 'POST', body: a.channel })),
    tool('update_channel', 'Update Channel (admin)', '更新渠道（管理员）：channel 对象必须包含 id，其余字段为要修改的内容；内部先读取现有配置合并后全量提交，未修改字段保持不变（key 无法通过本工具修改）', Type.Object({ channel: Type.Record(Type.String(), Type.Any(), { description: '渠道配置对象（含 id）' }) }), async (a) => {
      // dyt-93: 后端 PUT 为全量语义，部分提交会把 status/type/models 等抹零。
      // 这里先 GET 现有配置合并后提交；key 置空走后端"空 key 保留"逻辑，避免脱敏 key 覆盖密钥
      const cur = await call(`/api/channel/${a.channel.id}`);
      try {
        const m = cur.content?.[0]?.text?.match(/\{.*\}$/s);
        const j = JSON.parse(m ? m[0] : '{}');
        if (j.success && j.data && typeof j.data === 'object') {
          const merged = { ...j.data, ...a.channel, key: '' };
          return call('/api/channel/', { method: 'PUT', body: merged });
        }
        return { content: [{ type: 'text', text: '读取渠道详情失败，无法安全更新' }], details: {} };
      } catch (e) {
        return { content: [{ type: 'text', text: '读取渠道详情解析失败，已取消更新以防字段被清空' }], details: {} };
      }
    }),
    tool('delete_channel', 'Delete Channel (admin)', '删除渠道（管理员）', Type.Object({ id: Type.Number({ description: '渠道 ID' }) }), async (a) => call(`/api/channel/${a.id}/`, { method: 'DELETE' })),
    tool('clone_channel', 'Clone Channel (admin)', '复制渠道（管理员）：保留全部配置与 Key 创建新渠道，新渠道默认启用（复制渠道请用本工具，不要用 add_channel 手动重建，避免密钥脱敏无法复制）', Type.Object({ id: Type.Number({ description: '要复制的渠道 ID' }) }), async (a) => call(`/api/channel/clone/${a.id}`, { method: 'POST' })),
    tool('fetch_channel_models', 'Fetch Channel Models (admin)', '从渠道上游探测模型列表（管理员）', Type.Object({ id: Type.Number({ description: '渠道 ID' }) }), async (a) => call(`/api/channel/fetch-models/${a.id}`)),
    tool('sort_channels', 'Sort Channels (admin)', '渠道排序（管理员）：body 为渠道 ID 数组，顺序即展示/分配优先级', Type.Object({ ids: Type.Array(Type.Number(), { description: '渠道 ID 数组，按期望顺序排列' }) }), async (a) => {
      // dyt-93: Go 侧无 /api/channel/sort 接口，逐渠道更新 priority 模拟排序
      const results = [];
      const n = a.ids.length;
      for (let i = 0; i < n; i++) {
        const priority = n - i; // 首位最高
        const r = await call('/api/channel/priority', { method: 'PUT', body: { id: a.ids[i], priority } });
        results.push(`#${a.ids[i]}=${priority} ${r.content?.[0]?.text?.slice(0, 60) || ''}`);
      }
      return { content: [{ type: 'text', text: '排序完成：\n' + results.join('\n') }], details: {} };
    }),
    tool('update_channel_balance', 'Update Channel Balance (admin)', '刷新渠道余额（管理员）', Type.Object({ id: Type.Number({ description: '渠道 ID，缺省 0 为全部' }) }), async (a) => call(`/api/channel/update_balance/${a.id || 0}`)),
    tool('delete_disabled_channels', 'Delete Disabled Channels (admin)', '删除所有已禁用渠道（管理员）', Type.Object({}), async () => call('/api/channel/disabled', { method: 'DELETE' })),
    tool('add_token', 'Add Token', '新增 API 令牌（自己的），token 对象包含 name/expired_time/remain_quota/limit_quota/model_limit_enabled 等字段', Type.Object({ token: Type.Record(Type.String(), Type.Any(), { description: '令牌配置对象' }) }), async (a) => call('/api/token/', { method: 'POST', body: a.token })),
    tool('update_token', 'Update Token', '更新 API 令牌（自己的）：token 对象必须包含 id，其余字段为要修改的内容；内部先读取现有配置合并后全量提交，未修改字段保持不变', Type.Object({ token: Type.Record(Type.String(), Type.Any(), { description: '令牌配置对象（含 id）' }) }), async (a) => {
      // dyt-93: 后端 PUT 是全量语义，部分提交会把 expired_time/remain_quota 等置 0
      const cur = await call(`/api/token/${a.token.id}`);
      try {
        const m = cur.content?.[0]?.text?.match(/\{.*\}$/s);
        const j = JSON.parse(m ? m[0] : '{}');
        if (j.success && j.data && typeof j.data === 'object') {
          const merged = { ...j.data, ...a.token };
          return call('/api/token/', { method: 'PUT', body: merged });
        }
        return { content: [{ type: 'text', text: '读取令牌详情失败，无法安全更新' }], details: {} };
      } catch (e) {
        return { content: [{ type: 'text', text: '读取令牌详情解析失败，已取消更新以防字段被置零' }], details: {} };
      }
    }),
    tool('delete_token', 'Delete Token', '删除 API 令牌（自己的）', Type.Object({ id: Type.Number({ description: '令牌 ID' }) }), async (a) => call(`/api/token/${a.id}`, { method: 'DELETE' })),
    tool('add_user', 'Add User (admin)', '新增用户（管理员），user 对象包含 username/password/display_name/quota 等字段', Type.Object({ user: Type.Record(Type.String(), Type.Any(), { description: '用户配置对象' }) }), async (a) => call('/api/user/', { method: 'POST', body: a.user })),
    tool('update_user', 'Update User (admin)', '更新用户（管理员）：user 对象必须包含 id，其余字段为要修改的内容；内部先读取现有配置合并后全量提交，未修改字段保持不变', Type.Object({ user: Type.Record(Type.String(), Type.Any(), { description: '用户配置对象（含 id）' }) }), async (a) => {
      // dyt-93: 后端 PUT 为全量语义且 role/quota 差异即触发更新（部分提交会把未传字段清零/降权）
      const cur = await call(`/api/user/${a.user.id}`);
      try {
        const m = cur.content?.[0]?.text?.match(/\{.*\}$/s);
        const j = JSON.parse(m ? m[0] : '{}');
        if (j.success && j.data && typeof j.data === 'object') {
          const merged = { ...j.data, ...a.user, password: a.user.password || '' };
          return call('/api/user/', { method: 'PUT', body: merged });
        }
        return { content: [{ type: 'text', text: '读取用户详情失败，无法安全更新' }], details: {} };
      } catch (e) {
        return { content: [{ type: 'text', text: '读取用户详情解析失败，已取消更新以防字段被清零' }], details: {} };
      }
    }),
    tool('delete_user', 'Delete User (admin)', '删除用户（管理员）', Type.Object({ id: Type.Number({ description: '用户 ID' }) }), async (a) => call(`/api/user/${a.id}`, { method: 'DELETE' })),
    tool('manage_user', 'Manage User (admin)', '管理用户（管理员）：重置密码/调整额度等，body 含 id 与要修改的字段', Type.Object({ body: Type.Record(Type.String(), Type.Any(), { description: '管理操作对象（含 id）' }) }), async (a) => call('/api/user/manage', { method: 'POST', body: a.body })),
    tool('get_options', 'System Options (admin)', '获取系统配置（管理员，只读）', Type.Object({}), async () => call('/api/option/')),
    tool('web_search', 'Web Search', '互联网搜索（AnySearch，匿名）：查询最新信息、新闻、文档、教程等', Type.Object({ query: Type.String({ description: '搜索关键词' }), max_results: Type.Optional(Type.Number({ description: '结果数量，默认 5，最大 10' })) }), async (a) => anysearchCall('search', { query: a.query, max_results: Math.min(Math.max(a.max_results || 5, 1), 10) })),
    tool('web_extract', 'Web Extract', '抓取网页正文为 Markdown（AnySearch，匿名）：用于阅读文章/文档/官方页面内容', Type.Object({ url: Type.String({ description: '页面 URL' }) }), async (a) => anysearchCall('extract', { url: a.url })),
    tool('list_users', 'Users (admin)', '列出所有用户（管理员）', Type.Object({}), async () => call('/api/user/?p=0&size=100')),
    tool('get_user', 'User Detail (admin)', '获取用户详情（管理员）', Type.Object({ id: Type.Number({ description: '用户 ID' }) }), async (a) => call(`/api/user/${a.id}`)),
    tool('list_logs', 'Logs (admin)', '查询使用日志（管理员）', Type.Object({ modelName: Type.Optional(Type.String({ description: '按模型筛选' })), channelId: Type.Optional(Type.Number({ description: '按渠道筛选' })) }), async (a) => {
      const q = new URLSearchParams({ p: '0', size: '20' });
      if (a.modelName) q.set('model_name', a.modelName);
      // dyt-93: 后端参数名是 channel（不是 channel_id）
      if (a.channelId) q.set('channel', String(a.channelId));
      return call(`/api/log/?${q.toString()}`);
    }),
    tool('get_fail_logs', 'Fail Logs (admin)', '查询最近失败日志（管理员）', Type.Object({}), async () => call('/api/log/fail/list?p=0&size=20')),
    tool('list_own_logs', 'My Logs', '查询我自己的使用日志', Type.Object({ modelName: Type.Optional(Type.String()) }), async (a) => {
      const q = new URLSearchParams({ p: '0', size: '20' });
      if (a.modelName) q.set('model_name', a.modelName);
      return call(`/api/log/self?${q.toString()}`);
    }),
  ];
}

// ---- session 管理 ----
const sessions = new Map(); // session_id -> { session, busy }

// dyt-93: 请求体统一读取，上限 16MB（仅监听 127.0.0.1，但防内网其他进程/代理打爆内存）
const BODY_LIMIT = 16 * 1024 * 1024;
async function readBody(req, res) {
  const chunks = [];
  let total = 0;
  for await (const c of req) {
    total += c.length;
    if (total > BODY_LIMIT) {
      res.writeHead(413, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ error: 'payload too large' }));
      req.destroy(); // 终止连接，停止继续传输
      return null;
    }
    chunks.push(c);
  }
  return Buffer.concat(chunks).toString('utf8');
}

function writeSse(res, obj) {
  if (res.writableEnded) return;
  try { res.write(`data: ${JSON.stringify(obj)}\n\n`); } catch (_) { /* client disconnected */ }
}

async function handleChat(req, res) {
  const raw = await readBody(req, res);
  if (raw === null) return;
  let body;
  try {
    body = JSON.parse(raw);
  } catch (e) {
    res.writeHead(400, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ error: 'invalid json' }));
    return;
  }
  const { session_id, model, message, token_key, access_token, channel_id, thinking_level } = body;
  console.log(`[/chat] session=${session_id} model=${model} msg=${String(message || '').slice(0, 50)} channel=${channel_id || '-'}`);
  // 模型表用登录用户的令牌同步：首次/过期时【同步等待】完成，确保 find 前表已就绪
  // （此前为异步不等待，首次请求时表尚未生成导致误报"模型不在模型表中"，重试才成功）
  if (token_key && (Date.now() - lastSync > 60_000 || !fs.existsSync(MODELS_PATH))) {
    const n = await syncModels(token_key);
    console.log(`[models] synced ${n} via user token`);
  }
  if (!session_id || !model || !message) {
    res.writeHead(400, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ error: 'session_id/model/message required' }));
    return;
  }

  res.writeHead(200, {
    'Content-Type': 'text/event-stream',
    'Cache-Control': 'no-cache',
    Connection: 'keep-alive',
    'X-Accel-Buffering': 'no',
  });

  try {
    await ensureModels(false);

    const authStorage = AuthStorage.inMemory();
    if (token_key) authStorage.setRuntimeApiKey('oneapi', token_key);
    const modelRegistry = ModelRegistry.create(authStorage, MODELS_PATH);
    const piModel = modelRegistry.find('oneapi', model);
    if (!piModel) {
      writeSse(res, { type: 'error', message: `模型 ${model} 不在模型表中（one-api 模型同步失败或模型不存在）` });
      res.end();
      return;
    }

    let holder = sessions.get(session_id);
    // 会话归属校验：session_id 绑定的用户必须与请求用户一致，防止跨用户会话窃取
    if (holder && holder.userId > 0 && Number(body.user_id) !== holder.userId) {
      // 注意：SSE 头已在上面 writeHead，这里只写事件与结束，不能再次 writeHead
      writeSse(res, { type: 'error', message: '会话不属于当前用户' });
      writeSse(res, { type: 'done' });
      res.end();
      return;
    }
    // dyt-93: 孤儿会话（持久化恢复且无 userId 归属，旧数据）禁止继续对话
    if (holder && !holder.pending && holder.userId <= 0) {
      writeSse(res, { type: 'error', message: '会话已失效，请新开会话' });
      writeSse(res, { type: 'done' });
      res.end();
      return;
    }
    // 工具调用凭据：当前登录用户自己的 access_token（每次 /chat 请求更新），
    // 不使用部署级管理员令牌，避免任意用户通过 Agent 以管理员身份操作
    const tools = makeTools({ getToken: () => holder?.accessToken || '' });
    if (!holder) {
      // 占位（防并发首请求双创建）：创建完成前 resume/后续请求见 busy 状态
      const placeholder = { busy: true, events: [], subscribers: new Set(), lastActive: Date.now(), userId: body.user_id || 0, pending: true };
      sessions.set(session_id, placeholder);
      try {
      // compaction: pi 原生上下文压缩，保持默认开启，防止历史膨胀
      const settingsManager = SettingsManager.inMemory({});
      const loader = new DefaultResourceLoader({
        cwd: process.cwd(),
        agentDir: AGENT_DIR,
        settingsManager,
        systemPromptOverride: () => SYSTEM_PROMPT,
      });
      await loader.reload();
      const { session } = await createAgentSession({
        model: piModel,
        thinkingLevel: 'off',
        modelRegistry,
        resourceLoader: loader,
        // 工具调用凭据：当前登录用户自己的 access_token（每次 /chat 请求更新）
        customTools: tools,
        channelId: channel_id || 0,
        sessionManager: SessionManager.inMemory(),
        settingsManager,
      });
      holder = {
        session,
        busy: false,
        busySince: 0,
        userId: body.user_id || 0,
        modelRegistry,
        authStorage,
        tokenKey: token_key || '',
        accessToken: access_token || '',
        events: [],
        subscribers: new Set(),
        toolCount: 0,
        loopAborted: false,
        epoch: 0,
        lastActive: Date.now(),
      };
      if (thinking_level) {
        try { session.setThinkingLevel(thinking_level); } catch (e) { /* 非法等级忽略 */ }
      }
      sessions.set(session_id, holder);

      // 迁移占位期间（创建过程）挂上的订阅者，然后覆盖占位
      for (const sub of placeholder.subscribers) holder.subscribers.add(sub);
      sessions.set(session_id, holder);
      session.subscribe((event) => {
        const emit = (obj) => {
          holder.events.push(obj);
          if (holder.events.length > MAX_EVENTS_PER_SESSION) holder.events.shift();
          for (const sub of holder.subscribers) writeSse(sub, obj);
          scheduleSave();
        };
        switch (event.type) {
          case 'message_update': {
            const ev = event.assistantMessageEvent;
            if (ev.type === 'text_delta') emit({ type: 'delta', content: ev.delta });
            else if (ev.type === 'thinking_delta') emit({ type: 'thinking', content: ev.delta });
            break;
          }
          case 'tool_execution_start':
            holder.toolCount++;
            emit({ type: 'tool_start', tool: event.toolName, args: event.args });
            if (holder.toolCount > 15) {
              holder.loopAborted = true;
              try { holder.session.abort(); } catch (_) { /* ignore */ }
              emit({ type: 'error', message: '工具调用次数过多（模型可能不支持工具调用），已中止，请更换模型' });
            }
            break;
          case 'tool_execution_end':
            emit({
              type: 'tool_end',
              tool: event.toolName,
              ok: !event.isError,
              result: typeof event.result === 'string' ? event.result.slice(0, MAX_RESULT_LENGTH) : JSON.stringify(event.result).slice(0, MAX_RESULT_LENGTH),
            });
            break;
          default:
            break;
        }
      });
      } catch (e) {
        // 创建失败：清理占位，给占位期挂上的订阅者收尾，避免前端悬挂
        sessions.delete(session_id);
        for (const sub of placeholder.subscribers) {
          try { writeSse(sub, { type: 'error', message: '会话创建失败' }); sub.end(); } catch (_) { /* ignore */ }
        }
        throw e;
      }
    } else {
      // 占位期（会话创建中）：等待创建完成，避免对半成品报错
      if (holder.pending) {
        const t0 = Date.now();
        while (holder.pending && Date.now() - t0 < 30000) {
          await new Promise((r) => setTimeout(r, 200));
          holder = sessions.get(session_id);
          if (!holder) break;
        }
        if (!holder || holder.pending) {
          writeSse(res, { type: 'error', message: '会话创建中，请稍后重试' });
          res.end();
          return;
        }
      }
      if (holder.busy) {
        // 上一轮仍在执行（页面刷新后重发等）：中止旧执行，新消息取代之
        // pi 会话保留全部历史，abort 后可直接 prompt 新消息
        try { holder.session.abort(); } catch (e) { /* ignore */ }
        for (const old of holder.subscribers) {
          if (old !== res) {
            try { writeSse(old, { type: 'error', message: '新消息已取代本执行' }); old.end(); } catch (_) { /* ignore */ }
          }
        }
        holder.busy = false;
        holder.events = [];
      }
      await holder.session.setModel(piModel);
      if (thinking_level) {
        try { holder.session.setThinkingLevel(thinking_level); } catch (e) { /* 非法等级忽略 */ }
      }
      if (token_key && token_key !== holder.tokenKey) {
        holder.tokenKey = token_key;
        await holder.authStorage.setRuntimeApiKey('oneapi', token_key);
      }
      if (access_token && access_token !== holder.accessToken) {
        holder.accessToken = access_token;
      }
      if (!holder.userId && body.user_id) {
        holder.userId = body.user_id;
      }
      // 工具集可能随版本更新：运行时同步，旧会话立即获得新工具
      try {
        await holder.session.setTools(tools);
      } catch (_) { /* 忽略 */ }
    }

    // dyt-93: 新消息 = 新意图：若非执行中（busy），清除停止标记，
    // 避免"停止后首条消息被吞"（仅占位期创建中被打断的场景才拦截）
    if (holder.stopped && !holder.busy) {
      holder.stopped = false;
    }
    holder.busy = true;
    holder.busySince = Date.now();
    holder.toolCount = 0;
    holder.loopAborted = false;
    holder.lastActive = Date.now();
    // 本 prompt 的事件缓冲（/resume 按此重放当前执行进度）
    holder.events = [];
    holder.subscribers.add(res);
    res.on('close', () => holder.subscribers.delete(res));
    // epoch：本执行代次；被新消息取代后旧执行的 finally 不再触碰新状态
    holder.epoch = (holder.epoch || 0) + 1;
    const epoch = holder.epoch;
    // 占位期（创建中）用户已点停止：本次不再执行
    if (holder.stopped) {
      holder.stopped = false;
      holder.busy = false;
      writeSse(res, { type: 'error', message: '已停止' });
      writeSse(res, { type: 'done' });
      res.end();
      return;
    }
    try {
      await Promise.race([
        holder.session.prompt(message),
        new Promise((_, reject) =>
          setTimeout(() => reject(new Error('Agent 执行超时（5min），请重试')), AGENT_EXEC_TIMEOUT_MS)
        ),
      ]);
    } catch (e) {
      try { holder.session.abort(); } catch (_) { /* ignore */ }
      if (epoch === holder.epoch) writeSse(res, { type: 'error', message: '请求处理失败' });
    } finally {
      if (epoch === holder.epoch) {
        holder.busy = false;
        holder.lastActive = Date.now();
        const subs = [...holder.subscribers];
        holder.subscribers.clear();
        for (const sub of subs) {
          writeSse(sub, { type: 'done' });
          sub.end();
        }
      }
    }
  } catch (e) {
    writeSse(res, { type: 'error', message: '请求处理失败' });
    res.end();
  }
}

async function handleResume(req, res) {
  const raw = await readBody(req, res);
  if (raw === null) return;
  let body;
  try {
    body = JSON.parse(raw);
  } catch (e) {
    res.writeHead(400, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ error: 'invalid json' }));
    return;
  }
  const { session_id, user_id } = body;
  const holder = sessions.get(session_id);
  if (holder && holder.userId > 0 && Number(user_id) !== holder.userId) {
    res.writeHead(200, {
      'Content-Type': 'text/event-stream',
      'Cache-Control': 'no-cache',
      Connection: 'keep-alive',
      'X-Accel-Buffering': 'no',
    });
    writeSse(res, { type: 'done' });
    res.end();
    return;
  }
  if (!holder) {
    // 会话不存在 = 该轮已结束：返回 200 + done，前端静默收尾（不报 404 错误）
    res.writeHead(200, {
      'Content-Type': 'text/event-stream',
      'Cache-Control': 'no-cache',
      Connection: 'keep-alive',
      'X-Accel-Buffering': 'no',
    });
    writeSse(res, { type: 'done' });
    res.end();
    return;
  }
  res.writeHead(200, {
    'Content-Type': 'text/event-stream',
    'Cache-Control': 'no-cache',
    Connection: 'keep-alive',
    'X-Accel-Buffering': 'no',
  });
  holder.lastActive = Date.now();
  // 重放已产生的事件
  for (const e of holder.events) writeSse(res, e);
  if (!holder.busy) {
    writeSse(res, { type: 'done' });
    res.end();
    return;
  }
  // 会话仍在执行：挂接续推
  holder.subscribers.add(res);
  res.on('close', () => holder.subscribers.delete(res));
}


// ---------- 聊天后台会话（轻量）：只做上游转发 + 事件缓冲 + 断点续传，不加载 pi ----------
const chatSessions = new Map(); // session_id -> { events, busy, subscribers, controller }

async function handleChatV1(req, res) {
  const raw = await readBody(req, res);
  if (raw === null) return;
  let body;
  try {
    body = JSON.parse(raw);
  } catch (e) {
    res.writeHead(400, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ error: 'invalid json' }));
    return;
  }
  const { session_id, model, messages, token_key, channel_id, thinking_level, user_id } = body;
  console.log(`[chat/v1] session=${session_id} model=${model} msgs=${Array.isArray(messages) ? messages.length : 0} channel=${channel_id || '-'} thinking=${thinking_level || '-'}`);
  if (!session_id || !model || !Array.isArray(messages) || messages.length === 0 || !token_key) {
    res.writeHead(400, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ error: 'session_id/model/messages/token_key required' }));
    return;
  }
  res.writeHead(200, {
    'Content-Type': 'text/event-stream',
    'Cache-Control': 'no-cache',
    Connection: 'keep-alive',
    'X-Accel-Buffering': 'no',
  });
  let holder = chatSessions.get(session_id);
  if (holder && holder.userId > 0 && Number(user_id) !== holder.userId) {
    // 注意：SSE 头已在上面 writeHead，这里只写事件与结束，不能再次 writeHead
    writeSse(res, { type: 'error', message: '会话不属于当前用户' });
    writeSse(res, { type: 'done' });
    res.end();
    return;
  }
  if (!holder) {
    holder = { events: [], busy: false, subscribers: new Set(), controller: null, epoch: 0, lastActive: Date.now(), userId: user_id || 0 };
    chatSessions.set(session_id, holder);
  } else if (!holder.userId && user_id) {
    holder.userId = user_id;
  }
  if (holder.busy) {
    // 上一轮仍在执行（刷新后重发等）：中止旧执行，新消息取代
    try { holder.controller.abort(); } catch (_) { /* ignore */ }
    for (const old of holder.subscribers) {
      if (old !== res) {
        try { writeSse(old, { type: 'error', message: '新消息已取代本执行' }); old.end(); } catch (_) { /* ignore */ }
      }
    }
    holder.busy = false;
    holder.events = [];
  }
  holder.events = [];
  holder.busy = true;
  holder.lastActive = Date.now();
  holder.subscribers.add(res);
  res.on('close', () => holder.subscribers.delete(res));
  holder.epoch = (holder.epoch || 0) + 1;
  const epoch = holder.epoch;

  const emit = (obj) => {
    holder.events.push(obj);
    if (holder.events.length > MAX_EVENTS_PER_SESSION) holder.events.shift();
    holder.lastActive = Date.now();
    for (const sub of holder.subscribers) writeSse(sub, obj);
    scheduleSave();
  };

  const controller = new AbortController();
  holder.controller = controller;
  try {
    const upstreamBody = { model, messages, stream: true };
    if (channel_id) upstreamBody.channel_id = channel_id;
    if (thinking_level && thinking_level !== 'off') upstreamBody.reasoning_effort = thinking_level;
    const resp = await fetch(ONEAPI_BASE + '/v1/chat/completions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token_key}` },
      body: JSON.stringify(upstreamBody),
      signal: controller.signal,
    });
    if (!resp.ok) {
      let msg = `上游错误 HTTP ${resp.status}`;
      try {
        const j = await resp.json();
        msg = j.error?.message || j.message || msg;
      } catch (_) { /* ignore */ }
      console.log(`[chat/v1] upstream error: ${msg}`);
      emit({ type: 'error', message: msg });
    } else if (!resp.body) {
      emit({ type: 'error', message: '上游无响应体' });
    } else {
      const reader = resp.body.getReader();
      const decoder = new TextDecoder();
      let buf = '';
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        buf += decoder.decode(value, { stream: true });
        const lines = buf.split('\n');
        buf = lines.pop();
        for (const line of lines) {
          const s = line.trim();
          if (!s.startsWith('data:')) continue;
          const data = s.slice(5).trim();
          if (data === '[DONE]') continue;
          try {
            const j = JSON.parse(data);
            const d = j.choices?.[0]?.delta;
            if (d?.content) emit({ type: 'delta', content: d.content });
            if (d?.reasoning_content) emit({ type: 'thinking', content: d.reasoning_content });
          } catch (_) { /* 忽略无法解析的行 */ }
        }
      }
    }
  } catch (e) {
    if (e.name !== 'AbortError' && epoch === holder.epoch) emit({ type: 'error', message: '工具执行失败' });
  } finally {
    if (epoch === holder.epoch) {
      holder.busy = false;
      holder.controller = null;
      holder.lastActive = Date.now();
      const subs = [...holder.subscribers];
      holder.subscribers.clear();
      for (const sub of subs) {
        writeSse(sub, { type: 'done' });
        sub.end();
      }
    }
  }
}

function handleChatResume(req, res) {
  const rawPromise = readBody(req, res).catch(() => null);
  let session_id = '';
  rawPromise.then((raw) => {
    if (raw === null) {
      // dyt-93: 请求被拒绝（超限）或读取出错时收尾响应，防止客户端悬挂
      try { if (!res.writableEnded) { res.writeHead(400, { 'Content-Type': 'application/json' }); res.end(JSON.stringify({ error: 'bad request' })); } } catch (_) { /* ignore */ }
      return;
    }
    let user_id = 0;
    try {
      const p = JSON.parse(raw);
      session_id = p.session_id;
      user_id = p.user_id || 0;
    } catch (_) { /* ignore */ }
    const holder = chatSessions.get(session_id);
    if (holder && holder.userId > 0 && Number(user_id) !== holder.userId) {
      res.writeHead(200, {
        'Content-Type': 'text/event-stream',
        'Cache-Control': 'no-cache',
        Connection: 'keep-alive',
        'X-Accel-Buffering': 'no',
      });
      writeSse(res, { type: 'done' });
      res.end();
      return;
    }
    if (holder) holder.lastActive = Date.now();
    console.log(`[chat/v1/resume] session=${session_id} exists=${!!holder} busy=${holder?.busy || false} events=${holder?.events?.length || 0}`);
    if (!holder) {
      // 会话不存在 = 该轮已结束：返回 200 + done，前端静默收尾（不报 404 错误）
      res.writeHead(200, {
        'Content-Type': 'text/event-stream',
        'Cache-Control': 'no-cache',
        Connection: 'keep-alive',
        'X-Accel-Buffering': 'no',
      });
      writeSse(res, { type: 'done' });
      res.end();
      return;
    }
    res.writeHead(200, {
      'Content-Type': 'text/event-stream',
      'Cache-Control': 'no-cache',
      Connection: 'keep-alive',
      'X-Accel-Buffering': 'no',
    });
    for (const e of holder.events) writeSse(res, e);
    if (!holder.busy) {
      writeSse(res, { type: 'done' });
      res.end();
      return;
    }
    holder.subscribers.add(res);
    res.on('close', () => holder.subscribers.delete(res));
  });
}


// ---- 停止生成（用户点击停止：中止后台执行，区别于刷新断线）----
function handleStop(req, res) {
  const rawPromise = readBody(req, res).catch(() => null);
  rawPromise.then((raw) => {
    if (raw === null) {
      try { if (!res.writableEnded) { res.writeHead(400, { 'Content-Type': 'application/json' }); res.end(JSON.stringify({ error: 'bad request' })); } } catch (_) { /* ignore */ }
      return;
    }
    let sid = '';
    let kind = 'agent';
    let user_id = 0;
    try {
      const p = JSON.parse(raw);
      sid = p.session_id;
      kind = p.kind || 'agent';
      user_id = p.user_id || 0;
    } catch (_) { /* ignore */ }
    const holder = kind === 'chat' ? chatSessions.get(sid) : sessions.get(sid);
    if (holder) {
      if (holder.userId > 0 && Number(user_id) !== holder.userId) {
        res.writeHead(403, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ error: 'session belongs to another user' }));
        return;
      }
      // 递增 epoch：使正在执行/等待执行的 finally 失去代次；stopped 标记阻止占位期后开始执行
      holder.epoch = (holder.epoch || 0) + 1;
      holder.stopped = true;
      try { holder.session?.abort(); } catch (_) { /* ignore */ }
      try { holder.controller?.abort(); } catch (_) { /* ignore */ }
      const subs = [...holder.subscribers];
      holder.subscribers.clear();
      for (const sub of subs) {
        try { writeSse(sub, { type: 'error', message: '已停止' }); sub.end(); } catch (_) { /* ignore */ }
      }
      holder.busy = false;
      holder.lastActive = Date.now();
    }
    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: true }));
  });
}
// ---- 会话事件持久化（容器重启后 resume 仍可重放历史）----
const SESSION_STORE = process.env.SESSION_STORE || '/data/pi-sessions.json';

function loadSessions() {
  try {
    const raw = fs.readFileSync(SESSION_STORE, 'utf8');
    const data = JSON.parse(raw);
    for (const [sid, rec] of Object.entries(data.chat || {})) {
      // dyt-93: 兼容旧格式（纯数组）与新格式（{events, userId}）
      const evs = Array.isArray(rec) ? rec : (rec && rec.events) || [];
      const holder = chatSessions.get(sid) || { events: [], busy: false, subscribers: new Set(), controller: null, lastActive: Date.now() };
      holder.events = Array.isArray(evs) ? evs : [];
      // dyt-93: 恢复 userId，保证重启后会话归属校验仍生效；旧数据无 userId 的孤儿会话
      // 直接丢弃（否则任意用户猜中 session_id 即可 resume 重放对话全文）
      if (!Array.isArray(rec) && rec && rec.userId) {
        holder.userId = rec.userId;
      } else {
        continue;
      }
      holder.lastActive = holder.lastActive || Date.now();
      chatSessions.set(sid, holder);
    }
    for (const [sid, evs] of Object.entries(data.agent || {})) {
      const holder = sessions.get(sid);
      if (holder && Array.isArray(evs)) holder.events = evs;
    }
    return true;
  } catch (e) {
    return false;
  }
}

let saveTimer = null;
// 定期清理：
// - 24 小时无活跃的会话（busy 会话不清）
// - busy 超过 1 小时视为卡死（上次审计发现的泄漏场景），强制清理
// - 全局会话总数上限 MAX_SESSIONS，超出时淘汰最旧的非 busy 会话
const MAX_SESSIONS = 500;
const BUSY_STALE_MS = 3600 * 1000;
setInterval(() => {
  const cutoff = Date.now() - 24 * 3600 * 1000;
  const busyCutoff = Date.now() - BUSY_STALE_MS;
  const abortHolder = (h) => {
    try { h.session?.abort?.(); } catch (e) { /* ignore */ }
    try { h.controller?.abort?.(); } catch (e) { /* ignore */ }
  };
  for (const [sid, h] of sessions) {
    if (h.busy && h.lastActive && h.lastActive < busyCutoff) {
      abortHolder(h);
      sessions.delete(sid);
      continue;
    }
    if (!h.busy && h.lastActive && h.lastActive < cutoff) sessions.delete(sid);
  }
  for (const [sid, h] of chatSessions) {
    if (h.busy && h.lastActive && h.lastActive < busyCutoff) {
      abortHolder(h);
      chatSessions.delete(sid);
      continue;
    }
    if (!h.busy && h.lastActive && h.lastActive < cutoff) chatSessions.delete(sid);
  }
  // 全局上限：超出时按 lastActive 最旧优先淘汰（busy 会话不淘汰）
  if (sessions.size + chatSessions.size > MAX_SESSIONS) {
    const all = [];
    for (const [sid, h] of sessions) if (!h.busy) all.push([sid, h, 0]);
    for (const [sid, h] of chatSessions) if (!h.busy) all.push([sid, h, 1]);
    all.sort((a, b) => (a[1].lastActive || 0) - (b[1].lastActive || 0));
    let toEvict = sessions.size + chatSessions.size - MAX_SESSIONS;
    for (const [sid, h, kind] of all) {
      if (toEvict <= 0) break;
      if (kind === 0) sessions.delete(sid);
      else chatSessions.delete(sid);
      toEvict--;
    }
  }
}, 30 * 60 * 1000);

function scheduleSave() {
  if (saveTimer) return;
  saveTimer = setTimeout(() => {
    saveTimer = null;
    try {
      const chat = {};
      for (const [sid, h] of chatSessions) {
        if (h.events && h.events.length > 0) {
          // dyt-93: 持久化 userId，重启后会话归属校验仍生效
          chat[sid] = { events: h.events.slice(-MAX_EVENTS_PER_SESSION), userId: h.userId || 0 };
        }
      }
      // agent 会话为一次性执行上下文，且事件含工具参数/结果等敏感内容，不落盘
      fs.mkdirSync(require('path').dirname(SESSION_STORE), { recursive: true });
      // dyt-93: 0600（内容含用户对话全文）
      fs.writeFileSync(SESSION_STORE, JSON.stringify({ chat, agent: {} }), { mode: 0o600 });
    } catch (e) { /* 忽略持久化失败 */ }
  }, 3000);
}
const server = http.createServer((req, res) => {
  if (req.method === 'GET' && req.url === '/health') {
    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: true, sessions: sessions.size }));
    return;
  }
  // dyt-96: 除 /health 外一律要求共享密钥（未配置密钥 = 拒绝服务）
  if (!requireAuth(req, res)) return;
  if (req.method === 'POST' && req.url === '/chat') {
    handleChat(req, res);
  } else if (req.method === 'POST' && req.url === '/resume') {
    handleResume(req, res);
  } else if (req.method === 'POST' && req.url === '/chat/v1') {
    handleChatV1(req, res);
  } else if (req.method === 'POST' && req.url === '/chat/v1/resume') {
    handleChatResume(req, res);
  } else if (req.method === 'POST' && req.url === '/stop') {
    handleStop(req, res);
  } else if (req.method === 'POST' && req.url === '/sync-models') {
    syncModels().then((n) => {
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ ok: true, models: n }));
    });
  } else {
    res.writeHead(404, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ error: 'not found' }));
  }
});

loadSessions();
setInterval(scheduleSave, 15000);

// dyt-93: 清理逻辑已在上方统一（agent/chat 会话、busy 卡死、全局上限），此处不再重复声明

function shutdown() {
  console.log('[shutdown] closing server...');
  saveTimer && clearTimeout(saveTimer);
  for (const [, h] of sessions) {
    try { h.session?.abort(); } catch (_) { /* ignore */ }
  }
  for (const [, h] of chatSessions) {
    try { h.controller?.abort(); } catch (_) { /* ignore */ }
  }
  server.close(() => process.exit(0));
  setTimeout(() => process.exit(1), 5000);
}
process.on('SIGTERM', shutdown);
process.on('SIGINT', shutdown);

server.listen(PORT, '127.0.0.1', () => {
  console.log(`pi-bridge listening on 127.0.0.1:${PORT}, one-api base: ${ONEAPI_BASE}`);
  if (BRIDGE_SECRET) {
    console.log('[auth] BRIDGE_SECRET 已配置：X-Bridge-Token 严格校验已启用');
  } else {
    console.warn('[auth] BRIDGE_SECRET 未配置：兼容模式（不校验请求头，仅监听 127.0.0.1）。' +
      '建议与 one-api 的 AGENT_BRIDGE_SECRET 成对配置以启用鉴权');
  }
  if (ONEAPI_ADMIN_TOKEN) syncModels();
  else console.warn('[models] ONEAPI_ADMIN_TOKEN 未配置，模型表不会自动同步');
});

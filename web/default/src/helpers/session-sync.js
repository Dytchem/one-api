// dyt-103: 会话跨设备同步 —— 登录用户将会话列表/内容同步到后端（/api/session），
// 同一账号在不同设备看到相同记录；未登录时仅本地存储（原行为）。
import { API } from './api';

const timers = {}; // kind -> timeout

// 防抖同步：2.5s 内多次 persist 只发一次全量
export function syncSessions(kind, list, userId) {
  if (!userId) return;
  if (timers[kind]) clearTimeout(timers[kind]);
  timers[kind] = setTimeout(() => {
    try {
      list.slice(0, 50).forEach((s) => {
        API.put('/api/user/session', {
          kind,
          session_id: s.id,
          title: s.title || '新对话',
          messages: (s.messages || []).slice(0, 80), // 单会话最多 80 条，防请求过大
        });
      });
    } catch (e) {
      /* 忽略同步失败（本地仍有记录） */
    }
  }, 2500);
}

export function deleteRemoteSession(kind, id, userId) {
  if (!userId) return;
  API.delete(`/api/user/session/${kind}/${id}`).catch(() => {});
}

// 拉取服务器会话列表；失败返回 null（调用方保留本地数据）
export async function loadRemoteSessions(kind, userId) {
  if (!userId) return null;
  try {
    const res = await API.get(`/api/user/session?kind=${kind}`);
    if (res.data && res.data.success) {
      return (res.data.data || []).map((s) => ({
        id: s.id,
        title: s.title || '新对话',
        messages: s.messages || [],
        updatedAt: (s.updated_at || 0) * 1000,
        model: '',
      }));
    }
  } catch (e) {
    /* 网络失败保持本地 */
  }
  return null;
}

// 合并本地与远程：远程为准（同 id 覆盖），本地独有的补入；按 updatedAt 降序
export function mergeSessions(local, remote) {
  if (!remote || remote.length === 0) return local;
  const merged = [...remote];
  const remoteIds = new Set(remote.map((s) => s.id));
  for (const l of local) {
    if (!remoteIds.has(l.id)) merged.push(l);
  }
  merged.sort((a, b) => (b.updatedAt || 0) - (a.updatedAt || 0));
  return merged;
}

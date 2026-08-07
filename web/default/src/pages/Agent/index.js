import React, { useContext, useEffect, useRef, useState } from 'react';
import { Button, Card, Dropdown, Icon } from 'semantic-ui-react';
import { API, showError } from '../../helpers';
import { syncSessions, deleteRemoteSession, loadRemoteSessions, mergeSessions } from '../../helpers/session-sync';
import { renderMarkdown } from '../../helpers/markdown';
import { UserContext } from '../../context/User';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { isAdmin } from '../../helpers';

function sanitizeTitle(text) {
  if (typeof text !== 'string') text = '';
  const t = text.replace(/\s+/g, ' ').trim();
  return t.length > 24 ? t.slice(0, 24) + '…' : t;
}

function prettyArgs(args) {
  try {
    return JSON.stringify(args, null, 2);
  } catch (e) {
    return String(args);
  }
}

function prettyResult(result) {
  if (!result) return '';
  if (result.length > 600) return result.slice(0, 600) + '…';
  return result;
}

class AgentErrorBoundary extends React.Component {
  constructor(props) {
    super(props);
    this.state = { error: null };
  }
  static getDerivedStateFromError(error) {
    return { error };
  }
  componentDidCatch(error, info) {
    console.error('Agent render failed:', error, info);
  }
  render() {
    if (this.state.error) {
      return (
        <div className='dashboard-container' style={{ padding: '40px', textAlign: 'center' }}>
          <h3>页面渲染出错</h3>
          <pre style={{ textAlign: 'left', background: '#f5f5f5', padding: '12px', overflow: 'auto' }}>
            {String(this.state.error && this.state.error.message ? this.state.error.message : this.state.error)}
          </pre>
          <button onClick={() => { this.setState({ error: null }); }} style={{ marginTop: '12px' }}>
            重试
          </button>
        </div>
      );
    }
    return this.props.children;
  }
}

const Agent = () => {
  const [userState] = useContext(UserContext);
  const { t } = useTranslation();
  const userId = userState?.user?.id;
  const admin = isAdmin();

  const [tokens, setTokens] = useState([]);
  const [token, setToken] = useState(null);
  const [channels, setChannels] = useState([]);
  const [channelId, setChannelId] = useState(0);
  const [models, setModels] = useState([]);
  const [model, setModel] = useState('');
  const [sessions, setSessions] = useState([]);
  const [activeId, setActiveId] = useState(null);
  const [input, setInput] = useState('');
  const [thinkingLevel, setThinkingLevel] = useState('off');
  const [thinkingCustom, setThinkingCustom] = useState('');
  const [showThinkingCustom, setShowThinkingCustom] = useState(false);
  const [expandedCards, setExpandedCards] = useState({});
  const [expandedThinking, setExpandedThinking] = useState({});
  const controllerRef = useRef(null);
  const resumeControllerRef = useRef(null);
  const listRef = useRef(null);
  const textareaRef = useRef(null);

  const storageKey = `agent-sessions-${userId || 'guest'}`;
  const prefsKey = `agent-prefs-${userId || 'guest'}`;

  const loadPrefs = () => {
    try {
      return JSON.parse(localStorage.getItem(prefsKey) || '{}');
    } catch (e) {
      return {};
    }
  };
  const savePrefs = (patch) => {
    try {
      localStorage.setItem(prefsKey, JSON.stringify({ ...loadPrefs(), ...patch }));
    } catch (e) {
      // 忽略
    }
  };
  const persist = (list) => {
    // dyt-103: 登录用户同步到服务器（跨设备），未登录仅本地
    syncSessions('agent', list, userId);
    try {
      localStorage.setItem(storageKey, JSON.stringify(list));
    } catch (e) {
      // 溢出时降级：压缩工具结果后重试，仍失败则只保留最近消息
      try {
        const compact = list.map((s) => ({
          ...s,
          messages: (s.messages || []).map((m) => ({
            ...m,
            parts: (m.parts || []).map((p) =>
              p.type === 'tool' ? { ...p, result: String(p.result || '').slice(0, 500) } : p
            ),
          })),
        }));
        localStorage.setItem(storageKey, JSON.stringify(compact));
      } catch (e2) {
        try {
          const minimal = list.map((s) => ({ ...s, messages: (s.messages || []).slice(-4) }));
          localStorage.setItem(storageKey, JSON.stringify(minimal));
        } catch (e3) {
          // 忽略
        }
      }
    }
  };

  const loadTokens = async () => {
    try {
      const res = await API.get('/api/token?p=0&size=100');
      if (res.data.success) {
        const list = res.data.data || [];
        setTokens(list);
        // dyt-93: 无限额度令牌（remain_quota=-1）也可用（原条件 remain_quota > 0 会跳过它们）
        const usable = list.find((tk) => tk.status === 1 && (tk.remain_quota < 0 || tk.remain_quota > 0));
        const prefs = loadPrefs();
        const remembered = prefs.tokenId ? list.find((tk) => tk.id === prefs.tokenId) : null;
        setToken(remembered || usable || list[0] || null);
      }
    } catch (err) {
      showError(err.message || '令牌列表加载失败');
    }
  };

  const loadChannels = async () => {
    try {
      const res = await API.get('/api/chat/channels');
      if (res.data.success) {
        const list = res.data.data || [];
        setChannels(list);
        const prefs = loadPrefs();
        if (prefs.channelId && list.some((cc) => cc.id === prefs.channelId)) {
          setChannelId(prefs.channelId);
        }
      }
    } catch (err) {
      // 忽略
    }
  };

  useEffect(() => {
    loadTokens();
    loadChannels();
  }, []);

  // 模型列表：Agent 用 pi 的模型表（bridge 同步），前端直接展示 one-api 模型
  useEffect(() => {
    if (!token) {
      setModels([]);
      return;
    }
    const prefs = loadPrefs();
    const ch = channels.find((cc) => cc.id === channelId);
    if (ch && ch.models && ch.models.length > 0) {
      const list = ch.models.map((m) => ({ key: m, text: m, value: m }));
      setModels(list);
      setModel(prefs.model && list.some((m) => m.value === prefs.model) ? prefs.model : list[0]?.value || '');
      return;
    }
    let cancelled = false;
    const doFetch = async () => {
      try {
        const resp = await fetch('/v1/models', {
          headers: { Authorization: `Bearer ${token.key}` },
        });
        if (!resp.ok) {
          if (!cancelled) setModels([]);
          return;
        }
        const json = await resp.json();
        if (cancelled) return;
        const list = (json.data || []).map((m) => ({ key: m.id, text: m.id, value: m.id }));
        setModels(list);
        setModel(prefs.model && list.some((m) => m.value === prefs.model) ? prefs.model : list[0]?.value || '');
      } catch (err) {
        if (!cancelled) setModels([]);
      }
    };
    doFetch();
    return () => {
      cancelled = true;
    };
  }, [token, channelId, channels]);

  useEffect(() => {
    try {
      const saved = JSON.parse(localStorage.getItem(storageKey) || '[]');
      setSessions(saved);
      const prefs = loadPrefs();
      if (prefs.thinkingLevel) {
        setThinkingLevel(prefs.thinkingLevel);
        setShowThinkingCustom(prefs.thinkingLevel === 'custom');
        if (prefs.thinkingCustom) setThinkingCustom(prefs.thinkingCustom);
      }
      if (saved.length > 0) {
        const remembered = prefs.activeSessionId
          ? saved.find((s) => s.id === prefs.activeSessionId)
          : null;
        setActiveId(remembered?.id || saved[0].id);
      }
    } catch (e) {
      setSessions([]);
    }
    // dyt-103: 拉取服务器会话合并（跨设备同步）
    let cancelled = false;
    loadRemoteSessions('agent', userId).then((remote) => {
      if (cancelled || !remote) return;
      setSessions((prev) => {
        const merged = mergeSessions(prev, remote);
        if (merged !== prev) persist(merged);
        return merged;
      });
    });
    return () => {
      cancelled = true;
    };
  }, [storageKey]);

  const activeSession = sessions.find((s) => s.id === activeId) || null;
  // 是否正在生成：由会话状态派生（刷新后从持久化恢复，界面立即回到"正在生成"）
  const streaming = activeSession?.streaming === true;

  const scrollToBottom = () => {
    const el = listRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  };

  useEffect(() => {
    scrollToBottom();
  }, [sessions, streaming]);

  // 订阅模型：后台（bridge）一直在执行并广播事件，本页只是订阅者。
  // 仅在挂载/切换会话时重新订阅（依赖不含 streaming，避免 send 设置 streaming 触发重复订阅）：
  // 会话处于生成中（streaming）时——
  // 重放已产生的事件（先清一次半成品再重放，避免重复），随后实时续推。
  // 刷新不影响后台执行；订阅失败则静默收尾（保留已收到内容，不报错）。
  useEffect(() => {
    const sess = activeSession;
    if (!sess || !token || sess.streaming !== true) return;
    if (!sess.messages || sess.messages.length === 0) return;
    const sid = sess.id;
    const controller = new AbortController();
    resumeControllerRef.current = controller;
    let clearedOnce = false;
    const clearPending = () => {
      if (clearedOnce) return;
      clearedOnce = true;
      updateSession(sid, (msgs) => {
        const copy = [...msgs];
        const l = copy[copy.length - 1];
        if (l && l.role === 'assistant' && !l.finished) {
          copy[copy.length - 1] = { ...l, content: '', thinking: '', toolCalls: [], parts: [] };
        }
        return copy;
      });
    };
    const finishSilently = () => {
      updateSession(
        sid,
        (msgs) => {
          const copy = [...msgs];
          const l = copy[copy.length - 1];
          if (l && l.role === 'assistant' && !l.finished) {
            copy[copy.length - 1] = { ...l, finished: true };
          }
          return copy;
        },
        { streaming: false }
      );
    };
    const applyResumeEvent = (data) => {
      if (data.type === 'delta' || data.type === 'thinking') {
        clearPending();
        handleAgentEvent(sid, data);
      } else if (data.type === 'tool_start' || data.type === 'tool_end') {
        clearPending();
        handleAgentEvent(sid, data);
      } else if (data.type === 'done') {
        finishSilently();
      } else if (data.type === 'error') {
        // 订阅到的错误静默收尾：保留已收到内容
        clearPending();
        finishSilently();
      }
    };
    const doResume = async () => {
      let idleGuard = null; // dyt-96: 提升到 try 之外——try/finally 是独立块作用域，块内声明 finally 不可见
      try {
        const resp = await fetch('/api/agent/resume', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ session_id: sid }),
          signal: controller.signal,
        });
        if (!resp.ok || !resp.body) {
          // 会话已不存在/服务不可达：视为该轮已结束，静默收尾
          finishSilently();
          return;
        }
        const reader = resp.body.getReader();
        const decoder = new TextDecoder();
        let buffer = '';
        let lastData = Date.now();
        idleGuard = setInterval(() => {
          if (Date.now() - lastData > 60000) controller.abort();
        }, 10000);
        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          lastData = Date.now();
          buffer += decoder.decode(value, { stream: true });
          const events = buffer.split('\n\n');
          buffer = events.pop();
          for (const evt of events) {
            const line = evt.split('\n').find((l) => l.startsWith('data:'));
            if (!line) continue;
            const raw = line.slice(5).trim();
            if (!raw) continue;
            try {
              applyResumeEvent(JSON.parse(raw));
            } catch (e) {
              // 忽略
            }
          }
        }
      } catch (err) {
        const emsg = String(err && err.message ? err.message : err);
        if (err.name !== 'AbortError' && !/input stream|Failed to fetch|network/i.test(emsg)) {
          finishSilently();
        }
      } finally {
        clearInterval(idleGuard);
        if (resumeControllerRef.current === controller) resumeControllerRef.current = null;
      }
    };
    doResume();
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeId, token]);

  const newSession = () => {
    const session = {
      id: Date.now().toString(36),
      title: t('chat.new_chat'),
      model: model || '',
      messages: [],
      updatedAt: Date.now(),
    };
    const list = [session, ...sessions];
    setSessions(list);
    persist(list);
    setActiveId(session.id);
    savePrefs({ activeSessionId: session.id });
    setInput('');
    textareaRef.current?.focus();
  };

  const removeSession = (id) => {
    const list = sessions.filter((s) => s.id !== id);
    setSessions(list);
    persist(list);
    // dyt-103: 服务器同步删除（跨设备）
    deleteRemoteSession('agent', id, userId);
    if (activeId === id) {
      const next = list[0]?.id || null;
      setActiveId(next);
      savePrefs({ activeSessionId: next });
    }
  };

  const updateSession = (id, updater, meta) => {
    setSessions((prev) => {
      const list = prev.map((s) => {
        if (s.id !== id) return s;
        const next = { ...s, messages: updater(s.messages), updatedAt: Date.now(), ...meta };
        if (next.messages.length > 0 && next.title === t('chat.new_chat')) {
          const first = next.messages.find((m) => m.role === 'user');
          if (first) next.title = sanitizeTitle(first.content);
        }
        return next;
      });
      persist(list);
      return list;
    });
  };

  const clearSession = () => {
    if (!activeSession) return;
    setSessions((prev) => {
      const list = prev.map((s) =>
        s.id === activeId ? { ...s, messages: [], title: t('chat.new_chat') } : s
      );
      persist(list);
      return list;
    });
  };

  const handleSseStream = async (resp, controller, onEvent) => {
    const reader = resp.body.getReader();
    const decoder = new TextDecoder();
    let buffer = '';
    let lastData = Date.now();
    let idleGuard = null; // dyt-96: try/finally 是独立块作用域，声明提到 try 外
    try {
      idleGuard = setInterval(() => {
        if (Date.now() - lastData > 60000) controller.abort();
      }, 10000);
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        lastData = Date.now();
        buffer += decoder.decode(value, { stream: true });
        const events = buffer.split('\n\n');
        buffer = events.pop();
        for (const evt of events) {
          const line = evt.split('\n').find((l) => l.startsWith('data:'));
          if (!line) continue;
          const raw = line.slice(5).trim();
          if (!raw) continue;
          try {
            onEvent(JSON.parse(raw));
          } catch (e) {
            // 忽略无法解析的行
          }
        }
      }
    } finally {
      clearInterval(idleGuard);
    }
  };

  // 事件按时间顺序写入消息片段（thinking / 文本 / 工具卡片穿插出现）
  const pushPart = (sid, part) => {
    updateSession(sid, (msgs) => {
      const copy = [...msgs];
      const last = copy[copy.length - 1];
      if (!last || last.role !== 'assistant') return copy;
      copy[copy.length - 1] = { ...last, parts: [...(last.parts || []), part] };
      return copy;
    });
  };
  // 同类型片段连续则追加，否则新开片段（保证顺序：文本后出现工具调用时不再拼到文本前）
  const appendToPart = (sid, kind, text) => {
    updateSession(sid, (msgs) => {
      const copy = [...msgs];
      const last = copy[copy.length - 1];
      if (!last || last.role !== 'assistant') return copy;
      const parts = last.parts || [];
      const i = parts.length - 1;
      if (i >= 0 && parts[i].type === kind && parts[i].complete !== true) {
        const np = [...parts];
        np[i] = { ...np[i], text: (np[i].text || '') + text };
        copy[copy.length - 1] = { ...last, parts: np };
      } else {
        copy[copy.length - 1] = {
          ...last,
          parts: [...parts, { type: kind, text }],
        };
      }
      return copy;
    });
  };

  const handleAgentEvent = (sid, data) => {
    if (data.type === 'delta') {
      appendToPart(sid, 'text', data.content);
    } else if (data.type === 'thinking') {
      appendToPart(sid, 'thinking', data.content);
    } else if (data.type === 'tool_start') {
      pushPart(sid, {
        type: 'tool',
        tool: data.tool,
        args: data.args,
        status: 'running',
        result: '',
      });
    } else if (data.type === 'tool_end') {
      updateSession(sid, (msgs) => {
        const copy = [...msgs];
        const last = copy[copy.length - 1];
        if (!last || last.role !== 'assistant') return copy;
        const parts = last.parts || [];
        const np = [...parts];
        let idx = -1;
        for (let i = np.length - 1; i >= 0; i--) {
          if (np[i].type === 'tool' && np[i].tool === data.tool && np[i].status === 'running') {
            idx = i;
            break;
          }
        }
        if (idx >= 0) {
          np[idx] = {
            ...np[idx],
            status: data.ok ? 'done' : 'error',
            result: data.result || '',
          };
          copy[copy.length - 1] = { ...last, parts: np };
        }
        return copy;
      });
    } else if (data.type === 'error') {
      appendToPart(sid, 'error', data.message || '');
    } else if (data.type === 'done') {
      updateSession(
        sid,
        (msgs) => {
          const copy = [...msgs];
          const last = copy[copy.length - 1];
          if (last && last.role === 'assistant') {
            copy[copy.length - 1] = { ...last, finished: true };
          }
          return copy;
        },
        { streaming: false }
      );
    }
  };

  const send = async () => {
    const text = input.trim();
    if (!text || streaming) return;
    if (!token) {
      showError('请先创建令牌再使用 Agent');
      return;
    }
    if (!model) {
      showError('请选择模型（需支持工具调用）');
      return;
    }
    let id = activeId;
    let baseSessions = sessions;
    if (!id) {
      const session = {
        id: Date.now().toString(36),
        title: t('chat.new_chat'),
        model,
        messages: [],
        updatedAt: Date.now(),
      };
      id = session.id;
      baseSessions = [session, ...sessions];
      setActiveId(id);
      savePrefs({ activeSessionId: id });
      setSessions(baseSessions);
      persist(baseSessions);
    }
    setInput('');

    // UI 立即进入"正在生成"：追加用户消息 + 空 assistant 消息，会话标记 streaming
    // （刷新后从此状态恢复：界面照旧显示生成中，并自动重新订阅续传）
    updateSession(
      id,
      (msgs) => [
        ...msgs,
        { role: 'user', content: text },
        { role: 'assistant', content: '', toolCalls: [], parts: [] },
      ],
      { streaming: true }
    );

    resumeControllerRef.current?.abort();
    resumeControllerRef.current = null;

    const controller = new AbortController();
    controllerRef.current = controller;

    let idleGuard = null;
    try {
      const body = { session_id: id, model, message: text, token_key: token.key };
      if (channelId) body.channel_id = channelId;
      const effectiveLevel =
        thinkingLevel === 'custom'
          ? thinkingCustom.trim()
          : thinkingLevel;
      if (effectiveLevel && effectiveLevel !== 'off') body.thinking_level = effectiveLevel;
      const resp = await fetch('/api/agent/chat', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
        signal: controller.signal,
      });
      if (!resp.ok) {
        let msg = `HTTP ${resp.status}`;
        try {
          const j = await resp.json();
          msg = j.message || msg;
        } catch (e) {
          /* ignore */
        }
        throw new Error(msg);
      }
      const reader = resp.body.getReader();
      const decoder = new TextDecoder();
      let buffer = '';
      let lastData = Date.now();
      idleGuard = setInterval(() => {
        if (Date.now() - lastData > 60000) controller.abort();
      }, 10000);
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        lastData = Date.now();
        buffer += decoder.decode(value, { stream: true });
        const events = buffer.split('\n\n');
        buffer = events.pop();
        for (const evt of events) {
          const line = evt
            .split('\n')
            .find((l) => l.startsWith('data:'));
          if (!line) continue;
          const raw = line.slice(5).trim();
          if (!raw) continue;
          let data;
          try {
            data = JSON.parse(raw);
          } catch (e) {
            continue;
          }
          if (data.type === 'delta') {
            handleAgentEvent(id, data);
          } else if (data.type === 'thinking') {
            handleAgentEvent(id, data);
          } else if (data.type === 'tool_start') {
            handleAgentEvent(id, data);
          } else if (data.type === 'tool_end') {
            handleAgentEvent(id, data);
          } else if (data.type === 'error') {
            updateSession(
              id,
              (msgs) => {
                const copy = [...msgs];
                const last = copy[copy.length - 1];
                if (last && last.role === 'assistant') {
                  copy[copy.length - 1] = { ...last, finished: true };
                }
                return copy;
              },
              { streaming: false }
            );
            handleAgentEvent(id, data);
          } else if (data.type === 'done') {
            handleAgentEvent(id, data);
          }
        }
      }
    } catch (err) {
      const emsg = String(err && err.message ? err.message : err);
      if (err.name === 'AbortError') {
        // 用户点击停止：stop() 已标记完成
      } else if (/input stream|Failed to fetch|network/i.test(emsg)) {
        // 连接中断（页面刷新/网络抖动）：后台仍在执行，保持生成状态，
        // 刷新后自动重新订阅续传，不显示错误
      } else {
        updateSession(
          id,
          (msgs) => {
            const copy = [...msgs];
            const last = copy[copy.length - 1];
            if (last && last.role === 'assistant' && !last.finished) {
              copy[copy.length - 1] = { ...last, finished: true };
            }
            return copy;
          },
          { streaming: false }
        );
        pushPart(id, { type: 'error', text: `请求失败：${emsg}` });
      }
    } finally {
      if (idleGuard) clearInterval(idleGuard);
      controllerRef.current = null;
    }
  };

  const stop = () => {
    controllerRef.current?.abort();
    resumeControllerRef.current?.abort();
    if (activeId) {
      // 通知 bridge 中止后台执行（仅用户主动停止；刷新断线不触发，保证续传）
      fetch('/api/agent/stop', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ session_id: activeId }),
      }).catch(() => {});

      updateSession(
        activeId,
        (msgs) => {
          const copy = [...msgs];
          const last = copy[copy.length - 1];
          if (last && last.role === 'assistant' && !last.finished) {
            copy[copy.length - 1] = { ...last, finished: true };
          }
          return copy;
        },
        { streaming: false }
      );
    }
  };

  const onKeyDown = (e) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      if (streaming) stop();
      else send();
    }
  };

  const toggleCard = (key) => {
    setExpandedCards((prev) => ({ ...prev, [key]: !prev[key] }));
  };

  const sidebarSelects = (
    <>
      <Button
        primary
        fluid
        icon
        labelPosition='left'
        onClick={newSession}
        style={{ marginBottom: '12px' }}
      >
        <Icon name='plus' />
        {t('chat.new_chat')}
      </Button>
      <div className='chat-select-field'>
        <div className='chat-select-label'>{t('chat.token')}</div>
        <Dropdown
          fluid
          selection
          value={token?.id || ''}
          options={tokens.map((tk) => ({
            key: tk.id,
            value: tk.id,
            text: `${tk.name}${tk.status !== 1 ? '（已禁用）' : ''}`,
          }))}
          onChange={(_, { value }) => {
            setToken(tokens.find((tk) => tk.id === value) || null);
            savePrefs({ tokenId: value });
          }}
        />
      </div>
      <div className='chat-select-field'>
        <div className='chat-select-label'>{t('chat.channel')}</div>
        <Dropdown
          fluid
          selection
          value={channelId}
          options={[
            { key: 0, value: 0, text: t('chat.auto_channel') },
            ...channels.map((ch) => ({ key: ch.id, value: ch.id, text: ch.name })),
          ]}
          onChange={(_, { value }) => {
            setChannelId(value);
            savePrefs({ channelId: value });
          }}
        />
      </div>
      <div className='chat-select-field'>
        <div className='chat-select-label'>{t('chat.model')}</div>
        <Dropdown
          fluid
          selection
          search
          value={model}
          options={models}
          onChange={(_, { value }) => {
            setModel(value);
            savePrefs({ model: value });
          }}
          placeholder={models.length ? '' : '无可用模型'}
        />
      </div>
      {!admin && (
        <div className='agent-admin-tip'>{t('agent.self_tip')}</div>
      )}
    </>
  );

  return (
    <div className='dashboard-container'>
      <Card fluid className='chart-card'>
        <Card.Content>
          <Card.Header className='header'>{t('agent.title')}</Card.Header>
          <div className='agent-note'>{t('agent.note')}</div>
          <div className='chat-layout'>
            <div className='chat-sidebar'>
              {sidebarSelects}
              <div className='session-list'>
                {sessions.length === 0 && (
                  <div className='session-empty'>{t('chat.no_sessions')}</div>
                )}
                {sessions.map((s) => (
                  <div
                    key={s.id}
                    className={`session-item ${s.id === activeId ? 'active' : ''}`}
                    onClick={() => {
                      setActiveId(s.id);
                      savePrefs({ activeSessionId: s.id });
                    }}
                  >
                    <span className='session-title'>{s.title}</span>
                    <Icon
                      name='trash alternate outline'
                      className='session-del'
                      onClick={(e) => {
                        e.stopPropagation();
                        removeSession(s.id);
                      }}
                    />
                  </div>
                ))}
              </div>
            </div>
            <div className='chat-main'>
              {!activeSession ? (
                <div className='chat-empty'>
                  <Icon name='magic' size='huge' />
                  <div>{t('agent.empty_hint')}</div>
                  {!token && (
                    <div className='chat-empty-tip'>
                      {t('chat.no_token_hint')}
                      <Link to='/token'>{t('chat.goto_token')}</Link>
                    </div>
                  )}
                </div>
              ) : (
                <>
                  <div className='chat-header'>
                    <span className='chat-header-title'>
                      <Icon name='magic' style={{ marginRight: '6px' }} />
                      {activeSession.title}
                      {model ? ` · ${model}` : ''}
                    </span>
                    <Button
                      size='small'
                      basic
                      onClick={clearSession}
                      disabled={streaming || activeSession.messages.length === 0}
                    >
                      <Icon name='eraser' />
                      {t('chat.clear')}
                    </Button>
                  </div>
                  <div className='chat-messages' ref={listRef}>
                    {activeSession.messages.map((m, idx) => {
                      if (m.role === 'user') {
                        return (
                          <div key={idx} className='msg-row user'>
                            <div className='msg-bubble'>
                              {typeof m.content === 'string' ? m.content : JSON.stringify(m.content)}
                            </div>
                          </div>
                        );
                      }
                      if (m.role === 'assistant') {
                        const hasParts = m.parts && m.parts.length > 0;
                        const hasToolCalls = m.toolCalls && m.toolCalls.length > 0;
                        const renderToolCard = (tc, ci, keyBase) => {
                          const key = `${keyBase}-${ci}`;
                          const expanded = expandedCards[key];
                          return (
                            <div key={ci} className={`tool-card ${tc.status}`}>
                              <div
                                className='tool-card-header'
                                onClick={() => toggleCard(key)}
                              >
                                <Icon name='cog' />
                                <span className='tool-card-name'>{tc.tool}</span>
                                <span className='tool-card-status'>
                                  {tc.status === 'running' && (
                                    <Icon loading name='spinner' />
                                  )}
                                  {tc.status === 'done' && (
                                    <Icon name='check circle' color='green' />
                                  )}
                                  {tc.status === 'error' && (
                                    <Icon name='times circle' color='red' />
                                  )}
                                </span>
                                <Icon
                                  name={expanded ? 'chevron up' : 'chevron down'}
                                  className='tool-card-chevron'
                                />
                              </div>
                              {expanded && (
                                <div className='tool-card-body'>
                                  {tc.args && Object.keys(tc.args).length > 0 && (
                                    <pre className='tool-card-args'>{prettyArgs(tc.args)}</pre>
                                  )}
                                  {tc.result && (
                                    <div className='tool-card-result'>
                                      <div className='tool-card-result-label'>
                                        结果
                                      </div>
                                      <pre>{prettyResult(tc.result)}</pre>
                                    </div>
                                  )}
                                </div>
                              )}
                            </div>
                          );
                        };
                        return (
                          <div key={idx} className={`msg-row assistant ${m.error ? 'error' : ''}`}>
                            <div className='msg-bubble'>
                              {hasParts ? (
                                m.parts.map((p, pi) => {
                                  if (p.type === 'thinking') {
                                    const tkey = `${idx}-t${pi}`;
                                    return (
                                      <div
                                        key={pi}
                                        className='msg-thinking'
                                        onClick={() =>
                                          setExpandedThinking((prev) => ({ ...prev, [tkey]: !prev[tkey] }))
                                        }
                                      >
                                        <div className='msg-thinking-header'>
                                          <Icon name='eye' style={{ margin: 0, fontSize: '12px' }} />
                                          思考过程
                                          <Icon
                                            name={expandedThinking[tkey] ? 'chevron up' : 'chevron down'}
                                            className='msg-thinking-chevron'
                                          />
                                        </div>
                                        {expandedThinking[tkey] !== false && (
                                          <div className='msg-thinking-body'>{p.text}</div>
                                        )}
                                      </div>
                                    );
                                  }
                                  if (p.type === 'text') {
                                    return (
                                      <div
                                        key={pi}
                                        className='msg-markdown'
                                        dangerouslySetInnerHTML={{ __html: renderMarkdown(p.text) }}
                                      />
                                    );
                                  }
                                  if (p.type === 'tool') {
                                    return renderToolCard(p, pi, idx);
                                  }
                                  if (p.type === 'error') {
                                    return (
                                      <div key={pi} className='msg-error'>
                                        {p.text}
                                      </div>
                                    );
                                  }
                                  return null;
                                })
                              ) : (
                                <>
                                  {m.thinking && (
                                    <div
                                      className='msg-thinking'
                                      onClick={() =>
                                        setExpandedThinking((prev) => ({ ...prev, [idx]: !prev[idx] }))
                                      }
                                    >
                                      <div className='msg-thinking-header'>
                                        <Icon name='eye' style={{ margin: 0, fontSize: '12px' }} />
                                        思考过程
                                        <Icon
                                          name={expandedThinking[idx] ? 'chevron up' : 'chevron down'}
                                          className='msg-thinking-chevron'
                                        />
                                      </div>
                                      {expandedThinking[idx] !== false && (
                                        <div className='msg-thinking-body'>{m.thinking}</div>
                                      )}
                                    </div>
                                  )}
                                  {m.content ? (
                                    <div
                                      className='msg-markdown'
                                      dangerouslySetInnerHTML={{ __html: renderMarkdown(m.content) }}
                                    />
                                  ) : null}
                                  {!m.content && !hasToolCalls && !m.thinking && <div>…</div>}
                                  {hasToolCalls &&
                                    m.toolCalls.map((tc, ci) => renderToolCard(tc, ci, idx))}
                                </>
                              )}
                            </div>
                          </div>
                        );
                      }
                      return null;
                    })}
                    {streaming && (
                      <div className='msg-row assistant'>
                        <div className='msg-bubble msg-streaming'>▋</div>
                      </div>
                    )}
                  </div>
                  <div className='chat-input'>
                    <textarea
                      ref={textareaRef}
                      rows={3}
                      placeholder={t('agent.placeholder')}
                      value={input}
                      onChange={(e) => setInput(e.target.value)}
                      onKeyDown={onKeyDown}
                      disabled={streaming}
                    />
                    <div className='chat-input-actions'>
                      <span className='chat-input-hint'>
                        {streaming ? t('chat.streaming') : t('chat.enter_hint')}
                      </span>
                      <div className='chat-thinking'>
                        <span className='chat-thinking-label'>{t('chat.thinking')}</span>
                        {[
                          ['off', t('chat.thinking_off')],
                          ['low', t('chat.thinking_low')],
                          ['medium', t('chat.thinking_medium')],
                          ['high', t('chat.thinking_high')],
                          ['custom', t('chat.thinking_custom')],
                        ].map(([val, label]) => (
                          <button
                            key={val}
                            type='button'
                            className={`chat-thinking-btn ${thinkingLevel === val ? 'active' : ''}`}
                            onClick={() => {
                              setThinkingLevel(val);
                              setShowThinkingCustom(val === 'custom');
                              savePrefs({ thinkingLevel: val });
                            }}
                          >
                            {label}
                          </button>
                        ))}
                        {showThinkingCustom && (
                          <input
                            className='chat-thinking-input'
                            value={thinkingCustom}
                            onChange={(e) => {
                              setThinkingCustom(e.target.value);
                              savePrefs({ thinkingCustom: e.target.value });
                            }}
                            placeholder='自定义单词'
                            disabled={streaming}
                          />
                        )}
                      </div>
                      <Button
                        primary
                        icon
                        labelPosition='left'
                        onClick={() => (streaming ? stop() : send())}
                        disabled={streaming ? false : !input.trim()}
                      >
                        <Icon name={streaming ? 'stop' : 'send'} />
                        {streaming ? t('chat.stop') : t('chat.send')}
                      </Button>
                    </div>
                  </div>
                </>
              )}
            </div>
          </div>
        </Card.Content>
      </Card>
    </div>
  );
};

export default () => (
  <AgentErrorBoundary>
    <Agent />
  </AgentErrorBoundary>
);

import React, { useContext, useEffect, useRef, useState } from 'react';
import { Button, Card, Dropdown, Icon } from 'semantic-ui-react';
import { API, showError } from '../../helpers';
import { renderMarkdown } from '../../helpers/markdown';
import { UserContext } from '../../context/User';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';

const MAX_ATTACHMENTS = 4;
const MAX_ATTACH_SIZE = 15 * 1024 * 1024;

function sanitizeTitle(text) {
  if (typeof text !== 'string') text = '';
  const t = text.replace(/\s+/g, ' ').trim();
  return t.length > 24 ? t.slice(0, 24) + '…' : t;
}

function buildContent(text, attachments) {
  if (attachments.length === 0) return text;
  const parts = [];
  attachments.forEach((a) => {
    if (a.mime.startsWith('image/')) {
      parts.push({ type: 'image_url', image_url: { url: a.dataUrl } });
    } else if (a.mime.startsWith('audio/')) {
      parts.push({
        type: 'input_audio',
        input_audio: { data: a.dataUrl.split(',')[1], format: a.ext },
      });
    } else if (a.mime.startsWith('video/')) {
      parts.push({
        type: 'input_video',
        input_video: { video: { data: a.dataUrl.split(',')[1], mime_type: a.mime } },
      });
    }
  });
  if (text) parts.push({ type: 'text', text });
  return parts;
}

function renderMessageContent(m) {
  const c = m.content;
  if (typeof c === 'string') return <div>{c}</div>;
  if (Array.isArray(c)) {
    return c.map((p, i) => {
      if (!p || typeof p !== 'object') return null;
      if (p.type === 'text') return <div key={i}>{p.text}</div>;
      if (p.type === 'image_url')
        return <img key={i} src={p.image_url?.url} className='msg-image' alt='' />;
      if (p.type === 'input_audio' || p.type === 'input_video')
        return (
          <div key={i} className='msg-attach-tag'>
            <Icon name={p.type === 'input_audio' ? 'music' : 'video'} />
            {p.type === 'input_audio' ? '音频' : '视频'}
          </div>
        );
      return null;
    });
  }
  return null;
}

class ChatErrorBoundary extends React.Component {
  constructor(props) {
    super(props);
    this.state = { error: null };
  }
  static getDerivedStateFromError(error) {
    return { error };
  }
  componentDidCatch(error, info) {
    console.error('Chat render failed:', error, info);
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

const Chat = () => {
  const [userState] = useContext(UserContext);
  const { t } = useTranslation();
  const userId = userState?.user?.id;

  const [tokens, setTokens] = useState([]);
  const [token, setToken] = useState(null);
  const [channels, setChannels] = useState([]);
  const [channelId, setChannelId] = useState(0);
  const [models, setModels] = useState([]);
  const [model, setModel] = useState('');
  const [sessions, setSessions] = useState([]);
  const [activeId, setActiveId] = useState(null);
  const [input, setInput] = useState('');
  const [attachments, setAttachments] = useState([]);
  const [thinkingLevel, setThinkingLevel] = useState('off');
  const [thinkingCustom, setThinkingCustom] = useState('');
  const [showThinkingCustom, setShowThinkingCustom] = useState(false);
  const [expandedThinking, setExpandedThinking] = useState({});
  const controllerRef = useRef(null);
  const resumeControllerRef = useRef(null);
  const onDeltaRef = useRef(null);
  const onThinkingRef = useRef(null);
  const listRef = useRef(null);
  const textareaRef = useRef(null);
  const fileInputRef = useRef(null);

  const storageKey = `chat-sessions-${userId || 'guest'}`;
  const prefsKey = `chat-prefs-${userId || 'guest'}`;

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
      // 忽略存储失败
    }
  };

  const persist = (list) => {
    // 大附件（base64 图片/音视频）不持久化，避免 localStorage 膨胀导致刷新后请求巨大
    const compact = list.map((s) => ({
      ...s,
      messages: s.messages.map((m) => {
        if (Array.isArray(m.content)) {
          return {
            ...m,
            content: m.content.map((p) => {
              if (
                p &&
                p.type === 'image_url' &&
                p.image_url &&
                typeof p.image_url.url === 'string' &&
                p.image_url.url.startsWith('data:') &&
                p.image_url.url.length > 120000
              ) {
                return { type: 'image_url', image_url: { url: '[图片附件已省略]' } };
              }
              return p;
            }),
          };
        }
        return m;
      }),
    }));
    try {
      localStorage.setItem(storageKey, JSON.stringify(compact));
    } catch (e) {
      // localStorage 溢出时退回存储文本摘要
      try {
        const minimal = compact.map((s) => ({ ...s, messages: s.messages.slice(-6) }));
        localStorage.setItem(storageKey, JSON.stringify(minimal));
      } catch (e2) {
        // 忽略
      }
    }
  };

  const loadTokens = async () => {
    try {
      const res = await API.get('/api/token?p=0&size=100');
      if (res.data.success) {
        const list = res.data.data || [];
        setTokens(list);
        const usable = list.find((tk) => tk.status === 1 && tk.remain_quota > 0);
        const prefs = loadPrefs();
        const remembered = prefs.tokenId
          ? list.find((tk) => tk.id === prefs.tokenId)
          : null;
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
        if (prefs.channelId && list.some((c) => c.id === prefs.channelId)) {
          setChannelId(prefs.channelId);
        }
      }
    } catch (err) {
      // 渠道列表加载失败不阻塞聊天
    }
  };

  useEffect(() => {
    loadTokens();
    loadChannels();
  }, []);

  // 模型列表：指定渠道 → 该渠道 models（非空时）；否则 → 令牌可用模型
  useEffect(() => {
    if (!token) {
      setModels([]);
      return;
    }
    const prefs = loadPrefs();
    const ch = channels.find((c) => c.id === channelId);
    if (ch && ch.models && ch.models.length > 0) {
      const list = ch.models.map((m) => ({ key: m, text: m, value: m }));
      setModels(list);
      setModel(
        prefs.model && list.some((m) => m.value === prefs.model)
          ? prefs.model
          : list[0]?.value || ''
      );
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
        setModel(
          prefs.model && list.some((m) => m.value === prefs.model)
            ? prefs.model
            : list[0]?.value || ''
        );
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
      updateSessionMessages(sid, (msgs) => {
        const copy = [...msgs];
        const l = copy[copy.length - 1];
        if (l && l.role === 'assistant' && !l.finished) {
          copy[copy.length - 1] = { ...l, content: '', thinking: '' };
        }
        return copy;
      });
    };
    const finishSilently = () => {
      updateSessionMessages(
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
      const append = (updater) =>
        updateSessionMessages(sid, (msgs) => {
          const copy = [...msgs];
          const l = copy[copy.length - 1];
          if (l && l.role === 'assistant') {
            copy[copy.length - 1] = updater(l);
          }
          return copy;
        });
      if (data.type === 'delta') {
        clearPending();
        append((m) => ({ ...m, content: (m.content || '') + data.content }));
      } else if (data.type === 'thinking') {
        clearPending();
        append((m) => ({ ...m, thinking: (m.thinking || '') + data.content }));
      } else if (data.type === 'done') {
        finishSilently();
      } else if (data.type === 'error') {
        // 订阅到的错误静默收尾：保留已收到内容
        clearPending();
        finishSilently();
      }
    };
    const doResume = async () => {
      let idleGuard = null; // dyt-96: try/finally 独立块作用域，声明提到 try 外
      try {
        const resp = await fetch('/api/chat/resume', {
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
            const line = evt
              .split('\n')
              .find((l) => l.startsWith('data:'));
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
    setAttachments([]);
    textareaRef.current?.focus();
  };

  const removeSession = (id) => {
    const list = sessions.filter((s) => s.id !== id);
    setSessions(list);
    persist(list);
    if (activeId === id) {
      const next = list[0]?.id || null;
      setActiveId(next);
      savePrefs({ activeSessionId: next });
    }
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
    setAttachments([]);
  };

  const updateSessionMessages = (id, updater, meta) => {
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

  const onFiles = (e) => {
    const files = Array.from(e.target.files || []);
    e.target.value = '';
    files.forEach((f) => {
      if (attachments.length + files.length > MAX_ATTACHMENTS) {
        showError(`最多同时上传 ${MAX_ATTACHMENTS} 个附件`);
        return;
      }
      if (f.size > MAX_ATTACH_SIZE) {
        showError(`${f.name} 超过 15MB 限制`);
        return;
      }
      const reader = new FileReader();
      reader.onload = () => {
        const a = {
          name: f.name,
          mime: f.type || 'application/octet-stream',
          dataUrl: reader.result,
          ext: (f.name.split('.').pop() || 'bin').toLowerCase(),
        };
        setAttachments((prev) => (prev.length >= MAX_ATTACHMENTS ? prev : [...prev, a]));
      };
      reader.readAsDataURL(f);
    });
  };

  const removeAttachment = (idx) => {
    setAttachments((prev) => prev.filter((_, i) => i !== idx));
  };

  const send = async () => {
    const text = input.trim();
    if ((!text && attachments.length === 0) || streaming) return;
    if (!token) {
      showError('请先创建令牌再使用 Chat');
      return;
    }
    if (!model) {
      showError('请选择模型');
      return;
    }
    const content = buildContent(text, attachments);
    let id = activeId;
    let baseSessions = sessions;
    if (!id) {
      const session = {
        id: Date.now().toString(36),
        title: t('chat.new_chat'),
        model: model,
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
    setAttachments([]);

    // 请求体：剔除历史中未完成的旧回复（该轮已被取代）
    let history = baseSessions.find((s) => s.id === id)?.messages || [];
    if (history.length > 0) {
      const last = history[history.length - 1];
      if (last && last.role === 'assistant' && !last.finished) {
        history = history.slice(0, -1);
      }
    }
    let messages = [...history, { role: 'user', content }];
    // 历史过大时裁剪历史（保留首条 + 最近 9 条，避免新消息被重复追加）
    if (JSON.stringify(messages).length > 150000 && messages.length > 12) {
      const trimmed = [history[0], ...history.slice(-9)];
      messages = [...trimmed, { role: 'user', content }];
    }
    // UI 立即进入"正在生成"：追加用户消息 + 空 assistant 消息，会话标记 streaming
    // （刷新后从此状态恢复：界面照旧显示生成中，并自动重新订阅续传）
    updateSessionMessages(
      id,
      (msgs) => [...msgs, { role: 'user', content }, { role: 'assistant', content: '', thinking: '', finished: false }],
      { streaming: true }
    );

    resumeControllerRef.current?.abort();
    resumeControllerRef.current = null;

    const controller = new AbortController();
    controllerRef.current = controller;

    const markFinished = () => {
      updateSessionMessages(
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
    };

    let idleGuard = null;
    try {
      const body = { session_id: id, model, messages, token_key: token.key };
      if (channelId) body.channel_id = channelId;
      const effectiveLevel =
        thinkingLevel === 'custom'
          ? thinkingCustom.trim()
          : thinkingLevel;
      if (effectiveLevel && effectiveLevel !== 'off') {
        body.thinking_level = effectiveLevel;
      }
      const resp = await fetch('/api/chat/send', {
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
            updateSessionMessages(id, (msgs) => {
              const copy = [...msgs];
              const last = copy[copy.length - 1];
              if (last && last.role === 'assistant') {
                copy[copy.length - 1] = { ...last, content: (last.content || '') + data.content };
              }
              return copy;
            });
          } else if (data.type === 'thinking') {
            updateSessionMessages(id, (msgs) => {
              const copy = [...msgs];
              const last = copy[copy.length - 1];
              if (last && last.role === 'assistant') {
                copy[copy.length - 1] = { ...last, thinking: (last.thinking || '') + data.content };
              }
              return copy;
            });
          } else if (data.type === 'error') {
            updateSessionMessages(
              id,
              (msgs) => {
                const copy = [...msgs];
                const last = copy[copy.length - 1];
                if (last && last.role === 'assistant') {
                  copy[copy.length - 1] = {
                    ...last,
                    content: last.content || `请求失败：${data.message}`,
                    error: true,
                    finished: true,
                  };
                }
                return copy;
              },
              { streaming: false }
            );
          } else if (data.type === 'done') {
            markFinished();
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
        updateSessionMessages(
          id,
          (msgs) => {
            const copy = [...msgs];
            const last = copy[copy.length - 1];
            if (last && last.role === 'assistant' && !last.finished) {
              copy[copy.length - 1] = {
                ...last,
                content: last.content || `请求失败：${emsg}`,
                error: true,
                finished: true,
              };
            }
            return copy;
          },
          { streaming: false }
        );
      }
    } finally {
      if (idleGuard) clearInterval(idleGuard);
      controllerRef.current = null;
      onDeltaRef.current = null;
      onThinkingRef.current = null;
    }
  };

  const stop = () => {
    controllerRef.current?.abort();
    resumeControllerRef.current?.abort();
    if (activeId) {
      // 通知 bridge 中止后台执行（仅用户主动停止；刷新断线不触发，保证续传）
      fetch('/api/chat/stop', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ session_id: activeId }),
      }).catch(() => {});

      updateSessionMessages(
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
    </>
  );

  return (
    <div className='dashboard-container'>
      <Card fluid className='chart-card'>
        <Card.Content>
          <Card.Header className='header'>{t('chat.title')}</Card.Header>
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
                  <Icon name='comments outline' size='huge' />
                  <div>{t('chat.empty_hint')}</div>
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
                    {activeSession.messages.map((m, idx) => (
                      <div key={idx} className={`msg-row ${m.role} ${m.error ? 'error' : ''}`}>
                        <div className='msg-bubble'>
                          {m.role === 'assistant' && !m.error ? (
                            <>
                              {m.thinking && (
                                <div
                                  className='msg-thinking'
                                  onClick={() =>
                                    setExpandedThinking((prev) => ({
                                      ...prev,
                                      [idx]: !prev[idx],
                                    }))
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
                              {typeof m.content === 'string' ? (
                                <div
                                  className='msg-markdown'
                                  dangerouslySetInnerHTML={{ __html: renderMarkdown(m.content) }}
                                />
                              ) : (
                                renderMessageContent(m)
                              )}
                            </>
                          ) : (
                            renderMessageContent(m)
                          )}
                        </div>
                      </div>
                    ))}
                    {streaming && (
                      <div className='msg-row assistant'>
                        <div className='msg-bubble msg-streaming'>▋</div>
                      </div>
                    )}
                  </div>
                  <div className='chat-input'>
                    {attachments.length > 0 && (
                      <div className='chat-attachments'>
                        {attachments.map((a, i) => (
                          <div key={i} className='chat-attach'>
                            {a.mime.startsWith('image/') ? (
                              <img src={a.dataUrl} alt='' />
                            ) : (
                              <Icon
                                name={a.mime.startsWith('audio/') ? 'music' : 'film'}
                              />
                            )}
                            <span className='chat-attach-name'>{a.name}</span>
                            <Icon
                              name='close'
                              className='chat-attach-del'
                              onClick={() => removeAttachment(i)}
                            />
                          </div>
                        ))}
                      </div>
                    )}
                    <textarea
                      ref={textareaRef}
                      rows={3}
                      placeholder={t('chat.placeholder')}
                      value={input}
                      onChange={(e) => setInput(e.target.value)}
                      onKeyDown={onKeyDown}
                      disabled={streaming}
                    />
                    <div className='chat-input-actions'>
                      <div className='chat-input-left'>
                        <Button
                          size='small'
                          basic
                          icon
                          title={t('chat.attach')}
                          onClick={() => fileInputRef.current?.click()}
                          disabled={streaming}
                        >
                          <Icon name='paperclip' />
                        </Button>
                        <input
                          ref={fileInputRef}
                          type='file'
                          multiple
                          accept='image/*,audio/*,video/*'
                          style={{ display: 'none' }}
                          onChange={onFiles}
                        />
                        <span className='chat-input-hint'>
                          {streaming ? t('chat.streaming') : t('chat.enter_hint')}
                        </span>
                      </div>
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
                        disabled={streaming ? false : !input.trim() && attachments.length === 0}
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
  <ChatErrorBoundary>
    <Chat />
  </ChatErrorBoundary>
);

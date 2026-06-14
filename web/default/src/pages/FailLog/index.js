import React, { useState, useEffect } from 'react';
import { Card, Table, Button, Modal, Label, Input, Select, Message } from 'semantic-ui-react';
import { useTranslation } from 'react-i18next';
import { API, showError, showWarning, showNotice, copy } from '../../helpers';

// 渠道名缓存
const channelCache = {};

const FailLog = () => {
  const { t } = useTranslation();
  const [logs, setLogs] = useState([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(0);
  const [loading, setLoading] = useState(false);
  const [channelFilter, setChannelFilter] = useState('');
  const [modelFilter, setModelFilter] = useState('');
  const [selectedLog, setSelectedLog] = useState(null);
  const [payloadData, setPayloadData] = useState(null);
  const [payloadLoading, setPayloadLoading] = useState(false);

  const pageSize = 50;

  const fetchLogs = async (p = 0) => {
    setLoading(true);
    try {
      const params = { p, size: pageSize };
      if (channelFilter) params.channel = channelFilter;
      if (modelFilter) params.model_name = modelFilter;
      const res = await API.get('/api/log/fail/list', { params });
      const { success, data, message } = res.data || {};
      if (success && data) {
        setLogs(data.items || []);
        setTotal(data.total || 0);
      } else {
        showError(message || '加载失败');
      }
    } catch (err) {
      showError(err.message || '请求失败');
    }
    setLoading(false);
  };

  useEffect(() => {
    fetchLogs(0);
    setPage(0);
  }, []);

  const handleSearch = () => {
    fetchLogs(0);
    setPage(0);
  };

  const handlePageChange = (newPage) => {
    setPage(newPage);
    fetchLogs(newPage);
  };

  const handleRowClick = async (log) => {
    setSelectedLog(log);
    setPayloadData(null);
    if (log.has_payload) {
      setPayloadLoading(true);
      try {
        const res = await API.get(`/api/log/fail/${log.id}`);
        const { success, data } = res.data || {};
        if (success) setPayloadData(data);
      } catch (err) {
        // ignore
      }
      setPayloadLoading(false);
    }
  };

  // 解析 status code 徽章
  const extractBadge = (content) => {
    if (!content) return null;
    // dyt-37: 先匹配 [code] 或 [code:msg]
    const m = content.match(/\[(\d+):?([^\]]*)\]/);
    if (m) return { code: m[1], msg: m[2] || null };
    // dyt-37: 回退匹配 HTTP xxx（如 "HTTP 400"）
    const httpM = content.match(/HTTP\s+(\d{3})/);
    if (httpM) return { code: httpM[1], msg: null };
    return null;
  };

  // 截取预览
  const getPreview = (content) => {
    if (!content) return '';
    const prefix = '探测失败，请求模型：';
    const idx = content.indexOf(prefix);
    if (idx >= 0) {
      const start = idx + prefix.length;
      const rest = content.substring(start);
      const barIdx = rest.indexOf(' | 上游：');
      const prev = barIdx >= 0 ? rest.substring(0, barIdx) : rest;
      return prev.length > 60 ? prev.substring(0, 60) + '…' : prev;
    }
    return content.length > 80 ? content.substring(0, 80) + '…' : content;
  };

  // 格式化时间
  const formatTime = (ts) => {
    const d = new Date(ts * 1000);
    const pad = (n) => String(n).padStart(2, '0');
    return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
  };

  // JSON 格式化
  const formatJSON = (s) => {
    try {
      const obj = typeof s === 'string' ? JSON.parse(s) : s;
      return JSON.stringify(obj, null, 2);
    } catch {
      return s || '(empty)';
    }
  };

  const totalPages = Math.ceil(total / pageSize);

  return (
    <div className='dashboard-container'>
      <Card fluid className='chart-card'>
        <Card.Content>
          <Card.Header className='header'>
            {t('fail_log.title', '失败日志')}
            <span style={{ float: 'right', fontSize: '14px', fontWeight: 'normal', color: '#999' }}>
              {t('fail_log.total', '共')} {total} {t('fail_log.items', '条')}
            </span>
          </Card.Header>

          {/* 筛选栏 */}
          <div style={{ marginBottom: 16, display: 'flex', gap: 12, alignItems: 'center', flexWrap: 'wrap' }}>
            <Input
              placeholder={t('fail_log.channel_placeholder', '渠道ID')}
              value={channelFilter}
              onChange={(e) => setChannelFilter(e.target.value)}
              size='small'
              style={{ width: 100 }}
            />
            <Input
              placeholder={t('fail_log.model_placeholder', '模型名')}
              value={modelFilter}
              onChange={(e) => setModelFilter(e.target.value)}
              size='small'
              style={{ width: 120 }}
            />
            <Button size='small' primary onClick={handleSearch} loading={loading}>
              {t('fail_log.search', '搜索')}
            </Button>
          </div>

          {/* 列表 */}
          <Table celled compact selectable size='small' style={{ fontSize: '13px' }}>
            <Table.Header>
              <Table.Row>
                <Table.HeaderCell style={{ width: 80 }}>{t('log.time', '时间')}</Table.HeaderCell>
                <Table.HeaderCell style={{ width: 60 }}>ID</Table.HeaderCell>
                <Table.HeaderCell style={{ width: 60 }}>{t('fail_log.channel', '渠道')}</Table.HeaderCell>
                <Table.HeaderCell style={{ width: 90 }}>{t('fail_log.model', '模型')}</Table.HeaderCell>
                <Table.HeaderCell style={{ width: 100 }}>{t('fail_log.status', '状态码')}</Table.HeaderCell>
                <Table.HeaderCell>{t('fail_log.preview', '预览')}</Table.HeaderCell>
                <Table.HeaderCell style={{ width: 60 }}>{t('fail_log.payload', 'Payload')}</Table.HeaderCell>
              </Table.Row>
            </Table.Header>
            <Table.Body>
              {logs.map((log) => {
                const badge = extractBadge(log.content);
                return (
                  <Table.Row
                    key={log.id}
                    onClick={() => handleRowClick(log)}
                    style={{ cursor: 'pointer' }}
                    active={selectedLog && selectedLog.id === log.id}
                  >
                    <Table.Cell>{formatTime(log.time)}</Table.Cell>
                    <Table.Cell>{log.id}</Table.Cell>
                    <Table.Cell>
                      <Label basic size='mini'>{log.channel_id}</Label>
                    </Table.Cell>
                    <Table.Cell style={{ maxWidth: 90, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {log.model_name}
                    </Table.Cell>
                    <Table.Cell>
                      {badge ? (
                        <Label
                          size='mini'
                          color={badge.code.startsWith('2') ? 'orange' : 'red'}
                          title={badge.msg || badge.code}
                        >
                          [{badge.code}]
                        </Label>
                      ) : (
                        <Label size='mini' color='red'>-</Label>
                      )}
                    </Table.Cell>
                    <Table.Cell style={{ maxWidth: 300, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {getPreview(log.content)}
                    </Table.Cell>
                    <Table.Cell style={{ textAlign: 'center' }}>
                      {log.has_payload ? '📋' : '-'}
                    </Table.Cell>
                  </Table.Row>
                );
              })}
              {logs.length === 0 && !loading && (
                <Table.Row>
                  <Table.Cell colSpan={7} style={{ textAlign: 'center', padding: 24, color: '#999' }}>
                    {t('fail_log.empty', '暂无失败日志')}
                  </Table.Cell>
                </Table.Row>
              )}
            </Table.Body>
          </Table>

          {/* 分页 */}
          {totalPages > 1 && (
            <div style={{ textAlign: 'center', marginTop: 12 }}>
              <Button.Group size='small'>
                <Button disabled={page <= 0} onClick={() => handlePageChange(page - 1)}>
                  {t('fail_log.prev', '上一页')}
                </Button>
                <Button disabled>
                  {page + 1} / {totalPages}
                </Button>
                <Button disabled={page >= totalPages - 1} onClick={() => handlePageChange(page + 1)}>
                  {t('fail_log.next', '下一页')}
                </Button>
              </Button.Group>
            </div>
          )}
        </Card.Content>
      </Card>

      {/* 详情 Modal */}
      {selectedLog && (
        <Modal
          open={!!selectedLog}
          onClose={() => { setSelectedLog(null); setPayloadData(null); }}
          size='large'
          closeIcon
        >
          <Modal.Header>
            {t('fail_log.detail_title', '失败请求详情')} — ID: {selectedLog.id}
          </Modal.Header>
          <Modal.Content scrolling style={{ maxHeight: '70vh' }}>
            {/* 基本元信息 */}
            <Message size='small'>
              <strong>{t('log.time', '时间')}:</strong> {formatTime(selectedLog.time)}&nbsp;&nbsp;
              <strong>{t('fail_log.channel', '渠道')}:</strong> {selectedLog.channel_id}&nbsp;&nbsp;
              <strong>{t('fail_log.model', '模型')}:</strong> {selectedLog.model_name}&nbsp;&nbsp;
              <strong>Tokens:</strong> {selectedLog.prompt_tokens}
            </Message>

            {/* Error */}
            <Card fluid>
              <Card.Content>
                <Card.Header>{t('fail_log.error_info', '错误信息')}</Card.Header>
                <pre style={{ fontSize: 12, whiteSpace: 'pre-wrap', wordBreak: 'break-all', maxHeight: 200, overflow: 'auto', background: '#fafafa', padding: 12, borderRadius: 4 }}>
                  {selectedLog.content}
                </pre>
              </Card.Content>
            </Card>

            {/* Payload */}
            {payloadLoading && <Message>Loading payload…</Message>}
            {selectedLog.has_payload && !payloadData && !payloadLoading && (
              <Message warning>{t('fail_log.payload_load_fail', '载荷加载失败')}</Message>
            )}
            {payloadData && (
              <>
                <Card fluid style={{ marginTop: 12 }}>
                  <Card.Content>
                    <Card.Header>
                      {t('fail_log.request', '请求体')}
                      <Button
                        size='mini'
                        basic
                        floated='right'
                        icon='copy'
                        content={t('fail_log.copy', '复制')}
                        onClick={async () => {
                          if (await copy(formatJSON(payloadData.request))) {
                            showNotice(t('fail_log.copy_success', '已复制到剪贴板'));
                          } else {
                            showWarning(t('fail_log.copy_failed', '复制失败，请手动复制'));
                          }
                        }}
                      />
                    </Card.Header>
                    <pre style={{
                      fontSize: 11, whiteSpace: 'pre-wrap', wordBreak: 'break-all',
                      maxHeight: 400, overflow: 'auto', background: '#f5f5f5', padding: 12, borderRadius: 4,
                      fontFamily: 'Menlo, Monaco, monospace'
                    }}>
                      {formatJSON(payloadData.request)}
                    </pre>
                  </Card.Content>
                </Card>

                <Card fluid style={{ marginTop: 12 }}>
                  <Card.Content>
                    <Card.Header>
                      {t('fail_log.response', '响应体')}
                      <Button
                        size='mini'
                        basic
                        floated='right'
                        icon='copy'
                        content={t('fail_log.copy', '复制')}
                        onClick={async () => {
                          if (await copy(formatJSON(payloadData.response))) {
                            showNotice(t('fail_log.copy_success', '已复制到剪贴板'));
                          } else {
                            showWarning(t('fail_log.copy_failed', '复制失败，请手动复制'));
                          }
                        }}
                      />
                    </Card.Header>
                    <pre style={{
                      fontSize: 11, whiteSpace: 'pre-wrap', wordBreak: 'break-all',
                      maxHeight: 400, overflow: 'auto', background: '#f5f5f5', padding: 12, borderRadius: 4,
                      fontFamily: 'Menlo, Monaco, monospace'
                    }}>
                      {payloadData.response || '(empty)'}
                    </pre>
                  </Card.Content>
                </Card>

                {payloadData.error && (
                  <Card fluid style={{ marginTop: 12 }}>
                    <Card.Content>
                      <Card.Header>{t('fail_log.error_info', 'Payload 错误')}</Card.Header>
                      <pre style={{
                        fontSize: 11, whiteSpace: 'pre-wrap', wordBreak: 'break-all',
                        maxHeight: 200, overflow: 'auto', background: '#fff0f0', padding: 12, borderRadius: 4
                      }}>
                        {payloadData.error}
                      </pre>
                    </Card.Content>
                  </Card>
                )}
              </>
            )}
            {!selectedLog.has_payload && !payloadLoading && (
              <Message info>
                {t('fail_log.no_payload', '该日志暂无完整请求/响应载荷（可能是旧版本日志）。')}
              </Message>
            )}
          </Modal.Content>
          <Modal.Actions>
            <Button onClick={() => { setSelectedLog(null); setPayloadData(null); }}>
              {t('fail_log.close', '关闭')}
            </Button>
          </Modal.Actions>
        </Modal>
      )}
    </div>
  );
};

export default FailLog;

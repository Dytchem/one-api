import React, { useState, useEffect } from 'react';
import { Card, Table, Button, Modal, Label, Input, Form, Pagination, Dropdown, Message } from 'semantic-ui-react';
import { useTranslation } from 'react-i18next';
import {
  API,
  showError,
  showWarning,
  showNotice,
  copy,
  timestamp2string,
} from '../../helpers';
import { ITEMS_PER_PAGE_OPTIONS } from '../../constants';
import { renderColorLabel } from '../../helpers/render';

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
  // 与其他列表页一致：每页条数可选并持久化
  const [itemsPerPage, setItemsPerPage] = useState(() => parseInt(localStorage.getItem('itemsPerPage') || '10') || 10);

  const fetchLogs = async (p = 0, size) => {
    setLoading(true);
    const sizeParam = size !== undefined ? size : itemsPerPage;
    try {
      const params = { p, size: sizeParam };
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

  const onPageChange = (e, { activePage }) => {
    const newPage = activePage - 1;
    setPage(newPage);
    fetchLogs(newPage);
  };

  const handleItemsPerPageChange = (e, { value }) => {
    setItemsPerPage(value);
    localStorage.setItem('itemsPerPage', value.toString());
    setPage(0);
    fetchLogs(0, value);
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

  // 预览保留关键上下文，完整内容在详情中查看
  const getPreview = (content) => {
    if (!content) return '';
    const compact = content.replace(/\s+/g, ' ').trim();
    return compact.length > 180 ? compact.substring(0, 180) + '…' : compact;
  };

  // 与日志列表同款：两行显示 (日期 / 时间)，点击复制请求 ID
  const renderTimestamp = (timestamp, request_id) => {
    const s = timestamp2string(timestamp);
    const parts = s.split(' ');
    const date = parts[0] || '';
    const time = parts[1] || '';
    return (
      <code
        onClick={async () => {
          if (await copy(request_id)) {
            showNotice(`已复制请求 ID：${request_id}`);
          } else {
            showWarning(`请求 ID 复制失败：${request_id}`);
          }
        }}
        style={{ cursor: 'pointer', fontSize: '11px', lineHeight: '1.2', whiteSpace: 'nowrap' }}
      >
        <div>{date}</div>
        <div>{time}</div>
      </code>
    );
  };

  // 格式化时间（详情弹窗用）
  const formatTime = (ts) => {
    return timestamp2string(ts);
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

  const totalPages = Math.ceil(total / itemsPerPage);

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

          {/* 筛选栏 —— 与日志列表同款 Form.Group */}
          <Form>
            <Form.Group className='logs-filter-group'>
              <Form.Input
                fluid
                label={t('fail_log.channel', '渠道ID')}
                size={'small'}
                width={3}
                value={channelFilter}
                placeholder={t('fail_log.channel_placeholder', '渠道ID')}
                onChange={(e) => setChannelFilter(e.target.value)}
              />
              <Form.Input
                fluid
                label={t('fail_log.model', '模型')}
                size={'small'}
                width={3}
                value={modelFilter}
                placeholder={t('fail_log.model_placeholder', '模型名')}
                onChange={(e) => setModelFilter(e.target.value)}
              />
              <Form.Button
                fluid
                label={t('fail_log.search', '搜索')}
                size={'small'}
                width={2}
                loading={loading}
                onClick={handleSearch}
              >
                {t('log.buttons.submit')}
              </Form.Button>
            </Form.Group>
          </Form>

          {/* 列表 —— 与日志列表同款表格样式 */}
          <Table
            basic={'very'}
            compact
            size='small'
            className={`logs-table fail-logs-table`}
          >
            <Table.Header>
              <Table.Row>
                <Table.HeaderCell style={{ whiteSpace: 'nowrap', textAlign: 'right' }} width={1}>
                  {t('log.time', '时间')}
                </Table.HeaderCell>
                <Table.HeaderCell style={{ whiteSpace: 'nowrap' }} width={1}>
                  ID
                </Table.HeaderCell>
                <Table.HeaderCell style={{ whiteSpace: 'nowrap' }} width={1}>
                  {t('fail_log.channel', '渠道')}
                </Table.HeaderCell>
                <Table.HeaderCell style={{ whiteSpace: 'nowrap' }} width={2}>
                  {t('fail_log.model', '模型')}
                </Table.HeaderCell>
                <Table.HeaderCell style={{ whiteSpace: 'nowrap' }} width={1}>
                  {t('fail_log.status', '状态码')}
                </Table.HeaderCell>
                <Table.HeaderCell style={{ whiteSpace: 'nowrap' }}>
                  {t('fail_log.preview', '预览')}
                </Table.HeaderCell>
                <Table.HeaderCell style={{ whiteSpace: 'nowrap', textAlign: 'center' }} width={1}>
                  {t('fail_log.payload', 'Payload')}
                </Table.HeaderCell>
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
                    <Table.Cell>
                      {renderTimestamp(log.time, log.request_id)}
                    </Table.Cell>
                    <Table.Cell>{log.id}</Table.Cell>
                    <Table.Cell>
                      <Label basic>{log.channel_id}</Label>
                    </Table.Cell>
                    <Table.Cell>
                      {log.model_name ? renderColorLabel(log.model_name) : ''}
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
                    <Table.Cell>
                      <span
                        style={{
                          wordBreak: 'break-word',
                          color: '#555',
                          lineHeight: '1.4',
                          display: '-webkit-box',
                          WebkitLineClamp: 3,
                          WebkitBoxOrient: 'vertical',
                          overflow: 'hidden',
                        }}
                        title={log.content}
                      >
                        {getPreview(log.content)}
                      </span>
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

            {/* 分页 —— 与日志列表同款：每页条数选择 + Pagination */}
            <Table.Footer>
              <Table.Row>
                <Table.HeaderCell colSpan='7'>
                  <Dropdown
                    selection
                    options={ITEMS_PER_PAGE_OPTIONS}
                    value={itemsPerPage}
                    onChange={handleItemsPerPageChange}
                    placeholder={t('common.page_size') || '每页显示'}
                    style={{ marginRight: '10px' }}
                  />
                  <Pagination
                    floated='right'
                    activePage={page + 1}
                    onPageChange={onPageChange}
                    size='small'
                    siblingRange={1}
                    totalPages={totalPages > 0 ? totalPages : 1}
                  />
                </Table.HeaderCell>
              </Table.Row>
            </Table.Footer>
          </Table>
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
                <strong>请求 ID:</strong> {selectedLog.request_id || '-'}&nbsp;&nbsp;
                <strong>Tokens:</strong> {selectedLog.prompt_tokens || 0}↑ {selectedLog.completion_tokens || 0}↓&nbsp;&nbsp;
                <strong>耗时:</strong> {selectedLog.elapsed_time || 0}ms
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

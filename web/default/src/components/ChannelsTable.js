import React, {useEffect, useState} from 'react';
import {useTranslation} from 'react-i18next';
import {Button, Dropdown, Form, Input, Label, Message, Pagination, Popup, Table,} from 'semantic-ui-react';
import {Link} from 'react-router-dom';
import {
  API,
  loadChannelModels,
  setPromptShown,
  shouldShowPrompt,
  showError,
  showInfo,
  showSuccess,
  timestamp2string,
} from '../helpers';

import {CHANNEL_OPTIONS, ITEMS_PER_PAGE_OPTIONS} from '../constants';
import {renderGroup, renderNumber} from '../helpers/render';

function renderTimestamp(timestamp) {
  return <>{timestamp2string(timestamp)}</>;
}

function renderType(type, t) {
  // 每次渲染重新构建，避免切换语言后缓存旧文案
  const type2label = new Map();
  for (let i = 0; i < CHANNEL_OPTIONS.length; i++) {
    type2label[CHANNEL_OPTIONS[i].value] = CHANNEL_OPTIONS[i];
  }
  type2label[0] = {
    value: 0,
    text: t('channel.table.status_unknown'),
    color: 'grey',
  };
  return (
    <Label basic color={type2label[type]?.color}>
      {type2label[type] ? type2label[type].text : type}
    </Label>
  );
}

function renderBalance(type, balance, t) {
  switch (type) {
    case 1: // OpenAI
        if (balance === 0) {
            return <span>{t('channel.table.balance_not_supported')}</span>;
        }
      return <span>${balance.toFixed(2)}</span>;
    case 4: // CloseAI
      return <span>¥{balance.toFixed(2)}</span>;
    case 8: // 自定义
      return <span>${balance.toFixed(2)}</span>;
    case 5: // OpenAI-SB
      return <span>¥{(balance / 10000).toFixed(2)}</span>;
    case 10: // AI Proxy
      return <span>{renderNumber(balance)}</span>;
    case 12: // API2GPT
      return <span>¥{balance.toFixed(2)}</span>;
    case 13: // AIGC2D
      return <span>{renderNumber(balance)}</span>;
    case 20: // OpenRouter
      return <span>${balance.toFixed(2)}</span>;
    case 36: // DeepSeek
      return <span>¥{balance.toFixed(2)}</span>;
    case 44: // SiliconFlow
      return <span>¥{balance.toFixed(2)}</span>;
    default:
      return <span>{t('channel.table.balance_not_supported')}</span>;
  }
}

function isShowDetail() {
  return localStorage.getItem('show_detail') === 'true';
}

const promptID = 'detail';

const ChannelsTable = () => {
  const { t } = useTranslation();
  const [channels, setChannels] = useState([]);
  const [healthData, setHealthData] = useState({});
  const [loading, setLoading] = useState(true);
  const [activePage, setActivePage] = useState(1);
  const [itemsPerPage, setItemsPerPage] = useState(() => parseInt(localStorage.getItem('itemsPerPage') || '10'));
  const [searchKeyword, setSearchKeyword] = useState('');
  const [searching, setSearching] = useState(false);
  const [updatingBalance, setUpdatingBalance] = useState(false);
  const [showPrompt, setShowPrompt] = useState(shouldShowPrompt(promptID));
  const [showDetail, setShowDetail] = useState(isShowDetail());
  const [sortField, setSortField] = useState(() => localStorage.getItem('channelSortField') || 'id');
  const [sortOrder, setSortOrder] = useState(() => localStorage.getItem('channelSortOrder') || 'desc');
  const [orderBy, setOrderBy] = useState('');

  const processChannelData = (channel) => {
    if (channel.models === '') {
      channel.models = [];
      channel.test_model = '';
    } else {
      channel.models = channel.models.split(',');
      if (channel.models.length > 0) {
        channel.test_model = channel.models[0];
      }
      channel.model_options = channel.models.map((model) => {
        return {
          key: model,
          text: model,
          value: model,
        };
      });
    }
    return channel;
  };

  const loadChannels = async (startIdx, order, sort, size) => {
    const orderParam = order !== undefined ? order : sortField;
    const sortParam = sort !== undefined ? sort : sortOrder;
    const sizeParam = size !== undefined ? size : itemsPerPage;
    const res = await API.get(`/api/channel/?p=${startIdx}&order=${orderParam}&sort=${sortParam}&size=${sizeParam}`);
    const { success, message, data } = res.data;
    if (success) {
      let localChannels = data.map(processChannelData);
      if (startIdx === 0) {
        setChannels(localChannels);
      } else {
        let newChannels = [...channels];
        newChannels.splice(
          startIdx * itemsPerPage,
          data.length,
          ...localChannels
        );
        setChannels(newChannels);
      }
    } else {
      showError(message);
    }
    setLoading(false);
  };

  const loadHealthData = async () => {
    const res = await API.get('/api/channel/health');
    const { success, data } = res.data;
    if (success && data) {
      const map = {};
      data.forEach(h => { map[h.channel_id] = h; });
      setHealthData(map);
    }
  };

  const handleItemsPerPageChange = (e, { value }) => {
    setItemsPerPage(value);
    localStorage.setItem('itemsPerPage', value.toString());
    setActivePage(1);
    loadChannels(0, undefined, undefined, value);
  };
  const onPaginationChange = (e, { activePage }) => {
    (async () => {
      if (activePage === Math.ceil(channels.length / itemsPerPage) + 1) {
        // In this case we have to load more data and then append them.
        await loadChannels(activePage - 1);
      }
      setActivePage(activePage);
    })();
  };

  const refresh = async () => {
    setLoading(true);
    await loadChannels(activePage - 1);
    await loadHealthData();
  };

  const toggleShowDetail = () => {
    setShowDetail(!showDetail);
    localStorage.setItem('show_detail', (!showDetail).toString());
  };

  useEffect(() => {
    loadChannels(0)
      .then()
      .catch((reason) => {
        showError(reason);
      });
    loadChannelModels().then();
    loadHealthData().then();
  }, []);

  const manageChannel = async (id, action, idx, value) => {
    let data = { id };
    let res;
    switch (action) {
      case 'delete':
        res = await API.delete(`/api/channel/${id}/`);
        break;
      case 'enable':
        data.status = 1;
        res = await API.put('/api/channel/', data);
        break;
      case 'disable':
        data.status = 2;
        res = await API.put('/api/channel/', data);
        break;
      case 'priority':
        if (value === '') {
          return;
        }
        data.priority = parseInt(value);
        res = await API.put('/api/channel/', data);
        break;
      case 'weight':
        if (value === '') {
          return;
        }
        data.weight = parseInt(value);
        if (data.weight < 0) {
          data.weight = 0;
        }
        res = await API.put('/api/channel/', data);
        break;
      case 'clone':
        res = await API.post(`/api/channel/clone/${id}/`);
        break;
    }
    const { success, message } = res.data;
    if (success) {
      if (action === 'clone') {
        showSuccess(t('channel.messages.clone_success'));
        await refresh();
        return;
      }
      showSuccess(t('channel.messages.operation_success'));
      let channel = res.data.data;
      let newChannels = [...channels];
      let realIdx = (activePage - 1) * itemsPerPage + idx;
      if (action === 'delete') {
        newChannels[realIdx].deleted = true;
      } else {
        newChannels[realIdx].status = channel.status;
      }
      setChannels(newChannels);
    } else {
      showError(message);
    }
  };

  const renderStatus = (status, t) => {
    switch (status) {
      case 1:
        return (
          <Label basic color='green'>
            {t('channel.table.status_enabled')}
          </Label>
        );
      case 2:
        return (
          <Popup
            trigger={
              <Label basic color='red'>
                {t('channel.table.status_disabled')}
              </Label>
            }
            content={t('channel.table.status_disabled_tip')}
            basic
          />
        );
      case 3:
        return (
          <Popup
            trigger={
              <Label basic color='yellow'>
                {t('channel.table.status_auto_disabled')}
              </Label>
            }
            content={t('channel.table.status_auto_disabled_tip')}
            basic
          />
        );
      default:
        return (
          <Label basic color='grey'>
            {t('channel.table.status_unknown')}
          </Label>
        );
    }
  };

  const renderResponseTime = (responseTime, t) => {
    let time = responseTime / 1000;
    time = time.toFixed(2) + 's';
    if (responseTime === 0) {
      return (
        <Label basic color='grey'>
          {t('channel.table.not_tested')}
        </Label>
      );
    } else if (responseTime <= 1000) {
      return (
        <Label basic color='green'>
          {time}
        </Label>
      );
    } else if (responseTime <= 3000) {
      return (
        <Label basic color='olive'>
          {time}
        </Label>
      );
    } else if (responseTime <= 5000) {
      return (
        <Label basic color='yellow'>
          {time}
        </Label>
      );
    } else {
      return (
        <Label basic color='red'>
          {time}
        </Label>
      );
    }
  };


  const renderHealthBadge = (channelId, t) => {
    const h = healthData[channelId];
    if (!h) {
      return <Label basic color='grey'>{t('channel.table.health_no_data')}</Label>;
    }
    if (h.degraded) {
      return (
        <Popup
          trigger={<Label basic color='red'>{t('channel.table.health_degraded')}</Label>}
          content={<div>
            <b>{t('channel.table.health_degraded_popup')}</b><br/>
            {t('channel.table.health_score')}: {h.health_score.toFixed(2)}<br/>
            {t('channel.table.health_success_rate')}: {(h.success_rate * 100).toFixed(0)}%<br/>
            {t('channel.table.health_tok_per_sec')}: {h.tok_per_sec > 0 ? h.tok_per_sec.toFixed(1) : '-'}<br/>
            {t('channel.table.health_ttft')}: {h.avg_ttft_ms > 0 ? `${h.avg_ttft_ms}ms` : '-'}
          </div>}
          basic
        />
      );
    }
    if (h.health_score >= 0.8) {
      return (
        <Popup
          trigger={<Label basic color='green'>{(h.health_score * 100).toFixed(0)}%</Label>}
          content={<div>
            {t('channel.table.health_score')}: {h.health_score.toFixed(2)}<br/>
            {t('channel.table.health_success_rate')}: {(h.success_rate * 100).toFixed(0)}%<br/>
            {t('channel.table.health_tok_per_sec')}: {h.tok_per_sec > 0 ? h.tok_per_sec.toFixed(1) : '-'} tok/s<br/>
            {t('channel.table.health_ttft')}: {h.avg_ttft_ms > 0 ? `${h.avg_ttft_ms}ms` : '-'}
          </div>}
          basic
        />
      );
    }
    if (h.health_score >= 0.5) {
      return (
        <Popup
          trigger={<Label basic color='yellow'>{(h.health_score * 100).toFixed(0)}%</Label>}
          content={<div>
            {t('channel.table.health_score')}: {h.health_score.toFixed(2)}<br/>
            {t('channel.table.health_success_rate')}: {(h.success_rate * 100).toFixed(0)}%<br/>
            {t('channel.table.health_tok_per_sec')}: {h.tok_per_sec > 0 ? h.tok_per_sec.toFixed(1) : '-'} tok/s<br/>
            {t('channel.table.health_ttft')}: {h.avg_ttft_ms > 0 ? `${h.avg_ttft_ms}ms` : '-'}
          </div>}
          basic
        />
      );
    }
    return (
      <Popup
        trigger={<Label basic color='orange'>{(h.health_score * 100).toFixed(0)}%</Label>}
        content={<div>
          {t('channel.table.health_score')}: {h.health_score.toFixed(2)}<br/>
          {t('channel.table.health_success_rate')}: {(h.success_rate * 100).toFixed(0)}%<br/>
          {t('channel.table.health_tok_per_sec')}: {h.tok_per_sec > 0 ? h.tok_per_sec.toFixed(1) : '-'} tok/s<br/>
          {t('channel.table.health_ttft')}: {h.avg_ttft_ms > 0 ? `${h.avg_ttft_ms}ms` : '-'}
        </div>}
        basic
      />
    );
  };

  const searchChannels = async () => {
    if (searchKeyword === '') {
      // if keyword is blank, load files instead.
      await loadChannels(0);
      setActivePage(1);
    setActivePage(1);
      return;
    }
    setSearching(true);
    const res = await API.get(`/api/channel/search?keyword=${searchKeyword}`);
    const { success, message, data } = res.data;
    if (success) {
      let localChannels = data.map(processChannelData);
      setChannels(localChannels);
      setActivePage(1);
    setActivePage(1);
    } else {
      showError(message);
    }
    setSearching(false);
  };

  const switchTestModel = async (idx, model) => {
    let newChannels = [...channels];
    let realIdx = (activePage - 1) * itemsPerPage + idx;
    newChannels[realIdx].test_model = model;
    setChannels(newChannels);
  };

  const testChannel = async (id, name, idx, m) => {
    const res = await API.get(`/api/channel/test/${id}?model=${m}`);
    const { success, message, time, model } = res.data;
    if (success) {
      let newChannels = [...channels];
      let realIdx = (activePage - 1) * itemsPerPage + idx;
      newChannels[realIdx].response_time = time * 1000;
      newChannels[realIdx].test_time = Date.now() / 1000;
      setChannels(newChannels);
      showSuccess(
        t('channel.messages.test_success', { name, model, time, message })
      );
    } else {
      showError(message);
    }
    let newChannels = [...channels];
    let realIdx = (activePage - 1) * itemsPerPage + idx;
    newChannels[realIdx].response_time = time * 1000;
    newChannels[realIdx].test_time = Date.now() / 1000;
    setChannels(newChannels);
  };

  const testChannels = async (scope) => {
    const res = await API.get(`/api/channel/test?scope=${scope}`);
    const { success, message } = res.data;
    if (success) {
      showInfo(t('channel.messages.test_all_started'));
    } else {
      showError(message);
    }
  };

  const deleteAllDisabledChannels = async () => {
    const res = await API.delete(`/api/channel/disabled`);
    const { success, message, data } = res.data;
    if (success) {
      showSuccess(
        t('channel.messages.delete_disabled_success', { count: data })
      );
      await refresh();
    } else {
      showError(message);
    }
  };

  const updateChannelBalance = async (id, name, idx) => {
    const res = await API.get(`/api/channel/update_balance/${id}/`);
    const { success, message, balance } = res.data;
    if (success) {
      let newChannels = [...channels];
      let realIdx = (activePage - 1) * itemsPerPage + idx;
      newChannels[realIdx].balance = balance;
      newChannels[realIdx].balance_updated_time = Date.now() / 1000;
      setChannels(newChannels);
      showSuccess(t('channel.messages.balance_update_success', { name }));
    } else {
      showError(message);
    }
  };

  const updateAllChannelsBalance = async () => {
    setUpdatingBalance(true);
    const res = await API.get(`/api/channel/update_balance`);
    const { success, message } = res.data;
    if (success) {
      showInfo(t('channel.messages.all_balance_updated'));
    } else {
      showError(message);
    }
    setUpdatingBalance(false);
  };

  const handleKeywordChange = async (e, { value }) => {
    setSearchKeyword(value.trim());
  };

  const sortChannel = async (key) => {
    // health 是内存指标不是数据库列，不支持服务端排序，改为客户端排序
    if (key === 'health') {
      const sorted = [...channels].sort((a, b) => {
        const ah = healthData[a.id] ? healthData[a.id].health_score : 0;
        const bh = healthData[b.id] ? healthData[b.id].health_score : 0;
        return bh - ah;
      });
      setChannels(sorted);
      return;
    }
    // Determine sort order: if clicking same field, toggle; otherwise default to desc
    let newOrder = 'desc';
    if (sortField === key) {
      newOrder = sortOrder === 'desc' ? 'asc' : 'desc';
    }
    setSortField(key);
    setSortOrder(newOrder);
    localStorage.setItem('channelSortField', key);
    localStorage.setItem('channelSortOrder', newOrder);
    setActivePage(1);
    // Reload data from server with new sort order
    await loadChannels(0, key, newOrder);
  };

  return (
    <>
      <Form onSubmit={searchChannels}>
        <Form.Input
          icon='search'
          fluid
          iconPosition='left'
          placeholder={t('channel.search')}
          value={searchKeyword}
          loading={searching}
          onChange={handleKeywordChange}
        />
      </Form>
      {showPrompt && (
        <Message
          onDismiss={() => {
            setShowPrompt(false);
            setPromptShown(promptID);
          }}
        >
          {t('channel.balance_notice')}
          <br />
          {t('channel.test_notice')}
          <br />
          {t('channel.detail_notice')}
        </Message>
      )}
      <div className='channels-table-wrap'>
      <Table basic={'very'} compact size='small' className={'channels-table' + (showDetail ? ' detail-mode' : '')}>
        <Table.Header>
          <Table.Row>
            <Table.HeaderCell
              style={{ cursor: 'pointer', width: '4%' }}
              onClick={() => {
                sortChannel('id');
              }}
            >
              {t('channel.table.id')}
            </Table.HeaderCell>
            <Table.HeaderCell
              style={{ cursor: 'pointer', width: '15%' }}
              onClick={() => {
                sortChannel('name');
              }}
            >
              {t('channel.table.name')}
            </Table.HeaderCell>
            <Table.HeaderCell
              style={{ cursor: 'pointer', width: '7%' }}
              onClick={() => {
                sortChannel('group');
              }}
            >
              {t('channel.table.group')}
            </Table.HeaderCell>
            <Table.HeaderCell
              style={{ cursor: 'pointer', width: '13%' }}
              onClick={() => {
                sortChannel('type');
              }}
            >
              {t('channel.table.type')}
            </Table.HeaderCell>
            <Table.HeaderCell
              style={{ cursor: 'pointer', width: '9%' }}
              onClick={() => {
                sortChannel('status');
              }}
            >
              {t('channel.table.status')}
            </Table.HeaderCell>
            <Table.HeaderCell
              style={{ cursor: 'pointer', width: '9%' }}
              onClick={() => {
                sortChannel('response_time');
              }}
              hidden={showDetail}
            >
              {t('channel.table.response_time')}
            </Table.HeaderCell>
            <Table.HeaderCell
              style={{ cursor: 'pointer', width: '9%' }}
              onClick={() => {
                sortChannel('balance');
              }}
            >
              {t('channel.table.balance')}
            </Table.HeaderCell>
            <Table.HeaderCell
              style={{ cursor: 'pointer', width: '8%' }}
              onClick={() => {
                sortChannel('health');
              }}
              hidden={!showDetail}
            >
              {t('channel.table.health')}
            </Table.HeaderCell>
            <Table.HeaderCell
              style={{ cursor: 'pointer', width: '8%' }}
              onClick={() => {
                sortChannel('priority');
              }}
              hidden={!showDetail}
            >
              {t('channel.table.priority')}
            </Table.HeaderCell>
            <Table.HeaderCell
              hidden={!showDetail}
              style={{ width: '13%' }}
            >
              {t('channel.table.test_model')}
            </Table.HeaderCell>
            <Table.HeaderCell style={{ width: '24%' }}>
              {t('channel.table.actions')}
            </Table.HeaderCell>
          </Table.Row>
        </Table.Header>

        <Table.Body>
          {channels
            .slice(
              (activePage - 1) * itemsPerPage,
              activePage * itemsPerPage
            )
            .map((channel, idx) => {
              if (channel.deleted) return <></>;
              return (
                <Table.Row key={channel.id}>
                  <Table.Cell>{channel.id}</Table.Cell>
                  <Table.Cell>
                    <span className='cell-ellipsis' title={channel.name}>
                      {channel.name
                        ? channel.name
                        : t('channel.table.no_name')}
                    </span>
                  </Table.Cell>
                  <Table.Cell>
                    <span className='cell-nowrap'>
                      {renderGroup(channel.group)}
                    </span>
                  </Table.Cell>
                  <Table.Cell>
                    <span className='cell-nowrap'>
                      {renderType(channel.type, t)}
                    </span>
                  </Table.Cell>
                  <Table.Cell>
                    <span className='cell-nowrap'>
                      {renderStatus(channel.status, t)}
                    </span>
                  </Table.Cell>
                  <Table.Cell hidden={showDetail}>
                    <Popup
                      content={
                        channel.test_time
                          ? renderTimestamp(channel.test_time)
                          : t('channel.table.not_tested')
                      }
                      key={channel.id}
                      trigger={renderResponseTime(channel.response_time, t)}
                      basic
                    />
                  </Table.Cell>
                  <Table.Cell>
                    <Popup
                      trigger={
                        <span
                          onClick={() => {
                            updateChannelBalance(channel.id, channel.name, idx);
                          }}
                          style={{ cursor: 'pointer', whiteSpace: 'nowrap' }}
                        >
                          {renderBalance(channel.type, channel.balance, t)}
                        </span>
                      }
                      content={t('channel.table.click_to_update')}
                      basic
                    />
                  </Table.Cell>
                  <Table.Cell hidden={!showDetail}>
                    {renderHealthBadge(channel.id, t)}
                  </Table.Cell>
                  <Table.Cell hidden={!showDetail}>
                    <Popup
                      trigger={
                        <Input
                          type='number'
                          defaultValue={channel.priority}
                          onBlur={(event) => {
                            manageChannel(
                              channel.id,
                              'priority',
                              idx,
                              event.target.value
                            );
                          }}
                        >
                          <input style={{ maxWidth: '60px' }} />
                        </Input>
                      }
                      content={t('channel.table.priority_tip')}
                      basic
                    />
                  </Table.Cell>
                  <Table.Cell hidden={!showDetail}>
                    <Dropdown
                      placeholder={t('channel.table.select_test_model')}
                      selection
                      options={channel.model_options}
                      defaultValue={channel.test_model}
                      onChange={(event, data) => {
                        switchTestModel(idx, data.value);
                      }}
                    />
                  </Table.Cell>
                  <Table.Cell>
                    <div
                      className='channel-actions'
                      style={{
                        display: 'flex',
                        alignItems: 'center',
                        flexWrap: 'wrap',
                        gap: '2px',
                        rowGap: '6px',
                      }}
                    >
                      <Button
                        size={'tiny'}
                        positive
                        onClick={() => {
                          testChannel(
                            channel.id,
                            channel.name,
                            idx,
                            channel.test_model
                          );
                        }}
                      >
                        {t('channel.buttons.test')}
                      </Button>
                      <Popup
                        trigger={
                          <Button size='tiny' negative>
                            {t('channel.buttons.delete')}
                          </Button>
                        }
                        on='click'
                        flowing
                        hoverable
                      >
                        <Button
                          size={'tiny'}
                          negative
                          onClick={() => {
                            manageChannel(channel.id, 'delete', idx);
                          }}
                        >
                          {t('channel.buttons.confirm_delete')} {channel.name}
                        </Button>
                      </Popup>
                      <Button
                        size={'tiny'}
                        onClick={() => {
                          manageChannel(
                            channel.id,
                            channel.status === 1 ? 'disable' : 'enable',
                            idx
                          );
                        }}
                      >
                        {channel.status === 1
                          ? t('channel.buttons.disable')
                          : t('channel.buttons.enable')}
                      </Button>
                      <Button
                        size={'tiny'}
                        as={Link}
                        to={'/channel/edit/' + channel.id}
                      >
                        {t('channel.buttons.edit')}
                      </Button>
                      <Button
                        size={'tiny'}
                        color='teal'
                        onClick={() => {
                          manageChannel(channel.id, 'clone', idx);
                        }}
                      >
                        {t('channel.buttons.clone')}
                      </Button>
                    </div>
                  </Table.Cell>
                </Table.Row>
              );
            })}
        </Table.Body>

        <Table.Footer>
          <Table.Row>
            <Table.HeaderCell colSpan={showDetail ? '11' : '8'}>
              <div className='channels-table-footer'>
              <Button size='tiny' as={Link} to='/channel/add' loading={loading}>
                {t('channel.buttons.add')}
              </Button>
              <Button
                size='tiny'
                loading={loading}
                onClick={() => {
                  testChannels('all');
                }}
              >
                {t('channel.buttons.test_all')}
              </Button>
              <Button
                size='tiny'
                loading={loading}
                onClick={() => {
                  testChannels('disabled');
                }}
              >
                {t('channel.buttons.test_disabled')}
              </Button>
              <Popup
                trigger={
                  <Button size='tiny' loading={loading}>
                    {t('channel.buttons.delete_disabled')}
                  </Button>
                }
                on='click'
                flowing
                hoverable
              >
                <Button
                  size='tiny'
                  loading={loading}
                  negative
                  onClick={deleteAllDisabledChannels}
                >
                  {t('channel.buttons.confirm_delete_disabled')}
                </Button>
              </Popup>
              <Dropdown
                selection
                options={ITEMS_PER_PAGE_OPTIONS}
                value={itemsPerPage}
                onChange={handleItemsPerPageChange}
                placeholder={t('common.page_size') || '每页显示'}
                style={{ marginRight: '10px' }}
              />
              <Pagination
                activePage={activePage}
                onPageChange={onPaginationChange}
                size='tiny'
                siblingRange={1}
                totalPages={
                  channels.length === 0
                    ? 1
                    : Math.ceil(channels.length / itemsPerPage)
                }
              />
              <Button size='tiny' onClick={refresh} loading={loading}>
                {t('channel.buttons.refresh')}
              </Button>
              <Button size='tiny' onClick={toggleShowDetail}>
                {showDetail
                  ? t('channel.buttons.hide_detail')
                  : t('channel.buttons.show_detail')}
              </Button>
              </div>
            </Table.HeaderCell>
          </Table.Row>
        </Table.Footer>
      </Table>
      </div>
    </>
  );
};

export default ChannelsTable;

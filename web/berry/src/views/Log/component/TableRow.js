import PropTypes from 'prop-types';

import { Box, Stack, TableRow, TableCell } from '@mui/material';

import { timestamp2string, renderQuota } from 'utils/common';
import Label from 'ui-component/Label';
import LogType from '../type/LogType';

function renderType(type) {
  const typeOption = LogType[type];
  if (typeOption) {
    return (
      <Label variant="filled" color={typeOption.color}>
        {' '}
        {typeOption.text}{' '}
      </Label>
    );
  } else {
    return (
      <Label variant="filled" color="error">
        {' '}
        未知{' '}
      </Label>
    );
  }
}

export default function LogTableRow({ item, userIsAdmin }) {
  return (
    <>
      <TableRow tabIndex={item.id}>
        <TableCell>{timestamp2string(item.created_at)}</TableCell>

        {userIsAdmin && <TableCell>{item.channel || ''}</TableCell>}
        {userIsAdmin && (
          <TableCell>
            <Label color="default" variant="outlined">
              {item.username}
            </Label>
          </TableCell>
        )}
        <TableCell>
          {item.token_name && (
            <Label color="default" variant="soft">
              {item.token_name}
            </Label>
          )}
        </TableCell>
        <TableCell>{renderType(item.type)}</TableCell>
        <TableCell>
          {item.model_name && (
            <Label color="primary" variant="outlined">
              {item.model_name}
            </Label>
          )}
        </TableCell>
        <TableCell>{item.prompt_tokens || ''}</TableCell>
        <TableCell>{item.completion_tokens || ''}</TableCell>
        <TableCell>{item.quota ? renderQuota(item.quota, 6) : ''}</TableCell>
        <TableCell sx={{ verticalAlign: 'top' }}>
          <Stack spacing={0.5}>
            <Box sx={{ whiteSpace: 'normal', wordWrap: 'break-word' }}>{item.content}</Box>
            <Stack direction="row" spacing={0.5} flexWrap="wrap" useFlexGap>
              {item.elapsed_time > 0 && (
                <Label variant="filled" color={item.type === 2 && item.content.includes('探针确认') ? 'warning' : 'error'}>
                  {item.elapsed_time} ms
                </Label>
              )}
              <Label variant="filled" color="secondary">
                {item.is_stream ? 'Stream' : 'Non-Stream'}
              </Label>
            </Stack>
          </Stack>
        </TableCell>
      </TableRow>
    </>
  );
}

LogTableRow.propTypes = {
  item: PropTypes.object,
  userIsAdmin: PropTypes.bool
};

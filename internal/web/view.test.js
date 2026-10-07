import assert from 'node:assert/strict';
import test from 'node:test';

import { view } from './static/view.js';

const states = [
  {
    state: { status: 'waiting_for_approval', hostname: 'workstation', os_name: 'Ubuntu', os_version: '26.04' },
    message: 'Pair a computer?',
    name: 'workstation',
    os: 'Ubuntu 26.04',
    buttons: ['Accept', 'Reject'],
    code: '',
    poll: true,
  },
  {
    state: { status: 'waiting_for_code', hostname: 'workstation', os_name: 'Ubuntu', os_version: '26.04', pairing_code: '1234-5678' },
    message: 'Type this code into the terminal of workstation:',
    notice: 'Never give this code to anyone.',
    name: 'workstation',
    os: 'Ubuntu 26.04',
    buttons: [],
    code: '1234-5678',
    poll: true,
  },
  {
    state: { status: 'finishing', hostname: 'workstation', os_name: 'Ubuntu', os_version: '26.04' },
    message: 'Code accepted. Finishing on workstation…',
    name: 'workstation',
    os: 'Ubuntu 26.04',
    buttons: [],
    code: '',
    poll: true,
  },
  {
    state: { status: 'paired', hostname: 'workstation', display_name: 'My workstation', os_name: 'Ubuntu', os_version: '26.04' },
    message: '✓ My workstation is paired.',
    notice: 'You can close this tab.',
    name: 'My workstation',
    os: 'Ubuntu 26.04',
    buttons: [],
    code: '',
    poll: false,
  },
  {
    state: { status: 'rejected', hostname: 'workstation', os_name: 'Ubuntu', os_version: '26.04' },
    message: 'Rejected. This computer was not paired.',
    name: 'workstation',
    os: 'Ubuntu 26.04',
    buttons: [],
    code: '',
    poll: false,
  },
  {
    state: { status: 'expired', hostname: 'workstation', os_name: 'Ubuntu', os_version: '26.04' },
    message: '✗ This link has expired. Run tervi pair on the computer again to get a new link.',
    name: 'workstation',
    os: 'Ubuntu 26.04',
    buttons: [],
    code: '',
    poll: false,
    error: true,
  },
  {
    state: { status: 'failed', failure_reason: 'wrong_codes', hostname: 'workstation', os_name: 'Ubuntu', os_version: '26.04' },
    message: '✗ Pairing failed: the wrong code was typed 5 times. Run tervi pair on the computer again to start over.',
    name: 'workstation',
    os: 'Ubuntu 26.04',
    buttons: [],
    code: '',
    poll: false,
    error: true,
  },
  {
    state: { status: 'failed', failure_reason: 'not_saved', hostname: 'workstation', os_name: 'Ubuntu', os_version: '26.04' },
    message: '✗ Pairing failed: the computer couldn\'t save its credential. Check the terminal on workstation for details.',
    name: 'workstation',
    os: 'Ubuntu 26.04',
    buttons: [],
    code: '',
    poll: false,
    error: true,
  },
  {
    state: { status: 'failed', failure_reason: 'not_confirmed', hostname: 'workstation', os_name: 'Ubuntu', os_version: '26.04' },
    message: '✗ Pairing failed: the computer didn\'t confirm in time. Run tervi pair on the computer again.',
    name: 'workstation',
    os: 'Ubuntu 26.04',
    buttons: [],
    code: '',
    poll: false,
    error: true,
  },
];

for (const expected of states) {
  test(`view maps ${expected.state.status}${expected.state.failure_reason ? `/${expected.state.failure_reason}` : ''}`, () => {
    const actual = view(expected.state);
    assert.equal(actual.message, expected.message);
    if (expected.notice) assert.equal(actual.notice, expected.notice);
    assert.equal(actual.name, expected.name);
    assert.equal(actual.os, expected.os);
    assert.deepEqual(actual.buttons, expected.buttons);
    assert.equal(actual.code, expected.code);
    assert.equal(actual.poll, expected.poll);
    assert.equal(Boolean(actual.error), Boolean(expected.error));
    if (actual.poll) assert.ok(['waiting_for_approval', 'waiting_for_code', 'finishing'].includes(expected.state.status));
    if (actual.buttons.length > 0) assert.equal(expected.state.status, 'waiting_for_approval');
    if (actual.code) assert.equal(expected.state.status, 'waiting_for_code');
  });
}

test('empty hostname and OS fields are shown as unknown', () => {
  const actual = view({ status: 'waiting_for_approval', hostname: '', os_name: '', os_version: '' });
  assert.equal(actual.name, 'unknown');
  assert.equal(actual.os, 'unknown unknown');
});

test('invalid_link gives an error result without polling', () => {
  const actual = view({ error: 'invalid_link' });
  assert.equal(actual.message, '✗ Invalid link.');
  assert.equal(actual.poll, false);
  assert.deepEqual(actual.buttons, []);
  assert.equal(actual.code, '');
  assert.equal(actual.error, true);
});

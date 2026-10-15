import assert from 'node:assert/strict';
import test from 'node:test';

import { afterRefresh, afterRevoke, REFRESH_MS, revokeQuestion, view } from './machines-view.js';

test('view builds the online row', () => {
  const result = view({ machines: [{
    display_name: 'bert-desktop', os_name: 'Ubuntu', os_version: '26.04', online: true,
    last_seen_seconds_ago: 0,
  }] });

  assert.deepEqual(result.rows, ['● bert-desktop   Ubuntu 26.04   Online']);
});

test('view builds the offline row with last seen', () => {
  const result = view({ machines: [{
    display_name: 'bert-desktop', os_name: 'Ubuntu', os_version: '26.04', online: false,
    last_seen_seconds_ago: 5,
  }] });

  assert.deepEqual(result.rows, [
    '○ bert-desktop   Ubuntu 26.04   Offline · last seen less than a minute ago',
  ]);
});

test('view builds the offline row for a machine never connected', () => {
  const result = view({ machines: [{
    display_name: 'bert-desktop', os_name: 'Ubuntu', os_version: '26.04', online: false,
    last_seen_seconds_ago: null,
  }] });

  assert.deepEqual(result.rows, [
    '○ bert-desktop   Ubuntu 26.04   Offline · never connected',
  ]);
});

test('view formats every last-seen time boundary', () => {
  const cases = [
    [59, 'less than a minute ago'],
    [60, '1 minute ago'],
    [3599, '59 minutes ago'],
    [3600, '1 hour ago'],
    [86399, '23 hours ago'],
    [86400, '1 day ago'],
  ];

  for (const [seconds, expected] of cases) {
    const result = view({ machines: [{
      display_name: 'time-check', os_name: 'Linux', os_version: '1', online: false,
      last_seen_seconds_ago: seconds,
    }] });
    assert.equal(result.rows[0], `○ time-check   Linux 1   Offline · last seen ${expected}`, `${seconds} seconds`);
  }
});

test('view describes an empty list', () => {
  const result = view({ machines: [], loaded: true, address: 'http://localhost:8080' });

  assert.equal(result.emptyMessage, 'No machines yet. Pair one with: tervi pair --server http://localhost:8080');
  assert.deepEqual(result.rows, []);
});

test('view does not describe an unloaded list as empty', () => {
  for (const failed of [false, true]) {
    const result = view({ machines: [], failed, loaded: false, address: 'http://localhost:8080' });
    assert.equal(result.emptyMessage, '');
  }
});

test('view shows a request failure while keeping the last list', () => {
  const machines = [{
    display_name: 'last good result', os_name: 'Linux', os_version: '1', online: false,
    last_seen_seconds_ago: null,
  }];
  const result = view({ machines, failed: true });

  assert.equal(result.failureMessage, "Can't reach the server. Trying again…");
  assert.deepEqual(result.rows, ['○ last good result   Linux 1   Offline · never connected']);
});

test('view keeps machine names as literal text', () => {
  const result = view({ machines: [{
    display_name: '<b>computer</b>', os_name: 'Linux', os_version: '1', online: true,
    last_seen_seconds_ago: null,
  }] });

  assert.equal(result.rows[0], '● <b>computer</b>   Linux 1   Online');
});

test('view exports the five-second refresh interval', () => {
  assert.equal(REFRESH_MS, 5000);
});

test('revoke question includes the machine name as literal text', () => {
  assert.equal(
    revokeQuestion({ display_name: 'bert-desktop' }),
    'Revoke bert-desktop? It stops working at once and must be paired again to come back.',
  );
  assert.equal(
    revokeQuestion({ display_name: '<b>computer</b>' }),
    'Revoke <b>computer</b>? It stops working at once and must be paired again to come back.',
  );
  const result = view({ machines: [{
    display_name: '<b>computer</b>', os_name: 'Linux', os_version: '1', online: true,
  }] });
  assert.equal(result.rows[0], '● <b>computer</b>   Linux 1   Online');
});

test('view shows a revoke failure by machine name', () => {
  const result = view({ machines: [], revokeFailure: 'bert-desktop' });

  assert.equal(result.failureMessage, "Couldn't revoke bert-desktop. Try again.");
});

test('afterRevoke removes machines on 204 and 404 without a failure', () => {
  const state = { machines: [
    { machine_id: 'one', display_name: 'first' },
    { machine_id: 'two', display_name: 'second' },
  ], revokeFailure: null };

  for (const status of [204, 404]) {
    const result = afterRevoke(state, 'one', status);
    assert.deepEqual(result.machines, [{ machine_id: 'two', display_name: 'second' }]);
    assert.equal(result.revokeFailure, null);
  }
});

test('afterRevoke keeps machines and names the failure for errors or no answer', () => {
  const state = { machines: [
    { machine_id: 'one', display_name: 'bert-desktop' },
    { machine_id: 'two', display_name: 'other' },
  ], revokeFailure: null };

  for (const status of [500, null]) {
    const result = afterRevoke(state, 'one', status);
    assert.deepEqual(result.machines, state.machines);
    assert.equal(result.revokeFailure, 'bert-desktop');
    assert.equal(view(result).failureMessage, "Couldn't revoke bert-desktop. Try again.");
  }

  const afterRefresh = view({ machines: state.machines, revokeFailure: null });
  assert.equal(afterRefresh.failureMessage, '');
});

test('afterRevoke leaves a row removed by refresh absent and names the failure', () => {
  const machine = { machine_id: 'one', display_name: 'bert-desktop' };
  const state = { machines: [{ machine_id: 'two', display_name: 'other' }], revokeMachine: machine };

  const failed = afterRevoke(state, 'one', 500);
  assert.deepEqual(failed.machines, state.machines);
  assert.equal(failed.revokeFailure, 'bert-desktop');

  const renamed = { machine_id: 'one', display_name: 'new server name' };
  const failedAfterRename = afterRevoke({ ...state, machines: [renamed] }, 'one', 500);
  assert.deepEqual(failedAfterRename.machines, [renamed]);
  assert.equal(failedAfterRename.revokeFailure, 'bert-desktop');

  const gone = afterRevoke(state, 'one', 404);
  assert.deepEqual(gone.machines, state.machines);
  assert.equal(gone.revokeFailure, null);
});

test('afterRefresh filters successful revokes and preserves the answer order', () => {
  const state = {
    machines: [{ machine_id: 'old', display_name: 'old result' }],
    revokeFailure: 'previous failure',
  };
  const answerMachines = [
    { machine_id: 'two', display_name: 'second' },
    { machine_id: 'one', display_name: 'first' },
    { machine_id: 'three', display_name: 'third' },
  ];

  const result = afterRefresh(state, answerMachines, ['one'], false);
  assert.deepEqual(result.machines, [answerMachines[0], answerMachines[2]]);
  assert.equal(result.revokeFailure, null);
});

test('afterRefresh keeps the revoke failure when one failed while refresh waited', () => {
  const state = {
    machines: [{ machine_id: 'one', display_name: 'bert-desktop' }],
    revokeFailure: 'bert-desktop',
  };
  const result = afterRefresh(state, [], [], true);

  assert.deepEqual(result.machines, []);
  assert.equal(result.revokeFailure, 'bert-desktop');
});

test('afterRefresh keeps the current list when refresh fails', () => {
  const machines = [{ machine_id: 'one', display_name: 'bert-desktop' }];
  const result = afterRefresh({ machines, revokeFailure: 'bert-desktop' }, null, ['one'], false);

  assert.deepEqual(result.machines, machines);
  assert.equal(result.revokeFailure, null);
});

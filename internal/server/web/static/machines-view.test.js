import assert from 'node:assert/strict';
import test from 'node:test';

import { REFRESH_MS, view } from './machines-view.js';

test('view builds the online row', () => {
  const result = view({ machines: [{
    display_name: 'bert-desktop', os_name: 'Ubuntu', os_version: '26.04', online: true,
    last_seen_seconds_ago: 0,
  }] });

  assert.equal(result.heading, 'Machines');
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

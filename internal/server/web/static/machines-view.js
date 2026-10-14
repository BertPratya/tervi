import { shown } from './view.js';

export const REFRESH_MS = 5000;

function timeAgo(seconds) {
  const value = Math.max(0, Math.floor(seconds));
  if (value < 60) return 'less than a minute ago';
  if (value < 3600) {
    const minutes = Math.floor(value / 60);
    return `${minutes} ${minutes === 1 ? 'minute' : 'minutes'} ago`;
  }
  if (value < 86400) {
    const hours = Math.floor(value / 3600);
    return `${hours} ${hours === 1 ? 'hour' : 'hours'} ago`;
  }
  const days = Math.floor(value / 86400);
  return `${days} ${days === 1 ? 'day' : 'days'} ago`;
}

export function view(state = {}) {
  const machines = Array.isArray(state.machines) ? state.machines : [];
  const rows = machines.map((machine) => {
    const name = shown(machine.display_name);
    const os = `${shown(machine.os_name)} ${shown(machine.os_version)}`;
    if (machine.online === true) return `● ${name}   ${os}   Online`;
    const lastSeen = machine.last_seen_seconds_ago == null
      ? 'never connected'
      : `last seen ${timeAgo(machine.last_seen_seconds_ago)}`;
    return `○ ${name}   ${os}   Offline · ${lastSeen}`;
  });

  return {
    rows,
    emptyMessage: state.loaded === true && rows.length === 0
      ? `No machines yet. Pair one with: tervi pair --server ${state.address}`
      : '',
    failureMessage: state.failed === true ? "Can't reach the server. Trying again…" : '',
  };
}

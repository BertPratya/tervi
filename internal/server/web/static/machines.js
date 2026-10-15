import { afterRefresh, afterRevoke, REFRESH_MS, revokeQuestion, view } from '/static/machines-view.js';
import { shown } from '/static/view.js';

const app = document.querySelector('#app');
const failureLine = document.querySelector('.warning');
const revokeDialog = document.querySelector('#revoke-dialog');
const revokeQuestionLine = document.querySelector('#revoke-question');
const confirmRevoke = document.querySelector('#confirm-revoke');
const cancelRevoke = document.querySelector('#cancel-revoke');
let machines = [];
let failed = false;
let loaded = false;
let revokeFailure = null;
let pendingRevoke = null;
let waitingRefresh = null;

function textElement(tag, className, text) {
  const element = document.createElement(tag);
  if (className) element.className = className;
  element.textContent = text;
  return element;
}

function render() {
  const page = view({ machines, failed, loaded, address: location.origin, revokeFailure });
  if (failureLine.textContent !== page.failureMessage) {
    failureLine.textContent = page.failureMessage;
  }
  app.replaceChildren();

  if (page.rows.length === 0) {
    if (page.emptyMessage) app.append(textElement('p', '', page.emptyMessage));
    return;
  }

  const list = document.createElement('ul');
  for (let index = 0; index < page.rows.length; index += 1) {
    const machine = machines[index];
    const row = textElement('li', '', '');
    row.append(textElement('span', '', page.rows[index]));
    const button = textElement('button', '', 'Revoke');
    button.type = 'button';
    button.dataset.machineId = machine.machine_id;
    button.setAttribute('aria-label', `Revoke ${shown(machine.display_name)}`);
    row.append(' ', button);
    list.append(row);
  }
  app.append(list);
}

async function revoke(machine) {
  let status = null;
  try {
    const response = await fetch(`/api/v1/machines/${encodeURIComponent(machine.machine_id)}/revoke`, {
      method: 'POST',
    });
    status = response.status;
  } catch {
    // A lost answer leaves the row visible so the user can try again.
  }

  if (waitingRefresh) {
    if (status === 204 || status === 404) {
      waitingRefresh.revokedSince.add(machine.machine_id);
    } else {
      waitingRefresh.failedSince = true;
    }
  }

  const next = afterRevoke({ machines, revokeFailure, revokeMachine: machine }, machine.machine_id, status);
  machines = next.machines;
  revokeFailure = next.revokeFailure;
  render();
}

app.addEventListener('click', (event) => {
  const button = event.target.closest('button[data-machine-id]');
  if (!button) return;
  const machine = machines.find((item) => item.machine_id === button.dataset.machineId);
  if (!machine) return;
  pendingRevoke = machine;
  revokeQuestionLine.textContent = revokeQuestion(machine);
  revokeDialog.showModal();
});

cancelRevoke.addEventListener('click', () => revokeDialog.close());
revokeDialog.addEventListener('close', () => {
  pendingRevoke = null;
});
confirmRevoke.addEventListener('click', () => {
  if (!pendingRevoke) return;
  const machine = pendingRevoke;
  pendingRevoke = null;
  revokeDialog.close();
  void revoke(machine);
});

async function refresh() {
  const tracker = { revokedSince: new Set(), failedSince: false };
  waitingRefresh = tracker;
  let answerMachines = null;
  try {
    const response = await fetch('/api/v1/machines', { cache: 'no-store' });
    if (!response.ok) throw new Error('request failed');
    const answer = await response.json();
    if (!Array.isArray(answer.machines)) throw new Error('invalid response');
    answerMachines = answer.machines;
    failed = false;
    loaded = true;
  } catch {
    failed = true;
  }

  if (waitingRefresh === tracker) waitingRefresh = null;
  const next = afterRefresh(
    { machines, revokeFailure },
    answerMachines,
    tracker.revokedSince,
    tracker.failedSince,
  );
  machines = next.machines;
  revokeFailure = next.revokeFailure;
  render();
  setTimeout(refresh, REFRESH_MS);
}

render();
refresh();

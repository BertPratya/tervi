import { view } from '/static/view.js';

const app = document.querySelector('#app');
const approvalKey = window.location.pathname.slice('/pair/'.length);
const initialView = view({ status: 'loading' });
let currentView = initialView;
let hasAnswer = false;
let actionRunning = false;
let inFlight = null;
let pollTimer = null;
let renderedKey = null;

function textElement(tag, className, text) {
  const element = document.createElement(tag);
  if (className) element.className = className;
  element.textContent = text;
  return element;
}

function render(nextView) {
  const nextKey = JSON.stringify(nextView);
  if (nextKey === renderedKey) {
    currentView = nextView;
    return;
  }
  renderedKey = nextKey;
  currentView = nextView;
  app.replaceChildren();

  const heading = textElement('h1', nextView.error ? 'error' : '', nextView.message);
  app.append(heading);

  if (nextView.message === 'Pair a computer?') {
    const details = document.createElement('section');
    details.className = 'details';
    details.append(textElement('p', '', `Computer: ${nextView.name}`));
    details.append(textElement('p', '', nextView.os));
    app.append(details);
  } else if (nextView.message.startsWith('✓ ')) {
    app.append(textElement('p', 'details', `${nextView.os}.`));
  }

  if (nextView.code) {
    app.append(textElement('strong', 'pairing-code', nextView.code));
  }
  if (nextView.notice) {
    app.append(textElement('p', '', nextView.notice));
  }

  if (nextView.buttons.length > 0) {
    const actions = document.createElement('div');
    actions.className = 'actions';
    for (const label of nextView.buttons) {
      const button = document.createElement('button');
      button.type = 'button';
      button.textContent = label;
      button.disabled = actionRunning;
      button.addEventListener('click', () => performAction(label.toLowerCase()));
      actions.append(button);
    }
    app.append(actions);
  }
}

function disableButtons(disabled) {
  for (const button of app.querySelectorAll('.actions button')) {
    button.disabled = disabled;
  }
}

async function request(path) {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 10_000);
  try {
    const response = await fetch(path, {
      method: path.endsWith('/current') ? 'GET' : 'POST',
      headers: { Authorization: `Bearer ${approvalKey}` },
      signal: controller.signal,
    });
    let answer;
    try {
      answer = await response.json();
    } catch {
      throw new Error('invalid response');
    }
    if (response.status === 404 && answer.error === 'invalid_link') {
      return { error: 'invalid_link' };
    }
    if (!response.ok) throw new Error('request failed');
    return answer;
  } finally {
    clearTimeout(timeout);
  }
}

function schedulePoll() {
  if (pollTimer !== null) clearTimeout(pollTimer);
  pollTimer = setTimeout(() => {
    pollTimer = null;
    poll();
  }, 2000);
}

async function poll() {
  if (actionRunning || inFlight !== null) return;
  const pending = request('/api/v1/approvals/current');
  inFlight = pending;
  try {
    const answer = await pending;
    hasAnswer = true;
    render(view(answer));
  } catch {
    if (!hasAnswer) render(initialView);
  } finally {
    if (inFlight === pending) inFlight = null;
    if (!actionRunning && currentView.poll) schedulePoll();
  }
}

async function performAction(action) {
  if (actionRunning) return;
  actionRunning = true;
  if (pollTimer !== null) {
    clearTimeout(pollTimer);
    pollTimer = null;
  }
  disableButtons(true);

  const previous = inFlight;
  if (previous !== null) await previous.catch(() => {});

  const pending = request(`/api/v1/approvals/current/${action}`);
  inFlight = pending;
  try {
    const answer = await pending;
    hasAnswer = true;
    render(view(answer));
  } catch {
    // Keep the last state visible; the next poll will retry the read.
  } finally {
    if (inFlight === pending) inFlight = null;
    actionRunning = false;
    disableButtons(false);
    if (currentView.poll) schedulePoll();
  }
}

render(initialView);
poll();

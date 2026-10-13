import { REFRESH_MS, view } from '/static/machines-view.js';

const app = document.querySelector('#app');
let machines = [];
let failed = false;

function textElement(tag, className, text) {
  const element = document.createElement(tag);
  if (className) element.className = className;
  element.textContent = text;
  return element;
}

function render() {
  const page = view({ machines, failed });
  app.replaceChildren();
  app.append(textElement('h1', '', page.heading));
  if (page.failureMessage) app.append(textElement('p', 'warning', page.failureMessage));

  if (page.rows.length === 0) {
    app.append(textElement('p', '', page.emptyMessage));
    return;
  }

  const list = document.createElement('ul');
  for (const row of page.rows) list.append(textElement('li', '', row));
  app.append(list);
}

async function refresh() {
  try {
    const response = await fetch('/api/v1/machines', { cache: 'no-store' });
    if (!response.ok) throw new Error('request failed');
    const answer = await response.json();
    if (!Array.isArray(answer.machines)) throw new Error('invalid response');
    machines = answer.machines;
    failed = false;
  } catch {
    failed = true;
  }

  render();
  setTimeout(refresh, REFRESH_MS);
}

render();
refresh();

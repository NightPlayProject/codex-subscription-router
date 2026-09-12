const { test } = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const path = require('node:path');

class Element {
  constructor(tag) { this.tag = tag; this.children = []; this.style = {}; this.events = {}; this.attributes = {}; this.scrollTop = 0; }
  append(...items) { this.children.push(...items); }
  appendChild(item) { this.append(item); }
  replaceChildren() { this.children = []; }
  addEventListener(name, handler) { this.events[name] = handler; }
  setAttribute(name, value) { this.attributes[name] = value; }
  focus() {}
}
const settle = () => new Promise(resolve => setImmediate(resolve));
async function setup() {
  const body = new Element('body');
  const state = { accounts: [{ id: 'primary', label: 'Primary', controller: true, connected: true, enabled: true }], calls: [] };
  const context = {
    document: { body, head: new Element('head'), readyState: 'complete', getElementById: () => null, createElement: tag => new Element(tag) },
    window: {}, Intl, URL,
    setInterval: callback => { state.poll = callback; },
    fetch: async (url, options) => {
      const route = url.split('/v1')[1];
      state.calls.push([route, options.method]);
      if (state.failRouting && route === '/routing-preference' && options.method === 'PUT') return { ok: false, json: async () => ({ error: 'Refresh failed' }) };
      let result = {};
      if (route === '/accounts' && options.method === 'POST') {
        const account = { id: 'new-account', label: 'Work', enabled: true, connected: false, threadCount: 0 };
        state.accounts.push(account); result = { account };
      } else if (route === '/accounts') result = { accounts: state.accounts };
      else if (route === '/routing-preference') result = options.body ? JSON.parse(options.body) : { accountId: '' };
      else if (route.endsWith('/login')) result = { login: { userCode: 'TEST-CODE' } };
      else if (route.endsWith('/remove')) state.accounts = state.accounts.filter(a => a.id !== 'new-account');
      return { ok: true, json: async () => result };
    },
  };
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../ui/windows-account-panel.js'), 'utf8'), context);
  await settle();
  const all = (node = body) => [node, ...node.children.flatMap(child => all(child))];
  const button = label => all().find(item => item.tag === 'button' && item.textContent === label);
  return { state, context, all, button };
}

test('device code clears after a successful background sign-in refresh', async () => {
  const ui = await setup();
  ui.button('Add subscription').events.click(); await settle();
  assert(ui.all().some(item => item.textContent === 'TEST-CODE'));
  ui.state.accounts[1].connected = true;
  await ui.state.poll();
  assert(!ui.all().some(item => item.textContent === 'TEST-CODE'));
});

test('chat selection also selects the plugin account after server success', async () => {
  const ui = await setup();
  ui.button('Add subscription').events.click(); await settle();
  ui.state.accounts[1].connected = true; await ui.state.poll();
  const select = ui.all().find(item => item.attributes['aria-label'] === 'Chat subscription');
  select.value = 'new-account'; select.events.change(); await settle();
  assert.equal(ui.context.__codexMuxPluginAccountId, 'new-account');
  assert(ui.state.calls.some(([route, method]) => route === '/routing-preference' && method === 'PUT'));
});

test('failed subscription refresh keeps previous plugin selection and shows the error', async () => {
  const ui = await setup();
  ui.state.failRouting = true;
  const select = ui.all().find(item => item.attributes['aria-label'] === 'Chat subscription');
  select.value = 'new-account'; select.events.change(); await settle();
  assert.equal(ui.context.__codexMuxPluginAccountId, 'primary');
  assert(ui.all().some(item => item.textContent === 'Refresh failed'));
});

test('signed-out account can be removed and pending code clears', async () => {
  const ui = await setup();
  ui.button('Add subscription').events.click(); await settle();
  assert.equal(ui.button('Remove').disabled, false);
  ui.button('Remove').events.click(); await settle();
  assert.equal(ui.state.accounts.length, 1);
  assert(!ui.all().some(item => item.textContent === 'TEST-CODE'));
});

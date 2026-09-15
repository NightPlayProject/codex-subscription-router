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
  setCustomValidity() {}
  reportValidity() {}
  select() {}
  querySelector() { return undefined; }
  contains(target) { return this === target || this.children.some(child => child.contains(target)); }
  get classList() {
    return {
      add: value => { this.className = [...new Set((this.className || '').split(' ').concat(value))].join(' '); },
      contains: value => (this.className || '').split(' ').includes(value),
    };
  }
}
const settle = () => new Promise(resolve => setImmediate(resolve));
async function setup(mainWorkspace = true, initialState = {}) {
  const body = new Element('body');
  const state = {
    accounts: [{ id: 'primary', label: 'Primary', controller: true, connected: true, enabled: true }],
    calls: [],
    ...initialState,
  };
  const context = {
    document: { querySelector: () => mainWorkspace ? {} : null, addEventListener: (name, handler) => { state[name] = handler; }, body, head: new Element('head'), readyState: 'complete', getElementById: () => null, createElement: tag => new Element(tag) },
    window: {}, Intl, URL, setTimeout,
    setInterval: (callback, delay) => { if (delay === 1000) state.poll = callback; else state.updatePoll = callback; },
    fetch: async (url, options) => {
      const route = url.split('/v1')[1];
      state.calls.push([route, options.method]);
      const method = String(options.method || 'GET').toUpperCase();
      if (method === 'GET' && state.getFailuresRemaining > 0) {
        state.getFailuresRemaining -= 1;
        throw new TypeError('Failed to fetch');
      }
      if (method !== 'GET' && state.failNextMutation) {
        state.failNextMutation = false;
        throw new TypeError('Failed to fetch');
      }
      if (state.failRouting && route === '/routing-preference' && options.method === 'PUT') return { ok: false, json: async () => ({ error: 'Refresh failed' }) };
      let result = {};
      if (route === '/updates') {
        if (options.method === 'POST') state.update = {...state.update, queued:true};
        return {ok:true,json:async()=>state.update || {supported:true}};
      }
      if (route === '/accounts' && options.method === 'POST') {
        const account = { id: 'new-account', label: 'Work', enabled: true, connected: false, threadCount: 0 };
        state.accounts.push(account); result = { account };
      } else if (route === '/accounts') result = { accounts: state.accounts };
      else if (route === '/routing-preference') {
        if (options.body) state.accountId = JSON.parse(options.body).accountId;
        result = { accountId: state.accountId || '', preparation: JSON.parse(JSON.stringify(state.preparation || {})) };
      }
      else if (route === '/accounts/primary' && options.method === 'PATCH') {
        if (state.failRename) return {ok:false,json:async()=>({error:'Rename failed'})};
        state.accounts[0].label = JSON.parse(options.body).label;
      }
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

test('cold start retries transient read failures and recovers without a stale error', async () => {
  const ui = await setup(true, { getFailuresRemaining: 2 });
  await new Promise(resolve => setTimeout(resolve, 250));
  assert(ui.button('Subscriptions'));
  assert(ui.state.calls.filter(([route]) => route === '/accounts' || route === '/routing-preference').length >= 4);
  assert(!ui.all().some(item => item.textContent === 'Failed to fetch'));
});

test('mutation requests stay single-shot on network failure', async () => {
  const ui = await setup();
  const select = ui.all().find(item => item.attributes['aria-label'] === 'Subscription for all chats');
  const before = ui.state.calls.filter(([route, method]) => route === '/routing-preference' && method === 'PUT').length;
  ui.state.failNextMutation = true;
  select.value = 'primary';
  select.events.change();
  await settle();
  const after = ui.state.calls.filter(([route, method]) => route === '/routing-preference' && method === 'PUT').length;
  assert.equal(after - before, 1);
  assert(ui.all().some(item => item.textContent === 'Failed to fetch'));
});

test('successful refresh clears a previous connection or routing error', async () => {
  const ui = await setup();
  const select = ui.all().find(item => item.attributes['aria-label'] === 'Subscription for all chats');
  ui.state.failRouting = true;
  select.value = 'primary';
  select.events.change();
  await settle();
  assert(ui.all().some(item => item.textContent === 'Refresh failed'));
  ui.state.failRouting = false;
  ui.button('Refresh').events.click();
  await settle();
  assert(!ui.all().some(item => item.textContent === 'Refresh failed'));
});

test('updates are checked independently and queued explicitly for next launch', async () => {
  const ui = await setup();
  assert(ui.state.calls.some(([route]) => route === '/updates'));
  ui.state.update = {supported:true,available:true};
  ui.button('Check for updates').events.click(); await settle();
  assert(ui.button('Update on next launch'));
  assert(!ui.state.calls.some(([route,method])=>route==='/updates' && method==='POST'));
  ui.button('Update on next launch').events.click(); await settle();
  assert(ui.all().some(item=>item.textContent?.startsWith('Update queued.')));
  assert(!ui.button('Update on next launch'));
});

test('device code clears after a successful background sign-in refresh', async () => {
  const ui = await setup();
  ui.button('Add subscription').events.click(); await settle();
  assert(ui.all().some(item => item.textContent === 'TEST-CODE'));
  ui.state.accounts[1].connected = true;
  await ui.state.poll();
  assert(!ui.all().some(item => item.textContent === 'TEST-CODE'));
});

test('chat selection leaves the plugin account unchanged after server success', async () => {
  const ui = await setup();
  ui.button('Add subscription').events.click(); await settle();
  ui.state.accounts[1].connected = true; await ui.state.poll();
  ui.context.__codexMuxPluginAccountId = 'primary';
  const select = ui.all().find(item => item.attributes['aria-label'] === 'Subscription for all chats');
  const readsBefore = ui.state.calls.filter(([route, method]) => (route === '/accounts' || route === '/routing-preference') && method === 'GET').length;
  select.value = 'new-account'; select.events.change(); await settle();
  assert.equal(ui.context.__codexMuxPluginAccountId, 'primary');
  assert(ui.state.calls.some(([route, method]) => route === '/routing-preference' && method === 'PUT'));
  const readsAfter = ui.state.calls.filter(([route, method]) => (route === '/accounts' || route === '/routing-preference') && method === 'GET').length;
  assert.equal(readsAfter, readsBefore, 'successful routing change performed a redundant full refresh');
});

test('failed subscription refresh keeps previous plugin selection and shows the error', async () => {
  const ui = await setup();
  ui.state.failRouting = true;
  const select = ui.all().find(item => item.attributes['aria-label'] === 'Subscription for all chats');
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

test('inline rename saves without a browser prompt and cancel keeps the name', async () => {
  const ui = await setup();
  ui.button('Rename').events.click();
  let input = ui.all().find(item => item.tag === 'input');
  input.value = 'Work Plus'; input.events.input();
  ui.all().find(item => item.tag === 'form').events.submit({preventDefault(){}});
  await settle();
  assert.equal(ui.state.accounts[0].label, 'Work Plus');
  assert(!ui.all().some(item => item.tag === 'form'));
  ui.button('Rename').events.click();
  ui.button('Cancel').events.click();
  assert.equal(ui.state.accounts[0].label, 'Work Plus');
});

test('preparation progress refreshes and failures stay visible in the panel', async () => {
  const ui = await setup();
  ui.state.preparation = {running:true,total:3,ready:1};
  ui.button('Refresh').events.click(); await settle();
  assert(ui.all().some(item => item.textContent === '1 ready'));
  ui.state.preparation = {running:false,total:3,ready:2,failed:1,error:'History unavailable'};
  await ui.state.poll();
  assert(ui.all().some(item => item.textContent?.includes('History unavailable')));
});

test('totals separate windows, exclude disabled accounts, and mask emails', async () => {
  const ui = await setup();
  const account = (id, enabled, used) => ({id, enabled, connected:true, email:'private@example.com', rateLimits:{primary:{windowDurationMins:300,usedPercent:used},secondary:{windowDurationMins:10080,usedPercent:10}}});
  ui.state.accounts = [account('one',true,40), account('two',true,20), account('disabled',false,0)];
  ui.button('Refresh').events.click(); await settle();
  assert(ui.all().some(item => item.textContent === '140%'));
  assert(ui.all().some(item => item.textContent === '180%'));
  assert(ui.all().some(item => item.textContent === '••••••••'));
  assert(!ui.all().some(item => item.textContent?.includes('private@example.com')));
});

test('outside pointer closes panel while inside pointer leaves it open', async () => {
  const ui = await setup();
  const panel = ui.all().find(item => item.className === 'cmx-panel cmx-hidden');
  panel.className = 'cmx-panel';
  ui.state.pointerdown({target:ui.button('Refresh')});
  assert.equal(panel.classList.contains('cmx-hidden'), false);
  ui.state.pointerdown({target:ui.context.document.body});
  assert.equal(panel.classList.contains('cmx-hidden'), true);
  assert.equal(ui.button('Subscriptions').attributes['aria-expanded'], 'false');
});

test('open-chat preparation is indeterminate while migrating and counts background work', async () => {
 const ui = await setup();
 ui.state.preparation = {running:true,total:10,ready:2,deferred:3,failed:1};
 ui.button('Refresh').events.click(); await settle();
 let bar=ui.all().find(item => item.tag === 'progress');
 assert.equal(bar.max,10); assert.equal(bar.value,6);
 ui.state.preparation.loading=1;
 await ui.state.poll();
 bar=ui.all().find(item => item.tag === 'progress');
 assert.equal(bar.value,undefined);
 assert.equal(bar.attributes['aria-label'],'Preparing open chats…');
 assert(ui.all().some(item => item.textContent?.includes('Closed chats switch automatically when opened.')));
});


test('open dropdown survives polling and update completion until blur', async () => {
  const ui = await setup();
  ui.state.preparation = {running:true,total:3,ready:0};
  const select = ui.all().find(item => item.attributes['aria-label'] === 'Plugins and MCP subscription');
  ui.all().find(item => item.className?.includes('cmx-panel')).className = 'cmx-panel';
  select.events.pointerdown();
  await ui.state.poll();
  ui.button('Check for updates').events.click(); await settle();
  assert(ui.all().includes(select), 'refresh replaced an open dropdown');
  select.events.blur();
  await new Promise(resolve => setTimeout(resolve, 5));
  assert(!ui.all().includes(select));
  assert(ui.all().some(item => item.textContent === 'Preparing open chats'));
});

test('dropdown selection commits while background render is pending', async () => {
  const ui = await setup();
  const select = ui.all().find(item => item.attributes['aria-label'] === 'Subscription for all chats');
  select.events.focus();
  ui.button('Check for updates').events.click(); await settle();
  assert(ui.all().includes(select));
  select.value = 'primary';
  select.events.change(); await settle();
  assert.equal(ui.state.accountId, 'primary');
  assert(!ui.all().includes(select));
});

 test('unchanged readiness polling preserves panel nodes and expanded details', async () => {
  const ui = await setup();
  const panel = ui.all().find(item => item.className?.includes('cmx-panel'));
  panel.className = 'cmx-panel';
  const before = [...panel.children];
  await ui.state.poll();
  assert.deepEqual(panel.children, before);
  assert.equal(panel.children[0], before[0]);
 });
 test('background update check detects a new revision without reopening', async () => {
  const ui = await setup();
  ui.state.update = {supported:true,available:true};
  ui.state.updatePoll(); await settle();
  assert(ui.button('Update on next launch'));
 });

test('blur before change does not replace a pending subscription selection', async () => {
 const ui = await setup();
 const select = ui.all().find(item => item.attributes['aria-label'] === 'Subscription for all chats');
 select.events.focus();
 ui.button('Check for updates').events.click(); await settle();
 select.value = 'primary';
 select.events.blur();
 assert(ui.all().includes(select));
 select.events.change(); await settle();
 assert.equal(ui.state.accountId, 'primary');
});

test('pet and auxiliary windows do not mount subscription controls or poll', async () => {
 const ui = await setup(false);
 assert(!ui.button('Subscriptions'));
 assert.equal(ui.state.calls.length, 0);
});

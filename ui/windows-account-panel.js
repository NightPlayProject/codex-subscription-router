(() => {
  "use strict";

  const API = "http://127.0.0.1:__CODEX_MUX_CONTROL_PORT__/v1";
  const TOKEN = "__CODEX_MUX_CONTROL_TOKEN__";
  const STYLE_ID = "codex-mux-windows-style";
  const ROOT_ID = "codex-mux-windows-root";

  async function request(path, options = {}) {
    const response = await fetch(`${API}${path}`, {
      ...options,
      headers: {
        "Content-Type": "application/json",
        "X-Codex-Mux-Token": TOKEN,
        ...(options.headers || {}),
      },
    });
    const body = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(body.error || `Request failed (${response.status})`);
    return body;
  }

  function text(value) {
    return value == null ? "" : String(value);
  }

  function usageWindows(account) {
    const limits = account?.rateLimits;
    const candidates = [limits?.primary, limits?.secondary].filter(Boolean);
    const weekly = candidates
      .filter((item) => Number(item?.windowDurationMins) >= 6 * 24 * 60)
      .sort((left, right) => Number(right?.windowDurationMins || 0) - Number(left?.windowDurationMins || 0))[0];
    const fiveHour = candidates
      .filter((item) => {
        const minutes = Number(item?.windowDurationMins);
        return Number.isFinite(minutes) && minutes >= 270 && minutes <= 330;
      })
      .sort((left, right) => Math.abs(Number(left?.windowDurationMins) - 300) - Math.abs(Number(right?.windowDurationMins) - 300))[0];

    return [
      fiveHour ? { label: "5h", window: fiveHour, resetKind: "time" } : null,
      weekly ? { label: "Weekly", window: weekly, resetKind: "date" } : null,
    ].filter(Boolean);
  }

  function remainingPercent(window) {
    const used = Number(window?.usedPercent);
    return Number.isFinite(used) ? Math.max(0, 100 - used) : null;
  }

  function resetText(window, kind) {
    const raw = Number(window?.resetsAt);
    if (!Number.isFinite(raw) || raw <= 0) return "";
    const value = raw > 1e12 ? raw : raw * 1000;
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return "";
    const formatted = kind === "time"
      ? new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit" }).format(date)
      : new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric" }).format(date);
    return `resets ${formatted}`;
  }

  function ensureStyle() {
    if (document.getElementById(STYLE_ID)) return;
    const style = document.createElement("style");
    style.id = STYLE_ID;
    style.textContent = `
      #${ROOT_ID}{position:fixed;right:18px;bottom:18px;z-index:2147483000;font:13px/1.35 system-ui,-apple-system,Segoe UI,sans-serif;color:#f5f5f5}
      #${ROOT_ID} *{box-sizing:border-box}
      #${ROOT_ID} button,#${ROOT_ID} input,#${ROOT_ID} select{font:inherit}
      .cmx-launch{border:1px solid rgba(255,255,255,.18);background:#202020;color:#fff;border-radius:999px;padding:9px 13px;box-shadow:0 10px 30px rgba(0,0,0,.32);cursor:pointer}
      .cmx-panel{width:min(390px,calc(100vw - 32px));max-height:min(620px,calc(100vh - 80px));overflow:auto;margin-bottom:10px;padding:14px;border:1px solid rgba(255,255,255,.15);border-radius:16px;background:rgba(24,24,24,.97);box-shadow:0 18px 50px rgba(0,0,0,.45);backdrop-filter:blur(12px)}
      .cmx-hidden{display:none}.cmx-head{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:10px}.cmx-title{font-size:15px;font-weight:650}.cmx-muted{color:#aaa;font-size:12px}.cmx-error{margin:8px 0;padding:8px;border-radius:9px;background:#4b2020;color:#ffdada;white-space:pre-wrap}
      .cmx-row{display:grid;grid-template-columns:minmax(0,1fr) auto;gap:10px;align-items:center;padding:10px 0;border-top:1px solid rgba(255,255,255,.08)}.cmx-name{font-weight:600;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}.cmx-sub{color:#aaa;font-size:12px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}.cmx-usage{display:flex;flex-direction:column;gap:1px;margin-top:2px}.cmx-usage-row{display:flex;align-items:baseline;gap:7px;color:#aaa;font-size:12px;min-width:0}.cmx-usage-label{width:44px;flex:0 0 44px;color:#c8c8c8}.cmx-usage-value{color:#e0e0e0;font-variant-numeric:tabular-nums}.cmx-usage-reset{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:#888}.cmx-actions{display:flex;gap:6px;flex-wrap:wrap;justify-content:flex-end}
      .cmx-btn{border:1px solid rgba(255,255,255,.16);background:#303030;color:#fff;border-radius:9px;padding:6px 8px;cursor:pointer}.cmx-btn:hover{background:#3a3a3a}.cmx-primary{background:#fff;color:#111;border-color:#fff}.cmx-primary:hover{background:#e9e9e9}.cmx-btn:disabled{opacity:.55;cursor:default}
      .cmx-login{margin-top:10px;padding:10px;border-radius:10px;background:#2b2b2b}.cmx-code{font:600 20px/1.2 ui-monospace,SFMono-Regular,Consolas,monospace;letter-spacing:.06em;margin:6px 0}.cmx-select{width:100%;margin-top:8px;background:#2b2b2b;color:#fff;border:1px solid rgba(255,255,255,.16);border-radius:9px;padding:7px}
    `;
    style.textContent += `
      #${ROOT_ID}{font-size:15px;line-height:1.45}
      #${ROOT_ID} .cmx-panel{width:min(390px,calc(100vw - 32px));padding:14px;max-height:min(620px,calc(100vh - 80px))}
      #${ROOT_ID} .cmx-title{font-size:16px}
      #${ROOT_ID} .cmx-name{font-size:16px;white-space:normal;overflow-wrap:anywhere}
      #${ROOT_ID} .cmx-muted,#${ROOT_ID} .cmx-sub{font-size:13px}
      #${ROOT_ID} .cmx-sub{letter-spacing:.12em;margin-top:5px}
      #${ROOT_ID} .cmx-row{padding:10px 0}
      #${ROOT_ID} .cmx-usage{gap:3px;margin-top:4px}
      #${ROOT_ID} .cmx-usage-row{display:grid;grid-template-columns:52px auto minmax(0,1fr);font-size:15px;gap:10px}
      #${ROOT_ID} .cmx-usage-reset{text-align:right;font-size:13px;color:#aaa}
      #${ROOT_ID} .cmx-summary{padding:10px 0;border-top:1px solid #ffffff20}
      #${ROOT_ID} .cmx-summary-title{font-size:16px;font-weight:600}
      #${ROOT_ID} .cmx-total{display:flex;justify-content:space-between;gap:12px;margin-top:4px;font-size:15px}
      #${ROOT_ID} .cmx-total strong{font-size:18px;font-variant-numeric:tabular-nums}
      #${ROOT_ID} .cmx-panel{transform-origin:bottom right;transition:opacity .16s ease,transform .16s ease,visibility .16s;opacity:1;transform:translateY(0) scale(1);scrollbar-gutter:stable}
      #${ROOT_ID} .cmx-panel.cmx-hidden{display:block;position:absolute;bottom:42px;right:0;opacity:0;visibility:hidden;pointer-events:none;transform:translateY(8px) scale(.98)}
      #${ROOT_ID} .cmx-row{grid-template-columns:minmax(0,1fr);gap:8px}
      #${ROOT_ID} .cmx-actions{justify-content:flex-start}
      #${ROOT_ID} .cmx-usage-value{white-space:nowrap;flex-shrink:0}
      #${ROOT_ID} .cmx-btn,#${ROOT_ID} .cmx-launch,#${ROOT_ID} .cmx-select{transition:background .14s ease,border-color .14s ease,transform .14s ease}
      #${ROOT_ID} button:active:not(:disabled){transform:scale(.97)}
      #${ROOT_ID} button:focus-visible,#${ROOT_ID} select:focus-visible{outline:2px solid #a7c7ff;outline-offset:3px}
      #${ROOT_ID} .cmx-login button{margin:6px 6px 0 0}
      @media(prefers-reduced-motion:reduce){#${ROOT_ID} .cmx-panel,#${ROOT_ID} button,#${ROOT_ID} .cmx-select{transition:none;transform:none}}
    `;
    document.head.appendChild(style);
  }

  function createButton(label, className = "cmx-btn") {
    const button = document.createElement("button");
    button.type = "button";
    button.className = className;
    button.textContent = label;
    return button;
  }

  async function mount() {
    if (!document.body || document.getElementById(ROOT_ID)) return;
    ensureStyle();

    const root = document.createElement("div");
    root.id = ROOT_ID;
    const panel = document.createElement("div");
    panel.className = "cmx-panel cmx-hidden";
    const launch = createButton("Subscriptions", "cmx-launch");
    root.append(panel, launch);
    document.body.appendChild(root);

    let accounts = [];
    let busy = false;
    let login = null;
    let refreshing = false;
    let error = "";
    let update = null;
    let updateBusy = false;
    let updateCheckedAt = 0;
    async function checkUpdates() {
      if (updateBusy) return;
      updateBusy = true;
      render();
      try {
        update = await request('/updates');
        updateCheckedAt = Date.now();
      } catch (caught) { update = { supported: true, error: caught?.message || String(caught) }; }
      finally { updateBusy = false; render(); }
    }
	let editing = null;
	let preparation = {};
	let preferredNewThreadAccountId = "";

    async function refresh() {
	  const [result, routing] = await Promise.all([
		request("/accounts"),
		request("/routing-preference"),
	  ]);
	  accounts = result.accounts || [];
	  if (login && (!accounts.some(a => a.id === login.accountId) || accounts.some(a => a.id === login.accountId && a.connected))) login = null;
	  preferredNewThreadAccountId = routing.accountId || "";
	  preparation = routing.preparation || {};
      const current = globalThis.__codexMuxPluginAccountId;
      if (!current || !accounts.some((account) => account.id === current && account.enabled && account.connected)) {
        globalThis.__codexMuxPluginAccountId = accounts.find(a => a.id === preferredNewThreadAccountId && a.enabled && a.connected)?.id || accounts.find((account) => account.enabled && account.connected)?.id || null;
      }
      render();
    }

    function setBusy(value) {
      busy = value;
      render();
    }

    async function run(action) {
      if (busy) return;
      error = "";
      setBusy(true);
      try {
        await action();
      } catch (caught) {
        error = caught?.message || String(caught);
      } finally {
        busy = false;
        render();
      }
    }

    function render() {
      const scrollTop = panel.scrollTop;
      panel.replaceChildren();
      const head = document.createElement("div");
      head.className = "cmx-head";
      const titleWrap = document.createElement("div");
      const title = document.createElement("div");
      title.className = "cmx-title";
      title.textContent = "Codex Subscription Router";
      const status = document.createElement("div");
      status.className = "cmx-muted";
      const connected = accounts.filter((account) => account.connected && account.enabled).length;
      status.textContent = `${connected} of ${accounts.length} subscriptions connected`;
      if (busy) status.textContent = "Updating subscriptions…";
      titleWrap.append(title, status);
      const refreshButton = createButton("Refresh");
      refreshButton.disabled = busy;
      refreshButton.addEventListener("click", () => run(refresh));
      head.append(titleWrap, refreshButton);
      panel.appendChild(head);

      const summary = document.createElement("div");
      summary.className = "cmx-summary";
      const summaryTitle = document.createElement("div");
      summaryTitle.className = "cmx-summary-title";
      summaryTitle.textContent = "Total usage remaining";
      summary.appendChild(summaryTitle);
      for (const label of ["5h", "Weekly"]) {
        const eligible = accounts.filter(account => account.connected && account.enabled);
        const values = eligible.map(account => usageWindows(account).find(item => item.label === label))
          .filter(Boolean).map(item => remainingPercent(item.window)).filter(value => value != null);
        if (!values.length) continue;
        const totalRow = document.createElement("div");
        totalRow.className = "cmx-total";
        const caption = document.createElement("span");
        caption.textContent = `${label} · ${values.length} subscription${values.length === 1 ? "" : "s"}`;
        const value = document.createElement("strong");
        value.textContent = `${values.reduce((sum, remaining) => sum + remaining, 0).toFixed(0)}%`;
        totalRow.append(caption, value);
        summary.appendChild(totalRow);
      }
      const explanation = document.createElement("div");
      explanation.className = "cmx-muted";
      explanation.textContent = summary.children.length > 1
        ? "Added across subscriptions; each keeps its own limits."
        : "Usage unavailable";
      summary.appendChild(explanation);
      panel.appendChild(summary);

      if (error) {
        const errorBox = document.createElement("div");
        errorBox.className = "cmx-error";
        errorBox.textContent = error;
        panel.appendChild(errorBox);
      }

      for (const account of accounts) {
        const row = document.createElement("div");
        row.className = "cmx-row";
        const details = document.createElement("div");
        const name = document.createElement("div");
        name.className = "cmx-name";
        name.textContent = `${text(account.label) || account.id}${account.planLabel ? ` · ${account.planLabel}` : ""}`;
        details.appendChild(name);
        if (account.connected) {
          const windows = usageWindows(account);
          if (windows.length > 0) {
            const usage = document.createElement("div");
            usage.className = "cmx-usage";
            for (const item of windows) {
              const row = document.createElement("div");
              row.className = "cmx-usage-row";
              const label = document.createElement("span");
              label.className = "cmx-usage-label";
              label.textContent = item.label;
              const value = document.createElement("span");
              value.className = "cmx-usage-value";
              const remaining = remainingPercent(item.window);
              value.textContent = remaining == null ? "–" : `${remaining.toFixed(0)}% left`;
              const reset = document.createElement("span");
              reset.className = "cmx-usage-reset";
              reset.textContent = resetText(item.window, item.resetKind);
              row.append(label, value);
              if (reset.textContent) row.appendChild(reset);
              usage.appendChild(row);
            }
            details.appendChild(usage);
          } else {
            const unavailable = document.createElement("div");
            unavailable.className = "cmx-sub";
            unavailable.textContent = "Usage unavailable";
            details.appendChild(unavailable);
          }
        } else {
          const signedOut = document.createElement("div");
          signedOut.className = "cmx-sub";
          signedOut.textContent = "Not signed in";
          details.appendChild(signedOut);
        }
        if (!account.enabled || account.email) {
          const meta = document.createElement("div");
          meta.className = "cmx-sub";
          meta.textContent = `${account.enabled ? "" : "disabled"}${!account.enabled && account.email ? " · " : ""}${account.email ? "••••••••" : ""}`;
          if (account.email) meta.setAttribute("aria-label", account.enabled ? "Email hidden" : "Disabled · Email hidden");
          details.appendChild(meta);
        }

        const actions = document.createElement("div");
        actions.className = "cmx-actions";
        const rename = createButton("Rename");
        rename.disabled = busy;
        rename.addEventListener("click", () => {
          editing = { id: account.id, value: account.label || "" };
          render();
          const input = panel.querySelector("input[aria-label='Subscription name']");
          input?.focus(); input?.select();
        });
        actions.appendChild(rename);
		if (editing?.id === account.id) {
		  const form = document.createElement("form");
		  const input = document.createElement("input");
		  input.className = "cmx-select";
		  input.setAttribute("aria-label", "Subscription name");
		  input.value = editing.value;
		  input.required = true; input.maxLength = 80; input.disabled = busy;
		  input.addEventListener("input", () => { editing.value = input.value; });
		  const save = createButton("Save name"); save.type = "submit"; save.disabled = busy;
		  const cancel = createButton("Cancel"); cancel.disabled = busy;
		  cancel.addEventListener("click", () => { editing = null; render(); });
		  form.addEventListener("submit", (event) => {
		    event.preventDefault();
		    const label = input.value.trim();
		    if (!label) { input.setCustomValidity("Enter a name."); input.reportValidity(); return; }
		    run(async () => {
		      await request(`/accounts/${encodeURIComponent(account.id)}`, { method: "PATCH", body: JSON.stringify({ label }) });
		      editing = null;
		      await refresh();
		    });
		  });
		  input.addEventListener("input", () => input.setCustomValidity(""));
		  form.append(input, save, cancel); details.appendChild(form);
		}

        if (!account.controller) {
          const enabled = createButton(account.enabled ? "Disable" : "Enable");
          enabled.disabled = busy;
          enabled.addEventListener("click", () => run(async () => {
            await request(`/accounts/${encodeURIComponent(account.id)}`, { method: "PATCH", body: JSON.stringify({ enabled: !account.enabled }) });
            await refresh();
          }));
          actions.appendChild(enabled);
        }
        if (account.connected && !account.controller) {
          const logout = createButton("Sign out");
          logout.disabled = busy;
          logout.addEventListener("click", () => run(async () => {
            await request(`/accounts/${encodeURIComponent(account.id)}/logout`, { method: "POST", body: "{}" });
            await refresh();
          }));
          actions.appendChild(logout);
        }
        if (!account.connected && !account.controller) {
          const signIn = createButton("Sign in");
          signIn.disabled = busy;
          signIn.addEventListener("click", () => run(async () => {
            const result = await request(`/accounts/${encodeURIComponent(account.id)}/login`, { method: "POST", body: JSON.stringify({ mode: "chatgptDeviceCode" }) });
            login = result.login ? { ...result.login, accountId: account.id } : null;
          }));
          const remove = createButton("Remove");
          remove.disabled = busy || account.threadCount > 0;
          remove.title = account.threadCount > 0 ? "Move this subscription’s chats to another subscription before removing it." : "Remove this signed-out subscription from the list. Saved files are kept.";
          remove.addEventListener("click", () => run(async () => {
            await request(`/accounts/${encodeURIComponent(account.id)}/remove`, { method: "POST", body: "{}" });
            await refresh();
          }));
          actions.append(signIn, remove);
        }
        row.append(details, actions);
        panel.appendChild(row);
      }

	  const routingLabel = document.createElement("div");
	  routingLabel.className = "cmx-muted";
	  routingLabel.style.marginTop = "10px";
	  routingLabel.textContent = "Subscription for all chats";
	  const routingSelect = document.createElement("select");
	  routingSelect.className = "cmx-select";
	  routingSelect.disabled = busy;
	  routingSelect.setAttribute("aria-label", "Subscription for all chats");
	  const automatic = document.createElement("option");
	  automatic.value = "";
	  automatic.textContent = "Automatic";
	  automatic.selected = preferredNewThreadAccountId === "";
	  routingSelect.appendChild(automatic);
	  for (const account of accounts.filter((item) => item.enabled && item.connected)) {
		const option = document.createElement("option");
		option.value = account.id;
		option.textContent = account.label || account.id;
		option.selected = preferredNewThreadAccountId === account.id;
		routingSelect.appendChild(option);
	  }
	  routingSelect.addEventListener("change", () => run(async () => {
		const updated = await request("/routing-preference", {
		  method: "PUT",
		  body: JSON.stringify({ accountId: routingSelect.value || "" }),
		});
		preferredNewThreadAccountId = updated.accountId || "";
		globalThis.__codexMuxPluginAccountId = preferredNewThreadAccountId || accounts.find(a => a.controller && a.connected)?.id || null;
		await refresh();
	  }));
	  panel.append(routingLabel, routingSelect);
	  const routingHelp = document.createElement("div");
	  routingHelp.className = "cmx-muted";
	  routingHelp.style.marginTop = "6px";
	  routingHelp.textContent = "Applies to every new and existing chat. Running replies finish first. ChatGPT Web models use the account signed in to ChatGPT Web.";
	  panel.appendChild(routingHelp);
	  if (preparation.loading || preparation.running) {
	    const loading = document.createElement("div");
	    loading.className = "cmx-muted";
	    loading.setAttribute("role", "status");
	    loading.textContent = preparation.loading ? "Loading chat history…" : "Checking existing chats…";
	    const bar = document.createElement("progress");
	    bar.setAttribute("aria-label", loading.textContent);
	    bar.style.width = "100%";
	    bar.style.accentColor = "#a7c7ff";
	    if (!preparation.loading && preparation.total) {
	      bar.max = preparation.total;
	      bar.value = (preparation.ready || 0) + (preparation.deferred || 0) + (preparation.failed || 0);
	    }
	    panel.append(loading, bar);
	  }
	  if (preparation.total) {
	    const progress = document.createElement("div"); progress.className = "cmx-muted";
	    progress.setAttribute("role", "status");
	    progress.textContent = preparation.running
	      ? `Updating existing chats… ${preparation.ready || 0} of ${preparation.total} ready`
	      : `${preparation.ready || 0} chats ready${preparation.deferred ? ` · ${preparation.deferred} switch when reopened or after their reply` : ""}`;
	    panel.appendChild(progress);
	    if (preparation.failed) {
	      const failure = document.createElement("div"); failure.className = "cmx-error";
	      failure.textContent = `${preparation.failed} chats could not switch. ${preparation.error || "Try selecting the subscription again."}`;
	      panel.appendChild(failure);
	    }
	  }

      const pluginLabel = document.createElement("div");
      pluginLabel.className = "cmx-muted";
      pluginLabel.style.marginTop = "10px";
      pluginLabel.textContent = "Subscription for plugins and connectors";
      const pluginSelect = document.createElement("select");
      pluginSelect.className = "cmx-select";
      pluginSelect.disabled = busy;
      pluginSelect.setAttribute("aria-label", "Plugins and MCP subscription");
      for (const account of accounts.filter((item) => item.enabled && item.connected)) {
        const option = document.createElement("option");
        option.value = account.id;
        option.textContent = account.label || account.id;
        option.selected = globalThis.__codexMuxPluginAccountId === account.id;
        pluginSelect.appendChild(option);
      }
      pluginSelect.addEventListener("change", () => {
        globalThis.__codexMuxPluginAccountId = pluginSelect.value || null;
      });
      panel.append(pluginLabel, pluginSelect);

      const add = createButton("Add subscription", "cmx-btn cmx-primary");
      add.style.marginTop = "10px";
      add.disabled = busy;
      add.addEventListener("click", () => run(async () => {
        const created = await request("/accounts", { method: "POST", body: JSON.stringify({ label: `Subscription ${accounts.length + 1}` }) });
        const result = await request(`/accounts/${encodeURIComponent(created.account.id)}/login`, { method: "POST", body: JSON.stringify({ mode: "chatgptDeviceCode" }) });
        login = result.login ? { ...result.login, accountId: created.account.id } : null;
        await refresh();
      }));
      panel.appendChild(add);

      const updates = document.createElement('div');
      updates.className = 'cmx-summary';
      updates.style.marginTop = '12px';
      const updateLabel = document.createElement('div');
      updateLabel.className = 'cmx-muted';
      updateLabel.setAttribute('role', 'status');
      updateLabel.textContent = updateBusy ? 'Checking for updates…'
        : update?.queued ? 'Update queued. Close Codex when ready, then open the Start menu shortcut. Updating may take a few minutes.'
        : update?.error ? `Could not check for updates: ${update.error}`
        : update?.available ? 'An update is available for the combined app.'
        : update?.supported ? 'Router + wallpapers are up to date.'
        : 'Install the combined app to enable updates.';
      const check = createButton('Check for updates');
      check.disabled = updateBusy;
      check.addEventListener('click', checkUpdates);
      updates.append(updateLabel, check);
      if (update?.available && !update.queued) {
        const queue = createButton('Update on next launch');
        queue.disabled = updateBusy;
        queue.addEventListener('click', async () => {
          if (updateBusy) return;
          updateBusy = true; render();
          try { update = await request('/updates', { method: 'POST', body: '{}' }); }
          catch (caught) { update = { ...update, error: caught?.message || String(caught) }; }
          finally { updateBusy = false; render(); }
        });
        updates.appendChild(queue);
      }
      panel.appendChild(updates);

      if (login) {
        const loginBox = document.createElement("div");
        loginBox.className = "cmx-login";
        const prompt = document.createElement("div");
        prompt.textContent = "Finish signing in with this code. This card closes automatically when connected.";
        const code = document.createElement("div");
        code.className = "cmx-code";
        code.textContent = login.userCode || "(code unavailable)";
        const open = createButton("Copy code & open sign-in");
        open.addEventListener("click", async () => {
          const userCode = login.userCode || "";
          const rawUrl = login.verificationUrl || login.authUrl || "";
          if (userCode) await navigator.clipboard.writeText(userCode).catch(() => {});
          try {
            const destination = new URL(rawUrl);
            const trusted = destination.protocol === "https:" && (destination.hostname === "chatgpt.com" || destination.hostname === "auth.openai.com");
            if (!trusted) throw new Error("untrusted verification URL");
            window.open(destination.href, "_blank", "noopener,noreferrer");
          } catch {
            error = "The sign-in verification page could not be opened safely.";
            render();
          }
        });
        const dismiss = createButton("Hide code");
        dismiss.addEventListener("click", () => { login = null; render(); });
        loginBox.append(prompt, code, open, dismiss);
        panel.appendChild(loginBox);
      }
      panel.scrollTop = scrollTop;
    }

    launch.addEventListener("click", () => {
      panel.classList.toggle("cmx-hidden");
      launch.setAttribute("aria-expanded", String(!panel.classList.contains("cmx-hidden")));
      if (!panel.classList.contains("cmx-hidden")) run(refresh);
      if (Date.now() - updateCheckedAt > 60 * 60 * 1000) checkUpdates();
    });
    launch.setAttribute("aria-expanded", "false");
    document.addEventListener("pointerdown", (event) => {
      if (!root.contains(event.target)) {
        panel.classList.add("cmx-hidden");
        launch.setAttribute("aria-expanded", "false");
      }
    }, true);
    root.addEventListener("keydown", (event) => {
      if (event.key === "Escape") { panel.classList.add("cmx-hidden"); launch.setAttribute("aria-expanded", "false"); launch.focus(); }
    });
    setInterval(async () => {
      if ((!login && !preparation.running && !preparation.loading && panel.classList.contains("cmx-hidden")) || busy || refreshing || editing) return;
      refreshing = true;
      try {
        if (login) await refresh();
        else {
          const routing = await request("/routing-preference");
          preparation = routing.preparation || {};
          preferredNewThreadAccountId = routing.accountId || "";
          render();
        }
      } catch { /* Preserve the current view during transient network failures. */ }
      finally { refreshing = false; }
    }, 1000);

    try {
      await refresh();
    } catch (caught) {
      error = caught?.message || String(caught);
      render();
    }
    checkUpdates();
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", () => mount().catch(() => {}), { once: true });
  } else {
    mount().catch(() => {});
  }
})();

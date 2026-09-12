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

  function weeklyUsage(account) {
    const limits = account?.rateLimits;
    const candidates = [limits?.primary, limits?.secondary].filter(Boolean);
    const weekly = candidates.find((item) => Number(item?.windowDurationMins) >= 6 * 24 * 60);
    const used = Number(weekly?.usedPercent);
    return Number.isFinite(used) ? `${Math.max(0, 100 - used).toFixed(0)}% weekly left` : "Usage unavailable";
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
      .cmx-row{display:grid;grid-template-columns:minmax(0,1fr) auto;gap:10px;align-items:center;padding:10px 0;border-top:1px solid rgba(255,255,255,.08)}.cmx-name{font-weight:600;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}.cmx-sub{color:#aaa;font-size:12px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}.cmx-actions{display:flex;gap:6px;flex-wrap:wrap;justify-content:flex-end}
      .cmx-btn{border:1px solid rgba(255,255,255,.16);background:#303030;color:#fff;border-radius:9px;padding:6px 8px;cursor:pointer}.cmx-btn:hover{background:#3a3a3a}.cmx-primary{background:#fff;color:#111;border-color:#fff}.cmx-primary:hover{background:#e9e9e9}.cmx-btn:disabled{opacity:.55;cursor:default}
      .cmx-login{margin-top:10px;padding:10px;border-radius:10px;background:#2b2b2b}.cmx-code{font:600 20px/1.2 ui-monospace,SFMono-Regular,Consolas,monospace;letter-spacing:.06em;margin:6px 0}.cmx-select{width:100%;margin-top:8px;background:#2b2b2b;color:#fff;border:1px solid rgba(255,255,255,.16);border-radius:9px;padding:7px}
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
    let error = "";

    async function refresh() {
      const result = await request("/accounts");
      accounts = result.accounts || [];
      const current = globalThis.__codexMuxPluginAccountId;
      if (!current || !accounts.some((account) => account.id === current && account.enabled)) {
        globalThis.__codexMuxPluginAccountId = accounts.find((account) => account.enabled)?.id || null;
      }
      render();
    }

    function setBusy(value) {
      busy = value;
      render();
    }

    async function run(action) {
      if (busy) return;
      setBusy(true);
      error = "";
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
      status.textContent = `${connected} connected · ${accounts.length} total`;
      titleWrap.append(title, status);
      const refreshButton = createButton("Refresh");
      refreshButton.disabled = busy;
      refreshButton.addEventListener("click", () => run(refresh));
      head.append(titleWrap, refreshButton);
      panel.appendChild(head);

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
        const sub = document.createElement("div");
        sub.className = "cmx-sub";
        const connection = account.connected ? weeklyUsage(account) : "Not signed in";
        sub.textContent = `${connection}${account.enabled ? "" : " · disabled"}${account.email ? ` · ${account.email}` : ""}`;
        details.append(name, sub);

        const actions = document.createElement("div");
        actions.className = "cmx-actions";
        const rename = createButton("Rename");
        rename.disabled = busy;
        rename.addEventListener("click", () => {
          const next = window.prompt("Subscription name", account.label || "");
          if (next == null || next.trim() === "") return;
          run(async () => {
            await request(`/accounts/${encodeURIComponent(account.id)}`, { method: "PATCH", body: JSON.stringify({ label: next.trim() }) });
            await refresh();
          });
        });
        actions.appendChild(rename);

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
        row.append(details, actions);
        panel.appendChild(row);
      }

      const pluginLabel = document.createElement("div");
      pluginLabel.className = "cmx-muted";
      pluginLabel.style.marginTop = "10px";
      pluginLabel.textContent = "Plugins / MCP subscription";
      const pluginSelect = document.createElement("select");
      pluginSelect.className = "cmx-select";
      for (const account of accounts.filter((item) => item.enabled)) {
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

      if (login) {
        const loginBox = document.createElement("div");
        loginBox.className = "cmx-login";
        const prompt = document.createElement("div");
        prompt.textContent = "Complete sign-in with this device code:";
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
        loginBox.append(prompt, code, open);
        panel.appendChild(loginBox);
      }
    }

    launch.addEventListener("click", () => {
      panel.classList.toggle("cmx-hidden");
      if (!panel.classList.contains("cmx-hidden")) run(refresh);
    });

    try {
      await refresh();
    } catch (caught) {
      error = caught?.message || String(caught);
      render();
    }
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", () => mount().catch(() => {}), { once: true });
  } else {
    mount().catch(() => {});
  }
})();

"use strict";
const byID = id => document.getElementById(id), form = byID("setup-form"), fields = form.elements;
let step = 0, csrf = "", checked = false, completed = null, generatedSync = "", busy = false;
const errorBox = byID("setup-error");
function notice(message) { byID("setup-toast").textContent = message; byID("setup-toast").hidden = false; }
async function run(fn) { if (busy)
    return; busy = true; errorBox.hidden = true; document.querySelectorAll("button").forEach(b => b.disabled = true); try {
    await fn();
}
catch (e) {
    errorBox.textContent = e.message || "操作未完成";
    errorBox.hidden = false;
}
finally {
    busy = false;
    document.querySelectorAll("button").forEach(b => b.disabled = false);
    byID("previous").disabled = step === 1;
} }
async function api(path, body) { const options = { credentials: "same-origin", cache: "no-store", headers: {} }; if (body !== undefined) {
    options.method = "POST";
    options.headers["Content-Type"] = "application/json";
    options.headers["X-CSRF-Token"] = csrf;
    options.body = JSON.stringify(body);
} const r = await fetch(path, options), v = await r.json(); if (!r.ok)
    throw new Error(v.error || "操作未完成"); return v; }
function show(n) { step = n; document.querySelectorAll("[data-step]").forEach(e => e.hidden = Number(e.dataset.step) !== n); form.hidden = n === 0 || n === 5; byID("step-caption").textContent = `第 ${n + 1} 步，共 6 步`; document.querySelectorAll("#setup-progress li").forEach((e, i) => { e.classList.toggle("active", i === n); e.classList.toggle("finished", i < n); }); byID("previous").disabled = n === 1; byID("next").textContent = n === 4 ? "确认并完成安装" : "下一步"; window.scrollTo(0, 0); }
function item(parent, label, value) { const row = document.createElement("div"), title = document.createElement("span"), text = document.createElement("strong"); row.className = "setup-check-row"; title.textContent = label; text.textContent = value; row.append(title, text); parent.append(row); }
async function check() { checked = false; const v = await api("/setup/api/check", {}); byID("environment").replaceChildren(); for (const [label, value] of [["运行版本", v.version], ["内部监听", v.listen], ["数据目录", v.dataDirectory], ["目录权限与 SQLite", "检查通过"]])
    item(byID("environment"), label, value); fields.issuer.value = v.issuer; fields.clientId.value = v.clientId; if (!fields.clientSecret.value)
    fields.clientSecret.value = v.clientSecret; checked = true; }
async function generate(key) { const v = await api("/setup/api/generate", { kind: key === "clientSecret" ? "clientSecret" : "password" }); fields[key].value = v.value; if (key === "syncPassword")
    generatedSync = v.value; }
function input() { const v = Object.fromEntries(new FormData(form)); v.writes = fields.writes.checked; return v; }
function validPage() { for (const el of form.querySelector(`[data-step="${step}"]`).querySelectorAll("input,select"))
    if (!el.reportValidity())
        return false; return true; }
async function copy(value) { try {
    await navigator.clipboard.writeText(value);
    notice("已复制，请保存到密码管理器");
}
catch {
    throw new Error("浏览器未允许复制，请手动复制或下载凭据清单");
} }
byID("unlock-form").addEventListener("submit", e => { e.preventDefault(); run(async () => { const v = await api("/setup/api/unlock", { code: byID("install-code").value }); csrf = v.csrf; byID("install-code").value = ""; show(1); await check(); for (const key of ["adminPassword", "webdavPassword", "oauthPassword", "syncPassword"])
    await generate(key); }); });
byID("recheck").addEventListener("click", () => run(check));
document.querySelectorAll("[data-generate]").forEach(b => b.addEventListener("click", () => run(async () => { if (b.dataset.generate === "syncPassword" && fields.dataMode.value === "existing")
    throw new Error("已有客户端数据请填写原同步加密密码，不能用新随机密码替代"); await generate(b.dataset.generate); })));
document.querySelectorAll("[data-show]").forEach(b => b.addEventListener("click", () => { const el = fields[b.dataset.show], visible = el.type === "password"; el.type = visible ? "text" : "password"; b.textContent = visible ? "隐藏" : "显示"; b.setAttribute("aria-label", b.textContent + "密码"); }));
document.querySelectorAll("[data-copy]").forEach(b => b.addEventListener("click", () => run(() => copy(fields[b.dataset.copy].value))));
byID("data-mode").addEventListener("change", () => { const existing = fields.dataMode.value === "existing"; byID("data-hint").textContent = existing ? "填写原客户端正在使用的同步加密密码。上传前无法确认密码是否正确。" : "全新使用可生成同步加密密码，并在所有客户端填写同一个密码。"; if (existing && fields.syncPassword.value === generatedSync)
    fields.syncPassword.value = ""; });
byID("previous").addEventListener("click", () => show(Math.max(1, step - 1)));
byID("next").addEventListener("click", () => { if (!validPage())
    return; run(async () => { if (!checked)
    throw new Error("请先通过环境检查"); if (step === 3) {
    const v = input();
    await api("/setup/api/validate", v);
    byID("setup-summary").replaceChildren();
    for (const [label, value] of [["公网基地址", v.issuer], ["管理员", v.adminUsername], ["业务用户", v.username], ["显示名称", v.displayName || v.username], ["数据来源", v.dataMode === "new" ? "全新使用" : "已有客户端数据"], ["MCP 写入", v.writes ? "启用已实现的写入工具" : "只读"], ["密码与密钥", "已设置，默认隐藏"]])
        item(byID("setup-summary"), label, value);
} if (step === 4) {
    const v = input(), result = await api("/setup/api/commit", v);
    completed = { ...result.connection, adminPassword: v.adminPassword };
    renderCompleted();
    show(5);
}
else
    show(step + 1); }); });
form.addEventListener("submit", e => { e.preventDefault(); byID("next").click(); });
const configFields = [["adminURL", "管理地址"], ["adminUsername", "管理员用户名"], ["adminPassword", "管理员密码", true], ["webdavURL", "WebDAV 基地址"], ["webdavFolder", "同步文件夹"], ["webdavUsername", "业务用户名"], ["webdavPassword", "WebDAV 密码", true], ["syncPassword", "同步加密密码", true], ["mcpURL", "MCP 地址"], ["clientId", "OAuth Client ID"], ["clientSecret", "OAuth Client Secret", true], ["oauthUsername", "OAuth 用户名"], ["oauthPassword", "OAuth 登录密码", true], ["scope", "授权范围"], ["tokenAuthMethod", "Token 认证方式"], ["redirectURI", "回调地址"]];
function renderCompleted() { byID("completed-config").replaceChildren(); for (const [key, label, sensitive] of configFields) {
    const row = document.createElement("div"), title = document.createElement("span"), value = document.createElement("span"), controls = document.createElement("div"), button = document.createElement("button");
    row.className = "config-row";
    title.className = "config-label";
    title.textContent = label;
    value.className = "config-value";
    value.textContent = sensitive ? "••••••••" : completed[key];
    if (sensitive) {
        const reveal = document.createElement("button");
        reveal.type = "button";
        reveal.className = "copy-button";
        reveal.textContent = "查看";
        let visible = false;
        reveal.addEventListener("click", () => { visible = !visible; value.textContent = visible ? completed[key] : "••••••••"; reveal.textContent = visible ? "隐藏" : "查看"; });
        controls.append(reveal);
    }
    button.type = "button";
    button.className = "copy-button";
    button.textContent = "复制";
    button.addEventListener("click", () => run(() => copy(completed[key])));
    controls.append(button);
    row.append(title, value, controls);
    byID("completed-config").append(row);
} }
function selected(keys) { return JSON.stringify(Object.fromEntries(keys.map(k => [k, completed[k]])), null, 2); }
byID("copy-client").addEventListener("click", () => run(() => copy(selected(["webdavURL", "webdavFolder", "webdavUsername", "webdavPassword", "syncPassword"]))));
byID("copy-gpt").addEventListener("click", () => run(() => copy(selected(["mcpURL", "clientId", "clientSecret", "oauthUsername", "oauthPassword", "scope", "tokenAuthMethod", "redirectURI"]))));
byID("download-credentials").addEventListener("click", () => { const url = URL.createObjectURL(new Blob([JSON.stringify(completed, null, 2)], { type: "application/json" })), a = document.createElement("a"); a.href = url; a.download = "spmcp-credentials.json"; a.click(); setTimeout(() => URL.revokeObjectURL(url), 1000); notice("凭据文件包含密码，请保存在私密位置"); });
byID("check-connection").addEventListener("click", () => run(async () => { const health = await fetch("/healthz", { cache: "no-store" }), discovery = await fetch("/.well-known/oauth-authorization-server", { cache: "no-store" }), mcp = await fetch("/mcp", { method: "POST", headers: { "Content-Type": "application/json", "Accept": "application/json, text/event-stream" }, body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/list", params: {} }) }); const metadata = await discovery.json(); if (!health.ok || !discovery.ok || metadata.issuer !== completed.issuer || mcp.status !== 401 || !mcp.headers.get("WWW-Authenticate"))
    throw new Error("接口检查未通过，请核对宝塔反代和 OAuth 发现路由"); const dav = await fetch("/super-productivity/", { method: "PROPFIND", headers: { Authorization: "Basic " + btoa(String.fromCharCode(...new TextEncoder().encode(completed.webdavUsername + ":" + completed.webdavPassword))), Depth: "0" } }); if (![207, 404].includes(dav.status))
    throw new Error("WebDAV 认证或路由检查未通过"); byID("connection-result").textContent = "服务与 OAuth 发现正常，MCP 未登录认证检查通过，WebDAV 凭据通过。等待客户端上传；ChatGPT 的实际授权与工具调用仍需在连接后验证。"; }));
show(0);
run(async () => { const r = await fetch("/setup/api/session", { cache: "no-store", credentials: "same-origin" }); if (!r.ok)
    return; const v = await r.json(); csrf = v.csrf; show(1); await check(); for (const key of ["adminPassword", "webdavPassword", "oauthPassword", "syncPassword"])
    await generate(key); });

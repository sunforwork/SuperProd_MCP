"use strict";
let csrf = "", users = [], connection = null, selected = null, toastTimer;
const $ = id => document.getElementById(id);
const dialog = $("user-dialog"), body = $("dialog-body");
function node(tag, text, cls) { const e = document.createElement(tag); if (text !== undefined)
    e.textContent = text; if (cls)
    e.className = cls; return e; }
function button(text, action, cls = "secondary") { const b = node("button", text, cls); b.type = "button"; b.addEventListener("click", () => run(action)); return b; }
function toast(text) { $("toast").textContent = text; $("toast").hidden = false; clearTimeout(toastTimer); toastTimer = setTimeout(() => $("toast").hidden = true, 4000); }
async function run(action) { try {
    await action();
}
catch (e) {
    toast(e.message || "操作未完成");
} }
async function api(path, data) {
    const options = { credentials: "same-origin", cache: "no-store", headers: {} };
    if (data !== undefined) {
        options.method = "POST";
        options.headers["Content-Type"] = "application/json";
        options.headers["X-CSRF-Token"] = csrf;
        options.body = JSON.stringify(data);
    }
    const r = await fetch(path, options);
    if (r.status === 401) {
        location.assign("/admin/login");
        throw new Error("请重新登录");
    }
    const v = await r.json();
    if (!r.ok)
        throw new Error(v.error || "操作未完成");
    return v;
}
function bytes(value) { if (value < 1024)
    return value + " B"; const n = Math.floor(Math.log(value) / Math.log(1024)); return (value / 1024 ** n).toFixed(1) + " " + ["B", "KB", "MB", "GB", "TB"][Math.min(n, 4)]; }
async function refresh() {
    const v = await api("/admin/api/users");
    users = v.users;
    $("users").replaceChildren();
    $("user-count").textContent = users.length;
    $("total-current").textContent = bytes(v.totals.currentBytes);
    $("total-history").textContent = bytes(v.totals.historyBytes);
    $("total-physical").textContent = bytes(v.totals.physicalBytes);
    $("management").textContent = "管理资料占用：" + bytes(v.managementBytes);
    $("updated").textContent = "更新于 " + new Date().toLocaleTimeString();
    for (const user of users) {
        const row = node("article", undefined, "user-row"), identity = node("div", undefined, "user-identity"), name = node("div", user.name, "user-name");
        name.append(node("span", user.username + " · " + (user.dataStatus === "empty" ? "等待首次同步" : "已上传同步文件"), "user-subtitle"));
        identity.append(node("span", Array.from(user.name)[0] || "U", "avatar"), name);
        const size = node("div", bytes(user.physicalBytes), "user-size");
        size.append(node("small", "文件 " + bytes(user.currentBytes) + " · 历史 " + bytes(user.historyBytes)));
        row.append(identity, node("span", user.enabled ? "已启用" : "已停用", "pill" + (user.enabled ? "" : " disabled")), size, button("管理", () => details(user)));
        $("users").append(row);
    }
}
function field(form, key, label, type = "text", value = "", required = false) {
    const l = node("label", label), input = document.createElement("input");
    input.name = key;
    input.type = type;
    input.value = value;
    input.required = required;
    input.setAttribute("aria-label", label);
    input.autocomplete = type === "password" ? "new-password" : key === "username" ? "username" : "off";
    input.maxLength = type === "password" ? 256 : 128;
    if (type === "password") {
        const wrap = node("span", undefined, "password-field"), reveal = button("显示", () => { const show = input.type === "password"; input.type = show ? "text" : "password"; reveal.textContent = show ? "隐藏" : "显示"; reveal.setAttribute("aria-label", show ? "隐藏密码" : "显示密码"); }, "reveal");
        reveal.setAttribute("aria-label", "显示密码");
        wrap.append(input, reveal);
        l.append(wrap);
    }
    else
        l.append(input);
    form.append(l);
    return input;
}
function section(title) { const s = node("section", undefined, "detail-section"); s.append(node("h3", title)); body.append(s); return s; }
function startDialog(title, subtitle) { connection = null; body.replaceChildren(); $("dialog-title").textContent = title; $("dialog-eyebrow").textContent = subtitle; if (!dialog.open)
    dialog.showModal(); }
function accountForm(user) {
    const form = document.createElement("form"), grid = node("div", undefined, "form-grid");
    field(grid, "name", "显示名称", "text", user?.name || "");
    field(grid, "username", "登录用户名", "text", user?.username || "", true);
    field(grid, "webdavPassword", user ? "新 WebDAV 密码（留空不变）" : "WebDAV 密码（留空生成）", "password");
    field(grid, "oauthPassword", user ? "新授权密码（留空不变）" : "OAuth 授权密码（留空生成）", "password");
    field(grid, "syncPassword", user ? "校正现有加密密码（留空不变）" : "同步加密密码（留空生成）", "password");
    if (user) {
        field(grid, "clientSecret", "OAuth Client Secret（留空不变）", "password");
        const l = node("label", "凭据保存方式"), select = document.createElement("select");
        select.name = "credentialMode";
        for (const [value, text] of [["reset", "设置新认证密码"], ["record", "验证并补录当前凭据"]]) {
            const o = node("option", text);
            o.value = value;
            select.append(o);
        }
        l.append(select);
        grid.append(l);
    }
    form.append(grid, node("p", user ? "加密密码只可校正为能解密现有文件的密码，不会重加密数据。补录凭据不会重置授权。" : "所有凭据均独立生成。新账号等待客户端首次上传数据。", "inline-note"));
    const submit = node("button", user ? "保存账号设置" : "创建用户", "primary wide");
    submit.type = "submit";
    form.append(submit);
    form.addEventListener("submit", e => {
        e.preventDefault();
        run(async () => {
            submit.disabled = true;
            try {
                const data = Object.fromEntries(new FormData(form));
                let id = user?.id;
                const out = await api(id ? "/admin/api/users/" + id : "/admin/api/users", data);
                id ||= out.id;
                await refresh();
                details(users.find(u => u.id === id));
                toast(user ? "设置已保存" : "用户已创建，可查看与复制连接配置");
            }
            finally {
                submit.disabled = false;
            }
        });
    });
    return form;
}
async function secrets(id) { if (!connection)
    connection = await api("/admin/api/users/" + id + "/secrets", {}); return connection; }
async function copy(text) {
    if (navigator.clipboard?.writeText)
        await navigator.clipboard.writeText(text);
    else {
        const input = node("textarea");
        input.value = text;
        body.append(input);
        input.select();
        const ok = document.execCommand("copy");
        input.remove();
        if (!ok)
            throw new Error("浏览器无法复制，请手动选择内容");
    }
    toast("已复制");
}
const configFields = [["mcpURL", "MCP 地址"], ["issuer", "OAuth issuer"], ["authorizationURL", "授权地址"], ["tokenURL", "Token 地址"], ["redirectURI", "回调地址"], ["scope", "Scope"], ["tokenAuthMethod", "Token 认证"], ["clientId", "Client ID"], ["clientSecret", "Client Secret", true], ["oauthUsername", "授权用户名"], ["oauthPassword", "授权密码", true], ["webdavURL", "WebDAV 基址"], ["webdavFolder", "同步文件夹"], ["webdavUsername", "WebDAV 用户名"], ["webdavPassword", "WebDAV 密码", true], ["syncPassword", "同步加密密码", true]];
function details(user) {
    if (!user)
        return;
    selected = user.id;
    startDialog(user.name, "ACCOUNT DETAILS");
    body.append(accountForm(user));
    const config = section("连接配置与凭据"), actions = node("div", undefined, "copy-actions");
    for (const [text, kind] of [["复制客户端配置", "client"], ["复制 ChatGPT 配置", "gpt"], ["复制全部配置（含密码）", "all"]])
        actions.append(button(text, async () => {
            const v = await secrets(user.id);
            const keys = kind === "client" ? ["webdavURL", "webdavFolder", "webdavUsername", "webdavPassword", "syncPassword"] : kind === "gpt" ? ["mcpURL", "clientId", "clientSecret", "oauthUsername", "oauthPassword", "scope", "tokenAuthMethod", "redirectURI"] : configFields.map(f => f[0]);
            const out = {};
            for (const key of keys)
                out[key] = v[key] || "原文未保存";
            await copy(JSON.stringify(out, null, 2));
        }, "copy-button"));
    config.append(actions);
    for (const [key, label, sensitive] of configFields) {
        const row = node("div", undefined, "config-row"), value = node("span", sensitive ? "••••••••" : "点击查看", "config-value"), controls = node("div");
        let visible = false;
        const view = button("查看", async () => { if (sensitive && visible) {
            value.textContent = "••••••••";
            view.textContent = "查看";
            visible = false;
            return;
        } const v = await secrets(user.id); value.textContent = v[key] || "原文未保存"; if (sensitive) {
            visible = true;
            view.textContent = "隐藏";
        } }, "copy-button");
        controls.append(view, button("复制", async () => { const v = await secrets(user.id); if (!v[key])
            throw new Error("原文未保存，请补录或重置该项凭据"); await copy(v[key]); }, "copy-button"));
        row.append(node("span", label, "config-label"), value, controls);
        config.append(row);
    }
    config.append(node("p", "旧凭据只有哈希时无法显示原文。可在上方选择“验证并补录当前凭据”，或设置新的认证凭据。", "footnote"));
    const status = section("同步读取检查");
    status.append(button("检查上传与同步密码", async () => { const v = await api("/admin/api/users/" + user.id + "/status", {}); toast(v.message); }));
    const usage = section("存储用量");
    usage.append(node("p", "当前文件 " + bytes(user.currentBytes) + "（" + user.currentFiles + " 个） · 历史 " + bytes(user.historyBytes) + "（" + user.historyVersions + " 个版本）", "muted"), node("p", "SQLite " + bytes(user.databaseBytes) + " · WAL " + bytes(user.walBytes) + " · SHM " + bytes(user.shmBytes), "muted"));
    const lifecycle = section("账号状态");
    lifecycle.append(button(user.enabled ? "停用账号并撤销授权" : "恢复账号", async () => { await api("/admin/api/users/" + user.id, { enabled: !user.enabled }); await refresh(); details(users.find(u => u.id === user.id)); toast(user.enabled ? "账号已停用，数据保留" : "账号已恢复，请重新授权"); }, "secondary danger-button"), button("生成新的 OAuth Client Secret", async () => { await api("/admin/api/users/" + user.id, { rotateClientSecret: true }); await refresh(); details(users.find(u => u.id === user.id)); toast("Client Secret 已更新，请在 ChatGPT 更新并重新授权"); }));
}
$("create").addEventListener("click", () => { selected = null; startDialog("新建独立用户", "NEW ACCOUNT"); body.append(accountForm(null)); });
$("close-dialog").addEventListener("click", () => dialog.close());
dialog.addEventListener("close", () => { selected = null; connection = null; body.replaceChildren(); });
$("refresh").addEventListener("click", () => run(refresh));
$("logout").addEventListener("click", () => run(async () => { await api("/admin/api/logout", {}); location.assign("/admin/login"); }));
$("admin-password").addEventListener("click", () => $("password-dialog").showModal());
$("close-password").addEventListener("click", () => $("password-dialog").close());
$("password-form").addEventListener("submit", e => { e.preventDefault(); run(async () => { const data = Object.fromEntries(new FormData(e.target)); delete data.username; await api("/admin/api/password", data); location.assign("/admin/login"); }); });
$("password-dialog").addEventListener("close", () => $("password-form").reset());
let lastActivity = 0;
for (const event of ["pointerdown", "keydown", "input"])
    document.addEventListener(event, e => {
        if (!e.isTrusted || !csrf || Date.now() - lastActivity < 60000)
            return;
        lastActivity = Date.now();
        run(() => api("/admin/api/activity", {}));
    }, { passive: true });
run(async () => { const session = await api("/admin/api/session"); csrf = session.csrf; await refresh(); setInterval(() => { if (!document.hidden)
    run(refresh); }, 30000); });
$("admin-username").addEventListener("click", () => $("username-dialog").showModal());
$("close-username").addEventListener("click", () => $("username-dialog").close());
$("username-dialog").addEventListener("close", () => $("username-form").reset());
$("username-form").addEventListener("submit", e => { e.preventDefault(); run(async () => { await api("/admin/api/username", Object.fromEntries(new FormData(e.target))); location.assign("/admin/login"); }); });

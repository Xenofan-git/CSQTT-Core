(function () {
  'use strict';
  const base = '/api';
  const cache = {
    configs: '[]', settings: '{}', logs: '[]', status: '{"connected":false}', selected: '{}',
    coreUpdate: '{"update":false,"version":""}', laluneUpdate: '{"update":false,"version":""}',
    vk: '{"hasToken":false,"fetcherOk":false,"fetching":false,"message":"","progress":0}', vkAuto: '{"pending":true}',
    deployLog: '', deploying: false, coreDownloading: false
  };
  const req = (path, options) => fetch(base + path, options || {}).then(r => r.text());
  const json = (path, options) => req(path, options).then(x => { try { return JSON.parse(x); } catch (_) { return x; } });
  async function refresh() {
    try { cache.configs = await req('/configs'); } catch (_) {}
    try { cache.settings = await req('/settings'); } catch (_) {}
    try { cache.logs = await req('/logs'); } catch (_) {}
    try { cache.status = await req('/vpn/status'); } catch (_) {}
    try { cache.selected = await req('/configs/selected'); } catch (_) {}
    try { cache.deployLog = await json('/deploy/log'); } catch (_) {}
    try { const x = await json('/deploy/status'); cache.deploying = !!x.deploying; } catch (_) {}
    try { const x = await json('/updates/core/check'); cache.coreUpdate = JSON.stringify(x); } catch (_) {}
    try { const x = await json('/updates/lalune'); cache.laluneUpdate = JSON.stringify({update:!!x.hasUpdate, version:x.remoteTag||'', error:x.error||''}); } catch (_) {}
    try { const x = await json('/updates/core/status'); cache.coreDownloading = !!x.downloading; } catch (_) {}
    try { const x = await json('/vk/state'); cache.vk = JSON.stringify(x); } catch (_) {}
  }
  let captchaOverlay = null;
  let captchaLastUpdated = 0;
  function removeCaptchaOverlay() {
    if (captchaOverlay) { captchaOverlay.remove(); captchaOverlay = null; }
  }
  function showCaptchaOverlay(state) {
    if (!state || !state.pending || !state.redirectUri) { removeCaptchaOverlay(); return; }
    if (captchaOverlay && captchaLastUpdated === state.updated) return;
    captchaLastUpdated = state.updated || 0;
    removeCaptchaOverlay();
    const box = document.createElement('div');
    box.style.cssText = 'position:fixed;z-index:2147483647;right:18px;bottom:18px;max-width:420px;background:#151515;color:#fff;padding:18px;border-radius:14px;box-shadow:0 8px 32px rgba(0,0,0,.45);font:14px/1.45 system-ui,sans-serif';
    const title = document.createElement('div'); title.textContent = '🔐 Требуется CAPTCHA VK'; title.style.cssText='font-size:17px;font-weight:700;margin-bottom:8px';
    const text = document.createElement('div'); text.textContent = 'CSQTT ожидает подтверждение. Открой CAPTCHA в браузере и пройди проверку.'; text.style.marginBottom='12px';
    const open = document.createElement('button'); open.textContent='Открыть CAPTCHA'; open.style.cssText='padding:9px 13px;border:0;border-radius:9px;cursor:pointer;font-weight:600;margin-right:8px';
    open.onclick=()=>{ try { const u = new URL(state.redirectUri); const extra = 'lalune_panel='+encodeURIComponent(location.origin)+'&lalune_session='+encodeURIComponent(state.sessionToken||''); u.hash = (u.hash ? u.hash + '&' : '') + extra; window.open(u.toString(),'_blank','noopener'); } catch (_) { window.open(state.redirectUri,'_blank'); } };
    const cancel = document.createElement('button'); cancel.textContent='Отмена'; cancel.style.cssText='padding:9px 13px;border:0;border-radius:9px;cursor:pointer';
    cancel.onclick=()=>{ fetch(base+'/captcha/cancel',{method:'POST'}).catch(()=>{}); removeCaptchaOverlay(); };
    box.append(title,text,open,cancel); document.body.appendChild(box); captchaOverlay=box;
  }
  async function refreshCaptcha() {
    try { const state = await json('/captcha/state'); showCaptchaOverlay(state); } catch (_) {}
  }
  setInterval(refresh, 1200);
  setInterval(refreshCaptcha, 700);
  setTimeout(refresh, 50);
  setTimeout(refreshCaptcha, 100);
  const syncReq = (path, method, body) => {
    try {
      const xhr = new XMLHttpRequest();
      xhr.open(method || 'GET', base + path, false);
      xhr.setRequestHeader('Content-Type', 'application/json');
      xhr.send(body === undefined ? null : JSON.stringify(body));
      return xhr.status >= 200 && xhr.status < 300 ? xhr.responseText : '';
    } catch (_) { return ''; }
  };
  const syncJson = (path, method, body, fallback) => {
    const raw = syncReq(path, method, body);
    if (!raw) return fallback;
    try { return JSON.parse(raw); } catch (_) { return fallback; }
  };
  const post = (path, body) => syncReq(path, 'POST', body) !== '';
  window.api = {
    GetConfigsJson: () => cache.configs,
    SaveConfig: (link, protocol) => {
      const x = syncJson('/configs', 'POST', {link, protocol: protocol || 'CSQTT'}, {ok:false});
      return !!x.ok;
    },
    DeleteConfig: id => {
      const x = syncJson('/configs/' + encodeURIComponent(id), 'DELETE', undefined, {ok:false});
      return !!x.ok;
    },
    GetSettingsJson: () => cache.settings,
    SaveSettings: j => post('/settings', JSON.parse(j)),
    GetLogsJson: () => cache.logs,
    ClearLogs: () => post('/logs/clear'),
    GetStatusJson: () => cache.status,
    Connect: id => post('/vpn/connect', {id}),
    Disconnect: () => post('/vpn/disconnect'),
    CheckCoreUpdate: () => {
      const x = syncReq("/updates/core/check", "GET");
      if (x) cache.coreUpdate = x;
      return cache.coreUpdate;
    },
    UpdateCore: () => !!syncJson("/updates/core", "POST", undefined, {ok:false}).ok,
    UpdateCoreAndWait: () => !!syncJson("/updates/core/wait", "POST", undefined, {ok:false}).ok,
    CheckLaLuneUpdate: () => {
      const x = syncJson("/updates/lalune", "GET", undefined, null);
      if (x) {
        cache.laluneUpdate = JSON.stringify({
          update: !!(x.hasUpdate || x.HasUpdate),
          version: x.remoteTag || x.RemoteTag || ''
        });
      }
      return cache.laluneUpdate;
    },
    OpenLaLuneReleases: () => { window.open('https://github.com/Endlad2/LaLune/releases/latest','_blank'); return true; },
    GetVKTokenState: () => {
      const x = syncReq("/vk/state", "GET");
      if (x) cache.vk = x;
      return cache.vk;
    },
    VkLogin: () => !!syncJson("/vk/login", "POST", undefined, {ok:false}).ok,
    DeleteVKToken: () => !!syncJson("/vk/delete", "POST", undefined, {ok:false}).ok,
    ValidateVKToken: () => {
      const x = syncReq("/vk/validate", "GET");
      if (x) cache.vk = x;
      return cache.vk;
    },
    RunVkAutoApiCalls: () => { post("/vk/auto"); return "{\"pending\":true}"; },
    PollAutoApiResult: () => {
      fetch(base + "/vk/auto/poll").then(r=>r.text()).then(x=>cache.vkAuto=x).catch(()=>{});
      return cache.vkAuto;
    },
    FinishVkCalls: callIdsJson => {
      try {
        return !!syncJson("/vk/finish", "POST", JSON.parse(callIdsJson), {ok:false}).ok;
      } catch (_) { return false; }
    },
    GetDeviceId: () => { try { return JSON.parse(cache.settings).deviceId || ''; } catch (_) { return ''; } },
    RegenerateDeviceId: () => {
      try {
        const id = (crypto.randomUUID ? crypto.randomUUID() : (
          'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, c => {
            const r = Math.random() * 16 | 0;
            const v = c === 'x' ? r : (r & 0x3 | 0x8);
            return v.toString(16);
          })
        )).replace(/-/g, '');
        const s = JSON.parse(cache.settings || '{}');
        s.deviceId = id;
        cache.settings = JSON.stringify(s);
        post('/settings', s);
        return id;
      } catch (_) { return ''; }
    },
    SetSelectedConfigJson: j => post('/configs/selected', JSON.parse(j)),
    GetSelectedConfigJson: () => {
      const x = syncReq("/configs/selected", "GET");
      if (x) cache.selected = x;
      return cache.selected;
    },
    IsCoreDownloading: () => {
      const x = syncJson("/updates/core/status", "GET", undefined, null);
      if (x) cache.coreDownloading = !!x.downloading;
      return cache.coreDownloading;
    },
    DeployProtocol: j => {
      try { return !!syncJson('/deploy', 'POST', JSON.parse(j), {ok:false}).ok; }
      catch (_) { return false; }
    },
    DeployLog: () => cache.deployLog,
    IsDeploying: () => cache.deploying
  };
})();
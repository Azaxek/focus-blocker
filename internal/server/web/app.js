// Focus Guard dashboard. Plain ES module. Design rule: the UI never waits for the network.
// Every edit changes the screen first and saves in the background; a failed save rolls back with a message.
const token = document.querySelector('meta[name="fg-token"]').content;
const $ = (s, el = document) => el.querySelector(s);
const pad = n => String(n).padStart(2, '0');
const clone = x => structuredClone(x);

function h(tag, props, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (v == null || v === false) continue;
    if (k === 'class') el.className = v;
    else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else if (['value', 'checked', 'disabled'].includes(k)) el[k] = v;
    else el.setAttribute(k, v === true ? '' : v);
  }
  for (const kid of kids.flat(Infinity)) if (kid != null && kid !== false) el.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
  return el;
}
const NS = 'http://www.w3.org/2000/svg';
function svgIcon(d) {
  const s = document.createElementNS(NS, 'svg');
  for (const [k, v] of Object.entries({ viewBox: '0 0 24 24', width: 16, height: 16, fill: 'none', stroke: 'currentColor', 'stroke-width': 1.8, 'stroke-linecap': 'round', 'stroke-linejoin': 'round', 'aria-hidden': 'true' })) s.setAttribute(k, v);
  const p = document.createElementNS(NS, 'path'); p.setAttribute('d', d); s.append(p); return s;
}
const LOCK = 'M5 11h14v10H5zM8 11V7a4 4 0 0 1 8 0v4', UNLOCK = 'M5 11h14v10H5zM8 11V7a4 4 0 0 1 7.5-2';

/* ---------- state ---------- */
const boot = JSON.parse(document.getElementById('boot').textContent);
const S = { status: boot.s.status, setup: boot.s.setup, unlocked: boot.s.unlocked, lockedUntil: boot.s.lockedUntil, uninstallPending: boot.s.uninstallPending, updateReady: boot.s.updateReady,
  cfg: boot.c.config, presets: boot.c.sitePresets, order: boot.c.categoryOrder, appPresets: boot.c.appPresets,
  offset: Date.parse(boot.s.status.now) - Date.now(), route: 'today', parentTab: 'activity', logFilter: 'all', listTab: 'sites', saved: clone(boot.c.config) };
const nowMs = () => Date.now() + S.offset;
const fmtTime = d => new Date(d).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
const fmtDate = d => new Date(d).toLocaleDateString([], { weekday: 'short', month: 'short', day: 'numeric' });
const fmtStamp = d => `${fmtDate(d)}, ${fmtTime(d)}`;
const fmtDur = ms => { const s = Math.max(0, Math.floor(ms / 1000)), hh = Math.floor(s / 3600), mm = Math.floor(s % 3600 / 60); return `${hh ? hh + ':' + pad(mm) : mm}:${pad(s % 60)}`; };
const hrs = x => x < 1 ? [String(Math.round(x * 60)), 'min'] : [String(Math.round(x * 10) / 10), 'h'];
const DAYS = [[1, 'Mon'], [2, 'Tue'], [3, 'Wed'], [4, 'Thu'], [5, 'Fri'], [6, 'Sat'], [0, 'Sun']];
const uid = () => 'w' + Math.random().toString(36).slice(2, 10);
// The schedule follows a pinned zone, so changing the Windows time zone cannot move a window.
const BROWSER_ZONE = (() => { try { return Intl.DateTimeFormat().resolvedOptions().timeZone || ''; } catch { return ''; } })();
const zoneOffset = z => { try { return new Intl.DateTimeFormat('en-US', { timeZone: z, timeZoneName: 'longOffset' }).format(new Date()); } catch { return z; } };
const wholeMin = sec => Math.floor(sec / 60);

/* ---------- network ---------- */
class ApiError extends Error { constructor(m, code, status) { super(m); this.code = code; this.status = status; } }
async function api(path, method = 'GET', body) {
  const res = await fetch(path, { method, credentials: 'same-origin', headers: { 'X-FG-Token': token, ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}) }, body: body !== undefined ? JSON.stringify(body) : undefined });
  let data = null; try { data = await res.json(); } catch { /* empty body */ }
  if (!res.ok) throw new ApiError(data?.error || res.statusText, data?.code, res.status);
  return data;
}
function toast(msg, kind = '') { const t = h('div', { class: 'toast ' + kind }, msg); $('#toasts').append(t); setTimeout(() => t.remove(), 4500); }

/* ---------- dialogs ---------- */
function modal(build, { wide = false, label = 'Dialog', dismiss = true } = {}) {
  return new Promise(resolve => {
    const dlg = h('dialog', { class: wide ? 'wide' : '', 'aria-label': label });
    let done = false;
    const close = v => { if (done) return; done = true; dlg.close(); dlg.remove(); resolve(v); };
    dlg.append(build(close));
    dlg.addEventListener('cancel', e => { e.preventDefault(); close(null); });
    if (dismiss) dlg.addEventListener('mousedown', e => { if (e.target === dlg) close(null); });
    document.body.append(dlg); dlg.showModal();
    (dlg.querySelector('[autofocus]') || dlg.querySelector('input,textarea,select,button'))?.focus();
  });
}
// Resolves true once onSubmit succeeded. The dialog opens instantly; only the submit button waits.
function askPasscode({ title, body, label = 'Continue', danger = false, confirmNew = false, onSubmit }) {
  return modal(close => {
    const err = h('p', { class: 'err', role: 'alert' });
    const p1 = h('input', { type: 'password', autocomplete: confirmNew ? 'new-password' : 'current-password', autofocus: true, 'aria-label': confirmNew ? 'New passcode' : 'Parent passcode' });
    const p2 = confirmNew ? h('input', { type: 'password', autocomplete: 'new-password', 'aria-label': 'New passcode again' }) : null;
    const go = h('button', { class: 'btn ' + (danger ? 'danger solid' : 'primary'), type: 'submit' }, label);
    return h('form', { class: 'dlg', onsubmit: async e => {
      e.preventDefault(); err.textContent = '';
      if (confirmNew && (p1.value.length < 6 || p1.value !== p2.value)) { err.textContent = p1.value.length < 6 ? 'Use at least 6 characters.' : 'The two passcodes do not match.'; return; }
      go.disabled = true; go.textContent = 'Checking…';
      try { await onSubmit(p1.value); close(true); } catch (ex) { err.textContent = ex.message; p1.select(); go.disabled = false; go.textContent = label; }
    } },
      h('h2', {}, title), body ? h('p', { class: 'muted' }, body) : null,
      h('label', { class: 'field' }, h('span', {}, confirmNew ? 'New passcode (6 or more characters)' : 'Parent passcode'), p1),
      confirmNew ? h('label', { class: 'field' }, h('span', {}, 'Type it again'), p2) : null, err,
      h('div', { class: 'actions' }, h('button', { class: 'btn ghost', type: 'button', onclick: () => close(false) }, 'Cancel'), go));
  }, { label: title, dismiss: false });
}
const confirmBox = (title, body, label = 'Delete') => modal(close => h('div', { class: 'dlg' }, h('h2', {}, title), h('p', { class: 'muted' }, body),
  h('div', { class: 'actions' }, h('button', { class: 'btn ghost', onclick: () => close(false) }, 'Cancel'), h('button', { class: 'btn danger solid', autofocus: true, onclick: () => close(true) }, label))), { label: title }).then(Boolean);

async function unlock() {
  if (S.unlocked) return true;
  const until = S.lockedUntil ? ` Locked until ${fmtTime(S.lockedUntil)}.` : '';
  const ok = await askPasscode({ title: 'Parent passcode', body: 'Enter the passcode to make changes.' + until, label: 'Unlock', onSubmit: p => api('/api/auth/login', 'POST', { passcode: p }) });
  if (ok) { S.unlocked = true; paintChrome(); render(); ensureZone(); }
  return ok;
}
// Pin the schedule to the zone the parent is in, once, so changing the Windows time zone cannot move a window.
let zoneTried = false;
function ensureZone() { if (zoneTried || !S.unlocked || S.cfg.timezone || !BROWSER_ZONE) return; zoneTried = true; edit(c => { c.timezone = BROWSER_ZONE; }); }

/* ---------- optimistic edits ---------- */
// edit() changes S.cfg and re-renders immediately; the save happens in the background.
let dirty = false, saving = false;
function edit(mutate) {
  if (!S.unlocked) { unlock().then(ok => ok && edit(mutate)); return false; }
  const next = clone(S.cfg), problem = mutate(next);
  if (problem) { toast(problem, 'bad'); return false; }
  S.cfg = next; render(); queueSave(); return true;
}
function queueSave() {
  dirty = true;
  if (saving) return;
  saving = true; paintSaving();
  (async () => {
    while (dirty) {
      dirty = false;
      const snap = clone(S.cfg);
      snap.appRules = snap.appRules.filter(r => !r.id.startsWith('custom-') || snap.apps.includes(r.id) || snap.windows.some(w => (w.apps || []).includes(r.id)));
      try {
        const d = await api('/api/config', 'PUT', snap);
        S.saved = clone(d.config);
        if (!dirty && JSON.stringify(d.config) !== JSON.stringify(S.cfg)) { S.cfg = d.config; render(); } // server normalised something
      } catch (e) {
        if (e.status === 401) { S.unlocked = false; paintChrome(); if (await unlock()) { dirty = true; continue; } }
        S.cfg = clone(S.saved); dirty = false; render(); toast('Not saved: ' + e.message, 'bad');
      }
    }
    saving = false; paintSaving();
    refreshStatus().then(() => { paintChrome(); if (S.route === 'today') render(); }).catch(() => {});
  })();
}
const paintSaving = () => { const el = $('#saving'); if (el) el.textContent = saving ? 'Saving…' : ''; };
const DOMAIN = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$/;
function cleanDomain(v) {
  let s = v.trim().toLowerCase();
  if (s.includes('://')) { try { s = new URL(s).host; } catch { return ''; } }
  s = s.replace(/[/?#].*$/, '').replace(/:\d+$/, '').replace(/^\*\./, '').replace(/^www\./, '').replace(/\.$/, '');
  return DOMAIN.test(s) && s.length <= 253 ? s : '';
}

/* ---------- status ---------- */
async function refreshStatus() {
  const d = await api('/api/status');
  Object.assign(S, { status: d.status, setup: d.setup, unlocked: d.unlocked, lockedUntil: d.lockedUntil, uninstallPending: d.uninstallPending, updateReady: d.updateReady, offset: Date.parse(d.status.now) - Date.now() });
}

/* ---------- shell ---------- */
const PAGES = { today: 'Today', schedule: 'Schedule', lists: 'Block lists', parent: 'Parent area' };
function theme(cycle) {
  let t = 'auto'; try { t = localStorage.getItem('fg-theme') || 'auto'; } catch { /* storage may be blocked */ }
  if (cycle) { t = { auto: 'dark', dark: 'light', light: 'auto' }[t]; try { localStorage.setItem('fg-theme', t); } catch { /* ignore */ } toast('Theme: ' + t); }
  if (t === 'auto') document.documentElement.removeAttribute('data-theme'); else document.documentElement.dataset.theme = t;
}
function buildShell() {
  $('#app').replaceChildren(
    h('header', { class: 'bar', id: 'bar' }, h('div', { class: 'col' },
      h('a', { class: 'brand', href: '#/today', 'aria-label': 'Focus Guard' }, (() => { const s = svgIcon('M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18z'); s.setAttribute('width', 22); s.setAttribute('height', 22); s.style.color = 'var(--accent)'; s.setAttribute('stroke-width', 3); return s; })(), h('span', {}, 'Focus Guard')),
      h('nav', { class: 'nav', 'aria-label': 'Main' }, Object.entries(PAGES).map(([k, l]) => h('a', { href: '#/' + k, 'data-page': k }, l))),
      h('div', { class: 'tools' }, h('span', { class: 'saving', id: 'saving', 'aria-live': 'polite' }),
        h('button', { class: 'btn ghost small', id: 'lockbtn', onclick: toggleLock }), h('button', { class: 'btn ghost small', onclick: () => theme(true), 'aria-label': 'Change theme' }, 'Theme')))),
    h('main', { class: 'col', id: 'main', tabindex: '-1' }, h('div', { id: 'banners' }), h('div', { id: 'view', style: 'display:grid;gap:var(--gap)' })));
  addEventListener('scroll', () => $('#bar').classList.toggle('scrolled', scrollY > 4), { passive: true });
}
async function toggleLock() {
  if (S.unlocked) { S.unlocked = false; paintChrome(); render(); api('/api/auth/logout', 'POST', {}).catch(() => {}); toast('Locked'); } else unlock();
}
function paintChrome() {
  for (const a of document.querySelectorAll('.nav a')) { if (a.dataset.page === S.route) a.setAttribute('aria-current', 'page'); else a.removeAttribute('aria-current'); }
  const lb = $('#lockbtn'); if (lb) { lb.replaceChildren(svgIcon(S.unlocked ? UNLOCK : LOCK), S.unlocked ? 'Unlocked' : 'Locked'); lb.title = S.unlocked ? 'Click to lock' : 'Click to unlock'; }
  const st = S.status, b = $('#banners'); if (!b) return;
  const items = [];
  if (st.problem) items.push(h('div', { class: 'banner bad', role: 'alert' }, 'Enforcement problem: ', st.problem));
  if (st.notice) items.push(h('div', { class: 'banner bad', role: 'alert' }, st.notice));
  if (S.cfg.timezone && zoneOffset(S.cfg.timezone) !== zoneOffset(BROWSER_ZONE)) items.push(h('div', { class: 'banner' }, `This PC's time zone (${BROWSER_ZONE}) is not the one Focus Guard follows (${S.cfg.timezone}). Windows stay on ${S.cfg.timezone}.`));
  if (!st.timeSynced) items.push(h('div', { class: 'banner' }, 'Internet time has not been checked yet, so the PC clock is trusted for now.'));
  if (S.updateReady) items.push(h('div', { class: 'banner' }, `Version ${S.updateReady} is downloaded and installs when no focus window is active.`));
  b.replaceChildren(...items);
}

/* ---------- routing and rendering ---------- */
function route() {
  const k = location.hash.replace(/^#\/?/, '');
  S.route = PAGES[k] ? k : 'today';
  document.title = PAGES[S.route] + ' · Focus Guard';
  render({ reset: true });
}
addEventListener('hashchange', route);
// render rebuilds the current page from state (cheap, synchronous) and keeps scroll positions.
function render({ reset = false } = {}) {
  const view = $('#view'); if (!view) return;
  const y = scrollY, ws = $('.week-scroll')?.scrollTop;
  paintChrome();
  view.replaceChildren();
  ({ today: pageToday, schedule: pageSchedule, lists: pageLists, parent: pageParent })[S.route](view);
  if (reset) scrollTo(0, 0); else { scrollTo(0, y); const w = $('.week-scroll'); if (w && ws != null) w.scrollTop = ws; }
}

/* ---------- today ---------- */
const kpi = (k, v, unit) => h('div', { class: 'kpi' }, h('div', { class: 'k' }, k), h('div', { class: 'v' }, v, unit ? h('small', {}, unit) : null));
function pageToday(view) {
  const s = S.status, R = s.rewards, onBreak = !!R.break, free = !s.blocking, next = s.next;
  const finding = onBreak && free ? `Earned free time until ${fmtTime(R.break.until)}, then focus time is back.`
    : free ? (next ? `Next: ${next.name}, ${fmtDate(next.start)} at ${fmtTime(next.start)}.` : 'No focus time is scheduled.')
    : `${s.active.map(a => a.name).join(' and ')} is on until ${fmtTime(s.until)}. ${s.sites} sites and ${s.apps} apps are blocked.`;
  view.append(h('div', { class: 'titlerow' }, h('h1', {}, 'Today'), h('p', {}, finding)));
  const [fd, fdu] = hrs(s.focusHoursToday), [fw, fwu] = hrs(s.focusHoursWeek);
  view.append(h('section', { class: 'card status', 'aria-label': 'Current status' },
    h('div', { class: 'word' }, free ? 'Free time' : 'Blocking'),
    h('div', { class: 'clock', id: 'clock', 'aria-hidden': 'true' }, '--:--'), h('div', { class: 'muted', id: 'clocksub' }),
    !free ? h('div', { class: 'track', role: 'progressbar', 'aria-label': 'Time elapsed in this window' }, h('i', { id: 'prog' })) : null,
    onBreak && free ? h('div', {}, h('button', { class: 'btn primary', onclick: backToFocus }, 'Back to focus now')) : null,
    !free ? h('div', {}, h('button', { class: 'btn danger', onclick: () => endEarly(s.active) }, 'End early (parent)')) : null));
  const ft = freeTimeCard(s);
  if (ft) view.append(ft);
  view.append(h('section', { class: 'card kpis', 'aria-label': 'Summary' },
    kpi('Focus streak', String(s.streak), s.streak === 1 ? 'day' : 'days'), kpi('Focus today', fd, fdu), kpi('Last 7 days', fw, fwu),
    kpi('Blocked today', String(s.siteBlocksToday + s.appBlocksToday), `${s.siteBlocksToday} sites, ${s.appBlocksToday} apps`)));
  const span = Date.parse(s.dayEnd) - Date.parse(s.dayStart), pct = t => Math.min(100, Math.max(0, (Date.parse(t) - Date.parse(s.dayStart)) / span * 100));
  view.append(h('section', { class: 'card', 'aria-label': "Today's schedule" }, h('h2', {}, "Today's schedule"),
    h('div', { class: 'ribbon', role: 'img', 'aria-label': s.today.length ? s.today.map(o => `${o.name} ${fmtTime(o.start)} to ${fmtTime(o.end)}`).join('; ') : 'No focus windows today' },
      s.today.map(o => h('div', { class: 'seg' + (o.ended ? ' ended' : ''), style: `left:${pct(o.start)}%;width:${Math.max(.4, pct(o.end) - pct(o.start))}%` })), h('div', { class: 'now', id: 'nowmark' })),
    h('div', { class: 'ticks', 'aria-hidden': 'true' }, ['12a', '3a', '6a', '9a', '12p', '3p', '6p', '9p', '12a'].map(t => h('span', {}, t))),
    s.today.length ? h('ul', { class: 'rows' }, s.today.map(o => h('li', {}, h('b', {}, o.name), o.ended ? h('span', { class: 'pill warn' }, 'ended early') : null, h('span', { class: 't' }, `${fmtTime(o.start)} – ${fmtTime(o.end)}`)))) : h('p', { class: 'empty' }, 'Nothing is scheduled today.')));
  tick();
}
// Earned free time: visible to the child, spendable without the passcode (a parent grants it).
function freeTimeCard(s) {
  const R = s.rewards;
  if (!R.balanceSec) return null; // while a break runs the status card above already says so
  const when = x => `${fmtDate(x)} ${fmtTime(x)}`;
  const items = R.items.map(i => h('li', {}, h('b', {}, i.reason), h('span', { class: 't' }, `${wholeMin(i.left)} min · good until ${when(i.expires)}`)));
  const maxMin = wholeMin(R.pauseMaxSec), buttons = [];
  if (R.canPause) {
    if (maxMin > 15) buttons.push(h('button', { class: 'btn', onclick: ev => redeem(15, ev.currentTarget) }, 'Use 15 min'));
    buttons.push(h('button', { class: 'btn primary', onclick: ev => redeem(0, ev.currentTarget) }, maxMin > 15 ? `Use all (${maxMin} min)` : `Use ${maxMin} min`));
  }
  const hint = R.break ? `Free time is running until ${fmtTime(R.break.until)}.`
    : R.canPause ? 'Using it pauses focus time. Whatever is left stays saved.'
    : s.blocking ? 'This focus time cannot be paused.' : 'You can use it during focus time.';
  return h('section', { class: 'card', 'aria-label': 'Earned free time' }, h('h2', {}, `Earned free time · ${wholeMin(R.balanceSec)} min`),
    items.length ? h('ul', { class: 'rows' }, items) : null, h('p', { class: 'muted', style: 'margin-top:10px' }, hint),
    buttons.length ? h('div', { class: 'row', style: 'margin-top:12px' }, buttons) : null);
}
async function redeem(minutes, btn) {
  btn.disabled = true;
  try { await api('/api/rewards/redeem', 'POST', { minutes }); await refreshStatus(); render(); toast('Free time started'); }
  catch (e) { btn.disabled = false; toast(e.message, 'bad'); await refreshStatus().catch(() => {}); render(); }
}
async function backToFocus() {
  try { const r = await api('/api/rewards/end', 'POST', {}); await refreshStatus(); render(); toast(`Back to focus. ${wholeMin(r.savedSeconds)} min saved for later.`); }
  catch (e) { toast(e.message, 'bad'); await refreshStatus().catch(() => {}); render(); }
}
async function endEarly(active) {
  let target = active[0];
  if (active.length > 1) {
    target = await modal(close => h('div', { class: 'dlg' }, h('h2', {}, 'Which window?'), active.map(a => h('button', { class: 'btn', onclick: () => close(a) }, `${a.name} (until ${fmtTime(a.end)})`)),
      h('div', { class: 'actions' }, h('button', { class: 'btn ghost', onclick: () => close(null) }, 'Cancel'))));
    if (!target) return;
  }
  const ok = await askPasscode({ title: `End “${target.name}” early?`, body: 'Blocking stops for this one occurrence only, and the activity log records it.', label: 'End early', danger: true,
    onSubmit: p => api('/api/windows/end', 'POST', { key: target.key, passcode: p }) });
  if (ok) { await refreshStatus(); render(); toast('Window ended early'); }
}
let refreshing = false;
function tick() {
  const s = S.status, now = nowMs(), clock = $('#clock');
  if (clock) {
    const brk = !s.blocking && s.rewards.break;
    const target = s.blocking ? Date.parse(s.until) : brk ? Date.parse(brk.until) : s.next ? Date.parse(s.next.start) : null;
    clock.textContent = target ? fmtDur(target - now) : '—';
    $('#clocksub').textContent = target ? (s.blocking ? 'remaining' : brk ? 'of free time left' : 'until the next window') : '';
    const prog = $('#prog');
    if (prog && s.active.length) { const a = Math.min(...s.active.map(x => Date.parse(x.start))); prog.style.width = Math.min(100, Math.max(0, (now - a) / (Date.parse(s.until) - a) * 100)) + '%'; }
    if (target && target - now < -800 && !refreshing) { refreshing = true; refreshStatus().then(() => { paintChrome(); render(); }).finally(() => { refreshing = false; }); }
  }
  const m = $('#nowmark'); if (m) m.style.left = Math.min(100, Math.max(0, (now - Date.parse(s.dayStart)) / (Date.parse(s.dayEnd) - Date.parse(s.dayStart)) * 100)) + '%';
}

/* ---------- schedule ---------- */
const ROW = 22;
const mins = v => { const [a, b] = v.split(':').map(Number); return a * 60 + b; };
const hm = m => `${pad(Math.floor(m / 60) % 24)}:${pad(m % 60)}`;
const t12 = m => fmtTime(new Date(2000, 0, 1, Math.floor(m / 60) % 24, m % 60));
function segments(w) {
  if (!w.days?.length) return [];
  const s = mins(w.start), e = mins(w.end), out = [];
  for (const d of w.days) { if (e > s) out.push({ day: d, from: s, to: e, w }); else { out.push({ day: d, from: s, to: 1440, w }); out.push({ day: (d + 1) % 7, from: 0, to: e, w, cont: true }); } }
  return out;
}
function pageSchedule(view) {
  const wins = S.cfg.windows, today = new Date(nowMs()).getDay();
  view.append(h('div', { class: 'titlerow' }, h('h1', {}, 'Schedule'), h('p', {}, 'Drag on a day to add a window, or click one to edit it. Changes need the parent passcode.'),
    h('button', { class: 'btn primary', onclick: () => editWindow({}) }, 'New window')));
  const cols = DAYS.map(([n, label]) => {
    const col = h('div', { class: 'daycol', 'data-day': n, role: 'group', 'aria-label': label });
    for (let i = 1; i < 24; i++) col.append(h('i', { class: 'hl', style: `top:${i * ROW * 2}px` }));
    const segs = wins.flatMap(segments).filter(x => x.day === n).sort((a, b) => a.from - b.from), lanes = [];
    for (const sg of segs) { let l = lanes.findIndex(end => end <= sg.from); if (l < 0) { l = lanes.length; lanes.push(0); } lanes[l] = sg.to; sg.lane = l; }
    for (const sg of segs) col.append(h('button', { class: 'blk' + (sg.w.disabled ? ' off' : ''), type: 'button',
      style: `top:${sg.from / 30 * ROW}px;height:${Math.max(16, (sg.to - sg.from) / 30 * ROW - 2)}px;left:calc(${sg.lane / lanes.length * 100}% + 2px);width:calc(${100 / lanes.length}% - 4px)`,
      'aria-label': `${sg.w.name}, ${t12(mins(sg.w.start))} to ${t12(mins(sg.w.end))}. Edit`, onclick: ev => { ev.stopPropagation(); editWindow(sg.w); } },
      h('b', {}, sg.cont ? sg.w.name + ' (cont.)' : sg.w.name), `${t12(sg.from)}–${t12(sg.to % 1440)}`));
    if (n === today) { const d = new Date(nowMs()); col.append(h('div', { class: 'nowline', style: `top:${(d.getHours() * 60 + d.getMinutes()) / 30 * ROW}px` })); }
    drag(col, n); return col;
  });
  const gutter = h('div', { class: 'gutter', 'aria-hidden': 'true' }, Array.from({ length: 24 }, (_, i) => h('div', {}, new Date(2000, 0, 1, i).toLocaleTimeString([], { hour: 'numeric' }))));
  const scroll = h('div', { class: 'week-scroll', tabindex: '0', 'aria-label': 'Weekly schedule' }, h('div', { class: 'week-body' }, gutter, cols));
  view.append(h('div', { class: 'week' }, h('div', { class: 'week-head' }, h('div', {}), DAYS.map(([n, l]) => h('div', { class: n === today ? 'today' : '' }, l))), scroll));
  const once = wins.filter(w => w.date);
  view.append(h('section', { class: 'card' }, h('h2', {}, 'One-day windows'), once.length
    ? h('ul', { class: 'rows' }, once.map(w => h('li', {}, h('b', {}, w.name), h('span', { class: 't' }, `${fmtDate(w.date + 'T12:00')} · ${t12(mins(w.start))} – ${t12(mins(w.end))}`), h('button', { class: 'btn small', onclick: () => editWindow(w) }, 'Edit'))))
    : h('p', { class: 'muted' }, 'None. In “New window” choose “One day only” for exams and special days.')));
  if (!view.dataset.scrolled) requestAnimationFrame(() => { const sc = $('.week-scroll'); if (sc && !sc.scrollTop) sc.scrollTop = ROW * 14; });
}
function drag(col, day) {
  let ghost = null, y0 = 0;
  const snap = y => Math.min(1440, Math.max(0, Math.round(y / ROW * 30 / 15) * 15)), yOf = e => e.clientY - col.getBoundingClientRect().top;
  col.addEventListener('pointerdown', e => { if (e.target.closest('.blk') || e.button !== 0) return; y0 = yOf(e); col.setPointerCapture(e.pointerId); ghost = h('div', { class: 'ghostsel' }); col.append(ghost); });
  col.addEventListener('pointermove', e => { if (!ghost) return; const a = Math.min(y0, yOf(e)), b = Math.max(y0, yOf(e)); ghost.style.top = snap(a) / 30 * ROW + 'px'; ghost.style.height = Math.max(ROW / 2, (snap(b) - snap(a)) / 30 * ROW) + 'px'; });
  col.addEventListener('pointerup', e => { if (!ghost) return; const a = snap(Math.min(y0, yOf(e))), b = snap(Math.max(y0, yOf(e))); ghost.remove(); ghost = null; if (b - a >= 15) editWindow({ days: [day], start: hm(a), end: hm(b % 1440) }); });
  col.addEventListener('pointercancel', () => { ghost?.remove(); ghost = null; });
}
async function editWindow(init) {
  if (!(await unlock())) return;
  const isNew = !init.id;
  const w = { id: init.id || uid(), name: init.name || '', days: init.days || [1, 2, 3, 4, 5], date: init.date || '', start: init.start || '15:30', end: init.end || '21:00', ownLists: !!init.ownLists, sites: init.sites || [], apps: init.apps || [], disabled: !!init.disabled, noBreak: !!init.noBreak };
  modal(close => {
    const err = h('p', { class: 'err', role: 'alert' });
    const name = h('input', { type: 'text', value: w.name, placeholder: 'e.g. School nights', maxlength: 60, autofocus: true, 'aria-label': 'Name' });
    const rep = h('div', { class: 'radio' }, ...[['weekly', 'Every week', !w.date], ['once', 'One day only', !!w.date]].map(([v, l, c]) => h('label', {}, h('input', { type: 'radio', name: 'rep', value: v, checked: c, onchange: sync }), l)));
    const boxes = DAYS.map(([n, l]) => h('label', {}, h('input', { type: 'checkbox', value: n, checked: w.days.includes(n) }), h('span', {}, l)));
    const daysEl = h('div', { class: 'days', role: 'group', 'aria-label': 'Days of the week' }, boxes);
    const date = h('input', { type: 'date', value: w.date || new Date(nowMs()).toISOString().slice(0, 10), 'aria-label': 'Date' });
    const start = h('input', { type: 'time', value: w.start, step: 300, 'aria-label': 'Start' }), end = h('input', { type: 'time', value: w.end, step: 300, 'aria-label': 'End' });
    const lm = h('div', { class: 'radio' }, ...[['global', 'Use the global lists', !w.ownLists], ['own', 'Use its own lists', w.ownLists]].map(([v, l, c]) => h('label', {}, h('input', { type: 'radio', name: 'lm', value: v, checked: c, onchange: sync }), l)));
    const sitesTa = h('textarea', { placeholder: 'One site per line, e.g. youtube.com', 'aria-label': 'Sites for this window' }, w.sites.join('\n'));
    const appBoxes = S.cfg.appRules.map(r => h('label', { class: 'switch' }, h('input', { type: 'checkbox', value: r.id, checked: w.apps.includes(r.id) }), r.name));
    const own = h('div', { style: 'display:grid;gap:12px' }, h('label', { class: 'field' }, h('span', {}, 'Sites'), sitesTa), h('div', { class: 'row', style: 'gap:14px' }, appBoxes));
    const off = h('input', { type: 'checkbox', checked: !w.disabled });
    const canPause = h('input', { type: 'checkbox', checked: !w.noBreak });
    const pickedRep = () => $('input[name=rep]:checked', rep).value, pickedLm = () => $('input[name=lm]:checked', lm).value;
    function sync() { daysEl.hidden = pickedRep() === 'once'; dateWrap.hidden = pickedRep() !== 'once'; own.hidden = pickedLm() !== 'own'; }
    const dateWrap = h('label', { class: 'field' }, h('span', {}, 'Date'), date);
    queueMicrotask(sync);
    return h('form', { class: 'dlg', onsubmit: e => {
      e.preventDefault();
      const nw = { id: w.id, name: name.value.trim(), start: start.value, end: end.value, disabled: !off.checked };
      if (!canPause.checked) nw.noBreak = true;
      if (!nw.name) { err.textContent = 'Give the window a name.'; return; }
      if (!nw.start || !nw.end || nw.start === nw.end) { err.textContent = 'Start and end must be different times.'; return; }
      if (pickedRep() === 'once') nw.date = date.value; else { nw.days = boxes.map(l => l.firstChild).filter(i => i.checked).map(i => Number(i.value)); if (!nw.days.length) { err.textContent = 'Pick at least one day.'; return; } }
      if (pickedLm() === 'own') { nw.ownLists = true; nw.sites = sitesTa.value.split('\n').map(cleanDomain).filter(Boolean); nw.apps = appBoxes.map(l => l.firstChild).filter(i => i.checked).map(i => i.value); }
      close(true);
      edit(c => { const i = c.windows.findIndex(x => x.id === w.id); if (i < 0) c.windows.push(nw); else c.windows[i] = nw; });
    } },
      h('h2', {}, isNew ? 'New focus window' : 'Edit focus window'), h('label', { class: 'field' }, h('span', {}, 'Name'), name), rep, daysEl, dateWrap,
      h('div', { class: 'row' }, h('label', { class: 'field grow' }, h('span', {}, 'Starts'), start), h('label', { class: 'field grow' }, h('span', {}, 'Ends'), end)),
      h('p', { class: 'faint' }, 'If the end is earlier than the start, the window runs past midnight.'), lm, own, h('label', { class: 'switch' }, off, 'Enabled'),
      h('label', { class: 'switch', title: 'Turn this off for exams or other nights that should never be paused.' }, canPause, 'Earned free time can pause this window'), err,
      h('div', { class: 'actions' },
        !isNew ? h('button', { class: 'btn danger', type: 'button', style: 'margin-right:auto', onclick: async () => { if (await confirmBox(`Delete “${w.name}”?`, 'It is removed from the schedule.')) { close(true); edit(c => { c.windows = c.windows.filter(x => x.id !== w.id); }); } } }, 'Delete') : null,
        h('button', { class: 'btn ghost', type: 'button', onclick: () => close(false) }, 'Cancel'), h('button', { class: 'btn primary', type: 'submit' }, 'Save')));
  }, { wide: true, label: 'Edit window', dismiss: false });
}

/* ---------- block lists ---------- */
function pageLists(view) {
  view.append(h('div', { class: 'titlerow' }, h('h1', {}, 'Block lists'), h('p', {}, 'Subdomains and www are always included. Changes need the parent passcode.')),
    h('div', { class: 'tabs', role: 'tablist' }, [['sites', 'Websites'], ['apps', 'Apps']].map(([k, l]) => h('button', { role: 'tab', 'aria-selected': String(S.listTab === k), onclick: () => { S.listTab = k; render(); } }, l))),
    S.listTab === 'sites' ? sitesPane() : appsPane());
}
// filterable() hides non-matching items in place, so typing never rebuilds the page or drops focus.
const filterBox = (ph, selector) => h('input', { type: 'search', placeholder: ph, 'aria-label': ph, oninput: e => { const q = e.target.value.trim().toLowerCase(); for (const el of document.querySelectorAll(selector)) el.hidden = !!q && !el.dataset.q.includes(q); } });
function sitesPane() {
  const have = new Set(S.cfg.sites), input = h('input', { type: 'text', placeholder: 'Add a site, e.g. youtube.com', 'aria-label': 'Add a site' }), err = h('p', { class: 'err', role: 'alert' });
  const add = h('form', { class: 'row', onsubmit: e => { e.preventDefault(); const d = cleanDomain(input.value); if (!input.value.trim()) return; if (!d) { err.textContent = `“${input.value.trim()}” is not a domain like example.com`; return; } err.textContent = ''; if (edit(c => { if (!c.sites.includes(d)) c.sites.push(d); })) { const f = $('form input[type=text]'); f?.focus(); } } },
    h('div', { class: 'grow' }, input), h('button', { class: 'btn primary', type: 'submit' }, 'Add'));
  const presets = S.order.map(cat => { const ds = S.presets[cat] || [], all = ds.every(d => have.has(d)); return h('button', { class: 'chip', type: 'button', 'aria-pressed': String(all), title: ds.join(', '),
    onclick: () => edit(c => { c.sites = all ? c.sites.filter(s => !ds.includes(s)) : [...new Set([...c.sites, ...ds])]; }) }, `${cat} · ${ds.length}`); });
  return h('div', { style: 'display:grid;gap:var(--gap)' }, h('section', { class: 'card' }, h('h2', {}, 'Categories'), h('div', { class: 'row' }, presets)),
    h('section', { class: 'card' }, h('h2', {}, `Blocked websites (${have.size})`), add, err, h('div', { style: 'height:10px' }), filterBox('Search the list', '[data-site]'),
      h('div', { class: 'row', style: 'margin-top:14px' }, [...have].sort().map(d => h('span', { class: 'chip', 'data-site': '', 'data-q': d }, d, h('button', { class: 'x', 'aria-label': 'Remove ' + d, onclick: () => edit(c => { c.sites = c.sites.filter(s => s !== d); }) }, '×'))),
        have.size ? null : h('p', { class: 'empty grow' }, 'No websites are blocked.'))));
}
const ruleSummary = r => { const a = [...(r.exe || []), ...(r.path || [])]; return a.slice(0, 3).join(', ') + (a.length > 3 ? '…' : ''); };
function appsPane() {
  const byId = Object.fromEntries(S.cfg.appRules.map(r => [r.id, r])), listed = S.cfg.apps.map(i => byId[i]).filter(Boolean), avail = S.appPresets.filter(p => !S.cfg.apps.includes(p.id));
  const name = h('input', { type: 'text', placeholder: 'Name, e.g. Geometry Dash', maxlength: 40, 'aria-label': 'App name' }), exe = h('input', { type: 'text', placeholder: 'Program file, e.g. game.exe', 'aria-label': 'Program file' });
  const folder = h('input', { type: 'text', placeholder: 'Optional install folder, e.g. \\Geometry Dash\\', 'aria-label': 'Install folder' }), err = h('p', { class: 'err', role: 'alert' });
  const running = h('select', { 'aria-label': 'Pick a running app', onchange: e => { if (e.target.value) { exe.value = e.target.value; if (!name.value) name.value = e.target.value.replace(/\.exe$/i, ''); } } }, h('option', { value: '' }, 'Pick a running app…'));
  const load = async () => { if (!(await unlock())) return; try { const ps = (await api('/api/processes')) || []; running.replaceChildren(h('option', { value: '' }, 'Pick a running app…'), ps.map(p => h('option', { value: p }, p))); toast(ps.length + ' running programs listed'); } catch (e) { toast(e.message, 'bad'); } };
  const custom = h('form', { style: 'display:grid;gap:10px', onsubmit: e => { e.preventDefault();
    const n = name.value.trim(), x = exe.value.trim();
    if (!n || !/\.exe$/i.test(x) || /[\\/:]/.test(x)) { err.textContent = 'Give a name and a program file ending in .exe (no folders).'; return; } err.textContent = '';
    const id = 'custom-' + (n.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '').slice(0, 24) || 'app') + '-' + Math.random().toString(36).slice(2, 6), rule = { id, name: n, exe: [x], original: [x] };
    if (folder.value.trim()) rule.path = [folder.value.trim()];
    edit(c => { c.appRules.push(rule); c.apps.push(id); }); } },
    h('div', { class: 'row' }, h('div', { class: 'grow' }, name), h('div', { class: 'grow' }, exe)),
    h('div', { class: 'row' }, h('div', { class: 'grow' }, folder), h('button', { class: 'btn', type: 'button', onclick: load }, 'List running apps'), h('div', { class: 'grow' }, running)), err, h('div', {}, h('button', { class: 'btn primary', type: 'submit' }, 'Add app')));
  return h('div', { style: 'display:grid;gap:var(--gap)' },
    avail.length ? h('section', { class: 'card' }, h('h2', {}, 'Add from presets'), h('div', { class: 'row' }, avail.map(p => h('button', { class: 'chip', type: 'button', onclick: () => edit(c => { if (!c.appRules.some(r => r.id === p.id)) c.appRules.push(p); c.apps.push(p.id); }) }, '+ ' + p.name)))) : null,
    h('section', { class: 'card' }, h('h2', {}, `Blocked apps (${listed.length})`), h('p', { class: 'muted', style: 'margin-bottom:10px' }, 'Matched by program name, install folder, original file name and publisher, so renaming the file does not help.'),
      filterBox('Search apps', '[data-app]'), h('ul', { class: 'rows' }, listed.length ? listed.map(r => h('li', { 'data-app': '', 'data-q': r.name.toLowerCase() }, h('b', {}, r.name), h('span', { class: 't faint', style: 'overflow:hidden;text-overflow:ellipsis;white-space:nowrap;margin-right:6px' }, ruleSummary(r)),
        h('button', { class: 'btn small danger', 'aria-label': 'Remove ' + r.name, onclick: () => edit(c => { c.apps = c.apps.filter(i => i !== r.id); }) }, 'Remove'))) : [h('li', { class: 'empty' }, 'No apps are blocked.')])),
    h('section', { class: 'card' }, h('h2', {}, 'Add your own app'), custom));
}

/* ---------- parent area ---------- */
const KIND = { window: ['Window', ''], 'blocked-app': ['App closed', ''], tamper: ['Tamper', 'bad'], auth: ['Passcode', ''], update: ['Update', ''], config: ['Settings', ''], service: ['Service', ''], error: ['Error', 'bad'], reward: ['Free time', ''] };
const FILTERS = { all: [], windows: ['window'], blocks: ['blocked-app'], rewards: ['reward'], tamper: ['tamper', 'error'], updates: ['update'], security: ['auth', 'config', 'service'] };
function pageParent(view) {
  view.append(h('div', { class: 'titlerow' }, h('h1', {}, 'Parent area')));
  if (!S.unlocked) { view.append(h('section', { class: 'card empty' }, h('p', {}, 'The activity log, update settings and passcode controls are behind the parent passcode.'), h('div', { style: 'margin-top:14px' }, h('button', { class: 'btn primary', onclick: unlock }, 'Unlock')))); return; }
  const body = h('div', { style: 'display:grid;gap:var(--gap)' }, h('p', { class: 'empty' }, 'Loading…'));
  view.append(h('div', { class: 'tabs', role: 'tablist' }, [['activity', 'Activity log'], ['rewards', 'Free time'], ['updates', 'Updates'], ['security', 'Security']].map(([k, l]) => h('button', { role: 'tab', 'aria-selected': String(S.parentTab === k), onclick: () => { S.parentTab = k; render(); } }, l))), body);
  const tab = S.parentTab, tabs = { activity: activityTab, rewards: rewardsTab, updates: updatesTab, security: securityTab };
  tabs[tab](body).catch(e => { if (e.status === 401) { S.unlocked = false; render(); } else if (S.parentTab === tab) body.replaceChildren(h('p', { class: 'err' }, e.message)); });
}
async function activityTab(body) {
  const kinds = FILTERS[S.logFilter].join(',');
  const [rows, counts] = await Promise.all([api('/api/log?limit=300' + (kinds ? '&kind=' + kinds : '')), api('/api/log?limit=1000&kind=tamper,blocked-app')]);
  if (S.parentTab !== 'activity') return;
  const tamper = (counts || []).filter(r => r.kind === 'tamper').length, closed = (counts || []).filter(r => r.kind === 'blocked-app').length;
  body.replaceChildren(h('section', { class: 'card' }, h('div', { class: 'row', style: 'margin-bottom:12px' }, Object.keys(FILTERS).map(k => h('button', { class: 'chip', 'aria-pressed': String(S.logFilter === k), onclick: () => { S.logFilter = k; render(); } }, k[0].toUpperCase() + k.slice(1)))),
    h('p', { class: 'muted', style: 'margin-bottom:8px' }, `${closed} app closures and ${tamper} tamper events in the retained log.`),
    (rows || []).length ? h('div', { class: 'tablewrap' }, h('table', { class: 'log' }, h('thead', {}, h('tr', {}, h('th', {}, 'When'), h('th', {}, 'Type'), h('th', {}, 'What happened'))),
      h('tbody', {}, rows.map(r => { const [l, c] = KIND[r.kind] || [r.kind, '']; return h('tr', {}, h('td', {}, fmtStamp(r.t)), h('td', {}, h('span', { class: 'pill ' + c }, l)), h('td', {}, r.msg)); })))) : h('p', { class: 'empty' }, 'Nothing here yet.')));
}
// Parents give free time here (for the gym, homework, chores). Every grant asks for the passcode again.
const QUICK = [['Gym', 30], ['Homework', 20], ['Chores', 15]];
async function rewardsTab(body) {
  await refreshStatus(); if (S.parentTab !== 'rewards') return;
  const R = S.status.rewards;
  const reason = h('input', { type: 'text', value: 'Gym', maxlength: 40, 'aria-label': 'What it is for' });
  const amount = h('input', { type: 'number', min: 5, max: 120, step: 5, value: 30, 'aria-label': 'Minutes' });
  const valid = h('select', { 'aria-label': 'How long it stays valid' }, [['Tonight only', 1], ['2 days', 2], ['This week', 7]].map(([l, v]) => h('option', { value: v }, l)));
  const err = h('p', { class: 'err', role: 'alert' });
  const chips = QUICK.map(([r, m]) => h('button', { class: 'chip', type: 'button', onclick: () => { reason.value = r; amount.value = m; } }, `${r} · ${m} min`));
  const form = h('form', { style: 'display:grid;gap:12px', onsubmit: async e => {
    e.preventDefault(); err.textContent = '';
    const minutes = Number(amount.value), why = reason.value.trim() || 'Reward', days = Number(valid.value);
    if (!(minutes >= 5 && minutes <= 120)) { err.textContent = 'Pick between 5 and 120 minutes.'; return; }
    const ok = await askPasscode({ title: `Give ${minutes} minutes of free time?`, body: `For “${why}”. It can pause focus time ${days === 1 ? 'until midnight tonight' : 'for ' + days + ' days'}.`, label: 'Give free time',
      onSubmit: p => api('/api/rewards/grant', 'POST', { minutes, reason: why, days, passcode: p }) });
    if (ok) { toast(`${minutes} minutes of free time given`); render(); }
  } },
    h('div', { class: 'row' }, chips),
    h('div', { class: 'row' }, h('label', { class: 'field grow' }, h('span', {}, 'What it is for'), reason), h('label', { class: 'field' }, h('span', {}, 'Minutes'), amount), h('label', { class: 'field' }, h('span', {}, 'Valid'), valid)),
    err, h('div', {}, h('button', { class: 'btn primary', type: 'submit' }, 'Give free time')));
  const capNow = S.cfg.rewardCapMinutes || R.capMin; // the edit shows at once; the status catches up after the save
  const cap = h('input', { type: 'number', min: 5, max: 600, step: 5, value: capNow, 'aria-label': 'Most free time per day', style: 'width:90px',
    onchange: e => { const v = Math.round(Number(e.target.value)); if (!(v >= 5 && v <= 600)) { toast('Pick between 5 and 600 minutes.', 'bad'); e.target.value = capNow; return; } edit(c => { c.rewardCapMinutes = v; }); } });
  const rows = R.items.length ? h('ul', { class: 'rows' }, R.items.map(i => h('li', {}, h('b', {}, i.reason), h('span', { class: 't' }, `${wholeMin(i.left)} min left · until ${fmtDate(i.expires)} ${fmtTime(i.expires)}`),
    h('button', { class: 'btn small danger', 'aria-label': `Take back ${i.reason}`, onclick: async () => {
      if (!(await confirmBox('Take back this free time?', `${wholeMin(i.left)} unused minutes for “${i.reason}” are removed.`, 'Take back'))) return;
      try { await api('/api/rewards/revoke', 'POST', { id: i.id }); toast('Taken back'); render(); } catch (ex) { toast(ex.message, 'bad'); }
    } }, 'Take back')))) : h('p', { class: 'empty' }, 'Nothing saved up right now.');
  body.replaceChildren(
    h('section', { class: 'card' }, h('h2', {}, 'Give free time'),
      h('p', { class: 'muted', style: 'margin-bottom:12px' }, 'A reward for going to the gym, finishing homework, or anything else. The child spends it from the Today page to pause focus time; whatever they do not use stays saved until it expires.'), form),
    h('div', { class: 'cols' },
      h('section', { class: 'card' }, h('h2', {}, 'Saved up'), rows),
      h('section', { class: 'card', style: 'display:grid;gap:12px' }, h('h2', {}, 'Today'),
        h('div', { class: 'kpis', style: 'grid-template-columns:repeat(2,1fr)' }, kpi('Given today', String(R.grantedToday), `of ${capNow} min`), kpi('Reward streak', String(R.streak), R.streak === 1 ? 'day' : 'days')),
        h('label', { class: 'field' }, h('span', {}, 'Most free time you can give in one day (minutes)'), cap),
        h('p', { class: 'faint' }, 'A window can opt out of this in its settings, for exam nights.'))));
}
async function updatesTab(body) {
  const u = await api('/api/updates'); if (S.parentTab !== 'updates') return;
  const msg = h('p', { class: 'muted', style: 'margin:12px 0' }, u.lastResult || 'No check has run yet.');
  const run = (btn, path, busy) => async () => { const t = btn.textContent; btn.disabled = true; btn.textContent = busy; try { const r = await api(path, 'POST', {}); msg.textContent = r.result; toast(r.result); } catch (e) { msg.textContent = e.message; toast(e.message, 'bad'); } btn.disabled = false; btn.textContent = t; refreshStatus().then(paintChrome).catch(() => {}); };
  const check = h('button', { class: 'btn' }, 'Check for updates'), inst = h('button', { class: 'btn primary' }, 'Install latest now');
  check.onclick = run(check, '/api/updates/check', 'Checking…'); inst.onclick = run(inst, '/api/updates/install', 'Working…');
  const auto = h('input', { type: 'checkbox', checked: u.auto, onchange: e => api('/api/updates/settings', 'PUT', { auto: e.target.checked }).then(() => toast('Saved')).catch(ex => { toast(ex.message, 'bad'); e.target.checked = !e.target.checked; }) });
  body.replaceChildren(h('section', { class: 'card' }, h('h2', {}, 'Updates'),
    h('div', { class: 'kpis' }, kpi('Installed', u.current), kpi('Last check', u.lastCheck ? fmtTime(u.lastCheck) : 'never', u.lastCheck ? fmtDate(u.lastCheck) : ''), kpi('Downloaded', u.ready || '—')), msg,
    h('div', { class: 'row' }, h('label', { class: 'switch' }, auto, 'Install updates automatically (never during a focus window)'), h('span', { class: 'grow' }), check, inst)),
    h('section', { class: 'card' }, h('h2', {}, 'Update history'), (u.history || []).length ? h('ul', { class: 'rows' }, u.history.map(r => h('li', {}, h('span', { class: 'muted' }, fmtStamp(r.t)), h('span', {}, r.msg)))) : h('p', { class: 'empty' }, 'No updates yet.')));
}
async function securityTab(body) {
  const err = h('p', { class: 'err', role: 'alert' }), o = h('input', { type: 'password', autocomplete: 'current-password' }), n = h('input', { type: 'password', autocomplete: 'new-password' }), n2 = h('input', { type: 'password', autocomplete: 'new-password' });
  const form = h('form', { style: 'display:grid;gap:12px', onsubmit: async e => { e.preventDefault(); err.textContent = '';
    if (n.value.length < 6) { err.textContent = 'Use at least 6 characters.'; return; } if (n.value !== n2.value) { err.textContent = 'The new passcodes do not match.'; return; }
    try { await api('/api/auth/change', 'POST', { old: o.value, new: n.value }); toast('Passcode changed'); o.value = n.value = n2.value = ''; } catch (ex) { err.textContent = ex.message; } } },
    h('label', { class: 'field' }, h('span', {}, 'Current passcode'), o), h('label', { class: 'field' }, h('span', {}, 'New passcode (6 or more characters)'), n), h('label', { class: 'field' }, h('span', {}, 'New passcode again'), n2), err, h('div', {}, h('button', { class: 'btn primary', type: 'submit' }, 'Change passcode')));
  const stop = h('button', { class: 'btn danger', onclick: async () => { if (await askPasscode({ title: 'Stop Focus Guard?', body: 'Blocking stops until the PC restarts or the service is started again. The log records this.', label: 'Stop it', danger: true, onSubmit: p => api('/api/service/stop', 'POST', { passcode: p }) })) toast('Stopping…'); } }, 'Stop Focus Guard');
  const zone = h('input', { type: 'text', list: 'zones', value: S.cfg.timezone || BROWSER_ZONE, 'aria-label': 'Time zone' });
  const zones = h('datalist', { id: 'zones' }, (Intl.supportedValuesOf ? Intl.supportedValuesOf('timeZone') : []).map(z => h('option', { value: z })));
  const zoneForm = h('form', { style: 'display:grid;gap:12px', onsubmit: e => { e.preventDefault(); const z = zone.value.trim(); if (z && edit(c => { c.timezone = z; })) toast('Time zone saved'); } },
    h('p', { class: 'muted' }, `Windows follow ${S.cfg.timezone || 'this PC'}${S.cfg.timezone ? ', no matter what the Windows time zone is set to' : ''}. Pinning stops a time zone change from moving a window.`),
    h('label', { class: 'field' }, h('span', {}, 'Time zone'), zone, zones), h('div', {}, h('button', { class: 'btn', type: 'submit' }, 'Save time zone')));
  body.replaceChildren(h('div', { class: 'cols' }, h('section', { class: 'card' }, h('h2', {}, 'Parent passcode'), form),
    h('section', { class: 'card' }, h('h2', {}, 'Time zone'), zoneForm),
    h('section', { class: 'card', style: 'display:grid;gap:12px' }, h('h2', {}, 'Session'), h('p', { class: 'muted' }, 'The dashboard locks itself after 10 idle minutes.'), h('div', {}, h('button', { class: 'btn', onclick: toggleLock }, 'Lock now')),
      h('h2', { style: 'margin-top:12px' }, 'Service'), h('p', { class: 'muted' }, 'Stopping needs the passcode. Windows administrators can still stop the service from the system.'), h('div', {}, stop),
      h('p', { class: 'faint' }, 'The lock is only strong if the everyday Windows account is a standard (non-admin) account.'))));
}

/* ---------- first run, uninstall prompt, boot ---------- */
function welcome() {
  $('#app').replaceChildren(h('div', { class: 'welcome' }, h('section', { class: 'card' }, h('h1', {}, 'Focus Guard: parent setup'),
    h('p', { class: 'muted' }, 'A parent chooses a passcode now. It is needed to end a focus window early, change the schedule or block lists, read the activity log, stop the service, or uninstall. It is stored only as a salted hash and cannot be recovered, so keep it somewhere safe.'),
    h('div', {}, h('button', { class: 'btn primary', autofocus: true, onclick: async () => {
      if (await askPasscode({ title: 'Create the parent passcode', body: 'Parents only: do not share it.', label: 'Create passcode', confirmNew: true, onSubmit: p => api('/api/auth/setup', 'POST', { passcode: p }) })) { S.setup = true; S.unlocked = true; start(); location.hash = '#/schedule'; toast('Passcode created. Add a focus window next.'); }
    } }, 'Create passcode')))));
}
let uninstallShown = false;
async function maybeUninstallPrompt() {
  if (!S.uninstallPending || uninstallShown) return; uninstallShown = true;
  await askPasscode({ title: 'Allow uninstall or reinstall?', body: 'The Focus Guard installer is asking to remove or replace the program. Only a parent should approve this.', label: 'Allow', danger: true, onSubmit: p => api('/api/uninstall/approve', 'POST', { passcode: p }) });
  uninstallShown = false;
}
let lastSig = '';
async function poll() {
  try { await refreshStatus(); } catch { return; }
  if (!$('#view')) return;
  const s = S.status, sig = JSON.stringify([s.blocking, s.active.map(a => a.key), s.next?.key, s.today.map(o => o.key + o.ended), s.streak, Math.floor(s.focusHoursToday * 30), s.siteBlocksToday, s.appBlocksToday, s.sites, s.apps, s.problem, s.notice, S.unlocked, s.paused.map(a => a.key), s.rewards.balanceSec, s.rewards.break?.until, s.rewards.canPause, s.rewards.streak]);
  paintChrome();
  if (sig !== lastSig) { lastSig = sig; if (S.route === 'today') render(); }
  maybeUninstallPrompt();
}
function start() { theme(false); buildShell(); route(); maybeUninstallPrompt(); ensureZone(); }
theme(false);
if (!S.setup) welcome(); else start();
setInterval(poll, 3000); setInterval(tick, 1000);
document.addEventListener('visibilitychange', () => { if (!document.hidden) poll(); });

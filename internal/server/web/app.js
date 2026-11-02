// Focus Guard dashboard. Plain ES modules, no build step, no innerHTML with data.
const token = document.querySelector('meta[name="fg-token"]').content;
const $ = (s, el = document) => el.querySelector(s);
const pad = n => String(n).padStart(2, '0');

/* ---------- tiny DOM helper ---------- */
function h(tag, props, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (v == null || v === false) continue;
    if (k === 'class') el.className = v;
    else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else if (['value', 'checked', 'disabled', 'selected'].includes(k)) el[k] = v;
    else el.setAttribute(k, v === true ? '' : v);
  }
  for (const kid of kids.flat(Infinity)) if (kid != null && kid !== false) el.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
  return el;
}
const ICONS = {
  home: '<path d="M3 11.5 12 4l9 7.5V20a1 1 0 0 1-1 1h-5v-6H9v6H4a1 1 0 0 1-1-1z"/>',
  calendar: '<rect x="3" y="5" width="18" height="16" rx="3"/><path d="M3 10h18M8 3v4M16 3v4"/>',
  list: '<path d="M8 6h13M8 12h13M8 18h13M3.5 6h.01M3.5 12h.01M3.5 18h.01"/>',
  lock: '<rect x="4" y="10" width="16" height="11" rx="3"/><path d="M8 10V7a4 4 0 0 1 8 0v3"/>',
  unlock: '<rect x="4" y="10" width="16" height="11" rx="3"/><path d="M8 10V7a4 4 0 0 1 7.5-2"/>',
  sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
};
function svgEl(tag, attrs = {}, ...kids) {
  const e = document.createElementNS('http://www.w3.org/2000/svg', tag);
  for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, v);
  e.append(...kids);
  return e;
}
function icon(name) {
  const s = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  s.setAttribute('viewBox', '0 0 24 24'); s.setAttribute('fill', 'none'); s.setAttribute('stroke', 'currentColor');
  s.setAttribute('stroke-width', '1.8'); s.setAttribute('stroke-linecap', 'round'); s.setAttribute('stroke-linejoin', 'round');
  s.setAttribute('class', 'icon'); s.setAttribute('aria-hidden', 'true'); s.innerHTML = ICONS[name]; // fixed strings only
  return s;
}

/* ---------- API ---------- */
class ApiError extends Error { constructor(m, code, status) { super(m); this.code = code; this.status = status; } }
async function api(path, method = 'GET', body) {
  const res = await fetch(path, {
    method, credentials: 'same-origin',
    headers: { 'X-FG-Token': token, ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}) },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  let data = null;
  try { data = await res.json(); } catch { /* empty */ }
  if (!res.ok) throw new ApiError(data?.error || res.statusText, data?.code, res.status);
  return data;
}

/* ---------- state ---------- */
const state = { status: null, setup: true, unlocked: false, lockedUntil: null, uninstallPending: false, updateReady: '',
  cfg: null, presets: {}, categoryOrder: [], appPresets: [], offset: 0, sig: '', route: 'dashboard', parentTab: 'activity', logFilter: 'all', listTab: 'sites', search: '' };
const nowMs = () => Date.now() + state.offset;
const fmtTime = d => new Date(d).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
const fmtDate = d => new Date(d).toLocaleDateString([], { weekday: 'short', month: 'short', day: 'numeric' });
const fmtStamp = d => `${fmtDate(d)}, ${fmtTime(d)}`;
function fmtDur(ms) {
  const s = Math.max(0, Math.floor(ms / 1000)), hh = Math.floor(s / 3600), mm = Math.floor(s % 3600 / 60), ss = s % 60;
  return hh > 0 ? `${hh}:${pad(mm)}:${pad(ss)}` : `${pad(mm)}:${pad(ss)}`;
}
const hrs = x => x < 1 ? `${Math.round(x * 60)} min` : `${Math.round(x * 10) / 10} h`;
const DAYS = [{ n: 1, s: 'Mon' }, { n: 2, s: 'Tue' }, { n: 3, s: 'Wed' }, { n: 4, s: 'Thu' }, { n: 5, s: 'Fri' }, { n: 6, s: 'Sat' }, { n: 0, s: 'Sun' }];
const uid = () => 'w' + Math.random().toString(36).slice(2, 10);

function toast(msg, kind = '') {
  const t = h('div', { class: 'toast ' + kind }, msg);
  $('#toasts').append(t);
  setTimeout(() => t.remove(), 5000);
}

/* ---------- modals ---------- */
function modal(build, opts = {}) {
  return new Promise(resolve => {
    const dlg = h('dialog', { class: opts.wide ? 'wide' : '', 'aria-label': opts.label || 'Dialog' });
    let done = false;
    const close = val => { if (done) return; done = true; dlg.close(); dlg.remove(); resolve(val); };
    dlg.append(build(close));
    dlg.addEventListener('cancel', e => { e.preventDefault(); close(null); });
    document.body.append(dlg);
    dlg.showModal();
    (dlg.querySelector('[autofocus]') || dlg.querySelector('input,select,textarea,button'))?.focus();
  });
}

// askPasscode shows the passcode prompt. onSubmit(passcode) must resolve on success
// or throw (the message is shown and the dialog stays open). Resolves true on success.
function askPasscode({ title, body, label = 'Continue', danger = false, confirmNew = false, onSubmit }) {
  return modal(close => {
    const err = h('p', { id: 'pcerr', class: 'err', role: 'alert' });
    const p1 = h('input', { type: 'password', id: 'pc1', autocomplete: confirmNew ? 'new-password' : 'current-password', autofocus: true, 'aria-describedby': 'pcerr' });
    const p2 = confirmNew ? h('input', { type: 'password', id: 'pc2', autocomplete: 'new-password' }) : null;
    const go = h('button', { class: 'btn ' + (danger ? 'danger solid' : 'primary'), type: 'submit' }, label);
    const form = h('form', { class: 'dlg', onsubmit: async e => {
      e.preventDefault(); err.textContent = '';
      if (confirmNew) {
        if (p1.value.length < 6) { err.textContent = 'Use at least 6 characters.'; return; }
        if (p1.value !== p2.value) { err.textContent = 'The two passcodes do not match.'; return; }
      }
      go.disabled = true;
      try { await onSubmit(p1.value); close(true); }
      catch (ex) { err.textContent = ex.message; p1.select(); }
      finally { go.disabled = false; }
    } },
      h('h2', {}, title), body ? h('p', { class: 'muted' }, body) : null,
      h('label', { class: 'field' }, h('span', {}, confirmNew ? 'New passcode' : 'Parent passcode'), p1),
      confirmNew ? h('label', { class: 'field' }, h('span', {}, 'Type it again'), p2) : null,
      err,
      h('div', { class: 'actions' }, h('button', { class: 'btn ghost', type: 'button', onclick: () => close(false) }, 'Cancel'), go));
    return form;
  }, { label: title });
}

function confirmBox(title, body, label = 'Delete') {
  return modal(close => h('div', { class: 'dlg' }, h('h2', {}, title), h('p', { class: 'muted' }, body),
    h('div', { class: 'actions' }, h('button', { class: 'btn ghost', onclick: () => close(false) }, 'Cancel'),
      h('button', { class: 'btn danger solid', autofocus: true, onclick: () => close(true) }, label))), { label: title }).then(Boolean);
}

async function ensureUnlocked() {
  if (state.unlocked) return true;
  const until = state.lockedUntil ? ` Locked until ${fmtTime(state.lockedUntil)}.` : '';
  const ok = await askPasscode({ title: 'Parent passcode', body: 'Enter the passcode to make changes.' + until, label: 'Unlock',
    onSubmit: async p => { await api('/api/auth/login', 'POST', { passcode: p }); } });
  if (ok) { state.unlocked = true; chrome(); }
  return ok;
}

/* ---------- data ---------- */
async function refreshStatus() {
  const d = await api('/api/status');
  state.status = d.status; state.setup = d.setup; state.unlocked = d.unlocked; state.lockedUntil = d.lockedUntil;
  state.uninstallPending = d.uninstallPending; state.updateReady = d.updateReady;
  state.offset = Date.parse(d.status.now) - Date.now();
}
async function refreshConfig() {
  const d = await api('/api/config');
  Object.assign(state, { cfg: d.config, presets: d.sitePresets, categoryOrder: d.categoryOrder, appPresets: d.appPresets });
}
// saveConfig keeps whatever else is in the config and handles an expired session.
async function saveConfig(mutator) {
  const next = structuredClone(state.cfg);
  mutator(next);
  // drop custom app rules nothing refers to any more
  const used = new Set([...next.apps, ...next.windows.flatMap(w => w.apps || [])]);
  next.appRules = next.appRules.filter(r => !r.id.startsWith('custom-') || used.has(r.id));
  try {
    const d = await api('/api/config', 'PUT', next);
    state.cfg = d.config;
    await refreshStatus().catch(() => {}); chrome();
    return true;
  } catch (e) {
    if (e.status === 401) { state.unlocked = false; chrome(); return (await ensureUnlocked()) ? saveConfig(mutator) : false; }
    toast(e.message, 'bad');
    return false;
  }
}

/* ---------- shell ---------- */
const PAGES = { dashboard: ['Dashboard', 'home'], schedule: ['Schedule', 'calendar'], lists: ['Block lists', 'list'], parent: ['Parent area', 'lock'] };
function applyTheme() {
  let t = 'auto';
  try { t = localStorage.getItem('fg-theme') || 'auto'; } catch { /* storage may be unavailable */ }
  if (t === 'auto') document.documentElement.removeAttribute('data-theme'); else document.documentElement.dataset.theme = t;
  return t;
}
function cycleTheme() {
  const order = ['auto', 'dark', 'light'];
  let t = 'auto';
  try { t = localStorage.getItem('fg-theme') || 'auto'; } catch { /* ignore */ }
  const next = order[(order.indexOf(t) + 1) % 3];
  try { localStorage.setItem('fg-theme', next); } catch { /* ignore */ }
  applyTheme(); toast('Theme: ' + next);
}

function buildShell() {
  const app = $('#app');
  app.replaceChildren(h('div', { class: 'shell' },
    h('aside', { class: 'side' },
      h('div', { class: 'brand' }, h('span', { class: 'logo' }), 'Focus Guard'),
      h('nav', { class: 'nav', 'aria-label': 'Main' }, Object.entries(PAGES).map(([k, [label, ic]]) =>
        h('a', { href: '#/' + k, 'data-page': k }, icon(ic), label))),
      h('div', { class: 'spacer' }),
      h('div', { class: 'foot muted', id: 'ver', style: 'padding:8px 12px;font-size:.8rem' })),
    h('main', { class: 'main', id: 'main', tabindex: '-1' },
      h('div', { class: 'topbar' }, h('h1', { id: 'title' }), h('span', { id: 'chip' }),
        h('button', { class: 'btn icon-btn ghost', id: 'lockbtn', 'aria-label': 'Lock or unlock', onclick: toggleLock }),
        h('button', { class: 'btn icon-btn ghost', 'aria-label': 'Change theme', onclick: cycleTheme }, icon('sun'))),
      h('div', { id: 'banners' }),
      h('div', { id: 'view' }))));
}

async function toggleLock() {
  if (state.unlocked) {
    await api('/api/auth/logout', 'POST', {}); state.unlocked = false; chrome(); route(); toast('Locked');
  } else if (await ensureUnlocked()) { route(); }
}

function chrome() {
  const s = state.status; if (!s) return;
  const chip = $('#chip');
  if (chip) chip.replaceChildren(s.blocking
    ? h('span', { class: 'chip accent' }, 'Blocking until ', h('b', {}, fmtTime(s.until)))
    : h('span', { class: 'chip free' }, 'Free time'));
  const lb = $('#lockbtn');
  if (lb) { lb.replaceChildren(icon(state.unlocked ? 'unlock' : 'lock')); lb.title = state.unlocked ? 'Unlocked: click to lock' : 'Locked: click to unlock'; }
  const v = $('#ver'); if (v) v.textContent = 'Version ' + s.version;
  const b = $('#banners');
  if (b) {
    const items = [];
    if (s.problem) items.push(h('div', { class: 'banner bad', role: 'alert' }, 'Enforcement problem: ', s.problem));
    if (!s.timeSynced) items.push(h('div', { class: 'banner' }, 'Internet time could not be checked, so the PC clock is being trusted for now.'));
    if (state.updateReady) items.push(h('div', { class: 'banner' }, `Version ${state.updateReady} is downloaded and will install the next time no focus window is active.`));
    b.replaceChildren(...items);
  }
}

/* ---------- routing ---------- */
function route() {
  const hash = location.hash.replace(/^#\/?/, '');
  state.route = PAGES[hash] ? hash : 'dashboard';
  for (const a of document.querySelectorAll('.nav a')) { if (a.dataset.page === state.route) a.setAttribute('aria-current', 'page'); else a.removeAttribute('aria-current'); }
  $('#title').textContent = PAGES[state.route][0];
  document.title = PAGES[state.route][0] + ' · Focus Guard';
  const view = $('#view');
  view.replaceChildren();
  ({ dashboard, schedule, lists, parent })[state.route](view);
}
window.addEventListener('hashchange', route);

/* ---------- dashboard ---------- */
const C = 2 * Math.PI * 100;
function dashboard(view) {
  const s = state.status;
  const free = !s.blocking;
  const active = s.active;
  const grad = svgEl('linearGradient', { id: 'grad', x1: 0, y1: 0, x2: 1, y2: 1 },
    svgEl('stop', { offset: 0, style: 'stop-color:var(--accent)' }), svgEl('stop', { offset: 1, style: 'stop-color:var(--accent-2)' }));
  const circle = (cls, stroke) => svgEl('circle', { class: cls, cx: 120, cy: 120, r: 100, fill: 'none', 'stroke-width': 14, 'stroke-linecap': 'round', 'stroke-dasharray': C, ...(stroke ? { stroke } : {}) });
  const fg = circle('ring-fg', free ? '' : 'url(#grad)');
  if (free) fg.style.stroke = 'var(--free)';
  fg.setAttribute('stroke-dashoffset', free ? C * .62 : C);
  fg.id = 'ringfg';
  const ring = svgEl('svg', { viewBox: '0 0 240 240', 'aria-hidden': 'true' }, svgEl('defs', {}, grad), circle('ring-bg'), fg);

  const next = s.next;
  const lead = free
    ? (next ? `Next focus time: ${next.name}` : 'No focus time is scheduled.')
    : `${active.map(a => a.name).join(' + ')} is on.`;
  const sub = free
    ? (next ? `Starts ${fmtDate(next.start)} at ${fmtTime(next.start)}. Everything works normally until then.` : 'Add a window on the Schedule page to start blocking.')
    : `${s.sites} sites and ${s.apps} apps are blocked until ${fmtTime(s.until)}.`;

  const hero = h('section', { class: 'card hero ' + (free ? 'free' : 'busy'), 'aria-label': 'Current status' },
    h('div', { class: 'ringbox' }, ring,
      h('div', { class: 'mid' }, h('div', { class: 'state' }, free ? 'Free time' : 'Blocking'),
        h('div', { class: 'clock num', id: 'clock', 'aria-hidden': 'true' }, '--:--'),
        h('div', { class: 'muted', id: 'clocksub', style: 'font-size:.85rem' }))),
    h('div', { class: 'hero-text' },
      h('div', { class: 'big' }, lead), h('p', { class: 'muted' }, sub),
      h('div', { class: 'row' }, active.map(a => h('span', { class: 'chip accent' }, `${a.name} · ${fmtTime(a.start)}–${fmtTime(a.end)}`))),
      !free ? h('div', {}, h('button', { class: 'btn danger', onclick: () => endEarly(active) }, 'End early (parent)')) : null));

  const tiles = h('div', { class: 'tiles' },
    tile(s.streak, s.streak === 1 ? 'day focus streak' : 'days focus streak'),
    tile(hrs(s.focusHoursToday), 'focus time today'),
    tile(hrs(s.focusHoursWeek), 'focus time, last 7 days'),
    tile(s.siteBlocksToday + s.appBlocksToday, `blocked today (${s.siteBlocksToday} sites, ${s.appBlocksToday} apps)`));

  const span = Date.parse(s.dayEnd) - Date.parse(s.dayStart);
  const pct = t => Math.min(100, Math.max(0, (Date.parse(t) - Date.parse(s.dayStart)) / span * 100));
  const tl = h('div', { class: 'timeline', role: 'img', 'aria-label': 'Today: ' + (s.today.length ? s.today.map(o => `${o.name} ${fmtTime(o.start)} to ${fmtTime(o.end)}`).join('; ') : 'no focus windows') },
    s.today.map(o => h('div', { class: 'seg' + (o.ended ? ' ended' : ''), style: `left:${pct(o.start)}%;width:${Math.max(.4, pct(o.end) - pct(o.start))}%`, title: `${o.name} ${fmtTime(o.start)}–${fmtTime(o.end)}` })),
    h('div', { class: 'now', id: 'nowmark' }));
  const sched = h('section', { class: 'card' }, h('h2', {}, "Today's schedule"), tl,
    h('div', { class: 'ticks', 'aria-hidden': 'true' }, ['12a', '3a', '6a', '9a', '12p', '3p', '6p', '9p', '12a'].map(t => h('span', {}, t))),
    s.today.length
      ? h('ul', { class: 'list' }, s.today.map(o => h('li', {}, h('span', { class: 'dot', style: o.ended ? 'background:var(--muted)' : '' }), h('b', {}, o.name),
          h('span', { class: 'muted num' }, `${fmtTime(o.start)} – ${fmtTime(o.end)}`), o.ended ? h('span', { class: 'chip warn' }, 'ended early') : null)))
      : h('p', { class: 'empty' }, 'Nothing scheduled today.'));

  view.append(hero, h('div', { style: 'height:16px' }), tiles, h('div', { style: 'height:16px' }), sched);
  tick();
}
const tile = (v, k) => h('div', { class: 'tile' }, h('div', { class: 'v num' }, String(v)), h('div', { class: 'k' }, k));

async function endEarly(active) {
  let target = active[0];
  if (active.length > 1) {
    target = await modal(close => h('div', { class: 'dlg' }, h('h2', {}, 'Which window?'),
      active.map(a => h('button', { class: 'btn', onclick: () => close(a) }, `${a.name} (until ${fmtTime(a.end)})`)),
      h('div', { class: 'actions' }, h('button', { class: 'btn ghost', onclick: () => close(null) }, 'Cancel'))));
    if (!target) return;
  }
  const ok = await askPasscode({ title: 'End “' + target.name + '” early?', body: 'Blocking stops for this one occurrence only. It is recorded in the activity log.', label: 'End early', danger: true,
    onSubmit: p => api('/api/windows/end', 'POST', { key: target.key, passcode: p }) });
  if (ok) { toast('Window ended early', 'ok'); await refreshStatus(); chrome(); route(); }
}

function tick() {
  const s = state.status; if (!s) return;
  const now = nowMs();
  const clock = $('#clock');
  if (clock) {
    let ms = 0, sub = '';
    if (s.blocking) { ms = Date.parse(s.until) - now; sub = 'remaining'; }
    else if (s.next) { ms = Date.parse(s.next.start) - now; sub = 'until next block'; }
    clock.textContent = (s.blocking || s.next) ? fmtDur(ms) : '—';
    $('#clocksub').textContent = (s.blocking || s.next) ? sub : '';
    const fg = $('#ringfg');
    if (fg && s.blocking && s.active.length) {
      const start = Math.min(...s.active.map(a => Date.parse(a.start))), total = Date.parse(s.until) - start;
      fg.setAttribute('stroke-dashoffset', C * Math.min(1, Math.max(0, (now - start) / total)));
    }
    if ((s.blocking || s.next) && ms <= 0 && !tick.pending) { tick.pending = true; setTimeout(async () => { await poll(); tick.pending = false; }, 1200); }
  }
  const mark = $('#nowmark');
  if (mark) mark.style.left = Math.min(100, Math.max(0, (now - Date.parse(s.dayStart)) / (Date.parse(s.dayEnd) - Date.parse(s.dayStart)) * 100)) + '%';
}

/* ---------- schedule ---------- */
const ROW = 22; // px per half hour
const mins = hm => { const [a, b] = hm.split(':').map(Number); return a * 60 + b; };
const hm = m => `${pad(Math.floor(m / 60) % 24)}:${pad(m % 60)}`;
const t12 = m => fmtTime(new Date(2000, 0, 1, Math.floor(m / 60) % 24, m % 60));

function segments(w) {
  if (!w.days?.length) return [];
  const s = mins(w.start), e = mins(w.end), out = [];
  for (const d of w.days) {
    if (e > s) out.push({ day: d, from: s, to: e, w });
    else { out.push({ day: d, from: s, to: 1440, w }); out.push({ day: (d + 1) % 7, from: 0, to: e, w, cont: true }); }
  }
  return out;
}

function schedule(view) {
  const wins = state.cfg.windows;
  const addBtn = h('button', { class: 'btn primary', onclick: async () => { if (await ensureUnlocked()) editWindow({}); } }, icon('plus'), 'New window');
  const cols = DAYS.map(d => h('div', { class: 'daycol', 'data-day': d.n, role: 'group', 'aria-label': d.s }));
  const today = new Date(nowMs()).getDay();

  for (const col of cols) {
    const day = Number(col.dataset.day);
    const segs = wins.flatMap(segments).filter(x => x.day === day).sort((a, b) => a.from - b.from);
    const lanes = [];
    for (const sg of segs) { let l = lanes.findIndex(end => end <= sg.from); if (l < 0) { l = lanes.length; lanes.push(0); } lanes[l] = sg.to; sg.lane = l; }
    for (const sg of segs) {
      const n = lanes.length;
      col.append(h('button', { class: 'blk' + (sg.w.disabled ? ' off' : ''), type: 'button',
        style: `top:${sg.from / 30 * ROW}px;height:${Math.max(14, (sg.to - sg.from) / 30 * ROW - 2)}px;left:calc(${sg.lane / n * 100}% + 2px);width:calc(${100 / n}% - 4px)`,
        'aria-label': `${sg.w.name}, ${t12(mins(sg.w.start))} to ${t12(mins(sg.w.end))}. Edit`,
        onclick: async ev => { ev.stopPropagation(); if (await ensureUnlocked()) editWindow(sg.w); } },
        h('b', {}, sg.cont ? sg.w.name + ' (cont.)' : sg.w.name), `${t12(sg.from)}–${t12(sg.to % 1440)}`));
    }
    if (day === today) { const now = new Date(nowMs()); col.append(h('div', { class: 'nowline', style: `top:${(now.getHours() * 60 + now.getMinutes()) / 30 * ROW}px` })); }
    enableDrag(col, day);
  }

  const gutter = h('div', { class: 'gutter', 'aria-hidden': 'true' }, Array.from({ length: 24 }, (_, i) =>
    h('div', {}, new Date(2000, 0, 1, i).toLocaleTimeString([], { hour: 'numeric' }))));
  const body = h('div', { class: 'week-body' }, gutter, cols);
  const scroll = h('div', { class: 'week-scroll', tabindex: '0', 'aria-label': 'Weekly schedule' }, body);
  const week = h('div', { class: 'week' },
    h('div', { class: 'week-head' }, h('div', {}), DAYS.map(d => h('div', { class: d.n === today ? 'today' : '' }, d.s))), scroll);

  const once = wins.filter(w => w.date);
  view.append(
    h('div', { class: 'row', style: 'margin-bottom:14px' }, h('p', { class: 'muted grow' }, 'Drag on a day to add a window, or click one to edit it. Changes need the parent passcode.'), addBtn),
    week,
    h('section', { class: 'card', style: 'margin-top:16px' }, h('h2', {}, 'One-day windows'),
      once.length ? h('ul', { class: 'list' }, once.map(w => h('li', {}, h('span', { class: 'dot' }), h('b', {}, w.name),
        h('span', { class: 'muted num grow' }, `${fmtDate(w.date + 'T12:00')} · ${t12(mins(w.start))} – ${t12(mins(w.end))}`),
        h('button', { class: 'btn small', onclick: async () => { if (await ensureUnlocked()) editWindow(w); } }, 'Edit'))))
      : h('p', { class: 'muted' }, 'None. Use “New window”, then choose “One day only”, for exams or special days.')));
  requestAnimationFrame(() => { scroll.scrollTop = ROW * 2 * 7; });
}

function enableDrag(col, day) {
  let ghost = null, y0 = 0;
  const snap = y => Math.min(1440, Math.max(0, Math.round(y / ROW * 30 / 15) * 15));
  const yOf = e => e.clientY - col.getBoundingClientRect().top;
  col.addEventListener('pointerdown', e => {
    if (e.target.closest('.blk') || e.button !== 0) return;
    y0 = yOf(e); col.setPointerCapture(e.pointerId);
    ghost = h('div', { class: 'ghost' }); col.append(ghost);
  });
  col.addEventListener('pointermove', e => {
    if (!ghost) return;
    const a = Math.min(y0, yOf(e)), b = Math.max(y0, yOf(e));
    ghost.style.top = snap(a) / 30 * ROW + 'px'; ghost.style.height = Math.max(ROW / 2, (snap(b) - snap(a)) / 30 * ROW) + 'px';
  });
  const finish = async e => {
    if (!ghost) return;
    const a = snap(Math.min(y0, yOf(e))), b = snap(Math.max(y0, yOf(e)));
    ghost.remove(); ghost = null;
    if (b - a < 15) return;
    if (await ensureUnlocked()) editWindow({ days: [day], start: hm(a), end: hm(b % 1440) });
  };
  col.addEventListener('pointerup', finish);
  col.addEventListener('pointercancel', () => { ghost?.remove(); ghost = null; });
}

function editWindow(init) {
  const isNew = !init.id;
  const w = { id: init.id || uid(), name: init.name || '', days: init.days || [1, 2, 3, 4, 5], date: init.date || '',
    start: init.start || '15:30', end: init.end || '21:00', ownLists: !!init.ownLists, sites: init.sites || [], apps: init.apps || [], disabled: !!init.disabled };
  return modal(close => {
    const err = h('p', { class: 'err', role: 'alert' });
    const name = h('input', { type: 'text', value: w.name, placeholder: 'e.g. School nights', maxlength: 60, required: true, autofocus: true });
    const oneOff = !!w.date;
    const rep = h('div', { class: 'radio' },
      h('label', {}, h('input', { type: 'radio', name: 'rep', value: 'weekly', checked: !oneOff, onchange: sync }), 'Every week'),
      h('label', {}, h('input', { type: 'radio', name: 'rep', value: 'once', checked: oneOff, onchange: sync }), 'One day only'));
    const dayBoxes = DAYS.map(d => h('label', {}, h('input', { type: 'checkbox', value: d.n, checked: w.days.includes(d.n) }), h('span', {}, d.s)));
    const daysEl = h('div', { class: 'days', role: 'group', 'aria-label': 'Days of the week' }, dayBoxes);
    const date = h('input', { type: 'date', value: w.date || new Date(nowMs()).toISOString().slice(0, 10) });
    const dateField = h('label', { class: 'field' }, h('span', {}, 'Date'), date);
    const start = h('input', { type: 'time', value: w.start, required: true, step: 300 });
    const end = h('input', { type: 'time', value: w.end, required: true, step: 300 });
    const lm = h('div', { class: 'radio' },
      h('label', {}, h('input', { type: 'radio', name: 'lm', value: 'global', checked: !w.ownLists, onchange: sync }), 'Use the global lists'),
      h('label', {}, h('input', { type: 'radio', name: 'lm', value: 'own', checked: w.ownLists, onchange: sync }), 'Use its own lists'));
    const sitesTa = h('textarea', { placeholder: 'one site per line, e.g. youtube.com', 'aria-label': 'Sites for this window' }, w.sites.join('\n'));
    const appBoxes = state.cfg.appRules.map(r => h('label', { class: 'switch' }, h('input', { type: 'checkbox', value: r.id, checked: w.apps.includes(r.id) }), r.name));
    const own = h('div', { class: 'grid' }, h('label', { class: 'field' }, h('span', {}, 'Sites'), sitesTa), h('div', { class: 'field' }, h('span', {}, 'Apps'), h('div', { class: 'row', style: 'gap:14px' }, appBoxes)));
    const off = h('input', { type: 'checkbox', checked: !w.disabled });
    function sync() {
      const once = $('input[name=rep]:checked', rep).value === 'once';
      daysEl.parentElement.hidden = once; dateField.hidden = !once;
      own.hidden = $('input[name=lm]:checked', lm).value !== 'own';
    }
    const form = h('form', { class: 'dlg', onsubmit: async e => {
      e.preventDefault(); err.textContent = '';
      const once = $('input[name=rep]:checked', rep).value === 'once';
      const nw = { id: w.id, name: name.value.trim(), start: start.value, end: end.value, disabled: !off.checked };
      if (!nw.name) { err.textContent = 'Give the window a name.'; return; }
      if (once) nw.date = date.value; else {
        nw.days = dayBoxes.map(l => l.firstChild).filter(i => i.checked).map(i => Number(i.value));
        if (!nw.days.length) { err.textContent = 'Pick at least one day.'; return; }
      }
      if ($('input[name=lm]:checked', lm).value === 'own') {
        nw.ownLists = true; nw.sites = sitesTa.value.split('\n').map(x => x.trim()).filter(Boolean);
        nw.apps = appBoxes.map(l => l.firstChild).filter(i => i.checked).map(i => i.value);
      }
      const ok = await saveConfig(c => { const i = c.windows.findIndex(x => x.id === w.id); if (i < 0) c.windows.push(nw); else c.windows[i] = nw; });
      if (ok) { close(true); toast(isNew ? 'Window added' : 'Window saved', 'ok'); route(); }
      else err.textContent = 'Could not save. See the message above.';
    } },
      h('h2', {}, isNew ? 'New focus window' : 'Edit focus window'),
      h('label', { class: 'field' }, h('span', {}, 'Name'), name),
      rep, h('div', {}, daysEl), dateField,
      h('div', { class: 'row' }, h('label', { class: 'field grow' }, h('span', {}, 'Starts'), start), h('label', { class: 'field grow' }, h('span', {}, 'Ends'), end)),
      h('p', { class: 'muted', style: 'font-size:.85rem' }, 'If the end is earlier than the start, the window runs past midnight into the next day.'),
      lm, own,
      h('label', { class: 'switch' }, off, 'Enabled'),
      err,
      h('div', { class: 'actions' },
        !isNew ? h('button', { class: 'btn danger', type: 'button', style: 'margin-right:auto', onclick: async () => {
          if (await confirmBox('Delete “' + w.name + '”?', 'This removes the window from the schedule.')) {
            if (await saveConfig(c => { c.windows = c.windows.filter(x => x.id !== w.id); })) { close(true); toast('Window deleted'); route(); }
          } } }, 'Delete') : null,
        h('button', { class: 'btn ghost', type: 'button', onclick: () => close(false) }, 'Cancel'),
        h('button', { class: 'btn primary', type: 'submit' }, 'Save')));
    queueMicrotask(sync);
    return form;
  }, { wide: true, label: 'Edit window' });
}

/* ---------- lists ---------- */
function lists(view) {
  const tabs = h('div', { class: 'tabs', role: 'tablist' }, [['sites', 'Websites'], ['apps', 'Apps']].map(([k, l]) =>
    h('button', { role: 'tab', 'aria-selected': String(state.listTab === k), onclick: () => { state.listTab = k; state.search = ''; route(); } }, l)));
  view.append(tabs, state.listTab === 'sites' ? sitesPane() : appsPane());
}
const searchBox = ph => h('input', { type: 'search', placeholder: ph, value: state.search, 'aria-label': ph, oninput: e => { state.search = e.target.value; const pos = e.target.selectionStart; route(); const n = $('input[type=search]'); n.focus(); n.setSelectionRange(pos, pos); } });
async function guarded(fn) { if (await ensureUnlocked()) { await fn(); route(); } }

function sitesPane() {
  const q = state.search.trim().toLowerCase();
  const all = [...state.cfg.sites].sort();
  const shown = all.filter(d => d.includes(q));
  const err = h('p', { class: 'err', role: 'alert' });
  const input = h('input', { type: 'text', placeholder: 'Add a site, e.g. youtube.com', 'aria-label': 'Add a site' });
  const add = h('form', { class: 'row', onsubmit: e => { e.preventDefault(); if (!input.value.trim()) return; guarded(async () => {
    const v = input.value; const ok = await saveConfig(c => c.sites.push(v)); if (ok) toast('Added', 'ok'); }); } },
    h('div', { class: 'grow' }, input), h('button', { class: 'btn primary', type: 'submit' }, 'Add'));
  const presets = state.categoryOrder.map(cat => {
    const ds = state.presets[cat] || [], have = ds.every(d => state.cfg.sites.includes(d));
    return h('button', { class: 'chip', type: 'button', 'aria-pressed': String(have), title: ds.join(', '),
      onclick: () => guarded(() => saveConfig(c => { c.sites = have ? c.sites.filter(s => !ds.includes(s)) : [...new Set([...c.sites, ...ds])]; })) }, `${cat} (${ds.length})`);
  });
  return h('div', { class: 'grid' },
    h('section', { class: 'card' }, h('h2', {}, 'Category presets'), h('p', { class: 'muted', style: 'margin-bottom:10px' }, 'Toggle a whole category on or off. Subdomains and www are always included.'), h('div', { class: 'row' }, presets)),
    h('section', { class: 'card' }, h('h2', {}, `Blocked websites (${all.length})`), add, err, h('div', { style: 'height:10px' }), searchBox('Search the list'),
      h('div', { class: 'row', style: 'margin-top:14px' }, shown.length ? shown.map(d => h('span', { class: 'chip' }, d,
        h('button', { class: 'x', 'aria-label': 'Remove ' + d, onclick: () => guarded(() => saveConfig(c => { c.sites = c.sites.filter(s => s !== d); })) }, '×'))) : h('p', { class: 'empty grow' }, q ? 'No matches.' : 'The list is empty.'))));
}

function ruleSummary(r) { return [...(r.exe || []), ...(r.path || [])].slice(0, 3).join(', ') + ([...(r.exe || []), ...(r.path || [])].length > 3 ? '…' : ''); }

function appsPane() {
  const q = state.search.trim().toLowerCase();
  const byId = Object.fromEntries(state.cfg.appRules.map(r => [r.id, r]));
  const listed = state.cfg.apps.map(id => byId[id]).filter(Boolean);
  const shown = listed.filter(r => r.name.toLowerCase().includes(q));
  const available = state.appPresets.filter(p => !state.cfg.apps.includes(p.id));
  const name = h('input', { type: 'text', placeholder: 'Name, e.g. Geometry Dash', maxlength: 40, 'aria-label': 'App name' });
  const exe = h('input', { type: 'text', placeholder: 'Program file, e.g. game.exe', 'aria-label': 'Program file name' });
  const folder = h('input', { type: 'text', placeholder: 'Optional install folder, e.g. \\Geometry Dash\\', 'aria-label': 'Install folder fragment' });
  const running = h('select', { 'aria-label': 'Pick a running app', onchange: e => { if (e.target.value) { exe.value = e.target.value; if (!name.value) name.value = e.target.value.replace(/\.exe$/i, ''); } } }, h('option', { value: '' }, 'Pick a running app…'));
  const loadRunning = () => guarded(async () => { try { const ps = await api('/api/processes'); running.replaceChildren(h('option', { value: '' }, 'Pick a running app…'), ps.map(p => h('option', { value: p }, p))); toast(ps.length + ' running programs listed'); } catch (e) { toast(e.message, 'bad'); } });
  const custom = h('form', { class: 'grid', onsubmit: e => { e.preventDefault(); guarded(async () => {
    if (!name.value.trim() || !exe.value.trim()) { toast('A name and a program file are needed.', 'bad'); return; }
    const id = 'custom-' + name.value.trim().toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '').slice(0, 24) + '-' + Math.random().toString(36).slice(2, 6);
    const rule = { id, name: name.value.trim(), exe: [exe.value.trim()], original: [exe.value.trim()] };
    if (folder.value.trim()) rule.path = [folder.value.trim()];
    if (await saveConfig(c => { c.appRules.push(rule); c.apps.push(id); })) toast('Added', 'ok'); }); } },
    h('div', { class: 'row' }, h('div', { class: 'grow' }, name), h('div', { class: 'grow' }, exe)),
    h('div', { class: 'row' }, h('div', { class: 'grow' }, folder), h('button', { class: 'btn', type: 'button', onclick: loadRunning }, 'List running apps'), h('div', { class: 'grow' }, running)),
    h('div', {}, h('button', { class: 'btn primary', type: 'submit' }, 'Add app')));
  return h('div', { class: 'grid' },
    available.length ? h('section', { class: 'card' }, h('h2', {}, 'Add from presets'), h('div', { class: 'row' }, available.map(p =>
      h('button', { class: 'chip', type: 'button', onclick: () => guarded(() => saveConfig(c => { if (!c.appRules.some(r => r.id === p.id)) c.appRules.push(p); c.apps.push(p.id); })) }, '+ ' + p.name)))) : null,
    h('section', { class: 'card' }, h('h2', {}, `Blocked apps (${listed.length})`),
      h('p', { class: 'muted', style: 'margin-bottom:10px' }, 'Matched by program name, install folder, original file name and publisher, so renaming the file does not help.'),
      searchBox('Search apps'),
      h('ul', { class: 'list' }, shown.length ? shown.map(r => h('li', {}, h('span', { class: 'dot' }), h('b', {}, r.name), h('span', { class: 'muted grow', style: 'overflow:hidden;text-overflow:ellipsis;white-space:nowrap' }, ruleSummary(r)),
        h('button', { class: 'btn small danger', 'aria-label': 'Remove ' + r.name, onclick: () => guarded(() => saveConfig(c => { c.apps = c.apps.filter(i => i !== r.id); })) }, 'Remove'))) : [h('li', { class: 'empty' }, q ? 'No matches.' : 'No apps are blocked.')])),
    h('section', { class: 'card' }, h('h2', {}, 'Add your own app'), custom));
}

/* ---------- parent area ---------- */
const KIND = { window: ['Window', 'accent'], 'blocked-app': ['App closed', 'warn'], tamper: ['Tamper', 'bad'], auth: ['Passcode', ''], update: ['Update', 'free'], config: ['Settings', ''], service: ['Service', ''], error: ['Error', 'bad'] };
const FILTERS = { all: [], windows: ['window'], blocks: ['blocked-app'], tamper: ['tamper', 'error'], updates: ['update'], security: ['auth', 'config', 'service'] };

async function parent(view) {
  if (!state.unlocked) {
    view.append(h('section', { class: 'card empty' }, h('div', { style: 'font-size:2rem' }, icon('lock')), h('h2', {}, 'This area is for parents'),
      h('p', { style: 'margin:8px 0 16px' }, 'The activity log, update settings and passcode controls are behind the parent passcode.'),
      h('button', { class: 'btn primary', onclick: async () => { if (await ensureUnlocked()) route(); } }, 'Unlock')));
    return;
  }
  const tabs = h('div', { class: 'tabs', role: 'tablist' }, [['activity', 'Activity log'], ['updates', 'Updates'], ['security', 'Security']].map(([k, l]) =>
    h('button', { role: 'tab', 'aria-selected': String(state.parentTab === k), onclick: () => { state.parentTab = k; route(); } }, l)));
  const body = h('div', {});
  view.append(tabs, body);
  try {
    await ({ activity: activityTab, updates: updatesTab, security: securityTab })[state.parentTab](body);
  } catch (e) {
    if (e.status === 401) { state.unlocked = false; chrome(); route(); } else body.append(h('p', { class: 'err' }, e.message));
  }
}

async function activityTab(body) {
  const kinds = FILTERS[state.logFilter].join(',');
  const rows = (await api('/api/log?limit=300' + (kinds ? '&kind=' + kinds : ''))) || [];
  const chips = Object.keys(FILTERS).map(k => h('button', { class: 'chip', 'aria-pressed': String(state.logFilter === k), onclick: () => { state.logFilter = k; route(); } }, k[0].toUpperCase() + k.slice(1)));
  const counts = (await api('/api/log?limit=1000&kind=tamper,blocked-app')) || [];
  const tamper = counts.filter(r => r.kind === 'tamper').length, apps = counts.filter(r => r.kind === 'blocked-app').length;
  body.append(h('section', { class: 'card' }, h('div', { class: 'row', style: 'margin-bottom:12px' }, chips),
    h('p', { class: 'muted', style: 'margin-bottom:10px' }, `${apps} app closures and ${tamper} tamper events in the retained log.`),
    rows.length ? h('div', { class: 'tablewrap' }, h('table', { class: 'log' }, h('thead', {}, h('tr', {}, h('th', {}, 'When'), h('th', {}, 'Type'), h('th', {}, 'What happened'))),
      h('tbody', {}, rows.map(r => { const [l, c] = KIND[r.kind] || [r.kind, '']; return h('tr', {}, h('td', { class: 'num' }, fmtStamp(r.t)), h('td', {}, h('span', { class: 'chip ' + c }, l)), h('td', {}, r.msg)); })))) : h('p', { class: 'empty' }, 'Nothing here yet.')));
}

async function updatesTab(body) {
  const u = await api('/api/updates');
  const auto = h('input', { type: 'checkbox', checked: u.auto, onchange: async e => { try { await api('/api/updates/settings', 'PUT', { auto: e.target.checked }); toast('Saved', 'ok'); } catch (ex) { toast(ex.message, 'bad'); e.target.checked = !e.target.checked; } } });
  const check = h('button', { class: 'btn', onclick: async ev => { ev.target.disabled = true; ev.target.textContent = 'Checking…'; try { const r = await api('/api/updates/check', 'POST', {}); toast(r.result, 'ok'); } catch (ex) { toast(ex.message, 'bad'); } route(); } }, 'Check now');
  body.append(h('section', { class: 'card' }, h('h2', {}, 'Updates'),
    h('div', { class: 'tiles' }, tile(u.current, 'installed version'), tile(u.lastCheck ? fmtTime(u.lastCheck) : 'never', u.lastCheck ? 'last check, ' + fmtDate(u.lastCheck) : 'last check'), tile(u.ready || 'none', 'downloaded, waiting')),
    h('p', { class: 'muted', style: 'margin:12px 0' }, u.lastResult || 'No check has run yet.'),
    h('div', { class: 'row' }, h('label', { class: 'switch' }, auto, 'Install updates automatically (only when no focus window is active)'), h('span', { class: 'grow' }), check)),
    h('section', { class: 'card' }, h('h2', {}, 'Update history'), (u.history || []).length
      ? h('ul', { class: 'list' }, (u.history || []).map(r => h('li', {}, h('span', { class: 'muted num' }, fmtStamp(r.t)), h('span', {}, r.msg))))
      : h('p', { class: 'empty' }, 'No updates yet.')));
}

async function securityTab(body) {
  const err = h('p', { class: 'err', role: 'alert' });
  const o = h('input', { type: 'password', autocomplete: 'current-password' }), n = h('input', { type: 'password', autocomplete: 'new-password' }), n2 = h('input', { type: 'password', autocomplete: 'new-password' });
  const change = h('form', { class: 'grid', onsubmit: async e => { e.preventDefault(); err.textContent = '';
    if (n.value.length < 6) { err.textContent = 'Use at least 6 characters.'; return; }
    if (n.value !== n2.value) { err.textContent = 'The new passcodes do not match.'; return; }
    try { await api('/api/auth/change', 'POST', { old: o.value, new: n.value }); toast('Passcode changed', 'ok'); o.value = n.value = n2.value = ''; } catch (ex) { err.textContent = ex.message; } } },
    h('label', { class: 'field' }, h('span', {}, 'Current passcode'), o), h('label', { class: 'field' }, h('span', {}, 'New passcode (6+ characters)'), n), h('label', { class: 'field' }, h('span', {}, 'New passcode again'), n2), err,
    h('div', {}, h('button', { class: 'btn primary', type: 'submit' }, 'Change passcode')));
  const stop = h('button', { class: 'btn danger', onclick: async () => {
    const ok = await askPasscode({ title: 'Stop Focus Guard?', body: 'Blocking stops until the PC restarts or the service is started again. This is recorded in the log.', label: 'Stop it', danger: true,
      onSubmit: p => api('/api/service/stop', 'POST', { passcode: p }) });
    if (ok) toast('Stopping…'); } }, 'Stop Focus Guard');
  body.append(h('div', { class: 'grid cols-2' },
    h('section', { class: 'card' }, h('h2', {}, 'Parent passcode'), change),
    h('section', { class: 'card' }, h('h2', {}, 'Session'),
      h('p', { class: 'muted', style: 'margin-bottom:12px' }, 'The dashboard locks itself after 10 idle minutes.'), h('button', { class: 'btn', onclick: toggleLock }, 'Lock now'),
      h('h2', { style: 'margin-top:24px' }, 'Service'), h('p', { class: 'muted', style: 'margin:6px 0 12px' }, 'Stopping needs the passcode. Windows administrators can still stop the service from the system.'), stop,
      h('h2', { style: 'margin-top:24px' }, 'Reminder'), h('p', { class: 'muted', style: 'margin-top:6px' }, 'The lock is only strong if the everyday Windows account is a standard (non-admin) account.'))));
}

/* ---------- first run + uninstall prompt ---------- */
function welcome() {
  $('#app').replaceChildren(h('div', { class: 'welcome' }, h('section', { class: 'card' },
    h('div', { class: 'brand', style: 'padding:0' }, h('span', { class: 'logo' }), 'Focus Guard'),
    h('h1', {}, 'Parent setup'),
    h('p', { class: 'muted' }, 'A parent chooses a passcode now. It is needed to end a focus window early, change the schedule or block lists, view the activity log, stop the service, or uninstall. It is stored only as a salted hash and cannot be recovered, so write it down somewhere safe.'),
    h('button', { class: 'btn primary', autofocus: true, onclick: async () => {
      const ok = await askPasscode({ title: 'Create the parent passcode', body: 'At least 6 characters. Parents only: do not share it.', label: 'Create passcode', confirmNew: true,
        onSubmit: p => api('/api/auth/setup', 'POST', { passcode: p }) });
      if (ok) { await boot(); toast('Passcode created. Next, add a focus window on the Schedule page.', 'ok'); location.hash = '#/schedule'; }
    } }, 'Create passcode'))));
}

let uninstallShown = false;
async function maybeUninstallPrompt() {
  if (!state.uninstallPending || uninstallShown) return;
  uninstallShown = true;
  await askPasscode({ title: 'Allow uninstall or reinstall?', body: 'The Focus Guard installer is asking to remove or replace the program. Only a parent should approve this.', label: 'Allow', danger: true,
    onSubmit: p => api('/api/uninstall/approve', 'POST', { passcode: p }) });
  uninstallShown = false;
}

/* ---------- boot ---------- */
let lastSig = '';
async function poll() {
  try {
    await refreshStatus();
  } catch { return; }
  if (!state.setup) return;
  if (!$('#view')) { await boot(); return; }
  chrome();
  const s = state.status;
  const sig = JSON.stringify([s.blocking, s.active.map(a => a.key), s.next?.key, s.today.map(o => o.key + o.ended), s.streak, Math.floor(s.focusHoursToday * 20), s.siteBlocksToday, s.appBlocksToday, s.sites, s.apps]);
  if (state.route === 'dashboard' && sig !== lastSig) { lastSig = sig; route(); }
  tick();
  maybeUninstallPrompt();
}

async function boot() {
  applyTheme();
  await Promise.all([refreshStatus(), refreshConfig()]);
  if (!state.setup) { welcome(); return; }
  buildShell(); chrome(); route();
  maybeUninstallPrompt();
}

boot().catch(e => { $('#app').replaceChildren(h('div', { class: 'welcome' }, h('section', { class: 'card' }, h('h2', {}, 'Cannot reach the Focus Guard service'), h('p', { class: 'muted' }, String(e.message))))); });
setInterval(poll, 4000);
setInterval(tick, 1000);
document.addEventListener('visibilitychange', () => { if (!document.hidden) poll(); });

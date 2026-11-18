// Gemeinsame Bausteine für die Browser-Skripte des Prüflabors.
// Der Browser fragt keinen echten DNS: Jede Domain aus beispiel.test geht
// an die IP, die laut Zone (labor/lauf/zone.txt) beim Öffnen gilt. Ein
// offener Kontext behält sie, wie ein Browser mit zwischengespeichertem DNS
// (Fall „alter Tab“ vom 04.11.).
'use strict';
const fs = require('fs');
const path = require('path');
const { chromium } = require('playwright');

const LAUF = path.join(__dirname, '..', 'lauf');

// LAB_MAP=hostdeck=10.77.0.10 hält einen Namen fest auf einer IP (etwa den alten
// Leitstand nach der DNS-Umstellung, wie ein alter Tab).
function zone() {
  const m = {};
  for (const line of fs.readFileSync(path.join(LAUF, 'zone.txt'), 'utf8').split('\n')) {
    const [name, typ, wert] = line.trim().split(/\s+/);
    if (name && name !== '@' && typ === 'A' && !m[name]) m[name] = wert;
  }
  for (const pair of (process.env.LAB_MAP || '').split(',')) {
    const [name, ip] = pair.split('=');
    if (name && ip) m[name.trim()] = ip.trim();
  }
  return m;
}

function secret(name) { return fs.readFileSync(path.join(LAUF, name), 'utf8').trim(); }

let browser = null;
async function launch() {
  if (!browser) browser = await chromium.launch();
  return browser;
}

// Ein Kontext mit fester DNS-Zuordnung. Die Zertifikate stellt die
// Labor-Zertifikatsstelle aus; Chromium kennt sie nicht, deshalb wird nur hier
// die Prüfung übersprungen. HostDeck und curl auf den Servern prüfen sie echt.
async function context(opts = {}) {
  const z = zone();
  const rules = Object.entries(z).map(([n, ip]) => 'MAP ' + n + '.beispiel.test ' + ip).join(', ');
  const env = Object.assign({}, process.env);
  for (const k of ['HTTPS_PROXY', 'https_proxy', 'HTTP_PROXY', 'http_proxy', 'ALL_PROXY', 'all_proxy']) delete env[k];
  const b = await chromium.launch({ env, args: ['--host-resolver-rules=' + rules + ', EXCLUDE localhost', '--no-proxy-server'] });
  const ctx = await b.newContext({
    ignoreHTTPSErrors: true,
    viewport: { width: opts.width || 1440, height: opts.height || 900 },
    colorScheme: opts.dark === false ? 'light' : 'dark',
    deviceScaleFactor: opts.scale || 1,
    // Z8: Browser in einer anderen Zeitzone als der Server (Labor-Server
    // laufen auf UTC). LAB_TZ oder opts.tz, sonst die des Rechners.
    timezoneId: opts.tz || process.env.LAB_TZ || undefined,
  });
  ctx._browser = b;
  ctx._zone = z;
  return ctx;
}

async function close(ctx) { await ctx._browser.close(); }

// API-Aufrufe laufen über eine Seite im selben Kontext, damit sie dieselbe
// DNS-Zuordnung und dieselbe Anmeldung (Cookie) nutzen wie der Browser.
async function apiPage(ctx, host) {
  ctx._api = ctx._api || {};
  if (!ctx._api[host]) {
    const pg = await ctx.newPage();
    await pg.goto('https://' + host + '/manifest.json', { waitUntil: 'load' });
    ctx._api[host] = pg;
  }
  return ctx._api[host];
}

async function api(ctx, host, p, body) {
  const pg = await apiPage(ctx, host);
  return pg.evaluate(async ([p, body]) => {
    const opt = body === null ? { credentials: 'same-origin' } : { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) };
    let r;
    try { r = await fetch(p, opt); } catch (e) { return { ok: false, status: 0, data: { error: String(e) } }; }
    let data = null;
    const t = await r.text();
    try { data = JSON.parse(t); } catch (e) { data = { text: t.slice(0, 2000) }; }
    return { ok: r.ok, status: r.status, data };
  }, [p, body === undefined ? null : body]);
}

// Anmelden mit dem Leitstand-Passwort (wie das Formular, nur ohne Tippen).
async function login(ctx, host, pwFile) {
  const r = await api(ctx, host, '/api/anmelden', { passwort: secret(pwFile) });
  if (!r.ok) throw new Error('Anmelden an ' + host + ': ' + r.status + ' ' + JSON.stringify(r.data));
}

// Bestätigt eine Aktion mit dem Passwort (POST /api/bestaetigen).
async function confirm(ctx, host, pwFile) {
  const r = await api(ctx, host, '/api/bestaetigen', { passwort: secret(pwFile) });
  if (!r.ok) throw new Error('Bestätigen: ' + r.status + ' ' + JSON.stringify(r.data));
}

// Sammelt alle Statusmarken einer Seite: ls-st, Bänder, Abzeichen, Glocke,
// Kacheln. Jede mit Stufe und Text.
async function marks(page) {
  return page.evaluate(() => {
    const out = [];
    const txt = e => (e.innerText || e.textContent || '').replace(/\s+/g, ' ').trim();
    // Knöpfe einer Meldung stehen oft hinter einem langen Text.
    const knoepfe = e => { const b = [...e.querySelectorAll('a.btn, button.btn')].map(txt).filter(Boolean); return b.length ? ' [Knöpfe: ' + b.join(' | ') + ']' : ''; };
    const ctxOf = e => {
      const row = e.closest('tr, li, .ls-row, .ls-kpi, .ov-rw, .ov-pt, .it, .ls-pr, section');
      return row ? txt(row).slice(0, 200) : '';
    };
    for (const e of document.querySelectorAll('.ls-st')) {
      // Der Verlauf der Warnungen ist Geschichte, kein Zustand von jetzt.
      if (e.closest('.uw-ev')) continue;
      const lvl = ['err', 'warn', 'ok', 'idle'].find(c => e.classList.contains(c)) || '';
      out.push({ art: 'marke', stufe: lvl, text: txt(e), umfeld: ctxOf(e), titel: e.title || '' });
    }
    for (const e of document.querySelectorAll('.band')) {
      const lvl = e.classList.contains('b-err') ? 'err' : e.classList.contains('b-warn') ? 'warn' : 'info';
      out.push({ art: 'band', stufe: lvl, text: txt(e).slice(0, 300) });
    }
    // Übersicht: Zustand-Pillen, Meldung, Kacheln.
    for (const e of document.querySelectorAll('.ov-stb')) {
      const lvl = ['err', 'warn', 'ok', 'idle'].find(c => e.classList.contains(c)) || '';
      out.push({ art: 'marke', stufe: lvl, text: txt(e), umfeld: ctxOf(e) });
    }
    for (const e of document.querySelectorAll('.ov-alert')) {
      const lvl = ['err', 'warn', 'acc', 'info'].find(c => e.classList.contains(c)) || '';
      out.push({ art: 'braucht-dich', stufe: lvl, text: txt(e).slice(0, 300) + knoepfe(e) });
    }
    for (const e of document.querySelectorAll('.ov-st.warn, .ov-st.err')) out.push({ art: 'kachel', stufe: e.classList.contains('err') ? 'err' : 'warn', text: txt(e).slice(0, 200) });
    for (const e of document.querySelectorAll('.ov-pt.bad')) out.push({ art: 'projekt', stufe: e.classList.contains('err') ? 'err' : 'warn', text: txt(e).slice(0, 200) });
    for (const e of document.querySelectorAll('.ls-att-i')) {
      const lvl = ['err', 'warn', 'acc'].find(c => e.classList.contains(c)) || '';
      out.push({ art: 'braucht-dich', stufe: lvl, text: txt(e).slice(0, 300) + knoepfe(e) });
    }
    for (const e of document.querySelectorAll('.ls-cnt')) {
      const lvl = ['err', 'warn', 'acc'].find(c => e.classList.contains(c)) || '';
      out.push({ art: 'abzeichen', stufe: lvl, text: txt(e), umfeld: txt(e.closest('a') || e) });
    }
    for (const e of document.querySelectorAll('.ls-bell-n')) {
      out.push({ art: 'glocke', stufe: e.classList.contains('err') ? 'err' : 'warn', text: txt(e) });
    }
    for (const e of document.querySelectorAll('.ls-kpi')) {
      const lvl = ['err', 'warn'].find(c => e.classList.contains(c)) || '';
      if (lvl) out.push({ art: 'kachel', stufe: lvl, text: txt(e).slice(0, 200) });
    }
    for (const e of document.querySelectorAll('.it.dead')) out.push({ art: 'lageplan', stufe: e.classList.contains('warn') ? 'warn' : 'err', text: txt(e) });
    for (const e of document.querySelectorAll('#net.warn, #net.err')) out.push({ art: 'netz', stufe: e.classList.contains('err') ? 'err' : 'warn', text: txt(e) });
    for (const e of document.querySelectorAll('.ls-safe.warn, .ls-safe.err')) out.push({ art: 'sicherheitsnetz', stufe: e.classList.contains('err') ? 'err' : 'warn', text: txt(e).slice(0, 200) });
    for (const e of document.querySelectorAll('.pill.bad')) out.push({ art: 'pille', stufe: 'err', text: txt(e), umfeld: ctxOf(e) });
    const h1 = document.querySelector('h1');
    const kacheln = [...document.querySelectorAll('.ls-kpi, .ov-st')].map(txt);
    const status = document.querySelector('.ls-head .ls-status');
    const dot = status && status.querySelector('.ls-dot');
    const em = status ? [...status.querySelectorAll('em')].map(e => ['err', 'warn', 'ok'].find(c => e.classList.contains(c)) || '') : [];
    // Für die Regeln I10 bis I19: Zustände je Projekt und je Domain an
    // jeder Stelle (data-projekt, data-domain mit data-zustand), Uhrzeiten,
    // Kacheln mit Wert und Stufe.
    // Verbindungskarte unter Überwachung (früher Lageplan der Übersicht).
    const place = e => e.closest('.ls-plan, .vm-rows') ? 'lageplan' : e.closest('.ls-tl') ? 'nach-last' : e.closest('.uw-list') ? 'ueberwachung' : e.closest('.uw-t') ? 'seiten'
      : e.closest('.dm-t') ? 'domains' : e.closest('.ls-pt, .ov-pg') ? 'projekte' : 'sonst';
    const projekte = [];
    for (const e of document.querySelectorAll('[data-projekt]')) {
      // Zusammengefasste Projekte im Lageplan tragen das Wort selbst.
      const z = e.querySelector('.ls-st[data-zustand]') || (e.dataset.zustand != null ? e : null);
      projekte.push({ wo: place(e), projekt: e.dataset.projekt, gruppe: e.dataset.gruppe || '', zustand: z ? z.dataset.zustand : null, text: txt(e).slice(0, 160) });
    }
    const domains = [];
    for (const e of document.querySelectorAll('[data-domain]')) {
      const z = e.dataset.zustand != null ? e : e.querySelector('[data-zustand]');
      const pill = e.querySelector('.ls-st');
      const lvl = e.classList.contains('it') ? (e.classList.contains('dead') ? (e.classList.contains('warn') ? 'warn' : 'err') : e.classList.contains('quiet') ? 'idle' : 'ok')
        : pill ? ['err', 'warn', 'ok', 'idle'].find(c => pill.classList.contains(c)) || '' : '';
      domains.push({ wo: place(e), domain: e.dataset.domain, zustand: z ? z.dataset.zustand : null, stufe: lvl,
        balken: [...e.querySelectorAll('.ls-upb i')].map(i => i.className), verfuegbarkeit: (e.querySelector('[data-verfuegbarkeit]') || {}).dataset ? (e.querySelector('[data-verfuegbarkeit]') || { dataset: {} }).dataset.verfuegbarkeit : undefined });
    }
    // Verbindungskarte: ein Projekt-Knoten fasst seine Domains zusammen
    // (im Titel); jede zählt als Domain der Karte.
    for (const e of document.querySelectorAll('.vm-rows a.vm-n[data-projekt] b[title]')) {
      for (const d of e.title.split(', ').filter(Boolean)) domains.push({ wo: 'lageplan', domain: d, zustand: null, stufe: '', balken: [], verfuegbarkeit: undefined });
    }
    const zeiten = [...document.querySelectorAll('time[datetime]')].filter(e => e.offsetParent !== null || e.getClientRects().length).map(e => ({ datetime: e.getAttribute('datetime'), text: txt(e), umfeld: ctxOf(e) }));
    const kpis = [...document.querySelectorAll('.ls-kpi[data-kpi]')].map(e => ({ id: e.dataset.kpi, wert: e.dataset.wert != null ? Number(e.dataset.wert) : null, stufe: ['err', 'warn'].find(c => e.classList.contains(c)) || '', text: txt(e).slice(0, 200) }));
    const ereignisse = [...document.querySelectorAll('.uw-ev li')].map(e => ({ art: e.dataset.art || '', pause: e.dataset.pause === 'ja', stufe: (['err', 'warn', 'ok', 'idle'].find(c => (e.querySelector('.ls-st') || { classList: { contains: () => false } }).classList.contains(c)) || ''), text: txt(e) }));
    const leer = [...document.querySelectorAll('.ls-empty.ok')].map(txt);
    const seitenSub = (() => { const h = [...document.querySelectorAll('.ls-sec h2')].find(x => txt(x) === 'Seiten'); const sub = h && h.parentElement.querySelector('.sub'); return sub ? txt(sub) : ''; })();
    return { titel: h1 ? txt(h1) : '', status: status ? txt(status) : '', kopf: dot ? (['err', 'warn', 'ok'].find(c => dot.classList.contains(c)) || '') : em.find(x => x === 'err') || em.find(x => x === 'warn') || (em.length ? 'ok' : ''),
      marken: out, kacheln, projekte, domains, zeiten, kpis, ereignisse, leer, seitenSub };
  });
}

// Geht zu einer Seite des Leitstands und wartet, bis sie steht.
async function goto(page, host, hash, waitMs = 2500) {
  const url = 'https://' + host + '/' + hash;
  if (page.url().startsWith('https://' + host + '/')) await page.evaluate(h => { location.hash = h; }, hash);
  else await page.goto(url, { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(waitMs);
}

function sleep(ms) { return new Promise(r => setTimeout(r, ms)); }

module.exports = { zone, secret, context, close, login, api, confirm, marks, goto, sleep, LAUF, launch };

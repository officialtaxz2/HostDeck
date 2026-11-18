// hostdeck serve: Stand, Router und die Auswahl (Fragen und Kisten). Die Seiten des
// Leitstands zeichnet leitstand.js mit demselben Stand. Alle Daten kommen von
// /api/state und werden nur als Text in die Seite eingesetzt (textContent),
// nie als HTML. Stile nur über Klassen oder element.style (CSSOM);
// style-Attribute verbietet die CSP.
'use strict';
(() => {
  // ---------- Wörter ----------
  const KISTEN = {
    open: ['Noch offen', 'Muss vor dem Backup entschieden werden.'],
    take: ['Übernehmen', 'Kommt auf dem neuen Server wieder an seinen Platz.'],
    archive: ['Archivieren', 'Liegt im Backup, kommt aber nicht von selbst zurück.'],
    skip: ['Weglassen', 'Wird nicht gesichert. Jeder Eintrag hat einen Grund.'],
  };
  const TILE = {
    take: ['Übernehmen', 'Kommt auf dem neuen Server wieder an seinen Platz.'],
    archive: ['Archivieren', 'Liegt im Backup, kommt nicht von selbst zurück.'],
    skip: ['Weglassen', 'Wird nicht gesichert, mit kurzem Grund.'],
  };
  const TILE_PROJECT = {
    take: ['Übernehmen', 'Wird gesichert und auf dem neuen Server gestartet.'],
    archive: ['Archivieren', 'Wird gesichert, aber nicht gestartet.'],
    skip: ['Weglassen', 'Wird nicht gesichert, mit kurzem Grund.'],
  };
  const STRATEGIES = {
    'dump': ['Datenbank-Dump', 'Dump der laufenden Datenbank; im letzten Lauf zusätzlich eine Kopie im gestoppten Zustand'],
    'stop-copy': ['Kopie bei gestopptem Container', 'Container kurz stoppen, kopieren, wieder starten'],
    'live-copy': ['Kopie im laufenden Betrieb', 'für Dateien, die sich selten ändern'],
    'rebuild': ['Nicht sichern, entsteht neu', 'für Caches, die sich von selbst wieder füllen'],
    'skip': ['Weglassen', 'wird nicht gesichert'],
    'archive': ['Archivieren', 'liegt im Backup, wird nicht zurückgespielt'],
    'open': ['Noch offen', 'muss vor dem Backup entschieden werden'],
  };
  const IMAGES = {
    'image-save': ['Image mitnehmen', 'genau dieses Image per docker save sichern'],
    'rebuild': ['Aus der Registry laden', 'auf dem neuen Server per docker pull holen'],
    'skip': ['Dienst weglassen', 'dieser Dienst kommt nicht mit'],
  };
  const ENGINES = { postgres: 'PostgreSQL', mysql: 'MySQL', mariadb: 'MariaDB', mongo: 'MongoDB', redis: 'Redis', valkey: 'Valkey', sqlite: 'SQLite' };
  const DUMP = new Set(['postgres', 'mysql', 'mariadb', 'mongo']);
  // Routen-Status aus der Bestandsaufnahme. Er ist nie der aktuelle Stand:
  // Ob ein gestoppter Container gewollt aus ist, weiß nur der Dienst.
  const ROUTE = {
    'ok': ['erreichbar', 'ok'],
    'container-gestoppt': ['Ziel war aus', 'muted'],
    'nichts-lauscht': ['zeigte ins Leere', 'bad'],
    'extern': ['extern', 'muted'],
    'kein-proxy': ['keine Weiterleitung', 'muted'],
  };
  // Zustand der Domains vom Dienst (eine Quelle), nach Domain. Ohne ihn
  // gilt der Stand der Bestandsaufnahme, so benannt.
  let domStates = null;
  const Z_PILL = { ok: 'ok', idle: 'muted', warn: 'bad', err: 'bad' };
  function routeInfo(r) {
    const z = domStates ? r.hosts.map(h => domStates.get(h)).find(Boolean) : null;
    if (z) return { word: z.wort, cls: Z_PILL[z.stufe] || 'muted', note: z.text || '', live: true, bad: z.stufe === 'err' || z.stufe === 'warn' };
    const st = ROUTE[r.status] || [r.status, 'muted'];
    return { word: st[0], cls: st[1], note: 'Stand der Bestandsaufnahme', live: false, bad: st[1] === 'bad' };
  }
  // „, 1 davon zeigt ins Leere“, mit dem Stand, aus dem die Zahl kommt.
  function deadText(routes, lead) {
    const info = routes.map(routeInfo);
    const n = info.filter(i => i.bad).length;
    const live = info.some(i => i.live);
    if (!n) return lead ? (live ? 'alle antworten oder sind pausiert' : 'keine zeigte ins Leere') : '';
    const t = live ? (n === 1 ? '1 davon antwortet nicht' : n + ' davon antworten nicht') : (n === 1 ? '1 davon zeigte bei der Bestandsaufnahme ins Leere' : n + ' davon zeigten bei der Bestandsaufnahme ins Leere');
    return lead ? t : ', ' + t;
  }
  const SPLIT_REASON = 'nicht benötigt';
  const AREAS = { home: 'Home-Ordner', web: 'Webseite', programme: 'Programme', einstellungen: 'Einstellungen', fund: 'außerhalb gefunden' };
  const TASK_KINDS = { cron: 'Cron', timer: 'Timer', dienst: 'Dienst' };
  const BEREICHE = [
    ['projekte', 'Projekte'], ['daten', 'Daten ohne Projekt'], ['server', 'Am Server selbst'],
    ['caddy', 'Caddy'], ['warnungen', 'Warnungen'], ['neuer-server', 'Für den neuen Server'],
  ];
  const NEED_REASON = 'Bitte kurz einen Grund angeben, ein paar Worte reichen.';
  const PER_KISTE = 8;
  const BIG = 200e6;

  let server = null; // Stand vom Server
  let work = null;   // bearbeitete Kopie
  let byKey = new Map();
  let serverKey = new Map();
  let questions = [];
  let saving = false;
  let LS = null; // Leitstand (leitstand.js)
  let svc = null; // Stand des Dienstes (nur bei hostdeck web), sonst null
  // Kein Umzug geplant: die Auswahl entscheidet nur, was ins Backup kommt.
  const alltag = () => !!(svc && svc.alltag);
  const ids = new Map();
  const ui = {
    answered: new Set(), expanded: new Set(), showAll: new Set(), addOpen: new Set(), errs: new Set(),
    search: {}, sort: 'size', grouped: true, tab: {}, menu: null, form: null,
    saveError: null, returnFid: null, prev: null,
  };

  // ---------- Hilfen ----------

  function el(tag, props, ...kids) {
    const e = document.createElement(tag);
    for (const [k, v] of Object.entries(props || {})) {
      if (v === null || v === undefined || v === false) continue;
      if (k === 'class') e.className = v;
      else if (k === 'text') e.textContent = v;
      else if (k.startsWith('on')) e.addEventListener(k.slice(2), v);
      else if (k === 'checked' || k === 'selected' || k === 'disabled' || k === 'open' || k === 'hidden') e[k] = Boolean(v);
      else if (k === 'value') e.value = v;
      else e.setAttribute(k, v === true ? '' : String(v));
    }
    return put(e, kids);
  }

  // put hängt nur vorhandene Kinder an; Node.append(null) schriebe „null“.
  function put(parent, ...kids) {
    for (const k of kids.flat(Infinity)) {
      if (k === null || k === undefined || k === false) continue;
      parent.append(k instanceof Node ? k : document.createTextNode(String(k)));
    }
    return parent;
  }

  const SVG = 'http://www.w3.org/2000/svg';
  const ICONS = {
    check: ['m5 12 5 5 9-10', 2.6],
    down: ['m6 9 6 6 6-6', 2.4],
    right: ['m9 6 6 6-6 6', 2.4],
    left: ['M19 12H5M11 6l-6 6 6 6', 2.4],
    next: ['M5 12h14M13 6l6 6-6 6', 2.4],
    warn: ['M12 3 2 20h20L12 3zM12 10v4M12 17v.5', 2.4],
    x: ['M6 6l12 12M18 6 6 18', 2.4],
    shield: ['M12 3 4 6v6c0 4.5 3.4 8 8 9 4.6-1 8-4.5 8-9V6l-8-3z', 2.2],
    refresh: ['M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8M21 3v5h-5M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16M8 16H3v5', 2],
  };
  function icon(name, px) {
    const s = document.createElementNS(SVG, 'svg');
    const n = String(px || 16);
    for (const [k, v] of [['width', n], ['height', n], ['viewBox', '0 0 24 24'], ['fill', 'none'], ['stroke', 'currentColor'],
      ['stroke-linecap', 'round'], ['stroke-linejoin', 'round'], ['aria-hidden', 'true'], ['focusable', 'false']]) s.setAttribute(k, v);
    if (name === 'theme') {
      const c = document.createElementNS(SVG, 'circle');
      for (const [k, v] of [['cx', '12'], ['cy', '12'], ['r', '8.5'], ['stroke-width', '2.2']]) c.setAttribute(k, v);
      const p = document.createElementNS(SVG, 'path');
      p.setAttribute('d', 'M12 3.5a8.5 8.5 0 0 1 0 17z');
      p.setAttribute('fill', 'currentColor');
      p.setAttribute('stroke', 'none');
      s.append(c, p);
      return s;
    }
    const [d, w] = ICONS[name];
    s.setAttribute('stroke-width', String(w));
    const p = document.createElementNS(SVG, 'path');
    p.setAttribute('d', d);
    s.append(p);
    return s;
  }

  function idFor(key) {
    if (!ids.has(key)) ids.set(key, 'k' + ids.size);
    return ids.get(key);
  }

  // Wie units.HumanSize in Go: Dezimal-Einheiten, deutsches Komma.
  function size(b) {
    if (b === undefined || b === null || b < 0) return '?';
    const units = ['B', 'kB', 'MB', 'GB', 'TB'];
    let f = b, i = 0;
    while (f >= 1000 && i < units.length - 1) { f /= 1000; i++; }
    let s;
    if (i === 0) s = String(b);
    else if (f >= 100) s = f.toFixed(0);
    else if (f >= 10) s = f.toFixed(1);
    else s = f.toFixed(2);
    // Geschütztes Leerzeichen: Zahl und Einheit brechen nie auseinander.
    return s.replace('.', ',') + '\u00a0' + units[i];
  }

  // Summe mit unbekannten Größen (-1): „mind.“ davor.
  function sum(list, f) {
    let n = 0, unknown = false;
    for (const x of list) { const v = f ? f(x) : x.size; if (v < 0) unknown = true; else n += v; }
    return { n, unknown };
  }
  function sizeSum(s) { return (s.unknown ? 'mind. ' : '') + size(s.n); }

  // Server-Namen von Wings sind UUIDs; die ersten acht Zeichen reichen.
  function shortName(n) { return /^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(n) ? n.slice(0, 8) + '…' : n; }
  function plural(n, one, many) { return n + ' ' + (n === 1 ? one : many); }
  function shortList(a, max) {
    return a.length <= max ? a.join(', ') : a.slice(0, max).join(', ') + ' und ' + (a.length - max) + ' weitere';
  }
  function andList(a) { return a.length < 2 ? a.join('') : a.slice(0, -1).join(', ') + ' und ' + a[a.length - 1]; }
  function cap(s) { return s ? s[0].toUpperCase() + s.slice(1) : s; }

  // Gleichartige Ordner: gleicher Elternordner und gleicher Namensanfang bis
  // zum ersten -, _ oder . (wie in discover). '' wenn der Name keinen hat.
  function siblingKey(p) {
    const i = p.lastIndexOf('/');
    const m = /^[^-_.]+[-_.]/.exec(p.slice(i + 1));
    return m ? p.slice(0, i + 1) + m[0] : '';
  }

  function clone(o) { return JSON.parse(JSON.stringify(o)); }

  function domainLink(d) {
    if (/^[a-z0-9.-]+\.[a-z]{2,}$/i.test(d)) return el('a', { href: 'https://' + d, target: '_blank', rel: 'noopener noreferrer' }, d);
    return d;
  }

  function engineText(d) {
    if (!d.engine) return '';
    let t = ENGINES[d.engine] || d.engine;
    if (d.engine_version) t += ' ' + d.engine_version;
    return t;
  }

  function fmtDate(s) {
    const d = new Date(s);
    if (isNaN(d)) return '';
    return d.toLocaleString('de-DE', { day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit' });
  }

  function host() { return server.host.hostname || 'dem Server'; }
  function needsReason(v) { return v === 'skip'; }
  function hasReason(o) { return !!(o.reason || '').trim(); }

  // ---------- Server ----------

  // Angemeldet ist die Seite über ein Cookie (HttpOnly); der Link aus dem
  // Terminal wird einmal dagegen getauscht und danach aus der Adresse
  // entfernt. bg: Abfrage im Hintergrund, zählt nicht als Aktivität.
  // Fehlt die Anmeldung oder will der Dienst den Schritt mit dem Passwort
  // bestätigt haben, fragt die Seite danach und schickt die Anfrage dann
  // noch einmal; once: nicht nachfragen (Anmelden und Bestätigen selbst).
  async function call(path, body, bg, once) {
    const opt = { headers: {}, credentials: 'same-origin' };
    if (bg) opt.headers['X-Hostdeck-Hintergrund'] = '1';
    if (body) {
      opt.method = 'POST';
      opt.headers['Content-Type'] = 'application/json';
      opt.body = JSON.stringify(body);
    }
    let r;
    try {
      r = await fetch(path, opt);
    } catch (e) {
      return { ok: false, status: 0, data: { error: 'Keine Verbindung zu HostDeck. Läuft es noch, und ist der SSH-Tunnel offen?' } };
    }
    let data = null;
    try { data = await r.json(); } catch (e) { data = { error: 'Antwort nicht lesbar (' + r.status + ')' }; }
    const res = { ok: r.ok, status: r.status, data };
    if (once || !LS || !data) return res;
    if (r.status === 401 && data.anmelden) {
      if (!(await LS.signIn())) return res;
      return call(path, body, bg, true);
    }
    if (r.status === 403 && data.bestaetigen) {
      if (!(await LS.confirmStep(path))) return { ok: false, status: 403, data: { error: 'Nicht bestätigt.', abgebrochen: true } };
      return call(path, body, bg, true);
    }
    return res;
  }

  // quiet: neu laden, ohne die Seite zu wechseln oder den Fokus zu bewegen.
  async function load(quiet) {
    const r = await call('/api/state');
    if (!r.ok) {
      fatal(r.data && r.data.error ? r.data.error : 'Fehler ' + r.status);
      return false;
    }
    server = r.data;
    work = clone(server);
    ui.answered.clear();
    ui.errs.clear();
    ui.saveError = null;
    reindex();
    questions = computeQuestions();
    const h = location.hash;
    if (!h.startsWith('#/')) history.replaceState(null, '', '#/lage');
    if (quiet) rerender();
    else render(true);
    return true;
  }

  async function save() {
    if (!work || saving) return;
    const errs = validate();
    if (errs.length) { reasonMissing(errs); return; }
    saving = true;
    const btn = document.getElementById('save');
    btn.disabled = true;
    btn.setAttribute('aria-busy', 'true');
    btn.textContent = 'Speichert …';
    setStatus('Speichert …', '');
    const r = await call('/api/save', { version: server.version, edits: edits() });
    saving = false;
    btn.removeAttribute('aria-busy');
    btn.textContent = 'Speichern';
    if (r.data && r.data.abgebrochen) { updateStatus(); return; }
    if (r.ok) {
      server = r.data;
      work = clone(server);
      ui.saveError = null;
      ui.errs.clear();
      reindex();
      rerender();
      toast('Gespeichert in manifest.yaml');
      return;
    }
    const d = r.data || {};
    if (r.status === 409) ui.saveError = { text: d.error || 'manifest.yaml hat sich inzwischen geändert.', reload: true };
    else if (r.status === 422) ui.saveError = { text: d.error || 'Nicht gespeichert:', problems: d.problems || [] };
    else ui.saveError = { text: 'Speichern ging nicht: ' + (d.error || 'Fehler ' + r.status) + '. Deine Änderungen sind noch da.', retry: true };
    rerender();
    const band = document.getElementById('save-error');
    if (band) band.scrollIntoView({ block: 'center' });
  }

  function reload() {
    if (work && edits().length > 0 && !confirm('Ungespeicherte Änderungen verwerfen und neu laden?')) return;
    load();
  }

  // ---------- Dienst ----------

  // Als Dienst (hostdeck web) gibt es /api/lauf: welcher Lauf gerade die Sperre
  // hält und wie es um die Bestandsaufnahme steht. Bei hostdeck serve fehlt es
  // (404); dann bleibt svc leer und die Seite nennt die Befehle wie bisher.
  // Die Abfragen laufen im Hintergrund und halten die Anmeldung nicht wach.
  const svcUI = { timer: 0, seen: null, starting: false, error: '', off: false };

  async function refreshLauf() {
    clearTimeout(svcUI.timer);
    if (svcUI.off) return;
    const r = await call('/api/lauf', null, true);
    if (r.status === 404) { svc = null; svcUI.off = true; return; }
    if (r.status === 401) {
      svcUI.off = true;
      fatal(r.data && r.data.error ? r.data.error : 'Du bist abgemeldet. Einen neuen Link gibt es auf dem Server mit: sudo hostdeck notausgang');
      return;
    }
    if (!r.ok) { svcUI.timer = setTimeout(refreshLauf, 15000); return; }
    const before = JSON.stringify(svc);
    svc = r.data;
    const b = svc.bestandsaufnahme || {};
    const last = b.zuletzt || '';
    if (svcUI.seen === null) svcUI.seen = last;
    if (!b.laeuft && last !== svcUI.seen) {
      // Eine Bestandsaufnahme ist fertig geworden (per Knopf oder nachts).
      svcUI.seen = last;
      if (b.fehler) announce('Die Bestandsaufnahme ist fehlgeschlagen.');
      else if (work && edits().length > 0) toast('Neue Bestandsaufnahme fertig. Lade neu, sobald du mit der Auswahl fertig bist.');
      else if (await load(true)) { toast('Neue Bestandsaufnahme fertig, die Seite zeigt den neuen Stand.'); }
    } else if (JSON.stringify(svc) !== before && work) rerender();
    svcUI.timer = setTimeout(refreshLauf, b.laeuft || svc.lauf ? 3000 : 60000);
  }

  async function startDiscover() {
    if (!svc || svcUI.starting) return;
    svcUI.starting = true;
    svcUI.error = '';
    rerender();
    const r = await call('/api/bestandsaufnahme', {});
    svcUI.starting = false;
    if (r.status === 202) {
      svc = r.data;
      announce('Bestandsaufnahme läuft');
    } else {
      svcUI.error = (r.data && r.data.error) || 'Neu erfassen ging nicht (' + r.status + ').';
    }
    rerender();
    refreshLauf();
  }

  // Knopf „Jetzt neu erfassen“ samt Grund, falls er gerade nicht geht.
  function discoverCtl(cls, label) {
    if (!svc) return null;
    const b = svc.bestandsaufnahme || {};
    const running = b.laeuft || svcUI.starting;
    let why = '';
    if (!running && svc.lauf) why = 'Geht erst, wenn „' + svc.lauf.was + '“ fertig ist.';
    else if (!running && work && edits().length > 0) why = 'Erst speichern, dann neu erfassen.';
    const btn = el('button', {
      class: 'btn btn-sm ' + (cls || ''), type: 'button', onclick: startDiscover,
      disabled: running || !!why, 'aria-busy': running ? 'true' : null,
    }, icon('refresh', 16), running ? 'Erfasst gerade …' : label || 'Jetzt neu erfassen');
    return { btn, why };
  }

  // Wie man die Bestandsaufnahme erneuert: als Dienst per Knopf, sonst per Befehl.
  function discoverHint() {
    const c = discoverCtl('btn-warn');
    if (!c) return null;
    return el('div', { class: 'acts' }, c.btn, c.why ? el('span', { class: 'why' }, c.why) : null);
  }

  // ---------- Änderungen ----------

  function walk(state, fn) {
    for (const p of state.projects) {
      fn('decision', p, p);
      for (const s of p.services) fn('image', s, p);
      for (const d of p.data) fn('data', d, p);
    }
    for (const c of state.containers) {
      fn('decision', c, c);
      for (const d of c.data) fn('data', d, c);
    }
    for (const d of state.loose) fn('data', d, null);
    if (state.caddy) fn('decision', state.caddy, null);
  }

  function caddyKey(o) { return o.key || 'host:caddy'; }

  function reindex() {
    byKey = new Map();
    walk(work, (kind, o, owner) => byKey.set(caddyKey(o), { kind, o, owner }));
    serverKey = new Map();
    walk(server, (kind, o) => serverKey.set(caddyKey(o), o));
  }

  function edits() {
    const before = new Map();
    walk(server, (kind, o) => before.set(caddyKey(o), o));
    const out = [];
    walk(work, (kind, o) => {
      const key = caddyKey(o);
      const b = before.get(key);
      if (kind === 'data' && o.added) {
        if (!o.removed) out.push({ key, split_of: o.split_of, strategy: o.strategy, reason: o.reason });
        return;
      }
      if (!b) return;
      if (o.removed) { out.push({ key, remove: true }); return; }
      const e = { key };
      let changed = false;
      if (kind === 'decision' && o.decision !== b.decision) { e.decision = o.decision; changed = true; }
      if (kind === 'image' && o.image !== b.image) { e.image = o.image; changed = true; }
      if (kind === 'data' && o.strategy !== b.strategy) { e.strategy = o.strategy; changed = true; }
      if (kind !== 'image' && (o.reason || '') !== (b.reason || '')) { e.reason = o.reason || ''; changed = true; }
      if (changed) out.push(e);
    });
    for (const g of work.gone) if (g.remove) out.push({ key: g.key, remove: true });
    return out;
  }

  function mustSave() {
    return server && (server.manifest.state !== 'vorhanden' || server.manifest.added.length > 0);
  }

  // Einträge, die weggelassen werden, aber noch keinen Grund haben.
  function validate() {
    const errs = [];
    walk(work, (kind, o, owner) => {
      if (o.removed) return;
      const v = kind === 'decision' ? o.decision : kind === 'data' ? o.strategy : null;
      if (needsReason(v) && !hasReason(o)) errs.push({ kind, o, owner });
    });
    return errs;
  }

  // ---------- Zählen ----------

  function isOpenEntry(kind, o, owner) {
    if (o.removed) return false;
    if (kind === 'decision') return o.decision === 'open';
    if (kind === 'data') {
      if (owner && owner.decision === 'skip') return false;
      return o.strategy === 'open';
    }
    return false;
  }

  function openList() {
    const out = [];
    walk(work, (kind, o, owner) => { if (isOpenEntry(kind, o, owner)) out.push({ kind, o, owner }); });
    return out;
  }

  function ownerOpen(owner) {
    let n = owner.decision === 'open' ? 1 : 0;
    for (const d of owner.data || []) if (isOpenEntry('data', d, owner)) n++;
    return n;
  }

  // Größen: Projektordner und Daten der übernommenen oder archivierten
  // Einträge, abzüglich ausgenommener Unterordner, plus mitgenommene Images.
  function totals(state) {
    const t = { take: 0, archive: 0, unknown: false };
    const images = new Map();
    const add = (bucket, n) => { if (n < 0) t.unknown = true; else t[bucket] += n; };
    const skipped = list => {
      const m = new Map();
      for (const d of list) {
        if (d.split_of && !d.removed && (d.strategy === 'skip' || d.strategy === 'rebuild') && d.size > 0) {
          m.set(d.split_of, (m.get(d.split_of) || 0) + d.size);
        }
      }
      return m;
    };
    const dataSizes = (bucket, list) => {
      const sk = skipped(list);
      for (const d of list) {
        if (d.split_of || d.removed) continue;
        if (['skip', 'rebuild', 'open'].includes(d.strategy)) continue;
        add(bucket, d.size < 0 ? -1 : Math.max(0, d.size - (sk.get(d.key) || 0)));
      }
      return sk;
    };
    for (const p of state.projects) {
      if (p.decision !== 'take' && p.decision !== 'archive') continue;
      const sk = dataSizes(p.decision, p.data);
      if (p.dir_exists) add(p.decision, p.size < 0 ? -1 : Math.max(0, p.size - (sk.get(p.key) || 0)));
      for (const s of p.services) {
        if (s.image === 'image-save' && s.image_size >= 0 && !images.has(s.image_id)) images.set(s.image_id, [p.decision, s.image_size]);
      }
    }
    for (const c of state.containers) {
      if (c.decision === 'take' || c.decision === 'archive') dataSizes(c.decision, c.data);
    }
    const sk = skipped(state.loose);
    for (const d of state.loose) {
      if (d.split_of || d.removed || ['skip', 'open', 'rebuild'].includes(d.strategy)) continue;
      add(d.strategy === 'archive' ? 'archive' : 'take', d.size < 0 ? -1 : Math.max(0, d.size - (sk.get(d.key) || 0)));
    }
    for (const [bucket, n] of images.values()) t[bucket] += n;
    return t;
  }

  // Images der übernommenen und archivierten Projekte, wie in totals gezählt.
  function imageStats() {
    const saved = new Map();
    const pulled = new Set();
    for (const p of work.projects) {
      if (p.decision !== 'take' && p.decision !== 'archive') continue;
      for (const s of p.services) {
        if (s.image === 'image-save' && s.image_size >= 0 && !saved.has(s.image_id)) saved.set(s.image_id, [p.decision, s.image_size]);
        if (s.image === 'rebuild') pulled.add(s.image_name);
      }
    }
    const r = { take: 0, archive: 0, takeN: 0, pulled: pulled.size };
    for (const [bucket, n] of saved.values()) { r[bucket] += n; if (bucket === 'take') r.takeN++; }
    return r;
  }

  // Umzugsleiste: was mitkommt, was im Archiv liegt, was zurückbleibt.
  function moveSummary() {
    const t = totals(work);
    const img = imageStats();
    const sp = server.space;
    const m = { take: t.take, archive: t.archive, unknown: t.unknown, img, left: null, regrow: null, archived: 0 };
    if (sp && sp.total) {
      m.left = Math.max(0, sp.data + sp.volumes - (t.take - img.take) - (t.archive - img.archive));
      m.regrow = Math.max(0, sp.docker - img.take - img.archive) + sp.system + sp.swap;
    }
    for (const p of work.projects) if (p.decision === 'archive') m.archived++;
    for (const c of work.containers) if (c.decision === 'archive') m.archived++;
    for (const d of work.loose) if (!d.split_of && !d.removed && d.strategy === 'archive') m.archived++;
    return m;
  }

  // Was ein Projekt mitbringt: Ordner, Daten und mitgenommene Images.
  function projectBytes(p) {
    const t = totals({ projects: [Object.assign({}, p, { decision: 'take' })], containers: [], loose: [] });
    return t.unknown ? -1 : t.take;
  }
  function itemBytes(it) {
    if (it.kind === 'project') return projectBytes(it.o);
    if (it.kind === 'container') return sum(it.o.data.filter(d => !d.split_of)).n;
    return it.o.size;
  }

  // Daten ohne Projekt kennen nur drei Wahlen; die Strategie dahinter
  // bleibt erhalten, solange sie passt.
  function looseValue(d) {
    return ['archive', 'skip', 'open'].includes(d.strategy) ? d.strategy : 'take';
  }

  function setLoose(d, v) {
    if (v === 'archive' || v === 'skip') d.strategy = v;
    else if (!['stop-copy', 'live-copy', 'dump'].includes(d.strategy)) d.strategy = 'live-copy';
  }

  function dataOptions(d) {
    const o = [];
    if (DUMP.has(d.engine)) o.push('dump');
    o.push('stop-copy', 'live-copy', 'rebuild', 'skip');
    if (!o.includes(d.strategy)) o.push(d.strategy);
    return o;
  }

  // Hinweise aus dem Manifest ohne das, was die Seite ohnehin zeigt
  // (Version, SQLite-Dateien, größte Unterordner).
  function dataNote(d) {
    if (!d.note) return '';
    return d.note.split('; ').filter(part =>
      part !== size(d.size).replace('\u00a0', ' ') &&
      !(d.git && part.startsWith('Git: ')) &&
      !(d.orphan && part === 'von keinem Container genutzt') &&
      !(d.former_project && part === 'gehörte zu ' + d.former_project) &&
      part !== 'gehört zu keinem laufenden Projekt' &&
      !(d.engine_version && part === 'Version ' + d.engine_version) &&
      !(d.sqlite && d.sqlite.length && part.startsWith('SQLite: ')) &&
      !(d.largest && d.largest.length && (part.startsWith('größte Unterordner:') || part.startsWith('einzelne lassen sich')))).join('; ');
  }

  function dataFacts(d) {
    const f = [];
    if (d.type === 'volume') f.push('Volume');
    else if (d.type === 'anon') f.push('anonymes Volume');
    else f.push(d.kind === 'config' ? 'Datei oder Konfiguration' : 'Ordner');
    f.push(size(d.size));
    if (d.area) f.push(AREAS[d.area] || d.area);
    if (d.engine) f.push(engineText(d));
    if (d.users && d.users.length) f.push((d.running ? 'genutzt von ' : 'gestoppt: ') + d.users.join(', '));
    if (d.orphan) f.push('von keinem Container genutzt');
    if (d.former_project) f.push('gehörte zu ' + d.former_project);
    if (d.sqlite && d.sqlite.length) f.push('SQLite: ' + shortList(d.sqlite, 3));
    if (d.git) f.push('Git' + (d.git.remote ? ': ' + d.git.remote : '') + (d.git.dirty ? ' (' + plural(d.git.dirty, 'Änderung', 'Änderungen') + ' offen)' : ''));
    if (!d.exists) f.push('nicht gefunden');
    return f.join(' · ');
  }

  function originText(s) {
    if (s.origin === 'unklar') return 'Herkunft unklar';
    if (s.origin === 'gebaut' || (!s.origin && s.local)) return 'hier gebaut';
    return s.source ? 'aus ' + s.source : 'aus einer Registry';
  }

  function projectWarnings(p) {
    const w = [];
    if (p.decision !== 'take') return w;
    for (const s of p.services) {
      if (s.image === 'skip') w.push('Dienst ' + s.name + ' kommt nicht mit, der Rest des Projekts schon.');
      if (s.image === 'rebuild' && s.origin === 'gebaut') w.push('Das Image von ' + s.name + ' ist auf diesem Server gebaut; aus einer Registry lässt es sich vermutlich nicht laden.');
    }
    for (const d of p.data) {
      if (d.split_of || d.removed) continue;
      if (d.strategy === 'skip') w.push(d.label + ' wird weggelassen, obwohl das Projekt übernommen wird.');
      if (d.strategy === 'rebuild' && DUMP.has(d.engine)) w.push('Die Datenbank ' + d.label + ' wird nicht gesichert.');
    }
    for (const m of p.managed) {
      if (m.path && managedSkip(m, p.data)) w.push('Server ' + shortName(m.name) + ' wird nicht gesichert.');
    }
    if (!p.dir_exists) w.push('Der Compose-Ordner fehlt; ohne ihn lässt sich das Projekt auf dem neuen Server nicht starten.');
    return w;
  }

  // Ordner-Eintrag, mit dem die Daten eines verwalteten Servers gesichert
  // werden, und der Eintrag, der sie davon ausnimmt.
  function managedParent(m, list) {
    let best = null;
    for (const d of list) {
      if (d.type !== 'path' || d.split_of || d.removed) continue;
      if (m.path.startsWith(d.label + '/') && (!best || d.label.length > best.label.length)) best = d;
    }
    return best;
  }

  function managedSkip(m, list) {
    return list.find(x => x.key === 'path:' + m.path && x.split_of && !x.removed && x.strategy === 'skip');
  }

  // ---------- Gruppen nach Herkunft ----------

  // Gleiches früheres Projekt oder bei Volumes der gleiche Namensanfang bis
  // zum ersten _ (Compose benennt Volumes so); bei Ordnern Elternordner und
  // Namensanfang wie in discover.
  function originKey(d) {
    if (d.former_project) return 'p:' + d.former_project;
    if (d.type === 'volume') { const i = d.label.indexOf('_'); return i > 0 ? 'p:' + d.label.slice(0, i) : ''; }
    if (d.type === 'path') { const k = siblingKey(d.label); return k ? 's:' + k : ''; }
    return '';
  }
  function groupName(k) { return k === 'o:' ? 'Weitere offene Einträge' : k[0] === 's' ? k.slice(2) + '…' : k.slice(2); }
  function groupKind(k, list) {
    if (k[0] === 's') return 'gemeinsamer Ordner';
    return list.some(d => d.former_project) ? 'früheres Projekt' : 'gleicher Namensanfang';
  }
  function memberLabel(k, d) {
    if (k === 'o:') return d.label;
    const base = k.slice(2);
    if (k[0] === 's') return '…' + d.label.slice(base.lastIndexOf('/'));
    return d.label.startsWith(base) && d.label.length > base.length ? '…' + d.label.slice(base.length) : d.label;
  }
  function memberNoun(members) {
    const t = new Set(members.map(d => d.type === 'path' ? 'path' : 'volume'));
    if (t.size > 1) return ['Eintrag', 'Einträge'];
    return t.has('path') ? ['Ordner', 'Ordner'] : ['Volume', 'Volumes'];
  }

  // Einheiten für Kisten und Fragen: Gruppen ab zwei Einträgen, sonst einzeln.
  function units(list, grouped) {
    if (!grouped) return list.map(d => ({ d }));
    const by = new Map();
    for (const d of list) {
      const k = originKey(d);
      if (k) { if (!by.has(k)) by.set(k, []); by.get(k).push(d); }
    }
    const out = [];
    const done = new Set();
    for (const d of list) {
      const k = originKey(d);
      if (!k || by.get(k).length < 2) { out.push({ d }); continue; }
      if (done.has(k)) continue;
      done.add(k);
      out.push({ id: 'grp:' + k, k, members: by.get(k) });
    }
    return out;
  }
  function unitSize(u) { return u.members ? sum(u.members) : sum([u.d]); }

  function looseOf(area) { return work.loose.filter(d => !d.split_of && (area === 'server' ? !!d.area : !d.area)); }

  // ---------- Fragen ----------

  // Einmal nach dem Laden berechnet; während der Sitzung bleibt die Liste
  // fest, damit die Nummern nicht springen.
  function computeQuestions() {
    const qs = [];
    const asked = new Set();
    const bySize = (a, b) => b.size - a.size;
    const one = (x, tag) => { const key = caddyKey(x.o); asked.add(key); qs.push({ type: 'one', key, tag, id: 'q:' + key }); };
    const open = openList().map(x => Object.assign(x, { size: x.kind === 'decision' && x.owner === x.o && x.o.services ? projectBytes(x.o) : (x.o.size || 0) }));
    open.sort(bySize);
    // Offene lose Einträge kommen gruppiert und höchstens als 8 Fragen;
    // was danach noch offen ist, steht gesammelt in der letzten davon.
    const openLoose = [];
    for (const x of open) {
      if (x.kind === 'data' && !x.owner) openLoose.push(x.o);
      else one(x, 'open');
    }
    const ous = units(openLoose, true).map(u => Object.assign(u, { size: unitSize(u).n })).sort(bySize);
    for (const u of ous.length > PER_KISTE ? ous.slice(0, PER_KISTE - 1) : ous) {
      if (!u.members) { one({ o: u.d }, 'open'); continue; }
      for (const d of u.members) asked.add(d.key);
      qs.push({ type: 'group', id: 'open:' + u.id, gid: u.id, k: u.k, keys: u.members.map(d => d.key), tag: 'open' });
    }
    const openRest = openLoose.filter(d => !asked.has(d.key));
    if (openRest.length === 1) one({ o: openRest[0] }, 'open');
    else if (openRest.length) {
      for (const d of openRest) asked.add(d.key);
      qs.push({ type: 'group', id: 'q:open-rest', k: 'o:', keys: openRest.map(d => d.key), tag: 'open' });
    }
    const chunk = (list, tag, max) => {
      const us = units(list, true).map(u => Object.assign(u, { size: unitSize(u).n })).sort(bySize);
      const out = [];
      for (const u of us) {
        if (max !== undefined && out.length >= max) break;
        if (tag === 'sug' && u.size < BIG) continue;
        out.push(u);
        if (u.members) {
          for (const d of u.members) asked.add(d.key);
          qs.push({ type: 'group', id: u.id, k: u.k, keys: u.members.map(d => d.key), tag });
        } else one({ o: u.d }, tag);
      }
    };
    if (server.manifest.state !== 'vorhanden') {
      if (work.projects.length + work.containers.length) qs.push({ type: 'projects', id: 'q:projekte', order: projectOrder() });
      chunk(looseOf('daten').filter(d => !asked.has(d.key)), 'sug', 8);
      const hints = work.loose.filter(d => !d.split_of && !asked.has(d.key) && (dataNote(d) || (d.git && d.git.dirty > 0)));
      hints.sort(bySize);
      for (const d of hints.slice(0, 8)) one({ o: d }, 'hint');
      const rest = looseOf('daten').filter(d => !asked.has(d.key));
      if (rest.length) qs.push({ type: 'rest', id: 'q:rest', keys: rest.map(d => d.key) });
    } else {
      walk(work, (kind, o, owner) => {
        if (!o.new || asked.has(caddyKey(o)) || kind === 'image' || o.split_of) return;
        if (kind === 'decision') one({ o }, 'new');
        else if (owner && !owner.new) one({ o }, 'new');
      });
      const fresh = work.loose.filter(d => d.new && !d.split_of && !asked.has(d.key));
      chunk(fresh, 'new', 8);
      const rest = fresh.filter(d => !asked.has(d.key));
      if (rest.length) qs.push({ type: 'rest', id: 'q:rest', keys: rest.map(d => d.key), fresh: true });
      if (work.gone.length) qs.push({ type: 'gone', id: 'q:gone' });
    }
    return qs;
  }

  function projectOrder() {
    const rank = { take: 0, open: 1, archive: 2, skip: 3 };
    const all = work.projects.map(o => ({ o, kind: 'project' })).concat(work.containers.map(o => ({ o, kind: 'container' })));
    for (const x of all) x.size = itemBytes(x);
    all.sort((a, b) => (rank[a.o.decision] - rank[b.o.decision]) || (b.size - a.size));
    return all.map(x => x.o.key);
  }

  function qEntries(q) { return (q.keys || []).map(k => byKey.get(k)).filter(Boolean).map(x => x.o); }

  // Kurzinfo für die Liste „Deine Fragen“ und den Startbildschirm.
  function qInfo(q) {
    if (q.type === 'projects') {
      const items = projectItems();
      const take = items.filter(x => x.o.decision === 'take').length;
      const t = take + (alltag() ? ' im Backup, ' : ' ziehen um, ') + (items.length - take) + ' nicht';
      return { title: 'Projekte', sub: t, done: t };
    }
    if (q.type === 'rest') {
      const list = qEntries(q);
      return { title: q.fresh ? 'Die übrigen neuen Einträge' : 'Der Rest auf einmal', sub: plural(list.length, 'kleiner Eintrag', 'kleine Einträge') + ' · ' + sizeSum(sum(list)), done: 'Vorschlag übernommen' };
    }
    if (q.type === 'gone') {
      const n = work.gone.filter(g => g.remove).length;
      return { title: 'Nicht mehr gefunden', sub: plural(work.gone.length, 'Eintrag', 'Einträge'), done: n ? n + ' entfernen' : 'alle behalten' };
    }
    if (q.type === 'group') {
      const list = qEntries(q);
      const v = commonValue(list);
      const noun = memberNoun(list);
      return { title: groupName(q.k), sub: plural(list.length, noun[0], noun[1]) + ' · ' + sizeSum(sum(list)), done: v === 'open' ? 'offen' : v ? TILE[v][0] : 'unterschiedlich' };
    }
    const x = byKey.get(q.key);
    if (!x) return { title: q.key, sub: 'nicht mehr da', done: '' };
    const o = x.o;
    const v = valueOf(x);
    let sub = 'noch offen';
    if (q.tag === 'hint') sub = o.git && o.git.dirty ? plural(o.git.dirty, 'Git-Änderung', 'Git-Änderungen') : dataNote(o).split('; ')[0];
    else if (q.tag === 'new') sub = 'neu' + (o.size >= 0 && o.size !== undefined ? ' · ' + size(o.size) : '');
    else if (q.tag === 'sug') sub = [o.engine ? ENGINES[o.engine] || o.engine : o.type === 'path' ? 'Ordner' : 'Volume', size(o.size), o.git && o.git.dirty ? 'Git-Änderungen' : null].filter(Boolean).join(' · ');
    return { title: o.name || o.label || 'Caddy', sub, done: v && v !== 'open' ? optionLabel(x, v) : 'offen' };
  }

  function projectItems() {
    return work.projects.map(o => ({ o, kind: 'project' })).concat(work.containers.map(o => ({ o, kind: 'container' })));
  }

  // Wert eines Eintrags in der Sprache der Kisten.
  function valueOf(x) {
    if (x.kind === 'decision') return x.o.decision;
    if (x.kind === 'data' && !x.owner) return looseValue(x.o);
    return x.o.strategy;
  }
  function commonValue(list) {
    const vs = new Set(list.map(looseValue));
    return vs.size === 1 ? [...vs][0] : null;
  }
  function optionLabel(x, v) {
    if (x.kind === 'data' && x.owner) return (STRATEGIES[v] || [v])[0];
    return (TILE[v] || STRATEGIES[v] || [v])[0];
  }
  function qIndex(n) { return Math.min(Math.max(1, n), questions.length); }
  function firstUnanswered() {
    const i = questions.findIndex(q => !ui.answered.has(q.id));
    return i < 0 ? 0 : i + 1;
  }

  // ---------- Router ----------

  function route() {
    const h = location.hash;
    let m;
    if ((m = /^#\/auswahl\/fragen\/(\d+)$/.exec(h))) return { view: 'frage', n: Number(m[1]) };
    if ((m = /^#\/auswahl\/bereich\/([a-z-]+)(?:\/(.+))?$/.exec(h))) {
      let key = null;
      try { key = m[2] ? decodeURIComponent(m[2]) : null; } catch (e) { key = null; }
      return { view: 'bereich', area: m[1], key };
    }
    if (h === '#/auswahl/start') return { view: 'start' };
    if (h === '#/auswahl') return { view: 'uebersicht' };
    // Alles andere gehört zum Leitstand: #/lage, #/projekte[/schlüssel], …
    if ((m = /^#\/([a-z]+)(?:\/(.+))?$/.exec(h)) && LS && LS.pages.includes(m[1])) {
      let key = null;
      try { key = m[2] ? decodeURIComponent(m[2]) : null; } catch (e) { key = null; }
      return { view: 'ls', page: m[1], key };
    }
    return { view: 'ls', page: 'lage', key: null };
  }
  function isAuswahl(r) { return r.view !== 'ls'; }
  function go(h) { if (location.hash === h) render(true); else location.hash = h; }
  function detailsHash(area, key) { return '#/auswahl/bereich/' + area + '/' + encodeURIComponent(key); }

  function areaList() {
    const out = [];
    const pc = work.projects.length + work.containers.length;
    if (pc) out.push(['projekte', pc]);
    const lo = looseOf('daten').length;
    if (lo) out.push(['daten', lo]);
    const sv = looseOf('server').length;
    if (sv || server.settings.length || server.system.length) out.push(['server', sv]);
    if (work.caddy) out.push(['caddy', work.caddy.routes.length]);
    if (server.findings.length) out.push(['warnungen', server.findings.filter(f => f.level === 'warnung').length || server.findings.length]);
    if (server.checks.length + server.tasks.length) out.push(['neuer-server', server.checks.length + server.tasks.length]);
    return out;
  }
  function areaName(id) { const a = BEREICHE.find(b => b[0] === id); return a ? a[1] : id; }

  // ---------- Kopfzeile, Stand, Darstellung ----------

  function setStatus(text, cls) {
    const s = document.getElementById('status');
    s.textContent = text;
    s.className = 'st ' + (cls || '');
  }

  function updateStatus() {
    if (!work || saving) return;
    const n = edits().length;
    const errs = validate().length;
    const dirty = n > 0 || mustSave();
    document.getElementById('save').disabled = !dirty;
    if (errs && dirty) setStatus(plural(errs, 'Grund fehlt', 'Gründe fehlen'), 'error');
    else if (n > 0) setStatus(plural(n, 'Änderung', 'Änderungen') + ' nicht gespeichert', 'dirty');
    else if (mustSave()) setStatus('Vorschlag noch nicht gespeichert', 'dirty');
    else setStatus('Alles gespeichert', '');
  }

  function toast(text) {
    const t = el('div', { class: 'toast', role: 'status' }, icon('check', 18), text);
    document.getElementById('toasts').append(t);
    setTimeout(() => t.remove(), 4000);
  }

  function announce(text) {
    const l = document.getElementById('live');
    l.textContent = '';
    setTimeout(() => { l.textContent = text; }, 60);
  }

  function fatal(msg) {
    setStatus('', '');
    document.getElementById('save').disabled = true;
    document.getElementById('areas').hidden = true;
    document.getElementById('axbar').hidden = true;
    document.getElementById('app').className = 'ls-page';
    document.getElementById('app').replaceChildren(
      el('div', { class: 'wrap wrap-n' }, el('div', { class: 'band b-err', role: 'alert' }, el('p', {}, msg))));
  }

  // Hell oder Dunkel: zeichnet der Leitstand (Knopf oben, Einstellungen ›
  // Darstellung); hier nur für die Befehle der Suche.
  function setTheme(v) {
    if (LS && LS.setLook) LS.setLook({ theme: v || 'system' });
  }

  // Pfeiltasten, Pos1/Ende, Esc und Tab in Menüs.
  function menuKeys(e, close) {
    const items = [...e.currentTarget.querySelectorAll('[role^="menuitem"]')];
    const i = items.indexOf(document.activeElement);
    let n = -1;
    if (e.key === 'ArrowDown') n = (i + 1) % items.length;
    else if (e.key === 'ArrowUp') n = (i - 1 + items.length) % items.length;
    else if (e.key === 'Home') n = 0;
    else if (e.key === 'End') n = items.length - 1;
    else if (e.key === 'Escape' || e.key === 'Tab') { e.preventDefault(); e.stopPropagation(); close(); return; }
    if (n >= 0) { e.preventDefault(); items[n].focus(); }
  }

  function renderAreasNav(r) {
    const nav = document.getElementById('areas');
    const show = r.view === 'uebersicht' || r.view === 'bereich';
    nav.hidden = !show;
    if (!show) { nav.replaceChildren(); return; }
    const cur = r.view === 'bereich' ? r.area : '';
    const list = [['', 'Übersicht', null]].concat(areaList().map(([id, n]) => [id, areaName(id), n]));
    const href = id => id ? '#/auswahl/bereich/' + id : '#/auswahl';
    nav.replaceChildren(
      el('div', { class: 'areas-in' }, list.map(([id, label, n]) => el('a', { href: href(id), 'aria-current': id === cur ? 'page' : null },
        label, n !== null ? el('span', { class: 'c' }, String(n)) : null))),
      el('div', { class: 'areas-sel' }, el('label', { for: 'area-sel' }, 'Bereich'),
        el('select', { id: 'area-sel', class: 'ctl', onchange: e => go(e.target.value) },
          list.map(([id, label, n]) => el('option', { value: href(id), selected: id === cur }, label + (n !== null ? ' · ' + n : ''))))));
  }

  // ---------- Bänder ----------

  function band(kind, kids, role) { return el('div', { class: 'band b-' + kind, role: role || null }, kids); }

  function banners(withChanges) {
    const out = [];
    const m = server.manifest;
    if (m.state === 'fehlt') out.push(band('info', el('p', {}, el('b', {}, 'Noch kein manifest.yaml.'), ' HostDeck zeigt seinen Vorschlag; Speichern legt die Datei an.')));
    if (m.state === 'ungueltig') {
      out.push(band('err', [el('p', {}, el('b', {}, 'manifest.yaml ist ungültig'), ' und wird nicht benutzt. Beim Speichern wird die alte Datei als manifest.yaml.ungueltig aufgehoben.'),
        (m.problems || []).length ? el('ul', { class: 'mono' }, m.problems.map(p => el('li', {}, p))) : null]));
    }
    if (withChanges && m.state === 'vorhanden' && (m.added.length || work.gone.length)) {
      const parts = [];
      if (m.added.length) parts.push(m.added.length === 1 ? '1 Eintrag ist neu dazugekommen' : m.added.length + ' Einträge sind neu dazugekommen');
      if (work.gone.length) parts.push(work.gone.length === 1 ? '1 wurde nicht mehr gefunden' : work.gone.length + ' wurden nicht mehr gefunden');
      const open = questions.filter(q => !ui.answered.has(q.id)).length;
      out.push(band('info', [el('p', {}, el('b', {}, 'Seit dem letzten Speichern hat sich etwas geändert. '), andList(parts) + '. ' +
        (open ? (open === 1 ? 'Das ist 1 Frage' : 'Das sind ' + open + ' Fragen') + '; der Rest bleibt, wie du ihn gespeichert hast. ' : '') + 'Neue Einträge tragen die Marke „neu“.'),
      open ? el('div', { class: 'acts' }, el('a', { class: 'btn btn-p btn-sm', href: '#/auswahl/fragen/' + firstUnanswered() }, plural(open, 'Frage', 'Fragen') + ' beantworten', icon('next', 16))) : null]));
    }
    // Manifeste aus älteren HostDeck-Versionen: lokal gebaute Images standen dort
    // auf „Aus der Registry laden“.
    const wrong = [];
    for (const p of work.projects) {
      if (p.decision !== 'take' && p.decision !== 'archive') continue;
      for (const s of p.services) if (s.image === 'rebuild' && s.origin === 'gebaut') wrong.push([p, s]);
    }
    if (wrong.length) {
      out.push(band('warn', [el('p', {}, el('b', {}, (wrong.length === 1 ? 'Bei einem Dienst steht' : 'Bei ' + wrong.length + ' Diensten steht') + ' „Aus der Registry laden“'),
        ', obwohl das Image hier gebaut wurde: ' + wrong.map(([p, s]) => p.name + '/' + s.name).join(', ') + '. Auf dem neuen Server ließe es sich so vermutlich nicht laden.'),
      el('div', { class: 'acts' }, el('button', {
        class: 'btn btn-sm btn-warn', type: 'button', 'data-fid': 'fix-images',
        onclick: () => { for (const [, s] of wrong) s.image = 'image-save'; rerender(); toast('Umgestellt, jetzt noch speichern'); },
      }, 'Alle auf „Image mitnehmen“ umstellen'))]));
    }
    const reloadBtn = el('button', { class: 'btn btn-sm btn-warn', type: 'button', onclick: reload }, 'Neu laden');
    const old = Date.now() - new Date(server.created).getTime() > 24 * 3600 * 1000;
    if (svc && (!server.host.root || old)) {
      // Als Dienst erfasst HostDeck selbst neu, auf Knopfdruck oder nachts.
      out.push(band('warn', [el('p', {}, el('b', {}, !server.host.root ? 'Die Bestandsaufnahme lief ohne root' : 'Die Bestandsaufnahme ist älter als einen Tag.'),
        !server.host.root ? ', manche Angaben fehlen.' : '', ' Neu erfassen dauert etwa eine Minute; die Seite lädt danach von selbst.'), discoverHint()]));
    } else if (!svc && !server.host.root) {
      out.push(band('warn', [el('p', {}, el('b', {}, 'Die Bestandsaufnahme lief ohne root'), ', manche Angaben fehlen. Besser: ', el('span', { class: 'mono' }, 'sudo ./hostdeck discover'), ', danach neu laden.'),
        el('div', { class: 'acts' }, reloadBtn)]));
    }
    if (!svc && old) {
      out.push(band('warn', [el('p', {}, el('b', {}, 'Die Bestandsaufnahme ist älter als einen Tag.'), ' Für den aktuellen Stand: ', el('span', { class: 'mono' }, 'sudo ./hostdeck discover'), ', danach neu laden.'),
        server.host.root ? el('div', { class: 'acts' }, reloadBtn) : null]));
    }
    return out;
  }

  function saveErrorBand() {
    const e = ui.saveError;
    if (!e) return null;
    if (e.reason && !validate().length) { ui.saveError = null; return null; }
    return el('div', { class: 'band b-err', role: 'alert', id: 'save-error' },
      el('p', {}, e.lead ? el('b', {}, e.lead + ' ') : null, e.text),
      e.problems && e.problems.length ? el('ul', {}, e.problems.map(p => el('li', {}, p))) : null,
      (e.reload || e.retry || e.href) ? el('div', { class: 'acts' },
        e.reload ? el('button', { class: 'btn btn-sm btn-err', type: 'button', onclick: () => load() }, 'Neu laden') : null,
        e.retry ? el('button', { class: 'btn btn-sm btn-err', type: 'button', onclick: save }, 'Nochmal speichern') : null,
        e.href ? el('a', { href: e.href }, 'Zum Eintrag') : null) : null);
  }

  function reasonFid(o) { return idFor(caddyKey(o)) + '-reason'; }

  // Wo das Feld für den Grund eines Eintrags steht.
  function locate(o, owner) {
    if (o === work.caddy) return '#/auswahl/bereich/caddy';
    if (owner) return detailsHash('projekte', owner.key);
    let d = o;
    if (d.split_of) {
      const p = byKey.get(d.split_of);
      if (p && p.owner) return detailsHash('projekte', p.owner.key);
      if (p) d = p.o;
    }
    return detailsHash(d.area ? 'server' : 'daten', d.key);
  }

  function reasonMissing(errs) {
    for (const x of errs) ui.errs.add(reasonFid(x.o));
    ui.saveError = null;
    rerender();
    for (const x of errs) {
      const f = document.querySelector('[data-fid="' + reasonFid(x.o) + '"]');
      if (f && f.offsetParent !== null) { f.focus(); f.scrollIntoView({ block: 'center' }); return; }
    }
    ui.saveError = { reason: true, lead: 'Speichern ging nicht:', text: 'bei ' + plural(errs.length, 'Eintrag', 'Einträgen') + ' fehlt der Grund fürs Weglassen.', href: locate(errs[0].o, errs[0].owner) };
    rerender();
    const b = document.getElementById('save-error');
    if (b) { b.scrollIntoView({ block: 'center' }); const a = b.querySelector('a'); if (a) a.focus(); }
  }

  // ---------- Bausteine ----------

  function seg(name, legend, opts, value, onChange) {
    const fs = el('fieldset', { class: 'seg' }, el('legend', { class: 'sr' }, legend));
    for (const [v, label] of opts) {
      put(fs, el('label', { class: 'v-' + v },
        el('input', { type: 'radio', name, value: v, checked: value === v, 'data-fid': name + '-' + v, onchange: () => onChange(v) }),
        el('span', {}, label)));
    }
    return fs;
  }

  function tiles(name, legend, opts, value, proposal, onChange, list) {
    const fs = el('fieldset', { class: 'choices' + (list ? ' list' : '') }, el('legend', { class: 'sr' }, legend));
    for (const [v, title, help] of opts) {
      put(fs, el('label', { class: 'tile' + (value === v ? ' on' : '') },
        el('input', { type: 'radio', name, value: v, checked: value === v, 'data-fid': name + '-' + v, onchange: () => onChange(v) }),
        el('span', {}, el('b', {}, title, v === proposal ? el('span', { class: 'tag t-sug' }, 'Vorschlag') : null), el('span', { class: 'note' }, help))));
    }
    return fs;
  }

  function reasonField(o, opts) {
    opts = opts || {};
    const fid = opts.fid || reasonFid(o);
    const eid = fid + '-e';
    const get = () => opts.get ? opts.get() : (o.reason || '');
    const bad = !opts.optional && ui.errs.has(fid) && !get().trim();
    const input = el('input', {
      type: 'text', class: 'fld', id: fid, 'data-fid': fid, value: get(), maxlength: 500, autocomplete: 'off',
      placeholder: opts.optional ? 'optional' : (opts.placeholder || 'Pflicht, z. B. „wird nicht mehr gebraucht“'),
      'aria-invalid': bad ? 'true' : null, 'aria-describedby': bad ? eid : null,
      oninput: e => {
        const v = e.target.value;
        if (opts.set) opts.set(v); else o.reason = v;
        if (v.trim() && ui.errs.has(fid)) {
          ui.errs.delete(fid);
          e.target.removeAttribute('aria-invalid');
          e.target.removeAttribute('aria-describedby');
          const m = document.getElementById(eid);
          if (m) m.remove();
        }
        updateStatus();
      },
    });
    return el('div', { class: 'reason' + (opts.cls ? ' ' + opts.cls : '') },
      el('label', { for: fid }, opts.label || 'Warum weglassen?'), input,
      bad ? el('p', { class: 'ferr', id: eid }, NEED_REASON) : null,
      opts.help ? el('span', { class: 'note' }, opts.help) : null);
  }

  // Fehler am Feld zeigen und den Fokus hineinsetzen.
  function flagField(fid) {
    ui.errs.add(fid);
    rerender();
    const f = document.querySelector('[data-fid="' + fid + '"]');
    if (f) { f.focus(); f.scrollIntoView({ block: 'center' }); }
  }

  function warnLine(text) { return el('p', { class: 'warn' }, icon('warn', 15), el('span', {}, text)); }
  function newTag(o) { return o.new ? el('span', { class: 'tag t-new', title: 'seit dem letzten Speichern neu gefunden' }, 'neu') : null; }

  // Im Alltag heißt dieselbe Entscheidung „im Backup“ und „nicht im Backup“
  // statt „kommt mit“ und „bleibt zurück“ (Idee 1).
  const wTake = () => alltag() ? 'Im Backup' : 'Kommt mit';
  const wLeft = () => alltag() ? 'Nicht im Backup' : 'Bleibt zurück';
  function numsList(m, cls) {
    return el('dl', { class: cls || 'nums', 'aria-label': 'Umzug in Zahlen' },
      el('div', {}, el('dt', {}, el('i', { class: 'sq m-take' }), wTake()), el('dd', {}, (m.unknown ? 'mind. ' : '') + size(m.take))),
      el('div', {}, el('dt', {}, el('i', { class: 'sq m-arch' }), 'Im Archiv'), el('dd', {}, size(m.archive))),
      m.left !== null ? el('div', {}, el('dt', {}, el('i', { class: 'sq m-left' }), wLeft()), el('dd', {}, size(m.left))) : null);
  }

  function openState() {
    const n = openList().length;
    return n ? el('p', { class: 'open-line' }, icon('warn', 16), 'Noch ' + n + ' offen') : el('p', { class: 'ok-line' }, icon('check', 18), 'Nichts mehr offen');
  }

  function details(id, summary, body, cls) {
    const d = el('details', { class: cls || 'fold', open: ui.expanded.has(id) }, el('summary', {}, summary), body);
    d.addEventListener('toggle', () => { if (d.open) ui.expanded.add(id); else ui.expanded.delete(id); });
    return d;
  }

  // ---------- Start ----------

  function questionMix() {
    const c = f => questions.filter(f).length;
    const parts = [];
    const open = c(q => q.tag === 'open');
    if (open) parts.push(plural(open, 'offener Punkt', 'offene Punkte'));
    if (c(q => q.type === 'projects')) parts.push('die Projekte');
    const big = c(q => q.tag === 'sug');
    if (big) parts.push(plural(big, 'großer Brocken', 'große Brocken'));
    const hint = c(q => q.tag === 'hint');
    if (hint) parts.push(plural(hint, 'Eintrag mit Hinweis', 'Einträge mit Hinweis'));
    const neu = c(q => q.tag === 'new');
    if (neu) parts.push(plural(neu, 'neuer Eintrag', 'neue Einträge'));
    const rest = questions.find(q => q.type === 'rest');
    if (rest) parts.push(rest.fresh ? 'die übrigen neuen auf einmal' : 'der Rest auf einmal');
    if (c(q => q.type === 'gone')) parts.push('die nicht mehr gefundenen');
    return cap(andList(parts)) + '.';
  }

  function viewStart() {
    const n = questions.length;
    const daten = looseOf('daten');
    const srv = looseOf('server');
    const warns = server.findings.filter(f => f.level === 'warnung').length;
    const stats = [
      [projectItems().length, 'Projekte', work.containers.length ? 'Docker-Compose und einzelne Container' : 'Docker-Compose, mit Daten und Images'],
      [daten.length, 'Daten ohne Projekt', 'Volumes und Ordner, die gerade keiner nutzt'],
      [srv.length, srv.length === 1 ? 'Ort am Server' : 'Orte am Server', shortList(srv.map(d => d.label), 3)],
      [work.caddy ? work.caddy.routes.length : 0, 'Caddy-Routen', work.caddy ? deadText(work.caddy.routes, true) : ''],
      [warns || server.findings.length, warns ? 'Warnungen' : 'Hinweise', 'zum Lesen, Speichern geht trotzdem'],
      [server.checks.length + server.tasks.length, 'für den neuen Server',
        [server.checks.length ? plural(server.checks.length, 'Punkt', 'Punkte') + ' Checkliste' : '', server.tasks.length ? plural(server.tasks.length, 'Aufgabe', 'Aufgaben') : ''].filter(Boolean).join(', ')],
    ].filter(s => s[0] > 0);
    const page = el('div', { class: 'wrap wrap-n stack' },
      banners(false), saveErrorBand(),
      el('div', { class: 'stack' },
        el('p', { class: 'eyb' }, 'Bestandsaufnahme vom ' + fmtDate(server.created)),
        el('h1', { class: 'h1 h1-xl', tabindex: '-1' }, 'Was soll mit auf den neuen Server?'),
        el('p', { class: 'lead' }, 'HostDeck hat ' + host() + ' durchgesehen und alles vorsortiert. Du beantwortest nur die Fragen, bei denen es auf dich ankommt. Alles andere kannst du später in Ruhe ansehen.')),
      el('ul', { class: 'stats', 'aria-label': 'Was HostDeck gefunden hat' }, stats.map(([v, t, note]) =>
        el('li', { class: 'card stat' }, el('span', { class: 'n' }, String(v)), el('b', {}, t), note ? el('span', { class: 'note' }, note) : null))),
      el('section', { class: 'card qstart', 'aria-labelledby': 'st-q' },
        el('h2', { class: 'h2', id: 'st-q' }, n ? (n === 1 ? '1 Frage' : n + ' Fragen') + ', dann ist deine Auswahl fertig' : 'Keine Fragen offen'),
        el('p', {}, n ? questionMix() : 'HostDeck hat nichts gefunden, bei dem es auf dich ankommt. In der Auswahl-Übersicht siehst du alles auf einmal.'),
        el('div', { class: 'acts hide-m' },
          n ? el('a', { class: 'btn btn-p btn-lg', href: '#/auswahl/fragen/1' }, 'Fragen starten', icon('next', 18)) : el('a', { class: 'btn btn-p btn-lg', href: '#/auswahl' }, 'Zur Auswahl-Übersicht'),
          n ? el('a', { href: '#/auswahl' }, 'Gleich zur Auswahl-Übersicht') : null)),
      el('p', { class: 'shield' }, icon('shield', 20), el('span', {}, 'Hier wird nur ausgewählt. Gespeichert wird in ', el('span', { class: 'mono' }, 'manifest.yaml'), ', am Server selbst ändert sich nichts. Du kannst jederzeit speichern und später weitermachen.')));
    const bar = el('div', { class: 'fbar col' },
      n ? el('a', { class: 'btn btn-p btn-lg', href: '#/auswahl/fragen/1' }, 'Fragen starten', icon('next', 18)) : el('a', { class: 'btn btn-p btn-lg', href: '#/auswahl' }, 'Zur Auswahl-Übersicht'),
      n ? el('a', { class: 'lk', href: '#/auswahl' }, 'Gleich zur Auswahl-Übersicht') : null);
    return [page, bar];
  }

  // ---------- Fragen ----------

  function applyValue(x, v) {
    if (x.kind === 'decision') x.o.decision = v;
    else if (x.kind === 'data' && !x.owner) setLoose(x.o, v);
    else x.o.strategy = v;
  }

  function looseChoices(d) {
    return ['take', 'archive', 'skip'].filter(v => !(v === 'take' && d.area === 'einstellungen'));
  }

  function bucketOf(it) { return it.kind === 'loose' ? looseValue(it.o) : it.o.decision; }

  // Inhalt einer Frage: Marken, Titel, Satz, Arbeit, Knopf.
  function questionBody(q) {
    const r = { tags: [], from: '', title: '', lead: null, body: [], next: 'Weiter', check: () => true, answer: true, extra: null };
    if (q.type === 'projects') {
      const items = q.order.map(k => byKey.get(k)).filter(Boolean).map(x => ({ o: x.o, kind: x.o.services ? 'project' : 'container' }));
      const by = v => items.filter(x => x.o.decision === v).length;
      const take = by('take');
      r.tags.push(el('span', { class: 'tag t-sug' }, 'Vorschlag von HostDeck prüfen'));
      r.from = 'Projekte';
      r.title = take === 1 ? 'Dieses Projekt zieht mit um. Passt das?' : take ? 'Diese ' + take + ' Projekte ziehen mit um. Passt das?' : 'Kein Projekt zieht mit um. Passt das?';
      const parts = ['Zusammen ' + sizeSum(sum(items.filter(x => x.o.decision === 'take'), itemBytes)) + ' mit Images.'];
      if (by('skip')) parts.push(plural(by('skip'), 'Projekt bleibt', 'Projekte bleiben') + ' zurück, mit Grund.');
      if (by('archive')) parts.push(plural(by('archive'), 'Projekt kommt', 'Projekte kommen') + ' ins Archiv.');
      if (by('open')) parts.push((by('open') === 1 ? '1 ist' : by('open') + ' sind') + ' noch offen.');
      r.lead = parts.join(' ');
      r.body.push(el('div', { class: 'phead', 'aria-hidden': 'true' }, el('span', {}, 'Projekt'), el('span', {}, 'Größe und Entscheidung')),
        el('ul', { class: 'prows' }, items.map(projectRow)),
        el('p', { class: 'note' }, 'Sicherungsart, Images und Unterordner stehen in den Details jedes Projekts, unter Auswahl › Projekte.'));
      r.next = 'Passt so, weiter';
      r.check = () => {
        const bad = items.find(x => x.o.decision === 'skip' && !hasReason(x.o));
        if (bad) { flagField(reasonFid(bad.o)); return false; }
        return true;
      };
      return r;
    }
    if (q.type === 'rest') {
      const list = qEntries(q);
      const c = v => list.filter(d => looseValue(d) === v).length;
      const words = { take: 'übernehmen', archive: 'archivieren', skip: 'weglassen', open: 'offen' };
      r.tags.push(el('span', { class: 'tag ' + (q.fresh ? 't-new' : 't-sug') }, q.fresh ? 'Neu' : 'Vorschlag von HostDeck prüfen'));
      r.from = 'Daten ohne Projekt';
      r.title = q.fresh ? 'Die übrigen ' + plural(list.length, 'neuen Eintrag', 'neuen Einträge') : 'Der Rest auf einmal: ' + plural(list.length, 'kleinerer Eintrag', 'kleinere Einträge');
      r.lead = 'Zusammen ' + sizeSum(sum(list)) + '. HostDeck hat sie vorsortiert: ' + andList(['take', 'archive', 'skip', 'open'].filter(c).map(v => c(v) + ' ' + words[v])) + '.';
      r.body.push(details(q.id + ':list', 'Alle ' + list.length + ' ansehen', el('ul', { class: 'mlist' }, list.map(d =>
        el('li', {}, el('span', { class: 'mono' }, d.label), el('span', { class: 'z' }, KISTEN[looseValue(d)][0] + ' · ' + size(d.size))))), 'members'));
      r.next = 'Vorschlag übernehmen, weiter';
      r.extra = () => el('a', { class: 'btn', href: '#/auswahl/bereich/daten' }, 'Einzeln ansehen');
      return r;
    }
    if (q.type === 'gone') {
      r.tags.push(el('span', { class: 'tag t-new' }, 'Seit dem letzten Speichern'));
      r.from = 'manifest.yaml';
      r.title = 'Diese Einträge gibt es auf dem Server nicht mehr. Aus der Auswahl entfernen?';
      r.lead = 'Sie stehen in manifest.yaml, wurden bei der letzten Bestandsaufnahme aber nicht mehr gefunden. Ohne Haken bleiben sie im Manifest stehen. Nach einem Umzug ist das meist, was auf dem alten Server nur ins Archiv kam; es liegt in dessen Backup.';
      const allGone = work.gone.every(g => g.remove);
      if (work.gone.length > 1) r.extra = () => el('button', { class: 'btn', type: 'button', 'data-fid': 'gone-all', onclick: () => { for (const g of work.gone) g.remove = !allGone; rerender(); } },
        allGone ? 'Keinen entfernen' : 'Alle ' + work.gone.length + ' entfernen');
      r.body.push(el('fieldset', { class: 'choices list' }, el('legend', { class: 'sr' }, 'Aus der Auswahl entfernen'),
        work.gone.map(g => el('label', { class: 'chk' },
          el('input', { type: 'checkbox', checked: g.remove, 'data-fid': idFor(g.key) + '-gone', onchange: e => { g.remove = e.target.checked; rerender(); } }),
          el('span', {}, el('span', { class: 'mono' }, g.label), ' ', el('span', { class: 'note' }, g.where))))));
      return r;
    }
    if (q.type === 'group') {
      const list = qEntries(q);
      const v = commonValue(list);
      const noun = memberNoun(list);
      const sv = commonValue(list.map(d => serverKey.get(d.key) || d));
      const tg = { open: ['t-open', 'Noch offen'], new: ['t-new', 'Neu'] }[q.tag] || ['t-sug', 'Vorschlag von HostDeck prüfen'];
      r.tags.push(el('span', { class: 'tag ' + tg[0] }, tg[1]));
      if (q.tag === 'open' && list.some(d => d.new)) r.tags.push(el('span', { class: 'tag t-new' }, 'Neu'));
      const area = list.length && list.every(d => d.area) ? 'server' : 'daten';
      r.from = areaName(area);
      const rest = q.k === 'o:';
      r.title = rest ? 'Was passiert mit den übrigen ' + list.length + ' offenen Einträgen?' : ['Was passiert mit ', el('span', { class: q.k[0] === 's' ? 'mono' : null }, groupName(q.k)), '?'];
      const why = rest ? 'HostDeck konnte sie nicht selbst einordnen' : q.k[0] === 's' ? 'im selben Ordner, mit gleichem Namensanfang' : list.some(d => d.former_project) ? 'gehörten zum früheren Projekt ' + q.k.slice(2) : 'mit gleichem Namensanfang';
      r.lead = plural(list.length, noun[0], noun[1]) + ' · ' + sizeSum(sum(list)) + ' · ' + why + '. Die Antwort gilt für alle.';
      const allowed = ['take', 'archive', 'skip'].filter(k => !(k === 'take' && list.some(d => d.area === 'einstellungen')));
      r.body.push(tiles(idFor(q.id), 'Entscheidung für ' + groupName(q.k), allowed.map(k => [k, TILE[k][0], TILE[k][1]]), v, sv,
        nv => { for (const d of list) setLoose(d, nv); rerender(); }));
      if (!v) r.body.push(el('p', { class: 'note' }, 'Gerade unterschiedlich gewählt. Eine Antwort hier gilt für alle.'));
      if (rest) r.body.push(el('p', { class: 'note' }, 'Lieber einzeln? Unter Auswahl › ' + areaName(area) + ' liegen sie in der Kiste „Noch offen“.'));
      const fid = idFor(q.id) + '-reason';
      if (v === 'skip') {
        const reasons = new Set(list.map(d => d.reason || ''));
        r.body.push(reasonField(null, {
          fid, label: 'Warum weglassen? (gilt für alle)', get: () => reasons.size === 1 ? [...reasons][0] : '',
          placeholder: reasons.size === 1 ? 'Pflicht, z. B. „alte Testdaten“' : 'unterschiedliche Gründe; hier eintippen ersetzt alle',
          set: val => { for (const d of list) d.reason = val; },
        }));
      }
      r.body.push(details(q.id + ':list', 'Alle ' + list.length + ' ansehen', el('ul', { class: 'mlist' }, list.map(d =>
        el('li', {}, el('span', { class: 'mono', 'aria-label': d.label }, memberLabel(q.k, d)), el('span', { class: 'z' }, size(d.size))))), 'members'));
      r.extra = () => el('button', { class: 'btn', type: 'button', onclick: () => { if (!rest) ui.expanded.add(q.gid || q.id); go('#/auswahl/bereich/' + area); } }, 'Lieber einzeln entscheiden');
      if (!v || v === 'open') { r.next = 'Überspringen'; r.answer = false; }
      r.check = () => {
        if (v === 'skip' && list.some(d => !hasReason(d))) { flagField(fid); return false; }
        return true;
      };
      return r;
    }
    // eine Frage zu einem Eintrag
    const x = byKey.get(q.key);
    if (!x) {
      r.title = 'Dieser Eintrag ist nicht mehr da';
      r.lead = 'Er wurde inzwischen entfernt. Weiter geht es mit der nächsten Frage.';
      return r;
    }
    const o = x.o;
    const v = valueOf(x);
    const sv = serverKey.get(q.key);
    const proposal = sv ? valueOf({ kind: x.kind, o: sv, owner: x.owner }) : null;
    const tag = { open: ['t-open', 'Noch offen'], hint: ['t-sug', 'Vorschlag von HostDeck prüfen'], sug: ['t-sug', 'Vorschlag von HostDeck prüfen'], new: ['t-new', 'Neu'] }[q.tag];
    r.tags.push(el('span', { class: 'tag ' + tag[0] }, tag[1]));
    let opts;
    const name = o.name || o.label || 'Caddy';
    const nameEl = el('span', { class: o.label && o.label.startsWith('/') ? 'mono' : null }, name);
    if (x.kind === 'decision') {
      const isProj = !!o.services;
      r.from = o === work.caddy ? 'Caddy' : 'Projekte';
      r.title = o === work.caddy ? 'Soll Caddy mit umziehen?' : [isProj ? 'Soll das Projekt ' : 'Soll der Container ', nameEl, ' mit umziehen?'];
      r.lead = o === work.caddy ? o.config + ' · ' + plural(o.routes.length, 'Route', 'Routen')
        : isProj ? [o.running + ' von ' + o.total + ' laufen', ...o.domains.slice(0, 2), size(projectBytes(o))].join(' · ')
          : [o.running ? 'läuft' : o.state, o.image].join(' · ');
      opts = ['take', 'archive', 'skip'].map(k => [k, TILE_PROJECT[k][0], TILE_PROJECT[k][1]]);
    } else if (x.owner) {
      r.from = 'Projekt ' + x.owner.name;
      r.title = ['Wie soll ', nameEl, ' gesichert werden?'];
      r.lead = dataFacts(o) + ' · gehört zu ' + x.owner.name;
      opts = dataOptions(o).filter(s => s !== 'open').map(s => [s, STRATEGIES[s][0], cap(STRATEGIES[s][1]) + '.']);
    } else {
      r.from = o.area ? 'Am Server selbst' : 'Daten ohne Projekt';
      r.title = ['Soll ', nameEl, ' mit umziehen?'];
      r.lead = dataFacts(o);
      opts = looseChoices(o).map(k => [k, TILE[k][0], TILE[k][1]]);
    }
    const note = x.kind === 'data' ? dataNote(o) : o.note;
    if (note) r.body.push(el('p', { class: 'hintbox' }, icon('warn', 16), el('span', {}, note)));
    if (o.git && o.git.dirty) r.body.push(warnLine(plural(o.git.dirty, 'Änderung', 'Änderungen') + ' im Git-Ordner nicht committet'));
    r.body.push(tiles(idFor(q.key), 'Entscheidung für ' + name, opts, v, proposal === 'open' ? null : proposal, nv => { applyValue(x, nv); rerender(); }, x.kind === 'data' && !!x.owner));
    if (v === 'skip') r.body.push(reasonField(o, { help: 'Ein paar Worte reichen. Der Grund steht später in manifest.yaml.' }));
    if (x.kind === 'data' && !x.owner) r.body.push(el('p', { class: 'note' }, 'Unsicher? Archivieren ist die sichere Wahl: nichts geht verloren, aber auch nichts kommt ungefragt zurück.'));
    if (v === 'open') { r.next = 'Überspringen'; r.answer = false; }
    r.check = () => {
      if (valueOf(x) === 'skip' && !hasReason(o)) { flagField(reasonFid(o)); return false; }
      return true;
    };
    return r;
  }

  // Zeile in der Frage zu den Projekten.
  function projectRow(it) {
    const o = it.o;
    const key = o.key;
    const name = 'p' + idFor(key);
    const set = v => { o.decision = v; rerender(); };
    const meta = [];
    if (it.kind === 'project') {
      meta.push(o.running + ' von ' + o.total + (o.running === o.total ? '' : ' laufen'));
      meta.push(...o.domains.slice(0, 2));
    } else meta.push(o.running ? 'läuft' : o.state, o.image);
    if (o.decision === 'skip' && hasReason(o)) meta.push('Grund: „' + o.reason + '“');
    const warns = [];
    if (o.git && o.git.dirty) warns.push(plural(o.git.dirty, 'Git-Änderung', 'Git-Änderungen'));
    if (it.kind === 'project' && !o.dir_exists) warns.push('Ordner fehlt');
    const od = ownerOpen(o) - (o.decision === 'open' ? 1 : 0);
    if (od) warns.push(plural(od, 'Datenordner', 'Datenordner') + ' offen');
    const values = ['take', 'archive', 'skip'];
    const marks = { take: 'm-take', archive: 'm-arch', skip: 'm-left', open: 'm-left' };
    return el('li', { class: 'prow' },
      el('div', { class: 'pn' }, el('b', {}, o.name), ' ', it.kind === 'container' ? el('span', { class: 'tag t-mut' }, 'Container') : null, newTag(o),
        el('div', { class: 'note' }, el('span', {}, meta.join(' · ')), warns.map(w => el('span', { class: 'warn' }, icon('warn', 14), w)))),
      el('span', { class: 'sz' }, size(itemBytes(it))),
      seg(name, 'Entscheidung für ' + o.name, values.map(v => [v, TILE[v][0]]), o.decision, set),
      el('div', { class: 'psel' }, el('i', { class: 'sq ' + marks[o.decision] }),
        el('select', { class: 'ctl', 'aria-label': 'Entscheidung für ' + o.name, 'data-fid': name + '-sel', onchange: e => set(e.target.value) },
          o.decision === 'open' ? el('option', { value: 'open', selected: true, disabled: true }, 'Noch offen') : null,
          values.map(v => el('option', { value: v, selected: o.decision === v }, TILE[v][0])))),
      o.decision === 'skip' ? reasonField(o, {}) : null);
  }

  function agendaItems(cur) {
    return questions.map((q, k) => {
      const inf = qInfo(q);
      const now = k + 1 === cur;
      const done = ui.answered.has(q.id) && !now;
      return el('li', {}, el('a', { class: 'q' + (done ? ' done' : ''), href: '#/auswahl/fragen/' + (k + 1), 'aria-current': now ? 'step' : null },
        el('span', { class: 'i', 'aria-hidden': 'true' }, done ? '✓' : String(k + 1)),
        el('span', { class: 't' + (inf.title.startsWith('/') ? ' mono' : '') }, inf.title),
        el('span', { class: 's' }, done ? inf.done : inf.sub),
        done ? el('span', { class: 'sr' }, ' (beantwortet)') : null));
    });
  }

  function viewQuestion(n) {
    if (!questions.length) { history.replaceState(null, '', '#/auswahl'); return viewOverview(); }
    const i = qIndex(n);
    if (i !== n) history.replaceState(null, '', '#/auswahl/fragen/' + i);
    const q = questions[i - 1];
    const N = questions.length;
    const b = questionBody(q);
    const nextHash = i < N ? '#/auswahl/fragen/' + (i + 1) : '#/auswahl';
    const nextName = i < N ? qInfo(questions[i]).title : 'Auswahl-Übersicht';
    const onNext = () => {
      if (!b.check()) return;
      if (b.answer) ui.answered.add(q.id);
      go(nextHash);
    };
    const backHash = i > 1 ? '#/auswahl/fragen/' + (i - 1) : '#/auswahl/start';
    const answered = questions.filter(x => ui.answered.has(x.id)).length;
    const main = el('div', { class: 'qmain' },
      el('div', { class: 'qhead' }, el('span', { class: 'eyb' }, 'Frage ' + i + ' von ' + N),
        el('a', { class: 'btn btn-sm', href: '#/auswahl' }, el('span', { class: 'hide-m' }, 'Fragen überspringen'), el('span', { class: 'show-m' }, 'Zur Übersicht'))),
      el('div', { class: 'prog', 'aria-hidden': 'true' }, questions.map((x, k) => el('i', { class: k + 1 === i ? 'n' : ui.answered.has(x.id) ? 'd' : null }))),
      el('details', { class: 'agenda-m' }, el('summary', {}, 'Deine Fragen (' + answered + ' von ' + N + ' beantwortet)'), el('ol', {}, agendaItems(i))),
      saveErrorBand(),
      el('section', { class: 'card qcard', 'aria-labelledby': 'q-title' },
        el('div', { class: 'qtags' }, b.tags, b.from ? el('span', { class: 'note' }, 'aus: ' + b.from) : null),
        el('div', { class: 'stack' },
          el('h1', { class: 'h1', id: 'q-title', tabindex: '-1' }, b.title),
          b.lead ? el('p', { class: 'lead' }, b.lead) : null),
        b.body,
        b.extra ? el('div', { class: 'show-m' }, b.extra()) : null),
      el('div', { class: 'qnav hide-m' },
        el('a', { class: 'btn', href: backHash }, icon('left', 18), i > 1 ? 'Zurück' : 'Zum Start'),
        el('div', { class: 'r' }, b.extra ? b.extra() : null,
          el('button', { class: 'btn btn-p', type: 'button', 'data-fid': 'q-next', onclick: onNext }, i === N && b.answer ? b.next.replace('weiter', 'zur Auswahl-Übersicht').replace(/^Weiter$/, 'Fertig, zur Auswahl-Übersicht') : b.next, icon('next', 18)))));
    const aside = el('aside', { class: 'agenda', 'aria-label': 'Deine Fragen' },
      el('h2', { class: 'lbl' }, 'Deine Fragen'),
      el('nav', { 'aria-label': 'Fragen' }, el('ol', {}, agendaItems(i))),
      el('div', { class: 'sep' }, el('h2', { class: 'lbl' }, 'Umzug in Zahlen'), numsList(moveSummary())));
    const bar = el('div', { class: 'fbar' },
      el('a', { class: 'btn', href: backHash, 'aria-label': i > 1 ? 'Zurück' : 'Zum Start' }, icon('left', 20)),
      el('button', { class: 'btn btn-p grow', type: 'button', 'data-fid': 'q-next-m', onclick: onNext },
        b.next === 'Weiter' ? 'Weiter: ' + nextName : b.next));
    return [el('div', { class: 'wrap' }, el('div', { class: 'qwrap' }, main, aside)), bar];
  }

  // ---------- Übersicht ----------

  function locateOpen(x) {
    if (x.o === work.caddy) return '#/auswahl/bereich/caddy';
    if (x.owner) return detailsHash('projekte', x.owner.key);
    return detailsHash(x.o.area ? 'server' : 'daten', x.o.key);
  }

  // Was mit deinen Daten passiert: drei Kacheln wie im Leitstand, darunter
  // der Balken mit dem, was nicht mitgezählt ist.
  function moveTiles(openN) {
    const m = moveSummary();
    const pre = m.unknown ? 'mind. ' : '';
    const data = m.take - m.img.take;
    const tile = (sq, label, value, sub, kind) => {
      const t = size(value);
      const i = t.lastIndexOf('\u00a0');
      return el('div', { class: 'ls-kpi' + (kind ? ' ' + kind : '') },
        el('span', { class: 'k-l' }, sq ? el('i', { class: 'sq ' + sq, 'aria-hidden': 'true' }) : null, el('span', {}, label)),
        el('b', { class: 'k-v' }, pre && sq === 'm-take' ? el('small', {}, pre) : null, i < 0 ? t : t.slice(0, i), i < 0 ? null : el('small', {}, t.slice(i))),
        el('span', { class: 'k-s' }, sub));
    };
    const kids = [
      tile('m-take', wTake(), m.take, m.img.takeN ? 'Daten ' + size(data) + ', ' + plural(m.img.takeN, 'Image', 'Images') + ' ' + size(m.img.take) : alltag() ? 'kommt ins Backup, wenn du eins erstellst' : 'wird gesichert und wieder eingerichtet'),
      tile('m-arch', 'Im Archiv', m.archive, plural(m.archived, 'Eintrag', 'Einträge') + ', holst du bei Bedarf zurück'),
      m.left !== null ? tile('m-left', wLeft(), m.left, 'wird nicht gesichert') : null,
      el('div', { class: 'ls-kpi' + (openN ? ' warn' : '') },
        el('span', { class: 'k-l' }, el('span', {}, 'Offen')),
        el('b', { class: 'k-v' }, String(openN)),
        el('span', { class: 'k-s' }, openN ? 'vor dem Backup alle beantworten' : 'nichts offen')),
    ];
    return el('div', { class: 'ls-kpis' }, kids);
  }
  function moveCard() {
    const m = moveSummary();
    const pre = m.unknown ? 'mind. ' : '';
    const bar = el('div', { class: 'mbar', role: 'img', 'aria-label': wTake() + ' ' + pre + size(m.take) + ', im Archiv ' + size(m.archive) + (m.left !== null ? ', ' + wLeft().toLowerCase() + ' ' + size(m.left) : '') });
    for (const [cls, v] of [['m-take', m.take], ['m-arch', m.archive], ['m-left', m.left || 0]]) {
      if (v <= 0) continue;
      const s = el('span', { class: cls });
      s.style.flexGrow = String(v); // CSSOM; ein style-Attribut verbietet die CSP
      bar.append(s);
    }
    return el('section', { class: 'ls-sec ax-move', 'aria-labelledby': 'mv-h' },
      el('header', { class: 'ls-sec-h' }, el('h2', { id: 'mv-h' }, 'Was mit deinen Daten passiert'),
        el('span', { class: 'sub' }, 'Anteile an dem, was HostDeck gefunden hat')),
      el('div', { class: 'ls-sec-b' }, bar,
        m.regrow !== null ? el('p', { class: 'note regrow' }, 'Nicht mitgezählt: ' + size(m.regrow) + (alltag() ? ' entstehen beim Wiederherstellen von selbst neu. ' : ' entstehen auf dem neuen Server von selbst neu. ') +
          (m.img.pulled ? 'Docker lädt ' + plural(m.img.pulled, 'Image', 'Images') + ' aus dem Netz, das' : 'Das') + ' System wird frisch installiert.') : null,
        diskBox()));
  }

  function diskBox() {
    const sp = server.space;
    if (!sp || !sp.total) return null;
    const parts = [
      ['data', 'Daten', sp.data + sp.volumes, 'Projekte, Volumes und Ordner'],
      ['docker', 'Docker-Images und Build-Cache', sp.docker, 'lässt sich neu laden oder bauen; lokale Images werden bei den Projekten gesichert'],
      ['system', 'System', sp.system, 'Programme, Logs, Paket-Caches; kommt mit dem neuen System'],
      ['swap', 'Swap', sp.swap, 'wird neu angelegt'],
      ['other', 'Kleinteiliges', sp.other, 'kleine Dateien an anderen Orten'],
    ].filter(p => p[2] > 0);
    const bar = el('div', { class: 'dbar', role: 'img', 'aria-label': 'Belegung der Platte: ' + parts.map(p => p[1] + ' ' + size(p[2])).join(', ') },
      parts.map(p => {
        const s = el('span', { class: 'd-' + p[0] });
        s.style.width = Math.min(100, 100 * p[2] / sp.total).toFixed(2) + '%';
        return s;
      }));
    return details('disk', 'Platte ansehen', el('div', { class: 'fbody' },
      el('p', { class: 'facts' }, 'Platte: ' + size(sp.used) + ' von ' + size(sp.total) + ' belegt' + (sp.partial ? ' (unvollständig gezählt)' : '')),
      bar,
      el('ul', { class: 'dleg' }, parts.map(p => el('li', {}, el('i', { class: 'sq d-' + p[0] }), el('b', {}, p[1] + ' ' + size(p[2])), el('span', { class: 'note' }, p[3]))))), 'fold disk');
  }

  function countText(list, val, words) {
    const parts = [];
    for (const v of ['take', 'archive', 'skip', 'open']) {
      const n = list.filter(x => val(x) === v).length;
      if (n) parts.push(n + ' ' + words[v]);
    }
    return parts.join(', ') || 'nichts';
  }

  function answerRows() {
    const rows = [];
    const W = { take: 'übernehmen', archive: 'archivieren', skip: 'weglassen', open: 'offen' };
    if (work.projects.length) {
      const P = work.projects;
      const c = v => P.filter(p => p.decision === v);
      const parts = [];
      if (c('take').length) parts.push(c('take').length + (alltag() ? ' im Backup' : ' ziehen um'));
      if (c('archive').length) parts.push(c('archive').length + ' kommen ins Archiv');
      if (c('skip').length) parts.push(c('skip').length + (alltag() ? ' nicht im Backup (' : ' bleiben zurück (') + shortList(c('skip').map(p => p.name), 3) + ')');
      if (c('open').length) parts.push(c('open').length + ' noch offen');
      rows.push(['Projekte', parts.join(', '), 'Ändern', '#/auswahl/bereich/projekte']);
      const svcs = P.filter(p => p.decision === 'take' || p.decision === 'archive').flatMap(p => p.services);
      if (svcs.length) {
        const img = imageStats();
        const saved = new Set(svcs.filter(s => s.image === 'image-save').map(s => s.image_id)).size;
        const skipped = svcs.filter(s => s.image === 'skip').length;
        const parts2 = [];
        if (saved) parts2.push(saved + ' mitnehmen (' + size(img.take + img.archive) + ')');
        if (img.pulled) parts2.push(img.pulled + ' aus dem Netz neu laden');
        if (skipped) parts2.push(plural(skipped, 'Dienst', 'Dienste') + ' weglassen');
        rows.push(['Images', parts2.join(', ') || 'keine', 'Ändern', '#/auswahl/bereich/projekte']);
      }
    }
    if (work.containers.length) rows.push(['Container ohne Compose-Projekt', countText(work.containers, c => c.decision, W), 'Ändern', '#/auswahl/bereich/projekte']);
    const daten = looseOf('daten');
    if (daten.length) rows.push(['Daten ohne Projekt', countText(daten, looseValue, W), 'Ändern', '#/auswahl/bereich/daten']);
    const srv = looseOf('server');
    if (srv.length) {
      let text;
      if (srv.length <= 4) {
        const parts = [];
        for (const v of ['take', 'archive', 'skip', 'open']) {
          const ls = srv.filter(d => looseValue(d) === v).map(d => el('span', { class: 'mono' }, d.label));
          if (!ls.length) continue;
          const joined = [];
          ls.forEach((s, k) => { if (k) joined.push(k === ls.length - 1 ? ' und ' : ', '); joined.push(s); });
          parts.push([joined, ' ' + W[v]]);
        }
        text = parts.map((p, k) => [k ? ', ' : '', p]);
      } else text = countText(srv, looseValue, W);
      rows.push(['Am Server selbst', text, 'Ändern', '#/auswahl/bereich/server']);
    }
    if (work.caddy) {
      const cd = work.caddy;
      const dec = { take: 'wird übernommen', archive: 'wird archiviert', skip: 'wird weggelassen', open: 'noch nicht entschieden' }[cd.decision] || cd.decision;
      rows.push(['Caddy', plural(cd.routes.length, 'Route', 'Routen') + deadText(cd.routes) + '; ' + dec, 'Ansehen', '#/auswahl/bereich/caddy']);
    }
    if (server.findings.length) {
      const w = server.findings.filter(f => f.level === 'warnung').length;
      rows.push(['Warnungen', plural(w, 'Warnung', 'Warnungen') + ' und ' + plural(server.findings.length - w, 'Hinweis', 'Hinweise') + ', zum Lesen', 'Lesen', '#/auswahl/bereich/warnungen']);
    }
    if (server.checks.length + server.tasks.length) {
      rows.push(['Für den neuen Server', [server.checks.length ? 'Checkliste mit ' + plural(server.checks.length, 'Punkt', 'Punkten') : '', server.tasks.length ? plural(server.tasks.length, 'zeitgesteuerte Aufgabe', 'zeitgesteuerte Aufgaben') : ''].filter(Boolean).join(', '), 'Ansehen', '#/auswahl/bereich/neuer-server']);
    }
    if (work.gone.length) {
      const n = work.gone.filter(g => g.remove).length;
      const qi = questions.findIndex(q => q.type === 'gone');
      rows.push(['Nicht mehr gefunden', plural(work.gone.length, 'Eintrag', 'Einträge') + ', ' + (n ? n + ' zum Entfernen markiert' : 'keiner zum Entfernen markiert'), 'Ansehen', qi >= 0 ? '#/auswahl/fragen/' + (qi + 1) : '#/auswahl']);
    }
    return rows;
  }

  function viewOverview() {
    const open = openList();
    const dirty = edits().length > 0 || mustSave();
    const goneLeft = work.gone.filter(g => !g.remove).length;
    const N = questions.length;
    // Nichts mehr offen heißt beantwortet, auch wenn die Antworten über den
    // Reiter Projekte oder aus einer früheren Sitzung kamen.
    const answered = !open.length && !goneLeft ? N : Math.min(N, questions.filter(q => ui.answered.has(q.id)).length);
    let status;
    if (open.length) {
      status = el('div', { class: 'sband s-warn' }, el('p', {}, icon('warn', 18), el('span', {}, el('b', {}, 'Noch ' + open.length + ' offen. '), 'Speichern geht trotzdem, vor dem Backup müssen alle beantwortet sein.')),
        el('a', { class: 'btn btn-sm btn-warn', href: locateOpen(open[0]) }, 'Zum nächsten offenen Punkt'));
    } else if (goneLeft) {
      const qi = questions.findIndex(q => q.type === 'gone');
      status = el('div', { class: 'sband s-warn' }, el('p', {}, icon('warn', 18), el('span', {}, el('b', {}, plural(goneLeft, 'Eintrag gibt', 'Einträge gibt') + ' es nicht mehr. '), 'Entfernen und speichern; so lange startet das Backup nicht.')),
        el('a', { class: 'btn btn-sm btn-warn', href: qi >= 0 ? '#/auswahl/fragen/' + (qi + 1) : '#/auswahl' }, 'Zur Frage'));
    } else if (dirty) {
      status = el('div', { class: 'sband s-ok' }, el('p', {}, icon('check', 18), el('span', {}, el('b', {}, 'Nichts mehr offen. '), 'Jetzt noch speichern, dann ist die Auswahl fertig fürs Backup.')),
        el('button', { class: 'btn btn-p btn-sm hide-m', type: 'button', onclick: save }, 'Speichern'));
    } else {
      status = el('div', { class: 'sband s-ok' }, el('p', {}, icon('check', 18), el('span', {}, el('b', {}, 'Alles gespeichert. '), 'Die Auswahl ist fertig fürs Backup.')));
    }
    const m = moveSummary();
    const pre = m.unknown ? 'mind. ' : '';
    // Technisches steht hinter dem „?“ (Idee 13), nicht unter der Seite.
    const tech = 'Hier entscheidest du, was ins Backup kommt. Speichern (oben in der Leiste oder Strg+S) legt manifest.yaml neben der Bestandsaufnahme an. '
      + (svc ? 'Der Leitstand zeigt die Bestandsaufnahme und schreibt nur manifest.yaml. Neu erfassen geht unter Einstellungen › Leitstand. '
        : 'hostdeck serve zeigt die Ergebnisse von hostdeck discover und schreibt nur manifest.yaml. Neue Bestandsaufnahme: sudo ./hostdeck discover, danach neu laden. ') + (server.tool || '');
    const hp = LS && LS.helpParts ? LS.helpParts('auswahl', 'Auswahl', tech) : null;
    const page = el('div', { class: 'wrap stack ax-ov' },
      el('header', { class: 'ls-head' },
        el('div', { class: 'ls-head-t' },
          el('div', { class: 'ls-title' }, el('h1', { tabindex: '-1' }, 'Auswahl'), server.created ? el('span', { class: 'ls-meta' }, 'Stand ' + fmtDate(server.created)) : null, hp ? hp[0] : null),
          el('p', { class: 'ls-status' }, alltag() ? 'Ins Backup kommen ' : 'Auf den neuen Server kommen ', el('b', {}, pre + size(m.take)), '.',
            m.archive ? [' Ins Archiv ', el('b', {}, size(m.archive)), '.'] : null,
            m.left !== null ? [' ', el('b', {}, size(m.left)), alltag() ? ' nicht.' : ' bleiben zurück.'] : null),
          hp ? hp[1] : null),
        N ? el('div', { class: 'ls-acts' }, el('a', { class: 'btn btn-sm', href: '#/auswahl/fragen/' + (answered === N ? 1 : firstUnanswered()) }, icon('next', 16), answered === N ? 'Fragen nochmal durchgehen' : 'Fragen weiter beantworten')) : null),
      moveTiles(open.length),
      banners(true), saveErrorBand(), status,
      el('div', { class: 'ax-cols' },
        el('section', { class: 'ls-sec flush', 'aria-labelledby': 'aw-h' },
          el('header', { class: 'ls-sec-h' }, el('h2', { id: 'aw-h' }, 'Deine Antworten'), el('span', { class: 'sub' }, 'je Bereich, was du entschieden hast')),
          el('div', { class: 'ls-sec-b' }, el('ul', { class: 'answers' }, answerRows().map(([t, text, link, href]) =>
            el('li', {}, el('b', {}, t), el('span', {}, text), el('a', { class: 'btn btn-sm', href, 'aria-label': t + ' ' + link.toLowerCase() }, link)))))),
        moveCard()),
      el('div', { class: 'ovfoot' },
        el('p', {}, hp ? null : tech),
        el('div', { class: 'acts' },
          el('button', { class: 'btn btn-sm', type: 'button', onclick: reload }, icon('refresh', 16), 'Neu laden'))));
    const bar = el('div', { class: 'fbar' }, el('button', { class: 'btn btn-p grow', type: 'button', onclick: save, disabled: !dirty }, 'Speichern'));
    return [page, bar];
  }

  // ---------- Bereiche mit Kisten ----------

  const AREA_TEXT = {
    projekte: ['Projekte', 'Jedes Projekt bringt seine Daten und Images mit. Ein Klick auf ein Projekt zeigt die Details.'],
    daten: ['Daten ohne Projekt', 'Volumes und Ordner, die gerade kein laufendes Projekt nutzt. Leg sie in die passende Kiste. Gruppen kannst du als Ganzes verschieben.'],
    server: ['Am Server selbst', 'Home-Ordner, eigene Programme, Webseiten und Einstellungen außerhalb von Docker. Vorschlag: archivieren, damit nichts verloren geht; auf den neuen Server kommt davon nur, was du dort selbst einrichtest.'],
  };
  const EMPTY = {
    take: ['Projekte, die auf dem neuen Server laufen sollen', 'was auf dem neuen Server wieder an seinen Platz soll'],
    archive: ['Projekte, die du nur aufheben willst', 'was du nur aufheben willst'],
    skip: ['Projekte, die nicht gesichert werden', 'was nicht gesichert werden soll'],
    open: ['Projekte, die noch offen sind', 'was noch offen ist'],
  };

  function areaHead(title, lead, meta) {
    return [el('a', { class: 'backl', href: '#/auswahl' }, '← Auswahl-Übersicht'),
      el('h1', { class: 'h1', tabindex: '-1' }, title),
      lead ? el('p', { class: 'lead' }, lead) : null,
      meta ? el('p', { class: 'facts' }, meta) : null];
  }

  function itemName(it) { return it.o.name || it.o.label; }

  // Verschieben: Menü mit den Kisten; Weglassen fragt nach dem Grund.
  function moveControl(ufid, label, cur, allowed, apply, nextFid) {
    const open = ui.menu === ufid;
    const closeMenu = () => { ui.menu = null; rerender(); focusFid(ufid + '-mv'); };
    const pick = v => {
      ui.menu = null;
      if (v === cur) { closeMenu(); return; }
      if (v === 'skip') { ui.form = ufid; ui.formVal = undefined; rerender(); focusFid(ufid + '-why'); return; }
      apply(v);
      announce(label + ' liegt jetzt in ' + KISTEN[v][0]);
      rerender();
      nextFid(v);
    };
    const btn = el('button', {
      type: 'button', class: 'mv', 'data-fid': ufid + '-mv', 'aria-haspopup': 'menu', 'aria-expanded': String(open),
      'aria-controls': open ? ufid + '-menu' : null, 'aria-label': 'Verschieben: ' + label + ', liegt in ' + KISTEN[cur][0],
      onclick: () => { ui.menu = open ? null : ufid; ui.form = null; rerender(); if (!open) focusMenu(ufid); else focusFid(ufid + '-mv'); },
      onkeydown: e => { if ((e.key === 'ArrowDown' || e.key === 'ArrowUp') && !open) { e.preventDefault(); ui.menu = ufid; rerender(); focusMenu(ufid); } },
    }, 'Verschieben', icon('down', 14));
    const menu = open ? el('div', { class: 'menu', role: 'menu', id: ufid + '-menu', 'aria-label': label + ' verschieben nach', onkeydown: e => menuKeys(e, closeMenu) },
      allowed.map(v => el('button', { type: 'button', class: 'mi', role: 'menuitemradio', 'aria-checked': String(v === cur), tabindex: '-1', onclick: () => pick(v) },
        icon('check', 18), el('span', {}, KISTEN[v][0])))) : null;
    let form = null;
    if (ui.form === ufid) {
      const fid = ufid + '-why';
      const eid = fid + '-e';
      const initial = apply.reason ? apply.reason() : '';
      const bad = ui.errs.has(fid);
      const cancel = () => { ui.form = null; ui.formVal = undefined; ui.errs.delete(fid); rerender(); focusFid(ufid + '-mv'); };
      const submit = () => {
        const v = (ui.formVal === undefined ? initial : ui.formVal).trim();
        if (!v) { flagField(fid); return; }
        ui.form = null; ui.formVal = undefined; ui.errs.delete(fid);
        apply('skip', v);
        announce(label + ' liegt jetzt in Weglassen');
        rerender();
        nextFid('skip');
      };
      form = el('div', { class: 'mform' },
        el('label', { for: fid }, 'Warum weglassen?'),
        el('input', {
          type: 'text', class: 'fld', id: fid, 'data-fid': fid, maxlength: 500, autocomplete: 'off', value: ui.formVal === undefined ? initial : ui.formVal,
          placeholder: 'Pflicht, z. B. „wird nicht mehr gebraucht“', 'aria-invalid': bad ? 'true' : null, 'aria-describedby': bad ? eid : null,
          oninput: e => { ui.formVal = e.target.value; },
          onkeydown: e => { if (e.key === 'Enter') { e.preventDefault(); submit(); } else if (e.key === 'Escape') { e.preventDefault(); cancel(); } },
        }),
        bad ? el('p', { class: 'ferr', id: eid }, NEED_REASON) : null,
        el('div', { class: 'acts' },
          el('button', { class: 'btn btn-p btn-sm', type: 'button', onclick: submit }, 'Weglassen'),
          el('button', { class: 'btn btn-sm', type: 'button', onclick: cancel }, 'Abbrechen')));
    }
    return { btn, menu, form, active: open || ui.form === ufid };
  }

  function focusMenu(ufid) {
    const m = document.getElementById(ufid + '-menu');
    if (m) (m.querySelector('[aria-checked="true"]') || m.querySelector('.mi')).focus();
  }

  function focusFid(fid) {
    const e = document.querySelector('[data-fid="' + fid + '"]');
    if (e && e.offsetParent !== null) { e.focus({ preventScroll: false }); return true; }
    return false;
  }

  function openDetails(area, key, fid) {
    ui.returnFid = fid;
    ui.pushed = true;
    ui.menu = null;
    ui.form = null;
    location.hash = detailsHash(area, key);
  }

  function looseMeta(o, inGroup) {
    const cm = [];
    if (o.engine) cm.push(engineText(o));
    else if (o.type === 'volume') cm.push('Volume');
    else if (o.type === 'anon') cm.push('anonymes Volume');
    else cm.push(o.kind === 'config' ? 'Datei' : 'Ordner');
    if (o.area) cm.push(AREAS[o.area] || o.area);
    if (inGroup) cm.push('Gruppe ' + groupName(inGroup.k));
    else if (o.former_project) cm.push('gehörte zu ' + o.former_project);
    return cm.join(' · ');
  }

  function whyLine(list) {
    const reasons = new Set(list.map(o => (o.reason || '').trim()));
    if (reasons.has('')) return warnLine('Grund fehlt');
    if (reasons.size > 1) return el('p', { class: 'why' }, el('span', {}, 'Grund: '), 'unterschiedlich');
    return el('p', { class: 'why' }, el('span', {}, 'Grund: '), [...reasons][0]);
  }

  // Eine Karte in einer Kiste: ein Eintrag oder eine Gruppe.
  function kisteCard(area, c, bucket, drawerKey) {
    const isProj = area === 'projekte';
    const refocus = (ufidFor) => v => { if (!focusFid(ufidFor(v) + '-mv')) { const t = document.querySelector('[data-fid="tab-' + v + '"]'); if (t && t.offsetParent !== null) t.focus(); } };
    if (c.g) {
      const g = c.g;
      const list = c.members;
      const ufid = 'g' + idFor(g.id) + bucket;
      const label = groupName(g.k);
      const noun = memberNoun(list);
      const allowed = ['take', 'archive', 'skip'].filter(v => !(v === 'take' && list.some(d => d.area === 'einstellungen')));
      const apply = (v, reason) => { for (const d of list) { setLoose(d, v); if (reason) d.reason = reason; } };
      const rs = new Set(list.map(d => d.reason || ''));
      apply.reason = () => rs.size === 1 ? [...rs][0] : '';
      const mc = moveControl(ufid, label, bucket, allowed, apply, refocus(v => list.length >= 2 ? 'g' + idFor(g.id) + v : 'u' + idFor(list[0].key)));
      const open = ui.expanded.has(g.id) || !!(ui.search[area] || '').trim();
      const nfid = ufid + '-n';
      return el('li', { class: 'kc' + (mc.active ? ' sel' : '') },
        el('div', { class: 'ct' },
          el('button', { type: 'button', class: 'nb', 'data-fid': nfid, 'aria-expanded': String(open),
            onclick: () => { if (ui.expanded.has(g.id)) ui.expanded.delete(g.id); else ui.expanded.add(g.id); rerender(); } },
          icon('right', 14), label),
          el('span', { class: 'sz' }, sizeSum(sum(list)))),
        el('div', { class: 'tags' }, el('span', { class: 'cnt' }, plural(list.length, noun[0], noun[1])), list.some(d => d.new) ? el('span', { class: 'tag t-new' }, 'neu') : null),
        el('div', { class: 'ca' }, el('span', { class: 'cm' }, groupKind(g.k, list)), mc.btn, mc.menu),
        bucket === 'skip' ? whyLine(list) : null,
        mc.form,
        open ? el('ul', { class: 'sub' }, list.map(d => {
          const f = 'm' + idFor(d.key);
          return el('li', {}, el('button', { type: 'button', class: 'nb', 'data-fid': f, 'aria-label': d.label + ', Details', onclick: () => openDetails(area, d.key, f) }, memberLabel(g.k, d)),
            el('span', { class: 'z' }, size(d.size)));
        })) : null);
    }
    const it = c.it;
    const o = it.o;
    const ufid = 'u' + idFor(o.key);
    const label = itemName(it);
    const allowed = ['take', 'archive', 'skip'].filter(v => !(v === 'take' && o.area === 'einstellungen'));
    const apply = (v, reason) => {
      if (it.kind === 'loose') setLoose(o, v); else o.decision = v;
      if (reason) o.reason = reason;
    };
    apply.reason = () => o.reason || '';
    const mc = moveControl(ufid, label, bucket, allowed, apply, refocus(() => ufid));
    const nfid = ufid + '-n';
    const tags = [newTag(o)];
    const warns = [];
    let meta;
    if (it.kind === 'project') {
      meta = [o.running + ' von ' + o.total + ' laufen', o.domains[0]].filter(Boolean).join(' · ');
      if (o.git && o.git.dirty) warns.push(plural(o.git.dirty, 'Git-Änderung', 'Git-Änderungen'));
      if (!o.dir_exists) warns.push('Compose-Ordner fehlt');
      const od = ownerOpen(o) - (o.decision === 'open' ? 1 : 0);
      if (od) warns.push(plural(od, 'Datenordner', 'Datenordner') + ' offen');
    } else if (it.kind === 'container') {
      tags.push(el('span', { class: 'tag t-mut' }, 'Container'));
      meta = o.running ? 'läuft' : o.state;
      const od = ownerOpen(o) - (o.decision === 'open' ? 1 : 0);
      if (od) warns.push(plural(od, 'Datenordner', 'Datenordner') + ' offen');
    } else {
      meta = looseMeta(o, c.inGroup);
      if (o.git && o.git.dirty) warns.push(plural(o.git.dirty, 'Änderung', 'Änderungen') + ' nicht committet');
      if (!o.exists) warns.push('nicht gefunden');
      if (o.area === 'fund') tags.push(el('span', { class: 'tag t-open' }, 'außerhalb gefunden'));
    }
    return el('li', { class: 'kc' + (mc.active || drawerKey === o.key ? ' sel' : '') },
      el('div', { class: 'ct' },
        el('button', { type: 'button', class: 'nb', 'data-fid': nfid, onclick: () => openDetails(area, o.key, nfid) }, label),
        el('span', { class: 'sz' }, size(isProj ? itemBytes(it) : o.size))),
      tags.some(Boolean) ? el('div', { class: 'tags' }, tags) : null,
      el('div', { class: 'ca' }, el('span', { class: 'cm' }, meta), mc.btn, mc.menu),
      warns.map(warnLine),
      bucket === 'skip' ? whyLine([o]) : null,
      mc.form);
  }

  let searchTimer = 0;

  function viewKisten(area, drawerKey) {
    const isProj = area === 'projekte';
    const items = isProj ? projectItems() : looseOf(area).map(o => ({ o, kind: 'loose' }));
    const grouped = !isProj && ui.grouped;
    const q = (ui.search[area] || '').trim().toLowerCase();
    const hit = o => !q || [o.name, o.label, o.path, o.image].some(s => s && s.toLowerCase().includes(q));
    const us = isProj ? items.map(it => ({ it })) : units(items.map(x => x.o), grouped).map(u => u.members ? { g: u } : { it: { o: u.d, kind: 'loose' } });
    const hasOpen = items.some(it => bucketOf(it) === 'open');
    const buckets = (hasOpen ? ['open'] : []).concat(['take', 'archive', 'skip']);
    const kisten = {};
    let found = 0;
    for (const b of buckets) {
      const all = items.filter(it => bucketOf(it) === b);
      const cards = [];
      for (const u of us) {
        if (u.it) {
          if (bucketOf(u.it) === b && hit(u.it.o)) cards.push({ it: u.it, size: itemBytes(u.it) });
          continue;
        }
        const mem = u.g.members.filter(d => looseValue(d) === b);
        const shown = q && !groupName(u.g.k).toLowerCase().includes(q) ? mem.filter(hit) : mem;
        if (!shown.length) continue;
        if (mem.length >= 2) cards.push({ g: u.g, members: shown, size: sum(shown).n });
        else cards.push({ it: { o: mem[0], kind: 'loose' }, inGroup: u.g, size: mem[0].size });
      }
      const name = c => (c.g ? groupName(c.g.k) : itemName(c.it)).toLowerCase();
      const fresh = c => c.g ? c.members.some(d => d.new) : !!c.it.o.new;
      if (ui.sort === 'name') cards.sort((a, b2) => name(a).localeCompare(name(b2), 'de'));
      else cards.sort((a, b2) => (ui.sort === 'new' ? Number(fresh(b2)) - Number(fresh(a)) : 0) || (b2.size - a.size));
      found += cards.reduce((n, c) => n + (c.g ? c.members.length : 1), 0);
      kisten[b] = { all, cards, total: sum(all, itemBytes) };
    }
    if (q) {
      clearTimeout(searchTimer);
      searchTimer = setTimeout(() => announce(plural(found, 'Eintrag', 'Einträge') + ' gefunden'), 500);
    }
    const counts = b => kisten[b].all.length;
    let cur = ui.tab[area];
    if (!buckets.includes(cur)) cur = hasOpen ? 'open' : buckets.slice().sort((a, b) => counts(b) - counts(a))[0];
    const m = moveSummary();
    const allSize = sum(items, itemBytes);
    const groupsN = grouped ? us.filter(u => u.g).length : 0;
    const openN = items.filter(it => bucketOf(it) === 'open').length;
    const meta = [isProj ? plural(work.projects.length, 'Projekt', 'Projekte') + (work.containers.length ? ' und ' + plural(work.containers.length, 'Container', 'Container') : '')
      : plural(items.length, 'Eintrag', 'Einträge') + (groupsN ? ' in ' + plural(groupsN, 'Gruppe', 'Gruppen') : ''), sizeSum(allSize), openN ? 'hier ' + openN + ' offen' : 'hier nichts offen'].join(' · ');

    const tools = el('div', { class: 'tools' },
      isProj ? null : el('label', { class: 'chk grp' }, el('input', { type: 'checkbox', checked: ui.grouped, 'data-fid': 't-grp', onchange: e => { ui.grouped = e.target.checked; rerender(); } }), 'Nach Herkunft gruppieren'),
      el('label', { class: 'sortl' }, 'Sortieren',
        el('select', { class: 'ctl', 'data-fid': 't-sort', onchange: e => { ui.sort = e.target.value; rerender(); } },
          [['size', 'Größe, große zuerst'], ['name', 'Name'], ['new', 'Neu zuerst']].map(([v, l]) => el('option', { value: v, selected: ui.sort === v }, l)))),
      el('div', { class: 'search' }, el('label', { class: 'sr', for: 't-q' }, 'Name oder Pfad suchen'),
        el('input', { type: 'search', id: 't-q', class: 'ctl', 'data-fid': 't-q', placeholder: 'Name oder Pfad suchen', autocomplete: 'off', value: ui.search[area] || '',
          oninput: e => { ui.search[area] = e.target.value; rerender(); } })));

    const tabs = el('div', { class: 'ktabs' + (buckets.length === 4 ? ' k4' : ''), role: 'tablist', 'aria-label': 'Kisten' },
      buckets.map((b, k) => el('button', {
        type: 'button', role: 'tab', class: 'ktab k-' + b, id: 'tab-' + b, 'data-fid': 'tab-' + b, 'aria-selected': String(b === cur), 'aria-controls': 'kiste-' + b, tabindex: b === cur ? '0' : '-1',
        onclick: () => { ui.tab[area] = b; rerender(); },
        onkeydown: e => {
          const d = e.key === 'ArrowRight' ? 1 : e.key === 'ArrowLeft' ? -1 : 0;
          if (!d) return;
          e.preventDefault();
          ui.tab[area] = buckets[(k + d + buckets.length) % buckets.length];
          rerender();
          focusFid('tab-' + ui.tab[area]);
        },
      }, el('b', {}, KISTEN[b][0]), el('span', { class: 'w' }, String(counts(b))), el('span', { class: 'z' }, sizeSum(kisten[b].total)))));

    const boxes = el('div', { class: 'kisten' + (buckets.length === 4 ? ' k4' : '') }, buckets.map(b => {
      const k = kisten[b];
      const hid = 'kh-' + b;
      const desc = isProj && b === 'take' ? 'Wird gesichert und auf dem neuen Server gestartet.' : isProj && b === 'archive' ? 'Wird gesichert, aber nicht gestartet.' : KISTEN[b][1];
      const key = area + ':' + b;
      const shown = ui.showAll.has(key) ? k.cards : k.cards.slice(0, PER_KISTE);
      const rest = k.cards.slice(shown.length);
      let body;
      if (!k.all.length) body = el('p', { class: 'kempty' }, 'Leer. Hierher ' + (isProj ? 'kommen ' + EMPTY[b][0] : 'kommt, ' + EMPTY[b][1]) + '.');
      else if (!k.cards.length) body = el('div', { class: 'nomatch' }, 'Kein Eintrag passt zu „' + (ui.search[area] || '').trim() + '“.', el('button', { class: 'lk', type: 'button', onclick: () => { ui.search[area] = ''; rerender(); focusFid('t-q'); } }, 'Suche leeren'));
      else body = el('ul', { class: 'cards' }, shown.map(c => kisteCard(area, c, b, drawerKey)));
      return el('section', { class: 'kiste k-' + b + (b === cur ? ' cur' : ''), id: 'kiste-' + b, role: 'tabpanel', 'aria-labelledby': hid },
        el('div', { class: 'kh' },
          el('h2', { id: hid }, KISTEN[b][0], el('span', { class: 'sr' }, ', ' + plural(k.all.length, 'Eintrag', 'Einträge') + ', ' + sizeSum(k.total))),
          el('div', { class: 'w', 'aria-hidden': 'true' }, k.all.length + ' · ' + sizeSum(k.total)),
          el('p', {}, desc)),
        body,
        rest.length ? el('button', {
          class: 'lk more', type: 'button',
          onclick: () => { ui.showAll.add(key); rerender(); const c = document.querySelectorAll('#kiste-' + b + ' .cards > li')[PER_KISTE]; if (c) c.querySelector('button').focus(); },
        }, rest.length + ' weitere zeigen · ' + plural(rest.reduce((n, c) => n + (c.g ? c.members.length : 1), 0), 'Eintrag', 'Einträge') + ', ' + sizeSum(sum(rest, c => c.size))) : null);
    }));

    const extra = [];
    if (area === 'server') {
      if (server.system.length) {
        extra.push(details('system', 'Wird nie gesichert: ' + plural(server.system.length, 'Systempfad', 'Systempfade'), el('div', { class: 'fbody' },
          el('p', { class: 'note' }, 'Diese Pfade binden Container ein, sie gehören aber zum System und entstehen auf dem neuen Server neu.'),
          el('ul', { class: 'mlist' }, server.system.map(s => el('li', {}, el('span', { class: 'mono' }, s.source), el('span', { class: 'z' }, s.container)))))));
      }
      if (server.settings.length) {
        const files = server.settings.reduce((n, g) => n + g.modified.length + g.added.length, 0);
        extra.push(details('settings', 'Geänderte oder eigene Dateien in /etc (' + files + ')', el('div', { class: 'fbody' },
          el('p', { class: 'note' }, 'Geändert heißt: anders als vom Paket mitgebracht. Eigene: keinem Paket zugeordnet. Alles liegt im Archiv von /etc zum Nachschlagen.'),
          el('ul', { class: 'card clist' }, server.settings.map(g => el('li', {}, el('b', { class: 'mono' }, g.dir + '/'),
            g.modified.length ? el('span', { class: 'note' }, 'geändert: ' + g.modified.join(', ')) : null,
            g.added.length ? el('span', { class: 'note' }, 'eigene: ' + shortList(g.added, 8)) : null))))));
      }
    }
    const text = AREA_TEXT[area];
    const page = el('div', { class: 'wrap' }, el('div', { class: 'alayout' },
      el('div', { class: 'amain' },
        el('div', { class: 'stack' }, areaHead(text[0], text[1] + (isProj && work.containers.length ? ' Container ohne Compose-Projekt tragen die Marke „Container“.' : ''), meta)),
        saveErrorBand(), numsList(m, 'nums-row'), tools, tabs, boxes, extra),
      el('aside', { class: 'aside', 'aria-label': 'Umzug in Zahlen' }, el('h2', { class: 'lbl' }, 'Umzug in Zahlen'), numsList(m),
        el('div', { class: 'sep' }, openState(), el('a', { href: '#/auswahl' }, 'Zur Auswahl-Übersicht und speichern')))));
    const bar = el('div', { class: 'fbar' }, el('span', { class: 'txt' }, size(m.take) + ' mit · ' + size(m.archive) + ' Archiv'), el('a', { class: 'btn btn-sm', href: '#/auswahl' }, 'Auswahl-Übersicht'));
    return [page, bar];
  }

  // ---------- Lesebereiche ----------

  function viewCaddy() {
    const cd = work.caddy;
    const card = el('section', { class: 'card pad stack', 'aria-label': 'Entscheidung für Caddy' },
      el('div', { class: 'dbar2' }, seg('caddy', 'Entscheidung für Caddy', ['take', 'archive', 'skip'].map(v => [v, TILE[v][0]]), cd.decision, v => { cd.decision = v; rerender(); }),
        el('span', { class: 'km' }, cd.decision === 'open' ? 'Noch offen' : TILE_PROJECT[cd.decision][1])),
      cd.decision === 'skip' ? reasonField(cd, {}) : null,
      cd.note ? el('p', { class: 'note' }, cd.note) : null,
      cd.error ? el('p', { class: 'errline' }, 'caddy adapt: ' + cd.error) : null);
    const rows = cd.routes.map(r => {
      const st = routeInfo(r);
      return el('tr', {},
        el('td', {}, r.hosts.length ? r.hosts.map((h, i) => [i ? ', ' : '', domainLink(h)]) : '(alle)'),
        el('td', {}, r.target || '', r.upstream ? el('div', { class: 'note mono' }, r.upstream) : null),
        el('td', {}, el('span', { class: 'pill ' + st.cls }, st.word), st.note ? el('div', { class: 'note' }, st.note) : null));
    });
    return [el('div', { class: 'wrap wrap-n stack' },
      el('div', { class: 'stack' }, areaHead('Caddy', 'Wird nach den Projektordnern wiederhergestellt, weil Caddy Dateien daraus importiert.',
        [el('span', { class: 'mono' }, cd.config), ' · ' + plural(cd.routes.length, 'Route', 'Routen') + deadText(cd.routes)])),
      saveErrorBand(), card,
      el('section', { class: 'sec', 'aria-labelledby': 'rt-h' }, el('h2', { class: 'h2', id: 'rt-h' }, 'Routen'),
        el('div', { class: 'tablewrap', tabindex: '0', role: 'region', 'aria-labelledby': 'rt-h' }, el('table', {},
          el('thead', {}, el('tr', {}, el('th', { scope: 'col' }, 'Adresse'), el('th', { scope: 'col' }, 'Ziel'), el('th', { scope: 'col' }, 'Zustand'))),
          el('tbody', {}, rows)))),
      cd.imports.length ? el('section', { class: 'sec', 'aria-labelledby': 'im-h' }, el('h2', { class: 'h2', id: 'im-h' }, 'Importierte Dateien'),
        el('ul', { class: 'card clist' }, cd.imports.map(i => el('li', { class: 'mono' }, i)))) : null)];
  }

  function viewFindings() {
    const f = server.findings.slice().sort((a, b) => (a.level === 'warnung' ? 0 : 1) - (b.level === 'warnung' ? 0 : 1));
    const w = f.filter(x => x.level === 'warnung').length;
    return [el('div', { class: 'wrap wrap-n stack' },
      el('div', { class: 'stack' }, areaHead('Warnungen und Hinweise', 'Aus der Bestandsaufnahme. Sie halten das Speichern nicht auf; lies sie einmal durch.',
        plural(w, 'Warnung', 'Warnungen') + ' · ' + plural(f.length - w, 'Hinweis', 'Hinweise'))),
      el('ul', { class: 'card flist' }, f.map(x => el('li', {},
        el('span', { class: 'tag ' + (x.level === 'warnung' ? 't-open' : 't-mut') }, x.level === 'warnung' ? 'Warnung' : 'Hinweis'), el('span', {}, x.message)))))];
  }

  function viewNewServer() {
    const out = [el('div', { class: 'stack' }, areaHead('Für den neuen Server', 'Das kommt nicht als Datei mit, sondern wird auf dem neuen Server eingerichtet. Steht auch im Bericht zum Abhaken.', null))];
    if (server.checks.length) {
      out.push(el('section', { class: 'sec', 'aria-labelledby': 'ck-h' }, el('h2', { class: 'h2', id: 'ck-h' }, 'Checkliste · ' + server.checks.length),
        el('ul', { class: 'card clist' }, server.checks.map(c => el('li', {}, el('b', {}, c.title), c.detail ? el('span', { class: 'note' }, c.detail) : null)))));
    }
    if (server.tasks.length) {
      out.push(el('section', { class: 'sec', 'aria-labelledby': 'tk-h' }, el('h2', { class: 'h2', id: 'tk-h' }, 'Zeitgesteuerte Aufgaben und eigene Dienste · ' + server.tasks.length),
        el('p', { class: 'note' }, 'Sie liegen im Archiv von /etc und den Crontabs. Auf dem neuen Server von Hand einrichten, wenn sie noch gebraucht werden. Passwörter in Befehlen sind ausgeblendet.'),
        el('ul', { class: 'card clist' }, server.tasks.map(t => el('li', {},
          el('b', {}, (TASK_KINDS[t.kind] || t.kind) + ((t.when || t.schedule) ? ' · ' + (t.when || t.schedule) : '') + (t.description ? ' · ' + t.description : '')),
          el('span', { class: 'note mono' }, t.command || t.source),
          el('span', { class: 'note' }, t.source + (t.timer ? ' und ' + t.timer : '') + ((t.kind !== 'cron' && !t.enabled) ? ' · nicht aktiviert' : '')))))));
    }
    return [el('div', { class: 'wrap wrap-n stack' }, out)];
  }

  // ---------- Details (Schublade) ----------

  function dsec(title, rows, one) {
    if (!rows.length) return null;
    return el('section', { class: 'dsec', 'aria-label': title }, el('h3', { class: 'lbl' }, title + (one ? '' : ' · ' + rows.length)), rows);
  }

  function splitReason(e, rel) {
    const fid = reasonFid(e);
    const bad = ui.errs.has(fid) && !hasReason(e);
    return [el('label', { class: 'z', for: fid }, 'Grund'),
      el('input', {
        type: 'text', class: 'fld', id: fid, 'data-fid': fid, value: e.reason || '', maxlength: 500, autocomplete: 'off', 'aria-label': 'Grund für ' + rel,
        'aria-invalid': bad ? 'true' : null,
        oninput: ev => { e.reason = ev.target.value; if (ev.target.value.trim()) { ui.errs.delete(fid); ev.target.removeAttribute('aria-invalid'); } updateStatus(); },
      }),
      bad ? el('p', { class: 'ferr' }, NEED_REASON) : null];
  }

  // Unterordner ausnehmen: die größten Unterordner zum Anhaken, dazu ein
  // freies Feld. Jeder Haken wird ein eigener path:-Eintrag mit skip.
  function splitBlock(parentKey, parentPath, largest, list) {
    const mine = list.filter(x => x.split_of === parentKey);
    const items = [];
    const seen = new Set();
    for (const l of largest || []) {
      const key = 'path:' + l.path;
      seen.add(key);
      items.push({ key, path: l.path, size: l.size, entry: mine.find(x => x.key === key) });
    }
    for (const s of mine) if (!seen.has(s.key)) items.push({ key: s.key, path: s.key.slice(5), size: s.size, entry: s });
    const box = el('div', { class: 'splits' + (items.length ? '' : ' quiet') });
    if (items.length) put(box, el('div', { class: 'st2' }, 'Unterordner nicht sichern'));
    for (const it of items) {
      const active = it.entry && !it.entry.removed;
      const rel = it.path.startsWith(parentPath + '/') ? it.path.slice(parentPath.length + 1) : it.path;
      put(box, el('div', { class: 'split' },
        el('label', { class: 'chk' },
          el('input', {
            type: 'checkbox', checked: active, 'data-fid': idFor(it.key) + '-split', onchange: e => {
              if (e.target.checked) {
                if (it.entry) it.entry.removed = false;
                else list.push({ key: it.key, type: 'path', label: it.path, split_of: parentKey, strategy: 'skip', reason: SPLIT_REASON, size: it.size, exists: true, added: true });
              } else if (it.entry) {
                if (it.entry.added) list.splice(list.indexOf(it.entry), 1);
                else it.entry.removed = true;
              }
              reindex();
              rerender();
            },
          }),
          el('span', { class: 'mono' }, rel)),
        it.size >= 0 ? el('span', { class: 'z' }, size(it.size)) : null,
        active ? el('span', { class: 'tag t-mut' }, 'wird nicht gesichert') : null),
      active ? el('div', { class: 'split' }, splitReason(it.entry, rel)) : null);
    }
    const addId = idFor(parentKey) + '-addsplit';
    const isOpen = ui.addOpen.has(parentKey);
    const input = el('input', { type: 'text', class: 'fld', id: addId, 'data-fid': addId, placeholder: parentPath + '/…', 'aria-label': 'Unterordner von ' + parentPath, autocomplete: 'off' });
    const err = el('p', { class: 'ferr', role: 'alert' });
    const add = () => {
      let p = input.value.trim().replace(/\/+$/, '');
      if (!p) return;
      if (!p.startsWith('/')) p = parentPath + '/' + p;
      const bad = p.split('/').some((s, i) => i > 0 && (s === '' || s === '.' || s === '..'));
      if (bad || !p.startsWith(parentPath + '/')) { err.textContent = 'Der Pfad muss in ' + parentPath + ' liegen.'; return; }
      const key = 'path:' + p;
      const existing = list.find(x => x.key === key);
      if (existing) existing.removed = false;
      else list.push({ key, type: 'path', label: p, split_of: parentKey, strategy: 'skip', reason: SPLIT_REASON, size: -1, exists: true, added: true });
      reindex();
      rerender();
    };
    input.addEventListener('keydown', e => { if (e.key === 'Enter') { e.preventDefault(); add(); } });
    const toggleId = idFor(parentKey) + '-addtoggle';
    put(box,
      el('button', {
        class: 'lk', type: 'button', 'data-fid': toggleId, 'aria-expanded': String(isOpen),
        onclick: () => { if (isOpen) ui.addOpen.delete(parentKey); else ui.addOpen.add(parentKey); rerender(); if (!isOpen) focusFid(addId); },
      }, items.length ? 'Anderen Unterordner ausnehmen …' : 'Unterordner nicht sichern …'),
      isOpen ? el('div', { class: 'sadd' }, input, el('button', { class: 'btn btn-sm', type: 'button', onclick: add }, 'Ausnehmen')) : null,
      err);
    return box;
  }

  function dataRow(d, list) {
    const note = dataNote(d);
    const row = el('div', { class: 'drow' },
      el('div', { class: 't mono' }, d.label, newTag(d), d.strategy === 'open' ? el('span', { class: 'tag t-open' }, 'offen') : null),
      el('select', { class: 'ctl', 'data-fid': idFor(d.key) + '-strategy', 'aria-label': 'Sicherung für ' + d.label, onchange: e => { d.strategy = e.target.value; rerender(); } },
        dataOptions(d).map(s => el('option', { value: s, selected: d.strategy === s }, (STRATEGIES[s] || [s])[0]))),
      el('div', { class: 'm' }, dataFacts(d) + ' · ' + (STRATEGIES[d.strategy] || ['', ''])[1] + (note ? ' · ' + note : '')));
    if (needsReason(d.strategy)) put(row, reasonField(d, { cls: 'full' }));
    const splittable = (d.largest && d.largest.length) || list.some(x => x.split_of === d.key) || d.size < 0 || d.size >= 10e6;
    if (d.type === 'path' && d.kind !== 'config' && splittable) put(row, splitBlock(d.key, d.label, d.largest, list));
    return row;
  }

  function managedRow(m, list) {
    const state = !m.container ? 'aus' : m.running ? 'läuft' : 'gestoppt';
    const row = el('div', { class: 'drow' },
      el('div', { class: 't' }, m.label ? '„' + m.label + '“' : null, el('span', { class: 'mono' }, shortName(m.name))),
      el('span', { class: 'tag ' + (m.running ? 't-ok' : 't-mut') }, state),
      el('div', { class: 'm' }, ['angelegt von ' + m.manager, m.size >= 0 ? size(m.size) : null, m.path].filter(Boolean).join(' · ') + '. ' +
        (m.container ? 'Wird über ' + m.manager + ' gestoppt, nicht mit docker stop.'
          : 'Gerade kein Container. Die Daten werden trotzdem gesichert; beim nächsten Start legt ' + m.manager + ' den Container neu an.')));
    const parent = m.path ? managedParent(m, list) : null;
    if (!parent) return row;
    const entry = managedSkip(m, list);
    put(row, el('div', { class: 'splits quiet' },
      el('div', { class: 'split' }, el('label', { class: 'chk' },
        el('input', {
          type: 'checkbox', checked: !!entry, 'data-fid': idFor(m.key) + '-skip', onchange: e => {
            const key = 'path:' + m.path;
            const old = list.find(x => x.key === key);
            if (e.target.checked) {
              if (old) { old.removed = false; old.strategy = 'skip'; old.split_of = parent.key; }
              else list.push({ key, type: 'path', label: m.path, split_of: parent.key, strategy: 'skip', reason: SPLIT_REASON, size: m.size, exists: true, added: true });
            } else if (old) {
              if (old.added) list.splice(list.indexOf(old), 1);
              else old.removed = true;
            }
            reindex();
            rerender();
          },
        }),
        el('span', {}, 'Diesen Server nicht sichern'))),
      entry ? el('div', { class: 'split' }, splitReason(entry, shortName(m.name))) : null));
    return row;
  }

  function svcRow(s) {
    return el('div', { class: 'drow' },
      el('div', { class: 't' }, s.name, newTag(s), el('span', { class: 'tag ' + (s.running ? 't-ok' : 't-mut') }, s.running ? 'läuft' : 'gestoppt')),
      el('select', { class: 'ctl', 'data-fid': idFor(s.key) + '-img', 'aria-label': 'Image für ' + s.name, onchange: e => { s.image = e.target.value; rerender(); } },
        Object.keys(IMAGES).map(v => el('option', { value: v, selected: s.image === v }, IMAGES[v][0]))),
      el('div', { class: 'm' }, el('span', { class: 'mono' }, s.image_name), ' · ' + originText(s) + (s.image_size >= 0 ? ' · ' + size(s.image_size) : '') + ' · ' + (IMAGES[s.image] || ['', ''])[1]),
      s.note ? el('div', { class: 'm' }, s.note) : null);
  }

  function decisionBar(o, legend, values, value, onChange, km) {
    return el('div', { class: 'dbar2' }, seg('d' + idFor(caddyKey(o)), legend, values.map(v => [v, TILE[v][0]]), value, onChange), el('span', { class: 'km' }, km));
  }

  function ownerDetails(p) {
    const isProj = !!p.services;
    const bytes = isProj ? projectBytes(p) : sum(p.data.filter(d => !d.split_of)).n;
    const km = p.decision === 'take' ? [wTake() + ': ', el('b', {}, size(bytes))] : p.decision === 'archive' ? ['Ins Archiv: ', el('b', {}, size(bytes))] : p.decision === 'skip' ? 'Wird nicht gesichert' : 'Noch offen';
    const facts = isProj ? [p.running + ' von ' + p.total + ' laufen', ...p.domains.map(domainLink),
      p.git ? 'Git ' + (p.git.branch || '') + (p.git.dirty ? ', ' + plural(p.git.dirty, 'Änderung', 'Änderungen') + ' nicht committet' : ', nichts offen') : null].filter(Boolean)
      : [p.running ? 'läuft' : p.state, p.image];
    const own = p.data.filter(d => !d.split_of);
    return {
      title: p.name,
      eyb: [isProj ? 'Projekt · ' : 'Container · ', el('span', { class: 'mono' }, isProj ? p.path : p.image)],
      facts: facts.map((f, i) => [i ? ' · ' : '', f]),
      tags: [newTag(p), isProj && !p.dir_exists ? el('span', { class: 'tag t-err' }, 'Ordner fehlt') : null],
      body: [
        decisionBar(p, 'Entscheidung für ' + p.name, ['take', 'archive', 'skip'], p.decision, v => { p.decision = v; rerender(); }, km),
        p.decision === 'skip' ? reasonField(p, {}) : null,
        p.decision === 'archive' ? reasonField(p, { optional: true, label: 'Begründung (optional)' }) : null,
        p.note ? el('p', { class: 'note' }, p.note) : null,
        isProj ? el('ul', { class: 'dwarns' }, projectWarnings(p).map(w => el('li', {}, warnLine(w)))) : null,
        isProj && p.path ? dsec('Projektordner', [el('div', { class: 'drow' },
          el('div', { class: 't mono' }, p.path),
          el('div', { class: 'm' }, [p.dir_exists ? size(p.size) : 'fehlt', p.git && p.git.remote ? 'Git: ' + p.git.remote : null, 'wird mit dem Projekt gesichert'].filter(Boolean).join(' · ')),
          p.dir_exists ? splitBlock(p.key, p.path, p.largest, p.data) : null)], true) : null,
        dsec('Daten', own.map(d => dataRow(d, p.data))),
        isProj ? dsec('Verwaltete Server', p.managed.map(m => managedRow(m, p.data))) : null,
        isProj ? dsec('Dienste und Images', p.services.map(svcRow)) : null,
      ],
    };
  }

  function looseDetails(d) {
    const k = originKey(d);
    const groupSize = k ? looseOf(d.area ? 'server' : 'daten').filter(x => originKey(x) === k).length : 0;
    const note = dataNote(d);
    const splittable = (d.largest && d.largest.length) || work.loose.some(x => x.split_of === d.key) || d.size < 0 || d.size >= 10e6;
    const v = looseValue(d);
    return {
      title: d.label,
      mono: d.label.startsWith('/'),
      eyb: (d.type === 'volume' ? 'Volume' : d.type === 'anon' ? 'anonymes Volume' : d.kind === 'config' ? 'Datei' : 'Ordner') + ' · ' + areaName(d.area ? 'server' : 'daten'),
      facts: dataFacts(d),
      tags: [newTag(d)],
      body: [
        decisionBar(d, 'Entscheidung für ' + d.label, looseChoices(d), v, nv => { setLoose(d, nv); rerender(); },
          v === 'open' ? 'Noch offen' : TILE[v][1]),
        v === 'skip' ? reasonField(d, {}) : null,
        note ? el('p', { class: 'hintbox' }, icon('warn', 16), el('span', {}, note)) : null,
        d.git && d.git.dirty ? warnLine(plural(d.git.dirty, 'Änderung', 'Änderungen') + ' im Git-Ordner nicht committet') : null,
        groupSize >= 2 ? el('p', { class: 'note' }, 'Gehört zur Gruppe ' + groupName(k) + ' (' + groupKind(k, looseOf(d.area ? 'server' : 'daten').filter(x => originKey(x) === k)) + ', ' + groupSize + ' Einträge).') : null,
        d.type === 'path' && d.kind !== 'config' && splittable ? el('div', { class: 'drow' }, splitBlock(d.key, d.label, d.largest, work.loose)) : null,
      ],
    };
  }

  function drawerContent(area, key) {
    const x = byKey.get(key);
    if (!x) return null;
    if (area === 'projekte' && x.kind === 'decision' && x.o !== work.caddy) return ownerDetails(x.o);
    if ((area === 'daten' || area === 'server') && x.kind === 'data' && !x.owner && !x.o.split_of && !!x.o.area === (area === 'server')) return looseDetails(x.o);
    return null;
  }

  function closeDrawer(area) {
    if (ui.pushed) { ui.pushed = false; history.back(); } else go('#/auswahl/bereich/' + area);
  }

  function trapKeys(e, box, close) {
    if (e.key === 'Escape') {
      if (ui.menu || ui.form) return;
      e.preventDefault();
      close();
      return;
    }
    if (e.key !== 'Tab') return;
    const f = [...box.querySelectorAll('a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), summary, [tabindex="0"]')].filter(x => x.offsetParent !== null);
    if (!f.length) return;
    const first = f[0], last = f[f.length - 1];
    if (e.shiftKey && (document.activeElement === first || !box.contains(document.activeElement))) { e.preventDefault(); last.focus(); }
    else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
  }

  // Eine Schublade liegt in #layer; solange sie offen ist, ist der Rest der
  // Seite inert und rollt nicht mit.
  function renderLayer(node) {
    const layer = document.getElementById('layer');
    const page = document.getElementById('page');
    const bar = document.getElementById('tabbar');
    if (!node) {
      layer.replaceChildren();
      // Ein Dialog zum Anmelden oder Bestätigen hält die Seite selbst inert.
      if (LS && LS.modalOpen()) return;
      page.removeAttribute('inert');
      bar.removeAttribute('inert');
      document.documentElement.classList.remove('locked');
      return;
    }
    page.setAttribute('inert', '');
    bar.setAttribute('inert', '');
    document.documentElement.classList.add('locked');
    // Dieselbe Schublade neu gezeichnet (Auffrischen alle paar Sekunden):
    // nicht noch einmal hereinrutschen, Scrollstand und Fokus bleiben.
    const old = layer.firstElementChild;
    if (!old || !node.dataset.key || old.dataset.key !== node.dataset.key) { layer.replaceChildren(node); return; }
    node.classList.add('still');
    const a = document.activeElement;
    const had = a && old.contains(a);
    const fid = had ? a.getAttribute('data-fid') : null, id = had ? a.id : '';
    const tops = [...old.querySelectorAll('.dscroll')].map(x => x.scrollTop);
    layer.replaceChildren(node);
    node.querySelectorAll('.dscroll').forEach((x, i) => { if (tops[i]) x.scrollTop = tops[i]; });
    if (!had) return;
    const t = (fid && node.querySelector('[data-fid="' + CSS.escape(fid) + '"]')) || (id && document.getElementById(id)) || node.querySelector('[role="dialog"] [tabindex="-1"]');
    if (t) t.focus({ preventScroll: true });
  }

  function renderDrawer(r) {
    const c = r.view === 'bereich' && r.key ? drawerContent(r.area, r.key) : null;
    if (!c) { renderLayer(null); return null; }
    const close = () => closeDrawer(r.area);
    const dlg = el('div', { class: 'drawer', role: 'dialog', 'aria-modal': 'true', 'aria-labelledby': 'dlg-title' },
      el('div', { class: 'dscroll' },
        el('a', { class: 'dback', href: '#/auswahl/bereich/' + r.area, onclick: e => { e.preventDefault(); close(); } }, '← ' + areaName(r.area)),
        el('div', { class: 'dhead' },
          el('div', {},
            el('p', { class: 'eyb' }, c.eyb),
            el('h2', { id: 'dlg-title', tabindex: '-1', class: c.mono ? 'mono' : null }, c.title),
            el('p', { class: 'facts' }, c.facts, ' ', c.tags)),
          el('button', { class: 'ib dx', type: 'button', 'aria-label': 'Schließen', onclick: close }, icon('x', 20))),
        saveErrorBand(),
        c.body),
      el('div', { class: 'dfoot' }, el('button', { class: 'btn btn-p', type: 'button', 'data-fid': 'dlg-done', onclick: close }, 'Fertig')));
    dlg.addEventListener('keydown', e => trapKeys(e, dlg, close));
    renderLayer(el('div', { class: 'ax-layer', 'data-key': r.area + '/' + r.key }, el('div', { class: 'scrim', onclick: close }), dlg));
    return c;
  }

  // ---------- Zeichnen ----------

  // Die Leiste der Auswahl (Speichern, Bereiche) steht in der Übersicht
  // der Auswahl unter dem Kopf; vor jedem Neuzeichnen kehrt sie an ihren
  // Platz vor der Seite zurück, damit sie nie aus dem Dokument fällt.
  let axRef = null;
  function axHome() {
    axRef = axRef || document.getElementById('axbar');
    const app = document.getElementById('app');
    if (axRef.parentNode !== app.parentNode || axRef.nextSibling !== app) app.parentNode.insertBefore(axRef, app);
    axRef.classList.remove('in-page');
    document.body.classList.remove('ax-in');
  }
  function render(nav) {
    if (!work) return;
    let r = route();
    if (r.view === 'bereich' && !areaList().some(a => a[0] === r.area)) { history.replaceState(null, '', '#/auswahl'); r = { view: 'uebersicht' }; }
    const prev = ui.prev;
    ui.prev = r;
    if (nav) { ui.menu = null; ui.form = null; }
    const app = document.getElementById('app');
    const hn = server.host.hostname || '';
    document.getElementById('axbar').hidden = !isAuswahl(r);
    document.body.classList.toggle('ax', isAuswahl(r));
    if (LS) LS.renderShell(r);
    if (!isAuswahl(r)) {
      const out = LS.render(r);
      const tabless = k => k ? k.replace(/\/(logs|verlauf|prozesse|konfiguration|ereignisse)$/, '') : k;
      const sameView = !!(prev && prev.view === 'ls' && prev.page === r.page && prev.key && r.key && tabless(prev.key) === tabless(r.key));
      // Seitenfenster der Übersicht auf und zu: die Seite bleibt, wie sie ist.
      const panel = !!(prev && prev.view === 'ls' && prev.page === 'lage' && r.page === 'lage');
      app.className = 'ls-page' + (nav && !panel ? ' enter' : '') + (nav && sameView ? ' tabsw' : '');
      axHome();
      app.replaceChildren(...out.nodes);
      renderAreasNav(r);
      renderLayer(out.drawer);
      document.title = out.title + ' · HostDeck Leitstand' + (hn ? ' · ' + hn : '');
      updateStatus();
      if (!nav) return;
      if (out.drawer) { const h = document.getElementById('ls-dt'); if (h) h.focus(); return; }
      // Reiter einer Projektseite: Fokus bleibt auf dem Reiter, die Seite
      // springt nicht nach oben.
      if (sameView) {
        const t = app.querySelector('.pv-tabs [aria-current="page"]');
        if (t) { t.focus({ preventScroll: true }); return; }
      }
      // Zurück aus einem Projekt: auf die Zeile, von der es kam.
      if (prev && prev.view === 'ls' && prev.page === r.page && prev.key && !r.key) {
        const back = app.querySelector('a[href="#/' + r.page + '/' + encodeURIComponent(tabless(prev.key)) + '"]');
        if (back) { back.focus(); return; }
      }
      // Unterziel einer Seite (etwa ein Ordner unter Speicherplatz): dorthin
      // statt nach oben.
      const fx = app.querySelector('[data-nav-focus]');
      if (fx) { fx.focus({ preventScroll: true }); fx.scrollIntoView({ block: 'start' }); return; }
      window.scrollTo(0, 0);
      const h1 = app.querySelector('h1');
      if (h1) h1.focus({ preventScroll: true });
      return;
    }
    let view;
    if (r.view === 'start') view = viewStart();
    else if (r.view === 'frage') view = viewQuestion(r.n);
    else if (r.view === 'bereich') {
      view = r.area === 'caddy' ? viewCaddy() : r.area === 'warnungen' ? viewFindings() : r.area === 'neuer-server' ? viewNewServer() : viewKisten(r.area, r.key);
    } else view = viewOverview();
    app.className = 'ax-page';
    axHome();
    app.replaceChildren(...view);
    // Gleicher Kopf wie überall (Idee 13): Titel und Satz, darunter die
    // Reiter Backup | Auswahl und als zweite Reihe die Bereiche.
    const axHead = app.querySelector('.ls-head');
    if (axHead) {
      const tabs = LS && LS.subTabs ? LS.subTabs('auswahl') : null;
      if (tabs) axHead.after(tabs);
      (tabs || axHead).after(axRef);
      axRef.classList.add('in-page');
    }
    document.body.classList.toggle('ax-in', !!axHead);
    renderAreasNav(r);
    const c = renderDrawer(r);
    document.getElementById('host').replaceChildren(el('b', {}, 'Auswahl'), el('span', { class: 'lng' }, ' · was von ' + (hn || 'diesem Server') + ' ins Backup kommt'));
    const t = r.view === 'start' ? 'Start · Auswahl' : r.view === 'frage' ? 'Frage ' + qIndex(r.n) + ' von ' + questions.length + ' · Auswahl'
      : r.view === 'bereich' ? (c ? c.title + ' · ' : '') + areaName(r.area) + ' · Auswahl' : 'Auswahl';
    document.title = t + ' · HostDeck Leitstand' + (hn ? ' · ' + hn : '');
    updateStatus();
    if (!nav) return;
    if (c) { const h = document.getElementById('dlg-title'); if (h) h.focus(); return; }
    if (prev && prev.view === 'bereich' && prev.key && ui.returnFid && focusFid(ui.returnFid)) { ui.returnFid = null; return; }
    ui.returnFid = null;
    window.scrollTo(0, 0);
    const h = document.querySelector('#app h1');
    if (h) h.focus({ preventScroll: true });
  }

  // Neu zeichnen nach einer Änderung: Fokus, Eingabestelle und Bildlauf der
  // Schublade bleiben.
  function rerender() {
    const a = document.activeElement;
    const fid = a && a.dataset ? a.dataset.fid : null;
    let sel = null;
    try { if (a && typeof a.selectionStart === 'number') sel = [a.selectionStart, a.selectionEnd]; } catch (e) { sel = null; }
    const sc = document.querySelector('.dscroll');
    const top = sc ? sc.scrollTop : 0;
    render(false);
    const sc2 = document.querySelector('.dscroll');
    if (sc2) sc2.scrollTop = top;
    if (!fid) return;
    const e = document.querySelector('[data-fid="' + fid + '"]');
    if (e && e.offsetParent !== null) {
      e.focus({ preventScroll: true });
      if (sel) try { e.setSelectionRange(sel[0], sel[1]); } catch (x) { /* kein Textfeld */ }
    }
  }

  // Nach dem Abmelden: Stand vergessen und die Anmeldeseite zeigen.
  async function signedOut(msg) {
    clearTimeout(svcUI.timer);
    Object.assign(svcUI, { seen: null, starting: false, error: '', off: false });
    server = null; work = null; svc = null;
    document.getElementById('axbar').hidden = true;
    renderLayer(null);
    if (!(await LS.signIn(msg))) return;
    if (await load()) refreshLauf();
  }

  // ---------- Start ----------

  // Tauscht den Link aus dem Terminal gegen eine Anmeldung.
  async function redeem(h) {
    history.replaceState(null, '', location.pathname + '#/lage');
    const r = await call('/api/einmal', { token: h }, false, true);
    if (!r.ok) { fatal(r.data && r.data.error ? r.data.error : 'Anmeldung fehlgeschlagen (' + r.status + ')'); return false; }
    return true;
  }

  async function init() {
    const h = location.hash.slice(1);
    const link = /^[0-9a-f]{32}$/.test(h) ? h : '';
    if (link) history.replaceState(null, '', location.pathname);
    if (window.hostdeckLeitstand) {
      LS = window.hostdeckLeitstand({
        el, size, plural, shortName, andList, cap, fmtDate, trapKeys, rerender, toast, announce, mustSave, projectBytes,
        state: () => server, work: () => work, hostName: host, move: moveSummary,
        openCount: () => openList().length, dirty: () => edits().length,
        svc: () => svc, svcError: () => svcUI.error, discoverCtl, setTheme,
        call, menuKeys, refreshLauf, signedOut, setSvc: v => { svc = v; }, reload: load,
        setDomainStates: list => { domStates = list ? new Map(list.flatMap(z => (z.domains || [z.domain]).map(h => [h, z]))) : null; },
      });
      LS.init();
    }
    document.getElementById('save').addEventListener('click', save);
    document.querySelector('.skip').addEventListener('click', e => {
      e.preventDefault();
      const t = document.querySelector('#layer h2') || document.querySelector('#app h1') || document.getElementById('app');
      t.focus();
    });
    document.addEventListener('keydown', e => {
      if ((e.ctrlKey || e.metaKey) && (e.key === 's' || e.key === 'S')) {
        e.preventDefault();
        if (!document.getElementById('save').disabled) save();
      }
    });
    document.addEventListener('pointerdown', e => {
      if (ui.menu && !e.target.closest('.menu') && !e.target.closest('.mv[aria-expanded="true"]')) { ui.menu = null; rerender(); }
    });
    window.addEventListener('beforeunload', e => {
      if (work && edits().length > 0) { e.preventDefault(); e.returnValue = ''; }
    });
    window.addEventListener('hashchange', () => {
      const x = location.hash.slice(1);
      if (/^[0-9a-f]{32}$/.test(x)) { redeem(x).then(ok => { if (ok) load(); }); return; }
      if (x.startsWith('/')) render(true);
    });
    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'visible' && svc) refreshLauf();
    });
    if (link && !(await redeem(link))) return;
    if (await load()) { refreshLauf(); if (LS) LS.refreshSession(); }
  }

  init();
})();

// hostdeck serve: Leitstand. Seitenleiste, Kopfzeile und die Seiten Übersicht,
// Projekte, Domains, Backup, Umzug und Speicherplatz. Die Auswahl (Fragen und
// Kisten) zeichnet app.js; beide teilen sich den Stand von /api/state.
//
// Alles zeigt nur, was HostDeck wirklich weiß: die Bestandsaufnahme, die Auswahl
// und die Liste der Backup-Läufe. Knöpfe ohne Funktion gibt es nicht; was
// noch fehlt, sagt die Seite in Worten. Daten kommen nur als Text in die
// Seite (textContent), Stile nur über Klassen oder element.style (CSSOM).
'use strict';
window.hostdeckLeitstand = function (core) {
  const { el, size, plural, shortName, fmtDate } = core;

  // ---------- Symbole (nach Lucide, ISC-Lizenz) ----------
  const SVG = 'http://www.w3.org/2000/svg';
  const C = r => 'M' + (12 + r) + ' 12a' + r + ' ' + r + ' 0 1 1-' + 2 * r + ' 0 ' + r + ' ' + r + ' 0 0 1 ' + 2 * r + ' 0z';
  const SHIELD = 'M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z';
  const IC = {
    lage: ['M3 3h7v9H3zM14 3h7v5h-7zM14 12h7v9h-7zM3 16h7v5H3z'],
    projekte: ['M12 2 3 7v10l9 5 9-5V7z', 'm3 7 9 5 9-5', 'M12 12v10'],
    domains: [C(10), 'M2 12h20', 'M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z'],
    backup: ['M2 3h20v5H2z', 'M4 8v11a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8', 'M10 12h4'],
    auswahl: ['m3 17 2 2 4-4', 'm3 7 2 2 4-4', 'M13 6h8', 'M13 12h8', 'M13 18h8'],
    umzug: ['M14 18V6a2 2 0 0 0-2-2H4a2 2 0 0 0-2 2v11a1 1 0 0 0 1 1h2', 'M15 18H9', 'M19 18h2a1 1 0 0 0 1-1v-3.65a1 1 0 0 0-.22-.62l-3.48-4.35A1 1 0 0 0 17.52 8H14', 'M19 18a2 2 0 1 1-4 0 2 2 0 0 1 4 0z', 'M9 18a2 2 0 1 1-4 0 2 2 0 0 1 4 0z'],
    platz: ['M22 12H2', 'M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z', 'M6 16h.01', 'M10 16h.01'],
    sicherheit: [SHIELD, 'M12 8v4', 'M12 16h.01'],
    leistung: ['m12 14 4-4', 'M3.34 19a10 10 0 1 1 17.32 0'],
    bell: ['M10.268 21a2 2 0 0 0 3.464 0', 'M3.262 15.326A1 1 0 0 0 4 17h16a1 1 0 0 0 .74-1.673C19.41 13.956 18 12.499 18 8A6 6 0 0 0 6 8c0 4.499-1.411 5.956-2.738 7.326'],
    phone: ['M7 2h10a2 2 0 0 1 2 2v16a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2z', 'M12 18h.01'],
    game: ['M6 11h4', 'M8 9v4', 'M15 12h.01', 'M18 10h.01', 'M17.32 5H6.68a4 4 0 0 0-3.978 3.59c-.006.052-.01.101-.017.152C2.604 9.416 2 14.456 2 16a3 3 0 0 0 3 3c1 0 1.5-.5 2-1l1.414-1.414A2 2 0 0 1 9.828 16h4.344a2 2 0 0 1 1.414.586L17 18c.5.5 1 1 2 1a3 3 0 0 0 3-3c0-1.545-.604-6.584-.685-7.258-.007-.05-.011-.1-.017-.151A4 4 0 0 0 17.32 5z'],
    puls: ['M22 12h-2.48a2 2 0 0 0-1.93 1.46l-2.35 8.36a.25.25 0 0 1-.48 0L9.24 2.18a.25.25 0 0 0-.48 0l-2.35 8.36A2 2 0 0 1 4.49 12H2'],
    trash: ['M3 6h18', 'M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6', 'M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2', 'M10 11v6', 'M14 11v6'],
    menu: ['M4 6h16', 'M4 12h16', 'M4 18h16'],
    einstellungen: ['M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z', C(3)],
    moon: ['M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9Z'],
    sun: [C(4), 'M12 2v2', 'M12 20v2', 'm4.93 4.93 1.41 1.41', 'm17.66 17.66 1.41 1.41', 'M2 12h2', 'M20 12h2', 'm6.34 17.66-1.41 1.41', 'm19.07 4.93-1.41 1.41'],
    monitor: ['M4 3h16a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2z', 'M8 21h8', 'M12 17v4'],
    dots: ['M11 12a1 1 0 1 0 2 0 1 1 0 1 0-2 0', 'M18 12a1 1 0 1 0 2 0 1 1 0 1 0-2 0', 'M4 12a1 1 0 1 0 2 0 1 1 0 1 0-2 0'],
    down: ['m6 9 6 6 6-6'],
    sort: ['m7 15 5 5 5-5', 'm7 9 5-5 5 5'],
    sortdown: ['m7 13 5 5 5-5', 'M12 18V6'],
    sortup: ['m7 11 5-5 5 5', 'M12 6v12'],
    search: ['M19 11a8 8 0 1 1-16 0 8 8 0 0 1 16 0z', 'm21 21-4.3-4.3'],
    right: ['m9 18 6-6-6-6'],
    ok: [SHIELD, 'm9 12 2 2 4-4'],
    alert: [SHIELD, 'M12 8v4', 'M12 16h.01'],
    check: ['M20 6 9 17l-5-5'],
    eyeoff: ['M10.733 5.076a10.744 10.744 0 0 1 11.205 6.575 1 1 0 0 1 0 .696 10.747 10.747 0 0 1-1.444 2.49', 'M14.084 14.158a3 3 0 0 1-4.242-4.242', 'M17.479 17.499a10.75 10.75 0 0 1-15.417-5.151 1 1 0 0 1 0-.696 10.75 10.75 0 0 1 4.446-5.143', 'm2 2 20 20'],
    lock: ['M5 11h14a2 2 0 0 1 2 2v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-7a2 2 0 0 1 2-2z', 'M7 11V7a5 5 0 0 1 10 0v4'],
    folder: ['M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2z'],
    db: ['M21 5c0 1.66-4 3-9 3S3 6.66 3 5s4-3 9-3 9 1.34 9 3z', 'M3 5v14a9 3 0 0 0 18 0V5', 'M3 12a9 3 0 0 0 18 0'],
    unlink: ['m18.84 12.25 1.72-1.71a5 5 0 0 0-.12-7.07 5 5 0 0 0-6.95 0l-1.72 1.71', 'm5.17 11.75-1.71 1.71a5 5 0 0 0 .12 7.07 5 5 0 0 0 6.95 0l1.71-1.71', 'M8 2v3', 'M2 8h3', 'M16 19v3', 'M19 16h3'],
    server: ['M4 2h16a2 2 0 0 1 2 2v4a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2z', 'M4 14h16a2 2 0 0 1 2 2v4a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2v-4a2 2 0 0 1 2-2z', 'M6 6h.01', 'M6 18h.01'],
    x: ['M18 6 6 18', 'm6 6 12 12'],
    done: [C(10), 'm9 12 2 2 4-4'],
    wait: [C(10)],
    clock: [C(10), 'M12 6v6l4 2'],
    warn: ['m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3', 'M12 9v4', 'M12 17h.01'],
    next: ['M5 12h14', 'm12 5 7 7-7 7'],
    info: [C(10), 'M12 16v-4', 'M12 8h.01'],
    bang: [C(10), 'M12 7v6', 'M12 16.5h.01'],
    laptop: ['M20 16V7a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v9m16 0H4m16 0 1.28 2.55a1 1 0 0 1-.9 1.45H3.62a1 1 0 0 1-.9-1.45L4 16'],
    edit: ['M12 20h9', 'M16.38 3.62a1 1 0 0 1 3 3L7.37 18.64a2 2 0 0 1-.86.5l-2.87.84a.5.5 0 0 1-.62-.62l.84-2.87a2 2 0 0 1 .5-.86z'],
    refresh: ['M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8', 'M21 3v5h-5', 'M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16', 'M8 16H3v5'],
    user: [C(10), 'M15 10a3 3 0 1 1-6 0 3 3 0 0 1 6 0z', 'M7 20.66V19a2 2 0 0 1 2-2h6a2 2 0 0 1 2 2v1.66'],
    logout: ['m16 17 5-5-5-5', 'M21 12H9', 'M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4'],
    eye: ['M2.06 12.35a1 1 0 0 1 0-.7 10.75 10.75 0 0 1 19.88 0 1 1 0 0 1 0 .7 10.75 10.75 0 0 1-19.88 0', C(3)],
    eyeoff: ['M10.73 5.08a10.74 10.74 0 0 1 11.2 6.57 1 1 0 0 1 0 .7 10.75 10.75 0 0 1-1.44 2.49', 'M14.08 14.16a3 3 0 0 1-4.24-4.24', 'M17.48 17.5a10.75 10.75 0 0 1-15.42-5.15 1 1 0 0 1 0-.7 10.75 10.75 0 0 1 4.45-5.14', 'm2 2 20 20'],
    protokoll: ['M15 12h-5', 'M15 8h-5', 'M19 17V5a2 2 0 0 0-2-2H4', 'M8 21h12a2 2 0 0 0 2-2v-1a1 1 0 0 0-1-1H11a1 1 0 0 0-1 1v1a2 2 0 1 1-4 0V5a2 2 0 1 0-4 0v2a1 1 0 0 0 1 1h3'],
    updates: [C(10), 'm16 12-4-4-4 4', 'M12 16V8'],
    upload: ['M12 3v12', 'm17 8-5-5-5 5', 'M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4'],
    download: ['M12 15V3', 'M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4', 'm7 10 5 5 5-5'],
    file: ['M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7z', 'M14 2v4a2 2 0 0 0 2 2h4', 'm9 15 2 2 4-4'],
    doc: ['M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7z', 'M14 2v4a2 2 0 0 0 2 2h4'],
    globelock: ['M15.69 15A14.5 14.5 0 0 1 12 22a14.5 14.5 0 0 1 0-20 10 10 0 1 0 9.54 13', 'M2 12h8.5', 'M20 6V4a2 2 0 1 0-4 0v2', 'M15 6h6a1 1 0 0 1 1 1v3a1 1 0 0 1-1 1h-6a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1z'],
    play: ['M6 3 20 12 6 21z'],
    pause: ['M8 5v14', 'M16 5v14'],
    left: ['m15 18-6-6 6-6'],
    bell: ['M10.268 21a2 2 0 0 0 3.464 0', 'M3.262 15.326A1 1 0 0 0 4 17h16a1 1 0 0 0 .74-1.673C19.41 13.956 18 12.499 18 8A6 6 0 0 0 6 8c0 4.499-1.411 5.956-2.738 7.326'],
    stop: ['M7 5h10a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V7a2 2 0 0 1 2-2z'],
    undo: ['M9 14 4 9l5-5', 'M4 9h10.5a5.5 5.5 0 0 1 0 11H11'],
    copy: ['M10 8h10a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H10a2 2 0 0 1-2-2V10a2 2 0 0 1 2-2z', 'M4 16a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h10a2 2 0 0 1 2 2'],
    door: ['M13 4h3a2 2 0 0 1 2 2v14', 'M2 20h3', 'M13 20h9', 'M10 12v.01', 'M13 4.56v16.16a.5.5 0 0 1-.63.48L5 19V5.79a2 2 0 0 1 1.51-1.94l5.86-1.47a.5.5 0 0 1 .63.48z'],
  };
  function ic(name, px) {
    const s = document.createElementNS(SVG, 'svg');
    const n = String(px || 18);
    for (const [k, v] of [['width', n], ['height', n], ['viewBox', '0 0 24 24'], ['fill', 'none'], ['stroke', 'currentColor'],
      ['stroke-width', '1.9'], ['stroke-linecap', 'round'], ['stroke-linejoin', 'round'], ['aria-hidden', 'true'], ['focusable', 'false']]) s.setAttribute(k, v);
    for (const d of IC[name] || IC.info) {
      const p = document.createElementNS(SVG, 'path');
      p.setAttribute('d', d);
      s.append(p);
    }
    return s;
  }

  // ---------- Wörter ----------
  const ENGINES = { postgres: 'PostgreSQL', mysql: 'MySQL', mariadb: 'MariaDB', mongo: 'MongoDB', redis: 'Redis', valkey: 'Valkey', sqlite: 'SQLite' };
  const DECISION = {
    take: ['kommt mit', 't-ok'], archive: ['ins Archiv', 't-sug'], skip: ['bleibt zurück', 't-mut'], open: ['offen', 't-open'],
  };
  // Im Alltag ist dieselbe Entscheidung eine fürs Backup, nicht für einen
  // Umzug (Idee 1).
  const DECISION_ALLTAG = { take: 'im Backup', archive: 'im Archiv', skip: 'nicht im Backup' };
  const isAlltag = () => !!(core.svc() && core.svc().alltag);
  function decWord(k) {
    if (isAlltag() && DECISION_ALLTAG[k]) return DECISION_ALLTAG[k];
    return (DECISION[k] || [k || '?'])[0];
  }
  // Routen-Status der Bestandsaufnahme: nie der aktuelle Stand. Ob ein
  // gestopptes Ziel gewollt aus ist, weiß nur der Dienst.
  const ROUTE = {
    'ok': ['antwortete', 'ok'], 'container-gestoppt': ['Ziel war aus', 'idle'], 'nichts-lauscht': ['zeigte ins Leere', 'err'],
    'extern': ['extern', 'idle'], 'kein-proxy': ['keine Weiterleitung', 'idle'],
  };
  const CHECK_AREAS = {
    benutzer: 'Benutzer', ssh: 'SSH', firewall: 'Firewall', swap: 'Swap', docker: 'Docker', zeitzone: 'Zeitzone',
    kernel: 'Kernel', pakete: 'Pakete', platten: 'Platten',
  };
  const PAGES = {
    lage: 'Übersicht', projekte: 'Projekte', domains: 'Domains', ueberwachung: 'Überwachung', backup: 'Backup', umzug: 'Umzug', platz: 'Speicherplatz',
    sicherheit: 'Sicherheit', leistung: 'Leistung', leistungstest: 'Leistungstest', tuning: 'Tuning', protokoll: 'Protokoll', updates: 'Updates', zugang: 'Zugang von außen',
    einstellungen: 'Einstellungen',
  };
  // Nur als Dienst: Protokoll, Updates und Zugang von außen.
  const SVC_PAGES = ['ueberwachung', 'sicherheit', 'leistung', 'leistungstest', 'tuning', 'protokoll', 'updates', 'zugang'];
  const ui = { pf: 'alle', pq: '', palette: null, ps: 'st', pdir: 1, pexp: new Set(), psel: new Set(), pmenu: '', want: null };

  // ---------- Kleine Bausteine ----------
  function em(cls, text) { return el('em', { class: cls }, text); }
  function num(v, unit) { return el('span', { class: 'ls-num' }, v, unit ? el('small', {}, unit) : null); }
  // size(…) als Zahl mit kleiner Einheit, etwa „24,7 GB“.
  function bytes(b) {
    const t = size(b);
    const i = t.lastIndexOf('\u00a0');
    return i < 0 ? num(t) : num(t.slice(0, i), '\u00a0' + t.slice(i + 1));
  }
  function st(kind, text) { return el('span', { class: 'ls-st ' + kind }, text); }
  function tag(decision) {
    const d = DECISION[decision] || [decision, 't-mut'];
    return el('span', { class: 'tag ' + d[1] }, decWord(decision));
  }
  // Abschnitt mit Kopf. o.help: Erklärung hinter „?“ im Kopf, aufgeklappt
  // gemerkt wie bei der Seite.
  function sec(title, body, o) {
    o = o || {};
    const hp = o.help ? helpParts('sec-' + (ui.page || '') + '-' + title, title, o.help) : null;
    return el('section', { class: 'ls-sec ' + (o.cls || ''), 'aria-label': o.aria || null },
      title || o.end ? el('header', { class: 'ls-sec-h' }, title ? el('h2', {}, title) : null, hp ? hp[0] : null, o.sub ? el('span', { class: 'sub' }, o.sub) : null,
        o.end ? el('div', { class: 'end' }, o.end) : null) : null,
      el('div', { class: 'ls-sec-b' }, hp ? hp[1] : null, body));
  }
  // Kopfzeile jeder Seite: kurzer Titel, eine Zeile zum Zustand, die
  // Erklärung hinter „?“. Was du aufgeklappt hast, merkt sich der Browser.
  const HELP_KEY = 'hostdeck-hilfe';
  const helpOpen = (() => { try { return new Set(JSON.parse(localStorage.getItem(HELP_KEY) || '[]')); } catch (e) { return new Set(); } })();
  // Knopf „?“ und die Erklärung dazu; der Browser merkt sich, was offen ist.
  function helpParts(key, title, text, show) {
    const open = !!show || helpOpen.has(key);
    const hid = 'ls-help-' + key.replace(/[^\w-]/g, '_');
    const help = el('div', { class: 'ls-help', id: hid, hidden: !open }, ic('info', 16), el('div', {}, text));
    const toggle = e => {
      const on = !helpOpen.has(key);
      if (on) helpOpen.add(key); else helpOpen.delete(key);
      try { localStorage.setItem(HELP_KEY, JSON.stringify([...helpOpen])); } catch (x) { /* ohne Speicher */ }
      e.currentTarget.setAttribute('aria-expanded', String(on));
      help.hidden = !on;
    };
    const btn = el('button', { type: 'button', class: 'ls-hbtn', 'aria-expanded': String(open), 'aria-controls': hid, 'aria-label': 'Erklärung zu ' + title, title: 'Erklärung', 'data-fid': 'help-' + key, onclick: toggle }, '?');
    return [btn, help];
  }
  function head(o) {
    const parts = String(o.k || '').split(' · ');
    const title = parts[0];
    // Das Etikett neben dem Titel heißt nur eins: wie aktuell die Seite ist
    // (Idee 11). Andere Zusätze im Schlüssel bleiben ungezeigt.
    const fresh = parts.slice(1).filter(x => /^(Stand |live$)/i.test(x)).pop() || '';
    const meta = /^live$/i.test(fresh) ? 'Live' : fresh;
    const hp = o.p ? helpParts(ui.page || title, title, o.p, o.show) : null;
    return el('header', { class: 'ls-head' },
      el('div', { class: 'ls-head-t' },
        el('div', { class: 'ls-title' }, el('h1', { tabindex: '-1' }, title), meta ? el('span', { class: 'ls-meta' }, meta) : null, hp ? hp[0] : null),
        el('p', { class: 'ls-status' }, o.h),
        hp ? hp[1] : null),
      o.a ? el('div', { class: 'ls-acts' }, o.a) : null);
  }
  function link(href, text, cls, icon) {
    return el('a', { class: cls || 'btn btn-sm', href }, icon ? ic(icon, 16) : null, text);
  }
  function when(s) {
    const d = new Date(s);
    if (isNaN(d)) return '';
    const now = new Date();
    const day = x => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
    const diff = Math.round((day(now) - day(d)) / 864e5);
    const hm = d.toLocaleTimeString('de-DE', { hour: '2-digit', minute: '2-digit' });
    if (diff === 0) return 'heute, ' + hm;
    if (diff === 1) return 'gestern, ' + hm;
    if (diff === -1) return 'morgen, ' + hm;
    return String(d.getDate()).padStart(2, '0') + '.' + String(d.getMonth() + 1).padStart(2, '0') + '., ' + hm;
  }
  function dur(sec) {
    if (!(sec > 0)) return '';
    if (sec < 60) return sec + ' s';
    const m = Math.round(sec / 60);
    return m < 60 ? m + ' min' : Math.floor(m / 60) + ' h ' + (m % 60) + ' min';
  }
  function base(p) { const i = p.lastIndexOf('/'); return i >= 0 ? p.slice(i + 1) || p : p; }
  function dataLabel(d) {
    const name = d.type === 'path' ? base(d.label) : d.label;
    // Ein Ordner mit einer SQLite-Datei darin ist keine Datenbank als Ganzes:
    // beim Namen bleiben und die Datei nur nennen.
    if (d.engine === 'sqlite' && d.type !== 'anon') return name + ' (mit SQLite)';
    const e = d.engine ? (ENGINES[d.engine] || d.engine) + (d.engine_version ? ' ' + d.engine_version : '') : '';
    return e ? e + (d.type === 'anon' ? '' : ' · ' + name) : name;
  }

  // ---------- Lage aus dem Stand ----------
  // Alles, was mehrere Seiten brauchen, einmal ausgerechnet.
  function model() {
    const s = core.state();
    const w = core.work();
    const owner = new Map(); // Containername -> Projekt
    for (const p of s.projects) for (const sv of p.services) for (const c of sv.containers || []) owner.set(c, p);
    const routes = s.caddy ? s.caddy.routes.map(r => Object.assign({}, r, { project: r.target ? owner.get(r.target) || null : null, z: zOf(r.hosts) })) : [];
    const dom = domSum(routes);
    const projects = w.projects.map(p => {
      const sp = s.projects.find(x => x.key === p.key) || p;
      const x = Object.assign({}, sp, { decision: p.decision, ok: sp.total > 0 && sp.running === sp.total, routes: routes.filter(r => r.project && r.project.key === p.key) });
      // Live-Stand vor Bestandsaufnahme: erledigte Jobs und von dir
      // gestoppte Container brauchen dich nicht.
      const lv = live();
      const cs = lv ? lv.filter(c => c.projekt === x.name) : [];
      const ls = cs.length ? liveSum(cs) : null;
      if (ls) {
        x.ok = ls.unexpected === 0 && !ls.restarting;
        x.running = ls.run; x.total = ls.total; x.live = true; x.ls = ls;
      }
      return x;
    });
    const containers = w.containers.map(c => Object.assign({}, s.containers.find(x => x.key === c.key) || c, { decision: c.decision }));
    let run = 0, all = 0;
    for (const p of s.projects) { run += p.running; all += p.total; }
    for (const c of s.containers) { all++; if (c.running) run++; }
    const backups = s.backups || [];
    const lastValid = backups.find(b => b.valid) || null;
    const open = core.openCount();
    const dirty = core.dirty();
    const mstate = s.manifest.state;
    // Nicht mehr Gefundenes muss raus, sonst lehnt die Vorprüfung das Backup ab.
    const gone = (s.gone || []).length;
    const auswahlDone = mstate === 'vorhanden' && open === 0 && gone === 0 && !core.mustSave();
    const warnings = s.findings.filter(f => f.level === 'warnung' && !/^Tote Route/.test(f.message));
    // Alltag: Kein Umzug steht an (Server-Seite, nach dem Einzug von
    // selbst). Dann zählt Betrieb; Auswahl und Umzug melden sich nur auf
    // ihren eigenen Seiten.
    const alltag = !!(core.svc() && core.svc().alltag);
    return { s, w, routes, dom, projects, containers, run, all, backups, last: backups[0] || null, lastValid, open, gone, dirty, mstate, auswahlDone, warnings, alltag };
  }

  // ---------- Seitenleiste, Kopfzeile, untere Leiste ----------
  // Stufe des Backups: als Dienst aus der Regel der Überwachung (eine
  // Quelle, V5), gleich in Pille, Sicherheitsnetz und Hinweisen: neutral
  // ohne Backup, bis die Regel nach ihren Tagen warnt.
  function backupView() {
    if (!core.svc()) return null;
    if (!mon.rep && !mon.loading && !mon.at) setTimeout(loadMon, 0);
    const w = mon.rep && mon.rep.warnungen;
    return w && w.backup && w.backup.bekannt ? w.backup : null;
  }
  function netState(m) {
    const sv = core.svc();
    if (sv && sv.kopie) return ['idle', 'info', 'Probe-Kopie', '', ' · keine Backups'];
    if (sv && sv.abgeloest) return ['idle', 'info', 'Abgelöst', '', ' · Backups macht der neue Server'];
    const bv = backupView();
    if (bv) {
      const k = bv.stufe === 'ok' ? 'ok' : bv.stufe === 'idle' ? 'idle' : bv.stufe;
      const icon = k === 'ok' ? 'ok' : k === 'idle' ? 'info' : 'alert';
      if (bv.gueltig) return [k, icon, 'Backup ', when(bv.zuletzt), k === 'ok' ? ' · gültig' : ' · ' + plural(bv.tage, 'Tag', 'Tage') + ' alt'];
      return [k, icon, 'Noch kein Backup', '', ''];
    }
    if (sv) return m.last && m.last.valid ? ['ok', 'ok', 'Backup ', when(m.last.started), ' · gültig'] : ['idle', 'info', 'Backup', '', ' · Stand wird geladen'];
    if (m.last && m.last.valid) return ['ok', 'ok', 'Backup ', when(m.last.started), ' · gültig'];
    if (m.last && m.lastValid) return ['warn', 'alert', 'Letztes Backup ungültig', '', ''];
    if (m.last) return ['err', 'alert', 'Backup ungültig', '', ''];
    return ['warn', 'alert', 'Noch kein Backup', '', ''];
  }
  // Rot und Gelb tragen ein Zeichen, nicht nur die Farbe.
  function cnt(text, kind) { return el('span', { class: 'ls-cnt' + (kind ? ' ' + kind : '') }, kind === 'err' ? ic('bang', 12) : kind === 'warn' ? ic('warn', 12) : null, text); }
  // Abzeichen in der Seitenleiste nur für das, was dich braucht (rot/gelb
  // aus „Braucht dich“) oder gerade läuft. Mengen wie „9 Projekte“ oder
  // „10 GB“ stehen auf den Seiten selbst.
  function attBadges(m) {
    const by = {};
    for (const x of attention(m)) {
      if (!x.pg || (x.k !== 'err' && x.k !== 'warn')) continue;
      const b = by[x.pg] || (by[x.pg] = { n: 0, err: false });
      b.n++;
      if (x.k === 'err') b.err = true;
    }
    return pg => by[pg] ? cnt(String(by[pg].n), by[pg].err ? 'err' : 'warn') : null;
  }
  // Domains, die nicht antworten, laut der einen Quelle; pausierte und
  // abgeschaltete zählen nicht.
  function liveDomainBadge() {
    if (!dz.list) return undefined;
    applyKept();
    const bad = dz.list.filter(z => z.stufe === 'err' || z.stufe === 'warn');
    return bad.length ? cnt(String(bad.length), bad.some(z => z.stufe === 'err') ? 'err' : 'warn') : null;
  }
  // Ein Abzeichen für zwei Seiten unter einem Eintrag (Backup mit Auswahl,
  // Überwachung mit Leistung).
  function sumBadge(att, a, b) {
    const x = att(a), y = att(b);
    if (!x || !y) return x || y;
    return cnt(String(Number(x.textContent) + Number(y.textContent)), x.classList.contains('err') || y.classList.contains('err') ? 'err' : 'warn');
  }
  // Unterseiten, die in der Seitenleiste unter einem Eintrag liegen.
  // Updates und „Von außen“ sind Reiter der Einstellungen.
  const PARENT = { auswahl: 'backup', leistung: 'ueberwachung', leistungstest: 'ueberwachung', tuning: 'ueberwachung', updates: 'einstellungen', zugang: 'einstellungen' };
  const SUBS = {
    backup: [['backup', 'Backup'], ['auswahl', 'Auswahl']],
    ueberwachung: [['ueberwachung', 'Live und Verlauf'], ['leistung', 'Leistung'], ['leistungstest', 'Leistungstest'], ['tuning', 'Tuning']],
  };
  function setTabs() {
    return [['einstellungen', 'Darstellung'], core.svc() ? ['einstellungen/mitteilungen', 'Mitteilungen'] : null, core.svc() ? ['einstellungen/grenzen', 'Grenzen'] : null,
      ['einstellungen/leitstand', 'Leitstand'], core.svc() ? ['updates', 'Updates'] : null, core.svc() ? ['zugang', 'Zugang von außen'] : null].filter(Boolean);
  }
  // Reiter unter dem Kopf. Für Backup und Überwachung stehen dieselben
  // Teile in der Seitenleiste; dort nur, solange sie zugeklappt ist.
  function subTabs(cur, key) {
    const top = PARENT[cur] || cur;
    const set = top === 'einstellungen' ? setTabs() : SUBS[top];
    if (!set) return null;
    const at = cur === 'einstellungen' && key ? cur + '/' + key : cur;
    return el('nav', { class: 'ls-subtabs' + (SUBS[top] ? ' in-nav' : ''), 'aria-label': 'Teile von ' + PAGES[top] }, set.map(([id, label]) =>
      el('a', { href: '#/' + id, 'aria-current': id === at ? 'page' : null }, label)));
  }
  // Abzeichen der Projekte: dieselbe Zahl wie der Filter „Braucht dich“,
  // sobald der Live-Stand da ist.
  function projBadge(m) {
    const rows = rowsAll(m);
    for (const x of rows) x.live = liveSum(liveFor(x));
    const n = rows.filter(PF[1][2]).length;
    return n ? cnt(String(n), 'err') : null;
  }
  // Seitenleiste: was man täglich braucht oben, „Verwaltung“ darunter,
  // Einstellungen ganz unten. Abzeichen nur für Rotes, Gelbes und Läufe.
  function navItems(m) {
    const att = attBadges(m);
    const dLive = liveDomainBadge();
    const sv = core.svc();
    return [
      [null, [
        ['lage', 'Übersicht', 'lage', null],
        ['projekte', 'Projekte', 'projekte', live() ? projBadge(m) : att('projekte'), projKids(m)],
        ['domains', 'Domains', 'domains', dLive === undefined ? att('domains') : dLive],
        sv ? ['ueberwachung', 'Überwachung', 'puls', ltBadge() || tnBadge() || sumBadge(att, 'ueberwachung', 'leistung')] : null,
        sv ? ['protokoll', 'Protokoll', 'protokoll', null] : null,
      ].filter(Boolean)],
      ['Verwaltung', [
        ['backup', 'Backup', 'backup', backupBadge() || (m.dirty ? cnt('geändert', 'acc') : m.alltag ? att('backup') : sumBadge(att, 'backup', 'auswahl'))],
        sv ? ['sicherheit', 'Sicherheit', 'sicherheit', scBadge() || att('sicherheit')] : null,
        ['platz', 'Speicherplatz', 'platz', att('platz')],
        // Im Alltag plant man hier den nächsten Umzug.
        // Steht ein Umzug an, trägt der Eintrag den Schritt, der dran ist.
        ['umzug', m.alltag ? 'Umzug planen' : 'Umzug', 'umzug', moveBadge() || (m.alltag || !sv ? null : att('umzug') || lotseStep(m))],
      ].filter(Boolean)],
    ];
  }
  // Projekte in der Leiste: Spielserver (erkannt), Webseiten (mit Domain),
  // Weitere. Nur wenn es mehr als eine Gruppe gibt, sonst sagt das nichts.
  const PG_NAV = [['spiel', 'Spielserver'], ['web', 'Webseiten'], ['weitere', 'Weitere']];
  function projKids(m) {
    if (core.svc() && !mon.rep && !mon.loading && !mon.at) setTimeout(loadMon, 0);
    const rows = pRows(m);
    const n = g => rows.filter(x => x.grp === g).length;
    const out = PG_NAV.map(([g, t]) => ['projekte/gruppe:' + g, t, n(g)]).filter(k => k[2] > 0);
    return out.length > 1 ? out.map(([h, t, c]) => [h, t, cnt(String(c))]) : null;
  }
  function kidBadge(id, att) {
    return id === 'leistungstest' ? ltBadge() : id === 'tuning' ? tnBadge() : id === 'leistung' ? att('leistung') : id === 'auswahl' ? att('auswahl') : null;
  }
  const shell = { r: null, at: '', bell: false, lauf: null };
  function renderShell(r) {
    // Neue Seite: offene Menüs oben rechts schließen.
    const at = r.view + '/' + (r.page || '');
    if (shell.at && shell.at !== at) shell.bell = false;
    shell.at = at;
    shell.r = r;
    const m = model();
    const cur = r.view === 'ls' ? r.page : 'auswahl';
    const navCur = PARENT[cur] || cur;
    const att = attBadges(m);
    document.getElementById('srv').replaceChildren(
      el('span', { class: 'ls-mark', 'aria-hidden': 'true' }, 'HD'),
      el('span', {}, el('b', {}, core.hostName()), el('small', {}, [m.s.host.os, m.s.host.docker ? 'Docker ' + m.s.host.docker : ''].filter(Boolean).join(' · ') || 'Leitstand')));
    document.getElementById('nav').replaceChildren(...navItems(m).map(([g, items]) =>
      el('div', { class: 'ls-nav-grp', role: g ? 'group' : null, 'aria-label': g }, g ? el('p', { class: 'ls-nav-g', 'aria-hidden': 'true' }, g) : null, items.map(([id, label, icon, badge, own]) => {
        // Eigene Unterpunkte (Projekt-Gruppen) stehen immer da; die
        // Unterseiten von Backup und Überwachung nur, solange man dort ist.
        if (own) {
          const at = navCur === id && r.key && r.key.startsWith('gruppe:') ? id + '/' + r.key : '';
          return [el('a', { href: '#/' + id, 'aria-current': id === navCur && !at ? 'page' : null }, ic(icon), el('span', {}, label), badge),
            el('div', { class: 'kids' }, own.map(([k, t, b]) => el('a', { href: '#/' + k, 'aria-current': k === at ? 'page' : null }, el('span', {}, t), b)))];
        }
        const kids = SUBS[id] && navCur === id ? SUBS[id] : null;
        return [el('a', { href: '#/' + id, class: kids ? 'on' : null, 'aria-current': id === navCur && !kids ? 'page' : null }, ic(icon), el('span', {}, label), badge),
          kids ? el('div', { class: 'kids' }, kids.map(([k, t]) => el('a', { href: '#/' + k, 'aria-current': k === cur ? 'page' : null }, el('span', {}, t), kidBadge(k, att)))) : null];
      }))));
    const sv = core.svc();
    const setBadge = sv ? (up.phase === 'installing' ? cnt('läuft', 'acc') : zugangBadge()) : null;
    document.getElementById('set').replaceChildren(
      el('a', { href: '#/einstellungen', 'aria-current': navCur === 'einstellungen' ? 'page' : null }, ic('einstellungen'), el('span', {}, 'Einstellungen'), setBadge), meEntry());
    document.getElementById('side-foot').replaceChildren(...foot());
    const mb = document.getElementById('menu-btn');
    if (!mb.firstChild) mb.append(ic('menu', 20));
    const sb = document.getElementById('search-btn');
    if (!sb.firstChild) sb.append(ic('search', 17), el('span', {}, 'Suchen'), el('kbd', {}, 'Strg K'));
    const tb = document.getElementById('tabbar');
    tb.replaceChildren(
      ...[['lage', 'Übersicht', 'lage'], ['projekte', 'Projekte', 'projekte'], ['domains', 'Domains', 'domains'],
        sv ? ['ueberwachung', 'Überwachung', 'puls'] : ['backup', 'Backup', 'backup']].map(([id, label, icon]) =>
        el('a', { href: '#/' + id, 'aria-current': id === navCur ? 'page' : null }, ic(icon, 21), el('span', {}, label))),
      el('button', { type: 'button', id: 'more-btn', 'aria-controls': 'side', 'aria-expanded': String(document.body.classList.contains('nav-open')), onclick: () => openNav(true) }, ic('menu', 21), el('span', {}, 'Mehr')));
    if (sv && !look.synced && !look.loading) lookLoad();
    renderRun();
    renderLookBtn();
    renderBell(m);
  }
  // Oben: was gerade läuft, als Hinweis mit Weg zur passenden Seite. Endet
  // ein Lauf, meldet ein Hinweis unten das Ergebnis aus dem Protokoll.
  const RUN_PAGE = [[/^Leistungstest/, 'leistungstest'], [/^Tuning|^Neustart des Servers/, 'tuning'], [/^Backup|^Probelauf$/, 'backup'], [/Umzug|Probe-Kopie|Rückweg|Generalprobe/, 'umzug'], [/Update/, 'updates'], [/Aufräumen|Papierkorb/, 'platz'],
    [/Caddy|Domain/, 'domains'], [/Firewall|Port|Sicherheit/, 'sicherheit'], [/Bestandsaufnahme/, 'lage']];
  function runPage(was) { const x = RUN_PAGE.find(([re]) => re.test(was)); return x ? x[1] : 'protokoll'; }
  function renderRun() {
    const top = document.querySelector('.ls-top');
    let box = document.getElementById('run');
    if (!box) { box = el('a', { id: 'run', class: 'ls-run', hidden: true }); top.insertBefore(box, document.getElementById('theme')); }
    const sv = core.svc();
    const l = sv && sv.lauf;
    const prev = shell.lauf;
    shell.lauf = l ? { was: l.was, seit: l.seit } : null;
    if (prev && !l) runDone(prev);
    box.hidden = !l;
    document.body.classList.toggle('has-run', !!l);
    if (!l) { box.replaceChildren(); return; }
    box.href = '#/' + runPage(l.was);
    box.setAttribute('aria-label', 'Läuft gerade: ' + l.was + ', seit ' + when(l.seit));
    box.replaceChildren(el('span', { class: 'spin', 'aria-hidden': 'true' }), el('span', { class: 'rl' }, el('b', {}, l.was), el('span', {}, ' läuft')));
  }
  async function runDone(prev) {
    const start = Date.parse(prev.seit) || 0;
    // Die Bestandsaufnahme meldet app.js selbst; kurze Läufe brauchen keinen Hinweis.
    if (prev.was === 'Bestandsaufnahme' || Date.now() - start < 8000) return;
    const r = await core.call('/api/protokoll', null, true, true);
    const list = (r.ok && r.data && r.data.eintraege) || [];
    const e = list.find(x => Date.parse(x.zeit) >= start - 1000 && !/ gestartet$/.test(x.text) && !['anmeldung', 'abmeldung', 'link', 'fehlversuch', 'bestaetigung'].includes(x.art));
    jr.stale = true;
    const bad = e && /ging schief|fehlgeschlagen|ungültig|gescheitert|nicht gestartet|nicht geklappt|Fehler|abgebrochen/i.test(e.text);
    notice(bad ? 'err' : 'ok', prev.was + (bad ? ': nicht geklappt' : ' ist fertig'), e ? e.text : '', '#/protokoll', 'Im Protokoll');
  }
  // Hinweis unten mit Weg: bleibt 9 Sekunden, solange der Zeiger nicht darauf liegt.
  // href kann auch eine Funktion sein (etwa „Rückgängig“): dann ein Knopf.
  function notice(kind, title, text, href, label) {
    const t = el('div', { class: 'ls-toast ' + kind, role: 'status' },
      el('span', { class: 'ti' }, ic(kind === 'err' ? 'warn' : 'done', 18)),
      el('div', { class: 'tt' }, el('b', {}, title), text ? el('span', {}, text) : null),
      typeof href === 'function' ? el('button', { type: 'button', class: 'btn btn-sm btn-g', 'data-fid': 'toast-act', onclick: () => { t.remove(); href(); } }, label || 'Ansehen')
        : href ? el('a', { class: 'btn btn-sm btn-g', href, onclick: () => t.remove() }, label || 'Ansehen') : null,
      el('button', { type: 'button', class: 'ls-ibtn tx', 'aria-label': 'Hinweis schließen', onclick: () => t.remove() }, ic('x', 16)));
    let timer = 0;
    const arm = () => { clearTimeout(timer); timer = setTimeout(() => t.remove(), 9000); };
    t.addEventListener('pointerenter', () => clearTimeout(timer));
    t.addEventListener('pointerleave', arm);
    t.addEventListener('focusin', () => clearTimeout(timer));
    document.getElementById('toasts').append(t);
    arm();
    return t;
  }
  // Glocke oben: wie viel dich braucht, als Liste zum Aufklappen. Dieselben
  // Punkte wie „Braucht dich“ auf der Übersicht.
  function renderBell(m) {
    const box = document.getElementById('bell');
    if (!box) return;
    const items = attention(m || model());
    const hot = items.filter(x => x.k === 'err' || x.k === 'warn');
    const err = hot.some(x => x.k === 'err');
    const close = focus => { shell.bell = false; renderBell(); if (focus) document.getElementById('bell-btn').focus(); };
    const open = () => { shell.bell = true; renderBell(); const f = document.querySelector('#bell-menu [role="menuitem"]'); if (f) f.focus(); };
    // Die Zahl an der Glocke zählt nur Gelbes und Rotes; der Kopf der Liste
    // sagt, was sonst noch drinsteht.
    const label = hot.length ? 'Braucht dich: ' + plural(hot.length, 'Warnung', 'Warnungen') : 'Nichts braucht dich gerade';
    box.replaceChildren(el('button', {
      type: 'button', class: 'ls-ibtn ls-bell-btn', id: 'bell-btn', 'aria-label': label, title: label, 'aria-haspopup': 'menu',
      'aria-expanded': String(shell.bell), 'aria-controls': shell.bell ? 'bell-menu' : null,
      onclick: () => shell.bell ? close(false) : open(),
      onkeydown: e => { if (e.key === 'ArrowDown' && !shell.bell) { e.preventDefault(); open(); } },
    }, ic('bell', 19), hot.length ? el('span', { class: 'ls-bell-n' + (err ? ' err' : ' warn'), 'aria-hidden': 'true' }, hot.length > 9 ? '9+' : String(hot.length)) : null));
    if (!shell.bell) return;
    const go = () => { shell.bell = false; renderBell(); };
    box.append(el('div', { class: 'menu ls-bellm', role: 'menu', id: 'bell-menu', 'aria-label': 'Braucht dich', onkeydown: e => core.menuKeys(e, () => close(true)) },
      el('div', { class: 'ls-bellm-h', 'aria-hidden': 'true' }, el('b', {}, 'Braucht dich'), el('span', {}, hot.length ? plural(hot.length, 'Warnung', 'Warnungen') + (items.length > hot.length ? ' · ' + plural(items.length - hot.length, 'Hinweis', 'Hinweise') : '') : items.length ? plural(items.length, 'Hinweis', 'Hinweise') : 'alles ruhig')),
      items.length ? items.slice(0, 7).map(x => el('a', {
        class: 'mi ls-bellm-i ' + x.k, role: 'menuitem', tabindex: '-1', href: (x.a && x.a.getAttribute && x.a.getAttribute('href')) || '#/lage', onclick: go,
      }, el('span', { class: 'ls-bellm-ic' }, ic(x.icon, 16)), el('span', { class: 'ls-bellm-t' }, el('b', {}, x.t), x.d ? el('span', {}, x.d) : null)))
        : el('p', { class: 'ls-bellm-ok' }, ic('done', 18), el('span', {}, 'Nichts braucht dich gerade.')),
      el('a', { class: 'mi ls-bellm-all', role: 'menuitem', tabindex: '-1', href: '#/lage', onclick: go }, el('span', {}, items.length > 7 ? 'Alle ' + items.length + ' auf der Übersicht' : 'Zur Übersicht'), ic('right', 16))));
  }
  // Seitenleiste und Glocke neu, ohne die Seite anzufassen (etwa wenn die
  // Hinweise des Dienstes nachkommen). Nicht, solange du darin bist.
  function refreshChrome() {
    if (!shell.r || !core.work()) return;
    const a = document.activeElement;
    if (a && a.closest && a.closest('#nav, #set, #bell')) return;
    renderShell(shell.r);
  }
  // Fuß der Seitenleiste: nur, was schiefging (Dienst nicht erreichbar,
  // Bestandsaufnahme gescheitert). Stand und Anmeldung stehen in
  // Einstellungen › Leitstand.
  // Ganz unten: wie du angemeldet bist, mit Abmelden. Mehr dazu steht in
  // Einstellungen › Leitstand.
  function meEntry() {
    const s = gate.sess;
    if (!s) return null;
    return el('div', { class: 'ls-me' },
      el('a', { href: '#/einstellungen/leitstand', class: 'ls-me-t', title: 'Angemeldet ' + via(s) + ', seit ' + when(s.seit) }, ic('user', 16), el('span', {}, 'Angemeldet', el('small', {}, via(s)))),
      el('button', { type: 'button', class: 'ls-me-out', 'data-fid': 'side-out', onclick: () => logout(false) }, ic('logout', 15), el('span', {}, 'Abmelden')));
  }
  function foot() {
    const sv = core.svc();
    const b = (sv && sv.bestandsaufnahme) || {};
    const err = core.svcError();
    return [
      b.fehler && !b.laeuft ? el('a', { class: 'err', href: '#/einstellungen/leitstand' }, ic('warn', 16), el('span', {}, 'Bestandsaufnahme ging schief')) : null,
      err ? el('div', { class: 'err', role: 'alert' }, ic('warn', 16), el('span', {}, err)) : null,
    ].filter(Boolean);
  }
  // Knopf neben einer Stand-Angabe; nur wenn er gerade geht, den Grund
  // dafür nennt der Fuß der Seitenleiste.
  function quickDiscover(cls) {
    const c = core.discoverCtl(cls, 'Neu erfassen');
    return c && !c.why ? c.btn : null;
  }
  function openNav(on) {
    // Schmal liegt die Seitenleiste über der Seite; solange sie offen ist,
    // erreicht die Tastatur nur sie.
    const narrow = window.matchMedia && window.matchMedia('(max-width: 980px)').matches;
    document.body.classList.toggle('nav-open', on);
    document.querySelector('.ls-main').inert = !!(on && narrow);
    document.getElementById('tabbar').inert = !!(on && narrow);
    document.getElementById('menu-btn').setAttribute('aria-expanded', on ? 'true' : 'false');
    if (on) { const a = document.querySelector('#nav a[aria-current]') || document.querySelector('#nav a'); if (a) a.focus(); }
  }

  // ---------- Übersicht ----------
  // Hinweise des Dienstes: Platte, Speicher, Zertifikate,
  // Neustart-Schleifen, Ports. Nur anzeigen; der Dienst hält sie 30 s.
  const hn = { list: null, kept: [], at: 0, loading: false, busy: '' };
  async function loadHints() {
    if (hn.loading || !core.svc()) return;
    hn.loading = true;
    const r = await core.call('/api/hinweise', null, true);
    hn.loading = false;
    hn.at = Date.now();
    if (r.ok) { hn.list = r.data.hinweise || []; hn.kept = r.data.so_gelassen || []; applyKept(); }
    if (ui.page === 'lage' && core.work() && !modalOpen()) core.rerender();
    else refreshChrome();
  }
  // ---------- Zustand der Domains (eine Quelle) ----------
  // Der Dienst rechnet aus, ob eine Domain antwortet, pausiert ist (du hast
  // ihr Ziel gestoppt), aus ist oder nicht antwortet. Der Leitstand zeigt nur,
  // was er sagt, überall mit denselben Wörtern. Den Routen-Status der
  // Bestandsaufnahme zeigt er nie als aktuellen Stand.
  const dz = { list: null, by: null, at: 0, loading: false, timer: 0, checked: null };
  function setDz(list) {
    dz.list = list || [];
    dz.by = new Map();
    for (const z of dz.list) for (const h of z.domains || [z.domain]) dz.by.set(h, z);
    applyKept();
    core.setDomainStates(dz.list);
  }
  // Was du bewusst so gelassen hast (Warnungen seite:<domain> und
  // zertifikat:<domain>), zählt nirgends mit: kein Abzeichen, keine Färbung,
  // nicht im Zustand (Datenprüfung M8). Die Domain steht
  // grau mit dem Wort da; sie antwortet ja wirklich nicht.
  function keptKeys() {
    const k = new Set((hn.kept || []).map(h => h.schluessel));
    const w = mon.rep && mon.rep.warnungen;
    for (const a of (w && w.aktiv) || []) if (a.so_gelassen) k.add(a.schluessel);
    return k;
  }
  function applyKept() {
    if (!dz.list) return;
    const k = keptKeys();
    for (const z of dz.list) {
      if (!('stufe0' in z)) { z.stufe0 = z.stufe; z.wort0 = z.wort; }
      z.kept = (z.stufe0 === 'err' || z.stufe0 === 'warn') && (z.domains || [z.domain]).some(h => k.has('seite:' + h));
      z.stufe = z.kept ? 'idle' : z.stufe0;
      z.wort = z.kept ? 'bewusst so gelassen' : z.wort0;
    }
  }
  async function loadDz() {
    clearTimeout(dz.timer);
    dz.timer = 0;
    if (dz.loading || !core.svc()) return;
    dz.loading = true;
    const r = await core.call('/api/domains/zustand', null, true);
    dz.loading = false;
    dz.at = Date.now();
    if (!r.ok) return;
    setDz(r.data.zustand);
    dz.checked = r.data.geprueft || null;
    // Läuft die Prüfung noch, gleich noch einmal nachsehen.
    if (r.data.pruefung_laeuft || dz.list.some(z => z.zustand === 'wird-geprueft' || z.zustand === 'startet')) dz.timer = setTimeout(loadDz, r.data.pruefung_laeuft ? 2000 : 15000);
    if (['lage', 'projekte', 'domains', 'ueberwachung'].includes(ui.page) && core.work() && !modalOpen() && !uw.hover) core.rerender();
    else refreshChrome();
  }
  function dzFresh(force) {
    if (!core.svc() || dz.loading) return;
    if (force) { dz.at = 0; clearTimeout(dz.timer); dz.timer = 0; }
    if (!dz.at || Date.now() - dz.at > 60000) setTimeout(loadDz, 0);
  }
  function zOf(hosts) {
    if (!dz.by) return null;
    for (const h of hosts || []) if (dz.by.has(h)) return dz.by.get(h);
    return null;
  }
  function zSt(z, sub) {
    const s = st(z.stufe, z.wort);
    if (z.text) s.title = z.text;
    return sub && z.text && z.zustand !== 'antwortet' ? [s, el('span', { class: 'sub' }, z.text)] : s;
  }
  // Eine Route im Leitstand: live, sonst als Stand der Bestandsaufnahme.
  function routeSt(r) {
    const z = r.z || zOf(r.hosts);
    if (z) return zSt(z);
    const s = st((ROUTE[r.status] || ['', 'idle'])[1], (ROUTE[r.status] || [r.status])[0]);
    s.title = 'Stand der Bestandsaufnahme';
    return s;
  }
  function routeDead(r) {
    const z = r.z || zOf(r.hosts);
    return z ? z.stufe === 'err' || z.stufe === 'warn' : !core.svc() && r.status === 'nichts-lauscht';
  }
  // Domains brechen nach einem Punkt um, nicht mitten im Namen.
  const dotBreak = d => String(d).split(/(?<=\.)/).map((x, i) => i ? [el('wbr'), x] : x);
  // Zahlen für Kopfzeilen und Kacheln: überall dieselben. Als Dienst
  // aus dem Zustand, sonst aus der Bestandsaufnahme.
  function domSum(routes) {
    if (core.svc()) {
      dzFresh();
      applyKept();
      const zs = (dz.list || []).filter(z => z.zustand !== 'aus');
      const checked = zs.filter(z => z.zustand !== 'wird-geprueft');
      const bad = checked.filter(z => z.stufe === 'err' || z.stufe === 'warn').map(z => ({ hosts: z.domains || [z.domain], text: z.text || z.wort, wort: z.wort, stufe: z.stufe }));
      const paused = checked.filter(z => z.zustand === 'pausiert').length;
      const quiet = checked.filter(z => z.zustand === 'startet').length;
      const kept = checked.filter(z => z.kept).length;
      return { live: true, checked: checked.length, total: checked.length - paused - quiet - kept, ok: checked.filter(z => z.zustand === 'antwortet').length, paused, quiet, kept, bad, err: bad.some(b => b.stufe === 'err') };
    }
    const checked = routes.filter(r => r.status !== 'extern' && r.status !== 'kein-proxy');
    const bad = checked.filter(r => r.status === 'nichts-lauscht').map(r => ({ hosts: r.hosts, text: 'auf Port ' + (r.upstream || '').split(':').pop() + ' lauschte nichts', stufe: 'err' }));
    const paused = checked.filter(r => r.status === 'container-gestoppt').length;
    return { live: false, checked: checked.length, total: checked.length - paused, ok: checked.filter(r => r.status === 'ok').length, paused, quiet: 0, bad, err: bad.length > 0 };
  }
  function domHead(d) {
    const verb = d.live ? 'antworten' : 'antworteten';
    const tail = [d.paused ? d.paused + ' pausiert' : '', d.quiet ? d.quiet + ' ' + (d.quiet === 1 ? 'startet' : 'starten') + ' gerade' : '', d.kept ? d.kept + ' bewusst so gelassen' : ''].filter(Boolean);
    const rest = tail.length ? ' ' + tail.join(', ') + '.' : '';
    // Eine Domain mit dem Wort ihrer Zeile (Datenprüfung N3), mehrere gezählt.
    if (d.bad.length) return [d.ok + ' von ' + plural(d.total, 'Domain', 'Domains') + ' ' + verb + '. ', em(d.err ? 'err' : 'warn', d.bad.length === 1 && d.bad[0].wort ? d.bad[0].hosts[0] + ' ' + d.bad[0].wort : d.bad.length + ' ' + (d.live ? (d.bad.length === 1 ? 'antwortet nicht' : 'antworten nicht') : (d.bad.length === 1 ? 'zeigte ins Leere' : 'zeigten ins Leere'))), '.' + rest];
    if (!d.total) return d.paused ? [em('', plural(d.paused, 'Domain', 'Domains') + ' pausiert'), '; du hast ihr Ziel gestoppt.' + (d.kept ? ' ' + d.kept + ' bewusst so gelassen.' : '')] : d.kept ? [plural(d.kept, 'Domain', 'Domains') + ' bewusst so gelassen.'] : ['Keine Domain zu prüfen.'];
    return ['Alle ' + plural(d.total, 'Domain', 'Domains') + ' ', em('ok', verb), '.' + rest];
  }

  const BACKUP_OLD_DAYS = 7;
  function attention(m) {
    const out = [];
    // Seite, auf die der Knopf führt: daraus zählt die Seitenleiste ihre Abzeichen.
    const add = (k, icon, t, d, a) => {
      const href = a && a.getAttribute ? a.getAttribute('href') || '' : '';
      out.push({ k, icon, t, d, a, pg: (/^#\/([a-z]+)/.exec(href) || [])[1] || '' });
    };
    if (core.svc()) {
      if (!hn.loading && (!hn.at || Date.now() - hn.at > 60000)) setTimeout(loadHints, 0);
      for (const h of hn.list || []) {
        add(h.stufe, h.symbol, h.titel, h.text, h.link ? link(h.link, h.knopf || 'Ansehen') : null);
        if (h.so_lassen) out[out.length - 1].so = h.schluessel;
      }
    }
    if (m.mstate === 'ungueltig') add('err', 'warn', 'Deine Auswahl ist beschädigt', 'manifest.yaml lässt sich nicht lesen. In der Auswahl siehst du, was nicht stimmt; Speichern legt eine neue an.', link('#/auswahl', 'Auswahl prüfen'));
    // Als Dienst meldet die Überwachung Domains, die nicht antworten (eine
    // Quelle); ohne Dienst bleibt nur der Stand der Bestandsaufnahme.
    if (!core.svc() && m.dom.bad.length) add('warn', 'unlink', plural(m.dom.bad.length, 'Domain zeigte', 'Domains zeigten') + ' ins Leere',
      'Stand der Bestandsaufnahme vom ' + fmtDate(m.s.created) + ': ' + m.dom.bad.map(d => d.hosts.join(', ') + ': ' + d.text).join('. ') + '.',
      link('#/domains', 'Ansehen'));
    // Als Dienst sagen die Hinweise der Überwachung live, was steht; der
    // Stand der Bestandsaufnahme wäre dann veraltet und widerspräche ihnen.
    const stopped = core.svc() ? [] : m.projects.filter(p => p.total > 0 && !p.ok);
    if (stopped.length) add('err', 'projekte', stopped.length === 1 ? 'Bei ' + stopped[0].name + ' stand ein Container' : 'In ' + stopped.length + ' Projekten standen Container',
      stopped.map(p => p.name + ': ' + p.running + ' von ' + p.total + (p.live ? ' laufen' : ' liefen')).join(', ') + '. Ob das gewollt ist, weißt nur du.',
      el('a', { class: 'btn btn-sm', href: '#/projekte', onclick: () => { ui.pf = 'dich'; } }, 'Ansehen'));
    // Die Firewall steht nur neutral unter Sicherheit, nie als Aufgabe:
    // Docker öffnet veröffentlichte Ports ohnehin an ihr vorbei.
    if (m.alltag) {
      if (core.svc() && core.svc().abschluss_offen) add('acc', 'umzug', 'Umzug abschließen', 'Ein paar Punkte nach dem Einzug: alte Tabs, erstes Backup hier, Testadresse, TTL, Reserve-Woche des alten Servers.', link('#/umzug', 'Ansehen'));
      if (m.dirty) add('acc', 'edit', 'Auswahl geändert, aber nicht gespeichert', plural(m.dirty, 'Änderung wartet', 'Änderungen warten') + ' in der Auswahl auf Speichern.', link('#/auswahl', 'Zur Auswahl'));
      const rank = { err: 0, warn: 1 };
      return out.sort((a, b) => (rank[a.k] ?? 2) - (rank[b.k] ?? 2));
    }
    // Mit Dienst führt der Lotse: ein einziger Eintrag „Umzug: <Schritt>“
    // statt einzelner Hinweise zu Auswahl und Backup .
    // Auf einer Probe-Kopie und einem abgelösten Server gehen Backups und
    // Umzüge nicht; dort gibt es auch keinen Eintrag dazu.
    if (core.svc()) {
      const lh = !core.svc().kopie && !core.svc().abgeloest ? lotseHint(m) : null;
      if (lh) add('acc', 'umzug', lh.t, lh.d, link('#/umzug', 'Weiter'));
      // Die Warnungen der Bestandsaufnahme betreffen, was ins Backup kommt;
      // auf einer Probe-Kopie und einem abgelösten Server gibt es keins.
      if (m.warnings.length && !core.svc().kopie && !core.svc().abgeloest) add('warn', 'warn', plural(m.warnings.length, 'Warnung', 'Warnungen') + ' aus der Bestandsaufnahme', m.warnings.slice(0, 2).map(f => f.message.split('. ')[0].replace(/\.$/, '')).join('. ') + (m.warnings.length > 2 ? ' und weitere.' : '.'), link('#/auswahl/bereich/warnungen', 'Warnungen lesen'));
      const rank = { err: 0, warn: 1 };
      return out.sort((a, b) => (rank[a.k] ?? 2) - (rank[b.k] ?? 2));
    }
    if (m.mstate === 'fehlt') add('warn', 'auswahl', 'Deine Auswahl fehlt noch', 'Bevor HostDeck sichern kann, entscheidest du, was mitkommt. Ein paar Fragen reichen.', link('#/auswahl/start', 'Auswahl starten', 'btn btn-p btn-sm'));
    else if (m.open) add('warn', 'auswahl', plural(m.open, 'Eintrag', 'Einträge') + ' in der Auswahl offen', 'Solange etwas offen ist, startet kein Backup.', link('#/auswahl', 'Entscheiden', 'btn btn-p btn-sm'));
    if (m.gone && m.mstate === 'vorhanden') add('warn', 'auswahl', plural(m.gone, 'Eintrag', 'Einträge') + ' nicht mehr gefunden', 'Sie stehen noch in deiner Auswahl, sind auf dem Server aber weg. Entfernen und speichern, sonst startet kein Backup.', link('#/auswahl', 'Ansehen', 'btn btn-p btn-sm'));
    if (m.dirty) add('acc', 'edit', 'Auswahl geändert, aber nicht gespeichert', plural(m.dirty, 'Änderung wartet', 'Änderungen warten') + ' in der Auswahl auf Speichern.', link('#/auswahl', 'Zur Auswahl'));
    if (m.s.manifest.added.length && m.mstate === 'vorhanden') add('warn', 'info', plural(m.s.manifest.added.length, 'neuer Eintrag', 'neue Einträge') + ' seit dem letzten Speichern', 'HostDeck hat sie mit Vorschlag eingetragen. Prüfen und speichern.', link('#/auswahl', 'Prüfen'));
    if (!m.lastValid) add('warn', 'backup', m.last ? 'Kein gültiges Backup' : 'Noch kein Backup', m.last ? 'Der letzte Lauf war ungültig: ' + m.last.verdict.replace(/^UNGÜLTIG:?\s*/, '') : 'Bis zum ersten gültigen Backup gibt es kein Sicherheitsnetz für den Umzug.', link('#/backup', core.svc() ? 'Zum Backup' : 'So geht das Backup'));
    else {
      const days = Math.floor((Date.now() - new Date(m.lastValid.started)) / 864e5);
      // Als Dienst erinnert die Überwachung an alte Backups (eigener Hinweis).
      if (!core.svc() && days >= BACKUP_OLD_DAYS) add('warn', 'backup', 'Letztes gültiges Backup ist ' + days + ' Tage alt', 'Was seitdem dazukam, fehlt im Sicherheitsnetz. Vor dem Umzug und vor größeren Änderungen lohnt ein neues.', link('#/backup', core.svc() ? 'Zum Backup' : 'So geht das Backup'));
    }
    if (m.warnings.length) add('warn', 'warn', plural(m.warnings.length, 'Warnung', 'Warnungen') + ' aus der Bestandsaufnahme', m.warnings.slice(0, 2).map(f => f.message.split('. ')[0].replace(/\.$/, '')).join('. ') + (m.warnings.length > 2 ? ' und weitere.' : '.'), link('#/auswahl/bereich/warnungen', 'Warnungen lesen'));
    const rank = { err: 0, warn: 1 };
    return out.sort((a, b) => (rank[a.k] ?? 2) - (rank[b.k] ?? 2));
  }
  // Leertext von „Braucht dich“: behauptet nur, was stimmt. Was du
  // absichtlich gestoppt hast, steht mit Zahl darin.
  function quietText(m) {
    if (!core.svc()) return 'Laut Bestandsaufnahme wartet nichts auf dich.';
    const sm = op.rep && op.rep.summe;
    const parts = [];
    if (sm && sm.von_dir) parts.push('Du hast ' + plural(sm.von_dir, 'Container', 'Container') + ' gestoppt');
    if (sm && sm.letzter_lauf) parts.push(plural(sm.letzter_lauf, 'Container ist', 'Container sind') + ' seit dem letzten Lauf aus');
    if (sm && sm.angelegt) parts.push(plural(sm.angelegt, 'Container ist', 'Container sind') + ' nur angelegt');
    if (m && m.dom.paused) parts.push(plural(m.dom.paused, 'Domain ist', 'Domains sind') + ' pausiert');
    if (!parts.length) return sm ? 'Alles läuft; nichts wartet auf dich.' : 'Nichts wartet auf dich.';
    return parts.join(', ') + '. Das ist gewollt; HostDeck warnt dafür nicht.';
  }
  function attList(items, m) {
    if (!items.length) return el('div', { class: 'ls-empty ok' }, ic('done', 24), el('div', {}, el('b', {}, 'Nichts braucht dich gerade.'), el('p', {}, quietText(m)), keptLine()));
    return el('ol', { class: 'ls-att' }, items.map(x => el('li', { class: 'ls-att-i ' + x.k },
      el('span', { class: 'ls-att-ic' }, ic(x.icon, 18)),
      el('div', { class: 'ls-att-t' }, el('b', {}, x.t), el('span', {}, x.d)),
      el('div', { class: 'ls-att-a' }, x.a, x.so ? keepBtn(x.so, x.t) : null))));
  }
  // „Bewusst so lassen“ (Designvorgabe, Grundregel 4): Die Warnung zählt
  // dann nirgends mehr und erinnert nicht; wird es schlimmer oder tritt sie
  // nach ihrem Ende neu auf, warnt HostDeck wieder. Nur wo es ein gewollter
  // Zustand sein kann; welche das sind, sagt der Dienst (so_lassen).
  function keepBtn(key, title, cls) {
    return el('button', { type: 'button', class: cls || 'btn btn-sm btn-g', 'data-fid': 'keep-' + key, disabled: !!hn.busy, 'aria-label': 'Bewusst so lassen: ' + title,
      onclick: () => keepAlert(key, true, title) }, ic('eyeoff', 16), 'Bewusst so lassen');
  }
  async function keepAlert(key, on, title) {
    if (hn.busy) return;
    const had = document.activeElement && document.activeElement.closest('[data-fid^="keep-"], [data-fid^="unkeep-"]');
    hn.busy = key;
    core.rerender();
    const r = await core.call('/api/ueberwachung/so-lassen', { schluessel: key, zuruecknehmen: !on });
    hn.busy = '';
    if (!r.ok) {
      core.rerender();
      notice('err', on ? 'Ging nicht' : 'Zurücknehmen ging nicht', (r.data && r.data.error) || 'Fehler ' + r.status);
      return;
    }
    hn.at = 0; mon.at = 0;
    await Promise.all([loadHints(), loadMon()]);
    core.rerender();
    const t = notice('ok', on ? 'Bleibt bewusst so' : 'HostDeck warnt wieder', on ? title + '. HostDeck erinnert nicht mehr daran. Wird es schlimmer oder tritt es neu auf, warnt es wieder.' : title + ' steht wieder als Meldung auf der Übersicht und in der Glocke.',
      on ? () => keepAlert(key, false, title) : null, 'Rückgängig');
    // Der Knopf ist mit der Warnung verschwunden: Der Fokus geht zum
    // Rückgängig-Knopf statt an den Anfang der Seite.
    const b = had && t.querySelector('[data-fid="toast-act"]');
    if (b && (!document.activeElement || document.activeElement === document.body)) b.focus();
  }
  // Was du bewusst so gelassen hast, steht als eine Zeile unter der Liste:
  // nicht als Aufgabe, aber auch nicht versteckt.
  function keptLine() {
    const k = hn.kept || [];
    if (!k.length) return null;
    return el('p', { class: 'ls-mut ls-kept' }, ic('eyeoff', 15), el('span', {}, (k.length === 1 ? 'Bewusst so gelassen: ' + k[0].titel + '. ' : plural(k.length, 'Warnung lässt', 'Warnungen lässt') + ' du bewusst so. '),
      el('a', { href: '#/ueberwachung', 'data-fid': 'kept-go' }, 'Unter Überwachung ansehen')));
  }
  // Ziel einer Domain ohne Projekt, in den Worten des Leitstands.
  function looseTarget(z) {
    if (z.leitstand) return 'HostDeck (dieser Leitstand)';
    if (z.projekt) return 'Projekt ' + z.projekt;
    if (/^Dienst /.test(z.ziel || '')) return z.ziel;
    return z.ziel ? 'Container ' + z.ziel : 'kein Ziel';
  }

  // ---------- Übersicht: Kennzahlen, was dich braucht, Verlauf ----------
  // Kennzahlen kommen live aus der Überwachung, die Linien aus den letzten
  // 24 Stunden. Ohne Dienst (hostdeck serve) aus der Bestandsaufnahme.
  const ov = { hist: null, at: 0, loading: false, timer: 0, all: false, room: 'alles', evAll: false };
  async function loadOvHist() {
    if (ov.loading || !core.svc()) return;
    ov.loading = true;
    const r = await core.call('/api/ueberwachung/verlauf?zeitraum=1d&punkte=144&spitzen=1&serien=' + encodeURIComponent('s/,d/,c/*/cpu,c/*/ram,p/*/cpu,p/*/ram'), null, true);
    ov.loading = false;
    ov.at = Date.now();
    if (r.ok) ov.hist = r.data;
    if (ui.page === 'lage' && core.work() && !modalOpen() && !uw.hover) core.rerender();
  }
  function ovTick() {
    clearTimeout(ov.timer);
    ov.timer = setTimeout(() => {
      ov.timer = 0;
      if (ui.page !== 'lage') return;
      if (document.visibilityState === 'visible' && !uw.hover && !modalOpen()) { mon.at = 0; loadMon(); loadOps(); if (Date.now() - ov.at > 120000) loadOvHist(); }
      ovTick();
    }, 30000);
  }
  // Zahl und Einheit getrennt, etwa „925“ und „MB“.
  function splitUnit(t) {
    const i = t.lastIndexOf(' ');
    return i < 0 ? [t, ''] : [t.slice(0, i), t.slice(i + 1)];
  }
  function kpi(o) {
    return el(o.href ? 'a' : 'div', { class: 'ls-kpi' + (o.kind ? ' ' + o.kind : ''), href: o.href || null, 'data-kpi': o.id || null, 'data-wert': o.wert != null ? String(o.wert) : null },
      el('span', { class: 'k-l' }, el('span', {}, o.l), o.tag ? el('em', { class: o.tag[0] }, o.tag[1]) : null),
      el('b', { class: 'k-v' }, o.v, o.u ? el('small', {}, o.u) : null),
      el('span', { class: 'k-s' }, o.s || ' '),
      o.spark === false ? null : o.spark || el('span', { class: 'k-sp', 'aria-hidden': 'true' }));
  }
  // Balken für eine Kachel: Anteil 0 bis 1.
  function meter(part, label, kind) {
    const s = el('span', { class: 'm-' + (kind || (part >= .9 ? 'cache' : 'docker')) });
    s.style.width = Math.max(0, Math.min(100, part * 100)).toFixed(1) + '%';
    return el('div', { class: 'ls-meter k-mt', role: 'img', 'aria-label': label + ': ' + pctText(part * 100) }, s);
  }
  // Linie mit Fläche für eine Kachel: über die Zeit, für die es Werte gibt
  // (frisch eingerichtet sind das keine 24 Stunden), Fläche je Stück.
  function kSpark(v, h, label, max, minSpan) {
    const times = h.zeiten;
    const idx = v.map((x, i) => x == null ? -1 : i).filter(i => i >= 0);
    const W = 160, H = 32;
    const svg = svgEl('svg', { class: 'uw-sp k-spk', viewBox: '0 0 ' + W + ' ' + H, preserveAspectRatio: 'none', role: 'img', 'aria-label': label });
    if (idx.length < 2) return svg;
    const t0 = times[idx[0]], t1 = times[idx[idx.length - 1]], span = Math.max(1, t1 - t0);
    let peak = 0, low = Infinity;
    for (const i of idx) { if (v[i] > peak) peak = v[i]; if (v[i] < low) low = v[i]; }
    // max 'own': eigene Skala vom kleinsten bis zum größten Wert, damit
    // Bewegung sichtbar wird; tut sich nichts, fällt die Linie weg (Idee 10).
    const own = max === 'own';
    if (own && peak - low < Math.max(Math.abs(peak) * 0.01, 1e-9)) return null;
    // minSpan: so viel zeigt die eigene Skala mindestens, damit ein paar
    // Zehntel keine Klippe werden.
    if (own && minSpan && peak - low < minSpan) { const mid = (peak + low) / 2; low = mid - minSpan / 2; peak = mid + minSpan / 2; }
    const ymin = own ? low - (peak - low) * 0.12 : 0;
    const ymax = own ? peak + (peak - low) * 0.12 : max || niceMax(peak, 'pct');
    const X = t => (t - t0) / span * W, Y = x => 2 + (1 - (Math.min(x, ymax) - ymin) / (ymax - ymin)) * (H - 3);
    const d = uwPath(v, times, h.schritt, X, Y, h);
    for (const part of d.split(' M').filter(Boolean)) {
      const pts = part.replace(/^M/, '').split('L').map(q => q.replace('h0.01', '').trim());
      if (pts.length < 2) continue;
      const x0 = pts[0].split(' ')[0], x1 = pts[pts.length - 1].split(' ')[0];
      svg.append(svgEl('path', { class: 'k-area', d: 'M' + x0 + ' ' + H + 'L' + pts.join('L') + 'L' + x1 + ' ' + H + 'Z' }));
    }
    svg.append(svgEl('path', { class: 'uw-l', 'vector-effect': 'non-scaling-stroke', d }));
    return svg;
  }
  // Gesundheit je Container für die Kachel, aus der Klasse vom Dienst:
  // ok, „unhealthy“, Schleife oder unerwartet aus, gewollt aus. Erledigte
  // Jobs und verwaltete Server zählen nicht.
  const HEALTH = { laeuft: 'ok', unhealthy: 'warn', schleife: 'err', unerwartet: 'err' };
  function healthOf(cs) {
    return cs.filter(c => !c.verwaltet_von && c.klasse !== 'job').map(c => HEALTH[c.klasse] || 'idle');
  }
  // Farben aus den Grenzen des Dienstes (eine Quelle, V13): dieselben Zahlen
  // wie die Regeln unter Überwachung, auch nach „Grenzen ändern“. Ohne
  // Dienst die Voreinstellungen, die HostDeck mit dem Stand mitliefert.
  function limits() {
    const w = mon.rep && mon.rep.warnungen;
    if (w && w.grenzen) return w.grenzen;
    const s = core.svc() ? null : core.state();
    return s && s.grenzen ? s.grenzen : null;
  }
  function cpuLevel(v) { const g = limits(); return g && v != null && v > g.prozessor_gelb ? 'warn' : ''; }
  function ramLevel(used, total) {
    const g = limits();
    if (!g || !total || used == null) return '';
    const free = 1 - used / total;
    return free < g.speicher_rot ? 'err' : free < g.speicher_gelb ? 'warn' : '';
  }
  // Kurze Spitzen färben die Übersicht nicht: Prozessor und
  // Arbeitsspeicher werden dort erst gelb oder rot, wenn die Regel schon
  // warnt oder ihre Wartezeit seit mindestens einer Minute läuft. Die
  // Wartezeit kommt vom Dienst, keine zweite Rechnung hier.
  const LASTING = 60;
  function lasting(key) {
    const w = mon.rep && mon.rep.warnungen;
    if (!w) return null;
    if ((w.aktiv || []).some(a => a.schluessel === key && !a.gut_seit && !a.so_gelassen)) return { aktiv: true };
    const p = (w.wartet || []).find(x => x.schluessel === key);
    const sec = p ? (Date.now() - Date.parse(p.seit)) / 1000 : -1;
    return sec >= LASTING ? { sek: sec, minuten: p.minuten } : null;
  }
  function diskLevel(used, total) {
    const g = limits();
    if (!g || !total) return '';
    const p = used / total, free = total - used;
    const minFree = g.platte_min_frei > 0 && total >= 4 * g.platte_min_frei ? g.platte_min_frei : 0;
    return p >= g.platte_rot || free < minFree ? 'err' : p >= g.platte_gelb ? 'warn' : '';
  }
  // Zeitpunkt mit Datum, wenn er nicht von heute ist, als <time>.
  function atTime(t) {
    const d = new Date(t * 1000);
    if (isNaN(d)) return '';
    // Dieselbe Schreibweise wie überall (when: „heute, 10:59“; Datenprüfung N2).
    return el('time', { datetime: d.toISOString() }, when(d.toISOString()));
  }
  function kpis(m) {
    const r = mon.rep, h = ov.hist;
    const out = [];
    ov.kpiWarn = [];
    if (core.svc() && !r && !mon.error) {
      for (let i = 0; i < 5; i++) out.push(el('div', { class: 'ls-kpi sk', 'aria-hidden': 'true' }, skel('skel-t'), skel('skel-h'), skel('skel-t skel-w')));
      return el('div', { class: 'ls-kpis', 'aria-busy': 'true' }, out);
    }
    const g = limits();
    const note = (kind, what) => { if (kind) ov.kpiWarn.push({ kind, what }); };
    if (r && r.laeuft) {
      const v = r.werte || {};
      const S = k => h ? h.serien[k] || [] : [];
      const sp = (vals, label, max, minSpan) => h ? kSpark(vals, h, label, max, minSpan) : null;
      if (v['s/cpu'] != null) {
        const c = h ? uwK(h, 's/cpu') || uwStatsP(S('s/cpu'), (h.spitzen || {})['s/cpu'], null, h.zeiten, h.schritt, null, h) : null;
        const [n, u] = splitUnit(cpuText(v['s/cpu']));
        const lc = lasting('prozessor');
        const k = lc ? cpuLevel(v['s/cpu']) : '';
        note(k, 'Prozessor ' + (lc && lc.sek ? 'seit ' + plural(Math.floor(lc.sek / 60), 'Minute', 'Minuten') + ' ' : '') + 'über ' + (g ? g.prozessor_gelb : '') + ' %; HostDeck warnt, wenn das ' + (g ? plural(g.prozessor_minuten, 'Minute', 'Minuten') : '') + ' anhält');
        out.push(kpi({ l: 'Prozessor', v: n, u, kind: k, href: '#/ueberwachung/1d', wert: v['s/cpu'], id: 'cpu', tag: k ? [k, 'über ' + g.prozessor_gelb + ' %'] : null,
          s: [c ? ['Spitze 24 Std ' + cpuText(c.peak) + ' ', atTime(c.at), ' · '] : null, plural(r.info.kerne, 'Kern', 'Kerne')],
          spark: sp(S('s/cpu'), 'Prozessor der letzten 24 Stunden') }));
      }
      if (v['s/ram'] != null && r.info.ram_gesamt) {
        const p = v['s/ram'] / r.info.ram_gesamt * 100;
        const [n, u] = splitUnit(size(v['s/ram']));
        const lr = lasting('speicher');
        const k = lr ? ramLevel(v['s/ram'], r.info.ram_gesamt) : '';
        note(k, 'Arbeitsspeicher ' + (lr && lr.sek ? 'seit ' + plural(Math.floor(lr.sek / 60), 'Minute', 'Minuten') + ' ' : '') + 'knapp; HostDeck warnt, wenn das ' + (g ? plural(g.speicher_minuten, 'Minute', 'Minuten') : '') + ' anhält');
        out.push(kpi({ l: 'Arbeitsspeicher', v: n, u, kind: k, href: '#/ueberwachung/1d', wert: p, id: 'ram', tag: k ? [k, 'knapp'] : null,
          s: 'belegt von ' + size(r.info.ram_gesamt) + ' · ' + pctText(p), spark: sp(S('s/ram'), 'Arbeitsspeicher der letzten 24 Stunden') }));
      }
      const disks = (r.info.laufwerke || []).filter(d => d.gesamt > 0);
      if (disks.length) {
        const d = disks.slice().sort((a, b) => b.belegt / b.gesamt - a.belegt / a.gesamt)[0];
        const p = d.belegt / d.gesamt * 100;
        const k = diskLevel(d.belegt, d.gesamt);
        note(k, 'Platte ' + d.pfad + (k === 'err' ? ' fast voll' : ' wird knapp'));
        out.push(kpi({ l: 'Platte', v: unitSplit(pctText(p))[0], u: '% belegt', kind: k, href: '#/platz', wert: p, id: 'platte', tag: k ? [k, k === 'err' ? 'dringend' : 'knapp'] : null,
          s: size(d.gesamt - d.belegt) + ' frei · Laufwerk ' + d.pfad,
          spark: sp(S('d/' + d.pfad).map(x => x == null ? null : x / d.gesamt * 100), 'Platte ' + d.pfad + ' der letzten 24 Stunden', 100) }));
      }
      // Domains statt Netz (Strukturvorschlag 2, 16.11.): eine Zahl
      // aus der einen Quelle (domSum), ein Kästchen je Domain. Netz steht
      // weiter in der Überwachung; ohne Domains bleibt hier die Netz-Kachel.
      const zs = (dz.list || []).filter(z => z.zustand !== 'aus');
      // Vor dem ersten Laden des Zustands steht schon „Domains“, wenn Caddy
      // welche kennt; sonst spränge die Kachel kurz von Netz auf Domains.
      const known = dz.list ? [] : m.routes.filter(r => r.status !== 'extern' && r.status !== 'kein-proxy');
      if (known.length) {
        const n = known.reduce((a, r) => a + r.hosts.length, 0);
        out.push(kpi({ l: 'Domains', v: '–', u: 'wird geprüft', href: '#/domains', id: 'domains', s: plural(n, 'Domain', 'Domains') + ' in Caddy' }));
      } else if (zs.length) {
        const d = m.dom;
        const kind = d.bad.length ? (d.err ? 'err' : 'warn') : '';
        const cls = z => z.stufe === 'ok' || z.stufe === 'err' || z.stufe === 'warn' ? z.stufe : 'idle';
        const blocks = el('span', { class: 'k-hb', role: 'img', 'aria-label': d.ok + ' antworten, ' + d.bad.length + ' ' + (d.bad.length === 1 ? 'antwortet' : 'antworten') + ' nicht, ' + (zs.length - d.ok - d.bad.length) + ' pausiert oder in Prüfung' },
          zs.slice(0, 32).map(z => el('i', { class: cls(z), title: z.domain + ': ' + z.wort })));
        const rest = [d.paused ? d.paused + ' pausiert' : '', d.quiet ? d.quiet + ' ' + (d.quiet === 1 ? 'startet' : 'starten') + ' gerade' : ''].filter(Boolean).join(' · ');
        // Alle pausiert: keine Zahl „0 von 0“, sondern wie viele ruhen.
        const [kv, ku] = d.total ? [d.ok, 'von ' + d.total + ' ' + (d.total === 1 ? 'antwortet' : 'antworten')] : [d.paused || zs.length, d.paused ? 'pausiert' : 'in Prüfung'];
        out.push(kpi({ l: 'Domains', v: String(kv), u: ku, kind, href: '#/domains', id: 'domains', wert: d.ok,
          tag: kind ? [kind, plural(d.bad.length, 'Problem', 'Probleme')] : null,
          s: [rest ? rest + ' · ' : '', dz.checked ? ['geprüft ', whenT(dz.checked)] : 'wird geprüft'], spark: blocks }));
      } else if (v['s/ein'] != null) {
        const [n, u] = splitUnit(size(Math.round(v['s/ein'] + (v['s/aus'] || 0))));
        out.push(kpi({ l: 'Netz', v: n, u: u + '/s', href: '#/ueberwachung/1d', id: 'netz', s: 'rein ' + size(Math.round(v['s/ein'])) + '/s · raus ' + size(Math.round(v['s/aus'] || 0)) + '/s',
          spark: sp(uwSum([S('s/ein'), S('s/aus')], h ? h.zeiten.length : 0), 'Netz der letzten 24 Stunden') }));
      }
    } else {
      const sp = m.s.space;
      if (sp && sp.total) {
        const p = sp.used / sp.total * 100;
        out.push(kpi({ l: 'Platte', v: unitSplit(pctText(p))[0], u: '% belegt', href: '#/platz', id: 'platte', wert: p, s: size(sp.used) + ' von ' + size(sp.total) }));
      }
      if (m.dom.total) out.push(kpi({ l: 'Domains', v: String(m.dom.ok), u: 'von ' + m.dom.total + (m.dom.live ? ' antworten' : ' antworteten'), kind: m.dom.bad.length ? (m.dom.err ? 'err' : 'warn') : '', href: '#/domains',
        s: (m.dom.paused ? m.dom.paused + ' pausiert · ' : '') + (m.dom.live ? 'über Caddy' : 'Stand der Bestandsaufnahme') }));
      out.push(kpi({ l: 'Backups', v: String(m.backups.filter(b => b.valid).length), u: 'gültig', href: '#/backup', s: m.last ? 'zuletzt ' + when(m.last.started) : 'noch keins' }));
    }
    // Container: Zahlen und Gründe vom Dienst (op.rep.summe), live sobald da.
    const cs = live();
    const sm = op.rep && op.rep.summe;
    const hs = cs && sm ? healthOf(cs) : null;
    if (hs) {
      const ok = hs.filter(x => x === 'ok').length, bad = hs.filter(x => x === 'err').length, sick = hs.filter(x => x === 'warn').length, off = hs.filter(x => x === 'idle').length;
      const blocks = el('span', { class: 'k-hb', role: 'img', 'aria-label': ok + ' gesund, ' + (bad + sick) + ' mit Problem, ' + off + ' aus' }, hs.slice(0, 32).map(x => el('i', { class: x })));
      const kind = bad ? 'err' : sick ? 'warn' : '';
      const why = sm.schleife ? plural(sm.schleife, 'in Neustart-Schleife', 'in Neustart-Schleife') + (sm.unerwartet ? ', ' + sm.unerwartet + ' unerwartet gestoppt' : '')
        : sm.unerwartet ? plural(sm.unerwartet, 'unerwartet gestoppt', 'unerwartet gestoppt') : sm.unhealthy ? plural(sm.unhealthy, '„unhealthy“', '„unhealthy“') : off ? stopWhy(sm) : 'alle laufen';
      note(kind, plural(bad + sick, 'Container hat', 'Container haben') + ' ein Problem');
      out.push(kpi({ l: 'Container', v: String(ok), u: 'von ' + hs.length + ' gesund', kind, href: '#/projekte', id: 'container', wert: ok, tag: kind ? [kind, plural(bad + sick, 'Problem', 'Probleme')] : null,
        s: why + (sm.jobs ? ' · ' + plural(sm.jobs, 'Job erledigt', 'Jobs erledigt') : ''), spark: blocks }));
    } else {
      out.push(kpi({ l: 'Container', v: String(m.run), u: 'von ' + m.all + ' liefen', kind: m.run < m.all ? 'warn' : '', href: '#/projekte', id: 'container', s: 'laut Bestandsaufnahme' }));
    }
    return el('div', { class: 'ls-kpis' }, out);
  }
  // ---------- Übersicht ----------
  // Begrüßung und Bereiche, dann je nach Lage „Erste Schritte“ (frisch
  // eingerichtet), „Alles in Ordnung.“ (nichts braucht dich) oder „Heute auf
  // einen Blick“. Darunter Zahlenkarten, Projektkacheln mit der Meldung,
  // Prozessor mit Ereignissen, Zustand, Domains und was passiert ist. Die
  // Verbindungskarte (früher Lageplan) steht unter Überwachung.
  const evs = { list: null, at: 0, loading: false };
  async function loadEvents() {
    if (evs.loading || !core.svc()) return;
    evs.loading = true;
    const r = await core.call('/api/ereignisse?tage=8', null, true);
    evs.loading = false;
    evs.at = Date.now();
    if (r.ok) { evs.list = r.data.ereignisse || []; evs.since = r.data.seit ? Date.parse(r.data.seit) : 0; }
    else if (!evs.list) evs.list = [];
    if (ui.page === 'lage' && core.work() && !modalOpen() && !uw.hover) core.rerender();
  }
  // Prozessor der Woche, nur für die gestrichelte Linie „24 Std davor“.
  const ovp = { hist: null, at: 0, loading: false };
  async function loadPrevCpu() {
    if (ovp.loading || !core.svc()) return;
    ovp.loading = true;
    const r = await core.call('/api/ueberwachung/verlauf?zeitraum=7d&punkte=336&serien=s/cpu', null, true);
    ovp.loading = false;
    ovp.at = Date.now();
    if (r.ok) ovp.hist = r.data;
    if (ui.page === 'lage' && core.work() && !modalOpen() && !uw.hover) core.rerender();
  }
  function greeting() {
    const h = new Date().getHours();
    return h >= 5 && h < 11 ? 'Guten Morgen' : h >= 11 && h < 18 ? 'Guten Tag' : 'Guten Abend';
  }
  const ROOMS = [['alles', 'Alles'], ['projekte', 'Projekte'], ['domains', 'Domains'], ['system', 'System']];
  function inRoom(...ks) { return !ov.room || ov.room === 'alles' || ks.includes(ov.room); }
  // Ab dieser Breite steht die Übersicht in einer Spalte (wie .ov-g2 in app.css).
  const OV_NARROW = window.matchMedia ? window.matchMedia('(max-width: 1100px)') : null;
  if (OV_NARROW && OV_NARROW.addEventListener) OV_NARROW.addEventListener('change', () => { if (ui.page === 'lage' && !modalOpen()) core.rerender(); });
  function narrowOv() { return !!(OV_NARROW && OV_NARROW.matches); }
  function rooms() {
    return el('div', { class: 'ov-rooms', role: 'group', 'aria-label': 'Bereich zeigen' }, ROOMS.map(([k, t]) =>
      el('button', { type: 'button', class: 'ls-chip', 'aria-pressed': String((ov.room || 'alles') === k), 'data-fid': 'room-' + k, onclick: () => { ov.room = k; core.rerender(); } }, t)));
  }
  // Kachel mit Kopf: Titel links, rechts etwas Kleines.
  function tile(title, body, o) {
    o = o || {};
    const id = o.id || null;
    return el('section', { class: 'ov-tile ' + (o.cls || ''), id, 'aria-labelledby': id ? id + '-h' : null, 'aria-label': id ? null : title },
      el('header', { class: 'ov-th' }, el('h2', { id: id ? id + '-h' : null }, title), o.end ? el('div', { class: 'end' }, o.end) : null),
      o.sub ? el('p', { class: 'ov-sub' }, o.sub) : null,
      body);
  }

  // ---------- Ereignisse ----------
  const EV_KIND = { projekt: ['Projekt', 'projekte'], domain: ['Domain', 'domains'], zertifikat: ['Zertifikat', 'lock'], spitze: ['Spitze', 'puls'], backup: ['Backup', 'backup'],
    update: ['Update', 'updates'], server: ['Server', 'server'], system: ['System', 'leistung'], sicherheit: ['Sicherheit', 'sicherheit'] };
  // Stufe als Farbe und Zeichen (nie nur Farbe).
  const EV_LV = { ok: ['ok', 'check'], err: ['err', 'alert'], warn: ['warn', 'warn'], info: ['acc', null], kept: ['kept', 'eyeoff'] };
  // Marken einer Domain, die du jetzt bewusst so lässt, stehen grau statt
  // rot; das Ereignis selbst bleibt.
  function evKeep(list) {
    const k = keptKeys();
    return list.map(e => e.art === 'domain' && e.domain && (e.stufe === 'err' || e.stufe === 'warn') && k.has('seite:' + e.domain) ? Object.assign({}, e, { stufe: 'kept' }) : e);
  }
  function evTag(e) {
    const [word] = EV_KIND[e.art] || ['Hinweis'];
    const [cls, icon] = EV_LV[e.stufe] || EV_LV.info;
    return el('span', { class: 'ov-tag ' + cls }, icon ? ic(icon, 12) : null, word);
  }
  function evTime(e) { return atTime(Date.parse(e.zeit) / 1000); }
  // Kurzer Name für Band und Kurve.
  function evShort(e) {
    if (e.art === 'spitze') return 'Spitze ' + cpuText(e.wert);
    if (e.art === 'domain') return e.stufe === 'ok' ? (e.domain || 'Domain') + ' wieder da' : e.stufe === 'err' || e.stufe === 'warn' || e.stufe === 'kept' ? (e.domain || 'Domain') + ' antwortete nicht' + (e.stufe === 'kept' ? ' (so gelassen)' : '') : e.titel;
    if (e.art === 'zertifikat') return e.stufe === 'ok' ? 'Zertifikat erneuert' : 'Zertifikat läuft ab';
    if (e.art === 'backup') return /herunter/.test(e.titel) ? 'Backup geladen' : e.stufe === 'err' ? 'Backup gescheitert' : 'Backup von Hand';
    if (e.art === 'update') return 'Update';
    return e.titel.length > 26 ? e.titel.slice(0, 25) + '…' : e.titel;
  }
  // Wichtig zuerst: rot, gelb, dann der Rest.
  const EV_RANK = { err: 0, warn: 1, ok: 2, info: 3, kept: 3 };
  function evPick(list, n) {
    return list.slice().sort((a, b) => (EV_RANK[a.stufe] ?? 3) - (EV_RANK[b.stufe] ?? 3) || Date.parse(b.zeit) - Date.parse(a.zeit)).slice(0, n)
      .sort((a, b) => Date.parse(a.zeit) - Date.parse(b.zeit));
  }
  // Ausfälle als Strecke: vom Beginn der Warnung bis zur Entwarnung (oder
  // jetzt, solange sie läuft).
  function evOutages(list, from, to) {
    const out = [];
    const asc = list.slice().sort((a, b) => Date.parse(a.zeit) - Date.parse(b.zeit));
    for (let i = 0; i < asc.length; i++) {
      const e = asc[i];
      if (e.stufe !== 'err' || (e.art !== 'domain' && e.art !== 'projekt')) continue;
      const who = e.domain || e.projekt;
      const end = asc.slice(i + 1).find(x => x.art === e.art && (x.domain || x.projekt) === who && x.stufe !== 'err');
      const t0 = Math.max(from, Date.parse(e.zeit)), t1 = Math.min(to, end ? Date.parse(end.zeit) : to);
      if (t1 > t0) out.push([t0, t1]);
    }
    return out;
  }
  function evRow(e) {
    return el('li', { class: 'ov-ev' }, evTag(e),
      el('div', { class: 't' }, el('b', {}, e.link ? el('a', { href: e.link }, e.titel) : e.titel), e.text ? el('span', {}, e.text) : null),
      el('span', { class: 'w' }, evTime(e)));
  }
  function ovEvents(o) {
    o = o || {};
    if (!core.svc()) return null;
    const list = evs.list;
    let body;
    if (!list) body = el('div', { class: 'ov-list', 'aria-busy': 'true' }, [0, 1, 2].map(() => el('div', { class: 'ov-ev sk' }, skel('skel-t'), skel('skel-w'))));
    else if (!list.length) body = el('div', { class: 'ov-empty' }, ic('clock', 26), el('b', {}, 'Noch nichts passiert' + (evs.since ? ' seit ' + fmtDate(new Date(evs.since).toISOString()) : '')),
      el('span', {}, 'Hier erscheinen Starts und Stopps, Störungen, Zertifikate, Spitzen und Backups von Hand, sobald es sie gibt.'));
    else {
      const n = ov.evAll ? 30 : o.n || 6;
      body = [el('ol', { class: 'ov-list' }, list.slice(0, n).map(evRow)),
        list.length > (o.n || 6) ? el('button', { type: 'button', class: 'btn btn-sm btn-g ov-more', 'data-fid': 'ev-more', 'aria-expanded': String(!!ov.evAll), onclick: () => { ov.evAll = !ov.evAll; core.rerender(); } },
          ov.evAll ? 'Weniger zeigen' : 'Mehr zeigen') : null];
    }
    return tile('Was passiert ist', body, { id: 'ov-ev', cls: o.cls, end: link('#/protokoll', 'Protokoll', 'btn btn-sm btn-g') });
  }

  // ---------- Heute auf einen Blick ----------
  function dayBand() {
    const now = Date.now();
    const d = new Date();
    const t0 = new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime(), t1 = t0 + 864e5;
    const X = t => Math.max(0, Math.min(100, (t - t0) / (t1 - t0) * 100));
    const list = evKeep((evs.list || []).filter(e => Date.parse(e.zeit) >= t0));
    const band = el('div', { class: 'ov-day', 'aria-hidden': 'true' }, el('span', { class: 'bd' }));
    const put = (cls, x, kids) => { const n = el('span', { class: cls }, kids); n.style.left = x.toFixed(2) + '%'; return n; };
    for (const [a, b] of evOutages(evs.list || [], t0, now)) {
      const s = el('span', { class: 'seg' });
      s.style.left = X(a).toFixed(2) + '%';
      s.style.width = Math.max(0.6, X(b) - X(a)).toFixed(2) + '%';
      band.append(s);
    }
    for (const h of [0, 6, 12, 18]) band.append(put('tx' + (h ? '' : ' first'), h / 24 * 100, String(h).padStart(2, '0') + ':00'));
    // Was dicht beieinander liegt, wird ein Punkt mit Zahl. Beschriftung in
    // zwei Reihen; was nicht passt, bleibt Punkt mit Namen für den Zeiger.
    // Vorleser bekommen die Liste darunter.
    const groups = [];
    for (const e of evPick(list, 12)) {
      const x = X(Date.parse(e.zeit));
      const g = groups[groups.length - 1];
      if (g && x - g.x < 2.2) g.es.push(e); else groups.push({ x, es: [e] });
    }
    const last = [-99, -99];
    for (const g of groups) {
      const top = g.es.slice().sort((a, b) => (EV_RANK[a.stufe] ?? 3) - (EV_RANK[b.stufe] ?? 3))[0];
      const [cls] = EV_LV[top.stufe] || EV_LV.info;
      const [, icon] = EV_KIND[top.art] || ['', 'info'];
      const mk = put('mk ' + cls, g.x, g.es.length > 1 ? el('b', {}, String(g.es.length)) : ic(icon, 14));
      mk.title = g.es.map(evShort).join(', ');
      band.append(mk);
      const row = g.x - last[0] >= 13 ? 0 : g.x - last[1] >= 13 ? 1 : -1;
      if (row >= 0) {
        last[row] = g.x;
        band.append(put('lb' + (row ? ' l2' : '') + (g.x > 88 ? ' r' : g.x < 6 ? ' l' : ''), g.x, evShort(top) + (g.es.length > 1 ? ' und ' + (g.es.length - 1) + ' weitere' : '')));
      }
    }
    band.append(put('now', X(now)));
    // Ohne Ereignis sagt es der sichtbare Satz darunter, für alle.
    const sr = list.length ? el('ul', { class: 'sr' }, list.slice().reverse().map(e => el('li', {}, (e.zeit ? new Date(e.zeit).toLocaleTimeString('de-DE', { hour: '2-digit', minute: '2-digit' }) + ' Uhr: ' : '') + e.titel))) : null;
    return tile('Heute auf einen Blick', [band, sr, !list.length && evs.list ? el('p', { class: 'ov-sub ov-quiet' }, 'Heute ist bisher nichts passiert.') : null],
      { id: 'ov-day', end: el('span', { class: 'ov-sub' }, '00:00 bis jetzt') });
  }

  // ---------- Zahlenkarten ----------
  function stTile(o) {
    return el(o.href ? 'a' : 'div', { class: 'ov-st' + (o.kind ? ' ' + o.kind : '') + (o.href ? '' : ' flat') + (o.calm ? ' calm' : ''), href: o.href || null, 'data-kpi': o.id, 'data-wert': o.wert != null ? String(o.wert) : null },
      el('span', { class: 'k' }, el('span', { class: 'ov-ic ' + o.c }, ic(o.icon, 18)), el('span', {}, o.l),
        o.tag ? el('span', { class: 'ov-flag ' + o.kind }, ic(o.kind === 'err' ? 'alert' : 'warn', 13), o.tag) : null),
      el('b', { class: 'v' }, o.v, o.u ? el('small', {}, o.u) : null),
      el('span', { class: 'dl' }, o.chip ? el('b', {}, o.chip) : null, el('span', {}, o.sub || '')),
      o.spark ? el('span', { class: 'ar' }, o.spark) : el('span', { class: 'ar none' }));
  }
  // Kurz nach dem Start fehlt ein Wert noch (Prozessor und Netz brauchen
  // zwei Messungen): Platzhalter, damit das Raster nicht springt.
  function measuring(id, l, icon, c) {
    return stTile({ id, l, icon, c, v: '–', sub: 'wird gemessen', href: '#/ueberwachung/1d' });
  }
  // Mittel seit Mitternacht aus dem Verlauf; null, solange es nichts gibt.
  function todayMean(v, h) {
    const d = new Date();
    const t0 = new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime() / 1000;
    let s = 0, n = 0;
    h.zeiten.forEach((t, i) => { if (t >= t0 && v[i] != null) { s += v[i]; n++; } });
    return n ? s / n : null;
  }
  function ovStats(m) {
    const r = mon.rep, h = ov.hist;
    if (!core.svc()) return kpis(m);
    if (!r && !mon.error) return el('div', { class: 'ov-stats', 'aria-busy': 'true' }, [0, 1, 2, 3].map(() => el('div', { class: 'ov-st sk', 'aria-hidden': 'true' }, skel('skel-t'), skel('skel-h'), skel('skel-t skel-w'))));
    if (!r || !r.laeuft) return kpis(m);
    ov.kpiWarn = [];
    const g = limits();
    const v = r.werte || {};
    const S = k => h ? h.serien[k] || [] : [];
    const sp = (vals, label, max, minSpan) => h ? kSpark(vals, h, label, max, minSpan) : null;
    const out = [];
    if (v['s/cpu'] != null) {
      const lc = lasting('prozessor');
      const k = lc ? cpuLevel(v['s/cpu']) : '';
      if (k) ov.kpiWarn.push({ kind: k, what: 'Prozessor über ' + g.prozessor_gelb + ' %' });
      const [n, u] = splitUnit(cpuText(v['s/cpu']));
      const avg = h ? todayMean(S('s/cpu'), h) : null;
      out.push(stTile({ id: 'cpu', l: 'Prozessor', icon: 'leistung', c: 'c-v', v: n, u, kind: k, tag: k ? 'über ' + g.prozessor_gelb + ' %' : null, wert: v['s/cpu'], href: '#/ueberwachung/1d',
        chip: avg != null ? 'Ø heute ' + cpuText(avg) : null, sub: 'alle Kerne = 100 %', spark: sp(S('s/cpu'), 'Prozessor der letzten 24 Stunden') }));
    } else out.push(measuring('cpu', 'Prozessor', 'leistung', 'c-v'));
    if (v['s/ram'] != null && r.info.ram_gesamt) {
      const p = v['s/ram'] / r.info.ram_gesamt * 100;
      const lr = lasting('speicher');
      const k = lr ? ramLevel(v['s/ram'], r.info.ram_gesamt) : '';
      if (k) ov.kpiWarn.push({ kind: k, what: 'Arbeitsspeicher knapp' });
      const [n, u] = splitUnit(size(v['s/ram']));
      out.push(stTile({ id: 'ram', l: 'Arbeitsspeicher', icon: 'monitor', c: 'c-b', v: n, u, kind: k, tag: k ? 'knapp' : null, wert: p, href: '#/ueberwachung/1d',
        chip: pctText(p), sub: 'von ' + size(r.info.ram_gesamt), spark: sp(S('s/ram').map(x => x == null ? null : x / r.info.ram_gesamt * 100), 'Arbeitsspeicher der letzten 24 Stunden', 'own') }));
    }
    const disks = (r.info.laufwerke || []).filter(d => d.gesamt > 0);
    if (disks.length) {
      const d = disks.slice().sort((a, b) => b.belegt / b.gesamt - a.belegt / a.gesamt)[0];
      const p = d.belegt / d.gesamt * 100;
      const k = diskLevel(d.belegt, d.gesamt);
      if (k) ov.kpiWarn.push({ kind: k, what: 'Platte ' + d.pfad + (k === 'err' ? ' fast voll' : ' wird knapp') });
      const [n, u] = splitUnit(size(d.gesamt - d.belegt));
      out.push(stTile({ id: 'platte', l: 'Platte frei', icon: 'platz', c: 'c-t', v: n, u, kind: k, tag: k ? (k === 'err' ? 'fast voll' : 'knapp') : null, wert: p, href: '#/platz',
        chip: pctText(p) + ' belegt', sub: disks.length > 1 ? 'Laufwerk ' + d.pfad + ', am vollsten' : 'Laufwerk ' + d.pfad,
        // Die Kurve zeigt, was frei ist (wie die Zahl), mit mindestens 5 Prozentpunkten Spanne.
        spark: sp(S('d/' + d.pfad).map(x => x == null ? null : 100 - x / d.gesamt * 100), 'Freier Platz auf ' + d.pfad + ' in 24 Stunden', 'own', 5) }));
    }
    if (v['s/ein'] != null) {
      const [n, u] = splitUnit(size(Math.round(v['s/ein'] + (v['s/aus'] || 0))));
      out.push(stTile({ id: 'netz', l: 'Netz', icon: 'globelock', c: 'c-p', v: n, u: u + '/s', href: '#/ueberwachung/1d',
        chip: 'rein ' + size(Math.round(v['s/ein'])) + '/s', sub: 'raus ' + size(Math.round(v['s/aus'] || 0)) + '/s',
        spark: sp(uwSum([S('s/ein'), S('s/aus')], h ? h.zeiten.length : 0), 'Netz der letzten 24 Stunden') }));
    } else out.push(measuring('netz', 'Netz', 'globelock', 'c-p'));
    return el('div', { class: 'ov-stats' }, out);
  }

  // ---------- Projekte als Kacheln ----------
  // Gruppen wie in der Leiste: Spielserver (erkannt), Webseiten (mit
  // Domain), Weitere.
  const PGROUP = { spiel: ['Spielserver', 'c-v', 'game'], web: ['Webseite', 'c-b', 'domains'], weitere: ['Projekt', 'c-t', 'projekte'] };
  function pGroup(x) {
    if (x.kind === 'g') return x.game ? 'spiel' : 'weitere';
    const games = gameList(mon.rep);
    const cs = liveFor(x) || [];
    if (games.some(gm => (x.kind === 'p' && gm.projekt === x.name) || gm.container === x.name || cs.some(c => c.name === gm.container))) return 'spiel';
    // Ein Projekt, in dem das Panel erkannter Spielserver läuft, gehört dazu.
    if (games.some(gm => gm.panel && cs.some(c => c.name === gm.panel))) return 'spiel';
    return x.domains.length ? 'web' : 'weitere';
  }
  // Wort für die Art eines Eintrags.
  function pKindWord(x) { return x.kind === 'g' ? (x.game ? 'Spielserver' : 'Verwalteter Server') : PGROUP[x.grp][0]; }
  // Die Einträge unter „Projekte“, eine Liste für alle Seiten (Übersicht,
  // Projekte, Leiste, Zustand, Überwachung; Datenprüfung H1): Compose-
  // Projekte, einzelne Container und jeder Server, den ein Panel startet,
  // einzeln mit seinem eigenen Zustand und Verbrauch.
  function pRows(m) {
    let all = rowsAll(m);
    const lv = live();
    for (const x of all) { x.live = lv ? liveSum(liveFor(x)) : null; x.grp = pGroup(x); }
    // Container ohne Projekt, die HostDeck nicht steuert und die nichts tun
    // (etwa erledigte Jobs), zeigen die Kacheln nicht.
    if (lv) all = all.filter(x => x.kind === 'p' || (x.live && x.live.total));
    all = all.concat(gRows(all));
    for (const x of all) if (x.kind === 'g') x.grp = pGroup(x);
    return all;
  }
  // Ein Server, den ein Panel startet (verwaltet_von), steht wie ein
  // Container da: Name, Zustand von Docker, Verbrauch. Gesteuert wird im
  // Panel, nicht hier. Dazu erkannte Spielserver eines Panels, deren
  // Container das Panel beim Stoppen gelöscht hat.
  function gRows(rows) {
    const out = [];
    const games = gameList(mon.rep);
    const lv = live() || [];
    const labels = new Map();
    for (const p of model().projects) for (const sv of p.managed || []) if (sv.label) labels.set(sv.name, sv.label);
    const add = (name, c, gm, panel) => {
      if (rows.some(x => x.kind === 'c' && x.name === name)) return;
      const nm = gm ? gName(gm) : labels.get(name) || shortName(name);
      out.push({ kind: 'g', game: gm || null, c: c || null, cname: name, panel, panelProject: panelName(panel), name: nm, key: 'g:' + name,
        ok: !!(c && c.laeuft), total: 0, running: c && c.laeuft ? 1 : 0, domains: [], size: -2,
        text: (nm + ' ' + name + ' ' + (gm ? gm.spiel : '') + ' ' + (panel || '')).toLowerCase(), live: live() ? gLive(c, gm) : null });
    };
    for (const c of lv) if (c.verwaltet_von) add(c.name, c, games.find(gm => gm.container === c.name), c.verwaltet_von);
    for (const gm of games) if (gm.panel && !gm.container && !out.some(x => x.cname === gm.id)) add(gm.id, null, gm, gm.panel);
    return out;
  }
  // Zustand eines verwalteten Servers: Wort und Stufe von Docker wie bei
  // jedem Container (betrieb), dazu Prozessor und Speicher jetzt.
  function gLive(c, gm) {
    if (!c) return { run: 0, total: 0, gesamt: 0, stufe: 'idle', wort: 'aus', restarting: false, unexpected: 0, cpu: null, mem: 0, managed: true };
    const loop = looping(c);
    return { run: c.laeuft && !loop ? 1 : 0, total: 0, gesamt: 1, stufe: c.stufe || 'idle', wort: c.wort || (c.laeuft ? 'läuft' : 'aus'), text: c.grund || '', restarting: loop, unexpected: 0,
      cpu: c.laeuft && c.cpu != null ? c.cpu : null, mem: c.laeuft ? c.speicher || 0 : 0, managed: true };
  }
  // Name des Panels, das einen Container startet: sein Compose-Projekt,
  // sonst der Container selbst.
  function panelName(by) {
    const pc = (live() || []).find(y => y.name === by);
    return (pc && pc.projekt) || by;
  }
  // Ein verwalteter Server führt zur Projektseite seines Panels.
  function gHref(x) {
    const p = model().projects.find(y => y.name === x.panelProject);
    return p ? '#/projekte/' + encodeURIComponent(p.key) : '#/ueberwachung/1d';
  }
  // Stand eines Eintrags in einem Wort für die Zählung: läuft, aus, Job
  // erledigt, angelegt. Eine Regel für Übersicht, Ruhe, Projekte und
  // Zustand (Datenprüfung H1, M3): Jobs und angelegte Container zählen
  // nicht als „aus“.
  function pState(x) {
    const s = x.live;
    // Ohne Dienst: was nicht lief, ist aus, auch ein Projekt ohne Container,
    // damit die Summe „Alle N“ ergibt.
    if (!s) return x.ok ? 'run' : 'off';
    if (s.run > 0) return 'run';
    if (x.kind !== 'g' && !s.total) return s.created && !s.jobs ? 'created' : 'job';
    return 'off';
  }
  function pCount(rows) {
    const n = { run: 0, off: 0, job: 0, created: 0 };
    for (const x of rows) n[pState(x)]++;
    return n;
  }
  // „11 laufen · 2 aus · 1 Job erledigt“, nur was es gibt.
  function pCountText(n) {
    return [n.run + ' ' + (n.run === 1 ? 'läuft' : 'laufen'), n.off ? n.off + ' aus' : '', n.job ? plural(n.job, 'Job erledigt', 'Jobs erledigt') : '', n.created ? n.created + ' angelegt' : ''].filter(Boolean).join(' · ');
  }
  function pOn(x) { return x.live ? x.live.run > 0 : !!x.ok; }
  function pTarget(x) { return (x.kind === 'p' ? 'projekt:' : 'container:') + x.name; }
  function pBad(x) { return !!(x.live && (x.live.stufe === 'err' || x.live.stufe === 'warn')); }
  function pSwitch(x, label) {
    const s = x.live;
    if (!s || !s.total || x.kind === 'g') return null;
    const on = pOn(x);
    return el('button', { type: 'button', class: 'ov-sw' + (on ? ' on' : ''), role: 'switch', 'aria-checked': String(on), 'aria-label': label || 'Eingeschaltet: ' + x.name,
      title: (on ? 'Stoppen' : 'Starten') + ' … (mit Vorschau)', disabled: !!op.busy, 'data-fid': 'sw-' + x.key,
      onclick: e => { e.stopPropagation(); opRun(pTarget(x), on ? 'stoppen' : 'starten'); } });
  }
  function pTile(x) {
    const s = x.live, h = ov.hist;
    const [, gc, gi] = PGROUP[x.grp];
    const gw = pKindWord(x);
    const on = pOn(x);
    const bad = pBad(x);
    const word = s ? s.wort : x.ok ? 'lief' : 'aus';
    // Messreihe: je Projekt, sonst je Container (ein Spielserver ohne
    // Container hat keine).
    const ck = x.kind === 'g' ? (x.c ? x.cname : '') : x.name;
    const sk = ck ? (x.kind === 'p' ? 'p/' : 'c/') + ck + '/cpu' : '';
    const ser = h && sk ? h.serien[sk] : null;
    const mv = sk && mon.rep && mon.rep.werte ? mon.rep.werte[sk] : null;
    const cpu = on ? (s && s.cpu != null ? cpuText(s.cpu) : mv != null ? cpuText(mv) : '') : '';
    return el('div', { class: 'ov-pt' + (on ? ' on' : ' off') + (bad ? ' bad ' + s.stufe : ''), 'data-projekt': x.kind === 'p' ? x.name : null },
      el('div', { class: 'top' }, el('span', { class: 'ov-ic ' + gc }, ic(gi, 18)), pSwitch(x)),
      el('div', { class: 'nm' }, el('a', { class: 'ov-pt-l', href: x.kind === 'g' ? gHref(x) : '#/lage/' + encodeURIComponent(x.key), 'data-fid': 'pt-' + x.key, title: x.kind === 'g' ? gFull(x.game) || x.cname : null }, x.name),
        el('small', {}, gw + ' · ', bad ? ic(s.stufe === 'err' ? 'alert' : 'warn', 13) : null, word, cpu ? ' · ' + cpu : '')),
      ser && ser.filter(v => v != null).length > 1 ? el('span', { class: 'spk' }, kSpark(ser, h, 'Prozessor von ' + x.name + ' in 24 Stunden')) : el('span', { class: 'spk none' }));
  }
  function ovProjects(m, o) {
    o = o || {};
    const rows = pRows(m);
    const wait = core.svc() && !live() && !op.error;
    if (wait) return tile('Projekte', el('div', { class: 'ov-pg', 'aria-busy': 'true' }, [0, 1, 2].map(() => el('div', { class: 'ov-pt sk' }, skel('skel-h'), skel('skel-t')))), { id: 'ov-pr', cls: o.cls });
    const val = x => (x.live && x.live.cpu) || 0;
    rows.sort((a, b) => (pBad(b) - pBad(a)) || (pOn(b) - pOn(a)) || val(b) - val(a) || a.name.localeCompare(b.name, 'de'));
    const max = o.max || 6;
    const shown = rows.slice(0, max);
    const n = pCount(rows);
    const [first, ...rest] = pCountText(n).split(' · ');
    // Die Kacheln stehen nach Prozessor jetzt (Datenprüfung, Option A); der
    // Link nennt dieselbe Zahl, die die Projektliste zeigt.
    return tile('Projekte', [
      el('p', { class: 'ov-sub' }, rows.length ? (ov.hist ? 'Sortiert nach Prozessor jetzt. Linie: Prozessor der letzten 24 Stunden. Der Schalter zeigt erst eine Vorschau.' : 'Sortiert nach Prozessor jetzt. Der Schalter zeigt erst eine Vorschau.') : 'Noch keine Projekte gefunden.'),
      el('div', { class: 'ov-pg' }, shown.map(pTile)),
      rows.length > max ? el('p', { class: 'ov-more' }, link('#/projekte', 'Alle ' + rows.length + ' Projekte', 'btn btn-sm btn-g', 'right')) : null],
    { id: 'ov-pr', cls: o.cls, end: rows.length ? el('span', { class: 'ov-count' }, el('b', {}, first.split(' ')[0]), ' ' + first.split(' ').slice(1).join(' ') + (rest.length ? ' · ' + rest.join(' · ') : '')) : null });
  }

  // ---------- Meldung und Backup ----------
  const LV_WORD = { err: 'Dringend', warn: 'Achtung', acc: 'Zu tun', ok: 'Erledigt' };
  function ovAlert(items) {
    if (!items.length) return null;
    const x = items[0];
    const rest = items.slice(1);
    const a = x.a && x.a.cloneNode ? x.a.cloneNode(true) : null;
    if (a) { a.className = 'btn btn-p'; a.setAttribute('data-fid', 'al-go'); }
    return el('section', { class: 'ov-alert ' + x.k, 'aria-labelledby': 'ov-al-h' },
      el('p', { class: 'ov-eyb' }, el('span', { class: 'ov-ic2' }, ic(x.icon, 18)), el('span', {}, LV_WORD[x.k] || 'Hinweis'), rest.length ? el('span', { class: 'n' }, '1 von ' + items.length) : null),
      el('h2', { id: 'ov-al-h' }, x.t),
      el('p', {}, x.d),
      a || x.so ? el('div', { class: 'fx' }, a, x.so ? keepBtn(x.so, x.t, 'btn btn-g') : null) : null,
      rest.length ? el('details', { class: 'ov-rest', open: ov.all ? true : null, ontoggle: e => { ov.all = e.target.open; } },
        el('summary', { 'data-fid': 'al-more' }, plural(rest.length, 'weiterer Punkt', 'weitere Punkte')), attList(rest)) : null,
      keptLine());
  }
  function ovBackup(m) {
    const sv = core.svc();
    if (!sv || sv.kopie || sv.abgeloest) return null;
    if (!bk.rep && !bk.loading && !bk.at) setTimeout(loadBackup, 0);
    const st = bk.rep && bk.rep.ordner;
    if (st && st.laeufe > 0) {
      return tile('Backup von Hand', [el('p', { class: 'ov-sub' }, [st.letzter ? when(st.letzter) : '', size(st.groesse), m.alltag ? 'liegt noch auf dem Server' : 'für den Umzug'].filter(Boolean).join(' · ')),
        bk.dlErr ? el('p', { class: 'gt-err', role: 'alert' }, ic('warn', 16), el('span', {}, bk.dlErr)) : null,
        el('div', { class: 'fx' }, el('button', { type: 'button', class: 'btn', 'data-fid': 'ov-dl', disabled: !!sv.lauf, onclick: download }, ic('download', 17), 'Herunterladen'), link('#/backup', 'Ansehen', 'btn btn-g'))],
      { id: 'ov-bk', cls: 'ov-bk' });
    }
    if (m.alltag) return null;
    return tile('Backup von Hand', [el('p', { class: 'ov-sub' }, m.lastValid ? 'Zuletzt gültig: ' + when(m.lastValid.started) : 'Für den Umzug brauchst du ein gültiges Backup.'),
      el('div', { class: 'fx' }, link('#/backup', 'Zum Backup', 'btn', 'backup'))], { id: 'ov-bk', cls: 'ov-bk' });
  }

  // ---------- Prozessor mit Ereignissen ----------
  function ovCpu() {
    const r = mon.rep, h = ov.hist;
    if (!core.svc() || !r || !r.laeuft || !h) return null;
    const v = h.serien['s/cpu'] || [];
    const idx = v.map((x, i) => x == null ? -1 : i).filter(i => i >= 0);
    if (idx.length < 2) return null;
    const now = Date.now() / 1000, t0 = now - 86400;
    const W = 640, H = 200, L = 0, T = 8, B = 22;
    // Spitzen je Abschnitt (kurze Spitzen verschwinden sonst im Mittel).
    const pk = (h.spitzen || {})['s/cpu'] || v;
    let peak = 0, peakAt = 0, sum = 0;
    for (const i of idx) { sum += v[i]; const p = pk[i] != null ? pk[i] : v[i]; if (p > peak) { peak = p; peakAt = h.zeiten[i]; } }
    // Die 24 Stunden davor, gestrichelt, aus dem Wochenverlauf.
    const pv = [];
    const ph = ovp.hist;
    if (ph && ph.serien['s/cpu']) ph.zeiten.forEach((t, i) => { const x = ph.serien['s/cpu'][i]; if (x != null && t >= t0 - 86400 && t <= t0) pv.push([t + 86400, x]); });
    const top = Math.max(peak, ...pv.map(p => p[1]));
    const ymax = top > 50 ? 100 : niceMax(Math.max(top, 5), 'pct');
    const X = t => L + (t - t0) / 86400 * (W - L), Y = x => T + (1 - Math.min(x, ymax) / ymax) * (H - T - B);
    const svg = svgEl('svg', { class: 'ov-chart', viewBox: '0 0 ' + W + ' ' + H, preserveAspectRatio: 'none', 'aria-hidden': 'true' });
    for (const f of [0, .25, .5, .75, 1]) svg.append(svgEl('line', { class: 'gl', x1: 0, x2: W, y1: Y(ymax * f), y2: Y(ymax * f), 'vector-effect': 'non-scaling-stroke' }));
    if (pv.length > 1) svg.append(svgEl('path', { class: 'prev', 'vector-effect': 'non-scaling-stroke', d: 'M' + pv.map(([t, x]) => X(t).toFixed(1) + ' ' + Y(x).toFixed(1)).join('L') }));
    const d = uwPath(v, h.zeiten, h.schritt, X, Y, h);
    // Fläche bis zur Spitze, Linie im Mittel.
    for (const part of uwPath(pk, h.zeiten, h.schritt, X, Y, h).split(' M').filter(Boolean)) {
      const pts = part.replace(/^M/, '').split('L').map(q => q.replace('h0.01', '').trim());
      if (pts.length < 2) continue;
      svg.append(svgEl('path', { class: 'area', d: 'M' + pts[0].split(' ')[0] + ' ' + (H - B) + 'L' + pts.join('L') + 'L' + pts[pts.length - 1].split(' ')[0] + ' ' + (H - B) + 'Z' }));
    }
    svg.append(svgEl('path', { class: 'line', 'vector-effect': 'non-scaling-stroke', d }));
    // Achsen als HTML, damit die Schrift nicht mitgestreckt wird.
    const ylab = el('div', { class: 'ov-y', 'aria-hidden': 'true' }, [1, .5, 0].map(f => { const s = el('span', {}, pctText(ymax * f, 0)); s.style.top = (Y(ymax * f) / H * 100).toFixed(1) + '%'; return s; }));
    const hm = t => new Date(t * 1000).toLocaleTimeString('de-DE', { hour: '2-digit', minute: '2-digit' });
    const xlab = el('div', { class: 'ov-x', 'aria-hidden': 'true' }, [0, 6, 12, 18, 24].map(k => { const s = el('span', {}, k === 24 ? 'jetzt' : hm(t0 + k * 3600)); s.style.left = (k / 24 * 100) + '%'; return s; }));
    // Ereignisse der letzten 24 Stunden als Marken über der Kurve.
    const recent = evKeep((evs.list || []).filter(e => Date.parse(e.zeit) / 1000 >= t0 && e.stufe !== 'info'));
    const marks = el('div', { class: 'ov-ems', 'aria-hidden': 'true' });
    const lastRow = [-99, -99];
    for (const e of evPick(recent, 5)) {
      const p = (Date.parse(e.zeit) / 1000 - t0) / 86400 * 100;
      const row = p - lastRow[0] >= 18 ? 0 : p - lastRow[1] >= 18 ? 1 : -1;
      if (row < 0) continue;
      lastRow[row] = p;
      const [cls] = EV_LV[e.stufe] || EV_LV.info;
      const [, icon] = EV_KIND[e.art] || ['', 'info'];
      const mk = el('span', { class: 'em ' + cls + (row ? ' lo' : '') + (p > 70 ? ' rt' : '') }, el('i', {}, ic(icon, 13)), el('b', {}, evShort(e)));
      mk.style.left = p.toFixed(2) + '%';
      marks.append(mk);
    }
    const mean = sum / idx.length;
    const prevMean = pv.length ? pv.reduce((a, p) => a + p[1], 0) / pv.length : null;
    const label = 'Prozessor der letzten 24 Stunden, Anteil am ganzen Server: Mittel ' + cpuText(mean) + ', Spitze ' + cpuText(peak) + ' um ' + hm(peakAt) + ' Uhr.' +
      (prevMean != null ? ' Die 24 Stunden davor: Mittel ' + cpuText(prevMean) + '.' : '') +
      (recent.length ? ' Ereignisse: ' + evPick(recent, 5).map(e => hm(Date.parse(e.zeit) / 1000) + ' ' + e.titel).join('; ') + '.' : '');
    return tile('Prozessor', el('div', { class: 'ov-cw', role: 'img', 'aria-label': label }, marks, el('div', { class: 'ov-plot' }, ylab, svg, xlab)),
      { id: 'ov-cpu', cls: 'ov-cpu', sub: 'alle Kerne = 100 % · Linie Mittel ' + cpuText(mean) + ', Fläche bis zur Spitze ' + cpuText(peak),
        end: [el('span', { class: 'ov-lg' }, el('span', {}, el('i', {}), 'letzte 24 Std'), pv.length > 1 ? el('span', {}, el('i', { class: 'y' }), '24 Std davor') : null), link('#/ueberwachung/1d', 'Überwachung', 'btn btn-sm btn-g')] });
  }

  // ---------- Zustand: die eigenen Prüfungen von hostdeck ----------
  const ZW = { ok: ['ok', 'check', 'In Ordnung'], warn: ['warn', 'warn', 'Achtung'], err: ['err', 'alert', 'Gestört'], idle: ['idle', 'clock', 'Wird geprüft'] };
  function zPill(k) { const [cls, icon, word] = ZW[k] || ZW.idle; return el('span', { class: 'ov-stb ' + cls }, ic(icon, 13), word); }
  // Eine Domain mit dem Wort der Domains-Seite (antwortet, zeigt ins Leere,
  // pausiert, bewusst so gelassen …), Zeichen wie die Zustand-Pillen.
  function domPill(z) {
    const k = z.stufe === 'ok' || z.stufe === 'err' || z.stufe === 'warn' ? z.stufe : 'idle';
    const icon = k !== 'idle' ? ZW[k][1] : z.kept ? 'eyeoff' : z.zustand === 'wird-geprueft' || z.zustand === 'startet' ? 'clock' : 'pause';
    return el('span', { class: 'ov-stb ' + k }, ic(icon, 13), z.wort || ZW[k][2]);
  }
  function healthChecks(m) {
    const out = [];
    const r = mon.rep;
    const add = (label, ok, total, level, href) => { if (total > 0) out.push({ label, ok, total, level: level || (ok === total ? 'ok' : 'warn'), href }); };
    // Projekte: dieselben Einträge wie unter Projekte, jeder, der laufen
    // soll; von dir oder vom Panel gestoppt, Jobs und angelegte nicht.
    const gs = op.rep ? pRows(m).filter(x => x.live && x.live.stufe !== 'idle') : [];
    if (gs.length) {
      const bad = gs.filter(x => x.live.stufe !== 'ok');
      add('Projekte laufen', gs.length - bad.length, gs.length, bad.some(x => x.live.stufe === 'err') ? 'err' : bad.length ? 'warn' : 'ok', '#/projekte');
    }
    // Domains: alle geprüften, die nicht pausiert sind.
    applyKept();
    const zs = (dz.list || []).filter(z => z.stufe === 'ok' || z.stufe === 'warn' || z.stufe === 'err');
    if (zs.length) {
      const bad = zs.filter(z => z.stufe !== 'ok');
      add('Domains erreichbar', zs.length - bad.length, zs.length, bad.some(z => z.stufe === 'err') ? 'err' : bad.length ? 'warn' : 'ok', '#/domains');
    }
    const w = r && r.warnungen;
    if (w) {
      // So gelassene Zertifikate zählen nicht mit.
      const kk = keptKeys();
      const certs = (w.seiten || []).filter(s => s.zertifikat_bis && !kk.has('zertifikat:' + s.domain));
      const cBad = certs.filter(s => certLevel(s.zertifikat_bis));
      add('Zertifikate gültig', certs.length - cBad.length, certs.length, cBad.some(s => certLevel(s.zertifikat_bis) === 'err') ? 'err' : cBad.length ? 'warn' : 'ok', '#/domains');
    }
    if (r && r.info) {
      const disks = (r.info.laufwerke || []).filter(d => d.gesamt > 0);
      const lv = disks.map(d => diskLevel(d.belegt, d.gesamt));
      add(disks.length > 1 ? 'Platten mit Platz' : 'Platte mit Platz', lv.filter(x => !x).length, disks.length, lv.includes('err') ? 'err' : lv.includes('warn') ? 'warn' : 'ok', '#/platz');
      const v = r.werte || {};
      if (v['s/ram'] != null && r.info.ram_gesamt) {
        const k = lasting('speicher') ? ramLevel(v['s/ram'], r.info.ram_gesamt) : '';
        add('Arbeitsspeicher reicht', k ? 0 : 1, 1, k || 'ok', '#/ueberwachung/1d');
      }
    }
    return out;
  }
  function ovHealth(m, o) {
    o = o || {};
    if (!core.svc()) return null;
    const cs = healthChecks(m);
    if (!cs.length) return tile('Zustand', el('div', { class: 'ov-list', 'aria-busy': 'true' }, skel('skel-t'), skel('skel-t')), { id: 'ov-z', cls: 'ov-z' });
    const ok = cs.reduce((a, c) => a + c.ok, 0), total = cs.reduce((a, c) => a + c.total, 0);
    const R = 46, Cf = 2 * Math.PI * R;
    const ring = svgEl('svg', { viewBox: '0 0 120 120', class: 'ov-ring', 'aria-hidden': 'true' });
    ring.append(svgEl('circle', { cx: 60, cy: 60, r: R, class: 'trk' }));
    const arc = svgEl('circle', { cx: 60, cy: 60, r: R, class: 'val' + (cs.some(c => c.level === 'err') ? ' err' : ok < total ? ' part' : ''), transform: 'rotate(-90 60 60)', 'stroke-dasharray': (Cf * ok / total).toFixed(1) + ' ' + Cf.toFixed(1) });
    ring.append(arc);
    const rows = el('ul', { class: 'ov-kl' }, cs.map(c => el('li', {}, zPill(c.level), el('a', { href: c.href }, c.label), el('b', {}, c.total === 1 && c.label === 'Arbeitsspeicher reicht' ? (c.ok ? 'ja' : 'knapp') : c.ok + ' / ' + c.total))));
    return tile('Zustand', [o.ring === false ? null : el('div', { class: 'ov-rw' }, ring, el('div', { class: 'in' }, el('b', {}, String(ok)), el('span', {}, 'von ' + total))), rows],
      { id: 'ov-z', cls: 'ov-z' + (o.ring === false ? ' flat' : ''), sub: ok === total ? 'Alle ' + total + ' Prüfungen in Ordnung' : ok + ' von ' + total + ' Prüfungen in Ordnung' });
  }

  // ---------- Domains ----------
  function ovDomains(m) {
    if (!core.svc()) return null;
    const zs = (dz.list || []).filter(z => z.zustand !== 'aus');
    if (!dz.list) return tile('Domains', el('div', { class: 'ov-list', 'aria-busy': 'true' }, skel('skel-t'), skel('skel-t')), { id: 'ov-dm' });
    if (!zs.length) return null;
    const sites = new Map(((mon.rep && mon.rep.warnungen && mon.rep.warnungen.seiten) || []).map(s => [s.domain, s]));
    const rank = { err: 0, warn: 1, idle: 2, ok: 3 };
    const list = zs.slice().sort((a, b) => (rank[a.stufe] ?? 2) - (rank[b.stufe] ?? 2) || a.domain.localeCompare(b.domain, 'de'));
    const d = m.dom;
    const pill = domPill;
    return tile('Domains', [el('ul', { class: 'ov-list' }, list.slice(0, 6).map(z => {
      const s = sites.get(z.domain);
      return el('li', { class: 'ov-it' }, el('span', { class: 'ov-ic c-t' }, ic(z.stufe === 'err' ? 'unlink' : 'domains', 16)),
        el('div', { class: 't' }, el('b', {}, dotBreak(z.domain)), el('span', {}, z.stufe === 'ok' && s && s.dauer_ms ? s.dauer_ms + ' ms' : z.text || z.wort)), pill(z));
    })), list.length > 6 ? el('p', { class: 'ov-more' }, link('#/domains', 'Alle ' + list.length + ' Domains', 'btn btn-sm btn-g', 'right')) : null],
    { id: 'ov-dm', end: el('span', { class: 'ov-sub' }, (d.total ? d.ok + ' von ' + d.total + ' erreichbar' : d.paused ? d.paused + ' pausiert' : d.kept ? '' : 'wird geprüft') + (d.kept ? (d.total || d.paused ? ' · ' : '') + d.kept + ' bewusst so gelassen' : '')) });
  }

  // ---------- Deine Woche (montags) ----------
  const WEEK_KEY = 'hostdeck-woche';
  function weekStart(d) { const x = new Date(d.getFullYear(), d.getMonth(), d.getDate()); x.setDate(x.getDate() - ((x.getDay() + 6) % 7)); return x; }
  function ovWeek() {
    if (!core.svc() || !evs.list) return null;
    const now = new Date();
    if (now.getDay() !== 1) return null;
    const mon0 = weekStart(now);
    const key = mon0.toISOString().slice(0, 10);
    let hidden = '';
    try { hidden = localStorage.getItem(WEEK_KEY) || ''; } catch (e) { hidden = ''; }
    if (hidden === key) return null;
    const from = mon0.getTime() - 7 * 864e5, to = mon0.getTime();
    // Erst, wenn HostDeck die ganze Vorwoche gesammelt hat; sonst stimmt „keine Störung“ nicht.
    if (!evs.since || evs.since > from) return null;
    const wk = evs.list.filter(e => { const t = Date.parse(e.zeit); return t >= from && t < to; });
    const outs = evOutages(wk.concat(evs.list.filter(e => Date.parse(e.zeit) >= to)), from, to);
    const mins = Math.round(outs.reduce((a, [x, y]) => a + (y - x), 0) / 60000);
    const backups = wk.filter(e => e.art === 'backup' && /erstellt/.test(e.titel)).length;
    const spikes = wk.filter(e => e.art === 'spitze').sort((a, b) => b.wert - a.wert);
    const fmt = d => d.toLocaleDateString('de-DE', { day: 'numeric', month: 'long' });
    const minText = n => n < 60 ? n + ' Min' : Math.round(n / 6) / 10 + ' Std';
    const line = outs.length ? plural(outs.length, 'Störung', 'Störungen') + ', zusammen ' + minText(mins) + '. Sonst lief alles.' : 'Ruhige Woche. Es gab keine Störung.';
    const hide = () => { try { localStorage.setItem(WEEK_KEY, key); } catch (e) { /* ohne Speicher */ } core.rerender(); const f = document.querySelector('[data-fid="room-alles"]'); if (f) f.focus({ preventScroll: true }); };
    return tile('Deine Woche', [el('p', { class: 'ov-wk' }, line),
      el('div', { class: 'ov-wn' },
        el('div', {}, el('b', {}, String(outs.length)), el('span', {}, outs.length === 1 ? 'Störung' : 'Störungen', outs.length ? ', ' + minText(mins) : '')),
        el('div', {}, el('b', {}, String(backups)), el('span', {}, backups === 1 ? 'Backup von Hand' : 'Backups von Hand')),
        el('div', {}, el('b', {}, spikes.length ? cpuText(spikes[0].wert) : '–'), el('span', {}, spikes.length ? 'höchste Spitze' : 'keine Spitze')))],
    { id: 'ov-wo', sub: fmt(new Date(from)) + ' bis ' + fmt(new Date(to - 864e5)),
      end: el('button', { type: 'button', class: 'ls-ibtn sm', 'aria-label': 'Wochenkarte bis nächsten Montag ausblenden', title: 'Ausblenden', 'data-fid': 'wk-x', onclick: hide }, ic('x', 16)) });
  }

  // ---------- Erste Schritte ----------
  // Nur auf einem neuen Server: Der Verlauf der Überwachung ist jünger als
  // eine Woche. Ein eingerichteter Server bekommt die Karte nicht, auch
  // wenn ein Schritt offen ist (etwa keine Mitteilungen aufs Handy).
  const FIRST_KEY = 'hostdeck-erste-schritte';
  const NEW_DAYS = 7;
  function isNewServer() {
    const r = mon.rep;
    if (!r) return false;
    const t = r.verlauf_seit ? Date.parse(r.verlauf_seit) : 0;
    return !t || Date.now() - t < NEW_DAYS * 864e5;
  }
  function firstSteps(m) {
    const sv = core.svc();
    if (!sv || sv.kopie || sv.abgeloest || !isNewServer()) return null;
    try { if (localStorage.getItem(FIRST_KEY) === 'aus') return null; } catch (e) { /* ohne Speicher */ }
    if (!nt.rep && !nt.loading && !nt.at) setTimeout(loadNt, 0);
    const steps = [
      ['Server verbunden', true],
      m.s.host.ohne_docker ? null : ['Projekte gefunden', m.projects.length + m.containers.length > 0, '#/projekte'],
      ['Domains geprüft', !m.routes.length || !!dz.checked, '#/domains'],
      ['Mitteilungen aufs Handy', !!(nt.rep && ((nt.rep.geraete || []).length || nt.rep.ntfy)), '#/einstellungen/mitteilungen'],
    ].filter(Boolean);
    // Ein Backup nur, solange ein Umzug ansteht (Backups nur von Hand).
    if (!m.alltag) steps.push(['Einmal ein Backup von Hand erstellen', !!m.lastValid, '#/backup']);
    const done = steps.filter(s => s[1]).length;
    if (done === steps.length || !nt.rep) return null;
    const bar = el('div', { class: 'ov-pb', role: 'progressbar', 'aria-valuemin': '0', 'aria-valuemax': String(steps.length), 'aria-valuenow': String(done), 'aria-label': 'Erste Schritte' }, el('i'));
    bar.firstChild.style.width = (done / steps.length * 100).toFixed(0) + '%';
    const off = () => { try { localStorage.setItem(FIRST_KEY, 'aus'); } catch (e) { /* ohne Speicher */ } core.rerender(); };
    return tile('Erste Schritte', [bar,
      el('ul', { class: 'ov-ck' }, steps.map(([t, ok, href]) => el('li', { class: ok ? 'dn' : '' }, el('span', { class: 'b' }, ok ? ic('check', 14) : null),
        el('span', {}, t, el('span', { class: 'sr' }, ok ? ' (erledigt)' : ' (offen)')), !ok && href ? link(href, 'Los', 'btn btn-sm') : null))),
      el('p', { class: 'ov-sub' }, 'Verschwindet, sobald alles erledigt ist. ', el('button', { type: 'button', class: 'ls-linkbtn', 'data-fid': 'first-off', onclick: off }, 'Jetzt ausblenden'))],
    { id: 'ov-first', end: el('span', { class: 'ov-sub' }, done + ' von ' + steps.length) });
  }

  // ---------- Ruhe ----------
  function ovCalm(m) {
    const rows = pRows(m);
    const n = pCount(rows);
    const d = m.dom;
    // Aus heißt hier: gestoppt, von dir oder vom Panel. Erledigte Jobs und
    // angelegte Container zählen nicht mit (Datenprüfung M3).
    const parts = [plural(n.run, 'Projekt läuft', 'Projekte laufen') + (n.off ? ', ' + n.off + ' ' + (n.off === 1 ? 'ist' : 'sind') + ' aus' : '')];
    if (d.total) parts.push(d.ok === d.total ? (d.total === 1 ? 'die Domain ist erreichbar' : 'alle ' + d.total + ' Domains sind erreichbar') : d.ok + ' von ' + d.total + ' Domains sind erreichbar');
    return el('section', { class: 'ov-calm', 'aria-labelledby': 'ov-calm-h' },
      el('span', { class: 'big' }, ic('check', 34)),
      el('div', {}, el('h2', { id: 'ov-calm-h' }, 'Alles in Ordnung.'), el('p', {}, parts.join(', ') + '. Es gibt nichts zu tun.'), keptLine()),
      el('a', { class: 'btn', href: '#/lage', 'data-fid': 'calm-ev', onclick: e => { e.preventDefault(); const t = document.getElementById('ov-ev'); if (t) { t.scrollIntoView({ behavior: 'smooth', block: 'start' }); const h = t.querySelector('h2'); if (h) { h.tabIndex = -1; h.focus({ preventScroll: true }); } } } }, 'Was heute passiert ist'));
  }

  // ---------- Seitenfenster eines Projekts ----------
  function projPanel(m, key) {
    const rows = pRows(m);
    const x = rows.find(y => y.key === key) || rowsAll(m).find(y => y.key === key);
    if (!x || x.kind === 'g') return null;
    if (!x.grp) { x.live = live() ? liveSum(liveFor(x)) : null; x.grp = pGroup(x); }
    const s = x.live, h = ov.hist;
    const cs = liveFor(x) || [];
    const on = pOn(x);
    const close = () => { location.hash = '#/lage'; };
    const [gw, gc, gi] = PGROUP[x.grp];
    const oldest = cs.filter(c => c.laeuft && c.seit).sort((a, b) => Date.parse(a.seit) - Date.parse(b.seit))[0];
    const head = el('div', { class: 'ov-dh' }, el('span', { class: 'ov-ic ' + gc }, ic(gi, 20)),
      el('div', {}, el('h2', { id: 'ls-dt', tabindex: '-1' }, x.name), el('p', {}, s ? liveStatus(s) : projStatus(x), oldest ? el('span', { class: 'ls-mut' }, ' · ' + since(oldest)) : null)),
      el('button', { type: 'button', class: 'ls-ibtn', 'aria-label': 'Schließen', 'data-fid': 'pp-x', onclick: close }, ic('x', 18)));
    const ser = k => h ? h.serien[(x.kind === 'p' ? 'p/' : 'c/') + x.name + '/' + k] : null;
    const mc = (label, val, u, k, sl) => el('div', { class: 'ov-mc' }, el('span', { class: 'k' }, label), el('b', { class: 'v' }, val, u ? el('small', {}, u) : null),
      ser(k) && ser(k).filter(y => y != null).length > 1 ? kSpark(k === 'ram' ? ser(k) : ser(k), h, sl) : null);
    const cpu = s && s.cpu != null ? splitUnit(cpuText(s.cpu)) : ['–', ''];
    const ram = s && s.cpu != null ? splitUnit(size(s.mem)) : ['–', ''];
    const sites = new Map(((mon.rep && mon.rep.warnungen && mon.rep.warnungen.seiten) || []).map(q => [q.domain, q]));
    const doms = x.domains.map(d => {
      const z = dz.by ? dz.by.get(d) : null;
      const q = sites.get(d);
      const cert = q && q.zertifikat_bis ? 'Zertifikat gültig ' + ((certWords(q.zertifikat_bis) || {}).d || '') : '';
      return el('li', { class: 'ov-it' }, el('span', { class: 'ov-ic c-t' }, ic('domains', 16)),
        el('div', { class: 't' }, el('b', {}, dotBreak(d)), el('span', {}, z && z.stufe !== 'ok' ? z.text || z.wort : cert || (z ? z.wort : ''))),
        z ? (z.stufe === 'ok' ? el('span', { class: 'w' }, q && q.dauer_ms ? q.dauer_ms + ' ms' : domPill(z)) : domPill(z)) : null);
    });
    const evl = (evs.list || []).filter(e => (e.projekt && (e.projekt === x.name || cs.some(c => c.name === e.projekt))) || (e.domain && x.domains.includes(e.domain))).slice(0, 5);
    const target = pTarget(x);
    const acts = s && s.total ? [
      s.run > 0 ? opButton(target, 'neustarten') : opButton(target, 'starten'),
      s.run > 0 ? opButton(target, 'stoppen', true) : null,
      link(pvHref(x, 'logs'), 'Logs', 'btn btn-sm btn-g', 'doc')] : [link(pvHref(x), 'Ansehen', 'btn btn-sm', 'right')];
    for (const b of acts) if (b && b.classList.contains('btn') && b.textContent.startsWith('Neu starten')) b.classList.add('btn-p');
    const box = el('div', { class: 'drawer ov-drw', role: 'dialog', 'aria-modal': 'true', 'aria-labelledby': 'ls-dt' },
      el('div', { class: 'dscroll' }, head,
        s && s.total ? el('div', { class: 'ov-onrow' }, el('div', {}, el('b', {}, 'Eingeschaltet'), el('span', {}, gw + ' · ' + plural(cs.filter(c => !c.verwaltet_von).length, 'Container', 'Container'))), pSwitch(x, 'Eingeschaltet')) : null,
        opResult(),
        el('div', { class: 'ov-two' }, mc('Prozessor', cpu[0], cpu[1], 'cpu', 'Prozessor von ' + x.name + ' in 24 Stunden'), mc('Arbeitsspeicher', ram[0], ram[1], 'ram', 'Arbeitsspeicher von ' + x.name + ' in 24 Stunden')),
        doms.length ? [el('h3', {}, 'Erreichbar unter'), el('ul', { class: 'ov-list' }, doms)] : null,
        el('h3', {}, 'Zuletzt'),
        evl.length ? el('ol', { class: 'ov-list' }, evl.map(evRow)) : el('p', { class: 'ov-sub' }, evs.list ? 'In den letzten Tagen ist hier nichts passiert.' : 'Wird geladen …'),
        el('p', { class: 'ov-sub' }, link(pvHref(x), 'Alles zu ' + x.name + ': Logs, Verlauf, Prozesse, Einstellungen', 'ls-tlink'))),
      el('div', { class: 'dfoot ov-acts' }, acts));
    box.addEventListener('keydown', e => core.trapKeys(e, box, close));
    return el('div', { class: 'ls-layer', 'data-key': 'projekt/' + key }, el('div', { class: 'scrim', onclick: close }), box);
  }

  // ---------- Seite ----------
  function pageLage(m, key) {
    const svc = core.svc();
    if (svc) {
      if (uwRO) uwRO.disconnect();
      if (!mon.loading && (!mon.at || Date.now() - mon.at > 30000)) setTimeout(loadMon, 0);
      if (!ov.loading && (!ov.at || Date.now() - ov.at > 120000)) setTimeout(loadOvHist, 0);
      if (!op.loading && (!op.rep || Date.now() - op.at > 30000)) setTimeout(loadOps, 0);
      if (!evs.loading && (!evs.at || Date.now() - evs.at > 60000)) setTimeout(loadEvents, 0);
      if (!ovp.loading && (!ovp.at || Date.now() - ovp.at > 600000)) setTimeout(loadPrevCpu, 0);
      if (!ov.timer) ovTick();
    }
    const items = attention(m);
    const hot = items.filter(x => x.k === 'err' || x.k === 'warn');
    const urgent = hot.filter(x => x.k === 'err').length;
    const host = core.hostName();
    // Zahlenkarten zuerst: Ist eine gelb oder rot, ohne dass schon ein
    // Hinweis läuft, ist der Kopf nicht ruhig.
    const stats = ovStats(m);
    const kw = (ov.kpiWarn || []).concat(svc && m.dom.live ? m.dom.bad.map(b => ({ kind: b.stufe, what: b.hosts.join(', ') + ' ' + b.wort })) : []);
    const ready = !svc || (mon.rep && op.rep && dz.list);
    const calm = !!svc && ready && !items.length && !kw.length;
    const first = firstSteps(m);
    const date = new Date().toLocaleDateString('de-DE', { weekday: 'long', day: 'numeric', month: 'long' });
    const status = hot.length
      ? [host + ' ' + (urgent ? 'meldet ein Problem. ' : 'läuft. '), el('b', { class: 'ov-need ' + (urgent ? 'err' : 'warn') }, ic(urgent ? 'alert' : 'warn', 16),
        plural(hot.length, 'Punkt braucht', 'Punkte brauchen') + ' dich' + (urgent && urgent < hot.length ? ', ' + urgent + ' davon dringend' : urgent ? ', dringend' : '') + '.'),
        // Dieselbe Zählung wie die Karte darunter („1 von N“, N10).
        items.length > hot.length ? ' Dazu ' + (items.length - hot.length) + ' zu tun.' : null]
      : kw.length ? [host + ' läuft. ', el('b', { class: 'ov-need ' + (kw.some(x => x.kind === 'err') ? 'err' : 'warn') }, ic('warn', 16), 'Gerade auffällig: ' + kw.map(x => x.what).join('; ') + '.')]
        : items.length ? [host + ' läuft ruhig. ', el('b', { class: 'ov-need acc' }, ic('info', 16), plural(items.length, 'Punkt wartet', 'Punkte warten') + ' auf dich.')]
          : [date + ' · ' + host];
    const r = mon.rep;
    const welcome = first && evs.list && !evs.list.some(e => e.art === 'update');
    const header = el('header', { class: 'ls-head ov-head' },
      el('div', { class: 'ls-head-t' }, el('h1', { tabindex: '-1' }, welcome ? 'Willkommen bei HostDeck' : greeting()), el('p', { class: 'ls-status' }, status)),
      el('div', { class: 'ls-acts' }, el('span', { class: 'ls-mut' }, svc && r && r.stand_von ? ['Stand ', whenT(r.stand_von)] : 'Bestandsaufnahme ' + fmtDate(m.s.created))));
    const alert = ovAlert(items);
    const backup = ovBackup(m);
    let side = alert || backup ? el('div', { class: 'ov-need-c' }, alert, backup) : null;
    const out = [header, svc ? rooms() : null, opResult()];
    // Schmal steht alles untereinander: Meldungen dann ganz oben.
    if (side && narrowOv() && inRoom('projekte', 'domains')) { out.push(side); side = null; }
    let calmCpu = false;
    if (first) {
      out.push(el('div', { class: 'ov-g2' }, first, inRoom('system', 'projekte', 'domains') ? ovEvents({ n: 5 }) : null));
    } else if (calm) {
      if (inRoom()) out.push(ovCalm(m));
      const wk = inRoom() ? ovWeek() : null;
      // Der Zustand steht immer da: neben der Woche, sonst neben dem
      // Prozessor wie im Alltag.
      if (inRoom('system') && wk) out.push(el('div', { class: 'ov-g2 even' }, wk, ovHealth(m, { ring: false })));
      else if (inRoom('system')) { const cpu = ovCpu(), z = ovHealth(m); out.push(cpu && z ? el('div', { class: 'ov-g2' }, cpu, z) : cpu || z); calmCpu = true; }
      else if (wk) out.push(wk);
    } else if (svc && inRoom()) {
      out.push(dayBand());
    }
    if (inRoom('system')) out.push(stats);
    if (inRoom('projekte')) out.push(side ? el('div', { class: 'ov-g2' }, ovProjects(m), side) : ovProjects(m, { max: 9 }));
    else if (side && inRoom('domains')) out.push(side);
    if (!calm && inRoom('system')) {
      const cpu = ovCpu();
      const z = ovHealth(m);
      out.push(cpu && z ? el('div', { class: 'ov-g2' }, cpu, z) : cpu || z);
    }
    const dm = inRoom('domains') ? ovDomains(m) : null;
    const ev = !first && inRoom('projekte', 'domains', 'system') ? ovEvents() : null;
    if (dm || ev) out.push(dm && ev ? el('div', { class: 'ov-g3' }, dm, ev) : dm || ev);
    if (!calm && !first && inRoom()) { const wk = ovWeek(); if (wk) out.push(wk); }
    if (calm && !calmCpu && inRoom('system')) { const cpu = ovCpu(); if (cpu) out.push(cpu); }
    if (key && svc) { const p = projPanel(m, key); if (p) out.push(p); }
    return out;
  }

  // ---------- Projekte ----------
  const PF = [
    ['alle', 'Alle', () => true],
    ['dich', 'Braucht dich', p => p.live ? p.live.unexpected > 0 || p.live.restarting : p.total > 0 && !p.ok],
    ['mit', 'Kommt mit', p => p.decision === 'take'],
    ['weg', 'Archiv oder zurück', p => p.decision === 'archive' || p.decision === 'skip'],
    ['offen', 'Offen', p => p.decision === 'open'],
  ];
  function rowsAll(m) {
    return m.projects.map(p => ({ kind: 'p', o: p, name: p.name, key: p.key, ok: p.ok, total: p.total, running: p.running, decision: p.decision,
      stack: p.services.map(s => s.name).join(', '), domains: [...new Set(p.routes.length ? p.routes.flatMap(r => r.hosts) : p.domains || [])],
      text: (p.name + ' ' + (p.domains || []).join(' ') + ' ' + p.routes.flatMap(r => r.hosts).join(' ') + ' ' + p.services.map(s => s.name + ' ' + s.image_name).join(' ')).toLowerCase(),
      size: core.projectBytes(p) }))
      .concat(m.containers.map(c => ({ kind: 'c', o: c, name: c.name, key: c.key, ok: c.running, total: 1, running: c.running ? 1 : 0, decision: c.decision,
        stack: 'Container ohne Compose-Projekt', domains: [], text: (c.name + ' ' + c.image).toLowerCase(), size: -2 })));
  }
  function projStatus(x) {
    if (!x.total) return st('idle', 'keine Container');
    if (x.kind === 'c') return st(x.ok ? 'ok' : 'idle', x.ok ? 'lief' : 'gestoppt');
    return st(x.ok ? 'ok' : 'err', x.total === 1 ? (x.ok ? 'lief' : 'gestoppt') : x.running + ' von ' + x.total + ' liefen');
  }
  // ---------- Betrieb: Container live ----------
  // Nur als Dienst. Die Liste kommt live aus Docker und frischt sich alle
  // 15 Sekunden auf, solange die Seite offen ist. Jede Aktion zeigt erst
  // eine Vorschau, dann bestätigst du mit dem Passwort; der Dienst nimmt nur
  // Starten, Stoppen und Neustarten an.
  const op = {
    rep: null, at: 0, loading: false, error: '', timer: 0, busy: '', result: null, key: '',
    lg: { name: '', lines: null, err: [], at: 0, stamp: '', loading: false, error: '', follow: false, timer: 0, cut: false, n: 200, end: true, top: 0, q: '', only: '', wrap: true },
  };
  // Der letzte Live-Stand bleibt für diesen Tab gemerkt. Beim Neuladen steht
  // die Seite damit sofort richtig da und frischt sich dann auf.
  const OPS_KEY = 'hostdeck-ops';
  try { const c = JSON.parse(sessionStorage.getItem(OPS_KEY) || 'null'); if (c && Array.isArray(c.container)) op.rep = c; } catch (e) { /* ohne Speicher */ }
  function skel(cls) { return el('span', { class: 'skel ' + (cls || ''), 'aria-hidden': 'true' }); }
  const OP_VERB = { starten: 'starten', stoppen: 'stoppen', neustarten: 'neu starten' };
  const OP_DONE = { starten: 'gestartet', stoppen: 'gestoppt', neustarten: 'neu gestartet' };
  const OP_BTN = { starten: 'Starten', stoppen: 'Stoppen', neustarten: 'Neu starten' };
  const OP_ICON = { starten: 'play', stoppen: 'stop', neustarten: 'refresh' };
  function live() { return core.svc() && op.rep ? op.rep.container : null; }
  function liveFor(x) {
    const l = live();
    if (!l) return null;
    return x.kind === 'p' ? l.filter(c => c.projekt === x.name) : l.filter(c => c.name === x.name);
  }
  // Zustand in Worten kommt vom Dienst (betrieb, die eine Quelle): je Container klasse, wort und stufe,
  // je Gruppe (Projekt, Container allein, verwaltete Server) Wort, Stufe,
  // Zusammensetzung und Zahlen. Übersicht, Lageplan, Überwachung und
  // Projekte übernehmen sie nur; der Prüfer im Labor vergleicht sie.
  function looping(c) { return c.klasse === 'schleife'; }
  function gkey(c) { return c.verwaltet_von ? 'panel:' + c.verwaltet_von : c.projekt ? 'p:' + c.projekt : 'c:' + c.name; }
  function grp(key) { const r = op.rep; return r && r.gruppen ? r.gruppen.find(g => g.schluessel === key) || null : null; }
  // Warum Container aus sind, mit Zahl, in fester Reihenfolge.
  const OFF_WHY = [['von_dir', 'von dir gestoppt'], ['letzter_lauf', 'seit dem letzten Lauf aus'], ['angelegt', 'angelegt'], ['unerwartet', 'unerwartet gestoppt']];
  function stopWhy(g) { return g ? OFF_WHY.filter(([k]) => g[k]).map(([k, w]) => g[k] + ' ' + w).join(' · ') : ''; }
  // Stand einer Gruppe vom Dienst; dazu Prozessor und Arbeitsspeicher
  // jetzt (Messwerte, kein Zustand). Mehrere Gruppen: die Summe aller.
  function liveSum(cs) {
    if (!cs || !cs.length || !op.rep || !op.rep.gruppen) return null;
    const own = cs.filter(c => !c.verwaltet_von);
    const keys = new Set((own.length ? own : cs).map(gkey));
    const g = keys.size === 1 ? grp([...keys][0]) : op.rep.summe;
    if (!g) return null;
    const s = Object.assign({}, g, { run: g.laufen, total: g.gesamt - g.jobs - g.angelegt, byYou: g.von_dir, jobs: g.jobs, created: g.angelegt,
      moved: g.letzter_lauf, unexpected: g.unerwartet, restarting: g.schleife > 0, cpu: null, mem: 0, panel: cs.length - own.length });
    for (const c of cs) if (c.cpu != null && (!own.length || !c.verwaltet_von)) { s.cpu = (s.cpu || 0) + c.cpu; s.mem += c.speicher || 0; }
    return s;
  }
  function liveStatus(s) {
    const b = st(s.stufe || 'idle', s.wort || '–');
    b.dataset.zustand = s.wort || '';
    if (s.text) b.title = s.text;
    return b;
  }
  // Zahl und Einheit bleiben zusammen (geschütztes Leerzeichen). Eine
  // Regel für jeden Anteil auf allen Seiten (Datenprüfung M1): unter 10 %
  // eine Nachkommastelle, sonst ganze Prozent; erst gerundet, dann
  // entschieden, damit 9,96 als „10 %“ steht und nicht als „10,0 %“.
  function pct(v) {
    const r = Math.round(v * 10) / 10;
    return (r < 10 ? r.toFixed(1) : String(Math.round(v))).replace('.', ',') + '\u00a0%';
  }
  // Anteil mit fester Stellenzahl d (0 oder 1); ohne d wie pct. Für
  // Prozessor gilt cpuText.
  function pctText(v, d) {
    if (d == null) return pct(v);
    return v.toFixed(d).replace('.', ',') + '\u00a0%';
  }
  // Prozessor als Anteil am ganzen Server: eine Nachkommastelle unter
  // 10 %; „< 0,1 %“ statt einer Null, die nicht gemessen wurde (6B).
  function cpuText(v) {
    if (v == null || isNaN(v)) return '–';
    return v > 0 && v < 0.05 ? '< 0,1\u00a0%' : pct(v);
  }
  function usage(s) {
    if (s.cpu != null) return cpuText(s.cpu) + ' · ' + size(s.mem);
    return s.run && op.rep && op.rep.messung_laeuft ? [skel('skel-t'), el('span', { class: 'sr' }, 'wird gemessen')] : '–';
  }
  function ago(iso) {
    const t = new Date(iso).getTime();
    if (!t) return '';
    const m = Math.max(0, Math.round((Date.now() - t) / 60000));
    if (m < 1) return 'gerade eben';
    if (m < 60) return 'seit ' + m + ' Min.';
    const h = Math.round(m / 60);
    if (h < 48) return 'seit ' + h + ' Std.';
    return 'seit ' + Math.round(h / 24) + ' Tagen';
  }
  function since(c) {
    if (!c.seit) return '';
    const a = ago(c.seit);
    if (c.job) return a === 'gerade eben' ? 'gerade beendet' : 'beendet ' + a.replace(/^seit /, 'vor ');
    if (a === 'gerade eben') return c.laeuft ? 'gerade gestartet' : 'gerade gestoppt';
    // Steht immer unter dem Zustandswort; „läuft · läuft seit“ wäre doppelt
    // (Datenprüfung N4).
    return a;
  }
  // Zeitstempel von Docker (2026-11-02T23:00:18.846286853Z) als Ortszeit.
  function logLine(l) {
    const m = /^(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(\.\d+)?Z /.exec(l);
    if (!m) return l;
    const d = new Date(m[1] + 'Z');
    if (isNaN(d)) return l;
    const two = n => String(n).padStart(2, '0');
    return two(d.getDate()) + '.' + two(d.getMonth() + 1) + '. ' + d.toLocaleTimeString('de-DE') + '  ' + l.slice(m[0].length);
  }
  function cState(c) {
    const b = st(c.stufe || 'idle', c.wort || c.zustand);
    b.dataset.zustand = c.wort || c.zustand;
    return b;
  }
  function stamp(iso) { const d = new Date(iso); return isNaN(d) ? '' : d.toLocaleTimeString('de-DE'); }

  // Nächstes Auffrischen planen: alle 15 Sekunden, solange gemessen wird
  // schon nach 1,5 Sekunden.
  function scheduleOps() {
    if (op.loading || op.timer || !core.svc() || document.visibilityState === 'hidden') return;
    const every = op.rep && op.rep.messung_laeuft ? 1500 : 15000;
    op.timer = setTimeout(loadOps, !op.rep && !op.error ? 0 : Math.max(0, every - (Date.now() - op.at)));
  }
  async function loadOps() {
    clearTimeout(op.timer);
    op.timer = 0;
    if (op.loading || !core.svc()) return;
    op.loading = true;
    const r = await core.call('/api/container', null, true);
    op.loading = false;
    op.at = Date.now();
    if (r.ok) {
      op.rep = r.data; op.error = '';
      try { sessionStorage.setItem(OPS_KEY, JSON.stringify(r.data)); } catch (e) { /* ohne Speicher */ }
    } else op.error = (r.data && r.data.error) || 'Docker antwortete nicht (Fehler ' + r.status + ').';
    // Übersicht und Überwachung nehmen die Wörter „von dir gestoppt“ und
    // „seit dem letzten Lauf aus“ daraus (Labor S4: direkt geöffnet stand dort sonst nur „gestoppt“).
    if ((ui.page === 'lage' || ui.page === 'ueberwachung') && core.work() && !modalOpen() && !uw.hover) { core.rerender(); return; }
    if (ui.page !== 'projekte') return;
    scheduleOps();
    if (core.work() && !modalOpen() && !uw.hover && !selecting()) core.rerender();
  }
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible' && ui.page === 'projekte') scheduleOps();
  });

  async function loadLogs() {
    const lg = op.lg;
    if (lg.loading || !lg.name) return;
    lg.loading = true;
    const r = await core.call('/api/container/logs?name=' + encodeURIComponent(lg.name) + '&zeilen=' + lg.n, null, true);
    lg.loading = false;
    lg.at = Date.now();
    clearTimeout(lg.timer);
    if (r.ok) { lg.lines = r.data.zeilen || []; lg.err = r.data.stderr || []; lg.cut = !!r.data.gekuerzt; lg.stamp = r.data.zeit; lg.error = ''; } else {
      lg.error = (r.data && r.data.error) || 'Die Logs ließen sich nicht laden (Fehler ' + r.status + ').';
      if (!lg.lines) lg.lines = [];
    }
    if (lg.follow && ui.page === 'projekte' && pv.tab === 'logs' && op.key && document.visibilityState !== 'hidden') lg.timer = setTimeout(loadLogs, 4000);
    if (core.work() && !modalOpen() && !selecting()) core.rerender();
  }

  // Vorschau: Was passiert, was bleibt, wie du zurückkommst. true heißt los.
  function opPreview(p) {
    return new Promise(resolve => {
      let close = null;
      const done = ok => { close(); resolve(ok); };
      const skipped = p.ausgelassen || [];
      const body = p.problem ? [el('p', { id: 'op-d', class: 'gt-lead' }, p.problem)] : [
        el('section', { class: 'op-part', id: 'op-d' }, el('h3', {}, p.container.length > 1 ? 'In dieser Reihenfolge' : 'Betrifft'),
          el('ol', { class: 'op-list' }, p.container.map(n => el('li', {}, el('span', { class: 'mono' }, n))))),
        skipped.length ? el('section', { class: 'op-part' }, el('h3', {}, 'Bleibt, wie es ist'),
          el('ul', { class: 'op-list' }, skipped.map(s => el('li', {}, el('span', { class: 'mono' }, s.name), el('span', { class: 'ls-mut' }, ': ' + s.grund))))) : null,
        el('dl', { class: 'op-kv' }, el('dt', {}, 'Danach'), el('dd', {}, p.auswirkung), el('dt', {}, 'Rückweg'), el('dd', {}, p.rueckweg)),
        ...(p.hinweise || []).map(h => el('p', { class: 'ls-note op-warn' }, ic('warn', 16), el('span', {}, h)))];
      const go = p.problem ? null : el('button', { type: 'button', class: 'btn btn-p', onclick: () => done(true) }, ic(OP_ICON[p.aktion], 16), OP_BTN[p.aktion]);
      const box = el('div', { class: 'gt-dlg op-dlg', role: 'dialog', 'aria-modal': 'true', 'aria-labelledby': 'op-h', 'aria-describedby': 'op-d' },
        el('p', { class: 'ls-eyb' }, ic('eye', 16), el('span', {}, 'Vorschau · noch ist nichts passiert')),
        el('h2', { id: 'op-h' }, p.titel + ' ' + OP_VERB[p.aktion] + '?'),
        ...body,
        el('div', { class: 'gt-acts' }, el('button', { type: 'button', class: 'btn', onclick: () => done(false) }, p.problem ? 'Schließen' : 'Abbrechen'), go));
      close = modal(box, () => done(false));
      (go || box.querySelector('.btn')).focus();
    });
  }

  async function opRun(target, action) {
    if (op.busy) return;
    op.busy = target + ' ' + action;
    core.rerender();
    const r = await core.call('/api/container/vorschau', { ziel: target, aktion: action });
    op.busy = '';
    if (!r.ok) {
      op.result = { ok: false, text: (r.data && r.data.error) || 'Die Vorschau ging nicht (Fehler ' + r.status + ').' };
      core.rerender();
      return;
    }
    const p = r.data;
    if (!(await opPreview(p))) { core.rerender(); return; }
    op.busy = target + ' ' + action;
    op.result = null;
    core.rerender();
    core.announce(p.titel + ': ' + OP_VERB[action] + ' läuft');
    const a = await core.call('/api/container/aktion', { ziel: target, aktion: action, container: p.container });
    op.busy = '';
    dzFresh(true);
    if (a.ok) {
      const bad = (a.data.schritte || []).filter(s => !s.ok);
      op.result = bad.length
        ? { ok: false, text: 'Nicht alles hat geklappt.', steps: bad }
        : { ok: true, text: p.titel + ' ' + OP_DONE[action] + (p.ziel.startsWith('container:') || p.container.join() === p.ziel.slice(8) ? '.' : ': ' + p.container.join(', ') + '.') };
      core.announce(op.result.text);
    } else if (!(a.data && a.data.abgebrochen)) {
      op.result = { ok: false, text: (a.data && a.data.error) || 'Das ging nicht (Fehler ' + a.status + ').' };
    }
    op.at = 0;
    await loadOps();
    core.rerender();
    // Der gedrückte Knopf ist danach oft ein anderer (Stoppen wird
    // Starten); der Fokus bleibt bei der Meldung statt auf der Seite.
    const f = document.querySelector('#page .op-x');
    if (f && !modalOpen() && (!document.activeElement || document.activeElement === document.body)) f.focus({ preventScroll: true });
  }

  // quiet: als Textknopf, etwa in jeder Zeile der Tabelle.
  function opButton(target, action, quiet, label) {
    const busy = op.busy === target + ' ' + action;
    return el('button', {
      type: 'button', class: 'btn btn-sm' + (quiet ? ' btn-g' : '') + (busy ? ' is-busy' : ''), 'data-fid': 'op-' + action + '-' + target,
      disabled: !!op.busy, 'aria-busy': busy ? 'true' : null,
      onclick: e => { e.stopPropagation(); opRun(target, action); },
    }, ic(OP_ICON[action], 16), (label || OP_BTN[action]) + (busy ? ' …' : ''));
  }
  // Knöpfe für ein ganzes Projekt oder einen Container ohne Projekt.
  function opActs(target, s) {
    if (!s.total) return null;
    const out = [];
    if (s.run > 0) out.push(opButton(target, 'neustarten'));
    if (s.run < s.total) out.push(opButton(target, 'starten'));
    if (s.run > 0) out.push(opButton(target, 'stoppen'));
    return out;
  }
  function opResult() {
    const r = op.result;
    if (!r) return null;
    return el('div', { class: 'ls-note op-res ' + (r.ok ? 'is-ok' : 'is-err'), role: 'status' }, ic(r.ok ? 'done' : 'warn', 16),
      el('span', {}, r.text, r.steps ? el('ul', {}, r.steps.map(s => el('li', {}, el('span', { class: 'mono' }, s.name), ': ' + s.fehler))) : null),
      el('button', { type: 'button', class: 'ls-ibtn op-x', 'aria-label': 'Meldung schließen', onclick: () => { op.result = null; core.rerender(); } }, ic('x', 16)));
  }

  // ---------- Mehrere auf einmal ----------
  // Dieselbe Reihenfolge wie bei einem: erst eine gemeinsame Vorschau aller
  // Pläne, dann einmal das Passwort (gilt fünf Minuten, reicht für alle),
  // dann nacheinander, jedes Projekt mit eigenem Eintrag im Protokoll.
  const OP_MANY = {
    starten: ['Die Container starten, ein Projekt nach dem anderen in der Reihenfolge oben.', 'Stoppen.'],
    stoppen: ['Die Container halten an; was daran hängt, ist so lange nicht erreichbar. HostDeck merkt sich, dass du sie gestoppt hast: Ein Backup oder ein Neustart des Projekts startet sie nicht wieder.', 'Starten. Daten und Einstellungen bleiben, wie sie sind.'],
    neustarten: ['Jeder Container startet neu, einer nach dem anderen, und ist dabei einige Sekunden nicht erreichbar.', 'Nicht nötig: Danach läuft alles wie vorher.'],
  };
  function manyLabel(n, plans) {
    return plans.every(p => p.ziel.startsWith('projekt:')) ? plural(n, 'Projekt', 'Projekte') : plural(n, 'Eintrag', 'Einträge');
  }
  function opPreviewMany(plans, fails, action) {
    return new Promise(resolve => {
      let close = null;
      const done = ok => { close(); resolve(ok); };
      const run = plans.filter(p => !p.problem);
      const left = plans.filter(p => p.problem).map(p => ({ name: p.titel, why: p.problem })).concat(fails.map(f => ({ name: f.name, why: f.fehler })));
      const nC = run.reduce((a, p) => a + p.container.length, 0);
      const body = [
        el('p', { id: 'op-d', class: 'gt-lead' }, run.length
          ? (run.length > 1 ? 'HostDeck geht sie nacheinander durch, in dieser Reihenfolge. ' : '') + 'Zusammen ' + (nC === 1 ? 'ist es 1 Container.' : 'sind es ' + nC + ' Container.')
          : 'Bei keinem der ausgewählten gibt es gerade etwas zu ' + OP_VERB[action] + '.'),
        run.length ? el('ol', { class: 'op-many' }, run.map(p => el('li', {},
          el('h3', {}, p.titel),
          el('p', { class: 'mono' }, p.container.join(' → ')),
          (p.ausgelassen || []).length ? el('p', { class: 'ls-mut' }, 'Bleibt: ' + p.ausgelassen.map(s => s.name + ' (' + s.grund + ')').join(', ')) : null,
          ...(p.hinweise || []).map(h => el('p', { class: 'ls-note op-warn' }, ic('warn', 16), el('span', {}, h)))))) : null,
        left.length ? el('section', { class: 'op-part' }, el('h3', {}, run.length ? 'Lässt HostDeck aus' : 'Warum'),
          el('ul', { class: 'op-list' }, left.map(l => el('li', {}, el('span', { class: 'mono' }, l.name), el('span', { class: 'ls-mut' }, ': ' + l.why))))) : null,
        run.length ? el('dl', { class: 'op-kv' }, el('dt', {}, 'Danach'), el('dd', {}, OP_MANY[action][0]), el('dt', {}, 'Rückweg'), el('dd', {}, OP_MANY[action][1])) : null];
      const go = run.length ? el('button', { type: 'button', class: 'btn btn-p', onclick: () => done(true) }, ic(OP_ICON[action], 16), run.length > 1 ? 'Alle ' + run.length + ' ' + OP_VERB[action] : OP_BTN[action]) : null;
      const box = el('div', { class: 'gt-dlg op-dlg', role: 'dialog', 'aria-modal': 'true', 'aria-labelledby': 'op-h', 'aria-describedby': 'op-d' },
        el('p', { class: 'ls-eyb' }, ic('eye', 16), el('span', {}, 'Vorschau · noch ist nichts passiert')),
        el('h2', { id: 'op-h' }, manyLabel(run.length || plans.length + fails.length, run.length ? run : plans) + ' ' + OP_VERB[action] + '?'),
        ...body,
        el('div', { class: 'gt-acts' }, el('button', { type: 'button', class: 'btn', onclick: () => done(false) }, run.length ? 'Abbrechen' : 'Schließen'), go));
      close = modal(box, () => done(false));
      (go || box.querySelector('.btn')).focus();
    });
  }
  async function opRunMany(items, action) {
    if (op.busy || !items.length) return;
    op.busy = 'viele ' + action;
    op.result = null;
    op.prog = { i: 0, n: items.length, name: '', what: 'Vorschau' };
    core.rerender();
    // Nacheinander statt gleichzeitig: Fragt der Dienst nach Anmeldung,
    // soll das genau einmal passieren.
    const plans = [], fails = [];
    for (const it of items) {
      op.prog.i++; op.prog.name = it.name; core.rerender();
      const r = await core.call('/api/container/vorschau', { ziel: it.target, aktion: action });
      if (r.ok) plans.push(r.data); else fails.push({ name: it.name, fehler: (r.data && r.data.error) || 'Die Vorschau ging nicht (Fehler ' + r.status + ').' });
    }
    op.busy = ''; op.prog = null;
    core.rerender();
    if (!(await opPreviewMany(plans, fails, action))) {
      core.rerender();
      const f = document.querySelector('[data-fid="psel-' + action + '"]');
      if (f && !f.disabled) f.focus({ preventScroll: true });
      return;
    }
    const run = plans.filter(p => !p.problem);
    op.busy = 'viele ' + action;
    op.prog = { i: 0, n: run.length, name: '', what: OP_BTN[action] };
    const ok = [], bad = [];
    let cut = false;
    for (const p of run) {
      op.prog.i++; op.prog.name = p.titel; core.rerender();
      core.announce(p.titel + ': ' + OP_VERB[action] + ' läuft');
      const a = await core.call('/api/container/aktion', { ziel: p.ziel, aktion: action, container: p.container });
      if (a.ok) {
        const b = (a.data.schritte || []).filter(s => !s.ok);
        if (b.length) bad.push(...b); else ok.push(p.titel.replace(/^(Projekt|Container) /, ''));
      } else if (a.data && a.data.abgebrochen) { cut = true; break; } else bad.push({ name: p.titel, fehler: (a.data && a.data.error) || 'Das ging nicht (Fehler ' + a.status + ').' });
    }
    dzFresh(true);
    op.busy = ''; op.prog = null;
    const notRun = run.length - ok.length - bad.length;
    if (cut && !ok.length && !bad.length) { core.rerender(); return; }
    op.result = bad.length || cut
      ? { ok: false, text: (ok.length ? OP_DONE[action][0].toUpperCase() + OP_DONE[action].slice(1) + ': ' + ok.join(', ') + '. ' : '') + (bad.length ? 'Nicht alles hat geklappt.' : '') + (cut && notRun ? ' Nicht bestätigt, ' + manyLabel(notRun, run) + ' nicht mehr ' + OP_DONE[action] + '.' : ''), steps: bad.length ? bad : null }
      : { ok: true, text: manyLabel(ok.length, run) + ' ' + OP_DONE[action] + ': ' + ok.join(', ') + '.' };
    if (!bad.length && !cut) ui.psel.clear();
    core.announce(op.result.text);
    op.at = 0;
    await loadOps();
    core.rerender();
    const f = document.querySelector('#page .op-x');
    if (f && !modalOpen() && (!document.activeElement || document.activeElement === document.body)) f.focus({ preventScroll: true });
  }

  // ---------- Projekte aktualisieren ----------
  // Nachsehen liest nur (Registry, Git-Gegenstelle). Aktualisieren zeigt
  // erst eine Vorschau mit Auswahl je Dienst, dann bestätigst du mit dem
  // Passwort. Der Dienst holt oder baut, startet neu, prüft und stellt bei
  // Problemen von selbst zurück. Das alte Image bleibt als Rückfall.
  const pu = { rep: null, fb: {}, job: null, at: 0, loading: false, checking: '', error: '', timer: 0, result: null };
  const UP_STATE = { aktuell: ['aktuell', 't-ok'], neu: ['Neues da', 't-new'], unbekannt: ['nicht prüfbar', 't-mut'], fest: ['fest eingestellt', 't-mut'], lokal: ['nur auf diesem Server', 't-mut'] };
  function upProject(name) {
    return pu.rep && pu.rep.projekte ? pu.rep.projekte.find(p => p.projekt === name) : null;
  }
  function upNewer(name) {
    const p = upProject(name);
    return p ? p.dienste.filter(s => s.stand === 'neu' && s.laeuft).length : 0;
  }
  async function loadUp() {
    if (pu.loading || !core.svc()) return;
    pu.loading = true;
    const r = await core.call('/api/aktualisieren', null, true);
    pu.loading = false;
    pu.at = Date.now();
    clearTimeout(pu.timer);
    pu.timer = 0;
    if (r.ok) {
      pu.rep = r.data.pruefstand; pu.fb = r.data.rueckfall || {}; pu.error = '';
      const was = pu.job && pu.job.laeuft;
      pu.job = r.data.auftrag || null;
      if (was && pu.job && !pu.job.laeuft) { pu.result = upResultOf(pu.job); op.at = 0; loadOps(); }
    } else if (r.status !== 404) pu.error = (r.data && r.data.error) || 'Fehler ' + r.status;
    if (pu.job && pu.job.laeuft && ui.page === 'projekte') pu.timer = setTimeout(loadUp, 1500);
    if (ui.page === 'projekte' && core.work() && !modalOpen()) core.rerender();
  }
  function upResultOf(j) {
    if (j.fehler) return { ok: false, text: j.fehler };
    const r = j.ergebnis || {};
    const names = (r.geaendert || []).map(c => c.dienst).join(', ');
    if (j.zurueck) return r.ok ? { ok: true, text: 'Projekt ' + j.projekt + ' läuft wieder mit dem Stand von vorher.' } : { ok: false, text: 'Zurückstellen ging nicht: ' + r.problem };
    if (r.ok && !names) return { ok: true, text: 'Projekt ' + j.projekt + ' war schon aktuell; nichts neu gestartet.' };
    if (r.ok) return { ok: true, text: 'Projekt ' + j.projekt + ' aktualisiert: ' + names + '. Das alte Image bleibt als Rückfall.' };
    if (r.zurueckgestellt) return { ok: false, text: 'Das neue lief nicht sauber (' + r.problem + '). HostDeck hat den alten Stand wiederhergestellt.' };
    return { ok: false, text: 'Aktualisieren ging nicht: ' + r.problem };
  }
  async function upCheck(project) {
    if (pu.checking) return;
    pu.checking = project || '*';
    core.rerender();
    const r = await core.call('/api/aktualisieren/nachsehen', { projekt: project || '' });
    pu.checking = '';
    if (!r.ok) pu.result = { ok: false, text: (r.data && r.data.error) || 'Nachsehen ging nicht (Fehler ' + r.status + ').' };
    await loadUp();
    core.rerender();
  }
  // Vorschau mit Auswahl; true und die gewählten Dienste heißt los.
  function upPreview(first, revert) {
    return new Promise(resolve => {
      let close = null, p = first, busy = false;
      const done = ok => { close(); resolve(ok ? p : null); };
      const box = el('div', { class: 'gt-dlg op-dlg up-dlg', role: 'dialog', 'aria-modal': 'true', 'aria-labelledby': 'up-h', 'aria-describedby': 'up-d' });
      const draw = () => {
        const opts = p.auswahl || [];
        const go = p.problem ? null : el('button', { type: 'button', class: 'btn btn-p', disabled: busy, onclick: () => done(true) }, ic(revert ? 'undo' : 'refresh', 16), revert ? 'Zurückstellen' : 'Aktualisieren');
        box.replaceChildren(...[
          el('p', { class: 'ls-eyb' }, ic('eye', 16), el('span', {}, 'Vorschau · noch ist nichts passiert')),
          el('h2', { id: 'up-h' }, 'Projekt ' + p.projekt + (revert ? ' auf vorher zurückstellen?' : ' aktualisieren?')),
          opts.length ? el('fieldset', { class: 'op-part up-pick' }, el('legend', {}, 'Dienste'),
            opts.map(o => el('label', { class: 'up-opt' },
              el('input', { type: 'checkbox', checked: o.gewaehlt, disabled: busy, onchange: e => pick(o.dienst, e.target.checked) }),
              el('span', {}, el('b', {}, o.dienst), ' ', el('span', { class: 'ls-mut' }, o.aktion + (o.datenbank ? ' · Datenbank' : '')),
                o.stand ? [' ', el('span', { class: 'tag ' + (UP_STATE[o.stand] || ['', 't-mut'])[1] }, (UP_STATE[o.stand] || [o.stand])[0])] : null,
                o.detail ? el('span', { class: 'ls-mut blk' }, o.detail) : null)))) : null,
          (p.ausgelassen || []).length ? el('section', { class: 'op-part' }, el('h3', {}, 'Bleibt, wie es ist'),
            el('ul', { class: 'op-list' }, p.ausgelassen.map(x => el('li', {}, el('span', { class: 'mono' }, x.name), el('span', { class: 'ls-mut' }, ': ' + x.grund))))) : null,
          p.problem ? el('p', { id: 'up-d', class: 'gt-lead' }, p.problem) : el('section', { class: 'op-part', id: 'up-d' }, el('h3', {}, 'Was passiert'),
            el('ol', { class: 'op-list' }, (p.schritte || []).map(x => el('li', {}, x)))),
          p.problem ? null : el('dl', { class: 'op-kv' }, el('dt', {}, 'Rückweg'), el('dd', {}, p.rueckweg)),
          ...(p.hinweise || []).map(h => el('p', { class: 'ls-note op-warn' }, ic('warn', 16), el('span', {}, h))),
          el('div', { class: 'gt-acts' }, el('button', { type: 'button', class: 'btn', onclick: () => done(false) }, p.problem ? 'Schließen' : 'Abbrechen'), go)].filter(Boolean));
      };
      const pick = async (name, on) => {
        const want = (p.auswahl || []).filter(o => o.dienst === name ? on : o.gewaehlt).map(o => o.dienst);
        busy = true; draw();
        const r = await core.call('/api/aktualisieren/vorschau', { projekt: p.projekt, dienste: want });
        busy = false;
        if (r.ok) p = r.data;
        draw();
        const f = box.querySelector('input:not([disabled])');
        if (f && document.activeElement === document.body) f.focus();
      };
      draw();
      close = modal(box, () => done(false));
      (box.querySelector('.btn-p') || box.querySelector('.btn')).focus();
    });
  }
  async function upRun(project, revert) {
    if (pu.checking || (pu.job && pu.job.laeuft)) return;
    pu.checking = 'vorschau';
    core.rerender();
    const r = await core.call(revert ? '/api/aktualisieren/zurueck/vorschau' : '/api/aktualisieren/vorschau', { projekt: project });
    pu.checking = '';
    if (!r.ok) { pu.result = { ok: false, text: (r.data && r.data.error) || 'Die Vorschau ging nicht (Fehler ' + r.status + ').' }; core.rerender(); return; }
    const p = await upPreview(r.data, revert);
    if (!p) { core.rerender(); return; }
    pu.result = null;
    const a = await core.call(revert ? '/api/aktualisieren/zurueck' : '/api/aktualisieren/starten', revert ? { projekt: project } : { projekt: project, dienste: p.dienste });
    if (!a.ok) {
      if (!(a.data && a.data.abgebrochen)) pu.result = { ok: false, text: (a.data && a.data.error) || 'Das ging nicht (Fehler ' + a.status + ').' };
      core.rerender();
      return;
    }
    pu.job = a.data;
    core.announce('Projekt ' + project + (revert ? ': Zurückstellen läuft' : ': Aktualisieren läuft'));
    loadUp();
  }
  // Karte „Version“ auf der Seite eines Projekts.
  function verBox(x) {
    if (!core.svc() || x.kind !== 'p') return null;
    const pc = upProject(x.name);
    const fb = pu.fb[x.name];
    const j = pu.job && pu.job.projekt === x.name ? pu.job : null;
    const running = !!(pu.job && pu.job.laeuft);
    const rows = pc ? (pc.fehler ? [el('p', { class: 'ls-mut' }, pc.fehler)] : pc.dienste.map(s => el('div', { class: 'ls-row' },
      el('div', {}, el('b', {}, s.dienst), el('span', { class: 'mono ls-mut' }, s.image + (s.art === 'gebaut' ? ' · selbst gebaut' : '')),
        s.detail ? el('span', { class: 'ls-mut' }, s.detail) : null),
      el('div', { class: 'ta-r' }, el('span', { class: 'tag ' + (UP_STATE[s.stand] || ['', 't-mut'])[1] }, (UP_STATE[s.stand] || [s.stand])[0]))))) :
      [el('p', { class: 'ls-mut' }, pu.rep ? 'Für dieses Projekt hat HostDeck noch nicht nachgesehen.' : 'Lädt …')];
    const canBack = fb && !fb.zurueck && (fb.dienste || []).some(d => d.neu);
    const busyCheck = pu.checking === x.name || pu.checking === '*';
    const acts = el('div', { class: 'op-btns' },
      el('button', { type: 'button', class: 'btn btn-sm', 'data-fid': 'up-check-' + x.name, disabled: !!pu.checking || running, 'aria-busy': busyCheck ? 'true' : null, onclick: () => upCheck(x.name) },
        ic('search', 16), busyCheck ? 'Sieht nach …' : 'Nachsehen'),
      el('button', { type: 'button', class: 'btn btn-sm' + (upNewer(x.name) ? ' btn-p' : ''), 'data-fid': 'up-run-' + x.name, disabled: !!pu.checking || running, onclick: () => upRun(x.name, false) },
        ic('refresh', 16), 'Aktualisieren …'),
      canBack ? el('button', { type: 'button', class: 'btn btn-sm btn-g', 'data-fid': 'up-back-' + x.name, disabled: !!pu.checking || running, onclick: () => upRun(x.name, true) },
        ic('undo', 16), 'Zurück auf vorher') : null);
    const res = pu.result && (!j || !j.laeuft) ? el('div', { class: 'ls-note op-res ' + (pu.result.ok ? 'is-ok' : 'is-err'), role: 'status' }, ic(pu.result.ok ? 'done' : 'warn', 16),
      el('span', {}, pu.result.text),
      el('button', { type: 'button', class: 'ls-ibtn op-x', 'aria-label': 'Meldung schließen', onclick: () => { pu.result = null; core.rerender(); } }, ic('x', 16))) : null;
    const log = j && (j.laeuft || j.zeilen.length) ? [
      el('p', { class: 'ls-mut' }, j.laeuft ? (j.zurueck ? 'Zurückstellen läuft …' : 'Aktualisieren läuft … Du kannst die Seite schließen; es läuft weiter.') : 'Ausgabe des letzten Laufs'),
      el('pre', { class: 'bk-log up-log', tabindex: '0', 'aria-label': 'Ausgabe', 'aria-live': j.laeuft ? 'polite' : null }, j.zeilen.join('\n'))] : [];
    const stamp2 = pc ? 'nachgesehen ' + when(pc.zeit) : '';
    const fbText = fb && !fb.zurueck && canBack ? 'Rückfall vom ' + when(fb.zeit) + ': ' + fb.dienste.filter(d => d.neu).map(d => d.dienst).join(', ') : '';
    return sec('Version', el('div', { class: 'pv-rows' }, res, ...rows, fbText ? el('p', { class: 'ls-mut' }, fbText) : null, ...log, acts), { cls: 'c6', sub: stamp2 });
  }

  // Rang für „nach Zustand“: Probleme zuerst, dann von dir Gestopptes,
  // dann was läuft.
  function stRank(x) {
    const s = x.live;
    if (s) return !s.total ? 4 : s.restarting ? 0 : s.unexpected ? 1 : s.byYou ? 2 : 3;
    return !x.total ? 4 : x.ok ? 3 : 1;
  }
  const PSORT = {
    name: (a, b) => a.name.localeCompare(b.name, 'de'),
    st: (a, b) => stRank(a) - stRank(b) || a.name.localeCompare(b.name, 'de'),
    cpu: (a, b) => ((b.live && b.live.cpu) || 0) - ((a.live && a.live.cpu) || 0),
    mem: (a, b) => ((b.live && b.live.mem) || 0) - ((a.live && a.live.mem) || 0),
    size: (a, b) => b.size - a.size,
  };
  // Menü „…“ einer Zeile: dieselben Wege wie in der Schublade.
  function rowMenu(x, target, s) {
    const id = 'pm-' + x.key;
    const open = ui.pmenu === x.key;
    const href = '#/projekte/' + encodeURIComponent(x.key);
    const close = focus => { ui.pmenu = ''; core.rerender(); if (focus) { const b = document.querySelector('[data-fid="pm-b-' + x.key + '"]'); if (b) b.focus(); } };
    const btn = el('button', { type: 'button', class: 'ls-ibtn ls-rbtn', 'data-fid': 'pm-b-' + x.key, 'aria-label': 'Weitere Aktionen für ' + x.name, 'aria-haspopup': 'menu', 'aria-expanded': String(open), 'aria-controls': open ? id : null,
      onclick: e => { e.stopPropagation(); ui.pmenu = open ? '' : x.key; core.rerender(); if (!open) { const f = document.querySelector('#' + CSS.escape(id) + ' [role="menuitem"]'); if (f) f.focus({ preventScroll: true }); } } }, ic('dots', 18));
    if (!open) return el('div', { class: 'ls-rmenu-w' }, btn);
    const act = (a, label) => el('button', { type: 'button', class: 'mi', role: 'menuitem', tabindex: '-1', disabled: !!op.busy, onclick: e => { e.stopPropagation(); ui.pmenu = ''; opRun(target, a); } }, ic(OP_ICON[a], 17), el('span', {}, label || OP_BTN[a] + ' …'));
    const items = [
      el('a', { class: 'mi', role: 'menuitem', tabindex: '-1', href, onclick: () => { ui.pmenu = ''; } }, ic('right', 17), el('span', {}, 'Öffnen')),
      s ? el('a', { class: 'mi', role: 'menuitem', tabindex: '-1', href: pvHref(x, 'logs'), onclick: () => { ui.pmenu = ''; } }, ic('doc', 17), el('span', {}, 'Logs ansehen')) : null,
      s && s.total && s.run > 0 ? act('neustarten') : null,
      s && s.total && s.run < s.total ? act('starten') : null,
      s && s.total && s.run > 0 ? act('stoppen') : null,
      isAlltag() ? null : el('a', { class: 'mi', role: 'menuitem', tabindex: '-1', href: '#/auswahl/bereich/projekte', onclick: () => { ui.pmenu = ''; } }, ic('auswahl', 17), el('span', {}, 'Umzug festlegen')),
    ].filter(Boolean);
    const menu = el('div', { class: 'menu ls-rmenu', role: 'menu', id, 'aria-label': 'Aktionen für ' + x.name, onkeydown: e => core.menuKeys(e, () => close(true)), onclick: e => e.stopPropagation() }, items);
    // Fest am Knopf, damit die Tabelle es nicht abschneidet; unten am
    // Fenster klappt es nach oben auf.
    requestAnimationFrame(() => {
      if (!btn.isConnected) return;
      const r = btn.getBoundingClientRect(), mh = menu.offsetHeight;
      menu.style.position = 'fixed';
      menu.style.right = Math.max(8, window.innerWidth - r.right) + 'px';
      menu.style.top = (r.bottom + 6 + mh > window.innerHeight - 8 && r.top - 6 - mh > 8 ? r.top - 6 - mh : r.bottom + 6) + 'px';
    });
    return el('div', { class: 'ls-rmenu-w' }, btn, menu);
  }
  function cpuCell(v, scale) {
    if (v == null) return '–';
    const bar = el('span', { class: 'ls-bar', 'aria-hidden': 'true' }, el('i', {}));
    bar.firstChild.style.width = Math.min(100, v / scale * 100).toFixed(1) + '%';
    return el('span', { class: 'ls-load' }, bar, el('span', { class: 'n' }, cpuText(v)));
  }
  function pageProjekte(m, key) {
    const svc = !!core.svc();
    scheduleOps();
    if (svc && !pu.loading && (!pu.at || (pu.job && pu.job.laeuft && !pu.timer))) setTimeout(loadUp, 0);
    const pk = pvSplit(rowsAll(m), key);
    // Dieselben Einträge wie auf der Übersicht und in der Leiste, mit
    // jedem Server, den ein Panel startet.
    let all = pRows(m);
    if (pk) return pageProjekt(m, pk.x, pk.tab);
    op.key = ''; pv.tab = ''; clearTimeout(op.lg.timer);
    // Eine Gruppe aus der Leiste (#/projekte/gruppe:spiel): dieselbe
    // Tabelle, nur mit ihren Projekten.
    const grp = key && key.startsWith('gruppe:') && PGROUP[key.slice(7)] ? key.slice(7) : '';
    const gName = grp ? PG_NAV.find(g => g[0] === grp)[1] : '';
    if (grp) {
      all = pRows(m).filter(x => x.grp === grp);
      if (!all.length) return [el('a', { class: 'pv-back', href: '#/projekte' }, ic('left', 16), 'Alle Projekte'),
        head({ i: 'projekte', k: gName, h: grp === 'spiel' ? 'HostDeck hat auf diesem Server keinen Spielserver erkannt.' : 'In dieser Gruppe ist gerade nichts.' })];
    }
    const lv = live();
    // Warten auf den ersten Live-Stand: dieselben Spalten wie danach, mit
    // Platzhaltern, damit nichts springt.
    const wait = svc && !lv && !op.error;
    const cols = !!lv || wait;
    for (const x of all) x.live = lv ? (x.kind === 'g' ? gLive(x.c, x.game) : liveSum(liveFor(x))) : null;
    let h;
    if (wait) {
      h = [skel('skel-h'), el('span', { class: 'sr' }, 'Der Zustand der Projekte wird geladen.')];
    } else if (lv) {
      // Einträge mit Diensten: Projekte und Container, die laufen sollen, und
      // jeder verwaltete Server (dieselbe Zählung wie die Übersicht).
      const pr = all.filter(x => x.live && (x.kind === 'g' || x.live.total));
      const rest = all.filter(x => x.kind !== 'g' && x.live && !x.live.total && liveFor(x).length);
      const bad = pr.filter(x => x.live.unexpected || x.live.restarting);
      const okN = pr.length - bad.length;
      const n = pCount(all);
      const names = l => l.length > 3 ? plural(l.length, 'Eintrag', 'Einträge') : l.join(l.length === 2 ? ' und ' : ', ');
      const offOther = pr.filter(x => pState(x) === 'off' && !x.live.byYou && !x.live.moved).map(x => x.name);
      h = bad.length
        ? [el('span', { class: 'ls-dot err', 'aria-hidden': 'true' }), okN + ' von ' + plural(pr.length, 'Projekt', 'Projekten') + ' laufen rund. Bei ', em('err', bad.map(x => x.name).join(' und ')),
          bad.length === 1 ? (bad[0].live.restarting ? ' steckt ein Container in einer Neustart-Schleife.' : ' steht ein Container.') : ' stimmt etwas nicht.']
        : pr.some(x => x.live.moved)
          // Umzugstag: Der letzte Lauf hat sie gestoppt; das ist Absicht.
          ? [el('span', { class: 'ls-dot', 'aria-hidden': 'true' }), plural(pr.filter(x => x.live.moved).length, 'Projekt ist', 'Projekte sind') + ' seit dem letzten Lauf des Umzugs aus. Sie laufen jetzt auf dem neuen Server; zurück holt sie „Zurück zum alten Server“ unter Umzug.']
        : pr.some(x => x.live.byYou) || offOther.length
          // Gestoppt ist kein Fehler, aber „alle laufen“ wäre falsch.
          ? (() => {
            const you = pr.filter(x => x.live.byYou).map(x => x.name);
            const bits = [];
            if (you.length) bits.push(you.length === 1 ? you[0] + ' hast du gestoppt' : names(you) + ' hast du (teils) gestoppt');
            if (offOther.length) bits.push(names(offOther) + (offOther.length === 1 ? ' ist aus' : ' sind aus'));
            return [el('span', { class: 'ls-dot ok', 'aria-hidden': 'true' }), plural(n.run, 'Projekt', 'Projekte') + ' ' + (n.run === 1 ? 'läuft' : 'laufen') + ', ' + bits.join(', ') + '.'];
          })()
          : [el('span', { class: 'ls-dot ok', 'aria-hidden': 'true' }), pr.length === 1 ? pr[0].name + ' läuft.' : 'Alle ' + plural(pr.length, 'Projekt', 'Projekte') + (rest.length ? ' mit Diensten' : '') + ' laufen.'];
      // Server, die ein Panel startet, zählen mit und stehen einzeln da.
      const nG = all.filter(x => x.kind === 'g').length;
      if (nG) h.push(' ' + (nG === 1 ? 'Einen Eintrag startet ein Panel; er steht' : nG + ' Einträge startet ein Panel; sie stehen') + ' einzeln in der Liste, mit eigenem Verbrauch.');
      // Projekte ohne Dienst (Job erledigt, angelegt, nur Panel): nicht
      // „alle laufen“ verschweigen, sondern in einem Satz dazu.
      if (rest.length) h.push(' Dazu ' + rest.map(x => x.name + ' (' + liveStatus(x.live).textContent + ')').join(', ') + '.');
      // Nach einem Umzug sind Einmal-Jobs nur angelegt.
      if (rest.some(x => x.live.created)) h.push(' Angelegte Container startet HostDeck nach einem Umzug nicht von selbst, sonst liefen Einmal-Jobs doppelt.');
    } else {
      const okN = m.projects.filter(p => p.ok).length;
      const bad = m.projects.filter(p => p.total > 0 && !p.ok);
      h = !m.projects.length ? ['Noch keine Projekte gefunden.'] : bad.length
        ? [el('span', { class: 'ls-dot err', 'aria-hidden': 'true' }), okN + ' von ' + plural(m.projects.length, 'Projekt', 'Projekten') + ' liefen rund. Bei ', em('err', bad.map(p => p.name).join(' und ')), bad.length === 1 ? ' stand ein Container.' : ' standen Container.']
        : [el('span', { class: 'ls-dot ok', 'aria-hidden': 'true' }), 'Alle ' + plural(m.projects.length, 'Projekt', 'Projekte') + ' liefen rund.'];
    }
    // Kennzahlen: Container aus Sicht von HostDeck.
    let strip = null;
    if (lv && op.rep.summe) {
      // Zahlen aus der Summe des Dienstes (eine Quelle, I10).
      const sm = op.rep.summe;
      const own = sm.gesamt - sm.jobs;
      const loops = lv.filter(c => !c.verwaltet_von && looping(c));
      const runOk = sm.laufen - sm.unhealthy;
      const off = sm.von_dir + sm.letzter_lauf + sm.angelegt + sm.unerwartet;
      const nP = all.filter(x => x.kind === 'p' && x.live && x.live.total).length, nC = all.filter(x => x.kind === 'c').length;
      const nG = all.filter(x => x.kind === 'g' && x.c).length;
      strip = el('div', { class: 'ls-kpis' },
        kpi({ l: 'Container', v: String(own), s: 'in ' + plural(nP, 'Projekt', 'Projekten') + (nC ? ' und ' + nC + ' einzeln' : '') + (nG ? ', dazu ' + plural(nG, 'verwalteter Server', 'verwaltete Server') : ''), spark: false }),
        kpi({ l: el('span', { class: 'ls-st ok' }, 'Container laufen'), v: String(runOk), s: runOk === own ? 'alle ohne Auffälligkeit' : 'ohne Auffälligkeit', spark: false }),
        kpi({ l: el('span', { class: 'ls-st ' + (loops.length ? 'err' : 'idle') }, 'Neustart-Schleife'), v: String(loops.length), kind: loops.length ? 'err' : '', s: loops.length ? loops.map(c => c.name).join(', ') : 'keine', spark: false }),
        kpi({ l: el('span', { class: 'ls-st ' + (sm.unerwartet ? 'err' : 'idle') }, 'Aus'), v: String(off), kind: sm.unerwartet ? 'err' : '', s: off ? stopWhy(sm) : 'nichts angehalten', spark: false }));
    }
    const body = el('div', {});
    // Auswahl für mehrere auf einmal: nur, was live da ist und HostDeck steuern darf.
    const selable = new Map();
    if (svc && lv) for (const x of all) if (x.live && x.live.total) selable.set(x.key, { x, target: (x.kind === 'p' ? 'projekt:' : 'container:') + x.name, name: x.name });
    for (const k of [...ui.psel]) if (!selable.has(k)) ui.psel.delete(k);
    const selCol = selable.size > 1;
    if (!selCol) ui.psel.clear();
    const selAll = selCol ? el('input', { type: 'checkbox', class: 'ls-selall', 'data-fid': 'psel-all', disabled: !!op.busy, 'aria-label': 'Alle sichtbaren auswählen',
      onchange: e => { for (const tr of tbody.children) { const x = all[Number(tr.dataset.i)]; if (!tr.hidden && !tr.classList.contains('sub') && selable.has(x.key)) { if (e.target.checked) ui.psel.add(x.key); else ui.psel.delete(x.key); } } core.rerender(); } }) : null;
    // Spalte, Filter und Knopf zum Umzug nur, wenn einer ansteht (Idee 1).
    const mvCol = !m.alltag;
    const filt = PF.filter(f => f[0] === 'alle' || ((mvCol || f[0] === 'dich') && all.some(x => f[2](x))));
    if (!filt.some(f => f[0] === ui.pf)) ui.pf = 'alle';
    const tbody = el('tbody', {});
    const count = el('span', { class: 'ls-mut' });
    const empty = !all.length
      ? el('div', { class: 'ls-nomatch', hidden: true }, ic('box', 22), el('span', {}, 'Noch keine Projekte oder Container.'))
      : el('div', { class: 'ls-nomatch', hidden: true }, ic('search', 22), el('span', {}, 'Kein Projekt passt zu Filter und Suche.'),
        el('button', { class: 'btn btn-sm', type: 'button', onclick: () => { ui.pf = 'alle'; ui.pq = ''; core.rerender(); } }, 'Filter zurücksetzen'));
    const apply = () => {
      const f = PF.find(x => x[0] === ui.pf);
      const q = ui.pq.trim().toLowerCase();
      let n = 0;
      for (const tr of tbody.children) {
        const x = all[Number(tr.dataset.i)];
        const show = f[2](x) && (!q || x.text.includes(q));
        tr.hidden = !show;
        if (show && !tr.classList.contains('sub')) n++;
      }
      count.textContent = n === all.length ? plural(all.length, 'Eintrag', 'Einträge') : n + ' von ' + all.length;
      empty.hidden = n > 0;
      if (selAll) {
        const vis = [...tbody.children].filter(tr => !tr.hidden && !tr.classList.contains('sub')).map(tr => all[Number(tr.dataset.i)].key).filter(k => selable.has(k));
        const on = vis.filter(k => ui.psel.has(k)).length;
        selAll.checked = vis.length > 0 && on === vis.length;
        selAll.indeterminate = on > 0 && on < vis.length;
        selAll.disabled = !!op.busy || !vis.length;
      }
      for (const b of chips.querySelectorAll('button')) b.setAttribute('aria-pressed', b.dataset.pf === ui.pf ? 'true' : 'false');
    };
    const chips = el('div', { class: 'ls-chips', role: 'group', 'aria-label': 'Filter' }, filt.map(([id, label, fn]) =>
      el('button', { type: 'button', class: 'ls-chip', 'data-pf': id, 'aria-pressed': id === ui.pf ? 'true' : 'false', onclick: () => { ui.pf = id; apply(); } },
        label, el('span', { class: 'n' }, String(all.filter(fn).length)))));
    const search = el('label', { class: 'ls-field' }, ic('search', 16), el('span', { class: 'sr' }, 'Projekt, Container oder Domain suchen'),
      el('input', { type: 'search', value: ui.pq, placeholder: 'Projekt, Container oder Domain', autocomplete: 'off', 'data-fid': 'p-search', oninput: e => { ui.pq = e.target.value; apply(); } }));
    // Sortieren: Klick auf eine Spaltenüberschrift, noch einmal dreht um.
    if (!lv && (ui.ps === 'cpu' || ui.ps === 'mem')) ui.ps = 'st';
    const order = all.map((x, i) => [x, i]).sort((a, b) => PSORT[ui.ps](a[0], b[0]) * ui.pdir || a[0].name.localeCompare(b[0].name, 'de'));
    // Zahlen stehen zuerst absteigend (größte oben), Namen aufsteigend.
    const th = (k, label, cls) => {
      const on = ui.ps === k;
      const asc = (k === 'name' || k === 'st') === (ui.pdir > 0);
      return el('th', { scope: 'col', class: cls || null, 'aria-sort': on ? (asc ? 'ascending' : 'descending') : null },
        el('button', { type: 'button', class: 'ls-sort' + (on ? ' on' : ''), 'data-fid': 'ps-' + k, onclick: () => { if (ui.ps === k) ui.pdir = -ui.pdir; else { ui.ps = k; ui.pdir = 1; } core.rerender(); } },
          label, ic(on ? (asc ? 'sortup' : 'sortdown') : 'sort', 13)));
    };
    const scale = Math.max(100, ...all.map(x => (x.live && x.live.cpu) || 0));
    // Wie viele Server startet ein Panel-Projekt? Steht in seiner Zeile; die
    // Server selbst stehen einzeln darunter in der Liste.
    const starts = new Map();
    for (const x of all) if (x.kind === 'g') starts.set(x.panelProject, (starts.get(x.panelProject) || 0) + 1);
    // Server eines Panels stehen eingerückt direkt unter ihrem Panel und
    // wandern beim Sortieren mit; fehlt das Panel in der
    // Liste, stehen sie für sich.
    const panels = new Set(order.filter(([x]) => x.kind === 'p' && starts.get(x.name)).map(([x]) => x.name));
    const nested = [];
    for (const pair of order) {
      const x = pair[0];
      if (x.kind === 'g' && panels.has(x.panelProject)) continue;
      nested.push(pair);
      if (x.kind === 'p' && panels.has(x.name)) for (const q of order) if (q[0].kind === 'g' && q[0].panelProject === x.name) nested.push(q);
    }
    for (const [x, i] of nested) {
      const g = x.kind === 'g' ? x : null;
      const kid = !!(g && panels.has(g.panelProject));
      const href = g ? gHref(x) : '#/projekte/' + encodeURIComponent(x.key);
      const s = x.live;
      const target = (x.kind === 'p' ? 'projekt:' : 'container:') + x.name;
      const cs = lv && !g ? liveFor(x) : null;
      const canOpen = !!(cs && cs.length && x.kind === 'p');
      const open = canOpen && ui.pexp.has(x.key);
      const quick = s && s.total ? (s.run > 0 ? 'neustarten' : 'starten') : null;
      const loopC = cs ? cs.filter(looping) : [];
      const note = loopC.length ? plural(loopC.reduce((a, c) => a + (c.neustarts || 0), 0), 'Neustart', 'Neustarts') : cs && cs.length === 1 ? since(cs[0]) : g && g.c ? [g.c.grund, since(g.c)].filter(Boolean).join(' · ') : '';
      const subline = g ? [g.game ? g.game.spiel : 'Container', 'gestartet vom Panel ' + g.panelProject].join(' · ')
        : [x.kind === 'p' ? plural(s ? s.gesamt : x.o.services.length, 'Container', 'Container') : 'einzeln'].concat(x.kind === 'p' && starts.get(x.name) ? ['startet ' + plural(starts.get(x.name), 'Server', 'Server')] : [], x.domains.slice(0, 2)).join(' · ') + (x.domains.length > 2 ? ' …' : '');
      const picked = ui.psel.has(x.key);
      const tr = el('tr', { class: 'click' + (open ? ' open' : '') + (picked ? ' picked' : '') + (kid ? ' kid' : ''), 'data-i': String(i), 'data-projekt': x.kind === 'p' ? x.name : null, 'data-gruppe': s && s.schluessel ? s.schluessel : null },
        el('td', { class: 'c-name' }, el('div', { class: 'nm' },
          selCol ? (selable.has(x.key) ? el('label', { class: 'ls-sel' }, el('input', { type: 'checkbox', checked: picked, disabled: !!op.busy, 'data-fid': 'psel-' + x.key, 'aria-label': x.name + ' auswählen',
            onchange: e => { if (e.target.checked) ui.psel.add(x.key); else ui.psel.delete(x.key); core.rerender(); } })) : el('span', { class: 'ls-sel' })) : null,
          canOpen ? el('button', { type: 'button', class: 'ls-chev', 'data-fid': 'pe-' + x.key, 'aria-expanded': String(open), 'aria-label': 'Container von ' + x.name + (open ? ' zuklappen' : ' aufklappen'),
            onclick: e => { e.stopPropagation(); if (open) ui.pexp.delete(x.key); else ui.pexp.add(x.key); core.rerender(); } }, ic(open ? 'down' : 'right', 16)) : cols ? el('span', { class: 'ls-chev-sp' }) : null,
          el('div', {}, g ? el('span', { class: 'sr' }, pKindWord(x) + ': ') : null, el('a', { href, class: 'rowlink', title: g ? gFull(g.game) || g.cname : null }, x.name), g && g.game ? [' ', gsTag()] : null, x.kind === 'p' && upNewer(x.name) ? [' ', el('span', { class: 'tag t-new up-tag' }, 'Update')] : null,
            el('span', { class: 'sub' }, subline)))),
        el('td', { class: 'c-st' }, s ? liveStatus(s) : wait ? skel('skel-t') : g ? st(g.ok ? 'ok' : 'idle', g.ok ? 'läuft' : 'aus') : projStatus(x), note ? el('span', { class: 'sub' }, note) : null),
        cols ? el('td', { class: 'c-cpu' }, s ? (s.cpu != null ? cpuCell(s.cpu, scale) : usage(s)) : skel('skel-t')) : null,
        cols ? el('td', { class: 'c-mem r' }, s && s.cpu != null ? size(s.mem) : s ? '–' : skel('skel-t')) : null,
        el('td', { class: 'c-size r' }, x.size >= 0 ? size(x.size) : x.size === -1 ? 'unbekannt' : '–'),
        mvCol ? el('td', { class: 'c-mv' }, g ? null : tag(x.decision)) : null,
        cols ? el('td', { class: 'c-act r' }, g ? el('span', { class: 'ls-mut' }, 'nur Anzeige') : s ? el('div', { class: 'acts' },
          quick ? el('button', { type: 'button', class: 'ls-ibtn ls-rbtn', disabled: !!op.busy, 'data-fid': 'op-' + quick + '-' + target, 'aria-label': x.name + ' ' + OP_VERB[quick] + ' …', title: OP_BTN[quick] + ' …',
            onclick: e => { e.stopPropagation(); opRun(target, quick); } }, ic(OP_ICON[quick], 17)) : null,
          rowMenu(x, target, s)) : skel('skel-b')) : el('td', { class: 'c-go', 'aria-hidden': 'true' }, ic('right', 16)));
      tr.addEventListener('click', e => { if (!e.target.closest('a, button, input, .ls-sel, .menu')) location.hash = href; });
      tbody.append(tr);
      if (open) for (const c of cs) {
        const ct = 'container:' + c.name;
        const sub = el('tr', { class: 'sub', 'data-i': String(i) },
          el('td', { class: 'c-name' }, el('div', { class: 'nm' }, selCol ? el('span', { class: 'ls-sel' }) : null, el('span', { class: 'ls-chev-sp' }), el('div', {}, el('span', { class: 'mono cn' }, c.name), el('span', { class: 'sub mono' }, c.image)))),
          el('td', { class: 'c-st' }, cState(c), el('span', { class: 'sub' }, looping(c) ? plural(c.neustarts, 'Neustart', 'Neustarts') + (c.exit_code ? ' · Code ' + c.exit_code : '') : since(c))),
          el('td', { class: 'c-cpu' }, c.cpu != null ? cpuCell(c.cpu, scale) : '–'),
          el('td', { class: 'c-mem r' }, c.cpu != null ? size(c.speicher || 0) : '–'),
          el('td', { class: 'c-size r' }),
          mvCol ? el('td', { class: 'c-mv' }) : null,
          el('td', { class: 'c-act r' }, el('div', { class: 'acts' },
            el('a', { class: 'btn btn-sm btn-g', href: pvHref(x, 'logs'), onclick: () => { ui.want = { key: x.key, name: c.name }; } }, ic('doc', 15), 'Logs'),
            c.verwaltet_von ? null : el('button', { type: 'button', class: 'ls-ibtn ls-rbtn', disabled: !!op.busy, 'aria-label': c.name + ' ' + OP_VERB[c.laeuft ? 'neustarten' : 'starten'] + ' …', title: OP_BTN[c.laeuft ? 'neustarten' : 'starten'] + ' …',
              onclick: e => { e.stopPropagation(); opRun(ct, c.laeuft ? 'neustarten' : 'starten'); } }, ic(c.laeuft ? 'refresh' : 'play', 16)))));
        tbody.append(sub);
      }
    }
    const known = new Set(all.map(x => x.kind === 'p' ? 'p:' + x.name : 'c:' + x.name));
    const fresh = lv ? lv.filter(c => !c.verwaltet_von && !known.has(c.projekt ? 'p:' + c.projekt : 'c:' + c.name)) : [];
    let selBar = null;
    if (selCol && (ui.psel.size || op.prog)) {
      const items = [...selable.values()].filter(it => ui.psel.has(it.x.key)).sort((a, b) => order.findIndex(o => o[0] === a.x) - order.findIndex(o => o[0] === b.x));
      const any = f => items.some(it => f(it.x.live));
      const act = (a, can) => el('button', { type: 'button', class: 'btn btn-sm', 'data-fid': 'psel-' + a, disabled: !!op.busy || !can,
        onclick: () => opRunMany(items, a) }, ic(OP_ICON[a], 16), OP_BTN[a] + ' …');
      const pg = op.prog;
      selBar = el('div', { class: 'ls-selbar', role: 'region', 'aria-label': 'Auswahl' },
        el('span', { class: 'n', 'aria-live': 'polite' }, pg ? [el('span', { class: 'spin', 'aria-hidden': 'true' }), pg.what + ' ' + pg.i + ' von ' + pg.n + (pg.name ? ' · ' + pg.name : '')]
          : plural(items.length, 'ausgewählt', 'ausgewählt')),
        el('div', { class: 'acts' }, act('neustarten', any(s => s.run > 0)), act('starten', any(s => s.run < s.total)), act('stoppen', any(s => s.run > 0)),
          el('button', { type: 'button', class: 'btn btn-sm btn-g', 'data-fid': 'psel-clear', disabled: !!op.busy, onclick: () => { ui.psel.clear(); core.rerender(); const f = document.querySelector('[data-fid="p-search"]'); if (f) f.focus({ preventScroll: true }); } }, ic('x', 15), 'Auswahl aufheben')));
    }
    body.append(
      el('div', { class: 'ls-pbar' }, chips, search),
      selBar || '',
      el('div', { class: 'ls-tw' }, el('table', { class: 'ls-t ls-pt' + (cols ? ' is-live' : '') + (selCol ? ' is-sel' : ''), 'aria-busy': wait ? 'true' : null },
        el('caption', { class: 'sr' }, 'Projekte und Container'),
        el('thead', {}, el('tr', {}, selAll ? (() => { const t = th('name', 'Projekt', 'c-name'); t.prepend(el('label', { class: 'ls-sel' }, selAll)); t.classList.add('has-sel'); return t; })() : th('name', 'Projekt', 'c-name'), th('st', 'Zustand', 'c-st'),
          cols ? th('cpu', 'Prozessor', 'c-cpu') : null, cols ? th('mem', 'Arbeitsspeicher', 'c-mem r') : null,
          th('size', 'Größe', 'c-size r'), mvCol ? el('th', { scope: 'col', class: 'c-mv' }, 'Umzug') : null,
          el('th', { scope: 'col', class: cols ? 'c-act' : 'c-go' }, el('span', { class: 'sr' }, cols ? 'Aktionen' : 'Öffnen')))),
        tbody)),
      empty,
      el('div', { class: 'ls-tfoot' }, count, el('span', { class: 'ls-mut' }, lv ? 'Live, Stand ' + stamp(op.rep.zeit) + ' · Prozessor als Anteil am ganzen Server · Größe aus der Bestandsaufnahme' : wait ? 'Zustand wird geladen …' : 'Größe: Ordner, Daten und mitgenommene Images')));
    apply();
    const lead = svc
      ? 'Ein Projekt ist ein Compose-Ordner mit seinen Containern. Zustand und Last kommen live aus Docker; „Neustart-Schleife“ heißt: läuft erst seit kurzem und ist schon mehrmals neu gestartet. Der Pfeil klappt die Container auf, ein Klick auf die Zeile öffnet alles zum Projekt. Vor jedem Starten, Stoppen oder Neustarten zeigt HostDeck, was passiert.'
      : 'Ein Projekt ist ein Compose-Ordner mit seinen Containern. Klick auf eine Zeile für Container, Domains und Daten. Starten und Stoppen geht, wenn HostDeck als Dienst auf dem Server läuft.';
    const out = [grp ? el('a', { class: 'pv-back', href: '#/projekte' }, ic('left', 16), 'Alle Projekte') : null,
    head({ i: 'projekte', k: (gName || 'Projekte') + (cols ? ' · live' : ' · Stand ' + fmtDate(m.s.created)), h, p: grp ? null : lead,
      a: grp || !mvCol ? null : link('#/auswahl/bereich/projekte', 'Umzug festlegen', 'btn btn-sm', 'auswahl') }),
    grp ? null : strip,
    opResult(),
    m.s.host.ohne_docker ? el('p', { class: 'band', role: 'status' }, 'Auf diesem Server ist Docker nicht installiert. Projekte und Container zeigt HostDeck, sobald es Docker gibt; die nächste Bestandsaufnahme findet sie dann.')
      : svc && op.error ? el('p', { class: 'band b-warn', role: 'status' }, 'Live-Stand nicht verfügbar: ' + op.error + ' Die Tabelle zeigt den Stand der Bestandsaufnahme.') : null,
    !grp && fresh.length ? el('p', { class: 'ls-note' }, ic('info', 16), el('span', {}, plural(fresh.length, 'Container läuft', 'Container laufen') + ', die die Bestandsaufnahme noch nicht kennt: ' +
      fresh.slice(0, 4).map(c => c.name).join(', ') + (fresh.length > 4 ? ' …' : '') + '. Neu erfassen nimmt sie auf.'), quickDiscover('btn btn-sm')) : null,
    sec(null, body, { cls: 'flush' })];
    return out;
  }

  // ---------- Projekt: eigene Seite ----------
  // Alles zu einem Projekt oder Container auf einer Seite mit Reitern. Der
  // Reiter steht in der Adresse (#/projekte/<schlüssel>/logs), so gehen
  // Zurück und Lesezeichen. Logs, Prozesse und Konfiguration liest HostDeck nur;
  // Starten, Stoppen und Neustarten zeigen wie überall erst die Vorschau.
  const PV_TABS = [['', 'Überblick'], ['logs', 'Logs'], ['verlauf', 'Verlauf'], ['prozesse', 'Prozesse'], ['konfiguration', 'Konfiguration'], ['ereignisse', 'Ereignisse']];
  const pv = { tab: '', cname: '', range: '1h', hist: null, histKey: '', loading: false, at: 0, error: '', det: {}, cause: null };
  function pvSplit(all, key) {
    if (!key) return null;
    let x = all.find(y => y.key === key);
    if (x) return { x, tab: '' };
    const i = key.lastIndexOf('/');
    const tab = i > 0 ? key.slice(i + 1) : '';
    if (!tab || !PV_TABS.some(t => t[0] === tab)) return null;
    x = all.find(y => y.key === key.slice(0, i));
    return x ? { x, tab } : null;
  }
  function pvHref(x, tab) { return '#/projekte/' + encodeURIComponent(x.key) + (tab ? '/' + tab : ''); }
  // Markierter Text in den Logs: dann nicht neu zeichnen, sonst ist die
  // Auswahl weg, bevor du kopieren kannst.
  function selecting() {
    const s = window.getSelection && window.getSelection();
    return !!(s && !s.isCollapsed && s.anchorNode && s.anchorNode.parentElement && s.anchorNode.parentElement.closest('#page'));
  }
  const EXIT_WHY = {
    1: 'Das Programm hat sich mit einem Fehler beendet.', 2: 'Das Programm wurde falsch aufgerufen.',
    125: 'Docker konnte den Container nicht anlegen.', 126: 'Der Startbefehl ist nicht ausführbar.', 127: 'Den Startbefehl gibt es im Image nicht.',
    134: 'Das Programm hat abgebrochen (SIGABRT).', 137: 'Er wurde hart beendet (SIGKILL), oft weil der Speicher nicht reichte.',
    139: 'Das Programm ist abgestürzt (Speicherzugriffsfehler).', 143: 'Er wurde regulär beendet (SIGTERM).',
  };
  // Der Container, der gerade Ärger macht, und in Worten, was los ist.
  function pvProblem(lv) {
    const own = lv.filter(c => !c.verwaltet_von && !c.job);
    let c = own.find(looping), kind = 'loop';
    if (!c) { c = own.find(y => !y.laeuft && !y.von_dir_gestoppt && !y.letzter_lauf && y.zustand !== 'created'); kind = 'down'; }
    if (!c) { c = own.find(y => y.laeuft && y.gesundheit === 'unhealthy'); kind = 'sick'; }
    if (!c) return null;
    const code = c.exit_code ? 'zuletzt mit Code ' + c.exit_code : '';
    const why = c.oom ? 'Der Kernel hat ihn beendet, weil der Speicher nicht reichte (OOM).' : EXIT_WHY[c.exit_code] || '';
    if (kind === 'loop') return { c, kind, t: (c.dienst || c.name) + ' startet immer wieder neu', d: [plural(c.neustarts, 'Neustart', 'Neustarts') + ' durch Docker' + (code ? ', ' + code : '') + '.', why].filter(Boolean).join(' ') };
    if (kind === 'down') return { c, kind, t: (c.dienst || c.name) + ' ist aus, ohne dass du ihn gestoppt hast', d: [(code ? 'Beendet ' + code + ' ' : 'Beendet ') + (ago(c.seit) || '').replace(/^seit /, 'vor ') + '.', why].filter(Boolean).join(' ').replace(' .', '.') };
    return { c, kind, t: (c.dienst || c.name) + ' läuft, meldet aber „unhealthy“', d: 'Sein eigener Gesundheits-Check schlägt fehl. Die Logs sagen meist, woran es hängt.' };
  }
  async function pvLoadCause(name) {
    pv.cause = { name, lines: null };
    const r = await core.call('/api/container/logs?name=' + encodeURIComponent(name) + '&zeilen=200', null, true);
    if (!pv.cause || pv.cause.name !== name) return;
    if (r.ok) {
      const er = new Set(r.data.stderr || []);
      const all = (r.data.zeilen || []).map((l, i) => [l, logLevel(l) === 'e']).filter(z => z[0].trim());
      const bad = all.filter(z => z[1]);
      const se = all.filter((z, i) => er.has(i));
      pv.cause.lines = (bad.length ? bad : se.length ? se : all).slice(-4);
      pv.cause.err = bad.length > 0;
    } else pv.cause.lines = [];
    if (ui.page === 'projekte' && core.work() && !modalOpen() && !selecting()) core.rerender();
  }
  function pvCause(x, lv) {
    const pr = pvProblem(lv);
    if (!pr) { pv.cause = null; return null; }
    if (!pv.cause || pv.cause.name !== pr.c.name) setTimeout(() => pvLoadCause(pr.c.name), 0);
    const cs = pv.cause && pv.cause.name === pr.c.name ? pv.cause : null;
    const ct = 'container:' + pr.c.name;
    const act = pr.c.verwaltet_von ? null : pr.kind === 'loop' ? opButton(ct, 'stoppen', false, 'Schleife anhalten') : pr.kind === 'down' ? opButton(ct, 'starten') : opButton(ct, 'neustarten');
    const lines = cs && cs.lines && cs.lines.length ? el('div', { class: 'pv-cl' },
      el('span', { class: 'pv-cl-h' }, cs.err ? 'Letzte Fehlerzeilen' : 'Letzte Zeilen'),
      el('pre', { class: 'pv-cl-p' }, cs.lines.map(z => el('span', { class: z[1] ? 'e' : null }, logLine(z[0]) + '\n')))) :
      cs && cs.lines === null ? el('div', { class: 'pv-cl', 'aria-busy': 'true' }, skel('skel-t skel-w')) : null;
    return el('section', { class: 'pv-cause' + (pr.kind === 'sick' ? ' warn' : ''), 'aria-labelledby': 'pv-cause-t' },
      el('span', { class: 'pv-cause-ic', 'aria-hidden': 'true' }, ic('warn', 20)),
      el('div', { class: 'pv-cause-b' },
        el('h2', { id: 'pv-cause-t' }, pr.t),
        el('p', {}, pr.d),
        lines,
        el('div', { class: 'op-btns' }, act,
          el('a', { class: 'btn btn-sm', href: pvHref(x, 'logs'), onclick: () => { ui.want = { key: x.key, name: pr.c.name }; } }, ic('doc', 16), 'Alle Logs'))));
  }
  // Kennzahlen des Projekts; Linien aus dem Verlauf, wenn es ihn gibt.
  function pvSum(names, h, kind) {
    if (!h) return null;
    const n = h.zeiten.length, out = new Array(n).fill(null);
    for (const nm of names) {
      const v = h.serien['c/' + nm + '/' + kind];
      if (v) v.forEach((x, i) => { if (x != null) out[i] = (out[i] || 0) + x; });
    }
    return out;
  }
  function pvKpis(x, lv, sum) {
    const names = lv.map(c => c.name);
    const h = pv.hist && pv.histKey.startsWith(x.key + '|') ? pv.hist : null;
    const rl = RANGE_LONG[pv.range] || '';
    const cpu = pvSum(names, h, 'cpu'), ram = pvSum(names, h, 'ram');
    const tc = h ? uwTotal(h, names, 'cpu', x.kind === 'p' ? x.name : '') : null;
    const restarts = lv.reduce((a, c) => a + (c.neustarts || 0), 0);
    const limit = lv.length && lv.every(c => memLimit(c)) ? lv.reduce((a, c) => a + memLimit(c), 0) : 0;
    const [mv, mu] = splitUnit(size(sum.mem || 0));
    const cs = tc && tc.any ? uwTotalStats(tc, h) : null;
    return el('div', { class: 'ls-kpis pv-kpis' },
      !sum.total
        // Job oder nur angelegt: „0 von 0 laufen“ sagt nichts.
        ? kpi({ l: 'Container', v: String(lv.filter(c => !c.verwaltet_von).length), s: liveStatus(sum).textContent, spark: false })
        : kpi({ l: 'Container', v: String(sum.run), u: 'von ' + sum.total, s: sum.restarting ? plural(lv.filter(looping).length, 'in Neustart-Schleife', 'in Neustart-Schleife') : sum.run === sum.total ? 'alle laufen' : stopWhy(sum), kind: sum.unexpected || sum.restarting ? 'err' : '', spark: false }),
      // Mittel und Spitze nennen ihren Zeitraum (Datenprüfung M4); groß steht
      // der Wert jetzt.
      kpi({ l: 'Prozessor jetzt', v: sum.cpu != null ? splitUnit(cpuText(sum.cpu))[0] : '–', u: sum.cpu != null ? splitUnit(cpuText(sum.cpu))[1] : '', s: cs ? 'Mittel ' + (RANGE_OF[pv.range] ? RANGE_OF[pv.range] + ' ' : '') + cpuText(cs.avg) + ' · Spitze ' + (cs.up ? 'bis zu ' : '') + cpuText(cs.peak) : 'Anteil am ganzen Server', spark: cpu ? kSpark(cpu, h, 'Prozessor, ' + rl) : null }),
      kpi({ l: 'Arbeitsspeicher jetzt', v: sum.cpu != null ? mv : '–', u: sum.cpu != null ? mu : '', s: limit ? 'Grenze ' + size(limit) : 'ohne feste Grenze', spark: ram ? kSpark(ram, h, 'Arbeitsspeicher, ' + rl, limit || null) : null }),
      kpi({ l: 'Neustarts', v: String(restarts), s: restarts ? 'durch Docker' : 'keine durch Docker', kind: lv.some(looping) ? 'err' : '', spark: false }),
      kpi({ l: 'Größe', v: x.size >= 0 ? splitUnit(size(x.size))[0] : '–', u: x.size >= 0 ? splitUnit(size(x.size))[1] : '', s: x.size >= 0 ? 'Ordner, Daten, Images' : 'aus der Bestandsaufnahme', spark: false }));
  }
  // Docker nennt ohne feste Grenze den ganzen Speicher des Servers als
  // Grenze; das ist keine.
  function memLimit(c) {
    const all = mon.rep && mon.rep.info ? mon.rep.info.ram_gesamt : 0;
    return c.speicher_grenze > 0 && (!all || c.speicher_grenze < all * 0.97) ? c.speicher_grenze : 0;
  }
  function opIcon(target, action, name) {
    return el('button', { type: 'button', class: 'ls-ibtn ls-rbtn', disabled: !!op.busy, 'data-fid': 'op-' + action + '-' + target, 'aria-label': name + ' ' + OP_VERB[action] + ' …', title: OP_BTN[action] + ' …',
      onclick: e => { e.stopPropagation(); opRun(target, action); } }, ic(OP_ICON[action], 16));
  }
  // Container des Projekts als Tabelle: live mit Last und Knöpfen, sonst
  // aus der Bestandsaufnahme.
  function pvContainers(x, lv, wait) {
    const p = x.o;
    if (wait) return sec('Container', el('div', { class: 'ls-stack', 'aria-busy': 'true' }, skel('skel-t'), skel('skel-t skel-w'), el('span', { class: 'sr' }, 'Container werden geladen.')), { cls: 'c12' });
    if (lv) {
      if (!lv.length) return sec('Container', el('p', { class: 'ls-mut' }, 'Docker kennt gerade keinen Container dazu.'), { cls: 'c12' });
      const scale = Math.max(100, ...lv.map(c => c.cpu || 0));
      const rows = lv.map(c => {
        const ct = 'container:' + c.name;
        return el('tr', {},
          el('td', { class: 'c-name' }, el('b', {}, c.dienst || c.name), el('span', { class: 'sub mono' }, (c.dienst ? c.name + ' · ' : '') + c.image)),
          el('td', { class: 'c-st' }, cState(c), el('span', { class: 'sub' }, [looping(c) || c.neustarts ? plural(c.neustarts, 'Neustart', 'Neustarts') : since(c), c.gesundheit === 'unhealthy' ? '„unhealthy“' : c.gesundheit === 'healthy' ? 'gesund' : '', c.oom ? 'zuletzt OOM' : ''].filter(Boolean).join(' · '))),
          el('td', { class: 'c-cpu' }, c.cpu != null ? cpuCell(c.cpu, scale) : '–'),
          el('td', { class: 'c-mem r' }, c.cpu != null ? size(c.speicher || 0) : '–', memLimit(c) ? el('span', { class: 'sub' }, 'von ' + size(memLimit(c))) : null),
          el('td', { class: 'c-act r' }, el('div', { class: 'acts' },
            el('a', { class: 'btn btn-sm btn-g', href: pvHref(x, 'logs'), onclick: () => { ui.want = { key: x.key, name: c.name }; } }, ic('doc', 15), 'Logs'),
            c.verwaltet_von ? el('span', { class: 'ls-mut', title: 'Steuern geht nur im Panel' }, 'Panel') : [
              c.laeuft ? opIcon(ct, 'neustarten', c.name) : opIcon(ct, 'starten', c.name),
              c.laeuft ? opIcon(ct, 'stoppen', c.name) : null])));
      });
      return sec('Container', el('div', { class: 'ls-tw' }, el('table', { class: 'ls-t pv-ct' },
        el('caption', { class: 'sr' }, 'Container von ' + x.name),
        el('thead', {}, el('tr', {}, el('th', { scope: 'col', class: 'c-name' }, 'Dienst'), el('th', { scope: 'col', class: 'c-st' }, 'Zustand'), el('th', { scope: 'col', class: 'c-cpu' }, 'Prozessor'),
          el('th', { scope: 'col', class: 'c-mem r' }, 'Arbeitsspeicher'), el('th', { scope: 'col', class: 'c-act' }, el('span', { class: 'sr' }, 'Aktionen')))),
        el('tbody', {}, rows))), { cls: 'c12 flush', sub: 'live, Stand ' + stamp(op.rep.zeit) });
    }
    const list = x.kind === 'p' ? p.services.map(s => [s.name, s.image_name || '', s.running, s.origin === 'gebaut' ? 'selbst gebaut' : s.origin === 'registry' ? 'aus Registry' : '']) : [[p.name, p.image || '', p.running, '']];
    return sec('Container', el('div', { class: 'ls-tw' }, el('table', { class: 'ls-t pv-ct' },
      el('caption', { class: 'sr' }, 'Container von ' + x.name),
      el('thead', {}, el('tr', {}, el('th', { scope: 'col' }, 'Dienst'), el('th', { scope: 'col' }, 'Zustand'), el('th', { scope: 'col' }, 'Herkunft'))),
      el('tbody', {}, list.map(([n, img, run, orig]) => el('tr', {},
        el('td', {}, el('b', {}, n), el('span', { class: 'sub mono' }, img)), el('td', {}, st(run ? 'ok' : 'idle', run ? 'lief' : 'gestoppt')), el('td', { class: 'ls-mut' }, orig || '–')))))),
    { cls: 'c12 flush', sub: 'Stand der Bestandsaufnahme' });
  }
  function pvOverview(m, x, lv, wait) {
    const p = x.o;
    const kv = (a, b) => el('div', { class: 'ls-kv' }, el('span', {}, a), el('span', {}, b));
    const routes = x.kind === 'p' ? p.routes.map(r => el('div', { class: 'ls-row' },
      el('div', {}, el('a', { class: 'mono', href: 'https://' + r.hosts[0], target: '_blank', rel: 'noopener noreferrer' }, r.hosts.join(', ')), el('span', { class: 'ls-mut mono' }, (r.upstream || '') + (r.target ? ' → ' + r.target : ''))),
      el('div', { class: 'ta-r' }, routeSt(r)))) : [];
    const data = (p.data || []).filter(d => !d.split_of).map(d => el('div', { class: 'ls-row' },
      el('div', {}, el('b', {}, dataLabel(d)), el('span', { class: 'mono ls-mut' }, d.label)), el('div', { class: 'ta-r' }, el('b', {}, size(d.size)))));
    // Server, die das Panel startet: Zustand und Verbrauch jetzt wie bei
    // jedem Container und wie in der Projektliste, wo jeder einzeln steht.
    const games = gameList(mon.rep);
    const lvAll = live();
    const managed = (p.managed || []).map(s => {
      const gm = games.find(g => g.container === s.name || g.id === s.name);
      const ls = lvAll ? gLive(lvAll.find(c => c.name === s.name), gm) : null;
      const use = ls && ls.cpu != null ? cpuText(ls.cpu) + ' · ' + size(ls.mem) : '';
      return el('div', { class: 'ls-row' },
        el('div', {}, el('b', { title: s.name }, gm ? gName(gm) : s.label || shortName(s.name), gm ? gsTag() : null), el('span', { class: 'ls-mut' }, (gm ? gm.spiel + ' · ' : '') + 'verwalteter Server' + (s.manager ? ' · ' + s.manager : ''))),
        el('div', { class: 'ta-r' }, ls ? liveStatus(ls) : st(s.running ? 'ok' : 'idle', s.running ? 'lief' : 'aus'), el('span', {}, [use, size(s.size)].filter(Boolean).join(' · '))));
    });
    const folder = x.kind === 'p' ? [kv('Ordner', el('span', { class: 'mono' }, p.path + (p.dir_exists ? '' : ' (fehlt)')))].concat(p.git ? [kv('Git', el('span', { class: 'mono' }, (p.git.remote || 'ohne Remote') + (p.git.branch ? ' · ' + p.git.branch : '') + (p.git.dirty ? ' · ' + p.git.dirty + ' nicht committet' : '')))] : []) : [];
    const card = (title, body, sub) => body && body.length ? sec(title, el('div', { class: 'pv-rows' }, body), { cls: 'c6', sub }) : null;
    return [pvContainers(x, lv, wait),
      card('Domains', routes, routes.length ? plural(routes.length, 'Weiterleitung', 'Weiterleitungen') + ' in Caddy' : ''),
      verBox(x),
      card('Daten', data, data.length ? size((p.data || []).filter(d => !d.split_of).reduce((a, d) => a + Math.max(0, d.size || 0), 0)) + ' zusammen' : ''),
      card('Verwaltete Server', managed),
      card('Ordner', folder),
      isAlltag() ? null : sec('Umzug', el('div', { class: 'pv-rows' },
        el('p', {}, 'Entscheidung: ', tag(x.decision), p.reason ? ' · ' + p.reason : ''),
        el('div', {}, link('#/auswahl/bereich/projekte/' + encodeURIComponent(p.key), 'In der Auswahl ändern', 'btn btn-sm', 'auswahl'))), { cls: 'c6' })];
  }
  // Auswahl des Containers für Logs, Prozesse und Konfiguration.
  function pvPick(lv, cur, onPick, label) {
    if (lv.length < 2) return null;
    return el('div', { class: 'ls-chips pv-pick', role: 'group', 'aria-label': label || 'Container' }, lv.map(c =>
      el('button', { type: 'button', class: 'ls-chip', 'data-fid': 'pv-c-' + c.name, 'aria-pressed': String(c.name === cur), onclick: () => onPick(c.name) },
        el('span', { class: 'ls-dot ' + (c.stufe === 'idle' ? '' : c.stufe || ''), 'aria-hidden': 'true' }), c.dienst || c.name)));
  }
  // Stufe einer Logzeile aus ihrem Text: e Fehler, w Warnung. Viele
  // Programme schreiben alles auf stderr; das allein ist kein Fehler.
  const LV_E = /\b(error|err|fatal|panic|crit|critical|emerg|exception|traceback|fehler)\b/i, LV_W = /\b(warn|warning|warnung)\b/i;
  function logLevel(l) { return LV_E.test(l) ? 'e' : LV_W.test(l) ? 'w' : ''; }
  function pvMark(text, q) {
    if (!q) return text;
    const out = [], low = text.toLowerCase();
    let i = 0, j;
    while ((j = low.indexOf(q, i)) >= 0) { if (j > i) out.push(text.slice(i, j)); out.push(el('mark', {}, text.slice(j, j + q.length))); i = j + q.length; }
    if (i < text.length) out.push(text.slice(i));
    return out;
  }
  function pvLogs(x, lv) {
    const lg = op.lg;
    const names = lv.map(c => c.name);
    if (!names.includes(lg.name)) { lg.name = (lv.find(c => looping(c)) || lv.find(c => c.laeuft) || lv[0] || {}).name || ''; lg.lines = null; lg.error = ''; }
    if (lg.lines === null && !lg.loading && lg.name) setTimeout(loadLogs, 0);
    const pick = n => { lg.name = n; lg.lines = null; lg.error = ''; lg.end = true; core.rerender(); };
    const q = lg.q.trim().toLowerCase();
    const er = new Set(lg.err || []);
    const all = lg.lines || [];
    const shown = [];
    const lvs = all.map(logLevel);
    all.forEach((l, i) => { if ((!lg.only || (lg.only === 'err' ? er.has(i) : lvs[i])) && (!q || l.toLowerCase().includes(q))) shown.push(i); });
    const two = n => String(n).padStart(2, '0');
    const body = lg.lines === null ? [el('span', { class: 'ls-mut' }, 'Lädt …')] : !all.length ? [el('span', { class: 'ls-mut' }, 'Dieser Container hat noch nichts ausgegeben.')] :
      !shown.length ? [el('span', { class: 'ls-mut' }, q ? 'Keine Zeile passt zur Suche.' : lg.only === 'err' ? 'Keine Zeile kam auf stderr.' : 'Keine Fehler oder Warnungen in diesen Zeilen.')] :
        shown.map(i => {
          const l = all[i];
          const mt = /^(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(\.\d+)?Z /.exec(l);
          const d = mt ? new Date(mt[1] + 'Z') : null;
          const t = d && !isNaN(d) ? two(d.getDate()) + '.' + two(d.getMonth() + 1) + '. ' + d.toLocaleTimeString('de-DE') : '';
          return el('span', { class: 'ln' + (t ? ' ht' : '') + (er.has(i) ? ' se' : '') + (lvs[i] ? ' lv-' + lvs[i] : '') }, t ? el('span', { class: 't' }, t) : null, pvMark(t ? l.slice(mt[0].length) : l, q), '\n');
        });
    const pre = el('pre', {
      class: 'pv-log' + (lg.wrap ? '' : ' nowrap'), id: 'op-log', tabindex: '0', 'aria-label': 'Logs von ' + lg.name,
      onscroll: e => { const y = e.target; lg.end = y.scrollHeight - y.scrollTop - y.clientHeight < 40; lg.top = y.scrollTop; },
    }, body);
    requestAnimationFrame(() => { if (pre.isConnected) pre.scrollTop = lg.end ? pre.scrollHeight : lg.top; });
    const save = () => {
      const text = shown.map(i => all[i]).join('\n') + '\n';
      const a = el('a', { href: URL.createObjectURL(new Blob([text], { type: 'text/plain;charset=utf-8' })), download: lg.name + '-logs-' + new Date().toISOString().slice(0, 16).replace(/[T:]/g, '-') + '.txt' });
      document.body.append(a); a.click(); a.remove();
      setTimeout(() => URL.revokeObjectURL(a.href), 4000);
    };
    const tog = (on, fid, label, fn) => el('button', { type: 'button', class: 'pv-tog', 'data-fid': fid, 'aria-pressed': String(!!on), onclick: fn }, label);
    const nSel = el('label', { class: 'pv-sel' }, el('span', {}, 'Zeilen'), el('select', { 'data-fid': 'lg-n', onchange: e => { lg.n = Number(e.target.value); lg.end = true; loadLogs(); } },
      [200, 1000, 2000].map(n => el('option', { value: String(n), selected: lg.n === n }, String(n)))));
    return sec(null, el('div', { class: 'pv-logw' },
      pvPick(lv, lg.name, pick),
      el('div', { class: 'pv-bar' },
        el('label', { class: 'ls-field pv-q' }, ic('search', 16), el('span', { class: 'sr' }, 'In den Logs suchen'),
          el('input', { type: 'search', value: lg.q, placeholder: 'In den Logs suchen', autocomplete: 'off', 'data-fid': 'lg-q', oninput: e => { lg.q = e.target.value; core.rerender(); } })),
        el('div', { class: 'pv-tools' },
          el('div', { class: 'ls-seg', role: 'group', 'aria-label': 'Zeilen zeigen' }, [['', 'Alle'], ['lv', 'Fehler und Warnungen'], ['err', 'stderr']].map(([id, t]) =>
            el('button', { type: 'button', 'data-fid': 'lg-only-' + id, 'aria-pressed': String((lg.only || '') === id), onclick: () => { lg.only = id; core.rerender(); } }, t))),
          tog(lg.wrap, 'lg-wrap', 'Umbrechen', () => { lg.wrap = !lg.wrap; core.rerender(); }),
          tog(lg.follow, 'lg-follow', 'Mitlaufen', () => { lg.follow = !lg.follow; lg.end = true; clearTimeout(lg.timer); if (lg.follow) loadLogs(); else core.rerender(); }),
          nSel,
          el('button', { type: 'button', class: 'ls-ibtn', 'data-fid': 'lg-ref', disabled: lg.loading, 'aria-label': 'Logs neu laden', title: 'Neu laden', onclick: () => { lg.end = true; loadLogs(); } }, ic('refresh', 17)),
          el('button', { type: 'button', class: 'ls-ibtn', 'data-fid': 'lg-dl', disabled: !shown.length, 'aria-label': 'Angezeigte Zeilen herunterladen', title: 'Herunterladen', onclick: save }, ic('download', 17)))),
      lg.error ? el('p', { class: 'band b-err', role: 'status' }, lg.error) : null,
      pre,
      el('p', { class: 'ls-mut pv-foot' }, (q || lg.only ? shown.length + ' von ' + all.length + ' Zeilen · ' : '') + (lg.stamp ? 'Stand ' + stamp(lg.stamp) + (lg.follow ? ', läuft mit' : '') + ' · ' : '') + 'die letzten ' + lg.n + ' Zeilen' + (lg.cut ? ', der Anfang ist gekürzt' : '') +
        (er.size ? ' · Strich links: kam auf stderr' : '') + (lvs.some(Boolean) ? ' · rot Fehler, gelb Warnung' : '') + '. HostDeck zeigt Logs nur an; sie landen nicht im Protokoll.')), { cls: 'c12' });
  }
  async function pvLoadHist(x, names) {
    if (pv.loading) return;
    pv.loading = true;
    const rng = pv.range, key = x.key + '|' + rng;
    const want = names.map(n => 'c/' + n + '/').concat(x.kind === 'p' ? ['p/' + x.name + '/'] : []);
    const r = await core.call('/api/ueberwachung/verlauf?zeitraum=' + rng + '&punkte=360&spitzen=1&serien=' + encodeURIComponent(want.join(',')), null, true);
    pv.loading = false;
    pv.at = Date.now();
    if (r.ok) { pv.hist = r.data; pv.histKey = key; pv.error = ''; } else pv.error = r.status === 404 ? 'Die Überwachung läuft nicht.' : (r.data && r.data.error) || 'Fehler ' + r.status;
    if (rng !== pv.range) { pvLoadHist(x, names); return; }
    if (ui.page === 'projekte' && core.work() && !modalOpen() && !uw.hover && !selecting()) core.rerender();
  }
  // Reihen für ein Diagramm: bis drei Container einzeln, sonst die zwei
  // größten und der Rest zusammen.
  function pvSeries(lv, h, kind) {
    const S = n => h.serien['c/' + n + '/' + kind] || null;
    const P = n => h.spitzen ? h.spitzen['c/' + n + '/' + kind] || null : null;
    const one = (c, i) => ({ label: c.dienst || c.name, v: S(c.name), p: P(c.name), k: uwK(h, 'c/' + c.name + '/' + kind), slot: i + 1 });
    const cs = lv.filter(c => S(c.name));
    if (cs.length <= 3) return cs.map(one);
    const avg = c => (uwK(h, 'c/' + c.name + '/' + kind) || uwStats(S(c.name), h.zeiten) || { avg: 0 }).avg;
    const top = [...cs].sort((a, b) => avg(b) - avg(a));
    // „übrige“ hat keine eigene Messung: Summe, Spitze als Obergrenze.
    const rest = uwTotal(h, top.slice(2).map(c => c.name), kind);
    return top.slice(0, 2).map(one).concat([{ label: 'übrige ' + (cs.length - 2), v: rest.v, p: rest.p, up: rest.up, since: rest.since, slot: 3 }]);
  }
  function pvVerlauf(x, lv) {
    const h = pv.hist && pv.histKey === x.key + '|' + pv.range ? pv.hist : null;
    const pick = id => { pv.range = id; pvLoadHist(x, lv.map(c => c.name)); core.rerender(); };
    const seg = el('div', { class: 'ls-seg', role: 'group', 'aria-label': 'Zeitraum' }, RANGES.map(([id, t]) =>
      el('button', { type: 'button', 'data-fid': 'pv-r-' + id, 'aria-pressed': String(id === pv.range), onclick: () => pick(id) }, t)));
    const bar = el('div', { class: 'pv-vbar' }, seg, el('span', { class: 'ls-mut' }, pv.loading ? 'lädt …' : h ? 'Stand ' + stamp(new Date(pv.at).toISOString()) : ''));
    if (!h) return [bar, sec(null, pv.error ? el('p', { class: 'band b-err', role: 'status' }, 'Der Verlauf ist nicht erreichbar: ' + pv.error) : el('div', { class: 'ls-stack', 'aria-busy': 'true' }, skel('skel-h'), skel('skel-t skel-w')), { cls: 'c12' })];
    const kerne = mon.rep && mon.rep.info ? mon.rep.info.kerne : 0;
    const limit = lv.length === 1 ? memLimit(lv[0]) || null : null;
    const net = [['ein', 1], ['aus', 2]].map(([kind, slot]) => {
      const t = uwTotal(h, lv.map(c => c.name), kind, x.kind === 'p' ? x.name : '');
      // „rein/raus“ wie überall; „aus“ hieße sonst auch ausgeschaltet.
      return { label: kind === 'ein' ? 'rein' : 'raus', v: t.v, p: t.p, up: t.up, k: t.k, since: t.since, slot };
    });
    const empty = 'Für diesen Zeitraum hat die Überwachung noch keine Werte zu diesem Projekt.';
    return [bar,
      uwChart({ marks: false, id: 'pv-cpu', hint: true, title: 'Prozessor', sub: 'Anteil am ganzen Server' + (kerne ? ' (alle ' + plural(kerne, 'Kern', 'Kerne') + ' = 100 %)' : ''), unit: 'pct', hist: h, range: pv.range, noZoom: true, empty, series: pvSeries(lv, h, 'cpu') }),
      uwChart({ marks: false, id: 'pv-ram', title: 'Arbeitsspeicher', sub: limit ? 'Grenze ' + size(limit) : 'ohne feste Grenze', unit: 'bytes', max: limit, hist: h, range: pv.range, noZoom: true, empty, series: pvSeries(lv, h, 'ram') }),
      uwChart({ marks: false, id: 'pv-net', title: 'Netz', sub: 'alle Container zusammen', unit: 'rate', hist: h, range: pv.range, noZoom: true, empty, series: net })];
  }
  async function pvLoadDet(name) {
    const d = pv.det[name] = pv.det[name] || {};
    if (d.loading) return;
    d.loading = true;
    const r = await core.call('/api/container/details?name=' + encodeURIComponent(name), null, true);
    d.loading = false;
    d.at = Date.now();
    if (r.ok) { d.data = r.data; d.error = ''; } else d.error = (r.data && r.data.error) || 'Die Einzelheiten ließen sich nicht laden (Fehler ' + r.status + ').';
    if (ui.page === 'projekte' && core.work() && !modalOpen() && !selecting()) core.rerender();
  }
  function pvDet(lv, maxAge) {
    if (!lv.some(c => c.name === pv.cname)) pv.cname = (lv.find(c => looping(c)) || lv.find(c => c.laeuft) || lv[0] || {}).name || '';
    const d = pv.det[pv.cname] || {};
    if (pv.cname && !d.loading && (!d.at || Date.now() - d.at > maxAge)) setTimeout(() => pvLoadDet(pv.cname), 0);
    return d;
  }
  const PROC_COLS = { PID: ['PID', 'r'], USER: ['Nutzer', ''], UID: ['Nutzer', ''], '%CPU': ['Prozessor', 'r'], RSS: ['Arbeitsspeicher', 'r'], ELAPSED: ['Läuft seit', 'r'], COMMAND: ['Programm', ''], CMD: ['Programm', ''] };
  function pvElapsed(s) {
    const m = /^(?:(\d+)-)?(?:(\d+):)?(\d+):(\d+)$/.exec(s || '');
    if (!m) return s;
    const d = +(m[1] || 0), h = +(m[2] || 0), mi = +m[3];
    return d ? d + ' T ' + h + ' Std' : h ? h + ' Std ' + mi + ' Min' : mi ? mi + ' Min' : (+m[4]) + ' s';
  }
  function pvProzesse(lv) {
    const d = pvDet(lv, 10000);
    if (!mon.rep && !mon.loading && !mon.at) setTimeout(loadMon, 0);
    const c = lv.find(y => y.name === pv.cname);
    const pick = n => { pv.cname = n; core.rerender(); };
    let body;
    if (!c) body = el('p', { class: 'ls-mut' }, 'Kein Container.');
    else if (!c.laeuft) body = el('div', { class: 'ls-empty' }, ic('stop', 22), el('div', {}, el('b', {}, (c.dienst || c.name) + ' läuft gerade nicht.'), el('p', {}, 'Prozesse gibt es nur in einem laufenden Container.')));
    else if (d.error) body = el('p', { class: 'band b-err', role: 'status' }, d.error);
    // Ohne Kernzahl stünde der Prozessor je Kern da; erst warten.
    else if (!d.data || (core.svc() && !(mon.rep && mon.rep.info) && !mon.at)) body = el('div', { class: 'ls-stack', 'aria-busy': 'true' }, skel('skel-t'), skel('skel-t skel-w'));
    else if (!d.data.prozesse) body = el('p', { class: 'band b-warn', role: 'status' }, d.data.prozesse_fehler || 'Docker nennt die Prozesse nicht.');
    else {
      const pr = d.data.prozesse;
      const cols = pr.titel.map(t => PROC_COLS[t] || [t, '']);
      const ci = pr.titel.indexOf('%CPU'), ri = pr.titel.indexOf('RSS'), ei = pr.titel.indexOf('ELAPSED');
      const num = v => Number(String(v).replace(',', '.')) || 0;
      const rows = [...pr.zeilen].sort((a, b) => ci >= 0 ? num(b[ci]) - num(a[ci]) : 0);
      const ram = ri >= 0 ? rows.reduce((a, r) => a + num(r[ri]) * 1024, 0) : 0;
      // ps rechnet in Prozent eines Kerns; HostDeck zeigt den Anteil am Server.
      const kerne = mon.rep && mon.rep.info ? mon.rep.info.kerne : 0;
      body = [el('div', { class: 'ls-tw' }, el('table', { class: 'ls-t pv-pt' },
        el('caption', { class: 'sr' }, 'Prozesse in ' + c.name),
        el('thead', {}, el('tr', {}, cols.map(([t, cl]) => el('th', { scope: 'col', class: cl || null }, t)))),
        el('tbody', {}, rows.map(r => el('tr', {}, r.map((v, i) => el('td', { class: (cols[i] && cols[i][1]) || null },
          i === ci ? cpuText(kerne > 1 ? num(v) / kerne : num(v)) : i === ri ? size(num(v) * 1024) : i === ei ? pvElapsed(v) : cols[i] && cols[i][0] === 'Programm' ? el('span', { class: 'mono' }, v) : v))))))),
      el('p', { class: 'ls-mut pv-foot' }, plural(rows.length, 'Prozess', 'Prozesse') + (ram ? ', zusammen ' + size(ram) + ' Arbeitsspeicher' : '') + (kerne > 1 ? ' · Prozessor als Anteil am ganzen Server' : '') + ' · Stand ' + stamp(d.data.zeit) + ', frischt sich alle 15 Sekunden auf. Die Befehlszeile zeigt HostDeck nicht: darin stehen manchmal Passwörter.')];
    }
    return sec(null, el('div', { class: 'ls-stack sm' }, pvPick(lv, pv.cname, pick), body), { cls: 'c12 pv-procs' });
  }
  const RP_TEXT = { always: 'immer neu starten', 'unless-stopped': 'neu starten, außer von Hand gestoppt', 'on-failure': 'nur nach einem Fehler neu starten', no: 'nie neu starten', '': 'nie neu starten' };
  function netMode(m) {
    if (m === 'host') return 'Netz des Servers (host): jeder offene Port ist von außen erreichbar, wenn die Firewall ihn lässt';
    if (m === 'none') return 'kein Netz';
    if (m && m.startsWith('container:')) return 'teilt das Netz von ' + m.slice(10);
    return m === 'bridge' || m === 'default' ? 'Standard (bridge)' : '';
  }
  function pvKonfig(lv) {
    const d = pvDet(lv, 60000);
    const pick = n => { pv.cname = n; core.rerender(); };
    const head = pvPick(lv, pv.cname, pick);
    if (d.error) return [head, sec(null, el('p', { class: 'band b-err', role: 'status' }, d.error), { cls: 'c12' })];
    if (!d.data) return [head, sec(null, el('div', { class: 'ls-stack', 'aria-busy': 'true' }, skel('skel-t'), skel('skel-t skel-w')), { cls: 'c12' })];
    const k = d.data.konfig;
    const kv = (a, b) => b == null || b === '' ? null : el('div', { class: 'ls-kv' }, el('span', {}, a), el('span', {}, b));
    const tm = s => { const t = Date.parse(s); return t && new Date(t).getFullYear() > 1 ? new Date(t).toLocaleString('de-DE') : ''; };
    const co = k.compose || {};
    const general = [kv('Image', el('span', { class: 'mono' }, k.image)), kv('Neustart', RP_TEXT[k.neustart_regel || ''] || k.neustart_regel),
      kv('Speichergrenze', k.speicher_grenze ? size(k.speicher_grenze) : 'keine'), k.privilegiert ? kv('Rechte', el('span', { class: 'tag t-open' }, 'privilegiert: darf fast alles am Server')) : null,
      kv('Gestartet', tm(k.gestartet)), kv('Zuletzt beendet', tm(k.beendet)),
      kv('Compose', co.projekt ? co.projekt + (co.dienst ? ' · Dienst ' + co.dienst : '') : ''), kv('Ordner', co.ordner ? el('span', { class: 'mono' }, co.ordner) : ''), kv('Datei', co.dateien ? el('span', { class: 'mono' }, co.dateien) : '')].filter(Boolean);
    const ports = (k.ports || []).map(p => el('tr', {}, el('td', { class: 'mono' }, p.container), el('td', { class: 'mono' }, p.host_port ? (p.host_ip && p.host_ip !== '0.0.0.0' && p.host_ip !== '::' ? p.host_ip + ':' : '') + p.host_port : '–'),
      el('td', {}, p.host_port ? (p.host_ip === '127.0.0.1' || p.host_ip === '::1' ? st('ok', 'nur dieser Server') : st('warn', 'von außen erreichbar')) : st('idle', 'nicht veröffentlicht'))));
    const mounts = (k.mounts || []).map(mt => el('tr', {}, el('td', { class: 'mono' }, mt.ziel), el('td', { class: 'mono' }, mt.quelle), el('td', {}, mt.typ === 'volume' ? 'Volume' : mt.typ === 'bind' ? 'Ordner' : mt.typ), el('td', {}, mt.schreibbar ? 'lesen und schreiben' : 'nur lesen')));
    const table = (cap, hs, rows) => el('div', { class: 'ls-tw' }, el('table', { class: 'ls-t pv-kt' }, el('caption', { class: 'sr' }, cap), el('thead', {}, el('tr', {}, hs.map(t => el('th', { scope: 'col' }, t)))), el('tbody', {}, rows)));
    const opts = Object.entries(k.log_optionen || {}).map(([a, b]) => a + '=' + b).join(', ');
    return [head,
      sec('Allgemein', el('div', { class: 'pv-rows' }, general), { cls: 'c6' }),
      sec('Netz', el('div', { class: 'pv-rows' }, kv('Modus', netMode(k.netzwerk_modus)), kv('Netzwerke', (k.netzwerke || []).length ? el('span', { class: 'pv-tags' }, k.netzwerke.map(n => el('span', { class: 'tag t-mut mono' }, n))) : 'keine'),
        kv('Logs', (k.log_treiber || 'Standard') + (opts ? ' · ' + opts : ''))), { cls: 'c6' }),
      sec('Ports', ports.length ? table('Ports', ['Im Container', 'Am Server', 'Erreichbar'], ports) : el('p', { class: 'ls-mut' }, 'Keine Ports veröffentlicht; erreichbar nur über Docker-Netze' + (k.netzwerk_modus === 'host' ? ' (Netz des Servers: alle Ports des Programms sind offen).' : '.')), { cls: 'c12' + (ports.length ? ' flush' : '') }),
      sec('Ordner und Volumes', mounts.length ? table('Ordner und Volumes', ['Im Container', 'Quelle', 'Art', 'Zugriff'], mounts) : el('p', { class: 'ls-mut' }, 'Nichts eingehängt: Daten gehen beim Neu-Anlegen verloren.'), { cls: 'c12' + (mounts.length ? ' flush' : '') }),
      sec('Umgebung', el('div', { class: 'pv-rows' },
        (k.umgebung || []).length ? el('span', { class: 'pv-tags' }, k.umgebung.map(n => el('span', { class: 'tag t-mut mono' }, n))) : el('p', { class: 'ls-mut' }, 'Keine Umgebungsvariablen.'),
        el('p', { class: 'ls-mut pv-foot' }, ic('lock', 14), ' Nur die Namen. Die Werte liest HostDeck nie, darin stehen oft Passwörter.')), { cls: 'c12', sub: (k.umgebung || []).length ? plural(k.umgebung.length, 'Variable', 'Variablen') : '' })];
  }
  function pvEreignisse(x, lv) {
    if (!mon.loading && (!mon.at || Date.now() - mon.at > 30000)) setTimeout(loadMon, 0);
    const w = mon.rep && mon.rep.warnungen;
    if (!w) return sec(null, mon.error ? el('p', { class: 'band b-err', role: 'status' }, mon.error) : el('div', { class: 'ls-stack', 'aria-busy': 'true' }, skel('skel-t'), skel('skel-t skel-w')), { cls: 'c12' });
    const mine = new Set(lv.map(c => c.name).concat(x.domains));
    const ours = k => { const i = (k || '').indexOf(':'); return i > 0 && mine.has(k.slice(i + 1)); };
    const act = w.aktiv.filter(a => ours(a.schluessel));
    const ev = w.verlauf.filter(e => ours(e.schluessel)).slice(0, 40);
    const now = act.length ? sec('Läuft gerade', el('ol', { class: 'ls-att' }, act.map(a => el('li', { class: 'ls-att-i ' + (a.so_gelassen ? 'kept' : a.gut_seit ? 'acc' : a.stufe) },
      el('span', { class: 'ls-att-ic' }, ic(a.so_gelassen ? 'eyeoff' : a.gut_seit ? 'done' : a.symbol, 18)), el('div', { class: 'ls-att-t' }, el('b', {}, a.titel), el('span', {}, a.text + ' Seit ' + when(a.seit) + '.' + (a.gut_seit ? ' Seit ' + when(a.gut_seit) + ' wieder in Ordnung; die Entwarnung kommt nach 10 Minuten.' : '') + (a.so_gelassen ? ' Bewusst so gelassen seit ' + when(a.so_gelassen) + '.' : ''))),
      el('div', { class: 'ls-att-a' }, a.so_gelassen ? el('button', { type: 'button', class: 'btn btn-sm btn-g', 'data-fid': 'unkeep-' + a.schluessel, disabled: !!hn.busy, onclick: () => keepAlert(a.schluessel, false, a.titel) }, ic('undo', 16), 'Wieder warnen')
        : a.so_lassen && !a.gut_seit ? keepBtn(a.schluessel, a.titel) : null)))), { cls: 'c12 flush' }) : null;
    const list = ev.length ? el('ul', { class: 'uw-ev' }, ev.map(e => el('li', {},
      el('time', { datetime: e.zeit }, when(e.zeit)), el('span', {}, st(e.stufe === 'err' && e.art !== 'entwarnung' ? 'err' : (EV[e.art] || ['', 'idle'])[1], (EV[e.art] || [e.art])[0])),
      el('span', { class: 't' }, e.titel.replace(/^Wieder in Ordnung: /, ''), e.text ? el('small', {}, e.text) : null)))) :
      el('div', { class: 'ls-empty ok' }, ic('done', 24), el('div', {}, el('b', {}, 'Keine Warnung in den letzten 30 Tagen.'), el('p', {}, 'Hier steht jede Warnung zu diesen Containern und Domains: Absturz, Neustart-Schleife, „unhealthy“, Seite antwortet nicht, Zertifikat.')));
    return [now, sec('Warnungen, 30 Tage', list, { cls: 'c12', sub: ev.length ? plural(ev.length, 'Ereignis', 'Ereignisse') : '', end: link('#/protokoll', 'Protokoll', 'btn btn-sm btn-g') })];
  }
  function pageProjekt(m, x, tab) {
    const svc = !!core.svc();
    if (op.key !== x.key) {
      op.key = x.key; op.result = null;
      Object.assign(op.lg, { name: '', lines: null, err: [], error: '', follow: false, n: 200, end: true, q: '', only: '' });
      clearTimeout(op.lg.timer);
      Object.assign(pv, { cname: '', det: {}, cause: null, hist: null, histKey: '', error: '' });
    }
    if (ui.want && ui.want.key === x.key && ui.want.name) {
      if (op.lg.name !== ui.want.name) { op.lg.name = ui.want.name; op.lg.lines = null; op.lg.end = true; }
      pv.cname = ui.want.name;
    }
    ui.want = null;
    if (!svc) tab = '';
    if (tab !== 'logs') clearTimeout(op.lg.timer);
    pv.tab = tab;
    if (svc && !mon.loading && (!mon.at || Date.now() - mon.at > 60000)) setTimeout(loadMon, 0);
    const lv = liveFor(x);
    const sum = lv ? liveSum(lv) : null;
    const wait = svc && !lv && !op.error;
    const target = (x.kind === 'p' ? 'projekt:' : 'container:') + x.name;
    if (lv && lv.length && (!pv.histKey.startsWith(x.key + '|') || Date.now() - pv.at > (pv.range === '15m' || pv.range === '1h' ? 30000 : 120000)) && !pv.loading && !pv.error) setTimeout(() => pvLoadHist(x, lv.map(c => c.name)), 0);
    const acts = sum ? opActs(target, sum) : null;
    const dom = x.domains[0];
    const facts = [sum ? liveStatus(sum) : wait ? skel('skel-t') : projStatus(x),
      el('span', { class: 'ls-mut' }, x.kind === 'p' ? plural(lv ? lv.length : x.o.services.length, 'Container', 'Container') : 'Container ohne Projekt'),
      dom ? el('a', { class: 'pv-dom', href: 'https://' + dom, target: '_blank', rel: 'noopener noreferrer' }, dom, x.domains.length > 1 ? el('span', { class: 'ls-mut' }, ' +' + (x.domains.length - 1)) : null) : null,
      tag(x.decision), x.kind === 'p' && upNewer(x.name) ? el('span', { class: 'tag t-new' }, 'Update da') : null];
    const hd = el('header', { class: 'pv-head' },
      el('a', { class: 'pv-back', href: '#/projekte' }, ic('left', 16), 'Projekte'),
      el('div', { class: 'pv-hrow' },
        el('div', { class: 'pv-id' }, el('span', { class: 'pv-ic', 'aria-hidden': 'true' }, ic('projekte', 22)),
          el('div', { class: 'pv-t' }, el('h1', { id: 'ls-dt', tabindex: '-1', 'data-fid': 'pv-h1' }, x.name), el('div', { class: 'pv-facts' }, facts.filter(Boolean)))),
        acts || wait ? el('div', { class: 'pv-acts op-btns' }, wait ? [skel('skel-b'), skel('skel-b')] : acts) : sum && sum.panel ? el('p', { class: 'ls-mut' }, 'Diese Container legt das Panel an; steuern geht nur dort.') : null));
    const tabs = svc ? el('nav', { class: 'ls-subtabs pv-tabs', 'aria-label': 'Ansicht von ' + x.name }, PV_TABS.map(([id, label]) =>
      el('a', { href: pvHref(x, id), 'aria-current': id === tab ? 'page' : null, 'data-fid': 'pv-t-' + id }, label))) : null;
    const live = lv && lv.length ? lv : null;
    let panel;
    if (!tab) panel = [live ? pvKpis(x, live, sum) : null, live ? pvCause(x, live) : null, ...pvOverview(m, x, lv, wait)];
    else if (!live) panel = [sec(null, wait ? el('div', { class: 'ls-stack', 'aria-busy': 'true' }, skel('skel-h'), skel('skel-t skel-w')) : el('p', { class: 'ls-mut' }, op.error ? 'Live-Stand nicht verfügbar: ' + op.error : 'Docker kennt gerade keinen Container dazu.'), { cls: 'c12' })];
    else if (tab === 'logs') panel = [pvLogs(x, live)];
    else if (tab === 'verlauf') panel = pvVerlauf(x, live);
    else if (tab === 'prozesse') panel = [pvProzesse(live)];
    else if (tab === 'konfiguration') panel = pvKonfig(live);
    else panel = pvEreignisse(x, live);
    return [hd, tabs, opResult(),
      svc && op.error ? el('p', { class: 'band b-warn', role: 'status' }, 'Live-Stand nicht verfügbar: ' + op.error) : null,
      el('div', { class: 'ls-grid eq pv-grid' }, ...[].concat(panel).filter(Boolean))];
  }

  // ---------- Domains ----------
  // Live aus Caddy: einmal ordnen (jede Domain eine Datei in sites/), dann
  // anlegen, Ziel ändern, ab- und anschalten. Jede Änderung zeigt erst die
  // Vorschau; der Dienst prüft mit Caddy, lädt neu, vergleicht alle Seiten
  // mit vorher und legt sonst alles zurück.
  const dm = { rep: null, job: null, at: 0, loading: false, timer: 0, error: '', result: null, busy: '', host: '', target: '', edit: null };
  const DM_KIND = { eigen: 'eigene Datei', aus: 'abgeschaltet', caddyfile: 'im Caddyfile', eingebunden: 'eingebunden', leitstand: 'Leitstand' };
  async function loadDm() {
    if (dm.loading || !core.svc()) return;
    dm.loading = true;
    const r = await core.call('/api/domains', null, true);
    dm.loading = false;
    dm.at = Date.now();
    clearTimeout(dm.timer);
    dm.timer = 0;
    if (r.ok) {
      const was = dm.job && dm.job.laeuft;
      dm.rep = r.data.stand; dm.job = r.data.auftrag || null; dm.error = '';
      if (r.data.zustand) { setDz(r.data.zustand); dz.at = Date.now(); }
      if (was && dm.job && !dm.job.laeuft) dm.result = dm.job.fehler ? { ok: false, text: dm.job.fehler + (dm.job.wie_vorher ? ' Alles ist wie vorher.' : '') } : { ok: true, text: dm.job.ergebnis };
    } else if (r.status !== 404) dm.error = (r.data && r.data.error) || 'Fehler ' + r.status;
    const busy = (dm.job && dm.job.laeuft) || (dm.rep && dm.rep.pruefung_laeuft);
    if (busy && ui.page === 'domains') dm.timer = setTimeout(loadDm, 1500);
    if (ui.page === 'domains' && core.work() && !modalOpen()) core.rerender();
  }
  // Steht eine Domain in zwei Blöcken (etwa einmal mit Port), sagt die
  // Adresse aus dem Block, welcher welcher ist.
  function dmAddr(x) {
    const extra = (x.adressen || []).filter(a => !x.domains.includes(a));
    return extra.length ? el('span', { class: 'sub' }, extra.join(', ')) : null;
  }
  // Zustand je Domain, auch wenn ein Caddy-Block mehrere hat:
  // dann eine Zeile je Domain mit ihrem Namen.
  function dmLive(x) {
    const one = d => {
      const z = zOf([d]);
      if (!z || z.zustand === 'wird-geprueft') return dm.rep && dm.rep.pruefung_laeuft ? el('span', { class: 'skel skel-t', 'aria-label': 'wird geprüft' }) : st('idle', z ? z.wort : '–');
      const s = zSt(z, true);
      (Array.isArray(s) ? s[0] : s).dataset.zustand = z.zustand;
      return s;
    };
    if (x.domains.length === 1) return el('div', { 'data-domain': x.domains[0] }, one(x.domains[0]));
    return el('div', { class: 'dm-each' }, x.domains.map(d => el('div', { 'data-domain': d }, el('small', { class: 'dm-h' }, d), one(d))));
  }
  function dmUp(x) {
    if (x.domains.length === 1) return upBars(x.domains);
    return el('div', { class: 'dm-each' }, x.domains.map(d => el('div', {}, el('small', { class: 'dm-h' }, d), upBars([d]))));
  }
  // Zertifikat überall gleich (Idee 12): „noch 89 Tage“, darunter das Datum.
  function certWords(bis) {
    const d = new Date(bis);
    if (isNaN(d)) return null;
    const days = Math.floor((d - Date.now()) / 864e5);
    return { days, t: days < 0 ? 'abgelaufen' : 'noch ' + plural(days, 'Tag', 'Tage'), d: 'bis\u00a0' + String(d.getDate()).padStart(2, '0') + '.' + String(d.getMonth() + 1).padStart(2, '0') + '.' + d.getFullYear() };
  }
  // Gelb und rot nach Einstellungen › Grenzen, dieselben Grenzen wie
  // Zustand und Warnungen (Datenprüfung M9).
  function certLevel(bis) {
    const t = Date.parse(bis);
    if (isNaN(t)) return '';
    const g = limits() || {};
    const days = (t - Date.now()) / 864e5;
    return days < (g.zertifikat_rot_tage || 7) ? 'err' : days < (g.zertifikat_gelb_tage || 14) ? 'warn' : '';
  }
  function certCell(bis) {
    const c = bis && certWords(bis);
    if (!c) return '–';
    const k = certLevel(bis);
    return [k ? st(k, c.t) : el('span', { class: 'nowrap' }, c.t), el('span', { class: 'sub nowrap' }, c.d)];
  }
  function dmCert(x) {
    const lv = x.live;
    if (x.art === 'aus' || !lv) return '–';
    if (lv.zertifikat_fehler) return el('span', { class: 'ls-st err' }, lv.zertifikat_fehler);
    return certCell(lv.zertifikat_bis);
  }
  function dmActs(x) {
    const host = x.domains[0];
    const running = !!(dm.job && dm.job.laeuft) || !!dm.busy;
    const b = (act, text, long, icon) => el('button', { type: 'button', class: 'btn btn-sm btn-g', 'data-fid': 'dm-' + act + '-' + host, 'aria-label': long + ' …', title: long, disabled: running, onclick: () => dmChange({ aktion: act, domain: host }) }, ic(icon, 16), text);
    if (x.art === 'aus') return [b('an', 'An …', host + ' wieder anschalten', 'play')];
    if (x.art !== 'eigen') return [];
    return [x.einfach ? b('ziel', 'Ziel …', 'Ziel von ' + host + ' ändern', 'edit') : null, b('aus', 'Aus …', host + ' abschalten', 'stop')];
  }
  function dmFile(x) {
    const base = x.datei.split('/').pop();
    const where = x.art === 'eigen' ? 'sites/' + base : x.art === 'aus' ? 'sites/aus/' + base : base;
    return [el('span', { class: 'mono', title: x.datei }, where), el('span', { class: 'ls-mut blk' }, DM_KIND[x.art] || x.art, x.projekt ? ' · Projekt ' + x.projekt : ''),
      x.von_hand ? el('span', { class: 'tag t-open' }, 'von Hand geändert') : null];
  }
  function dmResult() {
    const r = dm.result;
    if (!r || (dm.job && dm.job.laeuft)) return null;
    return el('div', { class: 'ls-note op-res ' + (r.ok ? 'is-ok' : 'is-err'), role: 'status' }, ic(r.ok ? 'done' : 'warn', 16), el('span', {}, r.text),
      el('button', { type: 'button', class: 'ls-ibtn op-x', 'aria-label': 'Meldung schließen', onclick: () => { dm.result = null; core.rerender(); } }, ic('x', 16)));
  }
  // Vorschau für Ordnen (mit Auswahl der Vorschläge) und für eine Domain.
  function dmPreview(first, reorg) {
    return new Promise(resolve => {
      let close = null, p = first, busy = false;
      const done = ok => { close(); resolve(ok ? p : null); };
      const box = el('div', { class: 'gt-dlg op-dlg up-dlg', role: 'dialog', 'aria-modal': 'true', 'aria-labelledby': 'dm-h', 'aria-describedby': 'dm-d' });
      const title = reorg ? 'Caddy ordnen?' : { neu: p.domain + ' anlegen?', ziel: 'Ziel von ' + p.domain + ' ändern?', aus: p.domain + ' abschalten?', an: p.domain + ' wieder anschalten?' }[p.aktion];
      const draw = () => {
        const go = p.problem ? null : el('button', { type: 'button', class: 'btn btn-p', disabled: busy, onclick: () => done(true) }, ic(reorg ? 'domains' : 'done', 16), reorg ? 'Ordnen' : 'Ausführen');
        const files = reorg ? (p.dateien || []).map(f => ({ pfad: f.pfad, hinweis: { neu: 'neu' + (f.domains ? ': ' + f.domains.join(', ') : ''), geaendert: 'geändert', aus: 'abgeschaltet: ' + (f.domains || []).join(', ') }[f.was], text: f.text })) :
          [p.vorher ? { pfad: p.datei, hinweis: 'vorher', text: p.vorher } : null, p.nachher ? { pfad: p.datei, hinweis: 'nachher', text: p.nachher } : null].filter(Boolean);
        box.replaceChildren(...[
          el('p', { class: 'ls-eyb' }, ic('eye', 16), el('span', {}, 'Vorschau · noch ist nichts passiert')),
          el('h2', { id: 'dm-h' }, title),
          reorg && (p.vorschlaege || []).length ? el('fieldset', { class: 'op-part up-pick' }, el('legend', {}, 'Vorschläge'),
            p.vorschlaege.map(c => el('label', { class: 'up-opt' },
              el('input', { type: 'checkbox', checked: c.gewaehlt, disabled: busy, onchange: e => pick(c.schluessel, e.target.checked) }),
              el('span', {}, el('b', {}, c.titel), el('span', { class: 'ls-mut blk' }, c.detail))))) : null,
          p.problem ? el('p', { id: 'dm-d', class: 'gt-lead' }, p.problem) : el('section', { class: 'op-part', id: 'dm-d' }, el('h3', {}, 'Was passiert'),
            el('ol', { class: 'op-list' }, (p.schritte || []).map(x => el('li', {}, x)))),
          !p.problem && reorg && (p.bleibt || []).length ? el('section', { class: 'op-part' }, el('h3', {}, 'Bleibt, wie es ist'),
            el('ul', { class: 'op-list' }, p.bleibt.map(x => el('li', { class: 'mono' }, x)))) : null,
          !p.problem && p.rueckweg ? el('dl', { class: 'op-kv' }, el('dt', {}, 'Rückweg'), el('dd', {}, p.rueckweg)) : null,
          ...(p.hinweise || []).map(h => el('p', { class: 'ls-note op-warn' }, ic('warn', 16), el('span', {}, h))),
          !p.problem && files.length ? el('details', { class: 'op-part dm-files' }, el('summary', {}, reorg ? 'Dateien ansehen (' + files.length + ')' : 'Datei ansehen'), filePreview({ dateien: files })) : null,
          el('div', { class: 'gt-acts' }, el('button', { type: 'button', class: 'btn', onclick: () => done(false) }, p.problem ? 'Schließen' : 'Abbrechen'), go)].filter(Boolean));
      };
      const pick = async (key, on) => {
        const o = Object.assign({ http3_aus: false, abschalten: [] }, p.auswahl);
        if (key === 'http3') o.http3_aus = on;
        else {
          const h = key.slice(4);
          o.abschalten = (o.abschalten || []).filter(x => x !== h).concat(on ? [h] : []);
        }
        busy = true; draw();
        const r = await core.call('/api/domains/ordnen/vorschau', { auswahl: o });
        busy = false;
        if (r.ok) p = r.data;
        draw();
      };
      draw();
      close = modal(box, () => done(false));
      (box.querySelector('.btn-p') || box.querySelector('.btn')).focus();
    });
  }
  async function dmRun(previewPath, body, runPath, reorg) {
    if (dm.busy || (dm.job && dm.job.laeuft)) return;
    dm.busy = 'vorschau'; dm.result = null;
    core.rerender();
    const r = await core.call(previewPath, body);
    dm.busy = '';
    if (!r.ok) { dm.result = { ok: false, text: (r.data && r.data.error) || 'Die Vorschau ging nicht (Fehler ' + r.status + ').' }; core.rerender(); return; }
    const p = await dmPreview(r.data, reorg);
    if (!p) { core.rerender(); return; }
    const a = await core.call(runPath, Object.assign({}, reorg ? { auswahl: p.auswahl } : body, { stand: p.stand }));
    if (!a.ok) {
      if (!(a.data && a.data.abgebrochen)) dm.result = { ok: false, text: (a.data && a.data.error) || 'Das ging nicht (Fehler ' + a.status + ').' };
      core.rerender();
      return;
    }
    dm.job = a.data.auftrag; dm.rep = a.data.stand;
    core.announce(dm.job.was + ' läuft');
    loadDm();
  }
  function dmReorg() { return dmRun('/api/domains/ordnen/vorschau', {}, '/api/domains/ordnen', true); }
  function dmChange(req) {
    if (req.aktion === 'ziel') {
      const x = dm.rep.seiten.find(s => s.domains.includes(req.domain));
      dm.edit = { domain: req.domain, ziel: x && x.ziele[0] ? x.ziele[0] : '' };
      core.rerender();
      return;
    }
    return dmRun('/api/domains/aendern/vorschau', req, '/api/domains/aendern', false);
  }
  function dmForm(kind) {
    const isNew = kind === 'neu';
    const e = dm.edit || {};
    const hostIn = isNew ? el('input', { id: 'dm-host', class: 'fld', type: 'text', inputmode: 'url', autocomplete: 'off', spellcheck: 'false', placeholder: 'app.example.de', value: dm.host, oninput: ev => { dm.host = ev.target.value; } }) : null;
    const tIn = el('input', { id: isNew ? 'dm-target' : 'dm-edit', class: 'fld', type: 'text', inputmode: 'text', autocomplete: 'off', spellcheck: 'false', placeholder: '3005 oder 127.0.0.1:3005', value: isNew ? dm.target : e.ziel, oninput: ev => { if (isNew) dm.target = ev.target.value; else e.ziel = ev.target.value; } });
    const running = !!(dm.job && dm.job.laeuft) || !!dm.busy;
    const submit = ev => {
      ev.preventDefault();
      if (isNew) dmRun('/api/domains/aendern/vorschau', { aktion: 'neu', domain: dm.host, ziel: dm.target }, '/api/domains/aendern', false);
      else { const req = { aktion: 'ziel', domain: e.domain, ziel: e.ziel }; dm.edit = null; dmRun('/api/domains/aendern/vorschau', req, '/api/domains/aendern', false); }
    };
    return el('form', { class: 'dm-form', onsubmit: submit },
      isNew ? el('label', { class: 'dm-f' }, el('span', {}, 'Domain'), hostIn) : el('p', { class: 'dm-f' }, 'Neues Ziel für ', el('b', { class: 'mono' }, e.domain)),
      el('label', { class: 'dm-f' }, el('span', {}, 'Ziel (Port oder Adresse:Port)'), tIn),
      el('div', { class: 'dm-fa' },
        el('button', { type: 'submit', class: 'btn btn-sm', disabled: running }, ic('eye', 16), 'Vorschau'),
        isNew ? null : el('button', { type: 'button', class: 'btn btn-sm btn-g', onclick: () => { dm.edit = null; core.rerender(); } }, 'Abbrechen')));
  }
  // Dauer in Worten, für Ausfälle („25 Min.“, „2 Std. 5 Min.“).
  function minsText(min) {
    const m = Math.max(1, Math.round(min));
    return m < 60 ? m + ' Min.' : Math.floor(m / 60) + ' Std.' + (m % 60 ? ' ' + (m % 60) + ' Min.' : '');
  }
  const ddmm = d => d.toLocaleDateString('de-DE', { day: '2-digit', month: '2-digit' });
  // Tagesbalken und Verfügbarkeit aus der Rechnung des Dienstes (eine
  // Quelle, V9, V11): Ausfälle und Pausen als Zeitspannen, in Tage des
  // Browsers geteilt. Pausen zählen weder als Ausfall noch als Messzeit;
  // der Prozentwert kommt fertig vom Dienst. Mit text: eine Zeile darunter
  // nennt die Tage mit Ausfall und die Pausen, sichtbar ohne Maus (6C).
  function upBars(hosts, o) {
    const r = mon.rep;
    const w = r && r.warnungen;
    if (!w) return mon.loading || !mon.at ? skel('skel-t') : '–';
    const scs = w.seiten.filter(sc => hosts.includes(sc.domain));
    if (!scs.length) return el('span', { class: 'ls-mut' }, 'noch nicht geprüft');
    const now = Date.now();
    const spans = l => (l || []).map(x => [Date.parse(x.von), x.bis ? Date.parse(x.bis) : now]).filter(x => x[0] < x[1]);
    const over = (list, a, b) => list.reduce((s, [x, y]) => s + Math.max(0, Math.min(b, y) - Math.max(a, x)), 0);
    const per = scs.map(sc => ({ outs: spans(sc.ausfall_zeiten), pauses: spans(sc.pausen), from: Date.parse(sc.verfuegbar_ab || sc.geprueft_seit) || now }));
    const from = Math.min(...per.map(p => p.from));
    const today = new Date(); today.setHours(0, 0, 0, 0);
    const N = 30;
    const downDays = [], pauseDays = [];
    const bars = [];
    for (let i = 0; i < N; i++) {
      const d0 = new Date(today); d0.setDate(d0.getDate() - (N - 1 - i));
      const d1 = new Date(d0); d1.setDate(d1.getDate() + 1);
      const a = d0.getTime(), b = Math.min(d1.getTime(), now);
      const label = d0.toLocaleDateString('de-DE', { weekday: 'short', day: '2-digit', month: '2-digit' });
      if (b <= Math.max(a, from)) { bars.push(el('i', { class: 'na', title: label + ': noch nicht gemessen' })); continue; }
      // Mehrere Domains (ein Block): je Tag der schlechteste Wert.
      let down = 0, paused = 0, span = 0;
      for (const p of per) {
        const x = Math.max(a, p.from);
        if (b <= x) continue;
        down = Math.max(down, over(p.outs, x, b));
        paused = Math.max(paused, over(p.pauses, x, b));
        span = Math.max(span, b - x);
      }
      const dm = down / 60000;
      if (dm) downDays.push(ddmm(d0) + ' ' + minsText(dm));
      if (paused) pauseDays.push(d0);
      const all = paused >= span - 60000;
      const cls = dm >= 30 ? 'err' : dm ? 'warn' : all ? 'pz' : 'ok';
      bars.push(el('i', { class: cls, title: label + ': ' + (dm ? minsText(dm) + ' ohne Antwort' : all ? 'pausiert, zählt nicht' : 'keine Warnung') + (paused && !all ? ', zeitweise pausiert' : '') }));
    }
    const vals = scs.map(sc => sc.verfuegbarkeit).filter(v => v != null);
    const up = vals.length ? Math.min(...vals) : null;
    const txt = up == null ? 'noch keine Messung' : (up >= 99.995 ? '100' : up.toFixed(2).replace('.', ',')) + '\u00a0%';
    const since = from > now - (N - 1) * 864e5 ? 'seit ' + ddmm(new Date(from)) : '30 Tage';
    const pausedNow = per.some(p => p.pauses.some(([, y]) => y === now));
    // Antwortet die Domain gerade nicht, zählt das erst ab der Warnung als
    // Ausfall; „kein Ausfall“ daneben läse sich falsch (Datenprüfung N3).
    const zNow = zOf(hosts);
    const badNow = zNow && (zNow.stufe === 'err' || zNow.stufe === 'warn');
    const notes = [downDays.length ? 'Ausfall ' + downDays.join(', ') : badNow ? 'noch kein Ausfall gezählt; das beginnt mit der Warnung' : 'kein Ausfall',
      pausedNow ? 'pausiert seit ' + ddmm(new Date(Math.max(...per.flatMap(p => p.pauses.filter(([, y]) => y === now).map(([x]) => Math.max(x, from)))))) + ', zählt nicht'
        : pauseDays.length ? plural(pauseDays.length, 'Tag', 'Tage') + ' mit Pause, zählt nicht' : ''].filter(Boolean);
    const box = el('span', { class: 'ls-up', 'data-verfuegbarkeit': up == null ? null : String(up) },
      el('span', { class: 'ls-upb', role: 'img', 'aria-label': 'Verfügbarkeit ' + since + ': ' + txt + '; ' + notes.join('; ') }, bars),
      // So gelassen: die Zahl bleibt, aber ohne Alarmfarbe.
      el('b', { class: up != null && up < 99 && !(zNow && zNow.kept) ? 'bad' : null }, txt), el('small', { class: 'ls-up-s' }, since));
    return o && o.text ? [box, el('span', { class: 'sub' }, notes.join(' · '))] : box;
  }
  // Drei Zahlen über der Tabelle: erreichbar, Zertifikate, Antwortzeit.
  function dmStats(sites) {
    const w = mon.rep && mon.rep.warnungen;
    const sum = domSum([]);
    if (!sum.checked) return null;
    const scs = w ? w.seiten.filter(sc => sites.some(x => x.domains.includes(sc.domain))) : [];
    const certs = scs.filter(sc => sc.zertifikat_bis).map(sc => ({ sc, c: certWords(sc.zertifikat_bis) })).filter(x => x.c).sort((a, b) => a.c.days - b.c.days);
    const ms = scs.filter(sc => sc.ok && sc.dauer_ms).map(sc => sc.dauer_ms);
    const nBad = sum.bad.length;
    const first = certs[0];
    const ck = first ? certLevel(first.sc.zertifikat_bis) : '';
    return el('div', { class: 'ov-stats' },
      stTile({ id: 'dm-ok', l: 'Erreichbar', icon: 'domains', c: 'c-b', v: String(sum.ok), u: 'von ' + sum.total, kind: nBad ? (sum.err ? 'err' : 'warn') : '', calm: true, tag: nBad ? (nBad === 1 ? sum.bad[0].wort || '1 antwortet nicht' : nBad + ' antworten nicht') : null,
        sub: nBad ? sum.bad.map(b => b.hosts[0]).join(', ') : [sum.paused ? plural(sum.paused, 'Domain', 'Domains') + ' pausiert' : '', sum.kept ? sum.kept + ' bewusst so gelassen' : ''].filter(Boolean).join(' · ') || 'alle antworten' }),
      first ? stTile({ id: 'dm-cert', l: 'Zertifikate', icon: 'lock', c: 'c-t', v: String(Math.max(0, first.c.days)), u: first.c.days === 1 ? 'Tag' : 'Tage', kind: ck, tag: ck ? (first.c.days < 0 ? 'abgelaufen' : 'bald fällig') : null,
        sub: 'bis zum nächsten Ablauf: ' + first.sc.domain + '. Caddy erneuert von selbst.' }) : null,
      ms.length ? stTile({ id: 'dm-ms', l: 'Antwortzeit', icon: 'clock', c: 'c-v', v: String(Math.round(ms.reduce((a, b) => a + b, 0) / ms.length)), u: 'ms',
        sub: 'im Mittel, vom Server aus gemessen' }) : null);
  }
  function pageDomainsLive(m) {
    if (!mon.loading && (!mon.at || Date.now() - mon.at > 60000)) setTimeout(loadMon, 0);
    if (!dm.loading && (!dm.at || Date.now() - dm.at > 30000 || ((dm.job && dm.job.laeuft) || (dm.rep && dm.rep.pruefung_laeuft)) && !dm.timer)) setTimeout(loadDm, 0);
    const r = dm.rep;
    if (!r) {
      return [head({ i: 'domains', k: 'Domains · live', h: dm.error ? ['Domains ', em('err', 'nicht erreichbar'), '.'] : 'Domains werden geladen …', p: dm.error || null }),
        sec(null, el('div', { class: 'ls-stack' }, el('span', { class: 'skel skel-h' }), el('span', { class: 'skel skel-t skel-w' }), el('span', { class: 'skel skel-t' })))];
    }
    if (!r.caddy) {
      return [head({ i: 'domains', k: 'Domains · live', h: ['Kein ', em('warn', 'Caddy'), ' auf diesem Server.'], p: 'HostDeck verwaltet Domains über Caddy als systemd-Dienst. Andere Webserver (nginx, Traefik) zeigt der Leitstand nicht.' }),
        sec('Nächster Schritt', el('div', { class: 'ls-stack sm' },
          el('p', {}, 'Richte Caddy als systemd-Dienst ein (unter Debian und Ubuntu: ', el('code', {}, 'sudo apt install caddy'), ') und trag deine Seiten ins Caddyfile ein. Nach dem Neu-Erfassen zeigt HostDeck hier jede Domain mit Zustand, Zertifikat und Verlauf.'),
          el('p', { class: 'ls-mut' }, 'Laufen deine Seiten über nginx oder Traefik, siehst du die Container weiter unter Projekte. Für den Umzug trägst du ihre Domains unter Umzug bei „Domains außerhalb von Caddy“ ein.'),
          el('div', { class: 'op-btns' }, quickDiscover('btn btn-sm'), link('#/umzug', 'Zum Umzug', 'btn btn-sm btn-g', 'umzug'))), { cls: 'c12' })];
    }
    const sites = r.seiten.filter(x => x.domains && x.domains.length);
    const live = sites.filter(x => x.art !== 'aus');
    const sum = domSum([]);
    const h = (r.pruefung_laeuft || !dz.list) && !sum.checked ? [plural(live.length, 'Domain', 'Domains') + ' · ', em('', 'wird geprüft …')] : domHead(sum);
    const running = !!(dm.job && dm.job.laeuft) || !!dm.busy;
    const zr = x => { const z = zOf(x.domains); return z && (z.stufe === 'err' || z.stufe === 'warn') ? 1 : 0; };
    const order = (a, b) => (a.art === 'aus') - (b.art === 'aus') || (zr(b) - zr(a)) || a.domains[0].localeCompare(b.domains[0]);
    // Die Spalte „Datei“ nur, wenn die Domains in verschiedenen Dateien
    // stehen oder eine von Hand geändert ist (Idee 14).
    const files = new Set(sites.map(x => x.datei + '|' + x.art));
    const fileCol = files.size > 1 || sites.some(x => x.von_hand);
    const table = el('div', { class: 'ls-tw' }, el('table', { class: 'ls-t dm-t' },
      el('caption', { class: 'sr' }, 'Domains von Caddy'),
      el('thead', {}, el('tr', {}, ['Domain', 'Zustand', 'Verfügbarkeit', 'Ziel', 'Zertifikat', fileCol ? 'Datei' : null, ''].filter(t => t !== null).map(t => el('th', { scope: 'col' }, t)))),
      el('tbody', {}, sites.slice().sort(order).map(x => el('tr', { class: x.art === 'aus' ? 'is-off' : null },
        el('td', { class: 'dm-d' }, x.domains.map((d, k) => [k ? el('br') : null, x.art !== 'aus' && /^[a-z0-9.-]+\.[a-z]{2,}$/i.test(d) ? el('a', { href: 'https://' + d, target: '_blank', rel: 'noopener noreferrer' }, d) : d]), dmAddr(x)),
        el('td', {}, dmLive(x)),
        el('td', {}, x.art === 'aus' ? '–' : dmUp(x)),
        el('td', { class: 'num' }, x.art === 'leitstand' ? 'HostDeck' : x.ziele.length ? x.ziele.join(', ') : '–'),
        el('td', {}, dmCert(x)),
        fileCol ? el('td', {}, dmFile(x)) : null,
        el('td', { class: 'r' }, el('div', { class: 'op-btns' }, dmActs(x))))))));
    const fileNote = !fileCol && sites.length ? (() => { const f = sites[0]; const base = f.datei.split('/').pop(); return 'alle in ' + (f.art === 'eigen' ? 'sites/' + base : f.datei); })() : r.caddyfile;
    const job = dm.job;
    const jobBox = job && job.laeuft ? sec(job.was + (job.domain ? ': ' + job.domain : '') + ' läuft …', jobSteps(job), { sub: 'Du kannst die Seite schließen; es läuft weiter.' })
      : job && job.fehler && dm.result && job.schritte.length ? sec('Ablauf: ' + job.was, jobSteps(job)) : null;
    // Geordnet und HTTP/3 an ist der Normalfall; abschalten nur auf Wunsch,
    // darum dann nur ein kleiner Kasten.
    const reorgBox = !r.geordnet || r.http3 ? sec(r.geordnet ? 'HTTP/3' : 'Caddy ordnen', el('div', { class: 'ls-stack' },
      el('p', { class: r.geordnet ? 'ls-mut' : null }, r.geordnet ? 'Jede Domain hat ihre eigene Datei, HTTP/3 ist an. Lass es so, solange Downloads gut laufen; nur wenn ein Anbieter UDP drosselt und Downloads kriechen, schaltest du es hier ab.'
        : 'Heute stehen die Domains im Caddyfile. Einmal ordnen: Jede Domain bekommt eine eigene Datei in sites/, das Caddyfile behält nur die allgemeinen Einstellungen. HostDeck prüft vorher, dass Caddy damit genau dasselbe tut, und schlägt dir Verbesserungen vor.'),
      el('div', { class: 'op-btns' }, el('button', { type: 'button', class: 'btn btn-sm' + (r.geordnet ? '' : ' btn-p'), 'data-fid': 'dm-ordnen', disabled: running, 'aria-busy': dm.busy === 'vorschau' ? 'true' : null, onclick: dmReorg }, ic('eye', 16), 'Vorschau …'))),
    { cls: 'c12', sub: r.caddyfile }) : null;
    const newBox = r.geordnet ? sec(dm.edit ? 'Ziel ändern' : 'Domain anlegen', dmForm(dm.edit ? 'ziel' : 'neu'), { sub: dm.edit ? null : 'Der DNS-Eintrag muss schon auf diesen Server zeigen; Caddy holt dann das Zertifikat.' }) : null;
    return [head({ i: 'domains', k: 'Domains' + (r.geprueft && !r.geprueft.startsWith('0001') ? ' · Stand ' + when(r.geprueft) : ' · live'), h,
      p: 'Welche Domain wohin zeigt, ob sie antwortet und wie lange ihr Zertifikat gilt. Jede Änderung zeigt erst eine Vorschau; danach prüft HostDeck alle Seiten und legt bei Problemen alles zurück.',
      a: el('button', { type: 'button', class: 'btn btn-sm', 'data-fid': 'dm-ref', disabled: dm.loading, onclick: () => { dm.at = 0; loadDm(); } }, ic('refresh', 16), 'Aktualisieren') }),
    dmResult(), dm.error ? el('p', { class: 'band b-err', role: 'status' }, dm.error) : null,
    jobBox, dmStats(live),
    sec('Domains', table, { cls: 'flush', sub: fileNote }),
    reorgBox, newBox];
  }
  function pageDomains(m) {
    if (core.svc()) return pageDomainsLive(m);
    return pageDomainsInv(m);
  }
  function pageDomainsInv(m) {
    const h = !m.routes.length ? ['Caddy hat ', em('warn', 'keine Routen'), '.'] : domHead(m.dom);
    const rows = m.routes.slice().sort((a, b) => (ROUTE[a.status] || ['', 'idle'])[1].localeCompare((ROUTE[b.status] || ['', 'idle'])[1]) || a.hosts[0].localeCompare(b.hosts[0]))
      .sort((a, b) => ((ROUTE[b.status] || [])[1] === 'err') - ((ROUTE[a.status] || [])[1] === 'err'));
    const table = el('div', { class: 'ls-tw' }, el('table', { class: 'ls-t' },
      el('caption', { class: 'sr' }, 'Routen von Caddy'),
      el('thead', {}, el('tr', {}, el('th', { scope: 'col' }, 'Domain'), el('th', { scope: 'col' }, 'Zustand'), el('th', { scope: 'col' }, 'Ziel'), el('th', { scope: 'col' }, 'Projekt'))),
      el('tbody', {}, rows.map(r => el('tr', {},
        el('td', { class: 'mono' }, r.hosts.map((d, k) => [k ? el('br') : null, /^[a-z0-9.-]+\.[a-z]{2,}$/i.test(d) ? el('a', { href: 'https://' + d, target: '_blank', rel: 'noopener noreferrer' }, d) : d])),
        el('td', {}, routeSt(r)),
        el('td', { class: 'mono' }, r.upstream || '–', r.target ? el('span', { class: 'ls-mut blk' }, r.target) : null),
        el('td', {}, r.project ? el('a', { href: '#/projekte/' + encodeURIComponent(r.project.key) }, r.project.name) : el('span', { class: 'ls-mut' }, 'kein Projekt')))))));
    const c = m.s.caddy;
    return [head({ i: 'domains', k: 'Domains · Stand ' + fmtDate(m.s.created), h,
      p: 'Caddy leitet jede Domain an einen Container weiter. Bei der Bestandsaufnahme hat HostDeck geprüft, ob am Ziel etwas lauscht.',
      a: link('#/auswahl/bereich/caddy', 'Caddy beim Umzug', 'btn btn-sm', 'auswahl') }),
    c ? sec('Routen', table, { cls: 'flush', sub: c.config ? 'aus ' + c.config : '' })
      : sec(null, el('div', { class: 'ls-empty' }, ic('info', 24), el('div', {}, el('b', {}, 'Kein Caddy gefunden.'), el('p', {}, 'HostDeck hat bei der Bestandsaufnahme keinen Caddy erkannt. Andere Webserver zeigt der Leitstand noch nicht.'))))];
  }

  // ---------- Backup ----------
  function moveBar() {
    const mv = core.move();
    const at = isAlltag();
    const W = at ? [['Im Backup', 'kommt ins Backup, wenn du eins erstellst'], ['Im Archiv', 'gesichert, holst du bei Bedarf selbst zurück'], ['Nicht im Backup', 'entsteht neu oder bewusst weggelassen']]
      : [['Kommt mit', 'wird gesichert und auf dem neuen Server eingerichtet'], ['Im Archiv', 'gesichert, holst du bei Bedarf selbst zurück'], ['Bleibt zurück', 'entsteht neu oder bewusst weggelassen']];
    const parts = [['tk', mv.take], ['ar', mv.archive], ['left', mv.left || 0]];
    const total = parts.reduce((a, b) => a + b[1], 0) || 1;
    const bar = el('div', { class: 'ls-meter big', role: 'img', 'aria-label': W[0][0] + ' ' + size(mv.take) + ', ' + W[1][0] + ' ' + size(mv.archive) + (mv.left !== null ? ', ' + W[2][0] + ' ' + size(mv.left) : '') },
      parts.map(([k, v]) => { const s = el('span', { class: 'm-' + k }); s.style.width = (v / total * 100).toFixed(2) + '%'; return s; }));
    const row = (k, w, v) => el('li', {}, el('i', { class: 'sw m-' + k }), el('span', {}, el('b', {}, w[0]), el('small', {}, w[1])), el('b', {}, bytes(v)));
    return el('div', { class: 'ls-stack sm' }, bar,
      el('ul', { class: 'ls-legend' }, row('tk', W[0], mv.take), row('ar', W[1], mv.archive), mv.left !== null ? row('left', W[2], mv.left) : null));
  }
  function pageBackupPC(m) {
    const b = m.last;
    const h = !b ? ['Noch kein Backup. ', em('warn', 'Dein erstes'), ' fehlt.'] : b.valid ? ['Dein letztes Backup ist ', em('ok', 'gültig'), '.'] : ['Dein letztes Backup ist ', em('err', 'nicht gültig'), '.'];
    const last = b ? el('div', { class: 'ls-hero' },
      el('span', { class: 'tag ' + (b.valid ? 't-ok' : 't-err') }, b.valid ? 'gültig' : 'ungültig'),
      el('b', { class: 'big' }, when(b.started)),
      el('span', { class: 'ls-mut' }, [dur(Math.round((new Date(b.finished) - new Date(b.started)) / 1000)), size(b.size) + ' geplant', size(b.added) + ' neu übertragen', plural(b.projects, 'Projekt', 'Projekte')].filter(Boolean).join(' · ')),
      !b.valid ? el('p', { class: 'band b-err' }, b.verdict) : null,
      el('div', { class: 'ls-kvs' },
        el('div', { class: 'ls-kv' }, el('span', {}, 'Ziel'), el('span', {}, 'dein PC, über hostdeck.exe')),
        el('div', { class: 'ls-kv' }, el('span', {}, 'Art'), el('span', {}, b.final ? 'letzter Lauf vor dem Umzug, Dienste blieben aus' : 'normales Backup, Dienste liefen danach weiter')),
        b.downtime_s ? el('div', { class: 'ls-kv' }, el('span', {}, 'Längste Pause'), el('span', {}, dur(b.downtime_s) + ' (ein Projekt stand kurz still)')) : null,
        el('div', { class: 'ls-kv' }, el('span', {}, 'Verschlüsselt'), el('span', {}, 'ja, mit deinem Backup-Passwort'))))
      : el('div', { class: 'ls-empty' }, ic('backup', 26), el('div', {}, el('b', {}, 'Noch kein Lauf.'), el('p', {}, 'Sobald du in hostdeck.exe ein Backup machst, steht es hier: wann, wie groß und ob es gültig ist.')));
    const how = el('ol', { class: 'ls-steps' },
      el('li', {}, el('b', {}, 'hostdeck.exe auf deinem PC öffnen'), el('span', {}, 'Es verbindet sich per SSH mit ' + core.hostName() + '.')),
      el('li', {}, el('b', {}, 'Punkt 1: Probelauf'), el('span', {}, 'zeigt nur, was ein Backup tun würde. Ändert nichts.')),
      el('li', {}, el('b', {}, 'Punkt 2: Backup jetzt machen'), el('span', {}, 'sichert deine Auswahl verschlüsselt auf deinen PC und prüft das Ergebnis.')),
      el('li', {}, el('b', {}, 'Danach hier nachsehen'), el('span', {}, 'Neu laden zeigt den Lauf in dieser Liste.')));
    const runs = m.backups.length ? el('div', { class: 'ls-tw' }, el('table', { class: 'ls-t' },
      el('caption', { class: 'sr' }, 'Letzte Backup-Läufe'),
      el('thead', {}, el('tr', {}, el('th', { scope: 'col' }, 'Wann'), el('th', { scope: 'col' }, 'Art'), el('th', { scope: 'col', class: 'r' }, 'Gesichert'), el('th', { scope: 'col', class: 'r', title: 'neu übertragen, gepackt' }, 'Davon neu'), el('th', { scope: 'col', class: 'r' }, 'Dauer'), el('th', { scope: 'col' }, 'Urteil'))),
      el('tbody', {}, m.backups.slice(0, 12).map(x => el('tr', {},
        el('td', { class: 'nowrap' }, fmtDate(x.started)), el('td', {}, x.final ? 'Letzter Lauf' : 'Backup'),
        el('td', { class: 'r nowrap' }, size(x.size)), el('td', { class: 'r nowrap' }, size(x.added)),
        el('td', { class: 'r nowrap' }, dur(Math.round((new Date(x.finished) - new Date(x.started)) / 1000))),
        el('td', {}, el('span', { class: 'tag ' + (x.valid ? 't-ok' : 't-err'), title: x.verdict }, x.valid ? 'gültig' : 'ungültig'))))))) : null;
    const tasks = m.s.tasks.length ? el('div', { class: 'ls-tw' }, el('table', { class: 'ls-t' },
      el('caption', { class: 'sr' }, 'Geplante Aufgaben auf dem Server'),
      el('thead', {}, el('tr', {}, el('th', { scope: 'col' }, 'Wann'), el('th', { scope: 'col' }, 'Was'), el('th', { scope: 'col' }, 'Art'))),
      el('tbody', {}, m.s.tasks.map(t => el('tr', {},
        el('td', { class: 'nowrap' }, t.when || t.schedule || '–'),
        el('td', {}, t.description || t.name || '', t.command ? el('span', { class: 'mono ls-mut blk' }, t.command) : null),
        el('td', {}, ({ cron: 'Cron', timer: 'Timer', dienst: 'Dienst' })[t.kind] || t.kind)))))) : null;
    return [head({ i: 'backup', k: 'Backup · Ziel: dein PC über hostdeck.exe', h,
      p: 'Der Server packt deine Auswahl verschlüsselt und schickt sie durch den SSH-Tunnel auf deinen PC. Vor jedem Umzug prüft HostDeck, ob ein gültiges Backup da ist.' }),
    m.s.backups_note ? el('p', { class: 'band b-warn' }, m.s.backups_note) : null,
    el('div', { class: 'ls-grid eq' },
      sec('Letztes Backup', last, { cls: 'c6' }),
      sec('Was gesichert wird', [moveBar(), el('div', { class: 'ls-row-end' }, el('span', { class: 'ls-mut' }, m.mstate === 'vorhanden' ? (m.open ? plural(m.open, 'Eintrag', 'Einträge') + ' offen' : 'Auswahl gespeichert, nichts offen') : 'Auswahl noch nicht gespeichert'), link('#/auswahl', 'Auswahl bearbeiten', 'btn btn-sm', 'auswahl'))], { cls: 'c6' }),
      sec('So machst du ein Backup', [how, el('p', { class: 'ls-note' }, ic('info', 16), el('span', {}, 'Läuft HostDeck als Dienst auf dem Server (sudo hostdeck einrichten), startest du Backups direkt hier im Leitstand.'))], { cls: 'c5' }),
      runs ? sec('Verlauf', runs, { cls: 'c7 flush', sub: 'die letzten Läufe, neueste zuerst' }) : null,
      tasks ? sec('Geplante Aufgaben auf dem Server', tasks, { cls: 'c12 flush', sub: 'aus Cron und systemd; ziehen mit um, wenn du sie auf dem neuen Server einrichtest' }) : null)];
  }

  // ---------- Läufe des Dienstes: Backup und Probe-Umzug ----------
  // Den Lauf führt der Dienst; die Seite fragt den Stand ab, zeigt die
  // Ausgabe und stellt Fragen des Laufs als Knöpfe. Schließt jemand den
  // Browser, läuft der Lauf weiter.
  function jobState(api, fid, done) {
    return { api, fid, done, rep: null, error: '', loading: false, at: 0, timer: 0, watching: false, start: '', lines: [], next: 0, busy: '', answered: 0, sure: false, follow: true, open: undefined };
  }
  async function loadJob(s) {
    if (s.loading) return;
    s.loading = true;
    const r = await core.call(s.api + '?ab=' + s.next, null, true);
    s.loading = false;
    s.at = Date.now();
    clearTimeout(s.timer);
    if (!r.ok) {
      s.error = (r.data && r.data.error) || 'Der Stand ließ sich nicht laden (Fehler ' + r.status + ').';
      if (s.watching) s.timer = setTimeout(() => loadJob(s), 3000);
      if (core.work()) core.rerender();
      return;
    }
    s.error = '';
    const job = r.data.auftrag;
    s.rep = r.data;
    if (job) {
      if (job.start !== s.start) { s.start = job.start; s.lines = []; s.next = job.erste; s.open = undefined; s.answered = 0; s.sure = false; }
      if (job.erste > s.next) { s.lines = []; }
      s.lines = s.lines.concat(job.zeilen);
      if (s.lines.length > 4000) s.lines = s.lines.slice(-4000);
      s.next = job.erste + job.zeilen.length;
    }
    const was = s.watching;
    s.watching = !!(job && job.laeuft);
    if (s.watching) s.timer = setTimeout(() => loadJob(s), 1500);
    else if (was && job) s.done(job);
    if (core.work()) core.rerender();
    if (s.follow) requestAnimationFrame(() => { const x = document.getElementById(s.fid + '-log'); if (x) x.scrollTop = x.scrollHeight; });
  }
  // startJob startet Probelauf oder Lauf; true, wenn er läuft.
  async function startJob(s, plan, body, label, sub) {
    if (s.busy) return false;
    s.busy = sub || (plan ? 'plan' : 'run');
    s.error = '';
    core.rerender();
    const r = await core.call(s.api + '/' + (sub || (plan ? 'probelauf' : 'starten')), body || {});
    s.busy = '';
    if (r.status === 202) {
      s.rep = r.data;
      s.start = '';
      s.next = 0;
      s.lines = [];
      s.follow = true;
      s.answered = 0;
      core.announce(label + ' läuft');
      loadJob(s);
      return true;
    }
    if (!(r.data && r.data.abgebrochen)) s.error = (r.data && r.data.error) || 'Das ging nicht (Fehler ' + r.status + ').';
    core.rerender();
    return false;
  }
  async function answerJob(s, q, value) {
    if (s.answered === q.id) return;
    s.answered = q.id;
    core.rerender();
    const r = await core.call(s.api + '/antwort', { id: q.id, antwort: value });
    if (!r.ok && r.status !== 409) { s.answered = 0; s.error = (r.data && r.data.error) || 'Antwort nicht angekommen.'; }
    loadJob(s);
  }
  async function cancelJob(s, msg) {
    s.sure = false;
    const r = await core.call(s.api + '/abbrechen', {});
    if (!r.ok && r.status !== 409) s.error = (r.data && r.data.error) || 'Abbrechen ging nicht.';
    else core.announce(msg);
    loadJob(s);
  }
  // jobView zeigt einen Lauf: Stand, Frage, Ausgabe, Abbrechen. t hält die
  // Texte, die sich zwischen Backup und Probe-Umzug unterscheiden.
  function jobView(job, s, t) {
    const run = job.laeuft;
    const plan = job.probelauf;
    const q = run && job.frage;
    const name = plan ? 'Probelauf' : t.name;
    const log = el('pre', { class: 'bk-log', id: s.fid + '-log', tabindex: '0', 'aria-label': 'Ausgabe des Laufs', onscroll: e => { const x = e.target; s.follow = x.scrollHeight - x.scrollTop - x.clientHeight < 40; } }, s.lines.join('\n') || '…');
    let state;
    if (run) state = el('p', { class: 'ls-note', role: 'status' }, ic('clock', 16), el('span', {}, name + ' läuft seit ' + hm(new Date(job.start)) + ' Uhr. Du kannst die Seite schließen; der Lauf geht weiter.'));
    else if (job.abgebrochen) state = el('div', { class: 'band b-warn', role: 'status' }, el('p', {}, el('b', {}, 'Abgebrochen. '), t.cancelled));
    else if (job.code === 0) {
      const [b, p] = t.done(plan);
      state = el('div', { class: 'zg-done bk-done' }, ic('done', 26), el('div', {}, el('b', {}, b), el('p', {}, p)));
    } else {
      // Die Gründe (Zeilen mit ✗) stehen oft weit oben in der Ausgabe;
      // hier gleich sichtbar.
      const why = s.lines.filter(l => /^\s*✗/.test(l)).map(l => l.replace(/^\s*✗\s*/, '')).slice(0, 6);
      const reasons = why.length ? el('ul', { class: 'bk-why' }, why.map(w => el('li', {}, w))) : null;
      const has = why.length > 0;
      state = job.code === 2
        ? el('div', { class: 'band b-warn', role: 'alert' }, el('p', {}, el('b', {}, name + ' nicht gestartet. '), t.blocked(has)), reasons)
        : el('div', { class: 'band b-err', role: 'alert' }, el('p', {}, el('b', {}, t.failedHead(plan)), t.failed(has)), reasons);
    }
    const question = q ? el('div', { class: 'bk-q', role: 'alert' },
      el('p', {}, q.text),
      el('div', { class: 'acts' }, (q.auswahl && q.auswahl.length ? q.auswahl : [{ text: 'Weiter', antwort: '' }]).map((c, i) =>
        el('button', { type: 'button', class: 'btn btn-sm' + (i === 0 ? ' btn-p' : ''), 'data-fid': s.fid + '-a' + i, disabled: s.answered === q.id, onclick: () => answerJob(s, q, c.antwort) }, c.text)))) : null;
    // Ohne Abbrechen (Tuning): ein halber Lauf wäre schlechter als ein ganzer.
    const stop = run && t.noStop ? el('p', { class: 'ls-mut' }, t.noStop) : run ? (s.sure
      ? el('div', { class: 'zg-sure' }, el('p', {}, t.stopAsk),
        el('div', { class: 'acts' }, el('button', { type: 'button', class: 'btn btn-sm btn-err', 'data-fid': s.fid + '-stop-go', onclick: () => cancelJob(s, t.stopping) }, 'Ja, abbrechen'),
          el('button', { type: 'button', class: 'btn btn-sm', 'data-fid': s.fid + '-stop-no', onclick: () => { s.sure = false; core.rerender(); } }, 'Weiterlaufen lassen')))
      : el('div', { class: 'ls-row-end' }, el('span', { class: 'ls-mut' }, 'Die Ausgabe aktualisiert sich von selbst.'),
        el('button', { type: 'button', class: 'btn btn-sm', 'data-fid': s.fid + '-stop', onclick: () => { s.sure = true; core.rerender(); } }, 'Abbrechen'))) : null;
    // Nach dem Lauf klappt die Ausgabe ein, außer etwas ging schief.
    const out = run ? log : el('details', { class: 'bk-out', open: job.code !== 0, ontoggle: e => { s.open = e.target.open; } },
      el('summary', {}, 'Ausgabe des Laufs (' + plural(s.lines.length, 'Zeile', 'Zeilen') + ')'), log);
    if (!run && s.open !== undefined) out.open = s.open;
    return el('div', { class: 'ls-stack sm' }, state, question, out, stop);
  }

  // ---------- Backup auf dem Server ----------
  // Als Dienst startet der Leitstand Probelauf und Backup selbst.
  const bk = jobState('/api/backup', 'bk', job => finished(job));
  bk.dlErr = '';
  const BACKUP_TEXT = {
    name: 'Backup',
    cancelled: 'Gestoppte Container laufen wieder. Unten steht, wie weit der Lauf kam.',
    done: plan => plan
      ? ['Probelauf fertig: alles bereit.', 'Es wurde nichts gestoppt und nichts gesichert. Das echte Backup startest du unter „Backup starten“.']
      : ['Backup fertig: gültig.', 'Das Backup liegt verschlüsselt auf dem Server. Herunterladen kannst du es unter „Auf dem Server“.'],
    blocked: has => has ? 'Die Vorprüfung hat das gefunden; am Server hat sich nichts geändert:' : 'Die Vorprüfung hat etwas gefunden; unten steht, was. Am Server hat sich nichts geändert.',
    failedHead: plan => plan ? 'Probelauf mit Fehlern. ' : 'Backup nicht gültig. ',
    failed: has => has ? 'Das ging schief; gestoppte Container laufen wieder:' : 'Unten steht, was schiefging. Gestoppte Container laufen wieder.',
    stopAsk: 'Lauf abbrechen? HostDeck startet gestoppte Container gleich wieder; ein halbes Backup zählt nicht.',
    stopping: 'Wird abgebrochen; HostDeck startet gestoppte Container wieder.',
  };
  function loadBackup() { return loadJob(bk); }
  async function finished(job) {
    jr.stale = true;
    core.refreshLauf();
    const what = job.probelauf ? 'Probelauf' : 'Backup';
    core.announce(job.abgebrochen ? what + ' abgebrochen' : job.code === 0 ? (job.probelauf ? 'Probelauf fertig' : 'Backup fertig: gültig') : what + ' fertig, aber nicht gültig');
    // Den Verlauf neu laden, ohne offene Änderungen der Auswahl zu verwerfen.
    if (!job.probelauf && core.dirty() === 0) await core.reload(true);
    else if (!job.probelauf) core.toast('Backup fertig. Lade die Seite neu, sobald du mit der Auswahl fertig bist.');
  }
  function backupBadge() {
    const l = (core.svc() || {}).lauf;
    return bk.watching || (l && (l.was === 'Backup' || l.was === 'Probelauf')) ? cnt('läuft', 'acc') : null;
  }
  function startBackup(plan) { return startJob(bk, plan, {}, plan ? 'Probelauf' : 'Backup'); }
  async function download() {
    bk.dlErr = '';
    const r = await core.call('/api/backup/herunterladen', {});
    if (!r.ok) {
      if (!(r.data && r.data.abgebrochen)) bk.dlErr = (r.data && r.data.error) || 'Herunterladen ging nicht (Fehler ' + r.status + ').';
      core.rerender();
      return;
    }
    // Ein Link startet den Download im Browser selbst; er kann ihn nach
    // einem Abbruch fortsetzen.
    const a = el('a', { href: r.data.pfad, download: '' });
    document.body.append(a);
    a.click();
    a.remove();
    jr.stale = true;
    core.toast('Download startet. Den Fortschritt zeigt dein Browser.');
  }
  function backupStart(st, sv, m) {
    const busy = sv.lauf && !bk.watching ? 'Geht erst, wenn „' + sv.lauf.was + '“ fertig ist.' : '';
    const run = bk.watching;
    return el('div', { class: 'ls-stack sm' },
      !st.bereit ? el('div', { class: 'band b-warn', role: 'status' }, el('p', {}, st.problem)) : null,
      el('p', { class: 'ls-mut' }, 'Der Probelauf zeigt nur, was passieren würde. Das Backup stoppt jedes Projekt kurz, sichert es verschlüsselt auf den Server und startet es wieder.'),
      bk.error ? el('p', { class: 'gt-err', role: 'alert' }, ic('warn', 16), el('span', {}, bk.error)) : null,
      el('div', { class: 'ls-row-end' },
        el('span', { class: 'ls-mut' }, busy || (run ? 'Ein Lauf läuft gerade.' : 'Das Backup bestätigst du mit deinem Passwort.')),
        el('div', { class: 'acts' },
          el('button', { type: 'button', class: 'btn', 'data-fid': 'bk-plan', disabled: !!busy || run || !!bk.busy, onclick: () => startBackup(true) }, ic('search', 18), bk.busy === 'plan' ? 'Startet …' : 'Probelauf'),
          el('button', { type: 'button', class: 'btn btn-p', 'data-fid': 'bk-run', disabled: !!busy || run || !!bk.busy || !st.bereit || !m.auswahlDone, title: m.auswahlDone ? null : 'Erst die Auswahl: ' + auswahlState(m), onclick: () => startBackup(false) }, ic('backup', 18), bk.busy === 'run' ? 'Startet …' : 'Backup erstellen'))));
  }
  // Backup vom Server löschen: erst die Vorschau, dann das Passwort.
  async function removeBackup(st, m) {
    const ok = await clDialog(done => [
      el('p', { class: 'ls-eyb' }, ic('eye', 16), el('span', {}, 'Vorschau · noch ist nichts passiert')),
      el('h2', { id: 'cl-h' }, 'Backup vom Server löschen?'),
      el('section', { class: 'op-part', id: 'cl-d' }, el('h3', {}, 'Was verschwindet'),
        el('ul', { class: 'op-list' }, el('li', {}, plural(st.laeufe, 'gültiges Backup', 'gültige Backups') + ' im Backup-Ordner auf dem Server, ' + size(st.groesse) + (st.letzter ? ', das neueste von ' + when(st.letzter) : '')))),
      el('section', { class: 'op-part' }, el('h3', {}, 'Was bleibt'),
        el('ul', { class: 'op-list' }, el('li', {}, 'deine heruntergeladenen Dateien und der Verlauf unter Backup'), el('li', {}, 'dein Backup-Passwort; das nächste Backup legt den Ordner damit neu an und sichert dann wieder alles'))),
      m.alltag ? null : el('p', { class: 'ls-note op-warn' }, ic('warn', 16), el('span', {}, 'Solange ein Umzug ansteht, braucht ihn das Backup: Der Probe-Umzug spielt es vom Server ein. Nach dem Löschen brauchst du dafür erst ein neues.')),
      el('div', { class: 'gt-acts' }, el('button', { type: 'button', class: 'btn', onclick: () => done(false) }, 'Abbrechen'),
        el('button', { type: 'button', class: 'btn btn-p', onclick: () => done(true) }, ic('trash', 16), 'Vom Server löschen'))]);
    if (!ok) { core.rerender(); return; }
    bk.dlErr = '';
    const r = await core.call('/api/backup/loeschen', {});
    if (!r.ok) {
      if (!(r.data && r.data.abgebrochen)) bk.dlErr = (r.data && r.data.error) || 'Löschen ging nicht (Fehler ' + r.status + ').';
    } else {
      bk.rep = Object.assign({}, bk.rep, { ordner: r.data.ordner, heruntergeladen: r.data.heruntergeladen });
      jr.stale = true;
      core.toast('Backup vom Server gelöscht.');
    }
    core.rerender();
  }
  // Ob und wann das Backup im Ordner vollständig heruntergeladen wurde.
  function fetchedNote(f) {
    if (!f) return null;
    const at = 'Vollständig heruntergeladen: ' + when(f.am) + '. ';
    if (f.loeschen_um) {
      const hm = new Date(f.loeschen_um).toLocaleTimeString('de-DE', { hour: '2-digit', minute: '2-digit' });
      return el('p', { class: 'ls-note', 'data-fid': 'bk-fetched' }, ic('check', 16), el('span', {}, at + 'HostDeck löscht es gegen ' + hm + ' Uhr vom Server; setzt dein Browser vorher noch einmal an, etwas später.'));
    }
    return el('p', { class: 'ls-note', 'data-fid': 'bk-fetched' }, ic('check', 16), el('span', {}, at + 'Es liegt noch auf dem Server. Brauchst du es dort nicht mehr, nimm „Vom Server löschen“.'));
  }
  function backupStored(st, sv, m) {
    const busy = sv.lauf ? 'Geht erst, wenn „' + sv.lauf.was + '“ fertig ist.' : '';
    return el('div', { class: 'ls-stack sm' },
      el('div', { class: 'ls-kvs' },
        el('div', { class: 'ls-kv' }, el('span', {}, 'Neuestes Backup'), el('span', {}, st.letzter ? when(st.letzter) : '–')),
        el('div', { class: 'ls-kv' }, el('span', {}, 'Gültige Backups'), el('span', {}, String(st.laeufe))),
        el('div', { class: 'ls-kv' }, el('span', {}, 'Belegt auf dem Server'), el('span', {}, size(st.groesse) + ', gepackt')),
        el('div', { class: 'ls-kv' }, el('span', {}, 'Verschlüsselt'), el('span', {}, 'ja, mit deinem Backup-Passwort'))),
      bk.dlErr ? el('p', { class: 'gt-err', role: 'alert' }, ic('warn', 16), el('span', {}, bk.dlErr)) : null,
      el('div', { class: 'ls-row-end' },
        el('span', { class: 'ls-mut' }, busy || (st.laeufe === 1 ? 'Eine Datei mit deinem Backup.' : 'Eine Datei mit allen ' + st.laeufe + ' Backups.') + ' Du bestätigst vorher mit deinem Passwort.'),
        el('div', { class: 'acts' },
          el('button', { type: 'button', class: 'btn', 'data-fid': 'bk-rm', disabled: !!busy, onclick: () => removeBackup(st, m) }, ic('trash', 18), 'Vom Server löschen …'),
          el('button', { type: 'button', class: 'btn btn-p', 'data-fid': 'bk-dl', disabled: !!busy, onclick: download }, ic('download', 18), 'Herunterladen'))),
      fetchedNote(bk.rep && bk.rep.heruntergeladen),
      // Ein Hinweis je Abschnitt (Idee 17): was nach dem Download passiert
      // und wie man das Backup öffnet, in einem Kasten.
      el('p', { class: 'ls-note' }, ic('info', 16), el('span', {},
        m.alltag ? 'Kurz nach dem vollständigen Download löscht HostDeck das Backup vom Server; eine halbe Stunde wartet es, falls dein Browser noch einmal ansetzt. '
          : ['Solange ein Umzug ansteht, bleibt das Backup auf dem Server; der Umzug spielt es von dort ein (', el('a', { href: '#/umzug', 'data-fid': 'bk-mode' }, 'kein Umzug geplant?'), '). '],
        'Zum Öffnen brauchst du dein Backup-Passwort; am Server zeigt es ', el('code', {}, 'sudo hostdeck backup-passwort --zeigen'), '.')));
  }
  // Was an der Auswahl fehlt, in wenigen Worten (Kacheln von Backup und Umzug).
  function auswahlState(m) {
    if (m.mstate !== 'vorhanden') return 'noch nicht gespeichert';
    if (m.open) return 'vorher startet kein Backup';
    if (m.gone) return plural(m.gone, 'Eintrag', 'Einträge') + ' nicht mehr gefunden';
    if (m.s.manifest.added.length) return plural(m.s.manifest.added.length, 'neuer Eintrag', 'neue Einträge') + ' zu prüfen';
    if (m.dirty) return 'Änderungen nicht gespeichert';
    return 'gespeichert, nichts offen';
  }
  function pageBackup(m) {
    const sv = core.svc();
    if (!sv) return pageBackupPC(m);
    // Beim Öffnen der Seite frisch laden: Ein Lauf kann auch aus einem
    // anderen Browser gestartet sein.
    if (!bk.loading && !bk.watching && ((!bk.rep && !bk.error) || Date.now() - bk.at > 10000)) loadBackup();
    const st = bk.rep ? bk.rep.ordner : null;
    const job = bk.rep && bk.rep.auftrag;
    const b = m.last;
    // Probe-Kopie und abgelöster Server: Hier startet kein Backup;
    // nichts davon ist gelb.
    const off = sv.kopie ? 'kopie' : sv.abgeloest ? 'abgeloest' : '';
    // Stufe wie Pille und Sicherheitsnetz (eine Quelle, V5): ohne Backup
    // neutral, bis die Regel „Backup alt“ nach ihren Tagen warnt.
    const bv = backupView();
    const bk2 = bv ? (bv.stufe === 'idle' || bv.stufe === 'ok' ? '' : bv.stufe) : 'warn';
    const old = bv && bv.gueltig && bk2 && b && b.valid;
    let h;
    if (bk.watching && job) h = [job.probelauf ? 'Probelauf' : 'Backup', ' ', em('acc', 'läuft'), ' …'];
    else if (off === 'kopie') h = ['Auf der Probe-Kopie gibt es ', em('acc', 'keine Backups'), '.'];
    else if (off) h = ['Dieser Server ist ', em('ok', 'abgelöst'), '; Backups macht der neue.'];
    else if (!b) h = ['Noch kein Backup. ', em(bk2, 'Dein erstes'), ' fehlt.'];
    else if (old) h = ['Dein letztes Backup ist gültig, aber ', em(bk2, plural(bv.tage, 'Tag', 'Tage') + ' alt'), '.'];
    else if (b.valid) h = ['Dein letztes Backup ist ', em('ok', 'gültig'), '.'];
    else h = ['Dein letztes Backup ist ', em('err', 'nicht gültig'), '.'];
    // Gültige Server-Läufe, die noch im Backup-Ordner liegen: die neuesten
    // st.laeufe; ältere hat HostDeck aufgeräumt oder nach dem Download gelöscht.
    const kept = new Set(m.backups.filter(x => x.valid && x.target === 'server').slice(0, st ? st.laeufe : 0));
    const onServer = x => kept.has(x);
    const runs = m.backups.length ? el('div', { class: 'ls-tw' }, el('table', { class: 'ls-t' },
      el('caption', { class: 'sr' }, 'Letzte Backup-Läufe'),
      el('thead', {}, el('tr', {}, el('th', { scope: 'col' }, 'Wann'), el('th', { scope: 'col' }, 'Ziel'), el('th', { scope: 'col', class: 'r' }, 'Gesichert'), el('th', { scope: 'col', class: 'r', title: 'neu übertragen, gepackt' }, 'Davon neu'), el('th', { scope: 'col', class: 'r' }, 'Dauer'), el('th', { scope: 'col' }, 'Urteil'))),
      el('tbody', {}, m.backups.slice(0, 12).map(x => el('tr', {},
        el('td', { class: 'nowrap' }, fmtDate(x.started)), el('td', {}, x.target !== 'server' ? 'dein PC' : !x.valid || !st ? 'Server' : onServer(x) ? 'Server, liegt dort' : 'Server, gelöscht'),
        el('td', { class: 'r nowrap' }, size(x.size)), el('td', { class: 'r nowrap' }, size(x.added)),
        el('td', { class: 'r nowrap' }, dur(Math.round((new Date(x.finished) - new Date(x.started)) / 1000))),
        el('td', {}, el('span', { class: 'tag ' + (x.valid ? 't-ok' : 't-err'), title: x.verdict }, x.valid ? 'gültig' : 'ungültig'))))))) : null;
    const kp = [];
    if (b) {
      const [d, t] = when(b.started).split(', ');
      const gone = b.valid && b.target === 'server' && st && !onServer(b);
      kp.push(kpi({ l: 'Letztes Backup', v: t || d, u: t ? d : '', kind: b.valid ? (old ? bk2 : '') : 'err', tag: b.valid ? (old ? [bk2, plural(bv.tage, 'Tag', 'Tage') + ' alt'] : ['ok', 'gültig']) : ['err', 'ungültig'], s: size(b.size) + ' gesichert · ' + (gone ? 'vom Server gelöscht' : plural(b.projects, 'Projekt', 'Projekte')), spark: false }));
    } else kp.push(kpi({ l: 'Letztes Backup', v: 'keins', kind: off ? '' : bk2, s: off ? 'hier keins' : 'dein erstes fehlt', spark: false }));
    if (off !== 'kopie') kp.push(kpi({ l: 'Auf dem Server', v: st ? String(st.laeufe) : '–', u: 'gültig', s: st && st.laeufe ? 'belegt ' + size(st.groesse) + ' gepackt' + (m.alltag ? ' · bis kurz nach dem Download' : ' · solange ein Umzug ansteht') : m.alltag ? 'nach dem Download gelöscht' : 'zum Herunterladen', spark: false }));
    if (st && !off) {
      const [n, u] = splitUnit(size(st.frei));
      kp.push(kpi({ l: 'Frei auf dem Server', v: n, u, s: 'Platz für neue Backups', spark: false }));
    }
    const mv0 = core.move();
    const [tn, tu] = splitUnit(size(mv0.take));
    // „0 offen“ neben „2 neue Einträge zu prüfen“ widersprach sich (Labor
    // 14.11.): Die Zahl zählt, was gerade das Backup aufhält.
    const added = m.s.manifest.added.length;
    kp.push(kpi({ l: 'Auswahl', v: m.auswahlDone ? tn : String(m.open || m.gone || added || 0), u: m.auswahlDone ? tu + ' kommen mit' : !m.open && m.gone ? 'fehlen' : !m.open && added ? 'zu prüfen' : 'offen',
      kind: m.auswahlDone || off ? '' : 'warn', href: '#/auswahl', s: auswahlState(m), spark: false }));
    const what = sec('Was gesichert wird', [moveBar(), el('div', { class: 'ls-row-end' }, el('span', { class: 'ls-mut' }, m.mstate === 'vorhanden' ? (m.open ? plural(m.open, 'Eintrag', 'Einträge') + ' offen' : m.gone ? plural(m.gone, 'Eintrag', 'Einträge') + ' nicht mehr gefunden: entfernen und speichern' : m.s.manifest.added.length ? plural(m.s.manifest.added.length, 'neuer Eintrag', 'neue Einträge') + ' seit dem Speichern: prüfen und speichern, sonst startet kein Backup' : 'Auswahl gespeichert, nichts offen') : 'Auswahl noch nicht gespeichert'), link('#/auswahl', 'Auswahl bearbeiten', 'btn btn-sm', 'auswahl'))], { cls: 'c6' });
    const offText = off === 'kopie' ? 'Backups startest du im Leitstand von ' + (sv.kopie.von || 'dem alten Server') + '. Was du hier änderst, ist weg, wenn du die Kopie entfernst.'
      : 'Liegt hier noch ein Backup, kannst du es unten herunterladen, etwa als Archiv, bevor du diesen Server kündigst.';
    return [head({ i: 'backup', k: 'Backup', h,
      p: off ? offText : el('ol', { class: 'ls-steps' },
        el('li', {}, el('b', {}, 'Probelauf'), el('span', {}, 'zeigt, was ein Backup tun würde. Stoppt nichts, sichert nichts.')),
        el('li', {}, el('b', {}, 'Backup erstellen'), el('span', {}, 'mit deinem Passwort bestätigen. HostDeck stoppt jedes Projekt kurz, sichert deine Auswahl verschlüsselt auf diesen Server und startet es wieder. Fragen kommen als Knopf.')),
        el('li', {}, el('b', {}, 'Herunterladen'), el('span', {}, 'als eine Datei auf deinen PC. ' + (m.alltag ? 'Kurz nach dem vollständigen Download löscht HostDeck das Backup vom Server.' : 'Solange ein Umzug ansteht, bleiben die letzten ' + ((st && st.behalten) || 2) + ' gültigen Backups auf dem Server.') + ' Backups laufen nur, wenn du sie startest.'))) }),
    el('div', { class: 'ls-kpis' }, kp),
    m.s.backups_note ? el('p', { class: 'band b-warn' }, m.s.backups_note) : null,
    !bk.watching && !m.auswahlDone && !off ? el('div', { class: 'band b-warn band-row', role: 'status' }, el('p', {}, el('b', {}, 'Erst die Auswahl. '),
      m.mstate !== 'vorhanden' ? 'Du hast noch nicht entschieden, was ins Backup kommt.' : m.open ? 'In deiner Auswahl ' + (m.open === 1 ? 'ist noch 1 Eintrag' : 'sind noch ' + m.open + ' Einträge') + ' offen.' : m.gone ? plural(m.gone, 'Eintrag', 'Einträge') + ' deiner Auswahl gibt es nicht mehr; unter „Nicht mehr gefunden“ entfernen und speichern.' : 'Seit dem Speichern ist Neues auf dem Server; HostDeck hat es mit Vorschlag eingetragen. Prüfen und speichern.',
      ' Vorher startet kein Backup.'), link('#/auswahl', 'Zur Auswahl', 'btn btn-sm', 'auswahl')) : null,
    el('div', { class: 'ls-grid eq' },
      job ? sec(bk.watching ? 'Lauf' : 'Letzter Lauf', jobView(job, bk, BACKUP_TEXT), { cls: 'c12', sub: bk.watching ? 'aktualisiert sich von selbst' : 'seit dem letzten Start des Dienstes' }) : null,
      off ? null : st ? sec('Backup starten', backupStart(st, sv, m), { cls: 'c6' }) : sec('Backup starten', el('p', { class: 'ls-mut' }, bk.error || 'Lädt …'), { cls: 'c6' }),
      st && st.laeufe ? sec('Auf dem Server', backupStored(st, sv, m), { cls: 'c6' }) : null,
      off === 'kopie' ? null : what,
      runs ? sec('Verlauf', runs, { cls: 'c6 flush', sub: 'die letzten Läufe, neueste zuerst' }) : null)];
  }

  // ---------- Umzug ----------
  // Lange Listen eingeklappt: Zusammenfassung, aufgeklappt als Chips.
  function moreList(items, label) {
    return el('details', { class: 'ls-more' }, el('summary', {}, label),
      el('ul', { class: 'ls-taglist' }, items.map(x => el('li', { class: 'mono' }, x))));
  }
  function longText(text, label) {
    return text.length <= 160 ? el('span', {}, text)
      : el('details', { class: 'ls-more' }, el('summary', {}, label || 'Ganze Liste zeigen'), el('p', { class: 'mono ls-code' }, text));
  }
  function moveChecklist(m) {
    // Die Firewall ist keine Aufgabe für den neuen Server (steht neutral
    // unter Sicherheit).
    const checks = m.s.checks.filter(c => c.area !== 'firewall');
    const groups = new Map();
    for (const c of checks) { if (!groups.has(c.area)) groups.set(c.area, []); groups.get(c.area).push(c); }
    return checks.length ? el('ul', { class: 'ls-check' }, [...groups].map(([area, list]) => list.map(c =>
      el('li', {}, el('span', { class: 'ar' }, CHECK_AREAS[area] || area),
        el('div', {}, el('b', {}, c.title), c.detail ? longText(c.detail) : null))))) : el('p', { class: 'ls-mut' }, 'Die Bestandsaufnahme hat nichts gefunden, das neu eingerichtet werden muss.');
  }
  function vsteps(steps) {
    const now = steps.findIndex(x => !x[2]);
    return el('ol', { class: 'ls-vsteps' }, steps.map(([t, d, done], i) => el('li', { class: done ? 'done' : now === i ? 'now' : '' },
      el('span', { class: 'dot' }, done ? ic('check', 14) : String(i + 1)), el('div', {}, el('b', {}, t, done ? el('span', { class: 'sr' }, ' (erledigt)') : null), el('span', {}, d)))));
  }
  function pageUmzug(m) {
    if (core.svc()) return pageUmzugSvc(m);
    const ready = !!m.lastValid && m.auswahlDone;
    const h = ready ? ['Bereit für den ', em('acc', 'neuen vServer'), '.'] : ['Erst das Backup, ', em('warn', 'dann der Umzug'), '.'];
    const steps = [
      ['Auswahl fertig', m.auswahlDone ? 'nichts offen, gespeichert' : m.mstate !== 'vorhanden' ? 'noch nicht gespeichert' : m.open ? 'noch ' + plural(m.open, 'Eintrag', 'Einträge') + ' offen' : 'Änderungen noch nicht gespeichert', m.auswahlDone],
      ['Gültiges Backup', m.lastValid ? 'zuletzt ' + when(m.lastValid.started) : 'noch keins', !!m.lastValid],
      ['Neuen vServer eintragen', 'in hostdeck.exe, Punkt 6: Server-Adresse', false],
      ['Probe: zurückspielen und prüfen', 'Punkt 7 zeigt erst, was passiert; Punkt 8 prüft, ob alles läuft. Der alte Server läuft dabei weiter.', false],
      ['Umzugstag', 'Punkt 3: letzter Lauf, die Dienste bleiben danach aus. Dann Punkt 7 und 8 auf dem neuen Server.', false],
      ['Domains umstellen', 'beim Anbieter deiner Domains auf die neue Adresse zeigen lassen', false],
    ];
    return [head({ i: 'umzug', k: 'Umzug · auf einen neuen vServer', h,
      p: 'Der Umzug läuft hier über hostdeck.exe auf deinem PC. Hier siehst du, wo du stehst und was der neue Server braucht. Läuft HostDeck als Dienst auf dem Server, geht der Probe-Umzug direkt aus dem Leitstand.' }),
    el('div', { class: 'ls-grid eq' },
      sec('Ablauf', vsteps(steps), { cls: 'c5' }),
      sec('Checkliste für den neuen Server', moveChecklist(m), { cls: 'c7', sub: 'Dinge, die nicht als Datei mitkommen, sondern neu eingerichtet werden' }))];
  }

  // Probe-Umzug als Dienst: Dieser Server meldet sich per SSH am neuen an,
  // bringt HostDeck dorthin und spielt das letzte gültige Backup ein. Das
  // Passwort des neuen Servers bleibt nur in dieser Seite, bis der Lauf
  // startet, und geht dann einmal an den Dienst; gespeichert wird es nicht.
  const mv = jobState('/api/umzug', 'mv', job => movedDone(job));
  mv.form = null;
  mv.pw = '';
  mv.bpw = ''; // Backup-Passwort: nur zum Vergleich, wird nie angezeigt
  mv.show = false;
  mv.check = '';
  // Art des Laufs: probelauf, probe-umzug, probe-anhalten, probe-entfernen.
  const moveArt = job => (job && job.art) || (job && job.probelauf ? 'probelauf' : 'probe-umzug');
  const MOVE_NAME = { 'probelauf': 'Probelauf', 'probe-umzug': 'Probe-Umzug', 'probe-anhalten': 'Probe-Kopie anhalten', 'probe-entfernen': 'Probe-Kopie entfernen', 'umzugstag': 'Umzugstag', 'rueckweg': 'Rückweg' };
  const curArt = () => moveArt(mv.rep && mv.rep.auftrag);
  const copyArt = () => /^probe-(anhalten|entfernen)$/.test(curArt());
  const dayArt = () => curArt() === 'umzugstag' || curArt() === 'rueckweg';
  const MOVE_TEXT = {
    get name() { return MOVE_NAME[curArt()]; },
    get cancelled() {
      if (dayArt()) return 'Oben unter „Umzugstag“ steht, wie weit er kam. Sind die Projekte hier schon aus, startet „Zurück zum alten Server“ sie wieder.';
      return copyArt() ? 'Was bis dahin angehalten oder gelöscht war, bleibt so; unten steht, wie weit der Lauf kam.'
        : 'Dieser Server läuft unverändert weiter. Auf dem neuen Server kann ein halber Stand liegen; unten steht, wie weit der Lauf kam.';
    },
    done: plan => plan
      ? ['Probelauf fertig: alles bereit.', 'Eingespielt wurde nichts. Den Probe-Umzug startest du jetzt oben im Lotsen.']
      : curArt() === 'umzugstag' ? (ud.rep && ud.rep.abgeloest
        ? ['Umzug abgeschlossen.', 'Alles ist auf dem neuen Server, und deine Domains zeigen dorthin. Unten steht die Ausgabe des Umzugstags.']
        : ['Alles ist auf dem neuen Server.', 'Jetzt stellst du deine Domains um; die Liste „Domains umstellen“ hakt jede ab, sobald sie dorthin zeigt. Bis dahin startet „Zurück zum alten Server“ alles hier wieder.'])
      : curArt() === 'rueckweg' ? ['Die Projekte laufen wieder hier.', 'Deine Domains zeigen auf diesen Server. Den Umzugstag kannst du später wiederholen.']
      : curArt() === 'probe-anhalten' ? ['Probe-Kopie angehalten.', 'Die Projekte auf ' + ((mv.rep && mv.rep.ziel) || 'dem neuen Server') + ' sind gestoppt und starten nicht von selbst. Daten und Container liegen noch dort.']
      : curArt() === 'probe-entfernen' ? ['Probe-Kopie entfernt.', 'Auf ' + ((mv.rep && mv.rep.ziel) || 'dem neuen Server') + ' ist gelöscht, was der Probe-Umzug angelegt hatte. Dieser Server und dein Backup sind unberührt.']
      : ['Probe-Umzug fertig: alles eingespielt und geprüft.', 'Deine Projekte laufen jetzt auch auf ' + ((mv.rep && mv.rep.ziel) || 'dem neuen Server') + ', samt Caddy, Zertifikaten und einer Probe-Kopie dieses Leitstands. Deine Domains zeigen weiter auf diesen Server, bis du sie umstellst; unten in der Ausgabe steht, wie jede Seite dort antwortet und unter welcher Adresse du die Kopie öffnest.'],
    blocked: has => copyArt()
      ? (has ? 'Das hat HostDeck gefunden; verändert wurde nichts:' : 'Unten steht, warum. Verändert wurde nichts.')
      : (has ? 'Das hat HostDeck gefunden; eingespielt wurde nichts:' : 'Unten steht, warum. Eingespielt wurde nichts.'),
    failedHead: plan => plan ? 'Probelauf mit Fehlern. ' : MOVE_NAME[curArt()] + ' mit Fehlern. ',
    failed: has => dayArt()
      ? (has ? 'Das ging schief; oben unter „Umzugstag“ steht, wie es weitergeht:' : 'Unten steht, was schiefging; oben unter „Umzugstag“, wie es weitergeht.')
      : has ? 'Das ging schief; dieser Server läuft unverändert weiter:' : 'Unten steht, was schiefging. Dieser Server läuft unverändert weiter.',
    get stopAsk() {
      if (curArt() === 'umzugstag') return 'Umzugstag abbrechen? Läuft schon der letzte Lauf, bleiben die Projekte hier aus, bis du „Zurück zum alten Server“ wählst.';
      return copyArt() ? 'Lauf abbrechen? Was schon angehalten oder gelöscht ist, bleibt so.'
        : 'Probe-Umzug abbrechen? Dieser Server bleibt unberührt; auf dem neuen bleibt liegen, was bis dahin eingespielt ist.';
    },
    stopping: 'Wird abgebrochen.',
  };
  function movedDone(job) {
    core.refreshLauf();
    loadUd();
    const what = MOVE_NAME[moveArt(job)];
    core.announce(job.abgebrochen ? what + ' abgebrochen' : job.code === 0 ? what + ' fertig' : what + ' mit Fehlern beendet');
    // Der Umzugstag macht hier einen letzten Lauf: Backup-Liste und
    // Übersicht neu laden wie nach einem Backup.
    if (moveArt(job) === 'umzugstag' && core.dirty() === 0) core.reload(true);
  }
  function moveBadge() {
    const l = (core.svc() || {}).lauf;
    if (ud.rep && ud.rep.abgeloest) return cnt('abgelöst', 'idle');
    if (ud.rep && ud.rep.umzugstag && ud.rep.umzugstag.phase === 'dns') return cnt('DNS', 'acc');
    if (mv.watching || (l && /^(Probe-Umzug|Probelauf Umzug|Probe-Kopie anhalten|Probe-Kopie entfernen|Umzugstag|Rückweg)/.test(l.was))) return cnt('läuft', 'acc');
    // Im Alltag soll eine laufende Probe-Kopie nicht aus dem Blick geraten.
    const pk = (core.svc() || {}).probe_kopie;
    // Neutral, nicht gelb: Die Probe-Kopie hast du selbst angelegt.
    return pk && !pk.angehalten && core.svc().alltag ? cnt('Probe-Kopie', 'acc') : null;
  }
  async function startMove(plan, day) {
    const f = mv.form;
    mv.check = !f.server.trim() ? 'server' : !mv.pw ? 'pw' : !mv.bpw ? 'bpw' : '';
    if (mv.check) {
      core.rerender();
      const x = document.getElementById('mv-' + mv.check);
      if (x) x.focus();
      return;
    }
    const body = { server: f.server.trim(), port: parseInt(f.port, 10) || 0, benutzer: f.benutzer.trim(), passwort: mv.pw, backup_passwort: mv.bpw };
    // Nach dem echten Start sind die Passwörter hier nicht mehr nötig.
    const ok = day ? await startJob(mv, false, body, 'Umzugstag', 'umzugstag') : await startJob(mv, plan, body, plan ? 'Probelauf' : 'Probe-Umzug');
    if (ok && !plan) { mv.pw = ''; mv.bpw = ''; mv.show = false; ud.sure = false; core.rerender(); loadUd(); }
  }
  // Probe-Kopie anhalten oder entfernen: braucht nur die Anmeldung am neuen
  // Server, nicht das Backup-Passwort. Der neue Server zeigt vorher, was er
  // tut, und fragt nach.
  async function startCopy(art) {
    const f = mv.form;
    mv.check = !f.server.trim() ? 'server' : !mv.pw ? 'pw' : '';
    if (mv.check) {
      core.rerender();
      const x = document.getElementById('mv-' + mv.check);
      if (x) x.focus();
      return;
    }
    const body = { server: f.server.trim(), port: parseInt(f.port, 10) || 0, benutzer: f.benutzer.trim(), passwort: mv.pw };
    await startJob(mv, false, body, MOVE_NAME[art], art);
  }
  // Umzugstag: Generalprobe, fester Ablauf, DNS, Rückweg.
  const ud = { rep: null, loading: false, at: 0, error: '', sure: false, busy: '', timer: 0 };
  async function loadUd() {
    if (ud.loading) return;
    ud.loading = true;
    const r = await core.call('/api/umzug/umzugstag', null, true);
    ud.loading = false;
    ud.at = Date.now();
    if (r.ok) { ud.rep = r.data; ud.error = ''; } else if (r.status !== 404) ud.error = (r.data && r.data.error) || 'Fehler ' + r.status;
    clearTimeout(ud.timer);
    // Während des Ablaufs und beim Warten auf DNS laufend nachsehen.
    const d = ud.rep && ud.rep.umzugstag;
    if (d && (d.phase === 'laeuft' || d.phase === 'dns')) ud.timer = setTimeout(() => { loadUd(); if (d.phase === 'dns' && ui.page === 'umzug') loadDn(); }, d.phase === 'dns' ? 60000 : 3000);
    if (ui.page === 'umzug') core.rerender();
  }
  async function dayCall(sub, label) {
    if (ud.busy) return;
    ud.busy = sub;
    ud.error = '';
    core.rerender();
    if (sub === 'zurueck') {
      await startJob(mv, false, {}, label, 'zurueck');
    } else {
      const r = await core.call('/api/umzug/' + sub, {});
      if (!r.ok) ud.error = (r.data && r.data.error) || 'Das ging nicht (Fehler ' + r.status + ').';
      else core.announce(label);
    }
    ud.busy = '';
    ud.sure = false;
    loadUd();
  }
  // Während der Umzugstag die Projekte hier aus hat oder dieser Server
  // abgelöst ist, gehen Probe-Umzug, Anhalten und Entfernen nicht.
  const dayLock = () => { const r = ud.rep; if (!r) return false; const p = r.umzugstag && r.umzugstag.phase; return !!r.abgeloest || p === 'laeuft' || p === 'dns' || p === 'fehler' || p === 'unterbrochen'; };
  const UD_STEP = { ok: ['ok', 'check', 'erledigt'], warnung: ['warn', 'warn', 'mit Hinweis'], fehler: ['err', 'x', 'fehlgeschlagen'], laeuft: ['acc', 'clock', 'läuft'], offen: ['idle', null, 'offen'], uebersprungen: ['idle', null, 'übersprungen'] };
  // inTool: im Werkzeug unter dem Lotsen. In Phase DNS steht der Text
  // „Jetzt bist du dran“ samt Knöpfen schon im Kasten oben.
  function dayBox(inTool) {
    const r = ud.rep;
    if (!r) return el('p', { class: ud.error ? 'gt-err' : 'ls-mut' }, ud.error || 'Lädt …');
    const d = r.umzugstag;
    const p = r.generalprobe;
    const run = mv.watching;
    const busy = run || !!ud.busy || !!mv.busy;
    const back = el('button', { type: 'button', class: 'btn', 'data-fid': 'ud-back', disabled: busy, onclick: () => dayCall('zurueck', 'Rückweg') }, ic('undo', 18), ud.busy === 'zurueck' ? 'Startet …' : 'Zurück zum alten Server');
    let band = null;
    if (r.abgeloest) {
      band = el('div', { class: 'zg-done bk-done', role: 'status' }, ic('done', 26), el('div', {},
        el('b', {}, 'Dieser Server ist abgelöst.'),
        el('p', {}, 'Seit ' + when(r.abgeloest.am) + ' zeigen deine Domains auf ' + r.abgeloest.durch + '; dort ist jetzt dein Leitstand. Die umgezogenen Projekte sind hier aus und warnen nicht, ihre Domains prüft der neue Server; Backups und Umzüge gehen hier nicht mehr. Lass diesen Server eine Woche als Reserve stehen, dann kannst du ihn beim Anbieter kündigen.'),
        el('p', { class: 'ls-mut' }, 'Geht drüben etwas schief: Stelle die DNS-Einträge zurück und wähle hier „Zurück zum alten Server“. Was drüben seitdem neu entstand, fehlt dann hier. Die Projekte auf ' + r.abgeloest.durch + ' laufen weiter; halte sie dort im Leitstand unter Projekte an, sonst laufen sie doppelt.'),
        el('div', { class: 'op-btns' }, back)));
    } else if (d && d.phase === 'dns' && !inTool) {
      band = el('div', { class: 'band b-info', role: 'status' },
        el('p', {}, el('b', {}, 'Jetzt bist du dran: '), 'Stelle bei deinem Domain-Anbieter deine Domains auf ' + (d.ip || d.host) + ' um, die Leitstand-Adresse zum Schluss. Der Lotse oben zeigt die Einträge zum Kopieren und hakt jede Domain ab. Zeigen alle dorthin, schließt HostDeck den Umzug von selbst ab, und der neue Leitstand übernimmt.'),
        el('div', { class: 'op-btns' }, back,
          ud.sure === 'fertig'
            ? [el('button', { type: 'button', class: 'btn btn-p', 'data-fid': 'ud-finish-ok', disabled: busy, onclick: () => dayCall('abschliessen', 'Umzug abgeschlossen') }, ic('check', 18), 'Ja, abschließen'),
              el('button', { type: 'button', class: 'btn', onclick: () => { ud.sure = false; core.rerender(); } }, 'Nein')]
            : el('button', { type: 'button', class: 'btn', 'data-fid': 'ud-finish', disabled: busy, onclick: () => { ud.sure = 'fertig'; core.rerender(); } }, ic('check', 18), 'Von Hand abschließen')),
        ud.sure === 'fertig' ? el('p', { class: 'ls-mut' }, 'Nur nötig, wenn eine Domain absichtlich woandershin zeigt und HostDeck deshalb nicht von selbst abschließt. Danach ist dieser Server abgelöst.') : null);
    } else if (d && (d.phase === 'fehler' || d.phase === 'unterbrochen')) {
      band = el('div', { class: 'band b-err', role: 'alert' },
        el('p', {}, el('b', {}, d.phase === 'unterbrochen' ? 'Der Umzugstag wurde unterbrochen. ' : 'Der Umzugstag ist stehen geblieben. '),
          r.aus_gelassen ? 'Die Projekte hier sind aus. Wähle „Zurück zum alten Server“, oder ' + (d.phase === 'unterbrochen' ? '' : 'behebe den Fehler und ') + 'starte den Umzugstag erneut (Formular „Umzugstag“ auf dieser Seite); der letzte Lauf gilt dann weiter.' : 'Die Projekte hier laufen.'),
        r.aus_gelassen ? el('div', { class: 'op-btns' }, back) : null);
    } else if (r.aus_gelassen && !run) {
      band = el('div', { class: 'band b-warn', role: 'status' },
        el('p', {}, plural(r.aus_gelassen, 'Container ist', 'Container sind') + ' seit einem letzten Lauf aus.'),
        el('div', { class: 'op-btns' }, back));
    }
    const probe = p
      ? el('p', {}, p.ok ? st('ok', 'Generalprobe bestanden') : st('warn', 'Generalprobe nicht bestanden'), ' ',
        el('span', { class: 'ls-mut' }, when(p.am) + ' auf ' + p.ziel + (p.ok ? '' : ': ' + (p.probleme || []).join('; '))))
      : el('p', { class: 'ls-mut' }, 'Noch keine Generalprobe: Ein Probe-Umzug, bei dem alles klappt, gilt drei Tage als Generalprobe.');
    const steps = d && d.phase !== 'fertig' && d.phase !== 'zurueck' ? el('ol', { class: 'ud-steps' }, d.schritte.map((x, i) => {
      const s = UD_STEP[x.stand] || UD_STEP.offen;
      return el('li', { class: 'ud-' + x.stand },
        el('span', { class: 'dot ' + s[0] }, s[1] ? ic(s[1], 14) : String(i + 1)),
        el('div', {}, el('b', {}, x.titel), el('span', { class: 'sr' }, ' (' + s[2] + ')'), x.hinweis ? el('span', { class: 'ls-mut blk' }, x.hinweis) : null));
    })) : null;
    const note = d && d.phase === 'zurueck' && d.hinweis ? el('p', { class: 'ls-mut' }, 'Letzter Umzugstag: ' + d.hinweis) : null;
    return el('div', { class: 'ls-stack sm' }, band, probe,
      !r.abgeloest && !(d && d.phase === 'dns') ? el('p', { class: 'ls-mut' }, r.bereit
        ? 'Den Umzugstag startest du im Formular „Umzugstag“ auf dieser Seite, mit denselben Angaben wie beim Probe-Umzug. Ab dem letzten Lauf sind deine Seiten aus, bis die Domains umgestellt sind; das Zurückspielen dauert etwa so lange wie beim Probe-Umzug.'
        : r.problem) : null,
      steps, note,
      ud.error ? el('p', { class: 'gt-err', role: 'alert' }, ic('warn', 16), el('span', {}, ud.error)) : null);
  }
  // only: nur diese Aktion zeigen (Lotse: genau ein Hauptknopf je
  // Schritt): plan, run, day oder copy. Ohne: alle.
  function moveForm(st, sv, only) {
    if (!mv.form) {
      const l = st.zuletzt || {};
      mv.form = { server: l.server || '', port: l.port && l.port !== 22 ? String(l.port) : '', benutzer: l.benutzer && l.benutzer !== 'root' ? l.benutzer : '' };
    }
    const f = mv.form;
    const run = mv.watching;
    const busy = sv.lauf && !run ? 'Geht erst, wenn „' + sv.lauf.was + '“ fertig ist.' : '';
    const off = !!busy || run || !!mv.busy || !st.bereit;
    const offCopy = !!busy || run || !!mv.busy || dayLock();
    const dayOK = ud.rep && ud.rep.bereit;
    const set = k => e => { f[k] = e.target.value; if (mv.check) { mv.check = ''; core.rerender(); } };
    const fld = (id, label, attrs, help) => el('div', { class: 'gt-fld ' + id }, el('label', { for: 'mv-' + id }, label),
      el('input', Object.assign({ class: 'fld', id: 'mv-' + id, 'data-fid': 'mv-' + id, type: 'text', autocomplete: 'off', spellcheck: 'false', autocapitalize: 'off', disabled: run }, attrs)), help || null);
    const serverBad = mv.check === 'server';
    const pwBad = mv.check === 'pw';
    const bpwBad = mv.check === 'bpw';
    // Kein „new-password“ und kein Name mit „passwort“: Sonst bietet der
    // Browser an, das Leitstand-Passwort durch dieses zu ersetzen.
    const pw = el('input', {
      class: 'fld', id: 'mv-pw', 'data-fid': 'mv-pw', type: mv.show ? 'text' : 'password', name: 'hostdeck-ziel-zugang', autocomplete: 'off', 'data-lpignore': 'true',
      spellcheck: 'false', autocapitalize: 'off', value: mv.pw, disabled: run, 'aria-invalid': pwBad ? 'true' : null, 'aria-describedby': 'mv-pw-h' + (pwBad ? ' mv-pw-e' : ''),
      oninput: e => { mv.pw = e.target.value; if (mv.check) { mv.check = ''; core.rerender(); } },
    });
    const eye = el('button', {
      type: 'button', class: 'gt-eye', 'aria-label': 'Passwort zeigen', 'aria-pressed': String(mv.show), 'aria-controls': 'mv-pw', 'data-fid': 'mv-eye', disabled: run,
      onclick: () => { mv.show = !mv.show; core.rerender(); },
    }, ic(mv.show ? 'eyeoff' : 'eye', 19));
    const form = el('form', { class: 'mv-form', novalidate: true, autocomplete: 'off', onsubmit: e => { e.preventDefault(); startMove(true); } },
      fld('server', 'Adresse des neuen Servers', {
        class: 'fld mono', inputmode: 'url', value: f.server, placeholder: '203.0.113.10', oninput: set('server'),
        'aria-invalid': serverBad ? 'true' : null, 'aria-describedby': serverBad ? 'mv-server-e' : null,
      }, serverBad ? el('p', { class: 'gt-err', id: 'mv-server-e', role: 'alert' }, ic('warn', 16), el('span', {}, 'Bitte die Adresse eingeben, wie in der Mail deines Anbieters.')) : null),
      fld('port', 'SSH-Port', { class: 'fld mono', inputmode: 'numeric', value: f.port, placeholder: '22', maxlength: 5, oninput: set('port') }),
      fld('user', 'Benutzer', { class: 'fld mono', value: f.benutzer, placeholder: 'root', maxlength: 32, oninput: set('benutzer') }),
      el('div', { class: 'gt-fld pw' }, el('label', { for: 'mv-pw' }, 'Passwort des neuen Servers'), el('div', { class: 'gt-pw' }, pw, eye),
        el('p', { class: 'zg-help', id: 'mv-pw-h' }, 'Geht nur für diesen Lauf an den neuen Server und wird nirgends gespeichert.'),
        pwBad ? el('p', { class: 'gt-err', id: 'mv-pw-e', role: 'alert' }, ic('warn', 16), el('span', {}, 'Bitte das Passwort eingeben; HostDeck meldet sich damit einmal an.')) : null),
      el('div', { class: 'gt-fld pw' }, el('label', { for: 'mv-bpw' }, 'Dein Backup-Passwort'),
        el('input', {
          class: 'fld', id: 'mv-bpw', 'data-fid': 'mv-bpw', type: 'password', name: 'hostdeck-sicherung-zugang', autocomplete: 'off', 'data-lpignore': 'true',
          spellcheck: 'false', autocapitalize: 'off', value: mv.bpw, disabled: run, 'aria-invalid': bpwBad ? 'true' : null, 'aria-describedby': 'mv-bpw-h' + (bpwBad ? ' mv-bpw-e' : ''),
          oninput: e => { mv.bpw = e.target.value; if (mv.check) { mv.check = ''; core.rerender(); } },
        }),
        el('p', { class: 'zg-help', id: 'mv-bpw-h' }, 'Vom Zettel oder aus dem Passwort-Manager. HostDeck prüft es nur und speichert es hier nicht; der neue Server braucht es, um das Backup zu öffnen.'),
        bpwBad ? el('p', { class: 'gt-err', id: 'mv-bpw-e', role: 'alert' }, ic('warn', 16), el('span', {}, 'Bitte dein Backup-Passwort eingeben.')) : null),
      el('button', { type: 'submit', hidden: true, tabindex: '-1', 'aria-hidden': 'true' }));
    const want = k => !only || only === k;
    const planBtn = el('button', { type: 'button', class: only === 'plan' ? 'btn btn-p' : 'btn', 'data-fid': 'mv-plan', disabled: off, onclick: () => startMove(true) }, ic('search', 18), mv.busy === 'plan' ? 'Startet …' : 'Probelauf');
    const runBtn = el('button', { type: 'button', class: 'btn btn-p', 'data-fid': 'mv-run', disabled: off || dayLock(), onclick: () => startMove(false) }, ic('umzug', 18), mv.busy === 'run' ? 'Startet …' : 'Probe-Umzug starten');
    return el('div', { class: 'ls-stack sm' },
      !st.bereit && only !== 'copy' ? el('div', { class: 'band b-warn band-row', role: 'status' }, el('p', {}, st.problem || 'Gerade geht kein Probe-Umzug.'), link('#/backup', 'Zum Backup', 'btn btn-sm', 'backup')) : null,
      form,
      mv.error ? el('p', { class: 'gt-err', role: 'alert' }, ic('warn', 16), el('span', {}, mv.error)) : null,
      want('plan') || want('run') ? el('div', { class: 'ls-row-end' },
        el('span', { class: 'ls-mut' }, busy || (run ? 'Ein Lauf läuft gerade.' : only === 'plan' ? 'Prüft den neuen Server; eingespielt wird nichts.' : st.backup ? 'Spielt das Backup von ' + when(st.backup) + ' ein.' : '')),
        el('div', { class: 'acts' }, want('plan') ? planBtn : null, want('run') ? runBtn : null)) : null,
      dayOK && want('day') ? el('div', { class: 'ls-row-end ud-start' },
        el('span', { class: 'ls-mut' }, ud.sure === 'start'
          ? el('b', {}, 'Wirklich jetzt? Ab dem letzten Lauf sind deine Seiten aus, bis du die Domains umgestellt hast' + (probeMinutes() ? ' (der Umzug selbst etwa ' + probeMinutes() + ' Minuten, so lange dauerte der Probe-Umzug, dazu ' + dnsWait(dn.rep) + ').' : '.') + (ttlMax(dn.rep) > 300 ? ' Deine Einträge gelten noch bis zu ' + ttlText(ttlMax(dn.rep)) + '; senk die TTL vorher auf 300.' : ''))
          : [el('b', {}, 'Umzugstag. '), 'Letzter Lauf hier, dann alles frisch auf den neuen Server, Server-Ebene mit deinen Haken, Prüfung jeder Seite. Danach stellst du die Domains um.']),
        el('div', { class: 'acts' }, ud.sure === 'start'
          ? [el('button', { type: 'button', class: 'btn', onclick: () => { ud.sure = false; core.rerender(); } }, 'Nein'),
            el('button', { type: 'button', class: 'btn btn-err', 'data-fid': 'mv-day-ok', disabled: off, onclick: () => startMove(false, true) }, ic('umzug', 18), mv.busy === 'umzugstag' ? 'Startet …' : 'Ja, Umzugstag starten')]
          : [
            // Nach einem Rückweg (etwa auf einen neu aufgesetzten Server) erst
            // wieder zur Probe; der Lotse rät dazu (Labor S5).
            ud.rep.umzugstag && ud.rep.umzugstag.phase === 'zurueck' ? el('button', { type: 'button', class: 'btn', 'data-fid': 'mv-run-again', disabled: off || dayLock(), onclick: () => startMove(false) }, ic('umzug', 18), mv.busy === 'run' ? 'Startet …' : 'Probe-Umzug wiederholen') : null,
            el('button', { type: 'button', class: only === 'day' ? 'btn btn-p' : 'btn', 'data-fid': 'mv-day', disabled: off, onclick: () => { ud.sure = 'start'; core.rerender(); } }, ic('umzug', 18), 'Umzugstag starten')])) : null,
      only === 'day' && !dayOK && ud.rep && ud.rep.problem ? el('p', { class: 'ls-note' }, ic('info', 16), el('span', {}, ud.rep.problem)) : null,
      want('copy') ? el('div', { class: 'ls-row-end' },
        el('span', { class: 'ls-mut' }, el('b', {}, 'Probe-Kopie auf dem neuen Server. '), 'Anhalten oder entfernen; beides zeigt vorher, was passiert.'),
        el('div', { class: 'acts' },
          el('button', { type: 'button', class: 'btn', 'data-fid': 'mv-halt', disabled: offCopy, onclick: () => startCopy('probe-anhalten') }, ic('stop', 18), mv.busy === 'probe-anhalten' ? 'Startet …' : 'Anhalten'),
          el('button', { type: 'button', class: 'btn btn-err', 'data-fid': 'mv-remove', disabled: offCopy, onclick: () => startCopy('probe-entfernen') }, ic('trash', 18), mv.busy === 'probe-entfernen' ? 'Startet …' : 'Entfernen'))) : null);
  }
  // DNS-Liste: Wohin zeigt jede Domain, die Caddy hier
  // bedient, laut ihrem zuständigen Nameserver? Am Umzugstag stellst du die
  // Einträge beim Anbieter deiner Domains um; ein Haken zeigt, was schon
  // auf den neuen Server zeigt.
  const dn = { rep: null, loading: false, at: 0, error: '', add: '', saving: false, addErr: '' };
  // Domains außerhalb von Caddy (Spielserver, Mail, VPN) trägst du von Hand
  // ein; die Liste prüft sie mit.
  async function saveDnExtra(list) {
    dn.saving = true;
    dn.addErr = '';
    core.rerender();
    const r = await core.call('/api/umzug/dns', { von_hand: list });
    dn.saving = false;
    if (r.ok) { dn.rep = r.data; dn.add = ''; core.announce('DNS-Liste gespeichert'); } else dn.addErr = (r.data && r.data.error) || 'Fehler ' + r.status;
    core.rerender();
  }
  async function loadDn() {
    if (dn.loading) return;
    dn.loading = true;
    core.rerender();
    const r = await core.call('/api/umzug/dns', null, true);
    dn.loading = false;
    dn.at = Date.now();
    if (r.ok) { dn.rep = r.data; dn.error = ''; } else dn.error = (r.data && r.data.error) || 'Fehler ' + r.status;
    core.announce(dn.error ? 'DNS-Prüfung fehlgeschlagen' : 'DNS geprüft');
    if (ui.page === 'umzug') core.rerender();
  }
  const DNS_STATE = { neu: ['ok', 'zeigt auf den neuen Server'], alt: ['idle', 'zeigt noch hierher'], gemischt: ['warn', 'teils neu, teils noch hier'], anders: ['warn', 'zeigt woandershin'], fehler: ['idle', 'kein Eintrag gefunden'] };
  const DNS_WHERE = { hier: 'dieser Server', neu: 'neuer Server', anders: 'woanders' };
  const ttlText = s => !s ? '' : s >= 172800 ? Math.round(s / 86400) + ' Tage' : s >= 3600 ? Math.round(s / 3600) + ' Std.' : s >= 60 ? Math.round(s / 60) + ' Min.' : s + ' s';
  // Größte Gültigkeit (TTL) der Einträge, die noch hierher zeigen: So lange
  // können Besucher nach dem Umstellen noch hier landen.
  function ttlMax(d) {
    let n = 0;
    for (const z of (d && d.domains) || []) if (z.stand !== 'neu') for (const e of z.eintraege || []) if (e.wohin === 'hier' && e.ttl > n) n = e.ttl;
    return n;
  }
  function dnsWait(d) {
    const n = ttlMax(d);
    return n ? 'bis zu ' + ttlText(n) + ', bis deine DNS-Änderungen überall ankommen (TTL deiner Einträge)' : 'die Zeit, bis deine DNS-Änderungen ankommen';
  }
  function ttlNote(d) {
    const n = ttlMax(d);
    if (n <= 300) return null;
    return el('div', { class: 'band b-warn', role: 'status', 'data-fid': 'lt-ttl' }, el('p', {}, el('b', {}, 'Deine DNS-Einträge gelten noch bis zu ' + ttlText(n) + ' (TTL ' + n + '). '),
      'Senke sie bei deinem Anbieter auf 300 Sekunden und warte danach ' + ttlText(n) + ', bevor du den Umzugstag startest. Sonst landen Besucher nach dem Umstellen bis zu ' + ttlText(n) + ' lang noch auf diesem Server, und hier sind die Projekte dann aus. Nur die TTL ändern, nicht das Ziel.'));
  }
  function dnsBox() {
    const r = dn.rep;
    const btn = el('button', { type: 'button', class: 'btn btn-sm', 'data-fid': 'dn-load', disabled: dn.loading, 'aria-busy': dn.loading ? 'true' : null, onclick: loadDn },
      ic('refresh', 16), dn.loading ? 'Fragt die Nameserver …' : r ? 'Neu prüfen' : 'DNS prüfen');
    const err = dn.error ? el('p', { class: 'gt-err', role: 'alert' }, ic('warn', 16), el('span', {}, dn.error)) : null;
    if (!r) {
      return el('div', { class: 'ls-stack sm' },
        el('p', { class: 'ls-mut' }, 'Noch nicht geprüft.'),
        err, el('div', { class: 'op-btns' }, btn));
    }
    const ds = r.domains || [];
    const done = ds.filter(d => d.stand === 'neu').length;
    const longTTL = ds.some(d => d.stand !== 'neu' && d.eintraege.some(e => e.ttl >= 3600));
    const v6here = ds.filter(d => d.eintraege.some(e => e.typ === 'AAAA' && e.wohin === 'hier')).map(d => d.domain);
    const viaNormal = ds.some(d => !d.nameserver && !d.fehler);
    const table = ds.length ? el('div', { class: 'ls-tw' }, el('table', { class: 'ls-t dn-t' },
      el('caption', { class: 'sr' }, 'Wohin deine Domains zeigen'),
      el('thead', {}, el('tr', {}, ['Domain', 'Stand', 'Einträge', 'Nameserver'].map(t => el('th', { scope: 'col' }, t)))),
      el('tbody', {}, ds.map(d => {
        const s = DNS_STATE[d.stand] || ['idle', d.stand];
        return el('tr', {},
          el('td', { class: 'mono' }, d.domain, d.von_hand ? el('span', { class: 'ls-mut blk' }, 'von Hand') : null, d.cname ? el('span', { class: 'ls-mut blk' }, 'Verweis auf ' + d.cname) : null),
          el('td', {}, d.stand === 'neu' ? el('span', { class: 'ls-st ok dn-ok' }, ic('check', 14), s[1]) : st(s[0], s[1])),
          el('td', {}, d.fehler ? el('span', { class: 'ls-mut' }, d.fehler) : d.eintraege.map((e, k) => [k ? el('br') : null,
            el('span', { class: 'mono' }, e.typ + ' ' + e.ip), ' ', el('span', { class: 'ls-mut' }, '· ' + DNS_WHERE[e.wohin] + (e.ttl ? ' · gilt ' + ttlText(e.ttl) : ''))])),
          el('td', { class: 'mono ls-mut' }, d.nameserver || 'normaler DNS-Weg'));
      })))) : el('p', { class: 'ls-mut' }, r.problem || 'Caddy bedient hier keine Domains.');
    return el('div', { class: 'ls-stack sm' },
      ds.length ? el('p', {}, el('b', {}, done + ' von ' + plural(ds.length, 'Domain', 'Domains')), ' zeigen auf den neuen Server' + (r.ziel ? ' (' + r.ziel + ')' : '') + '.') : null,
      !r.ziel ? el('p', { class: 'ls-mut' }, 'Noch kein neuer Server bekannt: Nach dem ersten Probelauf weiß HostDeck dessen Adresse.') : null,
      table,
      longTTL ? el('p', { class: 'ls-note' }, ic('info', 16), el('span', {}, 'Tipp: Stell die Gültigkeit (TTL) der Einträge beim Anbieter einen Tag vor dem Umzug auf 5 Minuten (300 Sekunden). Dann kommen deine Besucher am Umzugstag schnell beim neuen Server an.')) : null,
      v6here.length ? el('p', { class: 'ls-note' }, ic('warn', 16), el('span', {}, 'IPv6 (AAAA) zeigt noch hierher: ' + v6here.join(', ') + '. Beim Umzug auch diese Einträge ändern oder löschen, sonst landen manche Besucher weiter auf diesem Server.')) : null,
      viaNormal ? el('p', { class: 'ls-mut' }, 'Wo „normaler DNS-Weg“ steht, war der zuständige Nameserver nicht direkt erreichbar; die Antwort kann dann noch ein paar Minuten alt sein.') : null,
      dnExtra(r),
      err, el('div', { class: 'op-btns' }, btn));
  }
  function dnExtra(r) {
    const list = r.von_hand || [];
    const add = () => { const v = dn.add.trim(); if (v) saveDnExtra(list.concat([v])); };
    return el('div', { class: 'ls-stack sm dn-extra' },
      el('label', { for: 'dn-add' }, el('b', {}, 'Domains außerhalb von Caddy')),
      el('p', { class: 'ls-mut', id: 'dn-add-h' }, 'Etwa für Spielserver, Mail oder VPN. HostDeck kennt nur die Domains aus Caddy; was du hier einträgst, prüft die Liste mit, und der Umzug gilt erst als abgeschlossen, wenn auch sie umgestellt sind.'),
      list.length ? el('ul', { class: 'dn-extra-l' }, list.map(d => el('li', {}, el('span', { class: 'mono' }, d),
        el('button', { type: 'button', class: 'btn btn-sm', 'aria-label': d + ' entfernen', disabled: dn.saving, onclick: () => saveDnExtra(list.filter(x => x !== d)) }, ic('x', 14))))) : null,
      el('form', { class: 'dn-add', novalidate: true, onsubmit: e => { e.preventDefault(); add(); } },
        el('input', { class: 'fld mono', id: 'dn-add', 'data-fid': 'dn-add', type: 'text', placeholder: 'mc.example.de', autocomplete: 'off', spellcheck: 'false', autocapitalize: 'off',
          value: dn.add, 'aria-describedby': 'dn-add-h' + (dn.addErr ? ' dn-add-e' : ''), 'aria-invalid': dn.addErr ? 'true' : null, oninput: e => { dn.add = e.target.value; } }),
        el('button', { type: 'submit', class: 'btn btn-sm', disabled: dn.saving }, dn.saving ? 'Speichert …' : 'Hinzufügen')),
      dn.addErr ? el('p', { class: 'gt-err', id: 'dn-add-e', role: 'alert' }, ic('warn', 16), el('span', {}, dn.addErr)) : null);
  }
  // Server-Ebene: was außer den Projekten mitkommt. Zeitzone
  // und Kernel-Einstellungen von selbst, Aufgaben und Zugänge mit Haken.
  // Der Probe-Umzug prüft sie dort nur; übernommen werden sie am Umzugstag.
  const se = { rep: null, loading: false, saving: false, error: '', at: 0 };
  async function loadSe() {
    if (se.loading) return;
    se.loading = true;
    const r = await core.call('/api/umzug/server-ebene', null, true);
    se.loading = false;
    se.at = Date.now();
    if (r.ok) { se.rep = r.data; se.error = ''; } else if (r.status !== 404) se.error = (r.data && r.data.error) || 'Fehler ' + r.status;
    if (ui.page === 'umzug') core.rerender();
  }
  async function seToggle(key, on) {
    if (!se.rep || se.saving) return;
    const set = new Set(se.rep.angekreuzt);
    if (on) set.add(key); else set.delete(key);
    se.rep.angekreuzt = [...set];
    se.saving = true;
    core.rerender();
    const r = await core.call('/api/umzug/server-ebene', { angekreuzt: [...set] });
    se.saving = false;
    if (r.ok) { se.rep = r.data; se.error = ''; core.announce(on ? 'Angekreuzt' : 'Haken entfernt'); } else { se.error = errText(r, 'Speichern'); loadSe(); }
    core.rerender();
  }
  function seItem(x) {
    const on = se.rep.angekreuzt.includes(x.schluessel);
    return el('label', { class: 'cl-it' + (on ? ' is-on' : '') },
      el('input', { type: 'checkbox', checked: on, disabled: se.saving, 'data-fid': 'se-' + x.schluessel, onchange: e => seToggle(x.schluessel, e.target.checked) }),
      el('span', { class: 'cl-t' }, el('b', {}, x.titel), x.detail ? el('span', { class: 'ls-mut blk' }, x.detail) : null),
      el('span'));
  }
  function seBox() {
    if (!se.loading && (!se.at || Date.now() - se.at > 60000)) setTimeout(loadSe, 0);
    const r = se.rep;
    if (!r) return el('p', { class: se.error ? 'gt-err' : 'ls-mut' }, se.error || 'Lädt …');
    if (r.problem) return el('p', { class: 'ls-mut' }, r.problem);
    const auto = r.punkte.filter(x => x.art === 'automatisch' && !x.von_hand);
    const manual = r.punkte.filter(x => x.von_hand);
    const groups = [['Zeitgesteuerte Aufgaben', r.punkte.filter(x => x.waehlbar && x.art === 'aufgabe')], ['Zugänge', r.punkte.filter(x => x.waehlbar && x.art === 'zugang')]].filter(g => g[1].length);
    return el('div', { class: 'ls-stack sm' },
      auto.length ? el('div', { class: 'cl-g-set', role: 'group', 'aria-label': 'Kommt von selbst mit' }, el('p', { class: 'sc-g' }, 'Kommt von selbst mit'),
        el('ul', { class: 'ls-legend' }, auto.map(x => el('li', {}, ic('check', 14), el('span', {}, el('b', {}, x.titel), x.detail ? el('small', {}, x.detail) : null))))) : null,
      groups.length ? el('div', { class: 'cl-list' }, groups.map(([t, list]) => el('fieldset', { class: 'cl-g-set' }, el('legend', {}, t + ' · ' + list.length), list.map(seItem))))
        : el('p', { class: 'ls-mut' }, 'Keine eigenen Aufgaben und Zugänge gefunden.'),
      manual.length || r.von_hand.length ? el('div', { class: 'cl-g-set', role: 'group', 'aria-label': 'Von Hand' }, el('p', { class: 'sc-g' }, 'Von Hand'),
        el('ul', { class: 'ls-legend' },
          manual.map(x => el('li', {}, ic('info', 14), el('span', {}, el('b', {}, x.titel), el('small', {}, x.von_hand), x.detail ? el('small', {}, x.detail) : null))),
          r.von_hand.length ? el('li', {}, ic('info', 14), el('span', {}, el('b', {}, plural(r.von_hand.length, 'geänderte Datei', 'geänderte Dateien') + ' in /etc'),
            el('small', {}, 'Ubuntu und Debian haben verschiedene; vergleiche sie von Hand.'), moreList(r.von_hand, 'Dateien zeigen'))) : null)) : null,
      se.error ? el('p', { class: 'gt-err', role: 'alert' }, ic('warn', 16), el('span', {}, se.error)) : null);
  }
  const moveHelp = () => [
    el('p', {}, 'Das Passwort des neuen Servers geht nur für diesen Lauf dorthin und wird nirgends gespeichert.'),
    el('p', {}, 'Dein Backup-Passwort bekommt der neue Server, um das Backup zu öffnen (dort in /root/.hostdeck/passwort, nur für root lesbar). So reicht dein Leitstand-Passwort allein nicht, um deine Daten auf einen fremden Server zu holen.'),
    el('p', {}, 'Probe-Kopie: Ein neuer Probe-Umzug frischt sie auf und überträgt nur, was sich geändert hat. Anhalten stoppt die Projekte dort, Entfernen löscht, was der Probe-Umzug angelegt hat. Das Backup-Passwort brauchst du dafür nicht.')];
  // Umzug als Modus umschalten (Server-Seite, ins Protokoll).
  const md = { busy: false, error: '' };
  async function setMovePlanned(on) {
    const pk = (core.svc() || {}).probe_kopie;
    let anyway = false;
    if (!on && pk && !pk.angehalten) {
      anyway = await clDialog(done => [
        el('h2', { id: 'cl-h' }, 'Die Probe-Kopie läuft noch'),
        el('p', { id: 'cl-d', class: 'gt-lead' }, 'Auf ' + pk.server + ' läuft seit ' + when(pk.seit) + ' die Probe-Kopie vom Probe-Umzug. Ihre Projekte laufen dort doppelt, etwa Bots oder Spielserver. Halte sie vorher an oder entferne sie: auf dieser Seite unter Werkzeuge, „Probe-Kopie anhalten oder entfernen“.'),
        el('p', { class: 'ls-note' }, ic('info', 16), el('span', {}, 'Schaltest du trotzdem um, bleibt sie stehen; der Leitstand erinnert unter Umzug daran.')),
        el('div', { class: 'gt-acts' }, el('button', { type: 'button', class: 'btn btn-p', onclick: () => done(false) }, 'Abbrechen'),
          el('button', { type: 'button', class: 'btn', onclick: () => done(true) }, 'Trotzdem umschalten'))]);
      if (!anyway) { core.rerender(); return; }
    }
    md.busy = true; md.error = '';
    core.rerender();
    const r = await core.call('/api/umzug/modus', anyway ? { geplant: on, trotzdem: true } : { geplant: on });
    md.busy = false;
    if (!r.ok) md.error = (r.data && r.data.error) || 'Das ging nicht (Fehler ' + r.status + ').';
    else { core.announce(on ? 'Umzug geplant' : 'Kein Umzug geplant'); await core.refreshLauf(); }
    refreshChrome();
    core.rerender();
  }
  // Eine Probe-Kopie, die drüben noch steht (auch im Alltag sichtbar).
  function probeBand(lotse) {
    const pk = (core.svc() || {}).probe_kopie;
    if (!pk) return null;
    return el('div', { class: 'band b-info', role: 'status', 'data-fid': 'md-probe' }, el('p', {},
      el('b', {}, pk.angehalten ? 'Auf ' + pk.server + ' steht noch die angehaltene Probe-Kopie. ' : 'Auf ' + pk.server + ' läuft noch die Probe-Kopie. '),
      (pk.angehalten ? 'Ihre Projekte sind gestoppt; die Dateien liegen dort seit ' : 'Ihre Projekte laufen dort doppelt, seit ') + when(pk.seit) + (lotse ? '. Anhalten oder entfernen geht unten unter Werkzeuge.' : '. Anhalten oder entfernen geht hier, sobald du den Umzug planst (unter Werkzeuge).')));
  }
  function pageUmzugAlltag(m) {
    return [head({ i: 'umzug', k: 'Umzug · gerade keiner geplant', h: ['Gerade steht ', em('ok', 'kein Umzug'), ' an.'], show: true,
      p: 'Der Leitstand zeigt deshalb den Betrieb: Übersicht, Projekte, Überwachung und Speicherplatz. Auswahl und Backup melden sich nur auf ihren eigenen Seiten, und kurz nach dem Herunterladen löscht HostDeck ein Backup vom Server. Willst du wieder umziehen, plane den Umzug; dann führt HostDeck durch Auswahl, Backup und Probe-Umzug, und die letzten gültigen Backups bleiben bis zum Umzug auf dem Server.',
      a: el('button', { type: 'button', class: 'btn btn-p', 'data-fid': 'md-on', disabled: md.busy, onclick: () => setMovePlanned(true) }, ic('umzug', 18), md.busy ? 'Schaltet um …' : 'Umzug planen') }),
    md.error ? el('p', { class: 'gt-err', role: 'alert' }, ic('warn', 16), el('span', {}, md.error)) : null,
    probeBand(),
    el('div', { class: 'ls-grid eq' },
      finishBox(),
      sec('Wenn du umziehen willst', vsteps([
        ['Umzug planen', 'schaltet Hinweise zu Auswahl, Backup und Umzug wieder ein', false],
        ['Auswahl prüfen', 'was mitkommt; Neues seit dem letzten Mal trägt HostDeck mit Vorschlag ein', m.auswahlDone],
        ['Backup erstellen', 'bleibt auf dem Server, bis der Umzug durch ist', false],
        ['Probe-Umzug und Umzugstag', 'wie beim letzten Mal, Schritt für Schritt hier auf der Seite', false],
      ]), { cls: 'c6' }),
      sec('Checkliste für einen neuen Server', moveChecklist(m), { cls: 'c6', sub: 'Dinge, die nicht als Datei mitkommen' }))];
  }
  // ---------- Umzugs-Lotse ----------
  // Solange ein Umzug geplant ist, führt die Seite Umzug in zehn Schritten:
  // oben „Schritt X von 10“, darunter der Kasten „Jetzt dran“ mit genau
  // einem Hauptknopf, die Schrittliste zum Nachlesen und die Werkzeuge
  // (zu, im passenden Schritt offen). Jeder Schritt ist „erledigt“ aus
  // Daten, die HostDeck schon hat; nur was nirgends steht (Probelauf, „Passt
  // so“, bewusst übersprungen), hält lotse.json im Ordner umzug fest.
  const lo = { rep: null, loading: false, at: 0, error: '', busy: '', why: '', more: '', tools: new Set(), steps: new Set() };
  async function loadLo() {
    if (lo.loading) return;
    lo.loading = true;
    const r = await core.call('/api/umzug/lotse', null, true);
    lo.loading = false;
    lo.at = Date.now();
    if (r.ok) { lo.rep = r.data || {}; lo.error = ''; } else if (r.status !== 404) lo.error = errText(r, 'Laden');
    if (ui.page === 'umzug' || ui.page === 'lage') core.rerender();
  }
  async function setLo(body, label) {
    if (lo.busy) return;
    lo.busy = Object.keys(body)[0];
    lo.error = '';
    core.rerender();
    const r = await core.call('/api/umzug/lotse', body);
    lo.busy = '';
    if (r.ok) { lo.rep = r.data || {}; core.announce(label); } else lo.error = errText(r, 'Speichern');
    core.rerender();
  }
  // Die Daten, aus denen der Lotse rechnet; DNS fragt die Nameserver und
  // dauert, darum nur alle fünf Minuten von selbst (Neu prüfen geht immer).
  function lotseLoad(withDns) {
    if (!mv.loading && !mv.watching && ((!mv.rep && !mv.error) || Date.now() - mv.at > 10000)) loadJob(mv);
    if (!ud.loading && (!ud.at || Date.now() - ud.at > 10000)) loadUd();
    if (!lo.loading && (!lo.at || Date.now() - lo.at > 10000)) loadLo();
    if (withDns && !dn.loading && !dn.error && (!dn.rep || Date.now() - (dn.at || 0) > 300000)) setTimeout(loadDn, 0);
  }
  const sameSet = (a, b) => a.length === b.length && a.every(x => b.includes(x));
  const GP_HOURS = 72;
  const LT_TITLES = ['Umzug planen', 'Auswahl prüfen', 'Backup erstellen', 'Neuer Server und Probelauf', 'DNS vorbereiten', 'Generalprobe', 'Umzugstag vorbereiten', 'Umzugstag', 'Domains umstellen', 'Abschluss auf dem neuen Server'];
  // Rechnet aus, welcher Schritt dran ist. Ohne Seite Umzug (etwa für
  // „Braucht dich“) reichen die Daten, die schon geladen sind.
  function lotse(m) {
    const sv = core.svc() || {};
    const st = mv.rep ? mv.rep.stand : null;
    const job = mv.rep && mv.rep.auftrag;
    const art = moveArt(job);
    const okJob = a => !!(job && !job.laeuft && art === a && job.code === 0);
    const ur = ud.rep || {};
    const day = ur.umzugstag || null;
    const phase = day ? day.phase : '';
    const L = lo.rep || {};
    const gp = ur.generalprobe || null;
    const gpLeft = gp && gp.ok ? new Date(gp.am).getTime() + GP_HOURS * 3600e3 - Date.now() : 0;
    const d = dn.rep;
    const own = (d && d.leitstand) || (sv.zugang && sv.zugang.adresse) || '';
    const saved = m.s.manifest.saved ? new Date(m.s.manifest.saved).getTime() : 0;
    const dayStarted = !!ur.abgeloest || (!!phase && phase !== 'zurueck');
    const auswahl = m.auswahlDone && !m.dirty && !m.s.manifest.added.length;
    const backupOK = !!(st && st.bereit && st.backup && new Date(st.backup).getTime() >= saved);
    const probe = !!(L.probelauf || gp || okJob('probelauf') || okJob('probe-umzug'));
    const test = d && d.testadresse;
    const testOK = !!(L.ohne_testadresse || (test && test.stand === 'neu'));
    const gpOK = !!(gp && gp.ok && gpLeft > 0);
    const seOK = !!(L.passt_so && se.rep && sameSet(L.passt_so_haken || [], se.rep.angekreuzt || []));
    const done = [
      !m.alltag,
      dayStarted || auswahl,
      dayStarted || backupOK,
      dayStarted || probe,
      dayStarted || testOK,
      dayStarted || gpOK,
      dayStarted || seOK,
      phase === 'dns' || !!ur.abgeloest,
      !!ur.abgeloest,
      false,
    ];
    const now = done.findIndex(x => !x);
    return { n: now, done, st, job, art, ur, day, phase, L, gp, gpLeft, gpExpired: !!(gp && gp.ok && gpLeft <= 0), d, own, test, backupOK, probe, okJob };
  }
  // Ein Satz zum Ergebnis eines erledigten Schritts.
  function lotseResult(i, x, m) {
    const L = x.L;
    switch (i) {
      case 0: return 'Umzug geplant; Backups bleiben bis nach dem Umzug auf dem Server.';
      case 1: return m.mstate === 'vorhanden' ? 'Auswahl gespeichert, nichts offen.' : 'Auswahl stand fest.';
      case 2: return x.st && x.st.backup ? 'Gültiges Backup von ' + when(x.st.backup) + '.' : 'Backup war fertig.';
      case 3: return L.probelauf ? (L.probelauf.art === 'probe-umzug' ? 'Probe-Umzug' : 'Probelauf') + ' ohne Fehler auf ' + L.probelauf.ziel + ', ' + when(L.probelauf.am) + '.' : 'Probelauf ohne Fehler.';
      case 4: return L.ohne_testadresse ? 'Übersprungen: ' + L.ohne_testadresse : x.test ? x.test.domain + ' zeigt auf den neuen Server.' : 'Erledigt.';
      case 5: return x.gp ? 'Generalprobe bestanden ' + when(x.gp.am) + '.' : 'Erledigt.';
      case 6: return L.passt_so ? 'Server-Ebene bestätigt ' + when(L.passt_so) + '.' : 'Erledigt.';
      case 7: return x.day ? 'Umzugstag ' + when(x.day.gestartet) + ': alles auf ' + (x.day.ip || x.day.host) + '.' : 'Erledigt.';
      case 8: return x.ur.abgeloest ? 'Abgelöst ' + when(x.ur.abgeloest.am) + ' (' + (x.ur.abgeloest.wie || 'DNS') + ').' : 'Erledigt.';
    }
    return '';
  }
  // Was jeder Schritt tut, zum Nachlesen in der Schrittliste.
  const LT_ABOUT = [
    'Schaltet die Hinweise zu Auswahl, Backup und Umzug ein. Backups bleiben bis nach dem Umzug auf dem Server, weil der Umzug sie von dort einspielt.',
    'Was mitkommt, was hier bleibt. Offene und nicht mehr gefundene Einträge musst du entscheiden; sonst startet kein Backup.',
    'Ein gültiges Backup ist das Sicherheitsnetz und die Grundlage der Generalprobe. Am Umzugstag macht HostDeck selbst noch einen letzten Lauf mit gestoppten Projekten.',
    'HostDeck meldet sich per SSH am neuen Server an und prüft ihn. Eingespielt wird nichts.',
    'Ein einziger DNS-Eintrag für die Testadresse, damit du die Probe-Kopie des Leitstands drüben öffnen kannst. Alle anderen Einträge bleiben bis zum Umzugstag, wie sie sind.',
    'Ein Probe-Umzug, bei dem alles klappt: Backup drüben einspielen, alles starten, jede Seite prüfen. Dieser Server läuft dabei weiter. Gilt drei Tage.',
    'Zeitgesteuerte Aufgaben und Zugänge mit Haken, dazu die erwartete Ausfallzeit. Ein Backup auf deinem PC schadet nicht, weil der alte Server später gekündigt wird.',
    'Letzter Lauf hier mit gestoppten Projekten, dann alles frisch auf den neuen Server, Server-Ebene mit deinen Haken, Prüfung jeder Seite.',
    'Jetzt stellst du die DNS-Einträge beim Anbieter um, die Leitstand-Adresse zuletzt. HostDeck hakt jede Domain ab und schließt ab, sobald alle umgestellt sind.',
    'Auf dem neuen Leitstand: alte Tabs schließen, erstes Backup dort, Testadresse löschen, TTL zurück. Der alte Server bleibt eine Woche Reserve.',
  ];
  function copyBtn(text, what) {
    return el('button', {
      type: 'button', class: 'btn btn-sm btn-ic lt-cp', 'aria-label': what + ' kopieren', title: 'Kopieren',
      onclick: async e => {
        const b = e.currentTarget;
        try { await navigator.clipboard.writeText(text); b.classList.add('is-done'); core.announce('Kopiert: ' + text); setTimeout(() => b.classList.remove('is-done'), 1500); } catch (x) { core.announce('Kopieren ging nicht; bitte von Hand markieren.'); }
      },
    }, ic('copy', 14));
  }
  // DNS-Tabelle: Name · Typ · Wert · TTL, mit Kopierknopf je Wert.
  function dnsRows(rows, caption) {
    return el('div', { class: 'ls-tw' }, el('table', { class: 'ls-t lt-dns' },
      el('caption', { class: 'sr' }, caption),
      el('thead', {}, el('tr', {}, ['Name', 'Typ', 'Wert', 'TTL', 'Stand'].map(t => el('th', { scope: 'col' }, t)))),
      el('tbody', {}, rows.map(r => el('tr', { class: r.done ? 'is-done' : '' },
        el('td', { class: 'mono' }, el('span', { class: 'lt-v' }, r.name, copyBtn(r.name, 'Name ' + r.name))),
        el('td', { class: 'mono' }, r.type),
        el('td', { class: 'mono' }, r.value ? el('span', { class: 'lt-v' }, r.value, copyBtn(r.value, 'Wert ' + r.value)) : el('span', { class: 'ls-mut' }, r.valueText || '')),
        el('td', { class: 'mono' }, r.ttl || ''),
        el('td', {}, r.state || null))))));
  }
  // missing: Ein fehlender Eintrag ist hier erwartet (Testadresse vor dem
  // Anlegen), also neutral statt rot.
  const dnsSt = (x, missing) => !x ? st('idle', 'noch nicht geprüft') : x.stand === 'neu' ? el('span', { class: 'ls-st ok dn-ok' }, ic('check', 14), 'zeigt auf den neuen Server')
    : x.stand === 'fehler' && missing ? st('idle', missing)
      : st(...(DNS_STATE[x.stand] || ['idle', x.stand]));
  // Die Adressen dieses Servers, auf die DNS-Einträge zeigen (ohne die
  // Docker-Netze, die auch zu den eigenen Adressen gehören).
  const hereIPs = d => [...new Set((d.domains || []).flatMap(x => (x.eintraege || []).filter(e => e.wohin === 'hier').map(e => e.ip)))];
  const newIPs = (d, v6) => ((d && d.ziel_ips) || []).filter(ip => v6 ? ip.includes(':') : !ip.includes(':'));
  function nsLine(d) {
    const z = (d && d.zonen) || [];
    if (!z.length) return null;
    return el('p', { class: 'ls-mut' }, 'Ändern bei deinem Domain-Anbieter, also dort, wo diese Nameserver verwaltet werden: ',
      z.map((x, i) => [i ? '; ' : '', el('b', {}, x.zone), ' (' + x.nameserver.join(', ') + ')']), '.');
  }
  // Die Zeilen, die am Umzugstag umgestellt werden: A auf die neue IP,
  // AAAA auf die neue IPv6 oder löschen.
  function switchRows(list, d, ip) {
    const v4 = ip && !ip.includes(':') ? [ip] : newIPs(d, false);
    const v6 = newIPs(d, true);
    const rows = [];
    for (const x of list) {
      const recs = x.eintraege || [];
      const has6 = recs.some(e => e.typ === 'AAAA');
      const ttl = Math.max(0, ...recs.map(e => e.ttl || 0));
      if (x.cname) { rows.push({ name: x.domain, type: 'CNAME', valueText: 'Verweis auf ' + x.cname + ': dort umstellen', state: dnsSt(x), done: x.stand === 'neu' }); continue; }
      rows.push({ name: x.domain, type: 'A', value: v4[0] || '', valueText: 'IPv4 des neuen Servers', ttl: ttl ? ttlText(ttl) : '', state: dnsSt(x), done: x.stand === 'neu' });
      if (has6) rows.push({ name: x.domain, type: 'AAAA', value: v6[0] || '', valueText: 'IPv6 des neuen Servers oder löschen', ttl: '', state: recs.some(e => e.typ === 'AAAA' && e.wohin === 'hier') ? st('warn', 'zeigt noch hierher') : null, done: !recs.some(e => e.typ === 'AAAA' && e.wohin === 'hier') });
    }
    return rows;
  }
  // Vorschläge für Domains außerhalb von Caddy (Zone, www, MX, SRV).
  function dnsSuggest(d) {
    const list = (d && d.vorschlaege) || [];
    if (!list.length) return null;
    return el('div', { class: 'ls-stack sm lt-sug' },
      el('p', {}, el('b', {}, 'Zeigen noch hierher, stehen aber in keiner Liste: '), 'HostDeck hat die Zonen, www, die Mail-Server (MX) und übliche SRV-Einträge gefragt. Gehören sie zu diesem Server, übernimm sie; ' + (ud.rep && ud.rep.umzugstag && ud.rep.umzugstag.phase === 'dns' ? 'dann stehen sie oben mit in der Liste zum Umstellen.' : 'dann stellst du sie am Umzugstag mit um.')),
      el('ul', { class: 'dn-extra-l' }, list.map(x => el('li', {}, el('span', { class: 'mono' }, x.domain),
        el('button', { type: 'button', class: 'btn btn-sm', disabled: dn.saving, 'data-fid': 'lt-sug-' + x.domain, onclick: () => saveDnExtra(((d.von_hand) || []).concat([x.domain])) }, 'Übernehmen')))));
  }
  // Generalprobe: Restzeit lesbar.
  // Dauer des letzten Probe-Umzugs in Minuten (0: unbekannt, etwa nach
  // einem Neustart des Dienstes).
  function probeMinutes() {
    const j = mv.rep && mv.rep.auftrag;
    if (!j || j.laeuft || !j.ende || moveArt(j) !== 'probe-umzug') return 0;
    return Math.max(1, Math.round((new Date(j.ende) - new Date(j.start)) / 60000));
  }
  function leftText(ms) {
    const h = Math.floor(ms / 3600e3);
    return h >= 24 ? 'noch ' + Math.floor(h / 24) + ' Tage ' + (h % 24) + ' Stunden' : h >= 1 ? 'noch ' + h + ' Stunden' : 'noch ' + Math.max(1, Math.round(ms / 60e3)) + ' Minuten';
  }
  // Empfehlung je Punkt der Server-Ebene: Nennt die Aufgabe den Ordner
  // oder einen Container eines Projekts, das mitkommt, ist sie empfohlen;
  // eines, das hier bleibt, nicht; sonst neutral.
  function seAdvice(x, m) {
    if (x.art !== 'aufgabe') return null;
    const text = (x.titel || '') + ' ' + (x.detail || '');
    for (const p of m.projects) {
      const names = [];
      for (const sv of p.services || []) for (const c of sv.containers || []) names.push(c);
      const hit = (p.path && p.path.length > 1 && text.includes(p.path)) || names.some(c => new RegExp('(^|[^\\w.-])' + c.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '($|[^\\w.-])').test(text));
      if (!hit) continue;
      if (p.decision === 'take') return ['ok', 'empfohlen: gehört zu ' + p.name + ', das mitkommt'];
      if (p.decision === 'skip') return ['idle', 'eher nicht: ' + p.name + ' bleibt hier'];
    }
    return null;
  }
  function seBoxAdvice(m) {
    const box = seBox();
    if (!se.rep || se.rep.problem) return box;
    for (const lab of box.querySelectorAll('label.cl-it')) {
      const inp = lab.querySelector('input');
      const key = inp && inp.getAttribute('data-fid') ? inp.getAttribute('data-fid').slice(3) : '';
      const x = se.rep.punkte.find(p => p.schluessel === key);
      const a = x ? seAdvice(x, m) : null;
      if (a) lab.querySelector('.cl-t').append(el('span', { class: 'blk' }, st(a[0], a[1])));
    }
    return box;
  }
  function stepBox(i, x, m) {
    const sv = core.svc() || {};
    const L = x.L;
    const d = x.d;
    const busy = !!lo.busy;
    const card = (title, why, body, main, more, running) => el('section', { class: 'ls-sec lt-now', 'aria-labelledby': 'lt-now-h' },
      el('header', { class: 'ls-sec-h' }, el('h2', { id: 'lt-now-h' }, running ? title : 'Jetzt dran: ' + title)),
      el('div', { class: 'ls-sec-b ls-stack sm' }, why ? el('p', { class: 'lt-why' }, why) : null, body, main ? el('div', { class: 'lt-main' }, main) : null,
        // Merkt sich auf- oder zugeklappt, sonst klappt es beim Neuzeichnen
        // alle paar Sekunden zu.
        more ? el('details', { class: 'lt-more', open: lo.more === title, ontoggle: e => { lo.more = e.currentTarget.open ? title : (lo.more === title ? '' : lo.more); } }, el('summary', {}, 'Was passiert dabei?'), el('div', { class: 'ls-stack sm' }, more)) : null,
        lo.error ? el('p', { class: 'gt-err', role: 'alert' }, ic('warn', 16), el('span', {}, lo.error)) : null));
    // Läuft ein Lauf, zeigt der Kasten den Lauf.
    if (mv.watching && x.job) {
      return card(MOVE_NAME[x.art] + ' läuft', 'Du kannst die Seite offen lassen oder später wiederkommen; der Lauf geht auf dem Server weiter.',
        [x.art === 'umzugstag' || x.art === 'rueckweg' ? dayBox() : null, jobView(x.job, mv, MOVE_TEXT)], null, null, true);
    }
    const fail = x.phase === 'fehler' || x.phase === 'unterbrochen';
    // Erneut starten geht direkt hier; einen Abschnitt „Neuer Server“ gibt es seit dem Lotsen nicht mehr (Labor S5).
    if (fail) return card('Umzugstag steht', null, [dayBox(), x.st ? moveForm(x.st, sv, 'day') : null], null, null);
    switch (i) {
      case 1: {
        const why = m.mstate !== 'vorhanden' ? 'Die Auswahl ist noch nicht gespeichert.' : m.open ? plural(m.open, 'Eintrag ist', 'Einträge sind') + ' offen.' : m.gone ? plural(m.gone, 'Eintrag wurde', 'Einträge wurden') + ' nicht mehr gefunden: entfernen und speichern.' : m.s.manifest.added.length ? plural(m.s.manifest.added.length, 'neuer Eintrag steht', 'neue Einträge stehen') + ' mit Vorschlag drin: prüfen und speichern.' : 'Änderungen warten aufs Speichern.';
        return card('Auswahl prüfen', why + ' Erst wenn nichts offen ist, startet ein Backup.', null, link('#/auswahl', 'Zur Auswahl', 'btn btn-p', 'auswahl'),
          [el('p', {}, 'In der Auswahl entscheidest du je Projekt, ob es mitkommt. Was du neu entscheidest, gilt ab dem nächsten Backup.')]);
      }
      case 2: {
        const stale = x.st && x.st.backup && !x.backupOK && x.st.bereit;
        return card('Backup erstellen', stale ? 'Du hast die Auswahl nach dem letzten Backup gespeichert; das nächste Backup nimmt sie mit.' : (x.st && x.st.problem) || 'Noch kein gültiges Backup auf dem Server.',
          el('p', { class: 'ls-mut' }, 'Dieses Backup spielt die Generalprobe drüben ein. Am Umzugstag macht HostDeck selbst noch einen letzten Lauf. Herunterladen kannst du es zusätzlich; solange der Umzug geplant ist, löscht HostDeck danach nichts vom Server.'),
          link('#/backup', 'Zum Backup', 'btn btn-p', 'backup'),
          [el('p', {}, 'Das Backup startest du auf der Seite Backup. Es prüft vorher die Auswahl und am Ende sich selbst; nur ein gültiges zählt.')]);
      }
      case 3:
        if (!x.st) return card('Neuer Server und Probelauf', null, el('p', { class: mv.error ? 'gt-err' : 'ls-mut' }, mv.error || 'Lädt …'), null, null);
        return card('Neuer Server und Probelauf', 'Trag den neuen Server ein: frisch mit Ubuntu oder Debian, SSH mit Passwort. Der Probelauf prüft ihn; eingespielt wird nichts.',
          moveForm(x.st, sv, 'plan'), null, moveHelp());
      case 4: {
        if (!d) return card('DNS vorbereiten', 'HostDeck fragt die zuständigen Nameserver …', el('p', { class: dn.error ? 'gt-err' : 'ls-mut' }, dn.error || 'Lädt …'), null, null);
        const ip = newIPs(d, false)[0] || d.ziel || '';
        const skip = el('div', { class: 'ls-stack sm' },
          el('label', { for: 'lt-why' }, 'Warum ohne Testadresse? (optional)'),
          el('input', { class: 'fld', id: 'lt-why', 'data-fid': 'lt-why', type: 'text', maxlength: 200, value: lo.why, oninput: e => { lo.why = e.target.value; } }),
          el('div', { class: 'op-btns' }, el('button', { type: 'button', class: 'btn btn-sm', 'data-fid': 'lt-skip', disabled: busy, onclick: () => setLo({ ohne_testadresse: lo.why.trim() || 'ohne Begründung' }, 'Schritt übersprungen') }, 'Ohne Testadresse weiter')));
        if (!d.leitstand) {
          return card('DNS vorbereiten', 'Dein Leitstand hat keine eigene Domain. Die Probe-Kopie öffnest du dann über den Notausgang des neuen Servers (sudo hostdeck notausgang); eine Testadresse brauchst du nicht.',
            [nsLine(d), dnsSuggest(d), dnExtra(d)], el('button', { type: 'button', class: 'btn btn-p', 'data-fid': 'lt-skip', disabled: busy, onclick: () => setLo({ ohne_testadresse: 'Der Leitstand hat keine eigene Domain.' }, 'Weiter ohne Testadresse') }, ic('next', 18), 'Weiter ohne Testadresse'), null);
        }
        const t = x.test;
        const longTTL = (d.domains || []).some(z => z.eintraege.some(e => e.ttl > 300));
        return card('DNS vorbereiten', 'Leg bei deinem Domain-Anbieter genau diesen einen Eintrag an. Alle anderen noch nicht ändern, sonst landen Besucher auf der Probe.',
          [dnsRows([{ name: t.domain, type: 'A', value: ip, valueText: 'erst nach dem Probelauf bekannt', ttl: '300', state: dnsSt(t, 'noch nicht angelegt'), done: t.stand === 'neu' }].concat(newIPs(d, true).length ? [{ name: t.domain, type: 'AAAA', value: newIPs(d, true)[0], ttl: '300', state: null }] : []), 'Eintrag für die Testadresse'),
            nsLine(d),
            longTTL ? el('p', { class: 'ls-note' }, ic('info', 16), el('span', {}, 'Einige deiner Einträge gelten länger als 5 Minuten (TTL). Stell sie jetzt auf 300 Sekunden; dann kommen Besucher am Umzugstag schnell beim neuen Server an. Nur die TTL ändern, nicht das Ziel.')) : null,
            el('p', {}, el('b', {}, 'Domains außerhalb von Caddy: '), 'Alle Einträge, die bei deinem Anbieter auf ' + (hereIPs(d).join(' oder ') || 'diesen Server') + ' zeigen, gehören in die Liste unten, etwa für Spielserver, Mail oder VPN. Nur dann stellst du sie am Umzugstag mit um.'),
            dnsSuggest(d), dnExtra(d),
            el('details', { class: 'lt-more' }, el('summary', {}, 'Ohne Testadresse weiter'), el('p', { class: 'ls-mut' }, 'Geht, wenn du die Probe-Kopie über den Notausgang öffnest. Die Seitenprüfung des Probe-Umzugs läuft trotzdem.'), skip)],
          el('button', { type: 'button', class: 'btn btn-p', 'data-fid': 'lt-dns', disabled: dn.loading, onclick: loadDn }, ic('refresh', 18), dn.loading ? 'Fragt die Nameserver …' : 'Neu prüfen'),
          [el('p', {}, 'HostDeck fragt den zuständigen Nameserver direkt; dein Rechner kann noch ältere Antworten kennen. Sobald ' + t.domain + ' auf ' + (ip || 'den neuen Server') + ' zeigt, ist der Schritt erledigt.')]);
      }
      case 5: {
        if (!x.st) return card('Generalprobe', null, el('p', { class: 'ls-mut' }, 'Lädt …'), null, null);
        const why = x.gpExpired ? 'Die Generalprobe vom ' + when(x.gp.am) + ' ist älter als drei Tage. Ein neuer Probe-Umzug frischt nur auf und überträgt, was sich geändert hat.'
          : x.gp && !x.gp.ok ? 'Die letzte Generalprobe hat nicht alles geschafft: ' + (x.gp.probleme || []).join('; ') + '. Liegt es an diesem Server (etwa eine fehlende Datei), behebe es hier und mach ein neues Backup; der Probe-Umzug spielt immer das letzte ein. Liegt es am neuen Server oder am Netz (etwa ein Image ließ sich nicht laden), starte den Probe-Umzug einfach noch einmal.'
            : 'Der Probe-Umzug spielt dein Backup drüben ein, startet alles und prüft jede Seite. Dieser Server läuft weiter, deine Domains zeigen weiter hierher.';
        const errs = x.gp && !x.gp.ok && !x.gpExpired ? x.gp.fehler || [] : [];
        return card('Generalprobe', why, [
          errs.length ? el('div', { class: 'band b-warn' }, el('p', {}, el('b', {}, 'Was drüben nicht ging:')), el('ul', { class: 'lt-errs' }, errs.map(e => el('li', {}, e))),
            x.job ? el('p', { class: 'ls-mut' }, 'Die ganze Ausgabe steht unter Werkzeuge, „Ausgabe des letzten Laufs“.') : null,
            el('div', { class: 'op-btns' }, link('#/backup', 'Neues Backup', 'btn btn-sm', 'backup'))) : null,
          moveForm(x.st, sv, 'run'),
          el('p', { class: 'ls-note' }, ic('info', 16), el('span', {}, 'Danach läuft drüben alles doppelt, etwa Bots oder verwaltete Server. Halte die Probe-Kopie unter Werkzeuge an, wenn das stört.'))], null, moveHelp());
      }
      case 6: {
        const pc = L.pc_kopie;
        const dur = x.gp ? probeMinutes() : 0;
        return card('Umzugstag vorbereiten', 'Hake an, was außer den Projekten mitkommt. Die Generalprobe gilt ' + leftText(x.gpLeft) + '.',
          [seBoxAdvice(m),
            d && (d.vorschlaege || []).length ? dnsSuggest(d) : null,
            el('p', { class: 'ls-mut' }, (dur ? 'Erwartete Ausfallzeit am Umzugstag: etwa ' + dur + ' Minuten (so lange dauerte der Probe-Umzug), dazu ' : 'Am Umzugstag sind deine Seiten etwa so lange aus, wie der Probe-Umzug dauerte, dazu ') + dnsWait(d) + '.'),
            ttlNote(d),
            pc ? el('p', { class: 'ls-mut' }, pc === 'geladen' ? 'Backup auf dem PC: erledigt.' : 'Backup auf dem PC: übersprungen.')
              : el('div', { class: 'band b-info band-row' }, el('p', {}, el('b', {}, 'Empfehlung: '), 'Lade das Backup jetzt einmal auf deinen PC. Der alte Server wird später gekündigt; dann hast du eine Kopie, die nicht an einem Server hängt.'),
                el('div', { class: 'op-btns' }, link('#/backup', 'Zum Backup', 'btn btn-sm', 'download'),
                  el('button', { type: 'button', class: 'btn btn-sm', disabled: busy, onclick: () => setLo({ pc_kopie: 'geladen' }, 'Erledigt') }, 'Erledigt'),
                  el('button', { type: 'button', class: 'btn btn-sm btn-g', disabled: busy, onclick: () => setLo({ pc_kopie: 'uebersprungen' }, 'Übersprungen') }, 'Überspringen')))],
          el('button', { type: 'button', class: 'btn btn-p', 'data-fid': 'lt-passt', disabled: busy || !se.rep || se.saving, onclick: () => setLo({ passt_so: true, haken: (se.rep && se.rep.angekreuzt) || [] }, 'Server-Ebene bestätigt') }, ic('check', 18), 'Passt so'),
          [el('p', {}, 'Beim Probe-Umzug hat der neue Server nur geprüft, was dazukäme; sonst liefen deine Aufgaben doppelt. Am Umzugstag übernimmt HostDeck genau, was hier angehakt ist. Was dort schon anders liegt, überschreibt HostDeck nie.')]);
      }
      case 7:
        if (!x.st) return card('Umzugstag', null, el('p', { class: 'ls-mut' }, 'Lädt …'), null, null);
        return card('Umzugstag', 'Ab dem letzten Lauf sind deine Seiten aus, bis die Domains umgestellt sind. Bis dahin holt „Zurück zum alten Server“ alles hierher zurück.',
          [x.phase !== 'zurueck' ? ttlNote(x.d) : null,
          x.phase === 'zurueck' && !(x.gp && x.day && x.day.ende && x.gp.am > x.day.ende) ? el('div', { class: 'band b-info', role: 'status', 'data-fid': 'lt-zurueck' }, el('p', {},
            // Nach dem Rückweg (Labor S5): Was gilt jetzt, was steht drüben noch?
            el('b', {}, (x.day && x.day.hinweis ? x.day.hinweis + ': ' : 'Rückweg: ') + 'Deine Projekte laufen wieder hier. '),
            'Auf ' + ((x.day && x.day.ip) || 'dem neuen Server') + ' stehen noch die Projekte vom letzten Umzugstag; halte sie dort unter Projekte an. Ein neuer Umzugstag spielt alles frisch hinüber, solange der Leitstand dort noch nicht übernommen hat; hat er übernommen (die Leitstand-Adresse zeigte schon dorthin), setz den neuen Server neu auf oder nimm einen anderen und mach vorher wieder einen Probe-Umzug.')) : null,
          moveForm(x.st, sv, 'day')], null,
          [el('p', {}, 'Die sieben Teilschritte siehst du hier live: Projekte hier stoppen, letzter Lauf, Probe-Kopie entfernen, alles frisch drüben einspielen, Server-Ebene, Seiten prüfen, warten auf DNS.')]);
      case 8: return stepDNS(x, m, card);
      case 9: return stepEnd(x, card);
    }
    return card(LT_TITLES[i], null, null, null, null);
  }
  // Schritt 9: DNS-Fahrplan. Erst alle Domains außer der Leitstand-Adresse,
  // dann diese zum Schluss; so siehst du hier den Fortschritt, bis der neue
  // Leitstand übernimmt.
  function stepDNS(x, m, card) {
    const d = x.d;
    const back = el('button', { type: 'button', class: 'btn', 'data-fid': 'ud-back', disabled: !!ud.busy || mv.watching, onclick: () => dayCall('zurueck', 'Rückweg') }, ic('undo', 18), ud.busy === 'zurueck' ? 'Startet …' : 'Zurück zum alten Server');
    const finish = ud.sure === 'fertig'
      ? [el('button', { type: 'button', class: 'btn btn-p', 'data-fid': 'ud-finish-ok', disabled: !!ud.busy, onclick: () => dayCall('abschliessen', 'Umzug abgeschlossen') }, ic('check', 18), 'Ja, abschließen'),
        el('button', { type: 'button', class: 'btn', onclick: () => { ud.sure = false; core.rerender(); } }, 'Nein')]
      : el('button', { type: 'button', class: 'btn', 'data-fid': 'ud-finish', disabled: !!ud.busy, onclick: () => { ud.sure = 'fertig'; core.rerender(); } }, ic('check', 18), 'Von Hand abschließen');
    if (!d) return card('Domains umstellen', 'HostDeck fragt die zuständigen Nameserver …', el('p', { class: dn.error ? 'gt-err' : 'ls-mut' }, dn.error || 'Lädt …'), null, null);
    const ip = (x.day && x.day.ip) || newIPs(d, false)[0] || d.ziel;
    const all = d.domains || [];
    // Leitstand ohne Domains: Nichts stellt sich um; der neue Leitstand
    // hat schon übernommen. Hier von Hand abschließen.
    if (!all.length) {
      return card('Umzug abschließen', 'Hier stehen keine Domains, die umziehen. Der neue Leitstand hat deshalb schon übernommen; schließe den Umzug hier von Hand ab, dann ist dieser Server abgelöst.',
        el('p', { class: 'ls-mut' }, 'Öffne den neuen Leitstand über dessen Notausgang (sudo hostdeck notausgang auf ' + ip + ').'),
        el('div', { class: 'op-btns' }, ud.sure === 'fertig' ? finish : el('button', { type: 'button', class: 'btn btn-p', 'data-fid': 'ud-finish', disabled: !!ud.busy, onclick: () => { ud.sure = 'fertig'; core.rerender(); } }, ic('check', 18), 'Von Hand abschließen'), back), null);
    }
    const own = d.leitstand;
    // Domains ohne DNS-Eintrag: nichts umzustellen; sie halten den Abschluss
    // nicht auf und zählen nicht mit.
    const none = all.filter(z => z.stand === 'fehler' && z.domain !== own);
    const first = all.filter(z => z.domain !== own && z.stand !== 'fehler');
    const last = all.filter(z => z.domain === own);
    const left = first.filter(z => z.stand !== 'neu');
    const ownNew = last.length && last[0].stand === 'neu';
    const mixed = all.filter(z => z.stand === 'gemischt').map(z => z.domain);
    const other = all.filter(z => z.stand === 'anders').map(z => z.domain);
    return card('Domains umstellen', 'Stell bei deinem Domain-Anbieter diese Einträge auf ' + ip + ' um, die Leitstand-Adresse zum Schluss. Jede Zeile hakt sich ab, sobald der zuständige Nameserver sie so meldet.',
      [ownNew && left.length ? el('div', { class: 'band b-warn', role: 'status' }, el('p', {}, el('b', {}, 'Die Leitstand-Adresse zeigt schon auf den neuen Server. '), 'Unter https://' + own + ' antwortet jetzt der neue Leitstand; dieser hier wartet, bis auch ' + (left.length === 1 ? 'die letzte Domain' : 'die übrigen ' + left.length + ' Domains') + ' umgestellt ' + (left.length === 1 ? 'ist' : 'sind') + '. Stell sie jetzt um; den Fortschritt siehst du weiter hier, über diese Seite oder den Notausgang.')) : null,
        // Alles grün, aber noch nicht abgelöst: Die Überwachung drüben
        // schließt innerhalb etwa einer Minute ab.
        !left.length && (!last.length || ownNew) && !mixed.length && !other.length ? el('div', { class: 'band b-info', role: 'status', 'data-fid': 'lt-gleich' }, el('p', {}, el('b', {}, 'Alles umgestellt. '),
          'HostDeck schließt gleich von selbst ab, meist innerhalb einer Minute; dann ist dieser Server abgelöst. Du musst nichts mehr tun.')) : null,
        el('h3', { class: 'lt-h3' }, 'Jetzt umstellen · ' + (first.length - left.length) + ' von ' + first.length),
        first.length ? dnsRows(switchRows(first, d, ip), 'Jetzt umstellen') : el('p', { class: 'ls-mut' }, 'Keine weiteren Domains.'),
        none.length ? el('p', { class: 'ls-mut' }, 'Ohne Eintrag im DNS, hier nichts umzustellen: ' + none.map(z => z.domain).join(', ') + '.') : null,
        last.length ? [el('h3', { class: 'lt-h3' }, 'Zum Schluss: die Leitstand-Adresse'),
          el('p', { class: 'ls-mut' }, 'Solange ' + own + ' hierher zeigt, siehst du hier den Fortschritt und hast den Rückweg. Zeigt sie um, übernimmt der neue Leitstand.'),
          dnsRows(switchRows(last, d, ip), 'Zum Schluss')] : null,
        mixed.length ? el('p', { class: 'ls-note' }, ic('warn', 16), el('span', {}, 'Teils neu, teils noch hier: ' + mixed.join(', ') + '. Meist fehlt ein zweiter Eintrag, oft der AAAA-Eintrag (IPv6).')) : null,
        other.length ? el('p', { class: 'ls-note' }, ic('info', 16), el('span', {}, 'Zeigt woandershin: ' + other.join(', ') + '. Liegt ein Proxy davor (etwa ein CDN), stellst du das Ziel dort um. Zeigt eine Domain absichtlich woanders hin, schließt HostDeck nicht von selbst ab; dann „Von Hand abschließen“.')) : null,
        nsLine(d),
        dnsSuggest(d),
        el('div', { class: 'op-btns' }, back, finish)],
      el('button', { type: 'button', class: 'btn btn-p', 'data-fid': 'lt-dns', disabled: dn.loading, onclick: loadDn }, ic('refresh', 18), dn.loading ? 'Fragt die Nameserver …' : 'Neu prüfen'),
      [el('p', {}, 'Abgeschlossen ist der Umzug, wenn keine Domain mehr hierher oder gemischt zeigt und mindestens eine auf den neuen Server. Dann ist dieser Server abgelöst: Die umgezogenen Projekte bleiben hier aus und warnen nicht, Backups und Umzüge gehen nur noch drüben.'),
        el('p', {}, 'Geht drüben etwas schief: DNS-Einträge zurückstellen und „Zurück zum alten Server“. Was drüben seitdem neu entstand, fehlt dann hier.')]);
  }
  // Schritt 10 auf dem alten Server: Weiter geht es im neuen Leitstand.
  function stepEnd(x, card) {
    const a = x.ur.abgeloest;
    const own = x.own;
    const until = a ? new Date(new Date(a.am).getTime() + 7 * 864e5) : null;
    return card('Abschluss auf dem neuen Server', 'Dieser Server ist abgelöst. Den Rest erledigst du im neuen Leitstand; er führt dich dort durch „Umzug abschließen“.',
      [el('ul', { class: 'ls-legend' },
        el('li', {}, ic('info', 14), el('span', {}, 'Diesen Server bis ' + (until ? fmtDate(until.toISOString()) : 'in einer Woche') + ' als Reserve stehen lassen, dann beim Anbieter kündigen.')),
        el('li', {}, ic('info', 14), el('span', {}, 'Vor dem Kündigen die Backups holen, die nur hier liegen: Seite Backup, oder über den Notausgang dieses Servers (sudo hostdeck notausgang).')),
        el('li', {}, ic('info', 14), el('span', {}, 'Landest du unter der Leitstand-Adresse noch hier, hält dein Browser die alte Verbindung: alle Tabs dieser Seite schließen und neu öffnen.'))),
        el('div', { class: 'op-btns' }, link('#/backup', 'Backups hier', 'btn btn-sm', 'backup'))],
      own ? el('a', { class: 'btn btn-p', href: 'https://' + own + '/#/umzug', target: '_blank', rel: 'noopener' }, ic('next', 18), 'Zum neuen Leitstand') : null,
      [el('p', {}, 'Geht drüben etwas schief: DNS-Einträge zurückstellen und hier unter Werkzeuge „Zurück zum alten Server“ wählen.')]);
  }
  // Schrittliste: erledigte mit Ergebnis, künftige grau; Anklicken klappt
  // die Einzelheiten auf (nachlesen, nicht noch einmal ausführen).
  function lotseList(x, m) {
    return el('ol', { class: 'lt-steps' }, LT_TITLES.map((t, i) => {
      const done = x.done[i];
      const cur = i === x.n;
      const open = lo.steps.has(i);
      return el('li', { class: done ? 'is-done' : cur ? 'is-now' : 'is-todo' },
        el('details', { open, ontoggle: e => { if (e.currentTarget.open) lo.steps.add(i); else lo.steps.delete(i); } },
          el('summary', {}, el('span', { class: 'dot' }, done ? ic('check', 14) : String(i + 1)),
            el('span', { class: 'lt-t' }, el('b', {}, t), el('span', { class: 'sr' }, done ? ' (erledigt)' : cur ? ' (jetzt dran)' : ' (kommt noch)'),
              el('span', { class: 'ls-mut blk' }, done ? lotseResult(i, x, m) : cur ? 'jetzt dran, siehe oben' : ''))),
          el('p', { class: 'ls-mut lt-about' }, LT_ABOUT[i])));
    }));
  }
  function toolBox(key, title, body, auto) {
    const open = lo.tools.has(key) || (auto && !lo.tools.has('-' + key));
    return el('details', { class: 'ls-sec lt-tool', open, 'data-fid': 'lt-tool-' + key, ontoggle: e => {
      const on = e.currentTarget.open;
      lo.tools.delete(key); lo.tools.delete('-' + key);
      if (on !== !!auto) lo.tools.add(on ? key : '-' + key);
    } }, el('summary', { class: 'ls-sec-h' }, el('h2', {}, title)), el('div', { class: 'ls-sec-b' }, typeof body === 'function' ? body() : body));
  }
  function pageLotse(m) {
    const x = lotse(m);
    lotseLoad(x.n >= 4 || x.n === -1);
    const sv = core.svc() || {};
    const i = x.n < 0 ? 9 : x.n;
    const h = x.ur.abgeloest ? ['Dieser Server ist ', em('ok', 'abgelöst'), '.'] : [LT_TITLES[i], ' ', em('acc', 'ist dran'), '.'];
    const calm = !x.ur.abgeloest && !mv.watching && !(x.phase && x.phase !== 'zurueck' && x.phase !== 'fertig');
    return [head({ i: 'umzug', k: 'Umzug', h: x.ur.abgeloest ? h : ['Schritt ' + (i + 1) + ' von 10: '].concat(h), show: false,
      p: 'Der Lotse führt in zehn Schritten von der Auswahl bis zum Abschluss auf dem neuen Server. Oben steht immer genau ein Schritt mit einem Hauptknopf; darunter kannst du nachlesen, was schon erledigt ist. Die Werkzeuge brauchst du nur ausnahmsweise.',
      a: calm ? el('button', { type: 'button', class: 'btn btn-g', 'data-fid': 'md-off', disabled: md.busy, title: 'Blendet Hinweise zu Auswahl und Umzug aus; kurz nach dem Herunterladen löscht HostDeck ein Backup dann vom Server.', onclick: () => setMovePlanned(false) }, md.busy ? 'Schaltet um …' : 'Umzug absagen') : null }),
    md.error ? el('p', { class: 'gt-err', role: 'alert' }, ic('warn', 16), el('span', {}, md.error)) : null,
    probeBand(true),
    el('div', { class: 'ls-grid eq' },
      stepBox(i, x, m),
      sec('Schritte', lotseList(x, m), { cls: 'c12', sub: 'anklicken zum Nachlesen' }),
      // Die Verbindungskarte zeigt, was nach dem Umzug wieder so
      // zusammenhängen muss (Bauplan: Lageplan wird Verbindungskarte).
      x.ur.abgeloest ? null : (() => { const v = vMap(m); v.classList.replace('c8', 'c12'); return v; })(),
      el('div', { class: 'lt-tools', role: 'group', 'aria-label': 'Werkzeuge' },
        el('p', { class: 'sc-g' }, 'Werkzeuge'),
        x.job && !mv.watching ? toolBox('lauf', 'Ausgabe des letzten Laufs', () => jobView(x.job, mv, MOVE_TEXT), false) : null,
        // Steht der Umzugstag, zeigt ihn schon der Kasten oben (nicht doppelt).
        x.ur && (x.ur.generalprobe || x.ur.umzugstag || x.ur.abgeloest || x.ur.aus_gelassen) && x.phase !== 'fehler' && x.phase !== 'unterbrochen' ? toolBox('tag', 'Generalprobe, Umzugstag und Rückweg', () => dayBox(true), x.n === -1 || !!x.ur.aus_gelassen) : null,
        toolBox('dns', 'DNS-Liste aller Domains', () => dnsBox(), false),
        toolBox('se', 'Server-Ebene', () => seBox(), false),
        toolBox('check', 'Checkliste für den neuen Server', () => moveChecklist(m), false),
        x.st && sv.probe_kopie ? toolBox('kopie', 'Probe-Kopie anhalten oder entfernen', () => moveForm(x.st, sv, 'copy'), false) : null))];
  }
  // Abzeichen in der Leiste: derselbe Schritt wie im Lotsen, aus denselben
  // Daten wie „Braucht dich“.
  function lotseStep(m) {
    lotseLoad(false);
    if (!lo.rep || !mv.rep || !ud.rep) return null;
    const x = lotse(m);
    if (x.n < 1 || x.n > 8) return null;
    const b = cnt('Schritt ' + (x.n + 1), 'acc');
    b.title = LT_TITLES[x.n] + ' (Schritt ' + (x.n + 1) + ' von 10)';
    return b;
  }
  // „Braucht dich“: ein einziger Eintrag, solange ein Umzug geplant ist.
  function lotseHint(m) {
    lotseLoad(false);
    if (!lo.rep || !mv.rep || !ud.rep) return null;
    const x = lotse(m);
    if (x.n < 0 || x.n === 9) return null;
    return { t: 'Umzug: ' + LT_TITLES[x.n], d: LT_ABOUT[x.n] };
  }
  // ---------- Abschluss auf dem neuen Server (Schritt 10) ----------
  const ab = { rep: null, loading: false, at: 0, error: '', busy: '' };
  async function loadAb() {
    if (ab.loading) return;
    ab.loading = true;
    const r = await core.call('/api/umzug/abschluss', null, true);
    ab.loading = false;
    ab.at = Date.now();
    if (r.ok) { ab.rep = r.data; ab.error = ''; } else if (r.status !== 404) ab.error = errText(r, 'Laden');
    if (ui.page === 'umzug') core.rerender();
  }
  async function setAb(body, label) {
    if (ab.busy) return;
    ab.busy = body.punkt || 'alle';
    ab.error = '';
    core.rerender();
    const r = await core.call('/api/umzug/abschluss', body);
    ab.busy = '';
    if (r.ok) { ab.rep = r.data; core.announce(label); await core.refreshLauf(); refreshChrome(); } else ab.error = errText(r, 'Speichern');
    core.rerender();
  }
  function finishBox() {
    if (!ab.loading && (!ab.at || Date.now() - ab.at > 30000)) setTimeout(loadAb, 0);
    const r = ab.rep;
    if (!r || !r.offen) return null;
    const until = fmtDate(r.bis);
    const from = r.alter_server || 'der alte Server';
    const ITEMS = {
      tabs: ['Alte Tabs schließen', 'Tabs des alten Leitstands können noch mit dem alten Server verbunden sein. Schließ alle und öffne den Leitstand neu.'],
      backup: ['Erstes Backup hier erstellen und herunterladen', 'Ab jetzt ist dieser Server dein Sicherheitsnetz. Im Alltag löscht HostDeck ein Backup kurz nach dem Herunterladen vom Server.'],
      testadresse: ['Testadresse ' + (r.testadresse || '') + ' beim Anbieter löschen', r.testadresse_weg ? 'HostDeck hat sie schon aus Caddy genommen; der DNS-Eintrag kann jetzt weg.' : 'HostDeck nimmt sie gerade aus Caddy. Erst danach den Eintrag löschen, sonst scheitert Caddy beim Erneuern ihres Zertifikats.'],
      ttl: ['TTL zurückstellen', 'Hast du die Gültigkeit (TTL) für den Umzug auf 300 Sekunden gesenkt, stell sie wieder auf den alten Wert, etwa 3600.'],
      reserve: ['Alten Server erst nach dem ' + until + ' kündigen', from + ' bleibt bis dahin Reserve. Vorher die Backups holen, die nur dort liegen: über dessen Notausgang (sudo hostdeck notausgang) auf der Seite Backup.'],
    };
    // Übernommen hat dieser Leitstand, sobald seine eigene Adresse hierher
    // zeigt. Andere Domains können dann noch auf den alten Server zeigen,
    // wo ihre Projekte seit dem Umzugstag aus sind (Labor S4, SK4a).
    if (!dn.loading && (!dn.at || Date.now() - dn.at > 30000)) setTimeout(loadDn, 0);
    const todo = dn.rep ? (dn.rep.domains || []).filter(z => z.stand === 'anders' || z.stand === 'gemischt') : [];
    return sec('Umzug abschließen', el('div', { class: 'ls-stack sm' },
      todo.length ? el('div', { class: 'band b-warn', role: 'status', 'data-fid': 'ab-dns' }, el('p', {},
        el('b', {}, plural(todo.length, 'Domain zeigt', 'Domains zeigen') + ' noch nicht hierher: '), todo.map(z => z.domain).join(', ') + '. ',
        'Stell sie jetzt bei deinem Domain-Anbieter auf ' + (hereIPs(dn.rep)[0] || (dn.rep.hier || [])[0] || 'diesen Server') + ' um: Auf ' + from + ' sind ihre Projekte seit dem Umzugstag aus. Zeigt eine absichtlich woandershin (etwa auf einen Proxy), ist das in Ordnung. Bist du mit „Zurück zum alten Server“ zurückgegangen, halte hier die Projekte unter Projekte an, sonst laufen sie doppelt.')) : null,
      el('p', {}, (todo.length ? 'Dieser Leitstand ist jetzt der echte.' : 'Der Umzug ist durch, dieser Leitstand ist jetzt der echte.') + ' Diese Punkte bleiben bis zum ' + until + ' hier stehen; danach ist Ruhe.'),
      el('div', { class: 'cl-list' }, r.punkte.map(k => {
        const on = !!(r.erledigt && r.erledigt[k]);
        const lock = k === 'testadresse' && !r.testadresse_weg && !on;
        return el('label', { class: 'cl-it' + (on ? ' is-on' : '') },
          el('input', { type: 'checkbox', checked: on, disabled: !!ab.busy || lock, 'data-fid': 'ab-' + k, onchange: e => setAb({ punkt: k, erledigt: e.target.checked }, e.target.checked ? 'Erledigt' : 'Wieder offen') }),
          el('span', { class: 'cl-t' }, el('b', {}, ITEMS[k][0]), el('span', { class: 'ls-mut blk' }, ITEMS[k][1])), el('span'));
      })),
      ab.error ? el('p', { class: 'gt-err', role: 'alert' }, ic('warn', 16), el('span', {}, ab.error)) : null,
      el('div', { class: 'op-btns' }, el('button', { type: 'button', class: 'btn btn-sm btn-g', 'data-fid': 'ab-weg', disabled: !!ab.busy, onclick: () => setAb({ alle_weg: true }, 'Abschluss ausgeblendet') }, 'Rest ausblenden'))),
    { cls: 'c12 lt-now', sub: 'Schritt 10 von 10' });
  }
  function pageUmzugSvc(m) {
    if (m.alltag) return pageUmzugAlltag(m);
    if (core.svc().kopie) return pageKopie();
    return pageLotse(m);
  }
  // Probe-Kopie: Hier startet kein Backup und kein Umzug; alles dazu
  // steuert der Leitstand des Servers, von dem die Kopie stammt.
  function pageKopie() {
    const k = core.svc().kopie;
    const from = k.von || 'dem alten Server';
    if (k.umzug) {
      // Wer hier landet, hat meist die Leitstand-Adresse schon umgestellt
      // (oder kommt über die Testadresse): Den Fortschritt zeigt dann auch
      // dieser Leitstand, aus Sicht des neuen Servers (Labor S4, SK4a).
      if (!dn.loading && (!dn.at || Date.now() - dn.at > 30000)) setTimeout(loadDn, 0);
      // Die Testadresse ist keine deiner Domains; sie fällt beim Einzug weg.
      const ds = dn.rep ? (dn.rep.domains || []).filter(z => z.domain !== k.testadresse) : [];
      const here = ds.filter(z => z.stand === 'alt');
      const todo = ds.filter(z => z.stand === 'anders' || z.stand === 'gemischt');
      const ST = { alt: ['ok', 'zeigt hierher'], anders: ['idle', 'zeigt noch auf ' + from], gemischt: ['warn', 'teils hierher, teils noch woanders'], fehler: ['idle', 'kein Eintrag gefunden'] };
      const rows = ds.map(z => el('tr', {}, el('td', { class: 'mono' }, z.domain), el('td', {}, st(...(ST[z.stand] || ['idle', z.stand])))));
      return [head({ i: 'umzug', k: 'Umzug · Einzug', h: todo.length ? ['Noch ', em('acc', plural(todo.length, 'Domain', 'Domains')), ' umstellen.'] : ['Hier ', em('acc', 'zieht dein Leitstand ein'), '.'], show: true,
        p: 'Der Umzugstag hat alles von ' + from + ' hierher gebracht. Sobald ' + (dn.rep && dn.rep.leitstand ? 'deine Leitstand-Adresse ' + dn.rep.leitstand : 'deine Domains') + ' hierher ' + (dn.rep && dn.rep.leitstand ? 'zeigt' : 'zeigen') + ', übernimmt dieser Leitstand von selbst; dann ist hier Alltag, und hier steht, was nach dem Umzug noch zu tun ist.' }),
      el('div', { class: 'ls-grid eq' },
        sec('Domains', [
          dn.rep ? el('div', { class: 'ls-tw' }, el('table', { class: 'ls-t' }, el('caption', { class: 'sr' }, 'Stand der Domains'),
            el('thead', {}, el('tr', {}, el('th', { scope: 'col' }, 'Domain'), el('th', { scope: 'col' }, 'Stand'))), el('tbody', {}, rows)))
            : el('p', { class: dn.error ? 'gt-err' : 'ls-mut' }, dn.error || 'HostDeck fragt die zuständigen Nameserver …'),
          el('p', { class: 'ls-mut' }, plural(here.length, 'Domain zeigt', 'Domains zeigen') + ' schon hierher. Stell die übrigen bei deinem Domain-Anbieter auf ' + ((dn.rep && (hereIPs(dn.rep)[0] || (dn.rep.hier || [])[0])) || 'diesen Server') + ' um. Den alten Leitstand erreichst du unter deiner Leitstand-Adresse nicht mehr, sobald sie hierher zeigt; auf dem alten Server hilft dann der Notausgang (sudo hostdeck notausgang), etwa für „Zurück zum alten Server“.'),
          el('div', { class: 'op-btns' }, el('button', { type: 'button', class: 'btn', 'data-fid': 'ez-dns', disabled: dn.loading, onclick: loadDn }, ic('refresh', 18), dn.loading ? 'Fragt die Nameserver …' : 'Neu prüfen'))],
          { cls: 'c12', sub: 'direkt beim zuständigen Nameserver gefragt' }))];
    }
    const item = (href, icon, t, d) => el('li', { class: 'ls-att-i idle' }, el('span', { class: 'ls-att-ic' }, ic(icon, 18)),
      el('div', { class: 'ls-att-t' }, el('b', {}, t), el('span', {}, d)), el('div', { class: 'ls-att-a' }, link(href, 'Ansehen')));
    return [head({ i: 'umzug', k: 'Umzug · Probe-Kopie', h: ['Das ist die ', em('acc', 'Probe-Kopie'), ' von ' + from + '.'], show: true,
      p: 'Hier probierst du aus, ob alles läuft. Backups und Umzüge startest du nicht hier, sondern im Leitstand von ' + from + ' unter Umzug: dort hältst du diese Kopie auch an, entfernst sie oder startest den Umzugstag.' }),
    el('div', { class: 'ls-grid eq' },
      sec('Was du hier prüfen kannst', el('ol', { class: 'ls-att' },
        item('#/projekte', 'projekte', 'Projekte', 'Laufen alle Container? Ein Einmal-Job steht hier als „angelegt“; er lief auf dem alten Server schon.'),
        item('#/domains', 'domains', 'Domains', 'HostDeck fragt deine Seiten hier über diesen Server ab. Deine Besucher landen weiter auf ' + from + ', bis du am Umzugstag die Domains umstellst.')), { cls: 'c12' }))];
  }

  // ---------- Speicherplatz und Aufräumen ----------
  // HostDeck listet auf, du hakst an. Build-Cache und Images ohne Container
  // löscht der Dienst direkt; Volumes ohne Container und alte Kopien des
  // Caddyfiles sichert er erst in den Papierkorb auf dem Server und löscht
  // sie, wenn die Sicherung geprüft ist. 30 Tage lang lassen sie sich
  // zurückholen.
  const cl = { rep: null, job: null, at: 0, loading: false, timer: 0, error: '', result: null, busy: '', sel: new Set(), seen: new Set() };
  const CL_GROUP = [['cache', 'Build-Cache'], ['image', 'Images ohne Container'], ['volume', 'Volumes ohne Container'], ['datei', 'Alte Kopien des Caddyfiles']];
  async function loadCl() {
    if (cl.loading || !core.svc()) return;
    cl.loading = true;
    const r = await core.call('/api/aufraeumen', null, true);
    cl.loading = false;
    cl.at = Date.now();
    clearTimeout(cl.timer);
    cl.timer = 0;
    if (r.ok) {
      const was = cl.job && cl.job.laeuft;
      cl.rep = r.data.stand; cl.job = r.data.auftrag || null; cl.error = '';
      const keys = new Set(cl.rep.vorschlaege.map(x => x.schluessel));
      for (const k of [...cl.sel]) if (!keys.has(k)) cl.sel.delete(k);
      for (const x of cl.rep.vorschlaege) {
        if (!cl.seen.has(x.schluessel) && x.vorgeschlagen) cl.sel.add(x.schluessel);
        cl.seen.add(x.schluessel);
      }
      if (was && cl.job && !cl.job.laeuft) cl.result = cl.job.fehler ? { ok: false, text: cl.job.fehler } : { ok: true, text: cl.job.ergebnis };
    } else if (r.status !== 404) cl.error = (r.data && r.data.error) || 'Fehler ' + r.status;
    if (cl.job && cl.job.laeuft && ui.page === 'platz') cl.timer = setTimeout(loadCl, 1500);
    if (ui.page === 'platz' && core.work() && !modalOpen()) core.rerender();
  }
  function clResult() {
    const r = cl.result;
    if (!r || (cl.job && cl.job.laeuft)) return null;
    return el('div', { class: 'ls-note op-res ' + (r.ok ? 'is-ok' : 'is-err'), role: 'status' }, ic(r.ok ? 'done' : 'warn', 16), el('span', {}, r.text),
      el('button', { type: 'button', class: 'ls-ibtn op-x', 'aria-label': 'Meldung schließen', onclick: () => { cl.result = null; core.rerender(); } }, ic('x', 16)));
  }
  const clRunning = () => !!(cl.job && cl.job.laeuft) || !!cl.busy;
  const clSize = n => n < 0 ? 'Größe unbekannt' : size(n);
  function clDate(s) {
    const d = new Date(s);
    return isNaN(d) ? '' : String(d.getDate()).padStart(2, '0') + '.' + String(d.getMonth() + 1).padStart(2, '0') + '.' + d.getFullYear();
  }
  const DK_CLS = { docker: 'c-b', cache: 'c-p', volumes: 'c-t', data: 'c-v', trash: 'trash', system: 'c-g' };
  function clDisk(r) {
    const d = r.platte;
    if (!d.gesamt) return el('p', { class: 'ls-mut' }, 'Die Platte ließ sich nicht messen.');
    const used = d.gesamt - d.frei;
    const known = d.images + d.build_cache + d.volumes + d.container + d.papierkorb;
    const parts = [
      ['docker', 'Images', 'Programme deiner Container', d.images],
      ['cache', 'Build-Cache', 'Zwischenstände vom Bauen', d.build_cache],
      ['volumes', 'Volumes', 'Daten der Container', d.volumes],
      ['data', 'Container', 'was Container in sich selbst geschrieben haben', d.container],
      ['trash', 'Papierkorb', 'zurückholbar, bis die Frist abläuft', d.papierkorb],
      ['system', 'Alles andere', 'Projektordner, System, Logs; die größten Ordner stehen unten', Math.max(0, used - known)],
    ].filter(x => x[3] > 0);
    // Wie die Platte in der Überwachung: Balken mit Lücken, darunter je
    // Teil Farbfeld, Name und Größe (nie nur Farbe).
    const bar = el('div', { class: 'dk-bar', role: 'img', 'aria-label': size(used) + ' von ' + size(d.gesamt) + ' belegt' },
      parts.map(([k, t, , v]) => { const sp = el('i', { class: DK_CLS[k], title: t + ': ' + size(v) }); sp.style.flexGrow = String(v); return sp; }),
      (() => { const sp = el('i', { class: 'free', title: 'Frei: ' + size(d.frei) }); sp.style.flexGrow = String(d.frei); return sp; })());
    const legend = el('ul', { class: 'dk-list dk-two' }, parts.map(([k, t, x, v]) => el('li', {}, el('span', { class: 'dk-sw ' + DK_CLS[k], 'aria-hidden': 'true' }), el('span', { class: 'dk-t' }, el('span', {}, t), el('small', {}, x)), el('b', {}, size(v)))),
      el('li', { class: 'free' }, el('span', { class: 'dk-sw free', 'aria-hidden': 'true' }), el('span', { class: 'dk-t' }, el('span', {}, 'Frei'), el('small', {}, 'noch verfügbar')), el('b', {}, size(d.frei))));
    // Die Reserve für root steht direkt an der Platte, damit die Größe
    // nicht kleiner wirkt als beim Anbieter oder in df.
    // Mehr als ein Viertel ist keine Reserve für root, sondern eine Quote
    // oder ein Messfehler; dann lieber nichts sagen.
    const res = d.reserve > 0 && d.reserve < d.gesamt / 4 ? el('p', { class: 'ls-mut', 'data-fid': 'cl-reserve' }, size(d.gesamt) + ' nutzbar, dazu ' + size(d.reserve) + ' Reserve, die das Dateisystem für root zurückhält (zusammen ' + size(d.gesamt + d.reserve) + ', wie df bei „Size“).') : null;
    return el('div', { class: 'ls-stack sm' }, bar, legend, res);
  }
  function clItem(x) {
    const on = cl.sel.has(x.schluessel);
    const tags = [x.papierkorb ? el('span', { class: 'tag t-sug' }, 'erst in den Papierkorb') : el('span', { class: 'tag t-mut' }, x.art === 'cache' ? 'entsteht neu' : 'ohne Sicherung'),
      x.hinweis ? el('span', { class: 'tag t-open' }, 'erst prüfen') : null];
    return el('label', { class: 'cl-it' + (on ? ' is-on' : '') },
      el('input', { type: 'checkbox', checked: on, disabled: clRunning(), 'data-fid': 'cl-' + x.schluessel, onchange: e => { if (e.target.checked) cl.sel.add(x.schluessel); else cl.sel.delete(x.schluessel); core.rerender(); } }),
      el('span', { class: 'cl-t' }, el('b', { class: x.art === 'cache' ? null : 'mono' }, x.name), x.detail ? el('span', { class: 'ls-mut blk' }, x.detail) : null,
        x.hinweis ? el('span', { class: 'cl-warn blk' }, ic('warn', 14), x.hinweis) : null, el('span', { class: 'cl-tags' }, tags)),
      el('b', { class: 'cl-g' }, x.groesse < 0 ? '?' : bytes(x.groesse)));
  }
  function clList(r) {
    if (!r.vorschlaege.length) return el('p', { class: 'ls-mut' }, 'Nichts aufzuräumen: Kein Build-Cache, kein Image und kein Volume, das kein Container braucht.');
    const groups = CL_GROUP.map(([k, t]) => [t, r.vorschlaege.filter(x => x.art === k)]).filter(g => g[1].length);
    const chosen = r.vorschlaege.filter(x => cl.sel.has(x.schluessel));
    const sum = chosen.reduce((a, x) => a + Math.max(0, x.groesse), 0);
    return el('div', { class: 'cl-list' },
      groups.map(([t, list]) => el('fieldset', { class: 'cl-g-set' }, el('legend', {}, t + ' · ' + list.length), list.map(clItem))),
      el('div', { class: 'cl-foot' },
        el('span', {}, chosen.length ? [plural(chosen.length, 'Eintrag', 'Einträge') + ' gewählt, bis zu ', el('b', {}, size(sum))] : 'Nichts gewählt.'),
        el('button', { type: 'button', class: 'btn btn-p', 'data-fid': 'cl-vorschau', disabled: !chosen.length || clRunning(), 'aria-busy': cl.busy === 'vorschau' ? 'true' : null, onclick: clStart }, ic('eye', 16), 'Vorschau …')));
  }
  function clTrash(r) {
    if (!r.papierkorb.length) return el('p', { class: 'ls-mut' }, 'Leer. Was Daten enthält, liegt hier ' + r.tage + ' Tage, bevor HostDeck es endgültig löscht.');
    return el('ul', { class: 'cl-trash' }, r.papierkorb.map(e => el('li', {},
      ic(e.art === 'volume' ? 'db' : e.art === 'pfad' ? 'folder' : 'file', 16),
      el('span', { class: 'cl-t' }, el('b', { class: 'mono' }, /^[0-9a-f]{64}$/.test(e.name) ? 'Volume ohne Namen (' + e.name.slice(0, 12) + ')' : e.name), el('span', { class: 'ls-mut blk' }, (e.art === 'volume' ? 'Volume' : e.art === 'pfad' ? (e.teilweise ? 'unter Ordner und Dateien teilweise gelöscht' : 'unter Ordner und Dateien gelöscht') : 'Datei') + ' · ' + clSize(e.groesse) + ' · weggeräumt ' + clDate(e.geloescht) + ' · zurückholbar bis ' + clDate(e.bis))),
      el('button', { type: 'button', class: 'btn btn-sm btn-g', 'data-fid': 'cl-zurueck-' + e.id.slice(0, 12), disabled: clRunning(), onclick: () => clRestore(e) }, ic('undo', 16), 'Zurückholen …'))));
  }
  function clDialog(build) {
    return new Promise(resolve => {
      let close = null;
      const done = ok => { close(); resolve(ok); };
      const box = el('div', { class: 'gt-dlg op-dlg up-dlg', role: 'dialog', 'aria-modal': 'true', 'aria-labelledby': 'cl-h', 'aria-describedby': 'cl-d' });
      box.replaceChildren(...build(done).filter(Boolean));
      close = modal(box, () => done(false));
      // [data-autofocus] gewinnt: Wo der Hauptknopf endgültig löscht, steht
      // der Fokus woanders (Enter-Enter darf nichts löschen).
      (box.querySelector('[data-autofocus]') || box.querySelector('.btn-p') || box.querySelector('.btn')).focus();
    });
  }
  function clPreview(p) {
    return clDialog(done => [
      el('p', { class: 'ls-eyb' }, ic('eye', 16), el('span', {}, 'Vorschau · noch ist nichts passiert')),
      el('h2', { id: 'cl-h' }, 'Aufräumen?'),
      el('section', { class: 'op-part' }, el('h3', {}, plural(p.eintraege.length, 'Eintrag', 'Einträge') + ', bis zu ' + size(p.gewinn)),
        el('ul', { class: 'op-list cl-pl' }, p.eintraege.map(x => el('li', {}, el('span', { class: x.art === 'cache' ? null : 'mono' }, x.name), ' · ' + clSize(x.groesse), x.papierkorb ? el('span', { class: 'ls-mut' }, ' · Papierkorb') : null)))),
      p.problem ? el('p', { id: 'cl-d', class: 'gt-lead' }, p.problem) : el('section', { class: 'op-part', id: 'cl-d' }, el('h3', {}, 'Was passiert'),
        el('ol', { class: 'op-list' }, (p.schritte || []).map(x => el('li', {}, x)))),
      ...(p.hinweise || []).map(h => el('p', { class: 'ls-note op-warn' }, ic('warn', 16), el('span', {}, h))),
      el('div', { class: 'gt-acts' }, el('button', { type: 'button', class: 'btn', onclick: () => done(false) }, p.problem ? 'Schließen' : 'Abbrechen'),
        p.problem ? null : el('button', { type: 'button', class: 'btn btn-p', onclick: () => done(true) }, ic('trash', 16), 'Aufräumen'))]);
  }
  async function clGo(path, body) {
    const a = await core.call(path, body);
    if (!a.ok) {
      if (!(a.data && a.data.abgebrochen)) cl.result = { ok: false, text: (a.data && a.data.error) || 'Das ging nicht (Fehler ' + a.status + ').' };
      core.rerender();
      return;
    }
    cl.job = a.data.auftrag; cl.rep = a.data.stand;
    core.announce(cl.job.was + ' läuft');
    loadCl();
  }
  async function clStart() {
    if (clRunning()) return;
    const keys = cl.rep.vorschlaege.map(x => x.schluessel).filter(k => cl.sel.has(k));
    cl.busy = 'vorschau'; cl.result = null;
    core.rerender();
    const r = await core.call('/api/aufraeumen/vorschau', { auswahl: keys });
    cl.busy = '';
    if (!r.ok) { cl.result = { ok: false, text: (r.data && r.data.error) || 'Die Vorschau ging nicht (Fehler ' + r.status + ').' }; core.rerender(); return; }
    if (!await clPreview(r.data)) { core.rerender(); return; }
    return clGo('/api/aufraeumen/starten', { auswahl: keys, stand: r.data.stand });
  }
  async function clRestore(e) {
    if (clRunning()) return;
    cl.result = null;
    const vol = e.art === 'volume';
    const ok = await clDialog(done => [
      el('p', { class: 'ls-eyb' }, ic('eye', 16), el('span', {}, 'Vorschau · noch ist nichts passiert')),
      el('h2', { id: 'cl-h' }, (/^[0-9a-f]{64}$/.test(e.name) ? 'Volume ohne Namen' : e.name) + ' zurückholen?'),
      el('section', { class: 'op-part', id: 'cl-d' }, el('h3', {}, 'Was passiert'), el('ol', { class: 'op-list' },
        vol ? [el('li', {}, 'Docker legt das Volume ', el('span', { class: 'mono' }, e.name), ' wieder an, mit seinen alten Labels.'), el('li', {}, 'HostDeck spielt die Daten (' + clSize(e.groesse) + ') aus dem Papierkorb hinein.'), el('li', {}, 'Ein Container, der es nutzt, findet es beim nächsten Start wieder.')]
          : e.art === 'pfad' ? [el('li', {}, 'HostDeck spielt es (' + clSize(e.groesse) + ') erst vollständig in einen Ordner beim Papierkorb zurück.'), e.teilweise ? el('li', {}, 'Beim Löschen blieb ein Teil stehen. HostDeck legt nur das Fehlende an den alten Ort: ', el('span', { class: 'mono' }, e.pfad), '. Was dort steht, bleibt.')
            : el('li', {}, 'Ist alles da, legt HostDeck es mit einem Schritt an den alten Ort: ', el('span', { class: 'mono' }, e.pfad), '.')]
          : [el('li', {}, 'HostDeck legt die Datei wieder an ihren alten Ort: ', el('span', { class: 'mono' }, e.pfad), '.'), el('li', {}, 'Caddy lädt sie nicht; es ist nur die alte Kopie.')],
        el('li', {}, 'Danach ist der Eintrag aus dem Papierkorb raus.'))),
      el('p', { class: 'ls-note op-warn' }, ic('info', 16), el('span', {}, e.teilweise ? 'HostDeck überschreibt nichts. Ist der Ort inzwischen geschützt, etwa als Projekt, holt HostDeck nichts zurück.' : 'Gibt es inzwischen wieder ' + (vol ? 'ein Volume' : e.art === 'pfad' ? 'etwas' : 'eine Datei') + ' mit diesem Namen, bricht HostDeck ab und überschreibt nichts.')),
      el('div', { class: 'gt-acts' }, el('button', { type: 'button', class: 'btn', onclick: () => done(false) }, 'Abbrechen'),
        el('button', { type: 'button', class: 'btn btn-p', onclick: () => done(true) }, ic('undo', 16), 'Zurückholen'))]);
    if (!ok) { core.rerender(); return; }
    return clGo('/api/aufraeumen/zurueckholen', { id: e.id });
  }
  // Alle Laufwerke aus der Überwachung, voll zuerst; nur, wenn es mehr
  // als eines gibt (sonst steht alles schon unter „Platte“).
  function drives() {
    if (!mon.loading && (!mon.at || Date.now() - mon.at > 60000)) setTimeout(loadMon, 0);
    const ds = mon.rep && mon.rep.info ? (mon.rep.info.laufwerke || []).filter(d => d.gesamt > 0) : [];
    if (ds.length < 2) return null;
    const rows = ds.map(d => ({ d, p: d.belegt / d.gesamt * 100 })).sort((a, b) => b.p - a.p).map(({ d, p }) => {
      // Farbe aus den Grenzen der Überwachung.
      const bar = el('span', { class: 'ls-bar', 'aria-hidden': 'true' }, el('i', { class: diskLevel(d.belegt, d.gesamt) || null }));
      bar.firstChild.style.width = Math.min(100, p).toFixed(1) + '%';
      return el('tr', {}, el('td', { class: 'mono' }, d.pfad), el('td', { class: 'c-cpu' }, el('span', { class: 'ls-load' }, bar, el('span', { class: 'n' }, pctText(p)))),
        el('td', { class: 'r' }, size(d.belegt)), el('td', { class: 'r' }, size(d.gesamt - d.belegt)), el('td', { class: 'r' }, size(d.gesamt)));
    });
    return sec('Laufwerke', el('div', { class: 'ls-tw' }, el('table', { class: 'ls-t' }, el('caption', { class: 'sr' }, 'Laufwerke'),
      el('thead', {}, el('tr', {}, el('th', { scope: 'col' }, 'Eingehängt in'), el('th', { scope: 'col' }, 'Belegt'), el('th', { scope: 'col', class: 'r' }, 'Genutzt'), el('th', { scope: 'col', class: 'r' }, 'Frei'), el('th', { scope: 'col', class: 'r' }, 'Größe'))),
      el('tbody', {}, rows))), { cls: 'c12 flush', sub: plural(ds.length, 'Laufwerk', 'Laufwerke') });
  }
  function pagePlatzLive(m, key) {
    if (!bk.loading && !bk.watching && ((!bk.rep && !bk.error) || Date.now() - bk.at > 60000)) setTimeout(loadBackup, 0);
    if (!cl.loading && (!cl.at || Date.now() - cl.at > 60000 || (cl.job && cl.job.laeuft && !cl.timer))) setTimeout(loadCl, 0);
    const r = cl.rep;
    if (!r) {
      return [head({ i: 'platz', k: 'Speicherplatz · Aufräumen', h: cl.error ? ['Der Stand ist ', em('err', 'nicht erreichbar'), '.'] : 'Docker zählt den Platz …', p: cl.error || 'Das dauert bei vielen Volumes ein paar Sekunden.' }),
        sec(null, el('div', { class: 'ls-stack' }, el('span', { class: 'skel skel-h' }), el('span', { class: 'skel skel-t skel-w' }), el('span', { class: 'skel skel-t' })))];
    }
    const d = r.platte;
    // Die Überschrift nennt nur, was HostDeck vorschlägt; der Rest braucht erst
    // deine Prüfung und wird darum nur leise genannt.
    const gain = r.vorschlaege.filter(x => x.vorgeschlagen).reduce((a, x) => a + Math.max(0, x.groesse), 0);
    const rest = r.vorschlaege.filter(x => !x.vorgeschlagen).reduce((a, x) => a + Math.max(0, x.groesse), 0);
    const pct = d.gesamt ? Math.round((d.gesamt - d.frei) / d.gesamt * 100) : 0;
    const h = r.problem ? ['Aufräumen geht ', em('err', 'gerade nicht'), '.']
      : [d.gesamt ? size(d.gesamt - d.frei) + ' von ' + size(d.gesamt) + ' ' : '', d.gesamt ? em(diskLevel(d.gesamt - d.frei, d.gesamt) || (limits() ? 'ok' : ''), 'belegt') : 'Platte', '. ',
        r.ohne_docker ? 'Ohne Docker gibt es hier nichts aufzuräumen.' : gain > 0 ? [em('acc', size(gain)), ' lassen sich sicher freiräumen.'] : rest > 0 ? ['Nichts mehr, was sicher weg kann.'] : 'Nichts aufzuräumen.'];
    const job = cl.job;
    const jobBox = job && job.laeuft ? sec(job.was + ' läuft …', jobSteps(job), { cls: 'c12', sub: 'Du kannst die Seite schließen; es läuft weiter.' })
      : job && job.fehler && cl.result && job.schritte.length ? sec('Ablauf: ' + job.was, jobSteps(job), { cls: 'c12' }) : null;
    return [head({ i: 'platz', k: 'Speicherplatz · Aufräumen · Stand ' + when(r.stand_von), h,
      p: 'Hier steht nur, was kein Container braucht. Build-Cache und Images löscht HostDeck direkt, sie entstehen neu. Was Daten enthält, kommt erst in den Papierkorb auf diesem Server und lässt sich ' + r.tage + ' Tage lang zurückholen.',
      a: el('button', { type: 'button', class: 'btn btn-sm', 'data-fid': 'cl-ref', disabled: cl.loading, onclick: () => { cl.at = 0; loadCl(); } }, ic('refresh', 16), 'Aktualisieren') }),
    clResult(), cl.error ? el('p', { class: 'band b-err', role: 'status' }, cl.error) : null, r.problem ? el('p', { class: 'band b-err', role: 'status' }, r.problem) : null,
    el('div', { class: 'ls-grid eq' },
      jobBox,
      sec('Platte', clDisk(r), { cls: 'c12' }),
      pzSec(m, key),
      drives(),
      r.problem || r.ohne_docker ? null : sec('Vorschläge', clList(r), { cls: 'c12', sub: 'HostDeck hat vorgewählt, was sicher weg kann; Einträge mit „erst prüfen“ wählst du selbst' }),
      sec('Papierkorb', clTrash(r), { cls: r.bleibt.length ? 'c7' : 'c12', sub: r.papierkorb.length ? 'nach ' + r.tage + ' Tagen leert HostDeck ihn nachts von selbst' : null }),
      r.bleibt.length ? sec('Fasst HostDeck nicht an', el('ul', { class: 'ls-legend' }, r.bleibt.map(k => el('li', {}, ic('lock', 14), el('span', {}, el('b', { class: 'mono' }, k.name), el('small', {}, k.warum)), el('span')))), { cls: 'c5' }) : null)];
  }
  // Die größten Ordner außerhalb von Docker, aus der Bestandsaufnahme: was
  // hinter „Alles andere“ steckt (Projektordner, System, Daten von HostDeck).
  const TOP_KIND = { daten: ['Daten', 't-ok'], system: ['System', 't-mut'], hostdeck: ['HostDeck', 't-sug'], swap: ['Swap', 't-mut'], sonst: ['sonstiges', 't-mut'] };
  const TOP_HOSTDECK = { backup: 'Backups auf dem Server', papierkorb: 'Papierkorb vom Aufräumen', ueberwachung: 'Verlauf der Überwachung', laeufe: 'Ausgaben der Backup-Läufe', aktualisieren: 'Rückfall beim Aktualisieren', umzug: 'Umzug', ergebnis: 'Bestandsaufnahme und Auswahl', leistung: 'Vorher-Stände' };
  function topDirs(m) {
    const sp = m.s.space;
    let top = sp && sp.top;
    if (!top || !top.length) return null;
    // Den Backup-Ordner misst der Dienst live: Nach dem Löschen stünde
    // sonst bis zur nächsten Bestandsaufnahme das alte Backup da.
    const bo = bk.rep && bk.rep.ordner;
    if (bo && bo.pfad) {
      top = top.map(x => x.path === bo.pfad ? Object.assign({}, x, { size: bo.groesse, live: true }) : x)
        .filter(x => !x.live || x.size >= 50e6).sort((a, b) => b.size - a.size);
      if (!top.length) return null;
    }
    const note = p => {
      const sub = /^\/var\/lib\/hostdeck\/([^/]+)$/.exec(p);
      if (sub) return TOP_HOSTDECK[sub[1]] || 'Daten von HostDeck';
      const pr = m.projects.find(x => x.path === p);
      return pr ? 'Projekt ' + pr.name : '';
    };
    return sec('Größte Ordner', el('div', { class: 'ls-tw' }, el('table', { class: 'ls-t' },
      el('caption', { class: 'sr' }, 'Größte Ordner außerhalb von Docker'),
      el('thead', {}, el('tr', {}, el('th', { scope: 'col' }, 'Ordner'), el('th', { scope: 'col' }, 'Art'), el('th', { scope: 'col', class: 'r' }, 'Größe'))),
      el('tbody', {}, top.map(x => {
        const k = TOP_KIND[x.kind] || TOP_KIND.sonst;
        const n = note(x.path);
        return el('tr', {}, el('td', {}, el('span', { class: 'mono' }, x.path), n || x.live ? el('span', { class: 'ls-mut blk' }, [n, x.live ? 'Größe von jetzt' : ''].filter(Boolean).join(' · ')) : null),
          el('td', {}, el('span', { class: 'tag ' + k[1] }, k[0])), el('td', { class: 'r nowrap' }, bytes(x.size)));
      })))), { cls: 'c12 flush', sub: 'außerhalb von Docker, laut Bestandsaufnahme vom ' + fmtDate(m.s.created), help: 'Images, Volumes und Container misst Docker selbst; sie stehen oben unter Platte. Hier steht, was HostDeck beim Durchsehen der Platte gezählt hat, je Ordner. Die Reserve, die das Dateisystem für root zurückhält (bei ext4 meist 5 %), zählt HostDeck wie df weder als belegt noch als frei.' });
  }
  // ---------- Ordner und Dateien (Speicherplatz) ----------
  // HostDeck misst die Platte wie du (nachts und auf Knopfdruck) und merkt sich
  // den Baum. Hier klickst du dich durch, größte zuerst, und löschst, was
  // die Schutzstufen erlauben: Vorschau, Passwort, Protokoll. Der Dienst
  // prüft jede Sperre selbst; die Seite zeigt sie nur an.
  const pz = { path: null, rep: null, job: null, at: 0, loading: false, timer: 0, error: '', missing: false, unsupported: false, result: null, busy: '', sel: new Set() };
  const PZ_LOCK = { 1: ['gesperrt', 'lock'], 2: ['nur Inhalt', 'lock'] };
  function pzHash(p) { return '#/platz/ordner' + (p === '/' ? '' : p.split('/').map(encodeURIComponent).join('/')); }
  // Pfad aus der Adresse: #/platz/ordner/opt/calagopus → /opt/calagopus.
  function pzRoute(key) {
    if (!key || !/^ordner(\/|$)/.test(key)) return null;
    const rest = key.slice(6).replace(/\/+$/, '');
    return rest === '' ? '/' : rest;
  }
  async function loadPz(path) {
    if (pz.loading || !core.svc()) return;
    pz.loading = true;
    const r = await core.call('/api/platz/ordner?pfad=' + encodeURIComponent(path), null, true);
    pz.loading = false;
    pz.at = Date.now();
    clearTimeout(pz.timer);
    pz.timer = 0;
    if (r.ok) {
      const was = pz.job && pz.job.laeuft;
      if (pz.path !== path) { pz.sel.clear(); pz.refocus = true; }
      pz.path = path; pz.rep = r.data.liste; pz.rep.eintraege = pz.rep.eintraege || []; pz.job = r.data.auftrag || null; pz.error = ''; pz.missing = false;
      if (was && pz.job && !pz.job.laeuft) {
        pz.result = pz.job.fehler ? { ok: false, text: pz.job.fehler } : { ok: true, text: pz.job.ergebnis };
        pz.sel.clear();
        // Was im Papierkorb liegt, zeigt Aufräumen; die Kachel Laufwerke
        // zeigt den neuen Platz.
        cl.at = 0;
        mon.at = 0; loadMon();
      }
    } else if (r.status === 404 && !(r.data && r.data.error)) {
      pz.unsupported = true;
    } else {
      pz.path = path; pz.rep = null;
      pz.missing = r.status === 404;
      pz.error = (r.data && r.data.error) || 'Fehler ' + r.status;
    }
    if (ui.page === 'platz' && pz.rep) {
      if (pz.job && pz.job.laeuft) pz.timer = setTimeout(() => loadPz(pz.path), 1500);
      else if (pz.rep.messung && pz.rep.messung.laeuft) pz.timer = setTimeout(() => loadPz(pz.path), 3000);
    }
    if (ui.page === 'platz' && core.work() && !modalOpen()) {
      // Neu gezeichnet: Der Fokus bleibt auf dem Ordnerpfad, wenn er dort war.
      const had = document.activeElement && document.activeElement.matches('[data-nav-focus]');
      core.rerender();
      const fx = document.querySelector('[data-nav-focus]');
      if (fx && (had || pz.refocus)) fx.focus({ preventScroll: true });
      pz.refocus = false;
    }
  }
  const pzRunning = () => !!(pz.job && pz.job.laeuft) || !!pz.busy;
  function pzDate(t) {
    if (!t) return '';
    const d = new Date(t * 1000);
    return String(d.getDate()).padStart(2, '0') + '.' + String(d.getMonth() + 1).padStart(2, '0') + '.' + d.getFullYear();
  }
  function pzKindTag(k) {
    if (!k) return null;
    const cls = /^Projekt /.test(k) ? 't-ok' : /^eingebunden/.test(k) ? 't-open' : k === 'Archiv' || k === 'hostdeck' ? 't-sug' : 't-mut';
    return el('span', { class: 'tag ' + cls }, k);
  }
  function pzCrumbs(path, focus) {
    const parts = path === '/' ? [] : path.slice(1).split('/');
    const items = [el('li', {}, parts.length ? el('a', { href: pzHash('/') }, 'Systemplatte') : el('span', { 'aria-current': 'page' }, 'Systemplatte'))];
    parts.forEach((n, i) => {
      const p = '/' + parts.slice(0, i + 1).join('/');
      items.push(el('li', {}, i === parts.length - 1 ? el('span', { class: 'mono', 'aria-current': 'page' }, n) : el('a', { class: 'mono', href: pzHash(p) }, n)));
    });
    return el('nav', { class: 'pz-crumbs', 'aria-label': 'Ordnerpfad', tabindex: focus ? '-1' : null, 'data-nav-focus': focus ? '' : null }, el('ol', {}, items));
  }
  // Größe des Backup-Ordners von jetzt: Nach „Vom Server löschen“ stünde
  // sonst bis zur nächsten Messung das alte Backup da.
  function pzLiveSize(x) {
    const bo = bk.rep && bk.rep.ordner;
    return bo && bo.pfad === x.pfad ? Object.assign({}, x, { groesse: bo.groesse, live: true }) : x;
  }
  function pzRow(x, total) {
    x = pzLiveSize(x);
    const g = x.schutz || {};
    const free = g.stufe === 3 && !x.einhaengepunkt;
    const on = pz.sel.has(x.pfad);
    const pct = total > 0 ? Math.min(100, x.groesse / total * 100) : 0;
    const bar = el('span', { class: 'ls-bar', 'aria-hidden': 'true' }, el('i'));
    bar.firstChild.style.width = pct.toFixed(1) + '%';
    const open = x.ordner && !x.einhaengepunkt && !x.nicht_lesbar;
    const name = open ? el('a', { class: 'mono', href: pzHash(x.pfad) }, x.name) : el('span', { class: 'mono' }, x.name);
    const lock = PZ_LOCK[g.stufe];
    const subs = [
      x.ziel ? 'Verknüpfung → ' + x.ziel : '',
      x.einhaengepunkt ? 'Einhängepunkt: andere Platte oder eingebundener Ordner, nicht mitgezählt' : '',
      x.nicht_lesbar ? 'nicht lesbar' : '',
      x.ordner && x.dateien ? plural(x.dateien, 'Datei', 'Dateien') : '',
      x.live ? 'Größe von jetzt' : '',
      // Grundordner sagt schon „nur Inhalt“; der Grund steht nur da, wo er
      // etwas Eigenes sagt.
      lock && g.grund && !/Grundordner|Ordner eines Benutzers/.test(g.grund) ? g.grund : '',
    ].filter(Boolean);
    return el('tr', { class: on ? 'is-on' : null },
      el('td', { class: 'pz-c' }, free ? el('input', { type: 'checkbox', checked: on, disabled: pzRunning(), 'aria-label': 'Auswählen: ' + x.name, 'data-fid': 'pz-sel-' + x.name,
        onchange: e => { if (e.target.checked) pz.sel.add(x.pfad); else pz.sel.delete(x.pfad); core.rerender(); } }) : lock ? el('span', { class: 'pz-lock', title: lock[0] }, ic(lock[1], 14), el('span', { class: 'sr' }, lock[0])) : null),
      el('td', { class: 'pz-n' }, el('span', { class: 'pz-nm' }, ic(x.ziel ? 'unlink' : x.ordner ? 'folder' : 'file', 16), name),
        subs.length ? el('span', { class: 'sub' }, subs.join(' · ')) : null,
        g.seite ? el('a', { class: 'sub', href: g.seite }, 'Eigener Weg im Leitstand') : null,
        el('span', { class: 'cl-tags pz-km' }, pzKindTag(g.art), lock ? el('span', { class: 'tag t-mut' }, lock[0]) : null)),
      el('td', { class: 'pz-k' }, el('span', { class: 'cl-tags' }, pzKindTag(g.art), lock ? el('span', { class: 'tag t-mut' }, lock[0]) : null)),
      el('td', { class: 'r nowrap' }, el('span', { class: 'ls-load pz-g' }, bar, el('span', { class: 'n' }, size(x.groesse)))),
      el('td', { class: 'r nowrap pz-d' }, pzDate(x.neueste)));
  }
  function pzTable(l) {
    const total = l.ordner.groesse;
    const rows = l.eintraege.map(x => pzRow(x, total));
    if (l.weitere > 0) {
      rows.push(el('tr', { class: 'pz-rest' }, el('td', { class: 'pz-c' }), el('td', { class: 'pz-n' }, el('span', { class: 'ls-mut' }, plural(l.weitere, 'weiterer Eintrag', 'weitere Einträge') + ', jeweils klein')),
        el('td', { class: 'pz-k' }), el('td', { class: 'r nowrap' }, size(l.weitere_groesse)), el('td', { class: 'pz-d' })));
    }
    return el('div', { class: 'ls-tw' }, el('table', { class: 'ls-t pz-t' },
      el('caption', { class: 'sr' }, 'Inhalt von ' + l.pfad + ', größte zuerst'),
      el('thead', {}, el('tr', {}, el('th', { scope: 'col', class: 'pz-c' }, el('span', { class: 'sr' }, 'Auswahl')), el('th', { scope: 'col' }, 'Name'), el('th', { scope: 'col', class: 'pz-k' }, 'Art'),
        el('th', { scope: 'col', class: 'r' }, 'Größe'), el('th', { scope: 'col', class: 'r pz-d' }, 'Zuletzt geändert'))),
      el('tbody', {}, rows)));
  }
  // Was du nicht siehst: gelöschte, aber noch offene Dateien und der Rest,
  // den nur df kennt.
  function pzHidden(l) {
    const out = [];
    const hidden = l.nicht_zuzuordnen || [];
    const held = hidden.reduce((a, h) => a + h.groesse, 0);
    if (hidden.length) {
      out.push(el('div', { class: 'ls-note op-warn' }, ic('info', 16), el('span', {},
        el('b', {}, 'Nicht zuzuordnen: ' + size(held)), ' belegen Dateien, die schon gelöscht sind, die aber ein Programm noch offen hält. Frei wird der Platz, wenn es sie schließt oder neu startet: ',
        hidden.map((h, i) => [i ? ', ' : '', el('span', { class: 'mono' }, h.prozess), ' (PID ' + h.pid + ', ' + size(h.groesse) + ')']), '.')));
    }
    const d = l.platte;
    if (d && d.gesamt && l.ordner.groesse > 0 && !l.teilweise) {
      const used = d.gesamt - d.frei;
      const gap = used - l.ordner.groesse - held;
      if (gap > 1e9) out.push(el('p', { class: 'ls-mut' }, 'Gemessen ' + size(l.ordner.groesse) + ', die Platte meldet ' + size(used) + ' belegt. Den Rest von ' + size(gap) + ' belegen Verwaltungsdaten des Dateisystems und was seit der Messung dazukam.'));
    }
    return out;
  }
  function pzStatus(l) {
    const ms = l.messung || {};
    if (ms.laeuft) return 'misst gerade' + (ms.eintraege ? ', ' + ms.eintraege.toLocaleString('de-DE') + ' Einträge bisher' : '') + ' …';
    if (l.frisch) return 'gerade frisch gemessen' + (l.nie_gemessen ? '' : ', kleiner Ordner');
    if (l.stand) return 'Stand ' + when(l.stand) + (ms.fehler ? ' · letzte Messung: ' + ms.fehler : '');
    return 'noch nicht gemessen';
  }
  function pzBody(m, path, focus) {
    const l = pz.rep;
    if (pz.missing) {
      return el('div', { class: 'ls-stack sm' }, pzCrumbs(path, focus), el('p', { class: 'gt-err', role: 'status' }, pz.error),
        el('p', {}, el('a', { href: pzHash(path.replace(/\/[^/]+$/, '') || '/') }, 'Zum Ordner darüber')));
    }
    if (!l || l.pfad !== path) {
      return el('div', { class: 'ls-stack sm', 'aria-busy': 'true' }, pzCrumbs(path, focus), pz.error ? el('p', { class: 'gt-err', role: 'status' }, pz.error) : [el('span', { class: 'skel skel-t skel-w' }), el('span', { class: 'skel skel-t' })]);
    }
    const sg = l.ordner.schutz || {};
    const chosen = l.eintraege.filter(x => pz.sel.has(x.pfad));
    const sum = chosen.reduce((a, x) => a + x.groesse, 0);
    const empty = !l.eintraege.length && !l.weitere;
    // Noch nie gemessen: ein Satz statt „0 B“ und Sperrhinweis (Idee 17).
    const fresh = !!(l.nie_gemessen && path === '/');
    return el('div', { class: 'ls-stack sm' },
      pzCrumbs(path, focus),
      fresh ? null : el('p', { class: 'ls-mut pz-st' }, size(l.ordner.groesse) + ' · ' + pzStatus(l)),
      l.hinweis ? el('p', { class: 'band b-warn', 'data-fid': 'pz-hinweis' }, l.hinweis + '. Solange löscht HostDeck hier nichts.') : null,
      l.teilweise ? el('p', { class: 'band b-warn' }, 'Teilweise gemessen: Die Messung hat ihre Obergrenze für Zeit oder Einträge erreicht. Die Zahlen sind eine Untergrenze.') : null,
      fresh ? el('p', {}, 'HostDeck hat die Platte noch nicht gemessen. „Neu messen“ geht sie einmal ganz durch, schonend im Hintergrund; danach klickst du dich ohne Warten durch. Nachts misst HostDeck von selbst neu.')
        : empty ? el('p', { class: 'ls-mut' }, l.ordner.einhaengepunkt ? 'Hier hängt eine andere Platte oder ein eingebundener Ordner; HostDeck misst und löscht dort nichts.' : 'Dieser Ordner ist leer.')
          : pzTable(l),
      !fresh && sg.stufe && sg.stufe < 3 && sg.grund ? el('p', { class: 'ls-note' }, ic('lock', 16), el('span', {}, 'Dieser Ordner ' + sg.grund + '.')) : null,
      path === '/' ? pzHidden(l) : null,
      empty ? null : el('div', { class: 'cl-foot' },
        el('span', {}, chosen.length ? [plural(chosen.length, 'Eintrag', 'Einträge') + ' gewählt, ', el('b', {}, size(sum))] : 'Ankreuzen, was weg soll. Gesperrtes hat kein Kästchen.'),
        el('button', { type: 'button', class: 'btn btn-p', 'data-fid': 'pz-vorschau', disabled: !chosen.length || pzRunning() || !!l.hinweis, 'aria-busy': pz.busy === 'vorschau' ? 'true' : null, onclick: pzStart }, ic('eye', 16), 'Löschen …')));
  }
  function pzResult() {
    const r = pz.result;
    if (!r || (pz.job && pz.job.laeuft)) return null;
    return el('div', { class: 'ls-note op-res ' + (r.ok ? 'is-ok' : 'is-err'), role: 'status' }, ic(r.ok ? 'done' : 'warn', 16), el('span', {}, r.text),
      el('button', { type: 'button', class: 'ls-ibtn op-x', 'aria-label': 'Meldung schließen', onclick: () => { pz.result = null; core.rerender(); } }, ic('x', 16)));
  }
  async function pzMeasure() {
    pz.busy = 'messen';
    core.rerender();
    const r = await core.call('/api/platz/messen', {});
    pz.busy = '';
    if (!r.ok) pz.result = { ok: false, text: (r.data && r.data.error) || 'Messen ging nicht (Fehler ' + r.status + ').' };
    else core.announce('Messung läuft');
    loadPz(pz.path || '/');
  }
  function pzSec(m, key) {
    if (pz.unsupported) return topDirs(m);
    const routed = pzRoute(key);
    const path = routed || pz.path || '/';
    const stale = !pz.at || Date.now() - pz.at > 60000 || (pz.job && pz.job.laeuft && !pz.timer) || (pz.rep && pz.rep.messung && pz.rep.messung.laeuft && !pz.timer);
    if (!pz.loading && (pz.path !== path || stale)) setTimeout(() => loadPz(path), 0);
    const ms = pz.rep && pz.rep.messung;
    const job = pz.job;
    const jobBox = job && job.laeuft ? el('div', { class: 'ls-stack sm' }, el('p', { role: 'status' }, el('b', {}, job.was + ' läuft …'), ' Du kannst die Seite schließen; es läuft weiter.'), jobSteps(job))
      : job && job.fehler && pz.result && job.schritte.length ? jobSteps(job) : null;
    return sec('Ordner und Dateien', el('div', { class: 'ls-stack' }, pzResult(), jobBox, pzBody(m, path, !!routed)), {
      cls: 'c12', sub: 'Systemplatte, größte zuerst',
      end: el('button', { type: 'button', class: 'btn btn-sm', 'data-fid': 'pz-messen', disabled: !!(ms && ms.laeuft) || pz.busy === 'messen', onclick: pzMeasure }, ic('refresh', 16), ms && ms.laeuft ? 'Misst …' : 'Neu messen'),
      help: 'HostDeck misst wie du: belegter Platz, nur diese Platte, Hardlinks einmal. Nachts und auf Knopfdruck, mit niedriger Priorität. Ordner unter 1 MB misst die Liste beim Öffnen frisch. Gesperrt ist, was zum System, zu Docker, zu HostDeck oder zu einem Projekt gehört; „nur Inhalt“ heißt: der Ordner bleibt, was darin liegt, lässt sich einzeln löschen. Andere Platten und Verknüpfungen fasst HostDeck nie an.' });
  }
  function pzPreview(p) {
    let trash = false;
    return clDialog(done => {
      const go = el('button', { type: 'button', class: 'btn btn-p', 'data-fid': 'pz-go', onclick: () => done({ trash }) }, ic('trash', 16), 'Endgültig löschen');
      const setMode = t => { trash = t; go.replaceChildren(ic('trash', 16), t ? 'In den Papierkorb' : 'Endgültig löschen'); };
      const radio = (val, label, hint, dis) => el('label', { class: 'pz-opt' + (dis ? ' is-off' : '') },
        el('input', { type: 'radio', name: 'pz-mode', value: val, checked: val === 'weg', disabled: !!dis, 'data-fid': 'pz-mode-' + val, onchange: () => setMode(val === 'korb') }),
        el('span', {}, el('b', {}, label), el('small', { class: 'blk' }, hint)));
      return [
        el('p', { class: 'ls-eyb' }, ic('eye', 16), el('span', {}, 'Vorschau · noch ist nichts passiert')),
        el('h2', { id: 'cl-h' }, p.eintraege.length ? plural(p.eintraege.length, 'Eintrag', 'Einträge') + ' löschen?' : 'Löschen geht so nicht'),
        p.eintraege.length ? el('section', { class: 'op-part' }, el('h3', {}, size(p.groesse) + ' ausgewählt' + (p.dateien ? ', ' + plural(p.dateien, 'Datei', 'Dateien') : '')),
          el('ul', { class: 'op-list cl-pl' }, p.eintraege.map(x => el('li', {}, el('span', { class: 'mono' }, x.pfad), ' · ' + size(x.groesse) + (x.ordner ? ' · ' + plural(x.dateien, 'Datei', 'Dateien') : x.verknuepfung ? ' · nur die Verknüpfung' : ''),
            (x.warnungen || []).map(w => el('span', { class: 'cl-warn blk' }, ic('warn', 14), w)))))) : null,
        p.geht_nicht && p.geht_nicht.length ? el('section', { class: 'op-part' }, el('h3', {}, 'Bleibt, weil gesperrt'),
          el('ul', { class: 'op-list' }, p.geht_nicht.map(b => el('li', {}, el('span', { class: 'mono' }, b.pfad), ': ' + b.grund, b.seite ? [' ', el('a', { href: b.seite }, 'Eigener Weg')] : null)))) : null,
        p.problem ? el('p', { id: 'cl-d', class: 'gt-lead' }, p.problem) : el('fieldset', { class: 'op-part pz-mode', id: 'cl-d' }, el('legend', {}, 'Wie löschen'),
          radio('weg', 'Endgültig löschen', 'Sofort weg, ' + size(p.frei_wird) + ' sind gleich frei. Zurückholen geht nicht.'),
          radio('korb', 'Erst in den Papierkorb', p.papierkorb_moeglich ? 'HostDeck sichert es auf diesem Server und löscht erst, wenn die Sicherung geprüft ist. Zurückholen geht bis ' + p.papierkorb_bis + ' unter Papierkorb; so lange bleibt der Platz zum größten Teil belegt.' : 'Geht hier nicht: ' + (p.papierkorb_grund || ''), !p.papierkorb_moeglich)),
        ...(p.hinweise || []).map(h => el('p', { class: 'ls-note' }, ic('info', 16), el('span', {}, h))),
        el('div', { class: 'gt-acts' }, el('button', { type: 'button', class: 'btn', 'data-autofocus': '', 'data-fid': 'pz-abbrechen', onclick: () => done(false) }, p.problem ? 'Schließen' : 'Abbrechen'), p.problem ? null : go)];
    });
  }
  async function pzStart() {
    if (pzRunning()) return;
    const paths = pz.rep.eintraege.map(x => x.pfad).filter(p => pz.sel.has(p));
    pz.busy = 'vorschau'; pz.result = null;
    core.rerender();
    const r = await core.call('/api/platz/vorschau', { auswahl: paths });
    pz.busy = '';
    if (!r.ok) { pz.result = { ok: false, text: (r.data && r.data.error) || 'Die Vorschau ging nicht (Fehler ' + r.status + ').' }; core.rerender(); return; }
    const ok = await pzPreview(r.data);
    if (!ok) {
      core.rerender();
      // Der Knopf ist neu gezeichnet; der Fokus kehrt dorthin zurück.
      const b = document.querySelector('[data-fid=pz-vorschau]');
      if (b) b.focus();
      return;
    }
    const a = await core.call('/api/platz/loeschen', { stand: r.data.stand, papierkorb: ok.trash });
    if (!a.ok) {
      if (!(a.data && a.data.abgebrochen)) pz.result = { ok: false, text: (a.data && a.data.error) || 'Das ging nicht (Fehler ' + a.status + ').' };
      core.rerender();
      return;
    }
    pz.job = a.data.auftrag;
    core.announce(pz.job.was + ' läuft');
    loadPz(pz.path);
  }
  function pagePlatz(m, key) {
    if (core.svc()) return pagePlatzLive(m, key);
    return pagePlatzInv(m);
  }
  function pagePlatzInv(m) {
    const sp = m.s.space;
    if (!sp || !sp.total) {
      const c = core.discoverCtl('');
      return [head({ i: 'platz', k: 'Speicherplatz', h: ['Zum Platz ', em('warn', 'weiß HostDeck noch nichts'), '.'],
        p: 'Die Bestandsaufnahme hat die Belegung der Systemplatte nicht erfasst.' + (c ? ' Neu erfassen hilft meist.' : ' Neu erfassen mit sudo ./hostdeck discover.'), a: quickDiscover('') })];
    }
    const free = Math.max(0, sp.total - sp.used);
    const parts = [
      ['docker', 'Docker', 'Images, Build-Cache und Container-Schichten', sp.docker],
      ['volumes', 'Volumes', 'Daten der Container', sp.volumes],
      ['data', 'Ordner deiner Projekte', 'Compose-Ordner und Datenordner', sp.data],
      ['system', 'System', 'Programme, Logs, Paket-Caches', sp.system + sp.other],
      ['swap', 'Swap', 'Auslagerungsdatei', sp.swap],
    ].filter(x => x[3] > 0).sort((a, b) => b[3] - a[3]);
    const bar = el('div', { class: 'ls-meter big', role: 'img', 'aria-label': size(sp.used) + ' von ' + size(sp.total) + ' belegt' },
      parts.map(([k, , , v]) => { const s = el('span', { class: 'm-' + k }); s.style.width = (v / sp.total * 100).toFixed(2) + '%'; return s; }));
    const legend = el('ul', { class: 'ls-legend' }, parts.map(([k, t, d, v]) => el('li', {}, el('i', { class: 'sw m-' + k }), el('span', {}, el('b', {}, t), el('small', {}, d)), el('b', {}, bytes(v)))),
      el('li', {}, el('i', { class: 'sw m-free' }), el('span', {}, el('b', {}, 'Frei'), el('small', {}, 'noch verfügbar')), el('b', {}, bytes(free))));
    const big = m.projects.map(p => [p, core.projectBytes(p)]).filter(x => x[1] > 0).sort((a, b) => b[1] - a[1]).slice(0, 6);
    const pct = Math.round(sp.used / sp.total * 100);
    return [head({ i: 'platz', k: 'Speicherplatz · Systemplatte · Stand ' + fmtDate(m.s.created),
      h: [size(sp.used) + ' von ' + size(sp.total) + ' ', em(diskLevel(sp.used, sp.total) || (limits() ? 'ok' : ''), 'belegt'), '.'],
      p: 'So verteilt sich der Platz auf der Systemplatte. Aufräumen per Knopf gibt es, sobald HostDeck als Dienst läuft.' }),
    sp.partial ? el('p', { class: 'band b-warn' }, 'Nicht alle Ordner ließen sich lesen; die Zahlen sind eine Untergrenze.') : null,
    el('div', { class: 'ls-grid eq' },
      sec('Wo der Platz steckt', el('div', { class: 'ls-stack sm' }, bar, legend), { cls: 'c7' }),
      big.length ? sec('Größte Projekte', el('ul', { class: 'ls-legend' }, big.map(([p, n]) => el('li', {}, ic('projekte', 14),
        el('span', {}, el('a', { href: '#/projekte/' + encodeURIComponent(p.key) }, p.name), el('small', {}, plural(p.services.length, 'Dienst', 'Dienste') + ' · ' + decWord(p.decision))), el('b', {}, bytes(n))))), { cls: 'c5', sub: 'Ordner, Daten und Images' }) : null)];
  }

  // ---------- Sicherheit und Leistung ----------
  // Erst messen, dann Vorschläge. Jede Änderung einzeln, mit Vorschau,
  // Passwort und Rückweg. Schließt HostDeck einen Port, wartet der Dienst auf ein
  // Lebenszeichen des Leitstands; kommt keins, nimmt er die Änderung nach
  // 90 Sekunden von selbst zurück.
  const sc = { rep: null, at: 0, loading: false, error: '', result: null, busy: '' };
  const pf = { rep: null, at: 0, loading: false, error: '', result: null, busy: '' };
  const mon = { rep: null, at: 0, loading: false, error: '' };
  async function loadLive(s, path, page) {
    if (s.loading || !core.svc()) return;
    s.loading = true;
    const r = await core.call(path, null, true);
    s.loading = false;
    s.at = Date.now();
    if (r.ok) { s.rep = r.data; s.error = ''; } else if (r.status !== 404) s.error = (r.data && r.data.error) || 'Fehler ' + r.status;
    if (ui.page === page && core.work() && !modalOpen() && !uw.hover) core.rerender();
    // Die Projekt-Gruppen der Leiste hängen an den erkannten Spielservern.
    else if (s === mon) refreshChrome();
  }
  // Badge in der Seitenleiste: wartet eine Änderung oder fehlen die Regeln.
  function scBadge() {
    const r = sc.rep;
    if (!r) return null;
    if (r.ausstehend) return cnt('wartet', 'acc');
    if (r.firewall && r.firewall.verloren) return cnt('Regeln fehlen', 'err');
    return null;
  }
  const loadSc = () => loadLive(sc, '/api/sicherheit', 'sicherheit');
  const loadPf = () => loadLive(pf, '/api/leistung', 'leistung');
  const loadMon = () => loadLive(mon, '/api/ueberwachung', ['ueberwachung', 'lage', 'projekte', 'domains', 'platz'].includes(ui.page) ? ui.page : 'leistung');
  function liveResult(s) {
    const r = s.result;
    if (!r) return null;
    return el('div', { class: 'ls-note op-res ' + (r.ok ? 'is-ok' : 'is-err'), role: 'status' }, ic(r.ok ? 'done' : 'warn', 16), el('span', {}, r.text),
      el('button', { type: 'button', class: 'ls-ibtn op-x', 'aria-label': 'Meldung schließen', onclick: () => { s.result = null; core.rerender(); } }, ic('x', 16)));
  }
  function liveWait(s, page, title) {
    if (!s.loading && !s.at) setTimeout(page === 'sicherheit' ? loadSc : page === 'puls' ? loadMon : loadPf, 0);
    return [head({ i: page, k: title, h: s.error ? ['Der Stand ist ', em('err', 'nicht erreichbar'), '.'] : 'HostDeck misst …', p: s.error || 'Das dauert ein paar Sekunden.' }),
      sec(null, el('div', { class: 'ls-stack' }, el('span', { class: 'skel skel-h' }), el('span', { class: 'skel skel-t skel-w' }), el('span', { class: 'skel skel-t' })))];
  }
  const errText = (r, what) => (r.data && r.data.error) || what + ' ging nicht (Fehler ' + r.status + ').';
  // Vorschau als Dialog: was passiert, Rückweg, Hinweise; bei einem Problem
  // nur Schließen.
  function livePreview(o) {
    return clDialog(done => [
      el('p', { class: 'ls-eyb' }, ic('eye', 16), el('span', {}, 'Vorschau · noch ist nichts passiert')),
      el('h2', { id: 'cl-h' }, o.title),
      o.body || null,
      o.problem ? el('p', { id: 'cl-d', class: 'gt-lead' }, o.problem) : el('section', { class: 'op-part', id: 'cl-d' }, el('h3', {}, 'Was passiert'),
        el('ol', { class: 'op-list' }, (o.steps || []).map(x => el('li', {}, x)))),
      !o.problem && o.effect ? el('section', { class: 'op-part' }, el('h3', {}, 'Danach'), el('p', {}, o.effect)) : null,
      !o.problem && o.back ? el('section', { class: 'op-part' }, el('h3', {}, 'Rückweg'), el('p', {}, o.back)) : null,
      ...(o.notes || []).map(h => el('p', { class: 'ls-note op-warn' }, ic('warn', 16), el('span', {}, h))),
      el('div', { class: 'gt-acts' }, el('button', { type: 'button', class: 'btn', onclick: () => done(false) }, o.problem ? 'Schließen' : 'Abbrechen'),
        o.problem ? null : el('button', { type: 'button', class: 'btn btn-p', onclick: () => done(true) }, ic(o.icon || 'check', 16), o.ok))]);
  }

  const portText = p => p.von === p.bis ? p.von + '/' + p.proto : p.von + '–' + p.bis + '/' + p.proto;
  const PORT_KIND = { ssh: 'SSH', web: 'Caddy', panel: 'Panel', container: 'Container', prozess: 'Programm', unbekannt: 'unbekannt', geschlossen: 'geschlossen' };
  // Ports kompakt (Idee 16): eine Tabelle, nach Projekt oder Dienst
  // gruppiert und nach Port sortiert; die Erklärung steht einmal je Gruppe.
  function scList(r) {
    if (!r.ports.length) return el('p', { class: 'ls-empty ls-mut' }, 'Von außen lauscht nichts außer dem, was HostDeck nicht anfasst.');
    const groups = new Map();
    for (const p of r.ports) {
      const k = p.projekt ? 'p:' + p.projekt : p.art === 'web' || p.art === 'ssh' ? p.art : 'w:' + (p.wer || '');
      if (!groups.has(k)) {
        const n = p.projekt ? null : cName(p.wer || 'unbekannt');
        groups.set(k, { t: p.projekt ? 'Projekt ' + p.projekt : p.art === 'web' ? 'Caddy' : p.art === 'ssh' ? 'SSH' : n.t, id: n && n.id, kind: PORT_KIND[p.art] || '', list: [] });
      }
      groups.get(k).list.push(p);
    }
    const rank = g => g.list.some(p => p.vorschlag && !p.geschlossen) ? 0 : g.list.some(p => !p.geschlossen && !p.bleibt_offen) ? 1 : g.list.some(p => p.geschlossen) ? 2 : 3;
    const list = [...groups.values()];
    for (const g of list) g.list.sort((x, y) => x.von - y.von || x.proto.localeCompare(y.proto));
    list.sort((x, y) => rank(x) - rank(y) || x.list[0].von - y.list[0].von);
    const why = p => p.bleibt_offen || p.rat || '';
    const busy = !!sc.busy || !!sc.rep.ausstehend;
    const state = p => p.geschlossen ? [ic('lock', 15), 'von HostDeck geschlossen']
      : p.bleibt_offen ? [ic('ok', 15), 'bleibt offen']
        : p.bewusst_offen ? [ic('eyeoff', 15), 'bewusst offen gelassen']
          : p.vorschlag ? [ic('warn', 15), p.risiko === 'hoch' ? 'schließen, Risiko hoch' : 'HostDeck rät: schließen']
            : [ic('door', 15), 'offen, du entscheidest'];
    const rows = [];
    for (const g of list) {
      const same = g.list.every(p => why(p) === why(g.list[0]));
      rows.push(el('tr', { class: 'sc-gh' }, el('th', { scope: 'rowgroup', colspan: '3' },
        el('b', {}, g.t), g.id ? el('span', { class: 'mono ls-mut' }, ' ' + g.id) : null,
        same && why(g.list[0]) ? el('span', { class: 'ls-mut blk' }, why(g.list[0])) : null)));
      for (const p of g.list) {
        const who = cName(p.wer || 'unbekannt');
        const extra = [];
        if (p.wie_vorher && !p.geschlossen) extra.push('auf dem alten Server zu');
        if (p.domains && p.domains.length && !p.geschlossen) extra.push('auch über ' + p.domains.join(', '));
        if (!p.geschlossen && p.risiko === 'mittel') extra.push('Risiko mittel');
        if (p.bewusst_offen && !p.geschlossen) extra.push('so gelassen seit ' + when(p.bewusst_offen));
        const close = p.geschlossen || p.bleibt_offen ? null
          : el('button', { type: 'button', class: 'btn btn-sm' + (p.vorschlag ? ' btn-p' : ''), 'data-fid': 'sc-' + p.schluessel, disabled: busy, 'aria-label': portText(p) + ' schließen …', onclick: () => scChange({ schliessen: [p.schluessel] }, p) }, ic('lock', 16), 'Schließen …');
        // Bewusst offen lassen: Der Vorschlag verschwindet,
        // die Firewall bleibt, wie sie ist; „Wieder vorschlagen“ nimmt es
        // zurück.
        const keep = p.geschlossen || p.bleibt_offen ? null
          : p.bewusst_offen ? el('button', { type: 'button', class: 'btn btn-sm btn-g', 'data-fid': 'sc-unkeep-' + p.schluessel, disabled: busy, 'aria-label': portText(p) + ' wieder vorschlagen', onclick: () => scKeepOpen(p, false) }, ic('undo', 16), 'Wieder vorschlagen')
            : p.vorschlag ? el('button', { type: 'button', class: 'btn btn-sm btn-g', 'data-fid': 'sc-keep-' + p.schluessel, disabled: busy, 'aria-label': portText(p) + ' bewusst offen lassen', onclick: () => scKeepOpen(p, true) }, ic('eyeoff', 16), 'Bewusst offen lassen') : null;
        const btn = p.geschlossen
          ? el('button', { type: 'button', class: 'btn btn-sm btn-g', 'data-fid': 'sc-' + p.schluessel, disabled: busy, onclick: () => scChange({ oeffnen: [p.schluessel] }, p) }, ic('undo', 16), 'Öffnen …')
          : close || keep ? el('div', { class: 'sc-acts' }, close, keep) : null;
        rows.push(el('tr', { class: p.vorschlag && !p.geschlossen ? 'is-sug' : p.geschlossen ? 'is-shut' : '' },
          // Der Dienst steht nur, wenn er mehr sagt als die Gruppe.
          el('td', { class: 'sc-port' }, el('span', { class: 'mono nowrap' }, portText(p)),
            who.t.toLowerCase() !== g.t.toLowerCase() && !(p.art === 'web' || p.art === 'ssh') ? el('span', { class: 'sub', title: who.id || null }, who.t) : null,
            same ? null : el('span', { class: 'sub' }, why(p)), extra.length ? el('span', { class: 'sub' }, extra.join(' · ')) : null),
          el('td', { class: 'sc-st' }, el('span', { class: 'sc-w' + (p.vorschlag && !p.geschlossen ? (p.risiko === 'hoch' ? ' err' : ' warn') : '') }, state(p))),
          el('td', { class: 'r' }, btn)));
      }
    }
    return el('div', { class: 'ls-tw' }, el('table', { class: 'ls-t sc-t' },
      el('caption', { class: 'sr' }, 'Ports, die von außen erreichbar sind, nach Projekt oder Dienst'),
      el('thead', {}, el('tr', {}, el('th', { scope: 'col' }, 'Port'), el('th', { scope: 'col' }, 'Zustand'), el('th', { scope: 'col' }, el('span', { class: 'sr' }, 'Aktion')))),
      el('tbody', {}, rows)));
  }
  // Container mit dem Namen, den du kennst (Idee 12): erkannte Spielserver
  // mit ihrem Namen, die Kennung nur als Zusatz.
  function cName(name) {
    if (core.svc() && !mon.rep && !mon.loading) setTimeout(loadMon, 0);
    // Derselbe Name wie unter Projekte: ein Server, den ein Panel startet,
    // mit dem Namen des Spiels; ein Container eines Compose-Projekts mit
    // seinem kurzen Namen (Datenprüfung M2).
    const g = gameList(mon.rep).find(x => x.container === name);
    if (g && !g.projekt && g.name && g.name !== name) return { t: gName(g), id: shortName(name) };
    return { t: shortName(name), id: shortName(name) !== name ? name : '' };
  }
  function scDocker(r) {
    if (!r.docker_zugriff.length) return el('p', { class: 'ls-mut' }, 'Kein Container hat vollen Zugriff auf Docker.');
    return el('ul', { class: 'ls-legend' }, r.docker_zugriff.map(d => el('li', {}, ic(d.panel ? 'server' : d.offen && d.offen.length ? 'warn' : 'info', 14),
      el('span', {}, el('b', { title: d.name }, cName(d.name).t + (d.projekt && d.projekt !== d.name ? ' · ' + d.projekt : '')), el('small', {}, d.rat)),
      el('span', { class: 'tag ' + (d.panel ? 't-mut' : d.offen && d.offen.length ? 't-err' : d.laeuft ? 't-open' : 't-mut') }, d.panel ? 'Panel' : d.offen && d.offen.length ? 'offen' : d.laeuft ? 'läuft' : 'gestoppt'))));
  }
  function scFirewall(r) {
    const f = r.firewall;
    const yes = (b, t, n) => el('div', { class: 'ls-kv' }, el('span', {}, t), el('span', {}, b ? (t === 'nftables' ? 'da' : 'an') : n || 'aus'));
    return el('div', { class: 'ls-stack sm' },
      el('div', { class: 'ls-kvs flat' }, yes(f.ufw, 'ufw'), yes(f.firewalld, 'firewalld'), yes(f.nft, 'nftables', 'fehlt'), yes(f.eigene, 'Regeln von HostDeck', 'keine')),
      el('p', { class: 'note' }, f.hinweis),
      f.nft ? null : el('p', { class: 'ls-note op-warn' }, ic('warn', 16), el('span', {}, 'Ohne nftables kann HostDeck keine Ports schließen. Auf Debian und Ubuntu heißt das Paket nftables.')));
  }
  function scSSH(r) {
    const s = r.ssh;
    if (!s.bekannt) return el('p', { class: 'ls-mut' }, 'sshd ließ sich nicht befragen.');
    return el('div', { class: 'ls-stack sm' },
      el('div', { class: 'ls-kvs flat' },
        el('div', { class: 'ls-kv' }, el('span', {}, 'Port'), el('span', {}, s.ports.join(', '))),
        el('div', { class: 'ls-kv' }, el('span', {}, 'Anmelden mit Passwort'), el('span', {}, s.passwort === 'yes' ? 'an' : s.passwort === 'no' ? 'aus' : s.passwort || '?')),
        el('div', { class: 'ls-kv' }, el('span', {}, 'root darf sich anmelden'), el('span', {}, ({ yes: 'ja, auch mit Passwort', no: 'nein', 'prohibit-password': 'nur mit Schlüssel', 'without-password': 'nur mit Schlüssel', 'forced-commands-only': 'nur feste Befehle' })[s.root] || s.root || '?'))),
      ...(s.rat || []).map(t => el('p', { class: 'ls-note op-warn' }, ic('info', 16), el('span', {}, t))),
      // Steht der Satz schon im Rat, nicht noch einmal.
      (s.rat || []).some(t => t.includes('aussperrst')) ? null : el('p', { class: 'note' }, 'SSH ändert HostDeck nicht, damit du dich nie aussperrst.'));
  }
  async function scKeepOpen(p, on) {
    if (sc.busy) return;
    sc.busy = 'offen';
    core.rerender();
    const r = await core.call('/api/sicherheit/offen-lassen', { schluessel: p.schluessel, zuruecknehmen: !on });
    sc.busy = '';
    sc.result = r.ok ? { ok: true, text: r.data.ergebnis } : { ok: false, text: errText(r, on ? 'Offen lassen' : 'Zurücknehmen') };
    sc.at = 0; hn.at = 0;
    await Promise.all([loadSc(), loadHints()]);
    focusFid(on ? 'sc-unkeep-' + p.schluessel : 'sc-keep-' + p.schluessel);
  }
  async function scChange(ch, port) {
    if (sc.busy) return;
    sc.busy = 'vorschau'; sc.result = null;
    core.rerender();
    const r = await core.call('/api/sicherheit/vorschau', ch);
    sc.busy = '';
    if (!r.ok) { sc.result = { ok: false, text: errText(r, 'Die Vorschau') }; core.rerender(); return; }
    const p = r.data;
    const closing = (ch.schliessen || []).length > 0;
    const ok = await livePreview({
      title: portText(port) + (closing ? ' von außen schließen?' : ' wieder öffnen?'),
      body: el('section', { class: 'op-part' }, el('h3', {}, closing ? 'Wird geschlossen' : 'Wird wieder offen'),
        el('ul', { class: 'op-list' }, (closing ? p.schliessen : p.oeffnen).map(x => el('li', {}, el('span', { class: 'mono' }, portText(x)), ' · ' + (x.wer || 'unbekannt'))))),
      problem: p.problem, steps: p.schritte, back: p.rueckweg, notes: p.hinweise,
      effect: closing ? 'Von außen kommt niemand mehr an diesen Port. Auf dem Server selbst, über Caddy und für deine Container bleibt er erreichbar.' : 'Der Port ist wieder von außen erreichbar, wie vor HostDeck.',
      ok: closing ? 'Schließen' : 'Öffnen', icon: closing ? 'lock' : 'undo',
    });
    if (!ok) { core.rerender(); return; }
    sc.busy = 'aendern';
    core.rerender();
    const a = await core.call('/api/sicherheit/aendern', Object.assign({}, ch, { stand: p.stand }));
    sc.busy = '';
    if (!a.ok) {
      if (!(a.data && a.data.abgebrochen)) sc.result = { ok: false, text: errText(a, 'Die Änderung') };
      sc.at = 0; loadSc();
      return;
    }
    sc.result = { ok: true, text: a.data.ergebnis };
    core.announce(a.data.ergebnis);
    // Erreicht der Leitstand den Dienst danach noch mit einer neuen
    // Anfrage, bestätigt er die Änderung von selbst.
    if (a.data.bestaetigen_bis) setTimeout(scKeep, 3000);
    sc.at = 0; loadSc();
  }
  async function scKeep() {
    const r = await core.call('/api/sicherheit/behalten', {}, true);
    if (r.ok) { sc.result = { ok: true, text: r.data.ergebnis }; core.announce(r.data.ergebnis); } else if (r.status !== 409) sc.result = { ok: false, text: errText(r, 'Die Bestätigung') };
    sc.at = 0; loadSc();
  }
  async function scReload() {
    if (sc.busy) return;
    sc.busy = 'laden';
    core.rerender();
    const r = await core.call('/api/sicherheit/neu-laden', {});
    sc.busy = '';
    if (r.ok) sc.result = { ok: true, text: r.data.ergebnis }; else if (!(r.data && r.data.abgebrochen)) sc.result = { ok: false, text: errText(r, 'Neu laden') };
    sc.at = 0; loadSc();
  }
  function pageSicherheit() {
    if (!core.svc()) return [head({ i: 'sicherheit', k: 'Sicherheit', h: 'Gibt es nur im Dienst.', p: 'Offene Ports misst HostDeck, wenn es als Dienst auf dem Server läuft (sudo ./hostdeck einrichten).' })];
    if (!sc.loading && (!sc.at || Date.now() - sc.at > 60000)) setTimeout(loadSc, 0);
    const r = sc.rep;
    if (!r) return liveWait(sc, 'sicherheit', 'Sicherheit');
    const sug = r.ports.filter(p => p.vorschlag && !p.geschlossen).length;
    const open = r.ports.filter(p => !p.geschlossen).length;
    const kept = r.ports.filter(p => p.bewusst_offen && !p.geschlossen).length;
    const keptT = kept ? (kept === 1 ? ' Einen lässt du bewusst offen.' : ' ' + kept + ' lässt du bewusst offen.') : '';
    const h = r.problem ? ['Messen ging ', em('err', 'nicht ganz'), '.']
      : sug ? [plural(open, 'Port ist', 'Ports sind') + ' von außen offen. ', em(r.ports.some(p => p.vorschlag && !p.geschlossen && p.risiko === 'hoch') ? 'err' : 'warn', sug === 1 ? '1 davon' : sug + ' davon'), ' würde HostDeck schließen.' + keptT]
        : [plural(open, 'Port ist', 'Ports sind') + ' von außen offen, ', em('ok', 'nichts Auffälliges'), '.' + keptT];
    const pend = r.ausstehend;
    return [head({ i: 'sicherheit', k: 'Sicherheit · Stand ' + when(r.stand_von), h,
      p: 'Was an deinem Server von außen erreichbar ist. HostDeck schließt nur, was du wählst, und nie SSH, die Webports 80 und 443 oder Ports des Panels. Nach jeder Änderung prüft der Leitstand, ob er dich noch erreicht; sonst nimmt HostDeck sie nach 90 Sekunden von selbst zurück.',
      a: el('button', { type: 'button', class: 'btn btn-sm', 'data-fid': 'sc-ref', disabled: sc.loading, onclick: () => { sc.at = 0; loadSc(); } }, ic('refresh', 16), 'Aktualisieren') }),
    liveResult(sc), sc.error ? el('p', { class: 'band b-err', role: 'status' }, sc.error) : null, r.problem ? el('p', { class: 'band b-warn', role: 'status' }, r.problem) : null,
    pend ? el('div', { class: 'band b-warn', role: 'status' }, el('p', {}, 'Wartet auf Bestätigung: ' + pend.was + '. Ohne Bestätigung nimmt HostDeck sie um ' + hm(new Date(pend.bis)) + ' Uhr zurück.'),
      el('div', { class: 'acts' }, el('button', { type: 'button', class: 'btn btn-sm btn-p', 'data-fid': 'sc-keep', onclick: scKeep }, ic('check', 16), 'Behalten'))) : null,
    r.firewall.verloren ? el('div', { class: 'band b-err', role: 'status' }, el('p', {}, 'Die Regeln von HostDeck fehlen gerade, etwa nach einem Neustart von nftables. Die Ports, die du geschlossen hast, sind deshalb wieder offen.'),
      el('div', { class: 'acts' }, el('button', { type: 'button', class: 'btn btn-sm btn-p', 'data-fid': 'sc-reload', disabled: !!sc.busy, onclick: scReload }, ic('refresh', 16), 'Neu laden …'))) : null,
    el('div', { class: 'ls-grid eq' },
      sec('Von außen erreichbar', scList(r), { cls: 'c12 flush', sub: 'auf allen Adressen des Servers, ohne das, was nur auf ihm selbst lauscht' }),
      sec('Voller Zugriff auf Docker', scDocker(r), { cls: 'c12' }),
      sec('Firewall', scFirewall(r), { cls: 'c6' }),
      sec('SSH', scSSH(r), { cls: 'c6' }))];
  }

  // Leistung
  // Prozessor und Arbeitsspeicher jetzt: läuft die Überwachung, ihr letzter
  // Wert wie auf Übersicht und Überwachung (eine Quelle, Datenprüfung H2);
  // sonst die eigene Messung der Leistung.
  function pfMon() { const r = mon.rep; return r && r.laeuft && r.werte ? r.werte : null; }
  function pfCpu(r) { const v = pfMon(); return v && v['s/cpu'] != null ? v['s/cpu'] : r.prozessor.belegt; }
  function pfUsed(r) { const v = pfMon(), m = r.speicher; return v && v['s/ram'] != null ? v['s/ram'] : m.gesamt - m.verfuegbar; }
  function pfNow(r) {
    const m = r.speicher, c = r.prozessor;
    const used = pfUsed(r), busy = pfCpu(r);
    const out = [];
    // Farben aus den Grenzen der Überwachung, wie auf der Übersicht.
    if (core.svc() && !mon.rep && !mon.loading) setTimeout(loadMon, 0);
    const g = limits();
    const [rn, ru] = m.gesamt ? splitUnit(size(used)) : ['?', ''];
    out.push(kpi({ l: 'Arbeitsspeicher', v: rn, u: ru, kind: m.gesamt ? ramLevel(used, m.gesamt) : '', id: 'ram', wert: m.gesamt ? used / m.gesamt * 100 : null, s: m.gesamt ? 'von ' + size(m.gesamt) + ' belegt' : 'nicht lesbar',
      spark: m.gesamt ? meter(used / m.gesamt, 'Arbeitsspeicher belegt') : false }));
    if (m.swap) {
      const [sn, su] = splitUnit(size(m.swap - m.swap_frei));
      out.push(kpi({ l: 'Swap', v: sn, u: su, s: 'von ' + size(m.swap) + (m.swap_von_hostdeck ? ', von HostDeck' : ''), spark: meter((m.swap - m.swap_frei) / m.swap, 'Swap belegt', 'swap') }));
    } else out.push(kpi({ l: 'Swap', v: 'keiner', s: 'swappiness ' + (m.swappiness < 0 ? '?' : m.swappiness), spark: false }));
    const [cn, cu] = splitUnit(cpuText(busy));
    out.push(kpi({ l: 'Prozessor', v: cn, u: cu, kind: cpuLevel(busy), id: 'cpu', wert: busy, s: plural(c.kerne, 'Kern', 'Kerne') + ' · Last ' + c.last.map(x => x.toLocaleString('de-DE', { minimumFractionDigits: 2, maximumFractionDigits: 2 })).join(' / '), spark: meter(busy / 100, 'Prozessor belegt') }));
    const [tn, tu] = splitUnit(pctText(c.steal, 1));
    const hi = g && c.steal > g.steal_gelb;
    out.push(kpi({ l: 'Steal', v: tn, u: tu, kind: hi ? 'warn' : '', id: 'steal', wert: c.steal, s: hi ? 'der Hoster teilt die Hardware stark' : 'Rechenzeit, die der Hoster anderen gibt', spark: meter(c.steal / 100, 'Steal', hi ? 'cache' : 'system') }));
    return el('div', { class: 'ls-kpis' }, out);
  }
  function pfSug(x, first) {
    return el('li', { class: 'sc-it' + (x.problem ? '' : ' is-sug') },
      el('span', { class: 'pf-g' }, el('b', {}, x.gewinn), x.schluessel === 'logs' ? el('small', {}, 'Logs heute') : null),
      el('span', { class: 'cl-t' }, el('b', {}, x.titel), el('span', { class: 'ls-mut blk' }, x.text),
        x.problem ? el('span', { class: 'cl-warn blk' }, ic('warn', 14), x.problem) : null,
        el('span', { class: 'cl-tags' }, el('span', { class: 'tag t-ok' }, 'Risiko ' + x.risiko), x.wie_vorher ? el('span', { class: 'tag t-sug' }, 'wie auf dem alten Server') : null, x.rueckweg ? el('span', { class: 'tag t-mut' }, x.zuruecknehmen ? 'wieder einschaltbar' : 'zurücknehmbar') : null)),
      el('button', { type: 'button', class: 'btn btn-sm' + (x.zuruecknehmen ? ' btn-g' : first ? ' btn-p' : ''), 'data-fid': 'pf-' + x.schluessel, disabled: !!x.problem || !!pf.busy, onclick: () => pfChange(x) }, ic(x.zuruecknehmen ? 'undo' : 'eye', 16), x.zuruecknehmen ? 'Zurücknehmen …' : 'Ansehen …'));
  }
  function pfSugs(r) {
    // Speichergrenzen schlägt HostDeck noch nicht vor; das steht ehrlich da, mit
    // dem echten Stand je Container statt einer festen Behauptung
    // (Datenprüfung M6).
    const cs = (r.container || []).filter(c => c.laeuft);
    const lim = cs.filter(c => memLimit(c)).length;
    const now = !cs.length ? '' : !lim ? 'Heute darf jeder laufende Container den ganzen Arbeitsspeicher nehmen. '
      : lim === cs.length ? 'Jeder laufende Container hat schon eine eigene Grenze. '
        : lim + ' von ' + plural(cs.length, 'laufendem Container', 'laufenden Containern') + ' ' + (lim === 1 ? 'hat' : 'haben') + ' eine eigene Grenze, die übrigen dürfen den ganzen Arbeitsspeicher nehmen. ';
    const fixed = el('li', { class: 'sc-it' }, el('span', { class: 'pf-g' }, el('b', {}, 'Schutz'), el('small', {}, 'vor Ausreißern')),
      el('span', { class: 'cl-t' }, el('b', {}, 'Speichergrenzen je Container'), el('span', { class: 'ls-mut blk' }, now + 'Passende Grenzen aus dem gemessenen Verbrauch schlägt HostDeck in einer späteren Version vor. Bis dahin siehst du unter Überwachung, was jeder Container braucht.'),
        el('span', { class: 'cl-tags' }, el('span', { class: 'tag t-mut' }, 'kommt später'))),
      link('#/ueberwachung/7d', 'Verbrauch ansehen'));
    const lead = r.vorschlaege.find(x => !x.problem && !x.zuruecknehmen);
    return el('ul', { class: 'sc-list' }, r.vorschlaege.map(x => pfSug(x, x === lead)), fixed);
  }
  function pfLogs(r) {
    const l = r.logs;
    const list = r.container.filter(c => c.groesse > 0).slice(0, 8);
    return el('div', { class: 'ls-stack sm' },
      el('p', { class: 'note pad' }, l.bekannt ? ['Zusammen ', el('b', {}, size(l.gesamt)), '. ', l.begrenzt ? 'Neue Container haben eine Grenze' + (l.max_size ? ' (' + (l.max_file || '1') + ' × ' + l.max_size + ')' : '') + '.' : 'Neue Container haben keine Grenze.', l.ohne_grenze ? ' ' + plural(l.ohne_grenze, 'Container schreibt', 'Container schreiben') + ' ohne Grenze.' : ''] : l.problem || 'Docker antwortet nicht.'),
      list.length ? el('div', { class: 'ls-tw' }, el('table', { class: 'ls-t' },
        el('caption', { class: 'sr' }, 'Logs je Container, die größten zuerst'),
        el('thead', {}, el('tr', {}, el('th', { scope: 'col' }, 'Container'), el('th', { scope: 'col' }, 'Grenze'), el('th', { scope: 'col', class: 'r' }, 'Größe'))),
        el('tbody', {}, list.map(c => el('tr', {},
          el('td', { title: c.name }, cName(c.name).t, c.laeuft ? null : el('span', { class: 'ls-mut' }, ' · gestoppt')),
          el('td', {}, c.begrenzt ? st('ok', 'mit Grenze') : st('idle', 'ohne Grenze')),
          el('td', { class: 'r nowrap' }, bytes(c.groesse))))))) : null);
  }
  function pfTop() {
    const list = (live() || []).filter(c => c.laeuft && c.speicher > 0).sort((a, b) => b.speicher - a.speicher).slice(0, 8);
    if (!list.length) return null;
    return sec('Container jetzt', el('div', { class: 'ls-tw' }, el('table', { class: 'ls-t' },
      el('caption', { class: 'sr' }, 'Container mit dem meisten Arbeitsspeicher'),
      el('thead', {}, el('tr', {}, el('th', { scope: 'col' }, 'Container'), el('th', { scope: 'col', class: 'r' }, 'Prozessor'), el('th', { scope: 'col', class: 'r' }, 'Arbeitsspeicher'))),
      el('tbody', {}, list.map(c => el('tr', {},
        el('td', {}, el('b', { title: c.name }, cName(c.name).t), c.verwaltet_von ? el('span', { class: 'ls-mut blk' }, 'gestartet vom Panel ' + panelName(c.verwaltet_von)) : c.projekt ? el('span', { class: 'ls-mut blk' }, 'Projekt ' + c.projekt) : null),
        el('td', { class: 'r nowrap' }, cpuText(c.cpu)),
        el('td', { class: 'r nowrap' }, bytes(c.speicher))))))), { cls: 'c5 flush', sub: 'der meiste Arbeitsspeicher zuerst; Prozessor als Anteil am Server', end: link('#/projekte', 'Alle Projekte', 'btn btn-sm btn-g') });
  }
  async function pfChange(x) {
    if (pf.busy) return;
    pf.busy = x.schluessel; pf.result = null;
    core.rerender();
    const r = await core.call('/api/leistung/vorschau', { schluessel: x.schluessel });
    pf.busy = '';
    if (!r.ok) { pf.result = { ok: false, text: errText(r, 'Die Vorschau') }; core.rerender(); return; }
    const p = r.data;
    const diff = p.vorher || p.nachher ? el('section', { class: 'op-part' }, el('h3', {}, p.datei || 'Änderung'),
      p.vorher ? el('p', { class: 'ls-mut' }, 'Vorher') : null, p.vorher ? el('pre', { class: 'pf-pre' }, p.vorher) : null,
      p.nachher ? el('p', { class: 'ls-mut' }, 'Nachher') : null, p.nachher ? el('pre', { class: 'pf-pre' }, p.nachher) : null) : null;
    const ok = await livePreview({ title: p.titel + '?', body: diff, problem: p.problem, steps: p.schritte, effect: p.auswirkung, back: p.rueckweg, notes: p.hinweise,
      ok: x.zuruecknehmen ? 'Zurücknehmen' : 'Übernehmen', icon: x.zuruecknehmen ? 'undo' : 'check' });
    if (!ok) { core.rerender(); return; }
    pf.busy = x.schluessel;
    core.rerender();
    const a = await core.call('/api/leistung/aendern', { schluessel: x.schluessel, stand: p.stand });
    pf.busy = '';
    if (a.ok) { pf.result = { ok: true, text: a.data.ergebnis }; core.announce(a.data.ergebnis); } else if (!(a.data && a.data.abgebrochen)) pf.result = { ok: false, text: errText(a, 'Die Änderung') };
    pf.at = 0; loadPf();
  }
  function pageLeistung() {
    if (!core.svc()) return [head({ i: 'leistung', k: 'Leistung', h: 'Gibt es nur im Dienst.', p: 'Speicher, Prozessor und Logs misst HostDeck, wenn es als Dienst auf dem Server läuft (sudo ./hostdeck einrichten).' })];
    if (!pf.loading && (!pf.at || Date.now() - pf.at > 30000)) setTimeout(loadPf, 0);
    if (!mon.loading && (!mon.at || Date.now() - mon.at > 10000)) setTimeout(loadMon, 0);
    if (!op.rep && !op.loading) setTimeout(loadOps, 0);
    const r = pf.rep;
    if (!r) return liveWait(pf, 'leistung', 'Leistung');
    const m = r.speicher;
    const memPct = m.gesamt ? pfUsed(r) / m.gesamt * 100 : 0;
    // Farben aus den Grenzen der Überwachung.
    const h = ['Arbeitsspeicher ', em(ramLevel(pfUsed(r), m.gesamt) || (limits() ? 'ok' : ''), pctText(memPct) + ' belegt'), ', Prozessor ', em(cpuLevel(pfCpu(r)) || (limits() ? 'ok' : ''), cpuText(pfCpu(r))), '.'];
    const top = pfTop();
    return [head({ i: 'leistung', k: 'Leistung · Stand ' + when(r.stand_von), h,
      p: 'Erst messen, dann ändern. Prozessor und Arbeitsspeicher kommen aus der Überwachung (wie auf der Übersicht), Steal und Last aus einer Messung beim Laden der Seite. Jeder Vorschlag sagt, was er bringt, was schiefgehen kann und wie du zurückkommst. Verläufe über Stunden und Tage und was die Messung selbst kostet, stehen unter Verlauf.',
      a: el('button', { type: 'button', class: 'btn btn-sm', 'data-fid': 'pf-ref', disabled: pf.loading, onclick: () => { pf.at = 0; mon.at = 0; loadPf(); loadMon(); } }, ic('refresh', 16), 'Aktualisieren') }),
    liveResult(pf), pf.error ? el('p', { class: 'band b-err', role: 'status' }, pf.error) : null,
    ...(r.hinweise || []).map(t => el('p', { class: 'band b-warn', role: 'status' }, t)),
    pfNow(r),
    el('div', { class: 'ls-grid eq' },
      sec('Vorschläge', pfSugs(r), { cls: top ? 'c7' : 'c12' }),
      top,
      sec('Container-Logs', pfLogs(r), { cls: 'c12 flush', sub: 'Docker kürzt sie von sich aus nie; HostDeck leert keine' }))];
  }

  // ---------- Leistungstest ----------
  // benchmark.sh als Lauf des Dienstes: starten, verfolgen, abbrechen;
  // darunter die Ergebnisse mit Übersicht, ZIP und Geekbench-Link.
  const lt = jobState('/api/leistungstest', 'lt', job => ltDone(job));
  Object.assign(lt, { gb: null, secs: 0, row: '', sum: {}, claim: {}, del: '', result: null });
  const LT_SECS = [[30, '30 s'], [60, '1 min'], [120, '2 min'], [300, '5 min']];
  const LT_STATE = { fertig: ['fertig', 't-ok'], unvollstaendig: ['unvollständig', 't-open'], abgebrochen: ['abgebrochen', 't-mut'], gescheitert: ['gescheitert', 't-err'] };
  const LT_TEXT = {
    name: 'Leistungstest',
    cancelled: 'Was bis dahin gemessen war, steht unten bei den Ergebnissen und liegt als ZIP bereit.',
    done: () => ['Leistungstest fertig.', 'Die Werte stehen unten bei den Ergebnissen, Rohdaten und Messübersicht in der ZIP.'],
    blocked: () => 'Unten steht, warum.',
    failedHead: () => 'Leistungstest nicht vollständig. ',
    failed: () => 'In der Ausgabe steht, was schiefging. Was gemessen wurde, steht unten bei den Ergebnissen.',
    stopAsk: 'Test abbrechen? Das Skript schreibt noch die Messübersicht und packt, was bis dahin gemessen ist, als ZIP.',
    stopping: 'Wird abgebrochen; das Skript schreibt noch die Messübersicht.',
  };
  const ltNum = v => v ? v.toLocaleString('de-DE', { minimumFractionDigits: 2, maximumFractionDigits: 2 }) : '–';
  function ltDone(job) {
    jr.stale = true;
    core.refreshLauf();
    core.announce(job.abgebrochen ? 'Leistungstest abgebrochen' : job.code === 0 ? 'Leistungstest fertig' : 'Leistungstest nicht vollständig');
  }
  function ltBadge() {
    const l = (core.svc() || {}).lauf;
    return lt.watching || (l && l.was === 'Leistungstest') ? cnt('läuft', 'acc') : null;
  }
  // Zeitfenster der Tests für die Diagramme (Unix-Sekunden), neueste zuerst;
  // der laufende bis jetzt.
  // Eigener, ruhiger Abruf ohne Ausgabe (ab=1e9): Die Diagramme laden
  // nicht alle paar Sekunden neu, während ein Test läuft.
  const ltm = { rep: null, at: 0, loading: false };
  async function loadLtMarks() {
    ltm.loading = true;
    const r = await core.call('/api/leistungstest?ab=1000000000', null, true);
    ltm.loading = false;
    ltm.at = Date.now();
    if (!r.ok) return;
    ltm.rep = r.data;
    if ((ui.page === 'ueberwachung' || ui.page === 'lage') && core.work() && !modalOpen() && !uw.hover) core.rerender();
  }
  function ltMarks() {
    if (!core.svc()) return [];
    if (!ltm.loading && Date.now() - Math.max(ltm.at, lt.at) > 60000) setTimeout(loadLtMarks, 0);
    const r = lt.at > ltm.at ? lt.rep : ltm.rep;
    if (!r) return [];
    const out = (r.tests || []).map(x => ({ a: Date.parse(x.start) / 1000, b: Date.parse(x.ende) / 1000 })).filter(m => m.a > 0 && m.b >= m.a);
    const j = r.auftrag;
    if (j && j.laeuft) out.unshift({ a: Date.parse(j.start) / 1000, b: Date.now() / 1000, run: true });
    return out;
  }
  // Grob: Sysbench zweimal die Dauer plus zwei Pausen à 10 s und viermal
  // 5 s Vor- und Nachlauf der Messung;
  // Geekbench braucht je nach Server einige Minuten.
  function ltMinutes(secs, gb) {
    const m = Math.max(1, Math.round((2 * secs + 40) / 60));
    return gb ? 'etwa ' + m + ' min plus Geekbench (meist 3 bis 6 min)' : 'etwa ' + m + ' min';
  }
  // Die Steal-Werte der Phase mit allen Threads, sonst der letzten mit Daten.
  function ltSteal(x) {
    const ph = (x.steal || []).filter(p => p.sekunden > 0);
    return ph.find(p => /Threads$/.test(p.phase)) || ph[ph.length - 1] || null;
  }
  function ltStart(info) {
    const gb = lt.gb, secs = lt.secs;
    const miss = info.pakete_fehlen || [];
    return livePreview({
      title: 'Leistungstest starten?',
      steps: [
        miss.length ? 'Fehlende Pakete mit apt-get installieren: ' + miss.join(', ') + '.' : null,
        gb ? 'Geekbench ' + info.geekbench_version + ' laufen lassen' + (info.geekbench_da ? '.' : '; das Skript lädt es vorher von Primate Labs herunter (gut 230 MB, entpackt rund 480 MB) und prüft die Dateinamen im Archiv.') : null,
        'Sysbench mit 1 Thread, ' + secs + ' s.',
        'Sysbench mit allen ' + info.threads + ' Threads, ' + secs + ' s; dabei misst mpstat die Steal-Time und vmstat den Rest.',
        'Messübersicht schreiben und alles als ZIP packen.',
      ].filter(Boolean),
      effect: 'Der Prozessor läuft ' + ltMinutes(secs, gb) + ' unter Volllast. Deine Projekte laufen weiter, nur langsamer. Die Warnungen zu Prozessor und Steal ruhen so lange; alles andere überwacht HostDeck weiter.',
      back: 'Abbrechen geht jederzeit; was bis dahin gemessen ist, bleibt als ZIP. Installierte Pakete bleiben auf dem Server.',
      notes: gb ? ['Geekbench lädt das Ergebnis öffentlich auf browser.geekbench.com hoch. Wer den Link kennt, sieht Prozessor, Speicher und Werte des Servers, aber nicht seine Adresse.'] : [],
      ok: 'Test starten', icon: 'play',
    });
  }
  async function ltGo(info) {
    if (!await ltStart(info)) { core.rerender(); return; }
    lt.result = null;
    await startJob(lt, false, { geekbench: lt.gb, sekunden: lt.secs }, 'Leistungstest');
  }
  function ltForm(r) {
    const info = r.vorab;
    // Geekbench lädt öffentlich hoch und läuft deshalb nur, wenn man es
    // ausdrücklich einschaltet.
    if (lt.gb === null) lt.gb = false;
    if (!lt.secs) lt.secs = info.sekunden || 120;
    if (!info.geekbench_moeglich) lt.gb = false;
    const sv = core.svc() || {};
    const busy = sv.lauf && !lt.watching ? 'Geht erst, wenn „' + sv.lauf.was + '“ fertig ist.' : '';
    const miss = info.pakete_fehlen || [];
    const noApt = info.ohne_apt && miss.length;
    const gbBox = el('label', { class: 'cl-it lt-opt' + (lt.gb ? ' is-on' : '') + (info.geekbench_moeglich ? '' : ' is-off') },
      el('input', { type: 'checkbox', checked: lt.gb, disabled: !info.geekbench_moeglich || lt.watching, 'data-fid': 'lt-gb', 'aria-describedby': 'lt-gb-d',
        onchange: e => { lt.gb = e.target.checked; core.rerender(); } }),
      el('span', { class: 'cl-t' }, el('b', {}, 'Geekbench ' + info.geekbench_version + ' dazu'),
        el('span', { class: 'ls-mut blk', id: 'lt-gb-d' }, info.geekbench_moeglich
          ? 'Vergleichbar mit anderen Servern. Lädt das Ergebnis öffentlich auf browser.geekbench.com hoch.' + (info.geekbench_da ? '' : ' Beim ersten Mal lädt das Skript Geekbench herunter (gut 230 MB, entpackt rund 480 MB).')
          : info.geekbench_grund || 'Geht auf diesem Server nicht.')),
      el('span'));
    const seg = el('div', { class: 'ls-seg', role: 'group', 'aria-labelledby': 'lt-dur' }, LT_SECS.map(([s, t]) =>
      el('button', { type: 'button', 'data-fid': 'lt-s-' + s, 'aria-pressed': String(lt.secs === s), disabled: lt.watching, onclick: () => { lt.secs = s; core.rerender(); } }, t)));
    const gbUse = info.geekbench_belegt > 0 ? el('div', { class: 'lt-gbuse' },
      el('span', { class: 'ls-mut' }, 'Geekbench liegt auf dem Server und belegt ' + size(info.geekbench_belegt) + '. Der nächste Test mit Geekbench lädt es sonst neu.'),
      el('button', { type: 'button', class: 'btn btn-sm btn-g', 'data-fid': 'lt-gb-rm', disabled: lt.watching || lt.del === 'gb', onclick: () => ltRemoveGb(info) }, ic('trash', 16), lt.del === 'gb' ? 'Entfernt …' : 'Entfernen …')) : null;
    return el('div', { class: 'ls-stack sm' },
      gbBox, gbUse,
      el('div', { class: 'lt-dur' }, el('span', { id: 'lt-dur' }, el('b', {}, 'Sysbench je Phase')), seg),
      miss.length ? el('p', { class: 'ls-note' + (noApt ? ' op-warn' : '') }, ic(noApt ? 'warn' : 'info', 16), el('span', {}, noApt
        ? 'Es fehlen ' + miss.join(', ') + ', und apt-get gibt es hier nicht. Installiere sie selbst, dann geht der Test.'
        : 'Fehlt noch: ' + miss.join(', ') + '. Das Skript installiert es mit apt-get und lässt es danach drauf.')) : null,
      lt.error ? el('p', { class: 'gt-err', role: 'alert' }, ic('warn', 16), el('span', {}, lt.error)) : null,
      el('div', { class: 'ls-row-end' },
        el('span', { class: 'ls-mut' }, busy || (lt.watching ? 'Ein Test läuft gerade.' : 'Dauer ' + ltMinutes(lt.secs, lt.gb) + '.')),
        el('button', { type: 'button', class: 'btn btn-p', 'data-fid': 'lt-run', disabled: !!busy || lt.watching || !!lt.busy || !!noApt, onclick: () => ltGo(info) },
          ic('play', 18), lt.busy ? 'Startet …' : 'Test starten …')));
  }
  // Phase und Restzeit aus den Zeitpunkt-Zeilen des Skripts
  // („2026-…+00:00 | START Sysbench Single-Core, threads=1, time=30s, …“).
  function ltPhase() {
    let ph = '', single = null;
    for (const l of lt.lines) {
      const m = /^(\S+) \| (START|Pause vor) (Geekbench|Sysbench [A-Za-z-]+|Gesamtmessung)(?:, threads=\d+, time=(\d+)s)?/.exec(l);
      if (!m || m[3] === 'Gesamtmessung') continue;
      ph = m[3].replace('Single-Core', 'mit 1 Thread').replace('Multi-Core', 'mit allen Threads');
      if (m[2] === 'START' && m[3] === 'Sysbench Single-Core' && m[4]) single = { at: Date.parse(m[1]), secs: Number(m[4]) };
    }
    if (!ph) return 'bereitet vor (Pakete, Download)';
    if (/^Geekbench/.test(ph)) return 'gerade: Geekbench, meist 3 bis 6 min';
    if (!single || !(single.at > 0)) return 'gerade: ' + ph;
    const left = Math.round((single.at + (2 * single.secs + 25) * 1000 - Date.now()) / 60000);
    return 'gerade: ' + ph + (left >= 1 ? ', noch etwa ' + left + ' min' : ', gleich fertig');
  }
  async function ltRemoveGb(info) {
    const ok = await clDialog(done => [
      el('h2', { id: 'cl-h' }, 'Geekbench vom Server entfernen?'),
      el('p', { id: 'cl-d', class: 'gt-lead' }, 'HostDeck löscht das heruntergeladene Geekbench ' + info.geekbench_version + ' samt Archiv (' + size(info.geekbench_belegt) + '). Deine Ergebnisse bleiben. Der nächste Test mit Geekbench lädt es neu herunter.'),
      el('div', { class: 'gt-acts' }, el('button', { type: 'button', class: 'btn', 'data-autofocus': '', onclick: () => done(false) }, 'Abbrechen'),
        el('button', { type: 'button', class: 'btn btn-err', onclick: () => done(true) }, ic('trash', 16), 'Entfernen'))]);
    if (!ok) { core.rerender(); return; }
    lt.del = 'gb';
    core.rerender();
    const r = await core.call('/api/leistungstest/geekbench-entfernen', {});
    lt.del = '';
    if (r.ok) { lt.rep = Object.assign({}, lt.rep, { vorab: r.data.vorab }); lt.result = { ok: true, text: 'Geekbench entfernt, ' + size(info.geekbench_belegt) + ' frei.' }; core.announce(lt.result.text); }
    else if (!(r.data && r.data.abgebrochen)) lt.result = { ok: false, text: errText(r, 'Entfernen') };
    core.rerender();
  }
  async function ltToggle(x) {
    lt.row = lt.row === x.id ? '' : x.id;
    core.rerender();
    if (!lt.row || lt.sum[x.id]) return;
    const r = await core.call('/api/leistungstest/bericht?id=' + encodeURIComponent(x.id), null, true);
    lt.sum[x.id] = r.ok ? r.data : { fehler: errText(r, 'Laden') };
    core.rerender();
  }
  async function ltClaim(x) {
    const r = await core.call('/api/leistungstest/claim?id=' + encodeURIComponent(x.id), null, true);
    lt.claim[x.id] = r.ok ? { link: r.data.link } : { fehler: errText(r, 'Laden') };
    core.rerender();
  }
  async function ltDelete(x) {
    const ok = await clDialog(done => [
      el('h2', { id: 'cl-h' }, 'Test vom ' + when(x.start) + ' löschen?'),
      el('p', { id: 'cl-d', class: 'gt-lead' }, 'HostDeck löscht Ergebnis, Messübersicht und ZIP (' + (x.zip ? size(x.zip) : 'keine ZIP') + ') vom Server. ' + (x.geekbench_link ? 'Das Ergebnis bei Geekbench bleibt online; entfernen kannst du es dort nur mit dem Claim-Link in deinem Konto.' : 'Das lässt sich nicht zurückholen.')),
      el('div', { class: 'gt-acts' }, el('button', { type: 'button', class: 'btn', 'data-autofocus': '', onclick: () => done(false) }, 'Abbrechen'),
        el('button', { type: 'button', class: 'btn btn-err', onclick: () => done(true) }, ic('trash', 16), 'Löschen'))]);
    if (!ok) { core.rerender(); return; }
    lt.del = x.id;
    core.rerender();
    const r = await core.call('/api/leistungstest/loeschen', { id: x.id });
    lt.del = '';
    if (r.ok) { lt.rep = Object.assign({}, lt.rep, { tests: r.data.tests }); lt.result = { ok: true, text: 'Test vom ' + when(x.start) + ' gelöscht.' }; if (lt.row === x.id) lt.row = ''; core.announce(lt.result.text); }
    else if (!(r.data && r.data.abgebrochen)) lt.result = { ok: false, text: errText(r, 'Löschen') };
    core.rerender();
  }
  // Im Verlauf der Überwachung: das Testfenster als Zoom im kleinsten
  // Zeitraum, der es enthält. Ältere Tests sind dort zu grob aufgelöst.
  function ltWindow(x) {
    const a = Date.parse(x.start) / 1000, b = Date.parse(x.ende) / 1000, now = Date.now() / 1000;
    if (!(a > 0) || !(b >= a) || now - a > 86400) return null;
    return () => {
      const rg = RANGES.find(([, , s]) => s >= now - a + 120) || RANGES[3];
      const pad = Math.max(60, rg[2] / 360 * 3);
      uw.range = rg[0];
      uw.zoom = [Math.floor(a - pad), Math.ceil(Math.min(now, b + pad))];
      uw.at = 0;
      location.hash = '#/ueberwachung';
    };
  }
  function ltDetail(x) {
    const s = lt.sum[x.id];
    const c = lt.claim[x.id];
    const win = ltWindow(x);
    const acts = el('div', { class: 'acts lt-acts' },
      x.zip ? el('a', { class: 'btn btn-sm', href: '/api/leistungstest/zip?id=' + encodeURIComponent(x.id), download: '', 'data-fid': 'lt-zip-' + x.id }, ic('download', 16), 'ZIP (' + size(x.zip) + ')') : null,
      x.geekbench_link ? el('a', { class: 'btn btn-sm', href: x.geekbench_link, target: '_blank', rel: 'noopener noreferrer' }, ic('right', 16), 'Bei Geekbench ansehen') : null,
      x.claim_link && !c ? el('button', { type: 'button', class: 'btn btn-sm', 'data-fid': 'lt-claim-' + x.id, onclick: () => ltClaim(x) }, ic('lock', 16), 'Claim-Link zeigen') : null,
      win ? el('button', { type: 'button', class: 'btn btn-sm', 'data-fid': 'lt-uw-' + x.id, onclick: win }, ic('puls', 16), 'Im Verlauf ansehen') : null,
      el('button', { type: 'button', class: 'btn btn-sm btn-g', 'data-fid': 'lt-del-' + x.id, disabled: lt.watching || lt.del === x.id, onclick: () => ltDelete(x) }, ic('trash', 16), lt.del === x.id ? 'Löscht …' : 'Löschen …'));
    const claim = c ? (c.link
      ? el('p', { class: 'ls-note' }, ic('lock', 16), el('span', {}, 'Privat: Mit diesem Link ordnest du das Ergebnis deinem Geekbench-Konto zu und kannst es dort löschen. ', el('a', { href: c.link, target: '_blank', rel: 'noopener noreferrer', class: 'mono lt-link' }, c.link)))
      : el('p', { class: 'gt-err', role: 'alert' }, ic('warn', 16), el('span', {}, c.fehler))) : null;
    const steal = (x.steal || []).filter(p => p.sekunden > 0);
    const stealT = steal.length ? el('div', { class: 'ls-tw' }, el('table', { class: 'ls-t lt-st' }, el('caption', { class: 'sr' }, 'Steal-Time je Phase'),
      el('thead', {}, el('tr', {}, ['Phase', 'Sekunden', 'Steal Ø', 'Spitze', 'Spitze 1 vCPU', '≥ 10 % / ≥ 20 %'].map((t, i) => el('th', { scope: 'col', class: i ? 'r' : null }, t)))),
      el('tbody', {}, steal.map(p => el('tr', {}, el('td', {}, p.phase), el('td', { class: 'r' }, String(p.sekunden)), el('td', { class: 'r' }, pctText(p.mittel, 1)),
        el('td', { class: 'r' }, pctText(p.spitze, 1)), el('td', { class: 'r' }, pctText(p.spitze_vcpu, 1)), el('td', { class: 'r' }, p.ueber10 + ' / ' + p.ueber20)))))) : null;
    const tail = x.ausgabe && x.ausgabe.length && x.stand !== 'fertig' ? el('details', { class: 'bk-out', open: !x.sysbench_1 },
      el('summary', {}, 'Letzte Zeilen der Ausgabe'), el('pre', { class: 'bk-log', tabindex: '0', 'aria-label': 'Letzte Zeilen der Ausgabe' }, x.ausgabe.join('\n'))) : null;
    const sum = !s ? el('span', { class: 'skel skel-t skel-w' })
      : s.fehler ? el('p', { class: 'gt-err', role: 'alert' }, ic('warn', 16), el('span', {}, s.fehler))
        : s.uebersicht ? el('details', { class: 'bk-out' }, el('summary', {}, 'Messübersicht des Skripts'), el('pre', { class: 'bk-log', tabindex: '0', 'aria-label': 'Messübersicht' }, s.uebersicht)) : null;
    return el('div', { class: 'ls-stack sm lt-det', id: 'lt-det-' + x.id },
      el('p', { class: 'ls-mut' }, [x.server, x.prozessor, x.vcpus ? x.vcpus + ' vCPU' : '', x.geekbench ? 'mit Geekbench' : 'ohne Geekbench', 'Sysbench je ' + x.sekunden + ' s', x.von_hostdeck_gepackt ? 'ZIP von HostDeck gepackt' : ''].filter(Boolean).join(' · ')),
      acts, claim, stealT, tail, sum);
  }
  function ltTable(r) {
    const list = r.tests || [];
    if (!list.length) return el('p', { class: 'ls-mut lt-empty' }, 'Noch kein Test. Der erste zeigt, was dieser Server leistet; spätere vergleichst du hier.');
    const best = Math.max(...list.map(x => x.sysbench_n || 0));
    const rows = [];
    for (const x of list) {
      const st0 = LT_STATE[x.stand] || [x.stand, 't-mut'];
      const sl = ltSteal(x);
      const open = lt.row === x.id;
      const bar = el('span', { class: 'ls-bar', 'aria-hidden': 'true' }, el('i'));
      bar.firstChild.style.width = (best && x.sysbench_n ? x.sysbench_n / best * 100 : 0).toFixed(1) + '%';
      rows.push(el('tr', { class: open ? 'is-open' : null },
        el('td', {}, el('b', {}, when(x.start)), el('span', { class: 'sub' }, [x.server, x.vcpus ? x.vcpus + ' vCPU' : ''].filter(Boolean).join(' · ')),
          x.sysbench_n ? el('span', { class: 'sub lt-mob' }, ltNum(x.sysbench_n) + ' Events/s mit ' + plural(x.threads || 0, 'Thread', 'Threads') + (sl ? ' · Steal Ø ' + pctText(sl.mittel, 1) : '')) : null),
        el('td', {}, el('span', { class: 'tag ' + st0[1] }, st0[0])),
        el('td', { class: 'r lt-wide' }, ltNum(x.sysbench_1)),
        el('td', { class: 'r lt-n lt-wide' }, ltNum(x.sysbench_n), x.threads ? el('span', { class: 'sub' }, x.threads + ' Threads') : null, x.sysbench_n ? bar : null),
        el('td', { class: 'r lt-wide' }, x.geekbench_1 ? [String(x.geekbench_1), el('span', { class: 'ls-mut' }, ' / '), String(x.geekbench_n || '–')]
          : x.geekbench_link ? el('a', { href: x.geekbench_link, target: '_blank', rel: 'noopener noreferrer', title: 'Geekbench gibt in der freien Version keine Punkte aus; sie stehen auf der Ergebnisseite.' }, 'Ergebnis ↗')
            : el('span', { class: 'ls-mut' }, x.geekbench ? '–' : 'aus')),
        el('td', { class: 'r lt-wide' }, sl ? [pctText(sl.mittel, 1), el('span', { class: 'sub' }, 'Spitze ' + pctText(sl.spitze, 1))] : el('span', { class: 'ls-mut' }, '–')),
        el('td', { class: 'r' }, el('button', { type: 'button', class: 'btn btn-sm btn-g', 'aria-expanded': String(open), 'aria-controls': open ? 'lt-det-' + x.id : null, 'data-fid': 'lt-open-' + x.id, onclick: () => ltToggle(x) },
          ic('down', 16), 'Details'))));
      if (open) rows.push(el('tr', { class: 'lt-row-det' }, el('td', { colspan: '7' }, ltDetail(x))));
    }
    return el('div', { class: 'ls-tw' }, el('table', { class: 'ls-t lt-t' }, el('caption', { class: 'sr' }, 'Ergebnisse der Leistungstests'),
      el('thead', {}, el('tr', {}, el('th', { scope: 'col' }, 'Gemessen'), el('th', { scope: 'col' }, 'Stand'), el('th', { scope: 'col', class: 'r lt-wide' }, 'Sysbench 1 Thread'),
        el('th', { scope: 'col', class: 'r lt-wide' }, 'Alle Threads'), el('th', { scope: 'col', class: 'r lt-wide' }, 'Geekbench'), el('th', { scope: 'col', class: 'r lt-wide' }, 'Steal Ø'), el('th', { scope: 'col', class: 'r' }, el('span', { class: 'sr' }, 'Mehr')))),
      el('tbody', {}, rows)));
  }
  function pageLeistungstest() {
    const help = 'Dein benchmark.sh, unverändert bis auf einen Schalter für Geekbench: Sysbench mit einem und mit allen Threads, auf Wunsch Geekbench 6, dazu Steal-Time (mpstat) und vmstat. Die Werte sind Events pro Sekunde; mehr ist besser. Ergebnisse und ZIP bleiben auf dem Server, bis du sie löschst, und ziehen beim Umzug mit. Im Terminal: sudo hostdeck leistungstest.';
    if (!core.svc()) return [head({ i: 'leistung', k: 'Leistungstest', h: 'Gibt es nur im Dienst.', p: 'Den Leistungstest startet HostDeck, wenn es als Dienst auf dem Server läuft (sudo ./hostdeck einrichten). Im Terminal geht er mit sudo hostdeck leistungstest.' })];
    if (!lt.loading && !lt.watching && (!lt.at || Date.now() - lt.at > 30000)) setTimeout(() => loadJob(lt), 0);
    const r = lt.rep;
    if (!r) return [head({ i: 'leistung', k: 'Leistungstest', h: lt.error ? ['Der Stand ist ', em('err', 'nicht erreichbar'), '.'] : 'HostDeck lädt …', p: lt.error || help }),
      sec(null, el('div', { class: 'ls-stack' }, el('span', { class: 'skel skel-h' }), el('span', { class: 'skel skel-t skel-w' })))];
    const job = r.auftrag;
    const list = r.tests || [];
    const last = list.find(x => x.sysbench_n);
    const h = lt.watching ? ['Ein Test ', em('warn', 'läuft'), '. Der Prozessor ist gerade voll belegt.']
      : last ? ['Zuletzt ', em('ok', ltNum(last.sysbench_n) + ' Events/s'), ' mit ' + plural(last.threads, 'Thread', 'Threads'),
        ltSteal(last) ? ', Steal Ø ' + pctText(ltSteal(last).mittel, 1) : '', ', ' + when(last.start),
        last.geekbench_link ? [', ', el('a', { href: last.geekbench_link, target: '_blank', rel: 'noopener noreferrer' }, 'Geekbench-Ergebnis ↗')] : '', '.']
        : 'Noch kein Test auf diesem Server.';
    return [head({ i: 'leistung', k: 'Leistungstest' + (list.length ? ' · ' + plural(list.length, 'Ergebnis', 'Ergebnisse') : ''), h, p: help }),
      r.fehler ? el('p', { class: 'band b-err', role: 'status' }, r.fehler) : null,
      liveResult(lt),
      el('div', { class: 'ls-grid eq' },
        sec('Neuer Test', ltForm(r), { cls: job ? 'c5' : 'c12' }),
        job ? sec(job.laeuft ? 'Läuft' : 'Letzter Lauf', jobView(job, lt, LT_TEXT), { cls: 'c7', sub: job.laeuft ? ltPhase() : null }) : null,
        sec('Ergebnisse', ltTable(r), { cls: 'c12 flush', sub: 'neueste zuerst; der Balken vergleicht alle Threads' }))];
  }

  // ---------- Tuning ----------
  // tune.sh (vps-tune) über den Dienst: Ist-Zustand, Preset mit Vorschau,
  // Anwenden, CPU-Vorrang je Dienst, Zurücknehmen und Neustart.
  const tn = jobState('/api/tuning', 'tn', job => tnDone(job));
  Object.assign(tn, { preset: '', set: {}, init: false, adv: false, svc: null, svcLoading: false, svcErr: '', svcOpen: false, prio: '', rb: false, result: null });
  const TN_LEVEL = [['low', 'niedrig'], ['normal', 'normal'], ['high', 'hoch']];
  function tnText(job) {
    const back = job && job.art === 'zuruecknehmen';
    return {
      name: back ? 'Zurücknehmen' : 'Tuning',
      noStop: 'Läuft bis zum Ende, meist nur Sekunden; ein halb angewendetes Tuning wäre schlechter. Zurücknehmen geht danach jederzeit.',
      cancelled: '',
      done: () => {
        const rb = tn.rep && tn.rep.stand && tn.rep.stand.neustart_noetig;
        return back
          ? ['Zurückgenommen.', 'Alles steht wieder wie vor vps-tune.' + (rb ? ' Bootparameter fallen erst mit dem nächsten Neustart weg.' : '')]
          : ['Tuning angewendet.', rb ? 'Werte zur Laufzeit wirken sofort, Bootparameter nach einem Neustart.' : 'Die Werte wirken sofort.'];
      },
      blocked: () => 'In der Ausgabe steht, warum.',
      failedHead: () => back ? 'Zurücknehmen unvollständig. ' : 'Tuning nicht vollständig angewendet. ',
      failed: () => 'Die Meldungen von tune.sh stehen in der Ausgabe. Vor einem Neustart prüfen.',
    };
  }
  function tnDone(job) {
    tn.svc = null;
    core.refreshLauf();
    core.announce(job.code === 0 ? (job.art === 'zuruecknehmen' ? 'Tuning zurückgenommen' : 'Tuning angewendet') : 'Tuning mit Fehlern beendet');
  }
  function tnBadge() {
    const l = (core.svc() || {}).lauf;
    if (tn.watching || (l && l.was === 'Tuning')) return cnt('läuft', 'acc');
    const st = tn.rep && tn.rep.stand;
    return st && st.neustart_noetig ? cnt('1', 'warn') : null; // Neustart nötig
  }
  const tnPreset = (r, name) => r.presets.find(p => p.name === name) || r.presets[0];
  // Beim ersten Laden steht im Formular, was gerade angewendet ist.
  function tnInit(r) {
    if (tn.init) return;
    tn.init = true;
    const ap = r.stand && r.stand.angewendet;
    const of = r.alter_server && r.alter_server.angebot;
    tn.preset = ap && r.presets.some(p => p.name === ap.preset) ? ap.preset : 'mixed';
    tn.set = {};
    // Nach dem Umzug steht ohne eigenes Tuning das vom alten Server drin.
    if (!ap && of) { tn.preset = of.preset; tn.set = Object.assign({}, of.schalter || {}); return; }
    if (!ap) return;
    const pr = tnPreset(r, tn.preset);
    for (const sw of r.schalter) {
      const v = ap.werte[sw.schluessel];
      if (v && v !== pr.werte[sw.schluessel] && !(sw.schluessel === 'iface' && v === 'auto')) tn.set[sw.schluessel] = v;
    }
  }
  function tnSet() {
    const o = {};
    for (const [k, v] of Object.entries(tn.set)) if (v) o[k] = v;
    return o;
  }
  function tnBusy() {
    const sv = core.svc() || {};
    if (tn.watching) return 'Das Tuning arbeitet gerade.';
    return sv.lauf ? 'Geht erst, wenn „' + sv.lauf.was + '“ fertig ist.' : '';
  }
  function tnStatus(r) {
    const st = r.stand;
    if (!st) return el('p', { class: 'gt-err', role: 'alert' }, ic('warn', 16), el('span', {}, r.fehler || 'Kein Stand.'));
    const kv = (k, v) => el('div', { class: 'ls-kv' }, el('span', {}, k), el('span', {}, v));
    const ap = st.angewendet;
    const pr = ap && r.presets.find(p => p.name === ap.preset);
    const boot = st.bootparameter.length
      ? el('ul', { class: 'tn-boot' }, st.bootparameter.map(b => el('li', {}, el('span', { class: 'mono' }, b.parameter),
        el('span', { class: 'tag ' + (b.aktiv ? 't-ok' : 't-open') }, b.aktiv ? 'aktiv' : 'nach Neustart'))))
      : el('p', { class: 'ls-mut' }, 'Keine Bootparameter von vps-tune.');
    return el('div', { class: 'ls-stack sm' },
      el('div', { class: 'ls-kvs flat' },
        kv('Angewendet', [ap ? (pr ? pr.titel + ' (' + ap.preset + ')' : ap.preset) + ', ' + when(ap.seit) : 'nichts',
          r.alter_server && r.alter_server.gleich ? el('span', { class: 'tag t-sug' }, 'wie auf dem alten Server') : null]),
        st.kopf.filter(x => x.name !== 'Angewendet').map(x => kv(x.name, x.wert))),
      el('p', { class: 'sc-g' }, 'Bootparameter'), boot,
      ...(st.boot_hinweise || []).map(h => el('p', { class: 'ls-note' }, ic('info', 16), el('span', {}, h))),
      el('p', { class: 'sc-g' }, 'Wirksamkeit'),
      el('div', { class: 'ls-kvs flat tn-kvs' }, st.wirksamkeit.map(x => kv(x.name, x.wert))),
      ...(st.hinweise || []).map(h => el('p', { class: 'ls-mut' }, h)),
      st.fehler ? el('p', { class: 'gt-err', role: 'alert' }, ic('warn', 16), el('span', {}, st.fehler)) : null,
      el('details', { class: 'bk-out' }, el('summary', {}, 'Ausgabe von tune.sh status'),
        el('pre', { class: 'bk-log', tabindex: '0', 'aria-label': 'Ausgabe von tune.sh status' }, st.ausgabe.join('\n'))));
  }
  function tnSwitch(r, pr, sw) {
    const k = sw.schluessel, id = 'tn-sw-' + k;
    const ctl = sw.werte
      ? el('select', { id, 'data-fid': id, disabled: tn.watching, onchange: e => { tn.set[k] = e.target.value; core.rerender(); } },
        el('option', { value: '', selected: !tn.set[k] }, 'wie Preset: ' + pr.werte[k]),
        sw.werte.map(v => el('option', { value: v, selected: tn.set[k] === v }, v)))
      : el('input', { id, 'data-fid': id, type: 'text', value: tn.set[k] || '', placeholder: 'automatisch', autocomplete: 'off', spellcheck: 'false', maxlength: '15', disabled: tn.watching,
        onchange: e => { tn.set[k] = e.target.value.trim(); core.rerender(); } });
    return el('div', { class: 'tn-sw' + (tn.set[k] ? ' is-set' : '') },
      el('label', { for: id }, el('b', {}, sw.name, el('span', { class: 'ls-mut mono' }, ' --' + k)), sw.boot ? el('span', { class: 'tag t-mut' }, 'Bootparameter') : null,
        el('span', { class: 'ls-mut blk' }, sw.text)),
      el('span', { class: 'pv-sel tn-ctl' }, ctl));
  }
  function tnForm(r) {
    const pr = tnPreset(r, tn.preset);
    const busy = tnBusy();
    const set = tnSet();
    const n = Object.keys(set).length;
    const seg = el('div', { class: 'ls-seg', role: 'group', 'aria-label': 'Preset' }, r.presets.map(p =>
      el('button', { type: 'button', 'data-fid': 'tn-pr-' + p.name, 'aria-pressed': String(tn.preset === p.name), disabled: tn.watching, onclick: () => { tn.preset = p.name; core.rerender(); } }, p.titel)));
    const adv = el('details', { class: 'bk-out tn-adv', open: tn.adv, ontoggle: e => { tn.adv = e.target.open; } },
      el('summary', {}, 'Erweitert: alle Schalter' + (n ? ' (' + n + ' geändert)' : '')),
      el('div', { class: 'tn-sws' }, r.schalter.map(sw => tnSwitch(r, pr, sw))),
      n ? el('button', { type: 'button', class: 'btn btn-sm btn-g', 'data-fid': 'tn-sw-reset', onclick: () => { tn.set = {}; core.rerender(); } }, ic('undo', 16), 'Alle wie im Preset') : null);
    return el('div', { class: 'ls-stack sm' },
      seg,
      el('p', { class: 'tn-pt' }, el('b', {}, pr.titel + ' (' + pr.name + ')' + (pr.empfohlen ? ', im Skript empfohlen' : '') + ': '), pr.text),
      n ? el('p', { class: 'ls-note' }, ic('edit', 16), el('span', {}, 'Abweichend vom Preset: ' + Object.entries(set).map(([k, v]) => '--' + k + ' ' + v).join(', '))) : null,
      adv,
      el('div', { class: 'ls-row-end' },
        el('span', { class: 'ls-mut' }, busy || 'Erst kommt die Vorschau; noch ändert sich nichts.'),
        el('button', { type: 'button', class: 'btn btn-p', 'data-fid': 'tn-plan', disabled: !!busy || !!tn.busy, onclick: () => tnApply(r) },
          ic('eye', 18), tn.busy === 'plan' ? 'Vorschau …' : 'Vorschau ansehen …')));
  }
  // Vorschau von tune.sh als Dialog: [PLAN]-Zeilen, was schon so ist,
  // Warnungen, Warntext mit „Ich habe verstanden“.
  function tnPlanDialog(p, o) {
    return clDialog(done => {
      const plan = [], same = [], warn = [];
      let last = null;
      for (const l of p.zeilen) {
        if (l.art === 'plan') { last = { t: l.text, d: [] }; plan.push(last); }
        else if (l.art === 'detail') { if (last) last.d.push(l.text); }
        else if (l.art === 'warn' || l.art === 'fehler' || l.art === 'hinweis') warn.push(l.text);
        else if (l.art === 'info' && !/^Nur Vorschau/.test(l.text)) same.push(l.text);
      }
      const problem = p.problem || (o.empty && !plan.length ? o.empty : '');
      const risky = !problem && p.riskant && p.riskant.length;
      let go = null;
      const ack = risky ? el('label', { class: 'cl-it lt-opt tn-ack' },
        el('input', { type: 'checkbox', 'data-fid': 'tn-ok', 'data-autofocus': '', onchange: e => { go.disabled = !e.target.checked; } }),
        el('span', { class: 'cl-t' }, el('b', {}, 'Ich habe verstanden'),
          el('span', { class: 'ls-mut blk' }, 'Das ersetzt die Abfrage im Skript; HostDeck ruft tune.sh dann mit --yes auf.')), el('span')) : null;
      go = problem ? null : el('button', { type: 'button', class: 'btn btn-p', 'data-fid': 'tn-go', disabled: !!risky, onclick: () => done(true) }, ic(o.icon, 16), o.ok);
      return [
        el('p', { class: 'ls-eyb' }, ic('eye', 16), el('span', {}, 'Vorschau von tune.sh · noch ist nichts passiert')),
        el('h2', { id: 'cl-h' }, o.title),
        problem ? el('p', { id: 'cl-d', class: 'gt-lead' }, problem)
          : el('section', { class: 'op-part', id: 'cl-d' }, el('h3', {}, plan.length ? 'Was sich ändert (' + plan.length + ')' : 'Was sich ändert'),
            plan.length ? el('ul', { class: 'op-list tn-pl' }, plan.map(x => el('li', {}, x.t, x.d.length ? el('pre', { class: 'pf-pre' }, x.d.join('\n')) : null)))
              : el('p', { class: 'ls-mut' }, 'Nichts; alles steht schon so.')),
        same.length && !p.problem ? el('details', { class: 'bk-out' }, el('summary', {}, 'Steht schon so (' + same.length + ')'),
          el('ul', { class: 'op-list tn-pl' }, same.map(x => el('li', {}, x)))) : null,
        ...warn.map(t => el('p', { class: 'ls-note op-warn' }, ic('warn', 16), el('span', {}, t))),
        !problem && p.neustart && o.reboot ? el('section', { class: 'op-part' }, el('h3', {}, 'Danach'), el('p', {}, o.reboot)) : null,
        !problem && o.back ? el('section', { class: 'op-part' }, el('h3', {}, 'Rückweg'), el('p', {}, o.back)) : null,
        risky ? el('div', { class: 'band b-err tn-warn', role: 'alert' }, el('pre', {}, p.warntext)) : null,
        ack,
        el('div', { class: 'gt-acts' }, el('button', { type: 'button', class: 'btn', onclick: () => done(false) }, problem ? 'Schließen' : 'Abbrechen'), go)];
    });
  }
  async function tnApply(r) {
    if (tn.busy) return;
    const pr = tnPreset(r, tn.preset);
    const body = { preset: pr.name, schalter: tnSet() };
    tn.busy = 'plan'; tn.result = null;
    core.rerender();
    const a = await core.call('/api/tuning/vorschau', body);
    tn.busy = '';
    if (!a.ok) { if (!(a.data && a.data.abgebrochen)) tn.result = { ok: false, text: errText(a, 'Die Vorschau') }; core.rerender(); return; }
    const p = a.data;
    const ok = await tnPlanDialog(p, {
      title: 'Tuning „' + pr.titel + '“ anwenden?', ok: 'Anwenden', icon: 'check',
      reboot: 'Werte zur Laufzeit wirken sofort. Die Bootparameter (' + (p.bootparameter || []).join(' ') + ') wirken erst nach einem Neustart; HostDeck bietet ihn dann hier an.',
      back: 'Vor jeder Änderung sichert das Skript den Originalwert. „Zurücknehmen“ stellt alles wieder her und entfernt die Dateien von vps-tune.',
    });
    if (!ok) { core.rerender(); return; }
    await startJob(tn, false, Object.assign(body, { verstanden: !!(p.riskant && p.riskant.length) }), 'Tuning', 'anwenden');
  }
  async function tnRestore() {
    if (tn.busy) return;
    tn.busy = 'restore'; tn.result = null;
    core.rerender();
    const a = await core.call('/api/tuning/zuruecknehmen/vorschau', {});
    tn.busy = '';
    if (!a.ok) { if (!(a.data && a.data.abgebrochen)) tn.result = { ok: false, text: errText(a, 'Die Vorschau') }; core.rerender(); return; }
    const ok = await tnPlanDialog(a.data, {
      title: 'Tuning zurücknehmen?', ok: 'Zurücknehmen', icon: 'undo',
      empty: 'Es gibt nichts zurückzunehmen: vps-tune hat auf diesem Server nichts verändert, oder es ist schon zurückgenommen.',
      reboot: 'Die Bootparameter von vps-tune fallen erst mit dem nächsten Neustart weg; HostDeck bietet ihn dann hier an.',
      back: 'Danach kannst du jederzeit wieder ein Preset anwenden.',
    });
    if (!ok) { core.rerender(); return; }
    await startJob(tn, false, {}, 'Zurücknehmen', 'zuruecknehmen');
  }
  async function tnReboot(st) {
    const ok = await clDialog(done => [
      el('h2', { id: 'cl-h' }, 'Server jetzt neu starten?'),
      el('p', { id: 'cl-d', class: 'gt-lead' }, st.neustart_grund + ' Deine Projekte sind dabei ein bis zwei Minuten nicht erreichbar. Container mit einer Neustart-Regel (etwa unless-stopped) starten danach von selbst, der Leitstand auch.'),
      el('div', { class: 'gt-acts' }, el('button', { type: 'button', class: 'btn', 'data-autofocus': '', onclick: () => done(false) }, 'Abbrechen'),
        el('button', { type: 'button', class: 'btn btn-err', 'data-fid': 'tn-reboot-go', onclick: () => done(true) }, ic('refresh', 16), 'Neu starten'))]);
    if (!ok) { core.rerender(); return; }
    tn.rb = true; tn.result = null;
    core.rerender();
    const a = await core.call('/api/tuning/neustart', {});
    tn.rb = false;
    if (a.ok) { tn.rep = a.data; core.announce('Der Server startet neu'); }
    else if (!(a.data && a.data.abgebrochen)) tn.result = { ok: false, text: errText(a, 'Der Neustart') };
    core.rerender();
  }
  function tnRebootBand(r) {
    if (r.neustart && !r.neustart.fehler) return el('div', { class: 'band b-warn', role: 'status' },
      el('p', {}, el('b', {}, 'Der Server startet neu '), '(angestoßen ' + hm(new Date(r.neustart.seit)) + ' Uhr). Der Leitstand ist in ein bis zwei Minuten wieder da; lade die Seite dann neu und sieh unter Bootparameter nach, ob alles aktiv ist.'));
    const fail = r.neustart ? el('div', { class: 'band b-err', role: 'alert' }, el('p', {}, r.neustart.fehler)) : null;
    const st = r.stand;
    if (!st || !st.neustart_noetig) return fail;
    const busy = tnBusy();
    return [fail, el('div', { class: 'band b-warn tn-rb', role: 'status' },
      el('p', {}, el('b', {}, 'Neustart nötig. '), st.neustart_grund, busy ? ' ' + busy : ''),
      el('button', { type: 'button', class: 'btn btn-sm', 'data-fid': 'tn-reboot', disabled: tn.rb || !!busy, onclick: () => tnReboot(st) }, ic('refresh', 16), tn.rb ? 'Startet neu …' : 'Server jetzt neu starten …'))];
  }
  // Umzug: Tuning vom alten Server als Angebot. Es zieht nicht
  // als Dateien mit; hier wendet tune.sh es nach Vorschau neu an.
  function tnOldText(r, o) {
    const pr = r.presets.find(p => p.name === o.angebot.preset);
    const sw = Object.entries(o.angebot.schalter || {}).map(([k, v]) => '--' + k + ' ' + v);
    return '„' + (pr ? pr.titel : o.angebot.preset) + '“ (' + o.angebot.preset + ')' + (sw.length ? ' mit ' + sw.join(', ') : '');
  }
  function tnOldPrio(o) {
    if (!o.vorrang || !tn.svc) return [];
    return o.vorrang.filter(x => { const u = tn.svc.find(s => s.name === x.dienst); return !u || u.stufe !== x.stufe; });
  }
  async function tnOldDrop() {
    tn.busy = 'drop';
    core.rerender();
    const a = await core.call('/api/tuning/alter-server/verwerfen', {});
    tn.busy = '';
    if (a.ok) { tn.rep = a.data; core.announce('Tuning vom alten Server verworfen'); }
    else tn.result = { ok: false, text: errText(a, 'Das Verwerfen') };
    core.rerender();
  }
  function tnOldBand(r) {
    const o = r.alter_server;
    if (!o || tn.watching) return null;
    const ap = r.stand && r.stand.angewendet;
    const tune = o.angebot && !o.gleich;
    const prio = tnOldPrio(o);
    if (!tune && !prio.length) return null;
    const busy = tnBusy();
    const from = o.von || 'dem alten Server';
    const lv = v => (TN_LEVEL.find(x => x[0] === v) || [v, v])[1];
    return el('div', { class: 'band b-info tn-old', role: 'status' },
      el('p', {}, el('b', {}, 'Vom Umzug: '), 'Auf ' + from + ' lief ',
        tune ? ['das Tuning ' + tnOldText(r, o) + ', angewendet ' + when(o.angewendet.seit) + '. ', ap ? 'Hier ist gerade ein anderes angewendet. ' : 'Hier ist noch keins angewendet. ',
          'Es zog nicht als Dateien mit; tune.sh wendet es hier passend zu Kernel und Netzwerkkarte neu an.'] : 'ein CPU-Vorrang, der hier fehlt.'),
      tune && (o.angebot.schalter || {}).net === 'off' && (tnPreset(r, o.angebot.preset).werte || {}).net === 'on'
        ? el('p', { class: 'ls-mut' }, 'Dort stand --net off, vielleicht weil BBR fehlte. Kann dieser Server BBR, stelle den Schalter unter „Erweitert“ auf „wie Preset“.') : null,
      prio.length ? el('ul', { class: 'tn-op' }, prio.map(x => {
        const u = tn.svc.find(s => s.name === x.dienst);
        return el('li', {}, el('span', {}, el('b', { class: 'mono' }, x.dienst), ': ' + lv(x.stufe), u ? '' : el('span', { class: 'ls-mut' }, ' (läuft hier gerade nicht)')),
          el('button', { type: 'button', class: 'btn btn-sm', 'data-fid': 'tn-op-' + x.dienst, disabled: !!busy || !!tn.prio, onclick: () => tnPrio(u || { name: x.dienst }, x.stufe) }, ic('check', 16), 'Übernehmen …'));
      })) : null,
      el('div', { class: 'acts' },
        tune ? el('button', { type: 'button', class: 'btn btn-sm btn-p', 'data-fid': 'tn-old', disabled: !!busy || !!tn.busy,
          onclick: () => { tn.preset = o.angebot.preset; tn.set = Object.assign({}, o.angebot.schalter || {}); tnApply(r); } }, ic('eye', 16), 'Vorschau wie auf dem alten Server …') : null,
        el('button', { type: 'button', class: 'btn btn-sm btn-g', 'data-fid': 'tn-old-drop', disabled: !!tn.busy, onclick: tnOldDrop }, tn.busy === 'drop' ? 'Verwirft …' : 'Nicht übernehmen'),
        busy ? el('span', { class: 'why' }, busy) : null));
  }
  async function loadTnSvc() {
    tn.svcLoading = true;
    const r = await core.call('/api/tuning/dienste', null, true);
    tn.svcLoading = false;
    if (r.ok) { tn.svc = r.data.dienste; tn.svcErr = ''; } else tn.svcErr = errText(r, 'Das Laden der Dienste');
    if (ui.page === 'tuning' && core.work() && !modalOpen()) core.rerender();
  }
  async function tnPrio(u, v) {
    const label = TN_LEVEL.find(x => x[0] === v)[1];
    const ok = await clDialog(done => [
      el('h2', { id: 'cl-h' }, 'CPU-Vorrang für ' + u.name + ': ' + label + '?'),
      el('p', { id: 'cl-d', class: 'gt-lead' }, v === 'normal'
        ? 'tune.sh nimmt seine Einstellung zurück: das Drop-in von vorher oder der Standard 100. Sicher gilt der alte Wert nach einem Neustart des Dienstes oder des Servers.'
        : 'tune.sh setzt CPUWeight=' + (v === 'high' ? 1000 : 20) + ' (Standard 100) mit systemctl set-property. Das wirkt sofort und nur, wenn Dienste um den Prozessor konkurrieren. Rückweg: normal oder Zurücknehmen.'),
      el('div', { class: 'gt-acts' }, el('button', { type: 'button', class: 'btn', onclick: () => done(false) }, 'Abbrechen'),
        el('button', { type: 'button', class: 'btn btn-p', onclick: () => done(true) }, ic('check', 16), 'Setzen'))]);
    if (!ok) { core.rerender(); return; }
    tn.prio = u.name; tn.result = null;
    core.rerender();
    const a = await core.call('/api/tuning/vorrang', { dienst: u.name, stufe: v });
    tn.prio = '';
    if (a.ok) {
      if (a.data.dienste) tn.svc = a.data.dienste;
      const e = a.data.ergebnis;
      tn.result = { ok: e.code === 0, text: e.code === 0 ? 'CPU-Vorrang für ' + u.name + ': ' + label + '.' : 'CPU-Vorrang ging nicht: ' + (e.zuletzt || 'siehe Protokoll') };
      core.announce(tn.result.text);
      core.refreshLauf();
    } else if (!(a.data && a.data.abgebrochen)) tn.result = { ok: false, text: errText(a, 'Der CPU-Vorrang') };
    core.rerender();
  }
  function tnUnit(u, busy) {
    // „normal“ nimmt tune.sh prio zurück und löscht dabei das Drop-in, auch
    // ein fremdes. Darum nur für Dienste, die vps-tune selbst verändert hat.
    const foreign = !u.von_vps_tune && u.stufe !== 'normal';
    const seg = el('div', { class: 'ls-seg', role: 'group', 'aria-label': 'CPU-Vorrang für ' + u.name }, TN_LEVEL.map(([v, t]) =>
      el('button', { type: 'button', 'data-fid': 'tn-p-' + u.name + '-' + v, 'aria-pressed': String(u.stufe === v), disabled: !!tn.prio || !!busy || (v === 'normal' && foreign),
        title: v === 'normal' && foreign ? 'Diesen CPUWeight hat nicht vps-tune gesetzt; „normal“ würde ihn ohne Sicherung löschen.' : null,
        onclick: () => { if (u.stufe !== v) tnPrio(u, v); } }, t)));
    const sub = [u.beschreibung, u.stufe === 'other' ? 'CPUWeight ' + u.gewicht + ', nicht von vps-tune' : u.gewicht ? 'CPUWeight ' + u.gewicht : '', u.laeuft ? '' : 'läuft gerade nicht'].filter(Boolean).join(' · ');
    return el('li', { class: 'tn-u' },
      el('span', { class: 'cl-t' }, el('b', { class: 'mono' }, u.name), u.von_vps_tune ? el('span', { class: 'tag t-sug' }, 'von vps-tune') : null, el('span', { class: 'ls-mut blk' }, sub)),
      tn.prio === u.name ? el('span', { class: 'ls-mut' }, 'setzt …') : seg);
  }
  function tnPrioView() {
    if (tn.svcErr) return el('p', { class: 'gt-err', role: 'alert' }, ic('warn', 16), el('span', {}, tn.svcErr));
    if (!tn.svc) return el('div', { class: 'ls-stack' }, el('span', { class: 'skel skel-t skel-w' }), el('span', { class: 'skel skel-t' }));
    const busy = tnBusy();
    const mine = tn.svc.filter(u => u.von_vps_tune), rest = tn.svc.filter(u => !u.von_vps_tune);
    return el('div', { class: 'ls-stack sm' },
      mine.length ? el('ul', { class: 'tn-us' }, mine.map(u => tnUnit(u, busy))) : el('p', { class: 'ls-mut' }, 'Noch kein Dienst mit eigenem Vorrang; alle teilen sich den Prozessor gleich (CPUWeight 100).'),
      rest.length ? el('details', { class: 'bk-out', open: tn.svcOpen, ontoggle: e => { tn.svcOpen = e.target.open; } },
        el('summary', {}, 'Laufende Dienste (' + rest.length + ')'), el('ul', { class: 'tn-us' }, rest.map(u => tnUnit(u, busy)))) : null,
      busy ? el('p', { class: 'ls-mut' }, busy) : null);
  }
  function pageTuning() {
    const help = 'Dein tune.sh (vps-tune), unverändert. HostDeck zeigt erst die Vorschau des Skripts (--dry-run) und wendet dann an. Vor jeder Änderung sichert das Skript den Originalwert; „Zurücknehmen“ stellt alles wieder her. Werte zur Laufzeit wirken sofort, Bootparameter erst nach einem Neustart. Anwenden, CPU-Vorrang, Zurücknehmen und Neustart fragen dein Passwort. Im Terminal: sudo hostdeck tuning.';
    if (!core.svc()) return [head({ i: 'leistung', k: 'Tuning', h: 'Gibt es nur im Dienst.', p: 'Das Tuning steuert HostDeck, wenn es als Dienst auf dem Server läuft (sudo ./hostdeck einrichten). Im Terminal geht es mit sudo hostdeck tuning status.' })];
    if (!tn.loading && !tn.watching && (!tn.at || Date.now() - tn.at > 30000)) setTimeout(() => loadJob(tn), 0);
    if (!tn.svc && !tn.svcLoading && !tn.svcErr) setTimeout(loadTnSvc, 0);
    const r = tn.rep;
    if (!r) return [head({ i: 'leistung', k: 'Tuning', h: tn.error ? ['Der Stand ist ', em('err', 'nicht erreichbar'), '.'] : 'HostDeck liest den Stand …', p: tn.error || help }),
      sec(null, el('div', { class: 'ls-stack' }, el('span', { class: 'skel skel-h' }), el('span', { class: 'skel skel-t skel-w' })))];
    tnInit(r);
    const st = r.stand, ap = st && st.angewendet, job = r.auftrag;
    const pr = ap && r.presets.find(p => p.name === ap.preset);
    const rb = st && st.neustart_noetig ? [', ', em('warn', 'Neustart nötig')] : '';
    const h = tn.watching ? ['Das Tuning ', em('warn', 'arbeitet'), '.']
      : !st ? ['Der Stand ist ', em('err', 'nicht lesbar'), '.']
        : ap ? ['Angewendet: ', em('ok', pr ? pr.titel : ap.preset), ' (' + ap.preset + ') seit ' + when(ap.seit), rb, '.']
          : ['Kein Tuning aktiv', rb, '.'];
    return [head({ i: 'leistung', k: 'Tuning', h, p: help + (r.version ? ' Auf dem Server liegt vps-tune ' + r.version + '.' : ''),
      a: el('button', { type: 'button', class: 'btn btn-sm', 'data-fid': 'tn-ref', disabled: tn.loading || tn.watching, onclick: () => { tn.svc = null; loadJob(tn); } }, ic('refresh', 16), 'Aktualisieren') }),
    r.fehler ? el('p', { class: 'band b-err', role: 'status' }, r.fehler) : null,
    tnRebootBand(r),
    tnOldBand(r),
    liveResult(tn),
    tn.error ? el('p', { class: 'gt-err', role: 'alert' }, ic('warn', 16), el('span', {}, tn.error)) : null,
    el('div', { class: 'ls-grid eq' },
      job ? sec(job.laeuft ? 'Läuft' : 'Letzter Lauf', jobView(job, tn, tnText(job)), { cls: 'c12' }) : null,
      sec('Ist-Zustand', tnStatus(r), { cls: 'c7', sub: 'tune.sh status' }),
      sec('Preset anwenden', tnForm(r), { cls: 'c5' }),
      sec('CPU-Vorrang je Dienst', tnPrioView(), { cls: 'c7', sub: 'tune.sh prio · CPUWeight' }),
      sec('Zurücknehmen', el('div', { class: 'ls-stack sm' },
        el('p', {}, 'Setzt jeden Wert, den vps-tune geändert hat, auf den gesicherten Originalwert zurück, nimmt den CPU-Vorrang zurück und entfernt alle Dateien von vps-tune (restore). Erst kommt die Vorschau.'),
        el('div', { class: 'ls-row-end' }, el('span', { class: 'ls-mut' }, tnBusy()),
          el('button', { type: 'button', class: 'btn', 'data-fid': 'tn-restore', disabled: !!tnBusy() || !!tn.busy, onclick: tnRestore }, ic('undo', 18), tn.busy === 'restore' ? 'Vorschau …' : 'Zurücknehmen …'))), { cls: 'c5' }))];
  }

  // ---------- Überwachung ----------
  // Verlauf aus /api/ueberwachung/verlauf. Jedes Diagramm hat eine Achse,
  // beginnt bei null und zeigt den ganzen Zeitraum: was vor dem Start von
  // HostDeck lag oder fehlt (HostDeck lief nicht), bleibt leer. Die Linie ist das
  // Mittel je Schritt, das Band reicht bis zur Spitze; Zuletzt, Mittel und
  // Spitze darunter rechnet der Dienst (kennzahlen, siehe uwK).
  const RANGES = [['15m', '15 Min', 900], ['1h', '1 Std', 3600], ['6h', '6 Std', 21600], ['1d', '24 Std', 86400], ['7d', '7 Tage', 604800], ['30d', '30 Tage', 2592000]];
  const RANGE_LONG = { '15m': '15 Minuten', '1h': '1 Stunde', '6h': '6 Stunden', '1d': '24 Stunden', '7d': '7 Tage', '30d': '30 Tage' };
  const RANGE_OF = { '15m': 'der letzten 15 Minuten', '1h': 'der letzten Stunde', '6h': 'der letzten 6 Stunden', '1d': 'der letzten 24 Stunden', '7d': 'der letzten 7 Tage', '30d': 'der letzten 30 Tage' };
  // zoom: [von, bis] in Unix-Sekunden für alle Diagramme der Seite;
  // hidden: ausgeblendete Reihen (Diagramm:Reihe); range 'live': jede
  // Sekunde (lv, unten); sel: gewählter Zeitpunkt eines Diagramms.
  const uw = { range: '1h', key: '', hist: null, histRange: '', at: 0, loading: false, error: '', sort: 'cpu', open: new Set(), timer: 0, hover: false, zoom: null, hidden: new Set(), sel: null, metric: 'cpu', room: 'alles', whoAll: false };
  const uwRO = typeof ResizeObserver === 'function' ? new ResizeObserver(es => { for (const e of es) { const f = e.target.uwDraw; if (f) f(e.contentRect.width); } }) : null;
  async function loadHist() {
    if (uw.loading || !core.svc() || uw.range === 'live') return;
    uw.loading = true;
    const rng = uw.range;
    const r = await core.call('/api/ueberwachung/verlauf?zeitraum=' + rng + '&punkte=360&spitzen=1&serien=' + encodeURIComponent('s/,d/,c/*/cpu,c/*/ram,p/*/cpu,p/*/ram'), null, true);
    uw.loading = false;
    uw.at = Date.now();
    if (r.ok) { uw.hist = r.data; uw.histRange = rng; uw.error = ''; } else if (r.status !== 404) uw.error = (r.data && r.data.error) || 'Fehler ' + r.status;
    if (rng !== uw.range) { if (uw.range !== 'live') loadHist(); return; }
    if (ui.page === 'ueberwachung' && core.work() && !modalOpen() && !uw.hover) core.rerender();
  }
  // Nachladen, solange die Seite offen und sichtbar ist: die Werte jetzt
  // (Kacheln) alle 10 Sekunden, der Verlauf im Takt des Zeitraums. Nicht,
  // während jemand ein Diagramm liest (Zeiger darauf oder Fokus darin);
  // in Live hält die Maus nichts an.
  function uwTick() {
    clearTimeout(uw.timer);
    uw.timer = setTimeout(() => {
      uw.timer = 0;
      if (ui.page !== 'ueberwachung') return;
      const live = uw.range === 'live';
      if (document.visibilityState === 'visible' && (live || !uw.hover) && !modalOpen()) {
        mon.at = 0; loadMon();
        const every = uw.range === '15m' || uw.range === '1h' ? 30000 : 120000;
        if (!live && !uw.zoom && Date.now() - uw.at >= every - 1000) loadHist();
      }
      uwTick();
    }, 10000);
  }
  const UW_FMT = {
    pct: v => pctText(v),
    bytes: v => size(Math.round(v)),
    rate: v => size(Math.round(v)) + '/s',
  };
  // Runde Obergrenze für die Achse (1, 2, 2,5, 5 × 10^n).
  function niceMax(v, unit) {
    if (!(v > 0)) return unit === 'pct' ? 10 : 1000;
    const e = Math.pow(10, Math.floor(Math.log10(v)));
    for (const f of [1, 2, 2.5, 5, 10]) if (v <= f * e * 1.0001) return Math.max(f * e, unit === 'pct' ? 1 : 1000);
    return 10 * e;
  }
  function uwTime(t, rng, long) {
    const d = new Date(t * 1000);
    const hm = d.toLocaleTimeString('de-DE', { hour: '2-digit', minute: '2-digit' });
    const dm = String(d.getDate()).padStart(2, '0') + '.' + String(d.getMonth() + 1).padStart(2, '0') + '.';
    const short = rng === '15m' || rng === '1h';
    if (long) return short ? hm : dm + ' ' + hm;
    return short || rng === '6h' || rng === '1d' ? hm : dm;
  }
  function uwStats(v, times) {
    let sum = 0, n = 0, peak = null, at = 0;
    v.forEach((x, i) => { if (x == null) return; sum += x; n++; if (peak === null || x > peak) { peak = x; at = times[i]; } });
    return n ? { avg: sum / n, peak, at, last: [...v].reverse().find(x => x != null) } : null;
  }
  // Kennzahlen einer Reihe vom Dienst: über den ganzen Zeitraum, jedes
  // Stück Zeit aus der feinsten Stufe. So ist die Spitze eines längeren
  // Zeitraums nie kleiner als die eines kürzeren, und „Zuletzt“ ist überall
  // der letzte Messwert. f rechnet um (etwa Byte in Prozent).
  function uwK(h, k, f) {
    const st = h && h.kennzahlen ? h.kennzahlen[k] : null;
    if (!st || st.spitze == null) return null;
    const g = f || (x => x);
    return { last: g(st.zuletzt), avg: g(st.mittel), peak: g(st.spitze), at: st.spitze_um, step: st.spitze_schritt, since: st.seit, up: false };
  }
  // Kennzahlen aus den Punkten (Ausschnitt beim Zoomen, Summen ohne
  // eigene Messung): Spitze aus p, „bis zu“, wenn sie aus einer
  // Obergrenze stammt (up je Punkt).
  // Das Mittel ist nach Zeit gewichtet wie beim Dienst: Jeder Punkt
  // zählt so lange, wie er steht; kurz nach einem Neustart liegen die
  // groben Punkte bis grob_bis weiter auseinander. Der letzte Punkt zählt
  // nur bis zur jüngsten Messung (bis), der erste eines neuen Verlaufs erst
  // ab dem Beginn der Reihe (since: seit aus den Kennzahlen, sonst beginn).
  function uwStatsP(v, p, up, times, step, keep, h, since) {
    let sum = 0, n = 0, peak = null, at = 0, last = null, u = false;
    const cTo = h && h.grob_bis || 0, cStep = h && h.grob_schritt || step;
    const b0 = since || (h && h.beginn) || 0, end = h && h.bis || 0;
    const dt = i => {
      const cap = times[i] <= cTo ? cStep : step;
      let w = Math.min(cap, i + 1 < times.length ? times[i + 1] - times[i] : end ? end - times[i] : cap);
      if (times[i] < b0) w -= b0 - times[i];
      return Math.max(1, w);
    };
    v.forEach((x, i) => {
      if (x == null || (keep && !keep(i))) return;
      const wt = dt(i);
      sum += x * wt; n += wt; last = x;
      const q = p && p[i] != null ? Math.max(p[i], x) : x, qu = !!(up && up[i]);
      // Bei Gleichstand zählt der genaue Wert, nicht die Obergrenze.
      if (peak === null || q > peak || (q === peak && u && !qu)) { peak = q; at = times[i]; u = qu; }
    });
    return n ? { avg: sum / n, peak, at, last, step, up: u } : null;
  }
  // Wie überall (atTime, R6): „heute, 15:23“, „gestern, 15:23“, „14.11.,
  // 15:23“; ist der Abschnitt länger als eine Minute, die Spanne
  // („gestern zwischen 15:20 und 15:30“).
  function uwPeakAt(st) {
    const a = atTime(st.at);
    if (!(st.step > 60) || !a) return a;
    const e = new Date((st.at + st.step) * 1000).toLocaleTimeString('de-DE', { hour: '2-digit', minute: '2-digit' });
    const t = a.textContent, i = t.lastIndexOf(' ');
    const day = t.slice(0, i).replace(/,$/, '');
    a.textContent = day + ' zwischen ' + t.slice(i + 1) + ' und ' + e;
    return a;
  }
  // Summe mehrerer Container als Reihe. Genau ist sie aus der Summe, die
  // HostDeck je Compose-Projekt mitmisst (p/…). Wo die fehlt (Zeit vor dem
  // Update, Spiele-Panel, „übrige“), zählt die Seite die Container
  // zusammen: Die Spitzen zusammengezählt sind dann nur eine Obergrenze
  // („bis zu“), sobald mehr als ein Container Werte hat.
  function uwTotal(h, names, kind, proj) {
    const n = h ? h.zeiten.length : 0;
    const v = new Array(n).fill(null), p = new Array(n).fill(null), up = new Array(n).fill(false), cnt = new Array(n).fill(0);
    if (!h) return { v, p, up, k: null, any: false };
    const P = k => (h.spitzen || {})[k] || null;
    // Beginn der Summe: der früheste der Container (für das Mittel).
    const first = Math.min(...names.map(nm => (uwK(h, 'c/' + nm + '/' + kind) || { since: Infinity }).since));
    const since = Number.isFinite(first) ? first : 0;
    let any = false;
    for (const nm of names) {
      const sv = h.serien['c/' + nm + '/' + kind], sp = P('c/' + nm + '/' + kind);
      if (!sv) continue;
      sv.forEach((x, i) => { if (x == null) return; any = true; v[i] = (v[i] || 0) + x; p[i] = (p[i] || 0) + (sp && sp[i] != null ? Math.max(sp[i], x) : x); cnt[i]++; });
    }
    cnt.forEach((c, i) => { up[i] = c > 1; });
    if (names.length === 1) return { v, p, up, k: uwK(h, 'c/' + names[0] + '/' + kind), any, since };
    let k = null;
    const pk = proj ? 'p/' + proj + '/' + kind : '';
    const pv = pk ? h.serien[pk] : null;
    if (pv) {
      const pp = P(pk);
      let prev = false;
      pv.forEach((x, i) => {
        if (x == null) { prev = false; return; }
        const q = pp && pp[i] != null ? Math.max(pp[i], x) : x;
        // Der erste Punkt mit Summe kann noch Zeit davor enthalten: Dort
        // bleibt die Obergrenze, wenn sie höher ist.
        v[i] = x;
        if (prev || p[i] == null || p[i] <= q) { p[i] = q; up[i] = false; }
        any = true; prev = true;
      });
      // Genau über den ganzen Zeitraum nur, wenn die Summe so weit
      // zurückreicht wie die Container.
      k = uwK(h, pk);
      if (k && k.since > first) k = null;
    }
    return { v, p, up, k, any, since };
  }
  // Kennzahlen einer Summe: vom Dienst, sonst aus den Punkten.
  function uwTotalStats(t, h) {
    return t.k || (h ? uwStatsP(t.v, t.p, t.up, h.zeiten, h.schritt, null, h, t.since) : null);
  }
  function svgEl(tag, attrs) {
    const e = document.createElementNS(SVG, tag);
    for (const [k, v] of Object.entries(attrs || {})) if (v != null) e.setAttribute(k, String(v));
    return e;
  }
  // Pfad mit Lücken: neu ansetzen, wo ein Wert fehlt oder mehr als zwei
  // Schritte zwischen zwei Punkten liegen.
  // Kurz nach einem Neustart liegen die Punkte bis grob_bis weiter
  // auseinander (Minuten statt 10 Sekunden); das ist keine Lücke.
  function uwPath(v, times, step, X, Y, h) {
    const cTo = h && h.grob_bis || 0, cStep = h && h.grob_schritt || step;
    let d = '', open = false, prevT = 0;
    v.forEach((x, i) => {
      if (x == null) { open = false; return; }
      const gap = open && times[i] - prevT > (prevT <= cTo ? cStep : step) * 2.5;
      d += (open && !gap ? 'L' : (d ? ' M' : 'M')) + X(times[i]).toFixed(1) + ' ' + Y(x).toFixed(1);
      if (!open || gap) d += 'h0.01';
      open = true; prevT = times[i];
    });
    return d;
  }
  // Liniendiagramm mit Fadenkreuz. Zeiger oder Pfeiltasten zeigen die Werte
  // zu einem Zeitpunkt in der Zeile darüber; ohne Auswahl steht dort das
  // Mittel und die Spitze.
  function uwChart(o) {
    const h = o.hist, rng = o.range;
    const full = (RANGES.find(r => r[0] === rng) || RANGES[1])[2];
    const tEnd = Math.max(Math.floor(Date.now() / 1000), h.zeiten.length ? h.zeiten[h.zeiten.length - 1] : 0);
    // Zoom gilt für alle Diagramme der Überwachung, nicht für die Übersicht.
    const z = !o.noZoom && uw.zoom ? uw.zoom : null;
    const t0 = z ? z[0] : tEnd - full, t1 = z ? z[1] : tEnd, span = Math.max(1, t1 - t0);
    const all = o.series.filter(s => s.v && s.v.some(x => x != null));
    const hid = s => uw.hidden.has(o.id + ':' + s.label);
    const fmt = UW_FMT[o.unit];
    // Lesen hält das Nachladen an (uw.hover), nicht aber in der Übersicht
    // (pause: false); dort bleibt der gewählte Zeitpunkt beim
    // Neuzeichnen stehen (uw.sel).
    const pause = o.pause !== false;
    const inWin = i => h.zeiten[i] >= t0 && h.zeiten[i] <= t1;
    // Zuletzt, Mittel und Spitze: ohne Zoom vom Dienst (k), über den
    // ganzen Zeitraum; im Ausschnitt aus den Punkten.
    const statsOf = s => (!z && s.k) || uwStatsP(s.v, s.p, s.up, h.zeiten, h.schritt, inWin, h, (s.k && s.k.since) || s.since);
    const stats = new Map(all.map(s => [s, statsOf(s)]));
    // Zeitfenster der Leistungstests: hinterlegt, damit ihre Spitzen nicht
    // wie ein Problem aussehen.
    const marks = (o.marks === false ? [] : ltMarks()).filter(m => m.b >= t0 && m.a <= t1);
    const inMark = t => marks.some(m => t >= m.a - (h.schritt || 0) && t <= m.b + (h.schritt || 0));
    // Ohne Werte: keine Achsen, nur der Hinweis.
    if (!all.length) {
      return sec(o.title, el('div', { class: 'ls-empty uw-empty' }, ic('puls', 22),
        el('div', {}, el('b', {}, 'Noch keine Werte'), el('p', {}, o.empty || 'In diesem Zeitraum gibt es noch keinen Verlauf.'))), { cls: o.cls || 'c6', sub: o.sub, end: o.end });
    }
    // Beginnt der Verlauf erst mitten im Zeitraum, sagt die Lesezeile, seit wann.
    let tFirst = Infinity;
    for (const s0 of all) s0.v.forEach((x, i) => { if (x != null && inWin(i) && h.zeiten[i] < tFirst) tFirst = h.zeiten[i]; });
    // Der erste Punkt eines neuen Verlaufs beginnt an seiner Grenze, die
    // Messungen erst bei beginn.
    if (tFirst !== Infinity && h.beginn > tFirst) tFirst = h.beginn;
    const sinceTxt = tFirst !== Infinity && tFirst - t0 > span * 0.08 ? 'Verlauf seit ' + uwTime(tFirst, z ? '7d' : rng, true) + '; davor hat HostDeck noch nicht gemessen.' : '';
    // Grobe Punkte nach einem Neustart sichtbar machen (6B): hinterlegt im
    // Diagramm und in Worten über dem Diagramm.
    const cTo = h.grob_bis && h.grob_bis > t0 && tFirst < h.grob_bis ? Math.min(h.grob_bis, t1) : 0;
    const grobTxt = cTo ? 'Bis ' + uwTime(cTo, z ? '7d' : rng, true) + ' liegen die Punkte weiter auseinander (' + (h.grob_schritt >= 600 ? 'alle 10 Minuten' : h.grob_schritt >= 60 ? 'jede Minute' : 'gröber') + '): Der Dienst wurde neu gestartet. Spitzen bleiben erhalten.' : '';
    const H = o.height || 188, top = 16, bottom = 22;
    // R: Platz rechts für den Wert am Ende der Linie (3.3), P: Breite der Zeichnung.
    const R = 60;
    let W = 560, P = W - R, idx = -1, ymax = 1;
    const read = el('p', { class: 'uw-read' });
    const rl = z ? uwTime(t0, '7d', true) + ' bis ' + uwTime(t1, '7d', true) : RANGE_LONG[rng];
    const svg = svgEl('svg', { class: 'uw-svg' + (o.noZoom ? '' : ' zoomable'), role: 'img', tabindex: all.length ? '0' : null, 'data-fid': 'uw-' + o.id });
    const label = () => o.title + ', ' + rl + ': ' + (all.length ? all.filter(s => !hid(s) && stats.get(s)).map(s => { const st0 = stats.get(s); return s.label + ' zuletzt ' + fmt(st0.last) + ', im Mittel ' + fmt(st0.avg) + ', Spitze ' + (st0.up ? 'bis zu ' : '') + fmt(st0.peak); }).join('; ') : 'noch kein Verlauf') + '.';
    const X = t => (t - t0) / span * P;
    const Y = v => top + (1 - Math.min(v, ymax) / ymax) * (H - top - bottom);
    // Legende zum Ein- und Ausblenden, darunter Zuletzt, Mittel, Spitze.
    const legend = all.length > 1 ? el('div', { class: 'uw-legend', role: 'group', 'aria-label': 'Reihen von ' + o.title }, all.map(s =>
      el('button', { type: 'button', class: 'uw-lg', 'aria-pressed': String(!hid(s)), 'data-fid': 'uw-lg-' + o.id + '-' + s.slot,
        onclick: e => { const k = o.id + ':' + s.label; if (uw.hidden.has(k)) uw.hidden.delete(k); else if (all.some(x => x !== s && !hid(x))) uw.hidden.add(k); e.currentTarget.setAttribute('aria-pressed', String(!uw.hidden.has(k))); draw(); } },
        el('i', { class: 's' + s.slot + (s.dash ? ' dash' : '') }), s.label))) : null;
    const nums = el('dl', { class: 'uw-nums' });
    function showNums() {
      const vis = all.filter(s => !hid(s) && stats.get(s));
      nums.replaceChildren(...vis.map(s => {
        const st0 = stats.get(s);
        return el('div', { class: 'uw-n' }, el('dt', {}, vis.length > 1 ? el('i', { class: 's' + s.slot }) : null, vis.length > 1 ? s.label : 'Werte'),
          el('dd', {}, el('span', {}, 'Zuletzt ', el('b', {}, fmt(st0.last))), el('span', {}, 'Mittel ', el('b', {}, fmt(st0.avg))),
            el('span', {}, 'Spitze ', st0.up ? 'bis zu ' : '', el('b', {}, fmt(st0.peak)), el('small', {}, ' ', uwPeakAt(st0), inMark(st0.at) ? ', im Leistungstest' : ''))));
      }));
      nums.hidden = !vis.length;
    }
    const keyOf = (s, x) => el('span', { class: 'uw-k' }, all.length > 1 ? el('i', { class: 's' + s.slot }) : null, s.label + ' ', el('b', {}, x));
    function showRead() {
      const vis = all.filter(s => !hid(s));
      if (idx < 0) {
        // Der Bedienhinweis steht nur am ersten Diagramm einer Seite.
        const hint = o.hint ? (o.noZoom ? 'Zeiger oder Pfeiltasten zeigen die Werte zu einem Zeitpunkt.' : 'Ziehen oder + und − vergrößern einen Ausschnitt; Zeiger oder Pfeiltasten zeigen die Werte.') : '';
        const mk = o.hint && marks.length ? 'Hinterlegt: ' + (marks.length === 1 ? 'Leistungstest ' : plural(marks.length, 'Leistungstest', 'Leistungstests') + ', zuletzt ') + uwTime(marks[0].a, '7d', true) + (marks[0].run ? ', läuft noch' : '–' + uwTime(marks[0].b, '1h', true)) + '; die Spitzen dort sind gewollt.' : '';
        read.replaceChildren(...[sinceTxt, o.hint ? grobTxt : '', mk, hint].filter(Boolean).map(t => el('span', { class: 'ls-mut' }, t)));
        return;
      }
      // replaceChildren macht aus null den Text „null“; daher nur, was da ist.
      read.replaceChildren(...[el('span', { class: 't' }, uwTime(h.zeiten[idx], z ? '7d' : rng, true)),
        inMark(h.zeiten[idx]) ? el('span', { class: 'uw-k uw-mk' }, 'Leistungstest') : null,
        ...vis.map(s => keyOf(s, s.v[idx] == null ? 'keine Messung' : fmt(s.v[idx]) + (s.p && s.p[idx] != null && s.p[idx] > s.v[idx] * 1.05 ? ' (Spitze ' + (s.up && s.up[idx] ? 'bis zu ' : '') + fmt(s.p[idx]) + ')' : ''))),
        pause && uw.hover && ui.page === 'ueberwachung' ? el('span', { class: 'uw-hold' }, 'angehalten, solange du liest') : null].filter(Boolean));
    }
    let cross = null, dots = [], sel = null;
    function draw(w) {
      W = Math.max(240, Math.round(w || W)); P = W - R;
      const vis = all.filter(s => !hid(s));
      let peak = 0;
      for (const s of vis) s.v.forEach((x, i) => { if (!inWin(i)) return; const p = s.p && s.p[i] != null ? s.p[i] : x; if (p != null && p > peak) peak = p; });
      ymax = o.max || niceMax(peak, o.unit);
      svg.setAttribute('viewBox', '0 0 ' + W + ' ' + H);
      svg.setAttribute('width', String(W)); svg.setAttribute('height', String(H));
      svg.setAttribute('aria-label', label());
      svg.replaceChildren();
      for (const f of [1, 0.5]) {
        const y = Y(ymax * f);
        svg.append(svgEl('line', { class: 'uw-grid', x1: 0, x2: W, y1: y, y2: y }));
        const t = svgEl('text', { class: 'uw-ax', x: 0, y: y - 4 }); t.textContent = fmt(ymax * f); svg.append(t);
      }
      svg.append(svgEl('line', { class: 'uw-base', x1: 0, x2: W, y1: Y(0), y2: Y(0) }));
      const axRng = z ? (span <= 7200 ? '1h' : span <= 172800 ? '1d' : '7d') : rng;
      [[0, 'start'], [1 / 3, 'middle'], [2 / 3, 'middle'], [1, 'end']].forEach(([f, a]) => {
        const t = svgEl('text', { class: 'uw-ax', x: f * P, y: H - 5, 'text-anchor': a }); t.textContent = uwTime(t0 + f * span, axRng); svg.append(t);
      });
      const clip = 'uwc-' + o.id;
      const defs = svgEl('defs'); const cp = svgEl('clipPath', { id: clip }); cp.append(svgEl('rect', { x: 0, y: 0, width: P + 1, height: H - bottom + 1 })); defs.append(cp); svg.append(defs);
      const g = svgEl('g', { 'clip-path': 'url(#' + clip + ')' });
      svg.append(g);
      if (cTo) {
        const r = svgEl('rect', { class: 'uw-grob', x: Math.max(0, X(t0)).toFixed(1), y: top - 6, width: Math.max(0, X(cTo) - Math.max(0, X(t0))).toFixed(1), height: (Y(0) - top + 6).toFixed(1) });
        const tt = svgEl('title'); tt.textContent = 'gröbere Punkte nach dem Neustart'; r.append(tt);
        g.append(r);
      }
      for (const m of marks) {
        const xa = Math.max(0, X(m.a)), xb = Math.min(P, X(m.b));
        const r = svgEl('rect', { class: 'uw-mark', x: xa.toFixed(1), y: top - 6, width: Math.max(3, xb - xa).toFixed(1), height: (Y(0) - top + 6).toFixed(1) });
        const tt = svgEl('title'); tt.textContent = 'Leistungstest'; r.append(tt);
        g.append(r);
      }
      for (const s of vis) {
        // Band vom Mittel bis zur Spitze: je zusammenhängendem Stück.
        if (s.p) {
          let up = [], down = [];
          const flush = () => { if (up.length > 1) g.append(svgEl('path', { class: 'uw-band s' + s.slot, d: 'M' + up.join('L') + 'L' + down.reverse().join('L') + 'Z' })); up = []; down = []; };
          let prev = 0;
          s.v.forEach((x, i) => {
            if (x == null) { flush(); return; }
            const t = h.zeiten[i];
            if (up.length && t - prev > h.schritt * 2.5) flush();
            const p = s.p[i] != null ? Math.max(s.p[i], x) : x;
            up.push(X(t).toFixed(1) + ' ' + Y(p).toFixed(1)); down.push(X(t).toFixed(1) + ' ' + Y(x).toFixed(1));
            prev = t;
          });
          flush();
        } else if (vis.length === 1) {
          const parts = uwPath(s.v, h.zeiten, h.schritt, X, Y, h).split(' M').filter(Boolean);
          for (const pp of parts) {
            const pts = pp.replace(/^M/, '').split('L').map(q => q.replace('h0.01', '').trim().split(' ').map(Number));
            if (pts.length < 2) continue;
            g.append(svgEl('path', { class: 'uw-a s' + s.slot, d: 'M' + pts[0][0] + ' ' + Y(0) + 'L' + pts.map(q => q.join(' ')).join('L') + 'L' + pts[pts.length - 1][0] + ' ' + Y(0) + 'Z' }));
          }
        }
      }
      // Liegen zwei Linien übereinander (Netz rein/raus, Platte lesen/schreiben),
      // ist die zweite gestrichelt: so bleiben beide sichtbar.
      for (const s of vis) g.append(svgEl('path', { class: 'uw-l s' + s.slot + (s.dash ? ' dash' : ''), d: uwPath(s.v, h.zeiten, h.schritt, X, Y, h) }));
      // Wert am Ende jeder Linie (3.3): ohne Maus lesbar. Ohne Zoom ist das
      // „Zuletzt“ des Dienstes (die jüngste Messung) beim Stand der Daten,
      // nicht das Mittel des laufenden Punkts; im Ausschnitt der
      // letzte Punkt darin.
      uwEnds(svg, vis.map(s => {
        if (!z && s.k && s.k.last != null) return { slot: s.slot, x: X(Math.min(t1, h.bis || tEnd)), y: Y(s.k.last), text: fmt(s.k.last) };
        for (let i = s.v.length - 1; i >= 0; i--) if (s.v[i] != null && inWin(i)) return { slot: s.slot, x: X(h.zeiten[i]), y: Y(s.v[i]), text: fmt(s.v[i]) };
        return null;
      }), top, Y(0));
      sel = svgEl('rect', { class: 'uw-sel', y: top - 6, height: Y(0) - top + 6, x: 0, width: 0, visibility: 'hidden' });
      svg.append(sel);
      cross = svgEl('line', { class: 'uw-x', y1: top - 6, y2: Y(0), visibility: 'hidden' });
      svg.append(cross);
      dots = vis.map(s => { const c = svgEl('circle', { class: 'uw-d s' + s.slot, r: 4.5, visibility: 'hidden' }); svg.append(c); return [s, c]; });
      showNums();
      setIdx(idx);
    }
    function setIdx(i) {
      idx = i;
      if (i >= 0) uw.sel = { id: o.id, t: h.zeiten[i] };
      if (cross) {
        const on = i >= 0;
        cross.setAttribute('visibility', on ? 'visible' : 'hidden');
        if (on) { const x = X(h.zeiten[i]); cross.setAttribute('x1', x); cross.setAttribute('x2', x); }
        for (const [s, c] of dots) {
          const v = on ? s.v[i] : null;
          c.setAttribute('visibility', v == null ? 'hidden' : 'visible');
          if (v != null) { c.setAttribute('cx', X(h.zeiten[i])); c.setAttribute('cy', Y(v)); }
        }
      }
      showRead();
    }
    function nearest(x) {
      const t = t0 + x / P * span;
      let best = -1, bd = Infinity;
      h.zeiten.forEach((zt, i) => { if (!inWin(i)) return; const d = Math.abs(zt - t); if (d < bd) { bd = d; best = i; } });
      return bd <= Math.max(h.schritt * 3, span / 100) ? best : -1;
    }
    {
      const px = e => { const r = svg.getBoundingClientRect(); return r.width ? Math.max(0, Math.min(P, (e.clientX - r.left) / r.width * W)) : 0; };
      let drag = null;
      svg.addEventListener('pointerenter', () => { if (pause) uw.hover = true; });
      svg.addEventListener('pointerdown', e => {
        if (o.noZoom || e.button !== 0 || e.pointerType === 'touch') return;
        drag = px(e); svg.setPointerCapture(e.pointerId);
      });
      svg.addEventListener('pointermove', e => {
        const x = px(e);
        setIdx(nearest(x));
        if (drag !== null && sel) {
          sel.setAttribute('visibility', 'visible');
          sel.setAttribute('x', String(Math.min(drag, x))); sel.setAttribute('width', String(Math.abs(x - drag)));
        }
      });
      svg.addEventListener('pointerup', e => {
        if (drag === null) return;
        const x = px(e), a = Math.min(drag, x), b2 = Math.max(drag, x);
        drag = null;
        if (sel) sel.setAttribute('visibility', 'hidden');
        if (b2 - a < 8) return;
        const za = t0 + a / P * span, zb = t0 + b2 / P * span;
        // Nicht feiner als drei Messpunkte.
        if (zb - za < h.schritt * 3) return;
        uw.zoom = [Math.floor(za), Math.ceil(zb)];
        uw.hover = false; uw.sel = null;
        core.rerender();
      });
      svg.addEventListener('pointerleave', () => { if (drag === null) { uw.hover = false; uw.sel = null; setIdx(-1); } });
      svg.addEventListener('focus', () => { if (pause) uw.hover = true; if (idx < 0) { const last = h.zeiten.map((_, i) => i).filter(inWin).pop(); setIdx(last == null ? -1 : last); } });
      svg.addEventListener('blur', () => { uw.hover = false; uw.sel = null; setIdx(-1); });
      svg.addEventListener('keydown', e => {
        const ids = h.zeiten.map((_, i) => i).filter(inWin);
        if (!ids.length) return;
        const pos = Math.max(0, ids.indexOf(idx));
        const moves = { ArrowLeft: -1, ArrowRight: 1, PageUp: -10, PageDown: 10 };
        if (e.key in moves) setIdx(ids[Math.max(0, Math.min(ids.length - 1, (idx < 0 ? ids.length - 1 : pos) + moves[e.key]))]);
        else if (e.key === 'Home') setIdx(ids[0]);
        else if (e.key === 'End') setIdx(ids[ids.length - 1]);
        else if (e.key === 'Escape') { if (uw.zoom && !o.noZoom) { uw.zoom = null; core.rerender(); return; } setIdx(-1); }
        else if (!o.noZoom && (e.key === '+' || e.key === '=' || e.key === '-')) {
          // Zoom per Tastatur (6C): + halbiert den Ausschnitt um den
          // gewählten Zeitpunkt, − verdoppelt ihn bis zum ganzen Zeitraum.
          const mid = idx >= 0 ? h.zeiten[idx] : t0 + span / 2;
          const w = e.key === '-' ? span * 2 : span / 2;
          if (e.key === '-' && w >= full) uw.zoom = null;
          else if (w >= h.schritt * 3) {
            const a = Math.max(tEnd - full, Math.min(mid - w / 2, tEnd - w));
            uw.zoom = [Math.floor(a), Math.ceil(a + w)];
          } else return;
          core.rerender();
        } else return;
        e.preventDefault();
      });
    }
    const fig = el('figure', { class: 'uw-fig' }, legend, read, svg, nums);
    fig.uwDraw = draw;
    if (uw.sel && uw.sel.id === o.id) { const t = uw.sel.t; let best = -1, bd = Infinity; h.zeiten.forEach((zt, i) => { if (inWin(i) && Math.abs(zt - t) < bd) { bd = Math.abs(zt - t); best = i; } }); idx = best; }
    draw();
    if (uwRO) uwRO.observe(fig);
    return sec(o.title, fig, { cls: o.cls || 'c6', sub: o.sub, end: o.end });
  }
  // Punkt und Zahl am Ende der Linien. Die Zahl steht in Schriftfarbe
  // (Kontrast wie Text), der Punkt in der Farbe der Reihe; liegen zwei
  // Enden dicht beieinander, rücken die Zahlen auseinander.
  function uwEnds(svg, ends, top, bottom) {
    const list = ends.filter(Boolean).sort((a, b) => a.y - b.y);
    // Erst nach unten auseinanderrücken, dann vom unteren Rand zurück nach
    // oben, damit sich am Rand nichts übereinanderschiebt (Idee 6).
    let prev = -Infinity;
    for (const e of list) {
      e.ty = Math.max(prev + 15, Math.max(top + 4, e.y + 4));
      prev = e.ty;
    }
    let next = bottom - 6 + 15;
    for (let i = list.length - 1; i >= 0; i--) {
      list[i].ty = Math.max(top + 4, Math.min(list[i].ty, next - 15));
      next = list[i].ty;
    }
    for (const e of list) {
      svg.append(svgEl('circle', { class: 'uw-d s' + e.slot, cx: e.x.toFixed(1), cy: e.y.toFixed(1), r: 4 }));
      const t = svgEl('text', { class: 'uw-end', x: (e.x + 9).toFixed(1), y: e.ty.toFixed(1) });
      t.textContent = e.text;
      svg.append(t);
    }
  }
  // Kleine Linie ohne Achsen für die Liste der Projekte.
  function uwSpark(v, h, rng, label) {
    const times = h.zeiten;
    const span = (RANGES.find(r => r[0] === rng) || RANGES[0])[2];
    const t1 = Math.max(Math.floor(Date.now() / 1000), times.length ? times[times.length - 1] : 0);
    const W = 120, H = 30;
    let peak = 0;
    for (const x of v) if (x != null && x > peak) peak = x;
    const ymax = niceMax(peak, 'pct');
    const svg = svgEl('svg', { class: 'uw-sp', viewBox: '0 0 ' + W + ' ' + H, preserveAspectRatio: 'none', role: 'img', 'aria-label': label });
    svg.append(svgEl('line', { class: 'uw-base', x1: 0, x2: W, y1: H - 1, y2: H - 1, 'vector-effect': 'non-scaling-stroke' }));
    svg.append(svgEl('path', { class: 'uw-l s1', 'vector-effect': 'non-scaling-stroke', d: uwPath(v, times, h.schritt, t => (t - t1 + span) / span * W, x => 2 + (1 - x / ymax) * (H - 4), h) }));
    return svg;
  }
  // Summe mehrerer Reihen je Zeitpunkt; fehlt überall ein Wert, bleibt er leer.
  function uwSum(list, n) {
    const out = new Array(n).fill(null);
    for (const v of list) if (v) v.forEach((x, i) => { if (x != null) out[i] = (out[i] || 0) + x; });
    return out;
  }
  // Projekte aus der Messung, dieselben Einträge wie unter „Projekte“
  // (Datenprüfung H1): Compose-Projekt, sonst der Container allein; jeder
  // Server, den ein Panel startet, einzeln mit seinem eigenen Verbrauch.
  // Zustand und Zahlen eines Projekts kommen vom Dienst
  // (grp), Prozessor als Anteil am ganzen Server.
  function uwGroups(r, h) {
    const ops = new Map((live() || []).map(c => [c.name, c]));
    // Namen verwalteter Server aus der Bestandsaufnahme, falls bekannt.
    const labels = new Map();
    for (const p of model().projects) for (const s of p.managed || []) if (s.label) labels.set(s.name, s.label);
    const v = r.werte || {};
    const n = h ? h.zeiten.length : 0;
    const ser = (name, k) => h ? h.serien['c/' + name + '/' + k] : null;
    const games = new Map(gameList(r).filter(s => s.container).map(s => [s.container, s]));
    const panelOf = panelName;
    // Was die Bestandsaufnahme noch nicht kennt, steht hier schon (es wird
    // gemessen), unter Projekte erst nach „Neu erfassen“; darum gekennzeichnet.
    const m = model();
    const knownP = new Set(m.projects.map(p => p.name)), knownC = new Set(m.containers.map(c => c.name));
    const gs = new Map();
    for (const c of r.info.container || []) {
      const o = ops.get(c.name);
      const proj = c.projekt || (o && o.projekt) || '';
      const by = o && o.verwaltet_von;
      const gm = games.get(c.name) || null;
      const cn = gm ? gName(gm) : labels.get(c.name) || shortName(c.name);
      const g = by ? { key: 'g:' + c.name, name: cn, kind: (gm ? gm.spiel + ' · Spielserver' : 'Verwalteter Server') + ' · gestartet vom Panel ' + panelOf(by), managed: true, single: true, panelProject: panelOf(by) }
        : proj ? { key: 'p:' + proj, name: proj, kind: 'Projekt', proj }
          : { key: 'c:' + c.name, name: shortName(c.name), kind: gm ? gm.spiel + ' · Spielserver' : 'Container ohne Projekt', single: true };
      if (!by && !(proj ? knownP.has(proj) : knownC.has(c.name))) { g.fresh = true; g.kind += ' · noch nicht erfasst'; }
      if (!gs.has(g.key)) gs.set(g.key, Object.assign(g, { cs: [], game: null }));
      const grpX = gs.get(g.key);
      if (gm && !grpX.game) grpX.game = gm;
      const ownNow = k => v['c/' + c.name + '/' + k] != null ? v['c/' + c.name + '/' + k] : !perContainer(v) && o && o.laeuft && o.cpu != null ? (k === 'cpu' ? o.cpu : o.speicher || 0) : undefined;
      grpX.cs.push({ c, o, name: by ? cn : labels.get(c.name) || shortName(c.name), game: gm, cpuNow: ownNow('cpu'), ramNow: ownNow('ram'), cpu: ser(c.name, 'cpu') || new Array(n).fill(null), ram: ser(c.name, 'ram') || new Array(n).fill(null) });
    }
    const nowSum = (cs, k) => cs.some(x => x[k] != null) ? cs.reduce((a, x) => a + (x[k] || 0), 0) : null;
    return [...gs.values()].map(g => {
      g.cpu = uwSum(g.cs.map(x => x.cpu), n); g.ram = uwSum(g.cs.map(x => x.ram), n);
      // Kennzahlen: je Container genau, je Gruppe aus der Projektsumme.
      const names = g.cs.map(x => x.c.name);
      for (const x of g.cs) { x.cpuSt = uwTotalStats(uwTotal(h, [x.c.name], 'cpu'), h); x.ramSt = uwTotalStats(uwTotal(h, [x.c.name], 'ram'), h); }
      const tc = uwTotal(h, names, 'cpu', g.proj), tr = uwTotal(h, names, 'ram', g.proj);
      g.cpuSt = tc.any ? uwTotalStats(tc, h) : null; g.ramSt = tr.any ? uwTotalStats(tr, h) : null;
      g.cpuNow = nowSum(g.cs, 'cpuNow'); g.ramNow = nowSum(g.cs, 'ramNow');
      g.grp = g.single ? null : grp(g.key);
      g.run = g.grp ? g.grp.laufen : g.cs.filter(x => x.c.zustand === 'running').length;
      return g;
    });
  }
  // Zustand eines Containers in Worten: das Wort des Dienstes, dazu der Grund
  // und die Neustarts.
  function uwState(x) {
    const o = x.o, c = x.c;
    const n = (o && o.neustarts) || c.neustarts || 0;
    const restarts = n ? plural(n, 'Neustart', 'Neustarts') : '';
    if (!o) return [c.zustand === 'running' ? 'läuft' : 'aus', restarts].filter(Boolean).join(' · ');
    return [o.wort, o.grund, restarts].filter(Boolean).join(' · ');
  }
  // Mittel im gewählten Zeitraum groß, jetzt und Spitze klein. Ohne Verlauf
  // je Container (etwa cgroup v1) der Wert jetzt.
  function uwShown(x, kind) {
    const s = kind === 'cpu' ? x.cpuSt : x.ramSt;
    return s ? s.avg : kind === 'cpu' ? x.cpuNow : x.ramNow;
  }
  function uwRow(x, h, rng, toggle) {
    const cs = h ? x.cpuSt : null;
    const rs = h ? x.ramSt : null;
    const upTo = st => st && st.up ? 'bis zu ' : '';
    const ram = v => v == null ? '–' : size(Math.round(v));
    const g = toggle ? x.grp : null;
    let name;
    if (toggle) {
      // Ein einzelner Container steht mit seinem Docker-Zustand da wie jeder
      // andere, auch ein Spielserver.
      const one = x.single && x.cs[0];
      const what = one ? [one.o ? st(one.o.stufe || 'idle', uwState(one)) : st(one.c.zustand === 'running' ? 'ok' : 'idle', uwState(one))] : g ? [liveStatus(g), g.gesamt > 1 ? ' · ' + g.text : ''] : [x.cs.length + ' Container'];
      const body = el('span', {}, el('b', { title: gFull(x.game) || gsTitle(x.game) || (one ? one.c.name : null) }, x.name, x.game ? gsTag() : null), el('small', {}, x.kind + ' · ', what));
      // Ein einzelner Container hat nichts aufzuklappen.
      name = one ? el('div', { class: 'uw-tg one' }, body)
        : el('button', { type: 'button', class: 'uw-tg', 'aria-expanded': String(toggle.open), 'data-fid': 'uw-g-' + x.key, onclick: toggle.on }, ic('right', 16), body);
    } else {
      const s = x.o ? st(x.o.stufe || 'idle', uwState(x)) : st(x.c.zustand === 'running' ? 'ok' : 'idle', uwState(x));
      if (x.o) s.dataset.zustand = x.o.wort;
      name = el('span', { class: 'uw-cn' }, el('b', { class: 'mono', title: x.c.name }, x.name), el('small', {}, s));
    }
    return el('li', { class: 'uw-row' + (toggle ? '' : ' sub') + (x.managed ? ' managed' : ''), 'data-gruppe': toggle ? x.key : null, 'data-projekt': toggle && x.key.startsWith('p:') ? x.name : null },
      el('div', { class: 'uw-n' }, name),
      h && h.zeiten.length ? uwSpark(x.cpu, h, rng, 'Verlauf Prozessor von ' + x.name) : el('span', { class: 'uw-sp' }),
      el('div', { class: 'uw-v', 'data-wert': uwShown(x, 'cpu') == null ? null : String(uwShown(x, 'cpu')) }, el('small', {}, cs ? 'Prozessor, Mittel' : 'Prozessor, jetzt'), el('b', {}, cpuText(uwShown(x, 'cpu'))),
        el('small', {}, cs ? (x.cpuNow != null ? 'jetzt ' + cpuText(x.cpuNow) + ' · ' : '') + 'Spitze ' + upTo(cs) + cpuText(cs.peak) : (toggle ? x.run : x.c.zustand === 'running') ? 'noch kein Verlauf' : 'lief in diesem Zeitraum nicht')),
      el('div', { class: 'uw-v' }, el('small', {}, rs ? 'Arbeitsspeicher, Mittel' : 'Arbeitsspeicher, jetzt'), el('b', {}, ram(uwShown(x, 'ram'))),
        el('small', {}, rs ? (x.ramNow != null ? 'jetzt ' + ram(x.ramNow) + ' · ' : '') + 'Spitze ' + upTo(rs) + ram(rs.peak) : '')));
  }
  function uwProjects(r, h, rng) {
    const gs = uwGroups(r, h);
    // Sortiert nach der groß gezeigten Zahl; verwaltete Server stehen
    // dazwischen, gekennzeichnet.
    const key = { cpu: g => -(uwShown(g, 'cpu') || 0), ram: g => -(uwShown(g, 'ram') || 0), name: () => 0 }[uw.sort];
    gs.sort((a, b) => key(a) - key(b) || a.name.localeCompare(b.name, 'de'));
    const chips = el('div', { class: 'ls-chips', role: 'group', 'aria-label': 'Sortieren nach' },
      [['cpu', 'Prozessor'], ['ram', 'Arbeitsspeicher'], ['name', 'Name']].map(([id, t]) => el('button', { type: 'button', class: 'ls-chip', 'data-fid': 'uw-s-' + id, 'aria-pressed': String(uw.sort === id), onclick: () => { uw.sort = id; core.rerender(); } }, t)));
    const items = [];
    for (const g of gs) {
      const open = !g.single && uw.open.has(g.key);
      items.push(uwRow(g, h, rng, { open, on: () => { if (open) uw.open.delete(g.key); else uw.open.add(g.key); core.rerender(); } }));
      if (open) for (const c of g.cs) items.push(uwRow(c, h, rng, null));
    }
    const by = { cpu: 'Prozessor', ram: 'Arbeitsspeicher', name: 'Name' }[uw.sort];
    return sec('Projekte', el('div', { class: 'ls-stack sm' },
      el('p', { class: 'note' }, 'Prozessor als Anteil am ganzen Server (alle ' + plural(r.info.kerne, 'Kern', 'Kerne') + ' = 100 %). Groß steht das Mittel im gewählten Zeitraum ' + RANGE_LONG[rng] + ', sortiert nach ' + by + '. Die Linie zeigt den Prozessor im Zeitraum; ein Klick auf ein Compose-Projekt zeigt seine Container.'),
      items.length ? el('ul', { class: 'uw-list' }, items) : el('p', { class: 'ls-mut' }, 'Keine Container gefunden.')),
    { cls: 'c12', end: chips });
  }
  function whenT(s) {
    const d = new Date(s);
    return isNaN(d) ? '' : el('time', { datetime: d.toISOString() }, when(s));
  }
  function uwInfo(r, h) {
    const first = h && h.zeiten.length ? h.zeiten[0] : 0;
    const k = r.kosten;
    return sec('Messung', el('ul', { class: 'ls-legend' },
      el('li', {}, ic('clock', 14), el('span', {}, el('b', {}, r.verlauf_seit ? ['Verlauf seit ', whenT(r.verlauf_seit)] : 'Noch kein Verlauf'),
        el('small', {}, 'Dienst läuft seit ', whenT(r.seit), '; misst alle ' + r.takt + ' Sekunden, letzter Wert ', whenT(r.stand_von))), null),
      el('li', {}, ic('db', 14), el('span', {}, el('b', {}, r.verlauf_platte > 0 ? 'Verlauf: ' + size(r.verlauf_platte) + ' auf der Platte' : 'Verlauf: noch nicht geschrieben'),
        el('small', {}, '1 Stunde in 10-Sekunden-Schritten, 2 Tage in Minuten, 30 Tage in 10 Minuten. ', first ? ['Dieser Zeitraum reicht zurück bis ', el('time', { datetime: new Date(first * 1000).toISOString() }, uwTime(first, '7d', true)), '.'] : '')), null),
      el('li', {}, ic('leistung', 14), el('span', {}, el('b', {}, 'HostDeck selbst: ' + (k.cpu_schnitt > 0 ? 'Mittel ' + cpuText(k.cpu_schnitt) : cpuText(k.cpu)) + ' des Servers, ' + size(k.ram) + ' Arbeitsspeicher'),
        el('small', {}, 'der ganze HostDeck-Dienst, nicht nur die Messung', k.cpu_schnitt > 0 ? ['; Mittel seit dem Start des Dienstes, in den letzten ' + r.takt + ' Sekunden ' + cpuText(k.cpu)] : '')), null),
      el('li', {}, ic('info', 14), el('span', {}, el('b', {}, 'Nur zum Anschauen'), el('small', {}, 'Die Überwachung ändert nichts am Server; sie zeigt und warnt nur.')), null)),
    { cls: 'c6' });
  }

  // ---------- Warnungen (4c) ----------
  const EV = {
    warnung: ['Warnung', 'warn'], dringend: ['Dringend', 'err'], erinnerung: ['Hält an', 'warn'], entwarnung: ['Entwarnung', 'ok'], hinweis: ['Hinweis', 'idle'],
  };
  function uwAlerts(w) {
    const run = w.aktiv.filter(a => !a.so_gelassen), kept = w.aktiv.filter(a => a.so_gelassen);
    const items = run.map(a => ({ k: a.gut_seit ? 'acc' : a.stufe, icon: a.gut_seit ? 'done' : a.symbol, t: a.titel,
      d: a.text + ' Seit ' + when(a.seit) + '.' + (a.gut_seit ? ' Seit ' + when(a.gut_seit) + ' wieder in Ordnung; die Entwarnung kommt nach 10 Minuten.' : ''),
      a: a.link ? link(a.link, a.knopf || 'Ansehen') : null, so: a.so_lassen && !a.gut_seit ? a.schluessel : '' }));
    // Bewusst so gelassen: grau, mit Auge-durchgestrichen und dem Wort, nie
    // nur über die Farbe; „Wieder warnen“ nimmt es zurück.
    const keptList = kept.length ? el('div', { class: 'uw-kept' }, el('h3', {}, ic('eyeoff', 16), 'Bewusst so gelassen'),
      el('ol', { class: 'ls-att' }, kept.map(a => el('li', { class: 'ls-att-i kept' },
        el('span', { class: 'ls-att-ic' }, ic(a.symbol, 18)),
        el('div', { class: 'ls-att-t' }, el('b', {}, a.titel), el('span', {}, 'Seit ' + when(a.seit) + ', so gelassen seit ' + when(a.so_gelassen) + '. Keine Erinnerung; wird es schlimmer oder tritt es nach dem Ende neu auf, warnt HostDeck wieder.')),
        el('div', { class: 'ls-att-a' }, el('button', { type: 'button', class: 'btn btn-sm btn-g', 'data-fid': 'unkeep-' + a.schluessel, disabled: !!hn.busy, 'aria-label': 'Wieder warnen: ' + a.titel,
          onclick: () => keepAlert(a.schluessel, false, a.titel) }, ic('undo', 16), 'Wieder warnen')))))) : null;
    if (!items.length) return sec('Warnungen', [el('div', { class: 'ls-empty ok' }, ic('done', 24), el('div', {}, el('b', {}, 'Alles ruhig.'),
      el('p', {}, kept.length ? 'Keine Regel schlägt an, die dich braucht.' : 'Keine Regel schlägt gerade an. Was HostDeck prüft und ab wann es warnt, steht unten unter Regeln.'))), keptList], { cls: 'c12 flush' });
    return sec('Warnungen', [el('ol', { class: 'ls-att' }, items.map(x => el('li', { class: 'ls-att-i ' + x.k },
      el('span', { class: 'ls-att-ic' }, ic(x.icon, 18)), el('div', { class: 'ls-att-t' }, el('b', {}, x.t), el('span', {}, x.d)), el('div', { class: 'ls-att-a' }, x.a, x.so ? keepBtn(x.so, x.t) : null)))), keptList],
    { cls: 'c12 flush', sub: plural(items.length, 'läuft', 'laufen') + '; stehen auch als Meldung auf der Übersicht und in der Glocke' });
  }
  // Seiten: jede Domain genau einmal, dieselbe Menge, dieselben Wörter und
  // Farben wie Lageplan und Domains: Zustand aus
  // /api/domains/zustand, Prüfwerte und Verfügbarkeit aus der Überwachung.
  function uwSites(w) {
    const zs = dz.list && dz.list.length ? dz.list : null;
    const byHost = new Map(w.seiten.map(sc => [sc.domain, sc]));
    const hosts = zs ? zs.map(z => z.domain) : w.seiten.map(sc => sc.domain);
    if (!hosts.length) return sec('Seiten', el('p', { class: 'ls-mut' }, w.seiten_geprueft ? 'Caddy kennt keine Domains, die HostDeck prüfen kann.' : 'Die erste Prüfung läuft; HostDeck prüft alle 5 Minuten jede Domain.'), { cls: 'c12' });
    const certTxt = sc => certCell(sc && sc.zertifikat_bis);
    const rank = { err: 0, warn: 1, idle: 2, ok: 3 };
    const zOfHost = h => zs ? zs.find(z => z.domain === h) : null;
    const order = hosts.slice().sort((a, b) => {
      const za = zOfHost(a), zb = zOfHost(b);
      return ((za ? rank[za.stufe] : 3) - (zb ? rank[zb.stufe] : 3)) || a.localeCompare(b);
    });
    const rows = order.map(h => {
      const z = zOfHost(h), sc = byHost.get(h);
      const word = z ? zSt(z) : sc ? (sc.pausiert ? st('idle', 'pausiert') : sc.ok ? st('ok', 'antwortet') : st('err', 'antwortet nicht')) : st('idle', '–');
      word.dataset.zustand = z ? z.zustand : '';
      const why = z && z.stufe !== 'ok' && z.text ? z.text : sc && !sc.ok && !sc.pausiert ? (sc.hinweis ? sc.hinweis + ' ' : '') + sc.antwort : '';
      return el('tr', { 'data-domain': h, 'data-zustand': z ? z.zustand : null },
        el('td', {}, h, z && z.leitstand ? el('span', { class: 'sub' }, 'dieser Leitstand') : null),
        el('td', {}, word, why ? el('span', { class: 'sub' }, why) : null),
        el('td', { class: 'uw-r', 'data-l': 'Antwort' }, sc && sc.ok && sc.dauer_ms ? sc.dauer_ms.toLocaleString('de-DE') + ' ms' : '–'),
        el('td', { 'data-l': 'Zertifikat' }, certTxt(sc)),
        el('td', { class: 'uw-up' }, sc ? upBars([h], { text: true }) : el('span', { class: 'ls-mut' }, z && z.zustand === 'aus' ? 'abgeschaltet, wird nicht geprüft' : 'noch nicht geprüft')));
    });
    return sec('Seiten', el('div', { class: 'ls-stack sm' },
      el('div', { class: 'ls-tw' }, el('table', { class: 'ls-t uw-t' },
        el('caption', { class: 'sr' }, 'Prüfung der Domains'),
        el('thead', {}, el('tr', {}, el('th', { scope: 'col' }, 'Domain'), el('th', { scope: 'col' }, 'Zustand'), el('th', { scope: 'col', class: 'uw-r' }, 'Antwort'), el('th', { scope: 'col' }, 'Zertifikat'), el('th', { scope: 'col' }, 'Verfügbarkeit'))),
        el('tbody', {}, rows))),
      el('p', { class: 'note' }, 'HostDeck ruft jede Domain alle 5 Minuten vom Server aus auf', w.seiten_geprueft ? [', zuletzt ', whenT(w.seiten_geprueft)] : '', '. Das erkennt, wenn Caddy oder ein Projekt nicht antwortet, aber keinen Fehler bei DNS oder beim Hoster. Verfügbarkeit gilt höchstens 30 Tage zurück; Zeiten, in denen du das Ziel gestoppt hast, zählen weder als Ausfall noch als Messzeit.')),
    { cls: 'c12', sub: plural(hosts.length, 'Domain', 'Domains') });
  }
  // Verlauf der Warnungen: Titel wie vom Dienst (Entwarnung „… antwortet
  // wieder“, V10). Was in eine Pause fällt, steht neutral da und zählt
  // nicht; stille Hinweise gingen nicht als Mitteilung hinaus.
  function uwEvents(w) {
    const list = w.verlauf.slice(0, 12);
    return sec('Verlauf der Warnungen', list.length ? el('ul', { class: 'uw-ev' }, list.map(e => {
      const kind = e.in_pause ? 'idle' : e.stufe === 'err' && e.art !== 'entwarnung' && e.art !== 'hinweis' ? 'err' : (EV[e.art] || ['', 'idle'])[1];
      const word = e.in_pause ? 'Pause' : (EV[e.art] || [e.art])[0];
      const note = [e.in_pause ? 'fiel in eine Pause, zählt nicht' : '', e.still ? 'ohne Mitteilung' : ''].filter(Boolean).join(' · ');
      return el('li', { 'data-art': e.art, 'data-pause': e.in_pause ? 'ja' : null },
        whenT(e.zeit), el('span', {}, st(kind, word)),
        el('span', { class: 't' }, e.titel.replace(/^Wieder in Ordnung: /, ''), note ? el('small', {}, note) : null));
    })) : el('p', { class: 'ls-mut' }, 'Noch keine Warnung. Hier steht später jede Warnung, Erinnerung und Entwarnung der letzten 30 Tage.'),
    { cls: 'c6', end: link('#/protokoll', 'Protokoll', 'btn btn-sm btn-g') });
  }
  // Änderbare Grenzen: Feld, Name, Einheit, Umrechnung fürs Formular.
  const LIM = [
    ['Platte', [['platte_gelb', 'gelb ab', '% belegt', 100], ['platte_rot', 'rot ab', '% belegt', 100], ['platte_min_frei', 'rot unter', 'GB frei', 1e-9]]],
    ['Arbeitsspeicher', [['speicher_gelb', 'gelb unter', '% frei', 100], ['speicher_rot', 'rot unter', '% frei', 100], ['speicher_minuten', 'wenn es so lange anhält', 'Minuten', 1]]],
    ['Prozessor', [['prozessor_gelb', 'gelb über', '%', 1], ['prozessor_minuten', 'wenn es so lange anhält', 'Minuten', 1]]],
    ['Steal', [['steal_gelb', 'gelb über', '%', 1], ['steal_minuten', 'wenn es so lange anhält', 'Minuten', 1]]],
    ['Seiten und Container', [['seite_fehlversuche', 'Seite rot nach', 'Prüfungen', 1], ['neustarts', 'Container rot bei', 'Neustarts pro Stunde', 1], ['unhealthy_minuten', '„unhealthy“ gelb nach', 'Minuten', 1]]],
    ['Zertifikat und Backup', [['zertifikat_gelb_tage', 'Zertifikat gelb', 'Tage vor Ablauf', 1], ['zertifikat_rot_tage', 'Zertifikat rot', 'Tage vor Ablauf', 1], ['backup_gelb_tage', 'Backup gelb nach', 'Tagen', 1], ['backup_rot_tage', 'Backup rot nach', 'Tagen', 1]]],
  ];
  const limShow = (v, f) => String(Math.round(v * f * 10) / 10).replace('.', ',');
  function uwLimits(w) {
    const g = w.grenzen, p = x => Math.round(x * 100) + ' %', m = x => plural(x, 'Minute', 'Minuten');
    const rows = [
      ['platz', 'Platte voll', 'gelb ab ' + p(g.platte_gelb) + ' belegt, rot ab ' + p(g.platte_rot) + (g.platte_min_frei > 0 ? ' oder unter ' + size(g.platte_min_frei) + ' frei (bei Platten ab ' + size(4 * g.platte_min_frei) + ')' : '')],
      ['leistung', 'Arbeitsspeicher knapp', m(g.speicher_minuten) + ' unter ' + p(g.speicher_gelb) + ' frei, rot unter ' + p(g.speicher_rot)],
      ['leistung', 'Prozessor dauerhaft voll', m(g.prozessor_minuten) + ' über ' + g.prozessor_gelb + ' %'],
      ['leistung', 'Steal hoch', m(g.steal_minuten) + ' über ' + g.steal_gelb + ' %'],
      ['unlink', 'Seite antwortet nicht', 'rot nach ' + g.seite_fehlversuche + ' Prüfungen hintereinander (' + m(g.seite_fehlversuche * 5) + ')'],
      ['projekte', 'Container', 'rot bei ' + g.neustarts + ' Neustarts in einer Stunde oder wenn er mit Fehler endet; gelb, wenn er ' + m(g.unhealthy_minuten) + ' „unhealthy“ ist'],
      ['domains', 'Zertifikat', 'gelb ' + g.zertifikat_gelb_tage + ' Tage vor Ablauf, rot ' + g.zertifikat_rot_tage + ' Tage vorher'],
      ['backup', 'Backup alt', 'gelb nach ' + g.backup_gelb_tage + ' Tagen, rot nach ' + g.backup_rot_tage + ' Tagen'],
    ];
    const body = nt.limOpen ? limForm(g) : el('ul', { class: 'ls-legend uw-rules' }, rows.map(([i, t, d]) => el('li', {}, ic(i, 14), el('span', {}, el('b', {}, t), el('small', {}, d)), null)));
    return sec('Regeln', el('div', { class: 'ls-stack sm' }, body,
      nt.limMsg ? el('div', { class: 'ls-note op-res ' + (nt.limMsg.ok ? 'is-ok' : 'is-err'), role: 'status' }, ic(nt.limMsg.ok ? 'done' : 'warn', 16), el('span', {}, nt.limMsg.text)) : null,
      nt.limOpen ? null : el('p', { class: 'note' }, 'Eine Warnung kommt einmal, nach 24 Stunden als Erinnerung und als Entwarnung, wenn es 10 Minuten lang wieder gut ist. Was du im Leitstand gestoppt hast, Einmal-Jobs und verwaltete Server (Container, die ein anderer Container anlegt) lösen nie eine aus; endet eine Warnung, weil du das Ziel stoppst, gibt es nur einen stillen Hinweis.')),
    { cls: 'c12', end: nt.limOpen ? null : el('button', { type: 'button', class: 'btn btn-sm btn-g', 'data-fid': 'uw-lim-edit', onclick: () => { nt.limOpen = true; nt.limMsg = null; nt.lim = null; core.rerender(); focusFid('uw-l-platte_gelb'); } }, ic('edit', 16), 'Grenzen ändern') });
  }
  function limForm(g) {
    if (!nt.lim) {
      nt.lim = {};
      LIM.forEach(([, fs]) => fs.forEach(([k, , , f]) => { nt.lim[k] = limShow(g[k], f); }));
    }
    const groups = LIM.map(([title, fs]) => el('fieldset', { class: 'uw-lim-g' }, el('legend', {}, title),
      fs.map(([k, label, unit]) => el('div', { class: 'uw-lim-f' }, el('label', { for: 'uw-l-' + k }, label),
        el('span', { class: 'uw-lim-in' }, el('input', { class: 'fld', id: 'uw-l-' + k, 'data-fid': 'uw-l-' + k, type: 'text', inputmode: 'decimal', autocomplete: 'off', value: nt.lim[k],
          oninput: e => { nt.lim[k] = e.target.value; } }), el('span', {}, unit))))));
    const save = async defaults => {
      let body;
      if (defaults) body = { voreinstellungen: true };
      else {
        const out = {};
        for (const [, fs] of LIM) for (const [k, label, , f] of fs) {
          const v = Number(String(nt.lim[k]).trim().replace(',', '.'));
          if (!isFinite(v) || String(nt.lim[k]).trim() === '') { nt.limMsg = { ok: false, text: '„' + label + '“ ist keine Zahl.' }; core.rerender(); focusFid('uw-l-' + k); return; }
          out[k] = f === 1 ? Math.round(v) : f === 100 ? Math.round(v) / 100 : Math.round(v * 1e9);
        }
        body = { grenzen: out };
      }
      nt.busy = 'lim'; core.rerender();
      const r = await core.call('/api/ueberwachung/grenzen', body);
      nt.busy = '';
      if (r.ok) {
        const ch = r.data.aenderungen || [];
        nt.limMsg = { ok: true, text: ch.length ? 'Übernommen und im Protokoll: ' + ch.join('; ') + '.' : 'Nichts geändert.' };
        nt.limOpen = false; nt.lim = null; mon.at = 0; loadMon();
      } else nt.limMsg = { ok: false, text: errText(r, 'Das Speichern') };
      core.rerender();
      focusFid(nt.limOpen ? 'uw-lim-save' : 'uw-lim-edit');
    };
    return el('form', { class: 'ls-stack sm', novalidate: true, onsubmit: e => { e.preventDefault(); save(false); } },
      el('p', { class: 'note' }, 'Gilt ab der nächsten Messung. Jede Änderung steht im Protokoll; ein Passwort braucht es dafür nicht.'),
      groups,
      el('div', { class: 'gt-acts' },
        el('button', { type: 'button', class: 'btn btn-g', onclick: () => { nt.limOpen = false; nt.lim = null; nt.limMsg = null; core.rerender(); focusFid('uw-lim-edit'); } }, 'Abbrechen'),
        el('button', { type: 'button', class: 'btn', disabled: nt.busy === 'lim', onclick: () => save(true) }, ic('undo', 16), 'Voreinstellungen'),
        el('button', { type: 'submit', class: 'btn btn-p', 'data-fid': 'uw-lim-save', disabled: nt.busy === 'lim' }, ic('check', 16), nt.busy === 'lim' ? 'Speichert …' : 'Übernehmen')));
  }

  // ---------- Mitteilungen (4d) ----------
  const nt = { rep: null, at: 0, loading: false, error: '', busy: '', msg: null, here: undefined, hereAt: 0, limOpen: false, lim: null, limMsg: null, beat: '', beatEdit: false, ntfy: { server: '', token: '' }, tests: null };
  const loadNt = () => loadLive(nt, '/api/ueberwachung/nachrichten', 'ueberwachung');
  const isIOS = () => /iPhone|iPad|iPod/.test(navigator.userAgent) || (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1);
  const isApp = () => (window.matchMedia && matchMedia('(display-mode: standalone)').matches) || navigator.standalone === true;
  const canPush = () => 'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window;
  function deviceName() {
    const u = navigator.userAgent;
    const dev = /iPhone/.test(u) ? 'iPhone' : isIOS() ? 'iPad' : /Android/.test(u) ? 'Android' : /Mac OS X/.test(u) ? 'Mac' : /Windows/.test(u) ? 'Windows-PC' : /Linux/.test(u) ? 'Linux' : 'Gerät';
    const br = isApp() ? 'Home-Bildschirm' : /Edg\//.test(u) ? 'Edge' : /Firefox\//.test(u) ? 'Firefox' : /Chrome\//.test(u) ? 'Chrome' : /Safari\//.test(u) ? 'Safari' : '';
    return dev + (br ? ' (' + br + ')' : '');
  }
  function b64Bytes(s) {
    const b = atob(s.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - s.length % 4) % 4));
    return Uint8Array.from(b, c => c.charCodeAt(0));
  }
  async function endpointID(ep) {
    const h = new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(ep)));
    return Array.from(h.slice(0, 6), x => x.toString(16).padStart(2, '0')).join('');
  }
  // Welches eingetragene Gerät dieses hier ist (über die Push-Adresse).
  async function findHere() {
    nt.hereAt = Date.now();
    let id = null;
    try {
      if (canPush() && Notification.permission === 'granted') {
        const reg = await navigator.serviceWorker.getRegistration('/');
        const sub = reg && await reg.pushManager.getSubscription();
        if (sub) id = await endpointID(sub.endpoint);
      }
    } catch (e) { id = null; }
    if (nt.here !== id) { nt.here = id; if (ui.page === 'ueberwachung' && core.work() && !modalOpen()) core.rerender(); }
  }
  async function ntAllow() {
    if (nt.busy) return;
    nt.busy = 'allow'; nt.msg = null;
    core.rerender();
    try {
      const perm = await Notification.requestPermission();
      if (perm !== 'granted') {
        nt.msg = { ok: false, text: perm === 'denied' ? 'Mitteilungen sind für HostDeck abgelehnt. Erlauben kannst du sie in den Einstellungen des Geräts (iPhone: Einstellungen › Mitteilungen › HostDeck).' : 'Mitteilungen wurden nicht erlaubt.' };
      } else {
        const reg = await navigator.serviceWorker.register('/sw.js', { scope: '/' });
        await navigator.serviceWorker.ready;
        const key = b64Bytes(nt.rep.web_push.schluessel);
        let sub = await reg.pushManager.getSubscription();
        const old = sub && sub.options && sub.options.applicationServerKey;
        if (sub && old && btoa(String.fromCharCode(...new Uint8Array(old))) !== btoa(String.fromCharCode(...key))) { await sub.unsubscribe(); sub = null; }
        if (!sub) sub = await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key });
        const j = sub.toJSON();
        const r = await core.call('/api/ueberwachung/nachrichten/geraet', { abo: { endpoint: j.endpoint, keys: { p256dh: j.keys.p256dh, auth: j.keys.auth } }, name: deviceName() });
        if (r.ok) { nt.rep = r.data.stand; nt.here = r.data.id; nt.msg = { ok: true, text: 'Dieses Gerät bekommt jetzt Mitteilungen. Schick dir zum Test eine Probe.' }; core.announce(nt.msg.text); }
        else if (!(r.data && r.data.abgebrochen)) nt.msg = { ok: false, text: errText(r, 'Das Eintragen') };
      }
    } catch (e) {
      nt.msg = { ok: false, text: 'Das ging nicht: ' + (e && e.message ? e.message : e) };
    }
    nt.busy = '';
    core.rerender();
    focusFid(nt.here ? 'nt-test-here' : 'nt-allow');
  }
  async function ntCall(path, body, what, done) {
    if (nt.busy) return;
    nt.busy = path; nt.msg = null;
    core.rerender();
    const r = await core.call(path, body);
    nt.busy = '';
    if (r.ok) { nt.rep = r.data.stand || r.data; nt.at = Date.now(); if (done) nt.msg = { ok: true, text: done }; if (done) core.announce(done); }
    else if (!(r.data && r.data.abgebrochen)) nt.msg = { ok: false, text: errText(r, what) };
    core.rerender();
    return r.ok;
  }
  async function ntRemove(d) {
    const here = d.id === nt.here;
    if (!(await ntCall('/api/ueberwachung/nachrichten/geraet/entfernen', { id: d.id }, 'Das Austragen', '„' + d.name + '“ bekommt keine Mitteilungen mehr.'))) return;
    if (here) {
      try { const reg = await navigator.serviceWorker.getRegistration('/'); const sub = reg && await reg.pushManager.getSubscription(); if (sub) await sub.unsubscribe(); } catch (e) { /* bleibt beim Browser */ }
      nt.here = null;
      core.rerender();
    }
    focusFid('nt-test');
  }
  async function ntTest(id) {
    if (nt.busy) return;
    nt.busy = 'test'; nt.msg = null; nt.tests = null;
    core.rerender();
    const r = await core.call('/api/ueberwachung/nachrichten/probe', id ? { geraet: id } : {});
    nt.busy = '';
    if (r.ok) {
      nt.rep = r.data.stand; nt.tests = r.data.ergebnisse;
      const bad = nt.tests.filter(x => !x.ok);
      nt.msg = bad.length ? { ok: false, text: 'Nicht zugestellt an ' + bad.map(x => x.ziel + ' (' + x.fehler + ')').join(', ') + '.' }
        : { ok: true, text: 'Probe verschickt an ' + nt.tests.map(x => x.ziel).join(', ') + '. Sie sollte in ein paar Sekunden ankommen.' };
      core.announce(nt.msg.text);
    } else nt.msg = { ok: false, text: errText(r, 'Die Probe') };
    core.rerender();
  }
  function ntHere(p) {
    if (!p.moeglich) return el('div', { class: 'ls-note' }, ic('info', 16), el('span', {}, p.grund, ' ', link('#/zugang', 'Zu „Von außen“')));
    if (location.protocol !== 'https:') return el('div', { class: 'ls-note' }, ic('info', 16), el('span', {}, 'Mitteilungen richtest du über die freigeschaltete Adresse ein, nicht über den Notausgang. Öffne dafür den Leitstand mit https auf dem Gerät, das sie bekommen soll.'));
    if (isIOS() && !isApp()) return el('div', { class: 'ls-stack sm' },
      el('p', {}, 'Auf dem iPhone und iPad gehen Mitteilungen, wenn der Leitstand auf dem Home-Bildschirm liegt (ab iOS 16.4):'),
      el('ol', { class: 'ls-steps' },
        el('li', {}, el('b', {}, 'Teilen'), el('span', {}, 'Unten in Safari auf das Teilen-Symbol (Quadrat mit Pfeil) tippen.')),
        el('li', {}, el('b', {}, '„Zum Home-Bildschirm“'), el('span', {}, 'auswählen und mit „Hinzufügen“ bestätigen.')),
        el('li', {}, el('b', {}, 'HostDeck öffnen'), el('span', {}, 'vom Home-Bildschirm aus, anmelden und hier „Mitteilungen erlauben“ tippen.'))));
    if (!canPush()) return el('div', { class: 'ls-note' }, ic('info', 16), el('span', {}, 'Dieser Browser kann keine Mitteilungen von Webseiten empfangen. Nimm einen aktuellen Browser oder unten ntfy.'));
    if (Notification.permission === 'denied') return el('div', { class: 'ls-note' }, ic('warn', 16), el('span', {}, 'Mitteilungen sind für HostDeck auf diesem Gerät abgelehnt. Erlauben kannst du sie in den Einstellungen des Geräts' + (isIOS() ? ' (Einstellungen › Mitteilungen › HostDeck)' : ' oder des Browsers (Schloss-Symbol neben der Adresse)') + '.'));
    if (nt.here && p.geraete.some(d => d.id === nt.here)) return el('div', { class: 'uw-nt-here ok' }, ic('done', 20),
      el('span', {}, el('b', {}, 'Dieses Gerät bekommt Mitteilungen.'), el('small', {}, 'Warnung, Dringend, Erinnerung und Entwarnung kommen hierher.')),
      el('button', { type: 'button', class: 'btn btn-sm', 'data-fid': 'nt-test-here', disabled: !!nt.busy, onclick: () => ntTest(nt.here) }, ic('bell', 16), nt.busy === 'test' ? 'Schickt …' : 'Probe hierher'));
    return el('div', { class: 'uw-nt-here' }, ic('phone', 20),
      el('span', {}, el('b', {}, 'Dieses Gerät bekommt noch keine Mitteilungen.'), el('small', {}, 'Du bestätigst einmal mit deinem Passwort; danach kommt jede Warnung auch hierher.')),
      el('button', { type: 'button', class: 'btn btn-p', 'data-fid': 'nt-allow', disabled: !!nt.busy, onclick: ntAllow }, ic('bell', 16), nt.busy === 'allow' ? 'Richtet ein …' : 'Mitteilungen erlauben'));
  }
  function ntDevices(p) {
    if (!p.geraete.length) return null;
    return el('ul', { class: 'uw-nt-dev' }, p.geraete.map(d => el('li', {},
      ic('phone', 16),
      el('span', {}, el('b', {}, d.name, d.id === nt.here ? el('span', { class: 'sub' }, ' · dieses Gerät') : null),
        el('small', {}, 'über ' + d.dienst + ', seit ' + when(d.seit) + (d.zuletzt_ok ? ', zuletzt zugestellt ' + when(d.zuletzt_ok) : '')),
        d.fehler ? el('small', { class: 'uw-nt-err' }, ic('warn', 14), d.fehler) : null),
      el('button', { type: 'button', class: 'btn btn-sm btn-g', disabled: !!nt.busy, 'aria-label': '„' + d.name + '“ austragen', onclick: () => ntRemove(d) }, 'Austragen'))));
  }
  function ntBeat(b) {
    const set = !!b;
    const form = el('form', { class: 'zg-row', novalidate: true, onsubmit: async e => {
      e.preventDefault();
      if (await ntCall('/api/ueberwachung/nachrichten/herzschlag', { adresse: nt.beat }, 'Das Einrichten', 'Herzschlag eingerichtet; der erste geht gleich raus.')) { nt.beat = ''; nt.beatEdit = false; }
      core.rerender();
    } },
    el('input', { class: 'fld mono', id: 'nt-beat', 'data-fid': 'nt-beat', type: 'text', inputmode: 'url', autocomplete: 'off', spellcheck: 'false', autocapitalize: 'off', placeholder: 'https://hc-ping.com/…', value: nt.beat, 'aria-label': 'Ping-Adresse für den Herzschlag', oninput: e => { nt.beat = e.target.value; } }),
    el('button', { type: 'submit', class: 'btn btn-p', disabled: !!nt.busy }, set ? 'Übernehmen' : 'Einrichten'));
    return el('div', { class: 'ls-stack sm' },
      el('h3', { class: 'uw-nt-h' }, 'Herzschlag', set ? (b.fehler ? st('err', 'kommt nicht an') : b.zuletzt_ok ? st('ok', 'kommt an') : st('idle', 'wartet')) : st('idle', 'aus')),
      el('p', { class: 'note' }, 'Fällt der ganze Server aus, kann HostDeck nichts mehr schicken. Mit dem Herzschlag meldet sich HostDeck alle ' + (b ? b.abstand_minuten : 5) + ' Minuten bei einem fremden Dienst wie healthchecks.io; bleibt die Meldung aus, schreibt dir der Dienst eine E-Mail. Ohne Adresse geht nichts nach draußen.'),
      set ? el('p', {}, el('span', { class: 'mono' }, b.adresse), b.zuletzt_ok ? ' · zuletzt angekommen ' + when(b.zuletzt_ok) : '', b.fehler ? el('span', { class: 'uw-nt-err' }, ic('warn', 14), b.fehler) : null) : null,
      set ? null : el('details', { class: 'gt-help' }, el('summary', {}, 'So bekommst du die Ping-Adresse'),
        el('ol', { class: 'ls-steps' },
          el('li', {}, el('b', {}, 'Konto anlegen'), el('span', {}, 'auf healthchecks.io (kostenlos, mit deiner E-Mail-Adresse).')),
          el('li', {}, el('b', {}, 'Check anlegen'), el('span', {}, 'Name „HostDeck“, Period 5 Minuten, Grace 10 Minuten.')),
          el('li', {}, el('b', {}, 'Ping-Adresse kopieren'), el('span', {}, 'beginnt mit https://hc-ping.com/ und hier einfügen. Die E-Mail an dich ist dort schon eingestellt.')))),
      !set || nt.beatEdit ? form : null,
      set ? el('div', { class: 'gt-acts' },
        el('button', { type: 'button', class: 'btn btn-sm btn-g', 'data-fid': 'nt-beat-edit', disabled: !!nt.busy, onclick: () => { nt.beatEdit = !nt.beatEdit; core.rerender(); if (nt.beatEdit) focusFid('nt-beat'); } }, nt.beatEdit ? 'Abbrechen' : 'Adresse ändern'),
        el('button', { type: 'button', class: 'btn btn-sm btn-g', disabled: !!nt.busy, onclick: () => ntCall('/api/ueberwachung/nachrichten/aus', { kanal: 'herzschlag' }, 'Das Abschalten', 'Herzschlag abgeschaltet.') }, 'Herzschlag abschalten')) : null);
  }
  function ntNtfy(n) {
    const f = nt.ntfy;
    const setup = el('form', { class: 'ls-stack sm', novalidate: true, onsubmit: e => { e.preventDefault(); ntCall('/api/ueberwachung/nachrichten/ntfy', { server: f.server, token: f.token, neues_thema: false }, 'Das Einrichten', 'ntfy eingerichtet. Abonniere jetzt das Thema in der ntfy-App.'); } },
      el('div', { class: 'gt-fld' }, el('label', { for: 'nt-srv' }, 'ntfy-Server'),
        el('input', { class: 'fld mono', id: 'nt-srv', type: 'text', inputmode: 'url', autocomplete: 'off', spellcheck: 'false', autocapitalize: 'off', placeholder: 'https://ntfy.sh', value: f.server, oninput: e => { f.server = e.target.value; } }),
        el('p', { class: 'zg-help' }, 'Leer lassen für den öffentlichen Dienst ntfy.sh.')),
      el('div', { class: 'gt-fld' }, el('label', { for: 'nt-tok' }, 'Token (nur für einen eigenen Server mit Anmeldung)'),
        el('input', { class: 'fld mono', id: 'nt-tok', type: 'password', autocomplete: 'off', 'data-lpignore': 'true', value: f.token, oninput: e => { f.token = e.target.value; } })),
      el('div', { class: 'gt-acts' }, el('button', { type: 'submit', class: 'btn', disabled: !!nt.busy }, 'ntfy einrichten')));
    return el('div', { class: 'ls-stack sm' },
        el('h3', { class: 'uw-nt-h' }, 'ntfy-App', n ? st('ok', 'eingerichtet') : st('idle', 'aus')),
        el('p', { class: 'note' }, 'ntfy ist eine kostenlose App für Android und iPhone. Sie geht auch ohne freigeschaltete Adresse. Wer das lange Thema kennt, kann beim öffentlichen Dienst mitlesen; die Nachrichten enthalten nur den Anlass.'),
        n ? el('div', { class: 'ls-stack sm' },
          el('p', {}, 'Thema ', el('b', { class: 'mono' }, n.thema), ' auf ', el('span', { class: 'mono' }, n.host), n.mit_token ? ' (mit Token)' : ''),
          el('div', { class: 'gt-acts' },
            el('a', { class: 'btn btn-sm', href: n.app }, 'In der ntfy-App öffnen'),
            el('a', { class: 'btn btn-sm btn-g', href: n.web, target: '_blank', rel: 'noopener' }, 'Im Browser'),
            el('button', { type: 'button', class: 'btn btn-sm btn-g', disabled: !!nt.busy, onclick: () => ntCall('/api/ueberwachung/nachrichten/ntfy', { server: n.server, token: '', neues_thema: true }, 'Das Ändern', 'Neues Thema angelegt; das alte bekommt nichts mehr.') }, 'Neues Thema'),
            el('button', { type: 'button', class: 'btn btn-sm btn-g', disabled: !!nt.busy, onclick: () => ntCall('/api/ueberwachung/nachrichten/aus', { kanal: 'ntfy' }, 'Das Abschalten', 'ntfy abgeschaltet.') }, 'ntfy abschalten'))) : setup);
  }
  // Noch kein Gerät für Mitteilungen: kurzer Hinweis mit Weg in die
  // Einstellungen, wo sie eingerichtet werden.
  function uwNotifyHint() {
    return sec(null, el('div', { class: 'ls-hintrow' }, el('span', {}, ic('bell', 16), el('span', {}, 'Warnungen kommen noch nicht aufs Handy.')),
      el('a', { class: 'btn btn-sm', href: '#/einstellungen/mitteilungen' }, 'Mitteilungen einrichten')), { cls: 'c12' });
  }
  function uwNotify() {
    if (!nt.loading && (!nt.at || Date.now() - nt.at > 30000)) setTimeout(loadNt, 0);
    const r = nt.rep;
    if (!r) return sec('Mitteilungen', nt.error ? el('p', { class: 'band b-err', role: 'status' }, 'Nicht erreichbar: ' + nt.error) : el('span', { class: 'skel skel-t skel-w' }), { cls: 'c12' });
    if (nt.here === undefined || Date.now() - nt.hereAt > 60000) { nt.here = nt.here === undefined ? null : nt.here; setTimeout(findHere, 0); }
    const p = r.web_push;
    const any = p.geraete.length || r.ntfy;
    const last = r.zuletzt;
    return sec('Mitteilungen', el('div', { class: 'ls-stack' },
      ntHere(p),
      nt.msg ? el('div', { class: 'ls-note op-res ' + (nt.msg.ok ? 'is-ok' : 'is-err'), role: 'status' }, ic(nt.msg.ok ? 'done' : 'warn', 16), el('span', {}, nt.msg.text),
        el('button', { type: 'button', class: 'ls-ibtn op-x', 'aria-label': 'Meldung schließen', onclick: () => { nt.msg = null; core.rerender(); } }, ic('x', 16))) : null,
      ntDevices(p),
      any ? el('div', { class: 'ls-row-end' },
        el('span', { class: 'ls-mut' }, last ? 'Zuletzt: „' + last.titel + '“, ' + when(last.zeit) + (last.ok ? ', zugestellt' : ', nicht zugestellt: ' + last.fehler) : 'Noch nichts verschickt.', r.ausstehend ? ' · ' + plural(r.ausstehend, 'Nachricht wartet', 'Nachrichten warten') + ' auf einen neuen Versuch' : ''),
        el('button', { type: 'button', class: 'btn btn-sm', 'data-fid': 'nt-test', disabled: !!nt.busy, onclick: () => ntTest('') }, ic('bell', 16), nt.busy === 'test' ? 'Schickt …' : 'Probe an alle')) : null,
      el('details', { class: 'gt-help', open: !!(r.herzschlag || r.ntfy) }, el('summary', {}, 'Weitere Wege (freiwillig): ntfy-App, Herzschlag nach außen'),
        el('div', { class: 'ls-stack sm' }, ntNtfy(r.ntfy), ntBeat(r.herzschlag)))),
    { cls: 'c12', sub: any ? plural(p.geraete.length + (r.ntfy ? 1 : 0), 'Empfänger', 'Empfänger') : 'aufs Handy, bei jeder Warnung und Entwarnung' });
  }
  // #/ueberwachung/<zeitraum> öffnet diesen Zeitraum (V20: der Link aus
  // dem 24-Std-Diagramm der Übersicht öffnet 24 Std).
  // ---------- Spielserver ----------
  // Erkannt vom Dienst (Paket spiele): am Image oder an den Dateien. HostDeck
  // fragt das Spiel nicht ab; Zustand und Verbrauch kommen wie bei jedem
  // Container von Docker. Nur Anzeige; gesteuert wird im Panel. „Spielserver“
  // heißt nur, was erkannt ist; alles andere bleibt ein verwalteter Server.
  function gameList(r) { return (r && r.spielserver) || []; }
  function gsTag() { return el('span', { class: 'tag t-sug gs-tag' }, 'Spiel'); }
  // Ein gekürzter Name (erste 8 Zeichen einer UUID) hat den vollen Wert im Titel.
  function gsTitle(gm) { return gm && gm.id !== gm.name && gm.id.startsWith(gm.name) ? gm.id : null; }
  // Kurzer, lesbarer Name für Kacheln und Listen. Oft ist der Name die
  // Begrüßung im Spiel (MOTD) mit Zierzeichen; dann der erste lesbare Teil,
  // gekürzt. Eine bloße Kennung heißt „Spielserver 8834a251“.
  function gName(gm) {
    // Farbcodes aus Minecraft (§a …) gehören nicht zum Namen.
    const raw = (gm.name || '').replace(/§[0-9a-fk-or]/gi, '').trim();
    if (!raw || gsTitle(gm)) return 'Spielserver ' + (raw || gm.id.slice(0, 8));
    const parts = raw.split(/[^\p{L}\p{N}\s.,:;!?'’&()+-]+/u).map(x => x.replace(/\s+/g, ' ').trim()).filter(x => (x.match(/\p{L}/gu) || []).length >= 2);
    let t = parts[0] || raw;
    if (t.length > 28) { const cut = t.slice(0, 28); t = cut.slice(0, Math.max(cut.lastIndexOf(' '), 12)).replace(/[\s.,:;-]+$/, '') + ' …'; }
    return t;
  }
  // Der volle Name im Titel, wenn die Kachel ihn kürzt.
  function gFull(gm) { return gm && gName(gm) !== gm.name ? gm.name + (gm.id !== gm.name ? ' · ' + gm.id : '') : null; }
  // ---------- Linien aus Stücken ----------
  function unitSplit(t) { const m = /^(.*)[\s ](\S+)$/.exec(t); return m ? [m[1], m[2]] : [t, '']; }
  // Stücke einer Linie: neu ansetzen, wo ein Wert fehlt oder der Abstand
  // größer ist als gap(t) des Punkts davor.
  function segs(pts, X, Y, gap) {
    const out = [];
    let cur = null, prev = 0;
    for (const [t, x] of pts) {
      if (x == null) { cur = null; continue; }
      if (!cur || t - prev > gap(prev)) { cur = []; out.push(cur); }
      cur.push([X(t), Y(x)]);
      prev = t;
    }
    return out;
  }
  const xy = p => p[0].toFixed(1) + ' ' + p[1].toFixed(1);
  function segLine(ss) { return ss.map(s => 'M' + s.map(xy).join('L') + (s.length === 1 ? 'h0.01' : '')).join(' '); }
  function segArea(ss, y0) { return ss.filter(s => s.length > 1).map(s => 'M' + s[0][0].toFixed(1) + ' ' + y0 + 'L' + s.map(xy).join('L') + 'L' + s[s.length - 1][0].toFixed(1) + ' ' + y0 + 'Z').join(''); }

  // ---------- Live ----------
  // Jede Sekunde ein Wert, nur solange jemand zuschaut: Der Dienst misst
  // schnell, solange die Seite fragt; die Seite fragt, solange Live offen,
  // sichtbar und nicht angehalten ist. Die Live-Teile bleiben dieselben
  // Knoten und ändern sich an Ort und Stelle (Text, Pfade, Balken); der Rest
  // der Seite wird im alten Takt neu gezeichnet. Die Maus hält nichts
  // an; dafür gibt es „Anhalten“.
  const LV_WIN = 300;
  const LV_SERIES = 's/,d/,p/*/cpu,p/*/ram,c/*/cpu,c/*/ram';
  const lv = { frames: [], fine: 0, takt: 1, got: 0, err: '', paused: 0, timer: 0, busy: false, started: 0, ui: null, said: '', open: new Set(), order: 0, orderKeys: null };
  // Jetzt auf der Uhr des Servers: die jüngste Messung plus die Zeit seit
  // ihrer Ankunft (die Uhren von Browser und Server dürfen abweichen).
  function lvNow() {
    const f = lv.frames[lv.frames.length - 1];
    const at = lv.paused || Date.now();
    return f ? f.t / 1000 + Math.max(0, (at - lv.got) / 1000) : at / 1000;
  }
  // Lücke: mehr als 2,5 Takte zwischen zwei Messungen; vor der ersten
  // schnellen Messung kommen die Werte aus dem Verlauf (alle 10 s).
  function lvGap(t) { return 2.5 * (lv.fine && t * 1000 >= lv.fine ? lv.takt : 10); }
  function lvLast() { return lv.frames.length ? lv.frames[lv.frames.length - 1].v : (mon.rep && mon.rep.werte) || {}; }
  function lvSer(win) {
    return f => {
      const t1 = lvNow(), t0 = t1 - win;
      return { pts: lv.frames.filter(x => x.t / 1000 >= t0 - 15).map(x => [x.t / 1000, f(k => x.v[k])]), t0, t1, gap: lvGap };
    };
  }
  function lvStart() {
    if (!lv.started) lv.started = Date.now();
    if (!lv.timer) { lvFetch(); lvTick(); }
  }
  function lvStop() {
    clearTimeout(lv.timer);
    Object.assign(lv, { timer: 0, started: 0, err: '', paused: 0, said: '' });
  }
  function lvTick() {
    clearTimeout(lv.timer);
    lv.timer = setTimeout(() => {
      lv.timer = 0;
      if (ui.page !== 'ueberwachung' || uw.range !== 'live' || !core.svc()) { lvStop(); return; }
      if (!lv.paused && document.visibilityState === 'visible') { lvFetch(); lvPaintState(); }
      lvTick();
    }, 1000);
  }
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible' && lv.timer && !lv.paused) lvFetch();
  });
  async function lvFetch() {
    if (lv.busy) return;
    lv.busy = true;
    const last = lv.frames.length ? lv.frames[lv.frames.length - 1].t : 0;
    const r = await core.call('/api/ueberwachung/live?seit=' + last + '&serien=' + encodeURIComponent(LV_SERIES), null, true);
    lv.busy = false;
    if (uw.range !== 'live') return;
    if (!r.ok) { lv.err = (r.data && r.data.error) || 'Fehler ' + r.status; lvPaintState(); return; }
    const d = r.data || {};
    lv.err = '';
    lv.takt = d.takt || 1;
    lv.fine = d.fein_ab || 0;
    const fr = (d.messungen || []).filter(f => f.t > last);
    if (fr.length) {
      lv.frames.push(...fr);
      lv.got = Date.now();
      const cut = lv.frames[lv.frames.length - 1].t - (LV_WIN + 20) * 1000;
      let i = 0;
      while (i < lv.frames.length && lv.frames[i].t < cut) i++;
      if (i) lv.frames.splice(0, i);
    }
    if (!lv.paused) lvPaint();
  }
  function lvPause() {
    lv.paused = lv.paused ? 0 : Date.now();
    if (!lv.paused) lvFetch();
    lvPaint();
  }
  // Zustand neben dem Titel: Wort, Klasse und Alter der jüngsten Messung.
  function lvState() {
    if (lv.paused) return ['paused', 'Angehalten', 'um ' + new Date(lv.paused).toLocaleTimeString('de-DE')];
    if (lv.err) return ['stale', 'keine Verbindung', 'versucht erneut'];
    const f = lv.frames[lv.frames.length - 1];
    const fine = f && lv.fine && f.t >= lv.fine;
    if (!fine) return Date.now() - lv.started > 5000 ? ['stale', 'verzögert', f ? 'Werte alle 10 s' : 'noch kein Wert'] : ['start', 'Live', 'startet …'];
    const age = Math.max(1, Math.round((Date.now() - lv.got) / 1000 + 0.5));
    if (age > 3 * lv.takt) return ['stale', 'verzögert', 'vor ' + age + ' s'];
    return ['on', 'Live', 'vor ' + age + ' s'];
  }
  function lvPaintState() {
    const u = lv.ui;
    if (!u) return;
    const [k, w, a] = lvState();
    u.pill.className = 'lv-state ' + k;
    u.pillW.textContent = w;
    u.pillA.textContent = a;
    u.pill.dataset.zustand = k;
    // Angesagt wird nur ein Wechsel des Zustands, nie das Alter (4.4).
    const say = k === 'start' ? '' : k === 'paused' ? 'Angehalten ' + a : k === 'on' ? 'Live' : w;
    if (say !== lv.said) { lv.said = say; u.sr.textContent = say; }
    u.pause.setAttribute('aria-pressed', String(!!lv.paused));
    u.pause.replaceChildren(ic(lv.paused ? 'play' : 'pause', 15), lv.paused ? 'Weiter' : 'Anhalten');
  }
  function lvBuild() {
    const pillW = el('span', { class: 'w' }), pillA = el('small');
    const u = {
      pill: el('span', { class: 'lv-state start', 'aria-hidden': 'true' }, el('i'), pillW, pillA), pillW, pillA,
      sr: el('span', { class: 'sr', role: 'status' }),
      hd: el('span', { class: 'lv-hd' }),
      pause: el('button', { type: 'button', class: 'btn btn-sm', 'data-fid': 'lv-pause', 'aria-pressed': 'false', onclick: lvPause }),
      g: {}, gauges: el('div', { class: 'ga-row', role: 'group', 'aria-label': 'Werte jetzt' }),
      rows: el('ul', { class: 'lv-list' }), rowMap: new Map(),
    };
    for (const [k, t] of GAUGES) { u.g[k] = uwGauge(k, t); u.gauges.append(u.g[k].node); }
    u.who = uwWho(() => lvPaint());
    const r = () => mon.rep || { info: {} };
    u.charts = [
      lvChart({ id: 'cpu', hint: true, title: 'Prozessor', sub: () => 'letzte 5 Minuten, jede Sekunde · alle Kerne = 100 %', unit: 'pct', series: [{ label: 'ausgelastet', k: 's/cpu', slot: 1 }, { label: 'Steal', k: 's/steal', slot: 2 }, { label: 'wartet auf Platte', k: 's/iowait', slot: 3 }] }),
      lvChart({ id: 'ram', title: 'Arbeitsspeicher', sub: () => 'letzte 5 Minuten' + (r().info.ram_gesamt ? ', bis ' + size(r().info.ram_gesamt) : ''), unit: 'bytes', max: () => r().info.ram_gesamt || null, series: [{ label: 'belegt', k: 's/ram', slot: 2 }] }),
      lvChart({ id: 'net', title: 'Netz', height: 120, sub: () => 'letzte 5 Minuten', unit: 'rate', series: [{ label: 'rein', k: 's/ein', slot: 1 }, { label: 'raus', k: 's/aus', slot: 2, dash: true }] }),
      lvChart({ id: 'io', title: 'Platte lesen und schreiben', height: 120, sub: () => 'letzte 5 Minuten', unit: 'rate', series: [{ label: 'lesen', k: 's/lesen', slot: 1 }, { label: 'schreiben', k: 's/schreiben', slot: 2, dash: true }] }),
    ];
    for (const c of u.charts) c.node.classList.replace('c6', 'c12');
    u.active = sec('Gerade aktiv', el('div', { class: 'ls-stack sm' },
      el('p', { class: 'note pad' }, 'Projekte und Spielserver, die gerade laufen, nach Prozessor jetzt; Prozessor als Anteil am ganzen Server. Ein Klick auf ein Projekt zeigt seine Container.'),
      u.nocg = el('p', { class: 'note pad', hidden: true }, NO_CG),
      u.rows), { cls: 'c12 flush', sub: 'jede Sekunde' });
    u.active.id = 'lv-aktiv';
    lv.ui = u;
    lvPaintState();
    return u;
  }
  function lvPaint() {
    const u = lv.ui, r = mon.rep;
    if (!u || !r) return;
    lvPaintState();
    const v = lvLast();
    // Kopf: Werte jetzt in Worten, Farben aus den Grenzen (wie die Kacheln).
    const c = v['s/cpu'], m = v['s/ram'], ramT = r.info.ram_gesamt || 0;
    const hl = k => k === 'warn' || k === 'err' ? k : 'ok';
    u.hd.replaceChildren(...(c == null ? ['HostDeck misst …'] : ['Prozessor jetzt ', em(hl(lasting('prozessor') ? cpuLevel(c) : ''), cpuText(c)),
      m != null && ramT ? ', Arbeitsspeicher ' : '', m != null && ramT ? em(hl(lasting('speicher') ? ramLevel(m, ramT) : ''), pctText(m / ramT * 100) + ' belegt') : '', '.']));
    const d = uwGaugeData(r, v, lvSer(LV_WIN));
    for (const [k] of GAUGES) u.g[k].set(d[k]);
    for (const ch of u.charts) ch.draw();
    lvRows();
    whoFill(u.who.grid, r, v, lv.gc.gs, lvMean(60));
  }
  // Mittel jedes Werts über die letzten n Sekunden (Live).
  function lvMean(n) {
    const fr = lv.frames;
    if (!fr.length) return null;
    const t0 = fr[fr.length - 1].t - n * 1000, sum = {}, cnt = {};
    for (let i = fr.length - 1; i >= 0 && fr[i].t >= t0; i--) for (const k in fr[i].v) { const x = fr[i].v[k]; if (x != null) { sum[k] = (sum[k] || 0) + x; cnt[k] = (cnt[k] || 0) + 1; } }
    for (const k in sum) sum[k] /= cnt[k];
    return sum;
  }
  // Laufendes Diagramm über 5 Minuten (4.3 Punkt 4): Wert am Ende der Linie,
  // darunter Jetzt, Mittel und Spitze. Zeiger und Pfeiltasten wählen einen
  // Zeitpunkt, der mitläuft; angehalten wird nichts.
  function lvChart(o) {
    const H = o.height || 170, top = 16, bottom = 22, R = 64;
    let W = 560, P = W - R, sel = null;
    const fmt = UW_FMT[o.unit];
    const svg = svgEl('svg', { class: 'uw-svg lv-svg', role: 'img', tabindex: '0', 'data-fid': 'lv-' + o.id });
    const read = el('p', { class: 'uw-read' });
    const cap = el('div', { class: 'lv-cap' });
    const legend = o.series.length > 1 ? el('div', { class: 'lv-legend', 'aria-hidden': 'true' }, o.series.map(s => el('span', { class: 'uw-k' }, el('i', { class: 's' + s.slot + (s.dash ? ' dash' : '') }), s.label))) : null;
    const fig = el('figure', { class: 'uw-fig' }, legend, read, svg, cap);
    const subEl = el('span', { class: 'sub' });
    const node = sec(o.title, fig, { cls: 'c6', sub: ' ' });
    const oldSub = node.querySelector('.ls-sec-h .sub');
    if (oldSub) oldSub.replaceWith(subEl);
    let times = [];
    function draw(w) {
      if (w) { W = Math.max(240, Math.round(w)); P = W - R; }
      subEl.textContent = o.sub();
      const t1 = lvNow(), t0 = t1 - LV_WIN;
      const fr = lv.frames.filter(f => f.t / 1000 >= t0 - 15);
      times = fr.map(f => f.t / 1000).filter(t => t >= t0);
      const data = o.series.map(s => ({ s, pts: fr.map(f => [f.t / 1000, f.v[s.k]]) }));
      let peak = 0;
      for (const dd of data) for (const [t, x] of dd.pts) if (x != null && t >= t0 && x > peak) peak = x;
      const ymax = (o.max && o.max()) || niceMax(peak, o.unit);
      const X = t => (t - t0) / LV_WIN * P, Y = x => top + (1 - Math.min(Math.max(x, 0), ymax) / ymax) * (H - top - bottom);
      svg.setAttribute('viewBox', '0 0 ' + W + ' ' + H);
      svg.setAttribute('width', String(W)); svg.setAttribute('height', String(H));
      const kids = [];
      for (const f of [1, 0.5]) {
        const y = Y(ymax * f);
        kids.push(svgEl('line', { class: 'uw-grid', x1: 0, x2: P, y1: y, y2: y }));
        const t = svgEl('text', { class: 'uw-ax', x: 0, y: y - 4 }); t.textContent = fmt(ymax * f); kids.push(t);
      }
      kids.push(svgEl('line', { class: 'uw-base', x1: 0, x2: P, y1: Y(0), y2: Y(0) }));
      [[0, 'start', 'vor 5 Min'], [0.5, 'middle', 'vor 2½ Min'], [1, 'end', 'jetzt']].forEach(([f, a, txt]) => {
        const t = svgEl('text', { class: 'uw-ax', x: f * P, y: H - 5, 'text-anchor': a }); t.textContent = txt; kids.push(t);
      });
      const clip = 'lvc-' + o.id;
      const defs = svgEl('defs'), cp = svgEl('clipPath', { id: clip });
      cp.append(svgEl('rect', { x: 0, y: 0, width: P + 1, height: H - bottom + 1 })); defs.append(cp); kids.push(defs);
      const g = svgEl('g', { 'clip-path': 'url(#' + clip + ')' });
      const ss = data.map(dd => segs(dd.pts, X, Y, lvGap));
      if (data.length === 1) g.append(svgEl('path', { class: 'uw-a s' + data[0].s.slot, d: segArea(ss[0], Y(0).toFixed(1)) }));
      data.forEach((dd, i) => g.append(svgEl('path', { class: 'uw-l s' + dd.s.slot + (dd.s.dash ? ' dash' : ''), d: segLine(ss[i]) })));
      kids.push(g);
      if (sel != null && sel < t0) sel = null;
      if (sel != null) {
        const x = X(sel);
        kids.push(svgEl('line', { class: 'uw-x', x1: x, x2: x, y1: top - 6, y2: Y(0) }));
        for (const dd of data) {
          const p = dd.pts.find(q => q[0] === sel);
          if (p && p[1] != null) kids.push(svgEl('circle', { class: 'uw-d s' + dd.s.slot, r: 4.5, cx: x, cy: Y(p[1]) }));
        }
      }
      svg.replaceChildren(...kids);
      const stats = data.map(dd => ({ s: dd.s, st: lvStats(dd.pts, t0, t1) }));
      uwEnds(svg, stats.map(x => x.st ? { slot: x.s.slot, x: X(x.st.lastT), y: Y(x.st.last), text: fmt(x.st.last) } : null), top, Y(0));
      const many = data.length > 1;
      cap.replaceChildren(...stats.filter(x => x.st).map(x => el('p', {}, many ? el('span', { class: 'uw-k' }, el('i', { class: 's' + x.s.slot }), x.s.label) : null,
        el('span', {}, 'Jetzt ', el('b', {}, fmt(x.st.last))), el('span', {}, 'Mittel 5 Min ', el('b', {}, fmt(x.st.avg))),
        el('span', {}, 'Spitze ', el('b', {}, fmt(x.st.peak)), el('small', {}, ' um ' + new Date(x.st.at * 1000).toLocaleTimeString('de-DE'))))));
      svg.setAttribute('aria-label', o.title + ', letzte 5 Minuten: ' + (stats.some(x => x.st) ? stats.filter(x => x.st).map(x => (many ? x.s.label + ' ' : '') + 'jetzt ' + fmt(x.st.last) + ', im Mittel ' + fmt(x.st.avg) + ', Spitze ' + fmt(x.st.peak)).join('; ') : 'noch kein Wert') + '.');
      if (sel != null) {
        read.replaceChildren(el('span', { class: 't' }, new Date(sel * 1000).toLocaleTimeString('de-DE')),
          ...data.map(dd => { const p = dd.pts.find(q => q[0] === sel); return el('span', { class: 'uw-k' }, many ? el('i', { class: 's' + dd.s.slot }) : null, dd.s.label + ' ', el('b', {}, p && p[1] != null ? fmt(p[1]) : 'keine Messung')); }));
      } else {
        read.replaceChildren(...(o.hint ? [el('span', { class: 'ls-mut' }, 'Zeiger oder Pfeiltasten zeigen den Wert zu einem Zeitpunkt; die Werte laufen dabei weiter.')] : []));
      }
    }
    const nearest = x => {
      if (!times.length) return null;
      const t = lvNow() - LV_WIN + x / P * LV_WIN;
      let best = null, bd = Infinity;
      for (const q of times) { const dd = Math.abs(q - t); if (dd < bd) { bd = dd; best = q; } }
      return bd <= Math.max(3, lvGap(best)) ? best : null;
    };
    svg.addEventListener('pointermove', e => {
      const b = svg.getBoundingClientRect();
      if (!b.width) return;
      sel = nearest(Math.max(0, Math.min(P, (e.clientX - b.left) / b.width * W)));
      draw();
    });
    svg.addEventListener('pointerleave', () => { sel = null; draw(); });
    svg.addEventListener('focus', () => { if (sel == null && times.length) { sel = times[times.length - 1]; draw(); } });
    svg.addEventListener('blur', () => { sel = null; draw(); });
    svg.addEventListener('keydown', e => {
      if (!times.length) return;
      const i = sel == null ? times.length - 1 : Math.max(0, times.indexOf(sel));
      const moves = { ArrowLeft: -1, ArrowRight: 1, PageUp: -10, PageDown: 10 };
      if (e.key in moves) sel = times[Math.max(0, Math.min(times.length - 1, i + moves[e.key]))];
      else if (e.key === 'Home') sel = times[0];
      else if (e.key === 'End') sel = times[times.length - 1];
      else if (e.key === 'Escape') sel = null;
      else return;
      e.preventDefault();
      draw();
    });
    fig.uwDraw = w => draw(w);
    return { node, fig, draw };
  }
  // Jetzt, Mittel (nach Zeit gewichtet) und Spitze im Fenster.
  function lvStats(pts, t0, t1) {
    let sum = 0, wsum = 0, peak = null, at = 0, last = null, lastT = 0;
    for (let i = 0; i < pts.length; i++) {
      const [t, x] = pts[i];
      if (x == null || t < t0) continue;
      const nt = i + 1 < pts.length ? pts[i + 1][0] : t1;
      const w = Math.max(0.1, Math.min(nt - t, lvGap(t)));
      sum += x * w; wsum += w; last = x; lastT = t;
      if (peak === null || x > peak) { peak = x; at = t; }
    }
    return wsum ? { avg: sum / wsum, peak, at, last, lastT } : null;
  }
  // Eine Zeile in „Gerade aktiv“; bleibt derselbe Knoten, solange es die
  // Gruppe gibt.
  function lvRow(key, sub, open) {
    const nm = el('span'), tg = gsTag(), sm = el('small');
    tg.hidden = true;
    const label = el('span', { class: 'lv-nl' }, el('b', {}, nm, tg), sm);
    let btn = null;
    const name = sub ? el('span', { class: 'uw-cn' }, label)
      : open ? (btn = el('button', { type: 'button', class: 'uw-tg', 'aria-expanded': 'false', 'data-fid': 'lv-g-' + key,
        onclick: () => { if (lv.open.has(key)) lv.open.delete(key); else lv.open.add(key); lvRows(); } }, ic('right', 16), label))
        : el('span', { class: 'lv-one' }, label);
    const barI = el('span');
    const cpu = el('b'), ram = el('b');
    const li = el('li', { class: 'lv-row' + (sub ? ' sub' : ''), 'data-gruppe': sub ? null : key },
      el('div', { class: 'nm' }, name), el('div', { class: 'lv-hbar', 'aria-hidden': 'true' }, barI),
      el('div', { class: 'num' }, cpu, el('small', {}, 'Prozessor')), el('div', { class: 'num' }, ram, el('small', {}, 'Speicher')));
    return {
      li,
      set(x) {
        nm.textContent = x.name;
        nm.title = x.title || '';
        tg.hidden = !x.game;
        sm.textContent = x.small;
        barI.style.width = x.cpu > 0 ? 'max(3px, ' + Math.min(100, x.cpu).toFixed(1) + '%)' : '0';
        cpu.textContent = cpuText(x.cpu);
        li.dataset.wert = x.cpu == null ? '' : String(x.cpu);
        ram.textContent = x.ram == null ? '–' : size(Math.round(x.ram));
        if (btn) btn.setAttribute('aria-expanded', String(lv.open.has(key)));
      },
    };
  }
  function lvRows() {
    const u = lv.ui, r = mon.rep;
    if (!u || !r) return;
    const v = lvLast();
    // Die Gruppen ändern sich nur mit dem Stand (alle 10 s), nicht je Sekunde.
    if (!lv.gc || lv.gc.r !== r || lv.gc.o !== op.rep) lv.gc = { r, o: op.rep, gs: uwGroups(r, null) };
    const items = [];
    // Ohne Messung je Container (etwa cgroup v1) stehen die Werte von Docker
    // da (nowOf); ohne die laufende Gruppe ohne Zahl.
    for (const g of lv.gc.gs) {
      const cpu = nowOf(g, v, 'cpu');
      if (cpu != null || g.run) items.push({ g, cpu, ram: nowOf(g, v, 'ram') });
    }
    u.nocg.hidden = !(items.length && !perContainer(v));
    const by = (a, b) => (b.cpu ?? -1) - (a.cpu ?? -1) || a.g.name.localeCompare(b.g.name, 'de');
    // Reihenfolge nach Prozessor jetzt; sie wechselt höchstens alle 5
    // Sekunden und nie, solange der Fokus in der Liste steht.
    const keys = items.map(x => x.g.key);
    const focusIn = u.rows.contains(document.activeElement);
    const same = lv.orderKeys && keys.length === lv.orderKeys.length && keys.every(k => lv.orderKeys.includes(k));
    if (same && (focusIn || Date.now() - lv.order < 5000)) items.sort((a, b) => lv.orderKeys.indexOf(a.g.key) - lv.orderKeys.indexOf(b.g.key));
    else {
      items.sort(by);
      lv.orderKeys = items.map(x => x.g.key); lv.order = Date.now();
    }
    const want = [], seen = new Set();
    const row = (id, sub, open, data) => {
      let o = u.rowMap.get(id);
      if (!o) { o = lvRow(id.slice(id.indexOf('|') + 1), sub, open); u.rowMap.set(id, o); }
      o.set(data);
      want.push(o.li); seen.add(id);
    };
    for (const { g, cpu, ram } of items) {
      const many = g.cs.length > 1;
      const gm = g.game;
      const small = g.single ? (gm || g.managed ? g.kind : 'Container')
        : 'Projekt · ' + (gm ? gm.spiel : plural(g.cs.length, 'Container', 'Container'));
      row((many ? 'G|' : 'S|') + g.key, false, many, { name: g.name, title: gsTitle(gm), game: !!gm, small, cpu, ram });
      if (many && lv.open.has(g.key)) {
        for (const x of g.cs) {
          const c1 = v['c/' + x.c.name + '/cpu'];
          if (c1 == null && x.c.zustand !== 'running') continue;
          row('C|' + g.key + '|' + x.c.name, true, false, { name: x.name, title: x.c.name, game: !!x.game, small: x.game ? x.game.spiel : 'Container', cpu: c1, ram: v['c/' + x.c.name + '/ram'] });
        }
      }
    }
    for (const id of [...u.rowMap.keys()]) if (!seen.has(id)) u.rowMap.delete(id);
    if (!want.length) want.push(u.empty || (u.empty = el('li', { class: 'lv-row lv-none' }, el('span', { class: 'ls-mut' }, 'Gerade läuft kein Projekt.'))));
    const kids = u.rows.children;
    if (want.length !== kids.length || want.some((n, i) => kids[i] !== n)) {
      const a = document.activeElement;
      u.rows.replaceChildren(...want);
      if (a && u.rows.contains(a) && document.activeElement !== a) a.focus({ preventScroll: true });
    }
  }

  // ---------- Überwachung ----------
  // Messuhren: Prozessor, Arbeitsspeicher, Platte, Netz. Der Bogen zeigt
  // den Anteil, die Mitte Zahl und Einheit; Gelb und Rot tragen zusätzlich
  // ein Zeichen und ein Wort. In Live ändern sie sich an Ort und Stelle.
  const GA_R = 78;
  function gaArc(f) {
    const a0 = 135 * Math.PI / 180, a1 = (135 + 270 * f) * Math.PI / 180;
    const p = a => (100 + GA_R * Math.cos(a)).toFixed(2) + ' ' + (100 + GA_R * Math.sin(a)).toFixed(2);
    return 'M' + p(a0) + 'A' + GA_R + ' ' + GA_R + ' 0 ' + (270 * f > 180 ? 1 : 0) + ' 1 ' + p(a1);
  }
  function uwGauge(id, label) {
    const svg = svgEl('svg', { class: 'ga-svg', viewBox: '0 0 200 172', 'aria-hidden': 'true', focusable: 'false' });
    for (let i = 0; i <= 30; i++) {
      const a = (135 + 9 * i) * Math.PI / 180, big = i % 5 === 0, r0 = big ? 88 : 91, r1 = 96;
      svg.append(svgEl('line', { class: 'ga-tk' + (big ? ' big' : ''), x1: (100 + r0 * Math.cos(a)).toFixed(2), y1: (100 + r0 * Math.sin(a)).toFixed(2), x2: (100 + r1 * Math.cos(a)).toFixed(2), y2: (100 + r1 * Math.sin(a)).toFixed(2) }));
    }
    const track = svgEl('path', { class: 'ga-trk', d: gaArc(1) });
    const val = svgEl('path', { class: 'ga-val' });
    const knob = svgEl('circle', { class: 'ga-knob', r: 7 });
    svg.append(track, val, knob);
    const v = el('span'), u = el('small'), inner = el('span', { class: 'ga-in' });
    const tag = el('span', { class: 'ga-tag', hidden: true });
    const foot = el('p', { class: 'ga-foot' });
    const node = el('section', { class: 'ga', role: 'group', 'data-kpi': id },
      el('h2', { class: 'ga-h', 'aria-hidden': 'true' }, label, tag),
      el('div', { class: 'ga-dial', 'aria-hidden': 'true' }, svg, el('div', { class: 'ga-mid' }, el('b', { class: 'ga-v' }, v, u), inner)),
      foot);
    foot.setAttribute('aria-hidden', 'true');
    return {
      id, node,
      set(d) {
        node.className = 'ga' + (d.kind ? ' ' + d.kind : '');
        node.setAttribute('aria-label', d.aria);
        if (d.wert != null) node.dataset.wert = String(d.wert); else delete node.dataset.wert;
        v.textContent = d.v; u.textContent = d.u || ''; inner.textContent = d.in || '';
        foot.textContent = d.foot || ' ';
        tag.hidden = !d.tag;
        if (d.tag) tag.replaceChildren(ic(d.tag[0] === 'err' ? 'bang' : 'warn', 13), d.tag[1]);
        const f = d.frac == null ? null : Math.max(0, Math.min(1, d.frac));
        val.style.display = knob.style.display = f == null ? 'none' : '';
        if (f != null) {
          val.setAttribute('d', gaArc(Math.max(f, 0.002)));
          const a = (135 + 270 * f) * Math.PI / 180;
          knob.setAttribute('cx', (100 + GA_R * Math.cos(a)).toFixed(2));
          knob.setAttribute('cy', (100 + GA_R * Math.sin(a)).toFixed(2));
        }
      },
    };
  }
  // Werte der Messuhren: v die Werte jetzt, ser(f) die Linie im Fenster
  // (für die Skala des Netzes).
  function uwGaugeData(r, v, ser) {
    const g = limits(), cores = r.info.kerne || 0, ramT = r.info.ram_gesamt || 0;
    const rate = x => x == null ? '–' : size(Math.round(x)) + '/s';
    const P = (x, d) => pctText(x, d);
    const out = {};
    const c = v['s/cpu'];
    {
      const k = lasting('prozessor') && c != null ? cpuLevel(c) : '';
      const [n, u] = c == null ? ['–', ''] : unitSplit(cpuText(c));
      const foot = [v['s/steal'] != null ? 'Steal ' + P(v['s/steal'], 1) : '', v['s/iowait'] != null ? 'wartet auf Platte ' + P(v['s/iowait'], 1) : ''].filter(Boolean).join(' · ');
      out.cpu = { v: n, u, in: cores ? 'alle ' + plural(cores, 'Kern', 'Kerne') : '', foot, kind: k, tag: k && g ? [k, 'über ' + g.prozessor_gelb + ' %'] : null, frac: c == null ? null : c / 100, wert: c,
        aria: 'Prozessor jetzt ' + (c == null ? 'unbekannt' : cpuText(c)) + (cores ? ', alle ' + plural(cores, 'Kern', 'Kerne') + ' = 100 %' : '') + (foot ? ', ' + foot : '') + (k ? ', ' + (k === 'err' ? 'dringend' : 'Achtung') : '') };
    }
    const m = v['s/ram'];
    {
      const p = m != null && ramT ? m / ramT * 100 : null;
      const k = lasting('speicher') && m != null ? ramLevel(m, ramT) : '';
      const [n, u] = m == null ? ['–', ''] : unitSplit(size(Math.round(m)));
      out.ram = { v: n, u, in: ramT ? 'von ' + size(ramT) : '', foot: p != null ? P(p) + ' belegt' : '', kind: k, tag: k ? [k, 'knapp'] : null, frac: p == null ? null : p / 100, wert: p,
        aria: 'Arbeitsspeicher jetzt ' + (m == null ? 'unbekannt' : size(Math.round(m)) + ' belegt') + (ramT ? ' von ' + size(ramT) : '') + (k ? ', knapp' : '') };
    }
    {
      const disks = (r.info.laufwerke || []).filter(x => x.gesamt > 0);
      const d = disks.slice().sort((a, b) => b.belegt / b.gesamt - a.belegt / a.gesamt)[0];
      const io = 'lesen ' + rate(v['s/lesen']) + ' · schreiben ' + rate(v['s/schreiben']);
      if (d) {
        const p = d.belegt / d.gesamt * 100, k = diskLevel(d.belegt, d.gesamt);
        const [dn, du] = unitSplit(pctText(p));
        out.disk = { v: dn, u: du, in: size(d.gesamt - d.belegt) + ' frei', foot: io, kind: k, tag: k ? [k, k === 'err' ? 'dringend' : 'knapp'] : null, frac: p / 100, wert: p,
          aria: 'Platte ' + pctText(p) + ' belegt, ' + size(d.gesamt - d.belegt) + ' frei auf ' + d.pfad + ', ' + io + (k ? ', ' + (k === 'err' ? 'dringend' : 'knapp') : '') };
      } else out.disk = { v: '–', u: '', in: 'kein Laufwerk', foot: io, frac: null, aria: 'Platte: ' + io };
    }
    {
      const a = v['s/ein'], b = v['s/aus'];
      const t = a == null && b == null ? null : (a || 0) + (b || 0);
      // Die Skala reicht bis zur Spitze im gezeigten Fenster (aufgerundet).
      let peak = t || 0;
      const sp = ser ? ser(get => { const x = get('s/ein'), y = get('s/aus'); return x == null && y == null ? null : (x || 0) + (y || 0); }) : null;
      if (sp) for (const [, x] of sp.pts) if (x != null && x > peak) peak = x;
      const top = niceMax(Math.max(peak, 1024), 'rate');
      const [n, u] = t == null ? ['–', ''] : unitSplit(rate(t));
      out.net = { v: n, u, in: 'rein ' + rate(a) + ' · raus ' + rate(b), foot: 'Skala bis ' + rate(top), frac: t == null ? null : t / top, wert: t,
        aria: 'Netz jetzt ' + rate(t) + ', rein ' + rate(a) + ', raus ' + rate(b) };
    }
    return out;
  }
  const GAUGES = [['cpu', 'Prozessor'], ['ram', 'Arbeitsspeicher'], ['disk', 'Platte'], ['net', 'Netz']];
  function uwGauges(r, v, ser) {
    const d = uwGaugeData(r, v, ser);
    return el('div', { class: 'ga-row', role: 'group', 'aria-label': 'Werte jetzt' }, GAUGES.map(([k, t]) => { const g = uwGauge(k, t); g.set(d[k]); return g.node; }));
  }
  // Ein Diagramm zur Zeit; die Reiter wählen, welches.
  const UW_METRICS = [['cpu', 'Prozessor'], ['ram', 'Arbeitsspeicher'], ['net', 'Netz'], ['disk', 'Platte']];
  function uwMetricTabs() {
    return el('div', { class: 'ls-seg uw-mt', role: 'group', 'aria-label': 'Diagramm zeigen' }, UW_METRICS.map(([k, t]) =>
      el('button', { type: 'button', 'data-fid': 'uw-m-' + k, 'aria-pressed': String((uw.metric || 'cpu') === k), onclick: () => { uw.metric = k; uw.sel = null; core.rerender(); } }, t)));
  }

  // Wert jetzt einer Gruppe (Projekt, Container, verwalteter Server) aus den
  // Werten v der Überwachung. Misst der Server je Container nichts (etwa
  // cgroup v1), gilt der Wert von Docker aus dem Betrieb, derselbe wie unter
  // Projekte (Datenprüfung M10).
  function nowOf(g, v, k) {
    if (g.proj && v['p/' + g.proj + '/' + k] != null) return v['p/' + g.proj + '/' + k];
    let s = null;
    for (const x of g.cs) { const y = v['c/' + x.c.name + '/' + k]; if (y != null) s = (s || 0) + y; }
    if (s != null || perContainer(v)) return s;
    for (const x of g.cs) if (x.o && x.o.laeuft && x.o.cpu != null) s = (s || 0) + (k === 'cpu' ? x.o.cpu : x.o.speicher || 0);
    return s;
  }
  // Misst die Überwachung Prozessor je Container? Ohne (cgroup v1) fehlen
  // c/<name>/cpu und ram; Netz und Platte je Container (c/<name>/ein …) gibt
  // es trotzdem, darum zählt nur cpu.
  function perContainer(v) { for (const k in v) if (k.startsWith('c/') && k.endsWith('/cpu')) return true; return false; }
  const NO_CG = 'Dieser Server führt keinen Verlauf je Container (ältere Linux-Einstellung cgroup v1). Die Werte jetzt kommen von Docker, wie unter Projekte.';

  // „Wer braucht gerade was“: Projekte, Spielserver und einzelne Container
  // nach Prozessor jetzt, als Anteil am ganzen Server. Der Balken misst am
  // größten Wert der Kacheln; die Zahl daneben ist der echte Anteil.
  function whoKind(g, m) {
    const k = whoKind0(g, m);
    // Wie unter Projekte und Live: woher ein verwalteter Server kommt.
    return [k[0], k[1] + (g.managed && g.panelProject ? ' · gestartet vom Panel ' + g.panelProject : '') + (g.fresh ? ' · noch nicht erfasst' : '')];
  }
  function whoKind0(g, m) {
    if (g.game) return ['spiel', 'Spielserver'];
    if (g.managed) return ['weitere', 'Verwalteter Server'];
    const p = g.proj && m.projects.find(x => x.name === g.proj);
    if (p) return p.routes.length || (p.domains || []).length ? ['web', 'Webseite'] : ['weitere', 'Projekt'];
    return ['weitere', 'Container'];
  }
  function whoHref(g, m) {
    // Ein verwalteter Server führt zur Projektseite seines Panels.
    const p = (g.proj || g.panelProject) && m.projects.find(x => x.name === (g.proj || g.panelProject));
    if (p) return '#/projekte/' + encodeURIComponent(p.key);
    const c = g.cs.length === 1 && m.containers.find(x => x.name === g.cs[0].c.name);
    return c ? '#/projekte/' + encodeURIComponent(c.key) : null;
  }
  function whoItems(r, v, gs) {
    const items = [];
    for (const g of gs) {
      const cpu = nowOf(g, v, 'cpu'), run = g.cs.some(x => x.c.zustand === 'running');
      if (cpu != null || run) items.push({ g, cpu, ram: nowOf(g, v, 'ram') });
    }
    return items.sort((a, b) => (b.cpu ?? -1) - (a.cpu ?? -1) || a.g.name.localeCompare(b.g.name, 'de'));
  }
  // Füllt das Raster; Kacheln mit gleichem Schlüssel bleiben dieselben
  // Knoten (Live), damit Fokus und Zeiger nicht springen.
  // Live kommt jede Sekunde ein Wert. Würde jedes Mal neu sortiert, tauschten
  // Kacheln mit ähnlicher Last ständig die Plätze. Die Reihenfolge bleibt
  // darum stehen und wird höchstens alle 15 Sekunden neu bestimmt; neue
  // Einträge kommen ans Ende. Steht der Fokus in der Liste, bleibt sie, wie
  // sie ist. rank (Live: Mittel der letzten Minute) bestimmt die Reihenfolge,
  // damit Projekte mit ähnlicher Last nicht hin und her tauschen.
  const WHO_RESORT = 15000;
  function whoOrder(grid, all, rank) {
    const now = Date.now();
    const prev = grid.whoOrder;
    const vals = all.some(x => x.cpu != null);
    const focus = prev && grid.contains(document.activeElement);
    // Kamen die ersten Werte erst jetzt, gilt die Reihenfolge ohne Werte nicht.
    if (!prev || (!focus && (now - grid.whoSorted >= WHO_RESORT || (vals && !grid.whoVals)))) {
      if (rank) all = all.slice().sort((a, b) => (rank.get(b.g.key) ?? -1) - (rank.get(a.g.key) ?? -1) || a.g.name.localeCompare(b.g.name, 'de'));
      grid.whoOrder = all.map(x => x.g.key);
      grid.whoSorted = now;
      grid.whoVals = vals;
      return all;
    }
    const pos = new Map(prev.map((k, i) => [k, i]));
    const out = all.slice().sort((a, b) => (pos.has(a.g.key) ? pos.get(a.g.key) : 1e9) - (pos.has(b.g.key) ? pos.get(b.g.key) : 1e9));
    grid.whoOrder = out.map(x => x.g.key);
    return out;
  }
  function whoFill(grid, r, v, gs, avg) {
    const m = model();
    const rank = avg ? new Map(whoItems(r, avg, gs).map(x => [x.g.key, x.cpu])) : null;
    const all = whoOrder(grid, whoItems(r, v, gs), rank);
    const items = uw.whoAll ? all : all.slice(0, 6);
    const top = Math.max(...items.map(x => x.cpu || 0), 0.1);
    const map = grid.whoMap || (grid.whoMap = new Map());
    const want = [];
    for (const x of items) {
      let t = map.get(x.g.key);
      if (!t) {
        const [grp, word] = whoKind(x.g, m);
        const href = whoHref(x.g, m);
        const n = el('b', {}), val = el('span', { class: 'who-v' }), bar = el('span', { class: 'who-bar', 'aria-hidden': 'true' }, el('i'));
        const head = el('div', { class: 'who-h' }, el('span', { class: 'ov-ic ' + PGROUP[grp][1], 'aria-hidden': 'true' }, ic(PGROUP[grp][2], 18)),
          el('span', { class: 'who-n' }, href ? el('a', { href, class: 'who-l' }, n) : n, el('small', {}, word)));
        t = { node: el('div', { class: 'who', 'data-projekt': x.g.proj || null }, head, val, bar), n, val, bar };
        map.set(x.g.key, t);
      }
      t.n.textContent = x.g.name;
      // replaceChildren nimmt einzelne Knoten, kein Feld (sonst steht dort „[object HTMLElement]“).
      t.val.replaceChildren(...(x.cpu == null ? [el('span', { class: 'who-run' }, 'läuft')] : [el('b', {}, cpuText(x.cpu))].concat(x.ram != null ? [el('small', {}, ' · ' + size(Math.round(x.ram)))] : [])));
      t.bar.hidden = x.cpu == null;
      if (x.cpu != null) t.bar.firstChild.style.width = Math.max(1.5, x.cpu / top * 100).toFixed(1) + '%';
      t.node.setAttribute('aria-label', x.g.name + ': ' + (x.cpu == null ? 'läuft, ohne Messung' : 'Prozessor ' + cpuText(x.cpu) + ' des Servers' + (x.ram != null ? ', Arbeitsspeicher ' + size(Math.round(x.ram)) : '')));
      t.node.setAttribute('role', 'group');
      want.push(t.node);
    }
    for (const k of [...map.keys()]) if (!items.some(x => x.g.key === k)) map.delete(k);
    if (!want.length) want.push(grid.whoEmpty || (grid.whoEmpty = el('p', { class: 'ls-mut' }, 'Gerade läuft kein Projekt.')));
    const kids = grid.children;
    if (want.length !== kids.length || want.some((n, i) => kids[i] !== n)) {
      const a = document.activeElement;
      grid.replaceChildren(...want);
      if (a && grid.contains(a) && document.activeElement !== a) a.focus({ preventScroll: true });
    }
    const more = grid.whoMore;
    if (more) {
      more.hidden = all.length <= 6;
      more.textContent = uw.whoAll ? 'Weniger zeigen' : 'Alle ' + all.length + ' zeigen';
      more.setAttribute('aria-expanded', String(!!uw.whoAll));
    }
    const noCg = items.length && !perContainer(v);
    if (grid.whoNote) grid.whoNote.hidden = !noCg;
  }
  function uwWho(onToggle) {
    const grid = el('div', { class: 'who-grid' });
    grid.whoMore = el('button', { type: 'button', class: 'btn btn-sm btn-g', 'data-fid': 'who-all', hidden: true, onclick: () => { uw.whoAll = !uw.whoAll; onToggle(); } });
    grid.whoNote = el('p', { class: 'note', hidden: true }, NO_CG);
    const node = sec('Wer braucht gerade was', el('div', { class: 'ls-stack sm' }, grid, grid.whoNote), { cls: 'c12', sub: 'Anteil am ganzen Server', end: grid.whoMore });
    node.id = 'uw-wer';
    return { node, grid };
  }

  // Verbindungskarte: Internet → Webserver → Projekte → Datenbanken. Gleiche
  // Daten wie früher der Lageplan; was nicht antwortet, ist gestrichelt mit
  // Zeichen und Wort. Schmal steht sie als Liste untereinander.
  let vmRO = null;
  function vmNode(icon, name, sub, o) {
    o = o || {};
    const inner = [ic(o.bad ? 'x' : icon, 17), el('span', {}, el('b', { title: o.title || null }, name), sub ? el('small', {}, sub) : null)];
    return o.href ? el('a', { class: 'vm-n' + (o.bad ? ' bad' : o.quiet ? ' quiet' : ''), href: o.href, 'data-projekt': o.proj || null }, inner)
      : el('div', { class: 'vm-n' + (o.bad ? ' bad' : o.quiet ? ' quiet' : '') + (o.cls ? ' ' + o.cls : ''), 'data-domain': o.domain || null }, inner);
  }
  function vMap(m) {
    const live = core.svc() && dz.list;
    if (core.svc()) dzFresh();
    const bad = z => z.stufe === 'err' || z.stufe === 'warn';
    const rows = [];
    const dbOf = p => {
      const ds = (p.data || []).filter(d => d.engine && !d.split_of).sort((a, b) => b.size - a.size);
      if (!ds.length) return null;
      return vmNode('db', 'Datenbank', (ENGINES[ds[0].engine] || ds[0].engine) + (ds[0].size ? ' · ' + size(ds[0].size) : '') + (ds.length > 1 ? ' · +' + (ds.length - 1) : ''), { title: dataLabel(ds[0]) });
    };
    let nDom = 0;
    const without = [];
    const word = zs => { const b = zs.filter(bad); return b.length ? (b.length === zs.length ? b[0].wort : b.length + ' von ' + plural(zs.length, 'Domain', 'Domains') + ' ' + (b.length === 1 ? 'antwortet' : 'antworten') + ' nicht') : ''; };
    if (live) {
      const byProj = new Map(), loose = [];
      for (const z of dz.list) {
        nDom++;
        const p = z.projekt && m.projects.find(x => x.name === z.projekt);
        if (p) { if (!byProj.has(p.key)) byProj.set(p.key, []); byProj.get(p.key).push(z); } else loose.push(z);
      }
      for (const p of m.projects) {
        const zs = byProj.get(p.key);
        if (!zs) { without.push(p); continue; }
        const all = zs.every(bad), w = word(zs);
        const doms = zs.map(z => z.domain);
        rows.push({ bad: all, some: !!w, node: vmNode('projekte', p.name, w || doms[0] + (doms.length > 1 ? ' +' + (doms.length - 1) : ''), { href: '#/projekte/' + encodeURIComponent(p.key), proj: p.name, bad: all, quiet: zs.every(z => z.stufe === 'idle'), title: doms.join(', ') }), data: dbOf(p) });
      }
      // Domains ohne Projekt: je Domain ein Knoten, mit dem Ziel darunter.
      for (const z of loose) rows.push({ bad: bad(z), node: vmNode(z.leitstand ? 'server' : 'domains', z.domain, bad(z) ? z.wort : looseTarget(z), { bad: bad(z), quiet: z.stufe === 'idle', domain: z.domain, title: z.text || z.wort }) });
    } else {
      for (const p of m.projects) {
        const hosts = p.routes.flatMap(r => r.hosts);
        if (!hosts.length) { without.push(p); continue; }
        nDom += hosts.length;
        const all = p.routes.every(routeDead);
        rows.push({ bad: all, node: vmNode('projekte', p.name, all ? 'zeigte ins Leere' : hosts[0] + (hosts.length > 1 ? ' +' + (hosts.length - 1) : ''), { href: '#/projekte/' + encodeURIComponent(p.key), proj: p.name, bad: all, title: hosts.join(', ') }), data: dbOf(p) });
      }
      for (const r of m.routes.filter(x => !x.project)) {
        nDom += r.hosts.length;
        const d = routeDead(r);
        rows.push({ bad: d, node: vmNode('domains', r.hosts[0], d ? 'zeigte ins Leere' : r.target ? 'Container ' + r.target : 'kein Ziel', { bad: d, domain: r.hosts[0] }) });
      }
    }
    // Was nicht antwortet, steht oben.
    rows.sort((a, b) => (b.bad - a.bad) || (!!b.some - !!a.some));
    const foot = without.length ? el('p', { class: 'vm-foot' }, el('b', {}, 'Ohne Domain: '),
      without.map((p, i) => [i ? ', ' : '', el('a', { href: '#/projekte/' + encodeURIComponent(p.key), 'data-projekt': p.name }, p.name)])) : null;
    if (!rows.length) {
      return sec('Verbindungen', el('div', { class: 'ls-stack sm' }, el('p', { class: 'ls-note' }, ic('info', 16), el('span', {}, 'Keine Domains gefunden. HostDeck liest sie aus Caddy; ohne Domains führt nichts von außen zu den Projekten. ', el('a', { href: '#/domains' }, 'Mehr dazu'))), foot), { cls: 'c8 vm-sec', sub: 'Was wohin führt' });
    }
    const inet = vmNode('domains', 'Internet', 'von außen', { cls: 'vm-in' });
    const web = vmNode('server', 'Webserver', (m.s.caddy ? 'Caddy · ' : '') + plural(nDom, 'Domain', 'Domains'), { cls: 'vm-web' });
    const svg = svgEl('svg', { class: 'vm-svg', 'aria-hidden': 'true', focusable: 'false' });
    const list = el('ul', { class: 'vm-rows' }, rows.map(x => el('li', { class: 'vm-row' + (x.bad ? ' bad' : '') }, x.node, x.data)));
    const wrap = el('div', { class: 'vm' }, svg, el('div', { class: 'vm-c' }, inet), el('div', { class: 'vm-c' }, web), list);
    const draw = () => {
      if (!wrap.isConnected) return;
      const stack = wrap.clientWidth < 620;
      wrap.classList.toggle('stack', stack);
      if (stack) { svg.replaceChildren(); return; }
      const R = wrap.getBoundingClientRect();
      svg.setAttribute('width', R.width.toFixed(0)); svg.setAttribute('height', R.height.toFixed(0));
      const at = (n, side) => { const b = n.getBoundingClientRect(); return [(side === 'r' ? b.right : b.left) - R.left, b.top + b.height / 2 - R.top]; };
      const ps = [];
      const curve = (a, b, cls) => {
        const mx = (a[0] + b[0]) / 2;
        ps.push(svgEl('path', { class: cls || '', d: 'M' + a[0].toFixed(1) + ' ' + a[1].toFixed(1) + 'C' + mx.toFixed(1) + ' ' + a[1].toFixed(1) + ' ' + mx.toFixed(1) + ' ' + b[1].toFixed(1) + ' ' + b[0].toFixed(1) + ' ' + b[1].toFixed(1) }));
      };
      curve(at(inet, 'r'), at(web, 'l'));
      for (const li of list.children) {
        const n = li.firstChild;
        curve(at(web, 'r'), at(n, 'l'), li.classList.contains('bad') ? 'bad' : '');
        if (n.nextSibling) curve(at(n, 'r'), at(n.nextSibling, 'l'));
      }
      svg.replaceChildren(...ps);
    };
    if (vmRO) vmRO.disconnect();
    vmRO = typeof ResizeObserver === 'function' ? new ResizeObserver(() => draw()) : null;
    if (vmRO) vmRO.observe(wrap);
    requestAnimationFrame(draw);
    const nBad = rows.filter(x => x.bad).length;
    return sec('Verbindungen', el('div', { class: 'ls-stack sm' }, wrap, foot,
      el('p', { class: 'sr' }, plural(rows.length, 'Ziel', 'Ziele') + ' hinter dem Webserver' + (nBad ? ', ' + nBad + ' davon ' + (nBad === 1 ? 'antwortet' : 'antworten') + ' nicht' : '') + '.')),
    { cls: 'c8 vm-sec', sub: 'Was wohin führt', end: live ? null : el('span', { class: 'sub' }, 'Stand der Bestandsaufnahme') });
  }

  // Platte nach Projekten: belegt und frei live, die Größe der Projekte aus
  // der Bestandsaufnahme. Der Balken hat Lücken zwischen den Teilen, die
  // Liste daneben nennt Name und Größe in derselben Reihenfolge.
  function uwDisk(r, m) {
    // Dasselbe Laufwerk wie auf der Übersicht: das vollste (Datenprüfung N9).
    const disks = (r.info.laufwerke || []).filter(x => x.gesamt > 0);
    const d = disks.slice().sort((a, b) => b.belegt / b.gesamt - a.belegt / a.gesamt)[0];
    if (!d) return null;
    // Farbe der Art wie unter Projekte (Spielserver, Webseite, Projekt).
    const kinds = new Map(pRows(m).map(x => [x.key, x.grp]));
    const isGame = p => kinds.get(p.key) === 'spiel';
    const ps = m.projects.map(p => ({ p, n: core.projectBytes(p) })).filter(x => x.n > 0).sort((a, b) => b.n - a.n);
    const shown = ps.slice(0, 5), rest = ps.slice(5).reduce((a, x) => a + x.n, 0);
    const sum = ps.reduce((a, x) => a + x.n, 0);
    const sys = d.belegt - sum;
    const items = shown.map(x => ({ t: x.p.name, n: x.n, c: isGame(x.p) ? 'c-v' : x.p.routes.length ? 'c-b' : 'c-t', href: '#/projekte/' + encodeURIComponent(x.p.key) }));
    if (rest > 0) items.push({ t: 'Weitere ' + plural(ps.length - 5, 'Projekt', 'Projekte'), n: rest, c: 'c-t' });
    // Hier nach Projekten geteilt, unter Speicherplatz nach Art (Images,
    // Volumes …); der Rest heißt darum anders und sagt, was drin ist.
    if (sys > 0) items.push({ t: 'Außerhalb der Projekte (System, Images, Logs)', n: sys, c: 'c-p' });
    const bar = el('div', { class: 'dk-bar', 'aria-hidden': 'true' }, items.map(x => { const s = el('i', { class: x.c, title: x.t + ': ' + size(x.n) }); s.style.flexGrow = String(x.n); return s; }),
      (() => { const s = el('i', { class: 'free' }); s.style.flexGrow = String(Math.max(0, d.gesamt - d.belegt)); return s; })());
    const row = (x, cls) => el('li', { class: cls || null }, el('span', { class: 'dk-sw ' + (x.c || 'free'), 'aria-hidden': 'true' }), x.href ? el('a', { href: x.href }, x.t) : el('span', {}, x.t), el('b', {}, size(x.n)));
    return sec('Platte', el('div', { class: 'ls-stack sm' }, bar,
      el('ul', { class: 'dk-list' }, items.map(x => row(x)), row({ t: 'Frei', n: d.gesamt - d.belegt }, 'free')),
      el('p', { class: 'note' }, 'Laufwerk ' + d.pfad + (disks.length > 1 ? ', am vollsten' : '') + '. Größe der Projekte laut Bestandsaufnahme vom ' + fmtDate(m.s.created) + ', belegt und frei jetzt. Nach Art aufgeteilt (Images, Volumes, Papierkorb) unter ', el('a', { href: '#/platz' }, 'Speicherplatz'), '.')),
    { cls: 'c4', sub: pctText(d.belegt / d.gesamt * 100) + ' belegt · ' + size(d.gesamt) });
  }
  function uwSetHint() {
    return el('section', { class: 'ls-sec c12 uw-hint' }, el('div', { class: 'ls-sec-b' },
      el('span', { class: 'ov-ic', 'aria-hidden': 'true' }, ic('einstellungen', 18)),
      el('p', {}, 'Grenzen und Mitteilungen stellst du an einem Ort ein.'),
      link('#/einstellungen/grenzen', 'Einstellungen', 'btn btn-sm')));
  }
  const UW_ROOMS = [['alles', 'Alles'], ['projekte', 'Projekte'], ['domains', 'Domains'], ['system', 'System']];
  function uwRooms() {
    return el('div', { class: 'ov-rooms', role: 'group', 'aria-label': 'Bereich zeigen' }, UW_ROOMS.map(([k, t]) =>
      el('button', { type: 'button', class: 'ls-chip', 'aria-pressed': String((uw.room || 'alles') === k), 'data-fid': 'uw-room-' + k, onclick: () => { uw.room = k; core.rerender(); } }, t)));
  }
  function uwIn(...ks) { return ks.includes(uw.room || 'alles'); }
  function pageUeberwachung(key) {
    if (key && key !== uw.key && (key === 'live' || RANGES.some(x => x[0] === key))) {
      uw.key = key;
      if (uw.range !== key) { uw.range = key; uw.zoom = null; uw.at = 0; uw.sel = null; }
    }
    if (!core.svc()) return [head({ i: 'puls', k: 'Überwachung', h: 'Gibt es nur im Dienst.', p: 'Die Überwachung läuft, wenn HostDeck als Dienst auf dem Server läuft (sudo ./hostdeck einrichten).' })];
    const isLive = uw.range === 'live';
    if (uwRO) uwRO.disconnect();
    if (!mon.loading && (!mon.at || Date.now() - mon.at > 10000)) setTimeout(loadMon, 0);
    if (!isLive && !uw.loading && (uw.histRange !== uw.range || !uw.at || Date.now() - uw.at > 30000)) setTimeout(loadHist, 0);
    if (!op.rep && !op.loading) setTimeout(loadOps, 0);
    if (!uw.timer) uwTick();
    if (isLive) lvStart(); else if (lv.timer) lvStop();
    const r = mon.rep;
    if (!r) return liveWait(mon, 'puls', 'Überwachung');
    if (!r.laeuft) return [head({ i: 'puls', k: 'Überwachung', h: ['Die Überwachung ', em('warn', 'läuft nicht'), '.'], p: 'Der Dienst misst gerade nicht. Ein Neustart des Dienstes oder ein Update startet sie wieder.' })];
    const pick = id => {
      uw.zoom = null; uw.sel = null; uw.hover = false;
      if (uw.range !== id) { uw.range = id; if (id !== 'live') loadHist(); }
      // Die Adresse nennt den Zeitraum; so führt ein Lesezeichen wieder hin.
      uw.key = id;
      try { history.replaceState(null, '', '#/ueberwachung/' + id); } catch (e) { /* ohne Verlauf bleibt die Adresse */ }
      if (id === 'live') lvStart(); else lvStop();
      uwTick(); core.rerender();
    };
    const chips = el('div', { class: 'uw-range' },
      el('div', { class: 'ls-seg', role: 'group', 'aria-label': 'Zeitraum' },
        el('button', { type: 'button', class: 'lv-seg-live', 'data-fid': 'uw-r-live', 'aria-pressed': String(isLive), onclick: () => pick('live') }, el('span', { class: 'ls-dot' + (isLive ? ' ok' : ''), 'aria-hidden': 'true' }), 'Live'),
        RANGES.map(([id, t]) => el('button', { type: 'button', 'data-fid': 'uw-r-' + id, 'aria-pressed': String(id === uw.range), onclick: () => pick(id) }, t))),
      uw.zoom ? el('button', { type: 'button', class: 'btn btn-sm', 'data-fid': 'uw-unzoom', onclick: () => { uw.zoom = null; core.rerender(); } }, ic('x', 15), 'Zoom zurück') : null);
    const w = r.warnungen;
    const fresh = nt.rep && !nt.rep.web_push.geraete.length && !nt.rep.ntfy;
    const nAl = w ? w.aktiv.filter(a => !a.gut_seit && !a.so_gelassen).length : 0;
    const alertsHd = nAl ? [em(w.aktiv.some(a => a.stufe === 'err' && !a.gut_seit && !a.so_gelassen) ? 'err' : 'warn', plural(nAl, 'Warnung läuft', 'Warnungen laufen')), '. '] : [];
    // Eine Domain, die gerade nicht antwortet, ohne Warnung dazu (die kommt
    // erst nach 2 Prüfungen hintereinander): Der Kopf sagt es, statt grün
    // über einer roten Zeile zu stehen.
    const dBad = domSum([]).bad.filter(b => !(w && w.aktiv.some(a => b.hosts.some(hh => a.schluessel === 'seite:' + hh))));
    if (dBad.length) alertsHd.push(em(dBad.some(b => b.stufe === 'err') ? 'err' : 'warn', dBad.length === 1 ? dBad[0].hosts[0] + ' ' + (dBad[0].wort || 'antwortet nicht') : dBad.length + ' Domains antworten nicht'), '; die Warnung folgt, wenn es so bleibt. ');
    if (isLive) return pageLive(r, chips, w, fresh, alertsHd);
    const h = uw.hist && uw.histRange === uw.range ? uw.hist : null;
    const rng = uw.range, rl = RANGE_LONG[rng];
    const S = k => h ? h.serien[k] || [] : [];
    const P = k => h && h.spitzen ? h.spitzen[k] || null : null;
    // Reihe mit Mittel, Spitze je Punkt und Kennzahlen; f rechnet um.
    const ser = (label, k, slot, f) => {
      const g = f || (x => x), m = a => a ? a.map(x => x == null ? null : g(x)) : null;
      return { label, v: m(S(k)), p: m(P(k)), k: uwK(h, k, f), slot };
    };
    const cpu = h ? uwK(h, 's/cpu') || uwStats(S('s/cpu'), h.zeiten) : null;
    // Kopf: beides als Mittel im gewählten Zeitraum (V16, 3.3), Farben aus
    // den Grenzen des Dienstes.
    const ramT = r.info.ram_gesamt || 0;
    const ramK = h && ramT ? uwK(h, 's/ram') || uwStats(S('s/ram'), h.zeiten) : null;
    const ramPct = ramK ? ramK.avg / ramT * 100 : null;
    const hl = k => k === 'warn' || k === 'err' ? k : 'ok';
    const hd = cpu || ramPct != null ? [
      cpu ? ['Prozessor im Mittel ' + RANGE_OF[rng] + ' ', em(hl(cpuLevel(cpu.avg)), cpuText(cpu.avg))] : null,
      cpu && ramPct != null ? ', Arbeitsspeicher ' : null,
      ramPct != null ? [cpu ? '' : 'Arbeitsspeicher im Mittel ' + RANGE_OF[rng] + ' ', em(hl(ramLevel(ramK.avg, ramT)), pctText(ramPct) + ' belegt')] : null, '.'] : 'HostDeck misst …';
    const disks = (r.info.laufwerke || []).filter(d => d.gesamt > 0).sort((a, b) => b.belegt / b.gesamt - a.belegt / a.gesamt).slice(0, 3);
    // Ein Diagramm zur Zeit (Endfassung): die Reiter im Kopf wählen es.
    const tabs = uwMetricTabs();
    const MK = {
      cpu: () => [uwChart({ id: 'cpu', hint: true, title: 'Prozessor', sub: 'alle ' + r.info.kerne + ' Kerne = 100 %', unit: 'pct', hist: h, range: rng, cls: 'c12', end: tabs,
        series: [ser('ausgelastet', 's/cpu', 1), ser('Steal', 's/steal', 2), ser('wartet auf Platte', 's/iowait', 3)] })],
      ram: () => [uwChart({ id: 'ram', title: 'Arbeitsspeicher', sub: 'bis ' + size(r.info.ram_gesamt) + ', die Grenze des Servers', unit: 'bytes', max: r.info.ram_gesamt || null, hist: h, range: rng, cls: 'c12', end: tabs,
        // Arbeitsspeicher hat überall Farbe 2, wie in der Übersicht.
        series: [ser('belegt', 's/ram', 2)].concat(r.info.swap_gesamt > 0 ? [ser('Swap', 's/swap', 3)] : []) })],
      // Zweite Reihe gestrichelt: Sie verdeckt sich sonst mit der ersten.
      net: () => [uwChart({ id: 'net', title: 'Netz', sub: 'über die Netzkarten des Servers', unit: 'rate', hist: h, range: rng, cls: 'c12', end: tabs,
        series: [ser('rein', 's/ein', 1), Object.assign(ser('raus', 's/aus', 2), { dash: true })] })],
      disk: () => [uwChart({ id: 'io', title: 'Platte lesen und schreiben', unit: 'rate', hist: h, range: rng, cls: 'c12', end: tabs,
        series: [ser('lesen', 's/lesen', 1), Object.assign(ser('schreiben', 's/schreiben', 2), { dash: true })] }),
      uwChart({ id: 'disk', title: 'Platte belegt', height: 120, sub: disks.length === 1 ? 'Laufwerk ' + disks[0].pfad + ', ' + size(disks[0].gesamt) + ' groß' : 'in Prozent je Laufwerk', unit: 'pct', max: 100, hist: h, range: rng, cls: 'c12',
        series: disks.map((d, i) => ser(disks.length === 1 ? 'belegt' : d.pfad, 'd/' + d.pfad, i + 1, x => x / d.gesamt * 100)) })],
    };
    const charts = h ? MK[MK[uw.metric] ? uw.metric : 'cpu']()
      : [sec(null, el('div', { class: 'ls-stack' }, uw.error ? el('p', { class: 'band b-err', role: 'status' }, 'Der Verlauf ist nicht erreichbar: ' + uw.error) : el('span', { class: 'skel skel-h' }), el('span', { class: 'skel skel-t skel-w' })), { cls: 'c12' })];
    // Die Messuhren zeigen den Wert jetzt (alle 10 s); die Skala des Netzes
    // reicht bis zur Spitze im Zeitraum.
    const full = (RANGES.find(x => x[0] === rng) || RANGES[1])[2];
    const hser = h ? f => {
      const t1 = Math.max(Math.floor(Date.now() / 1000), h.zeiten.length ? h.zeiten[h.zeiten.length - 1] : 0);
      return { pts: h.zeiten.map((t, i) => [t, f(k => { const a = h.serien[k]; return a ? a[i] : null; })]), t0: t1 - full, t1, gap: t => (h.grob_bis && t <= h.grob_bis ? h.grob_schritt || h.schritt : h.schritt) * 2.5 };
    } : null;
    const who = uwIn('alles', 'projekte') ? uwWho(() => core.rerender()) : null;
    if (who) whoFill(who.grid, r, r.werte || {}, uwGroups(r, null));
    return [head({ i: 'puls', k: 'Überwachung · ' + rl + ' · Stand ' + when(r.stand_von), h: alertsHd.concat(hd),
      p: 'HostDeck misst den Server und jedes Projekt alle ' + r.takt + ' Sekunden und hebt den Verlauf 30 Tage auf. Die Messuhren zeigen den Wert jetzt, das Diagramm den Zeitraum; die Reiter über dem Diagramm wählen, was es zeigt. Fahre über das Diagramm oder wähle es mit der Tab-Taste und den Pfeiltasten, um die Werte zu einem Zeitpunkt zu sehen; solange du liest, lädt die Seite nicht nach. Live zeigt jede Sekunde einen Wert. „Alles“ zeigt die ganze Seite, die Knöpfe Projekte, Domains und System nur ihren Teil.',
      a: el('div', { class: 'uw-acts' }, chips, el('button', { type: 'button', class: 'btn btn-sm', 'data-fid': 'uw-ref', disabled: uw.loading, onclick: () => { mon.at = 0; uw.at = 0; loadMon(); loadHist(); } }, ic('refresh', 16), 'Aktualisieren')) }),
    uwRooms(),
    uw.error && h ? el('p', { class: 'band b-warn', role: 'status' }, 'Nachladen ging nicht: ' + uw.error) : null,
    uwIn('alles', 'system') ? uwGauges(r, r.werte || {}, hser) : null,
    el('div', { class: 'ls-grid eq' }, uwBody(r, w, fresh, { charts: uwIn('alles', 'system') ? charts : [], who: who && who.node, projects: uwIn('alles', 'projekte') && h ? uwProjects(r, h, rng) : null, h }))];
  }
  // Was unter Messuhren und Diagramm steht, je nach Bereich (Alles,
  // Projekte, Domains, System); gleich für Live und Zeitraum.
  function uwBody(r, w, fresh, o) {
    const m = model();
    const vm = uwIn('alles', 'domains') ? vMap(m) : null;
    const disk = uwIn('alles', 'system') ? uwDisk(r, m) : null;
    if (vm && (!disk || uwIn('domains'))) vm.classList.replace('c8', 'c12');
    if (disk && !vm) disk.classList.replace('c4', 'c6');
    const running = w && w.aktiv.length;
    // „Alles“ ist die Summe der Bereiche: oben wie die Vorlage, darunter
    // Projekte, Seiten, Verlauf der Warnungen und Messung.
    return [running ? uwAlerts(w) : null, ...o.charts, o.who, vm, disk,
      o.active || null, o.projects,
      uwIn('alles', 'domains') && w ? uwSites(w) : null,
      uwIn('alles', 'system') && w ? uwEvents(w) : null,
      uwIn('alles', 'system') ? uwInfo(r, o.h) : null,
      uwIn('alles') ? (fresh ? uwNotifyHint() : uwSetHint()) : null];
  }
  // Live: dieselben Knoten bei jedem Zeichnen (lv.ui), damit Fokus, Zeiger
  // und Werte zwischen zwei Sekunden stehen bleiben.
  function pageLive(r, chips, w, fresh, alertsHd) {
    const u = lv.ui || lvBuild();
    const hd = head({ i: 'puls', k: 'Überwachung', h: [...alertsHd, u.hd],
      p: 'Live misst jede Sekunde, solange diese Ansicht offen und sichtbar ist; danach wieder alle ' + r.takt + ' Sekunden. Die Maus hält nichts an. „Anhalten“ hält Messuhren und Diagramm an, „Weiter“ holt nach, was in der Zeit dazukam. Zum Vergrößern eines Ausschnitts nimm 15 Min.',
      a: el('div', { class: 'uw-acts' }, chips, u.pause) });
    const h1 = hd.querySelector('h1');
    if (h1) h1.after(u.pill, u.sr);
    if (uwRO) for (const c of u.charts) uwRO.observe(c.fig);
    // Ein Diagramm zur Zeit; die Reiter stehen in seinem Kopf.
    const ch = u.charts[{ cpu: 0, ram: 1, net: 2, disk: 3 }[uw.metric] ?? 0];
    const hh = ch.node.querySelector('.ls-sec-h');
    if (hh) { const old = hh.querySelector('.end'); if (old) old.remove(); hh.append(el('div', { class: 'end' }, uwMetricTabs())); }
    setTimeout(lvPaint, 0);
    lvPaint();
    return [hd, uwRooms(), uwIn('alles', 'system') ? u.gauges : null,
      el('div', { class: 'ls-grid eq' }, uwBody(r, w, fresh, { charts: uwIn('alles', 'system') ? [ch.node] : [], who: uwIn('alles', 'projekte') ? u.who.node : null, active: uwIn('alles', 'projekte') ? u.active : null, h: null }))];
  }

  // ---------- Zugang: anmelden, bestätigen, abmelden ----------
  // Die Anmeldung prüft der Dienst; die Seite kennt nur ein Cookie, das sie
  // nicht lesen kann. Wie sie angemeldet ist, sagt /api/sitzung.
  const gate = { signing: null, confirming: null, sess: null, depth: 0, saved: null };
  const hm = d => d.toLocaleTimeString('de-DE', { hour: '2-digit', minute: '2-digit' });
  const gateText = g => g === 'Notausgang' ? 'den Notausgang (SSH-Tunnel)' : g;

  // Ein fester, unsichtbarer Benutzername: Passwort-Manager legen das
  // Passwort dann für diese Adresse unter „leitstand“ ab.
  function userField() {
    return el('input', { type: 'text', name: 'username', autocomplete: 'username', value: 'leitstand', readonly: true, hidden: true, tabindex: '-1' });
  }
  function pwField(id, label) {
    const err = el('p', { class: 'gt-err', id: id + '-err', hidden: true });
    const input = el('input', {
      class: 'fld', id, type: 'password', name: 'password', autocomplete: 'current-password', required: true,
      spellcheck: 'false', autocapitalize: 'off', 'aria-describedby': id + '-err',
    });
    const eye = el('button', {
      type: 'button', class: 'gt-eye', 'aria-label': 'Passwort zeigen', 'aria-pressed': 'false', 'aria-controls': id,
      onclick: () => {
        const show = input.type === 'password';
        input.type = show ? 'text' : 'password';
        eye.setAttribute('aria-pressed', String(show));
        eye.replaceChildren(ic(show ? 'eyeoff' : 'eye', 19));
        input.focus();
      },
    }, ic('eye', 19));
    return {
      wrap: el('div', { class: 'gt-fld' }, el('label', { for: id }, label), el('div', { class: 'gt-pw' }, input, eye), err),
      input,
      error(text) {
        err.hidden = !text;
        err.replaceChildren(text ? ic('warn', 16) : '', text ? el('span', {}, text) : '');
        if (text) input.setAttribute('aria-invalid', 'true'); else input.removeAttribute('aria-invalid');
      },
    };
  }
  // Absenden mit Passwort; gemeinsam für Anmelden und Bestätigen. Bei
  // einer Sperre bleibt der Knopf bis zu ihrem Ende aus.
  function pwSubmit(f, btn, label, path, done) {
    let timer = 0;
    const submit = async () => {
      if (btn.disabled) return;
      const pw = f.input.value;
      if (!pw) { f.error('Bitte gib dein Leitstand-Passwort ein.'); f.input.focus(); return; }
      f.error('');
      btn.disabled = true;
      btn.setAttribute('aria-busy', 'true');
      btn.textContent = 'Prüft …';
      const r = await core.call(path, { passwort: pw }, false, true);
      btn.removeAttribute('aria-busy');
      btn.textContent = label;
      const d = r.data || {};
      if (r.ok) { clearTimeout(timer); done(true, d); return; }
      if (r.status === 401 && d.anmelden && path !== '/api/anmelden') { clearTimeout(timer); done(false, d); return; }
      if (r.status === 429 && d.warten > 0) {
        f.error((d.error || 'Zu viele falsche Passwörter.') + ' Wieder möglich ab ' + hm(new Date(Date.now() + d.warten * 1000)) + ' Uhr.');
        clearTimeout(timer);
        timer = setTimeout(() => { btn.disabled = false; f.error(''); }, d.warten * 1000);
        return;
      }
      btn.disabled = false;
      f.error(d.error || 'Das ging gerade nicht (Fehler ' + r.status + ').');
      f.input.select();
      f.input.focus();
    };
    return { submit, stop: () => clearTimeout(timer) };
  }
  function lostPassword() {
    return el('details', { class: 'gt-help' }, el('summary', {}, 'Passwort vergessen oder noch keins?'),
      el('p', {}, 'Auf dem Server eingeben: ', el('code', {}, 'sudo hostdeck passwort'), '. Das legt ein neues Passwort fest und meldet alle ab.'),
      el('p', {}, 'Ohne Passwort hinein: ', el('code', {}, 'sudo hostdeck notausgang'), ' gibt dir einen Link, der zwei Minuten gilt.'));
  }

  // Dialog über allem; solange einer offen ist, ist der Rest inert.
  const INERT = ['page', 'tabbar', 'layer'];
  function modal(box, onEsc) {
    const prev = document.activeElement;
    if (gate.depth++ === 0) {
      gate.saved = INERT.map(id => document.getElementById(id).inert).concat(document.documentElement.classList.contains('locked'));
      for (const id of INERT) document.getElementById(id).inert = true;
      document.documentElement.classList.add('locked');
    }
    const layer = el('div', { class: 'ls-layer gt-layer' }, el('div', { class: 'scrim', onclick: onEsc || null }), box);
    box.addEventListener('keydown', e => core.trapKeys(e, box, onEsc || (() => {})));
    document.body.append(layer);
    return () => {
      layer.remove();
      if (--gate.depth === 0) {
        INERT.forEach((id, i) => { document.getElementById(id).inert = gate.saved[i]; });
        document.documentElement.classList.toggle('locked', gate.saved[3]);
      }
      if (prev && prev.isConnected && prev.focus) prev.focus({ preventScroll: true });
    };
  }
  function modalOpen() { return gate.depth > 0; }

  // Anmelden: als eigene Seite, solange noch nichts geladen ist, sonst als
  // Dialog über der Seite, damit ungespeicherte Änderungen bleiben.
  function signIn(reason) {
    if (gate.signing) return gate.signing;
    gate.signing = new Promise(resolve => {
      const overlay = !!core.work();
      const f = pwField('gt-pw', 'Leitstand-Passwort');
      const btn = el('button', { type: 'submit', class: 'btn btn-p btn-lg gt-go' }, 'Anmelden');
      let close = null;
      const s = pwSubmit(f, btn, 'Anmelden', '/api/anmelden', () => {
        gate.signing = null;
        if (close) close();
        else {
          document.body.classList.remove('gate');
          const app = document.getElementById('app');
          app.className = '';
          app.replaceChildren(el('p', { class: 'loading', 'aria-busy': 'true' }, 'Lade die Bestandsaufnahme …'));
        }
        core.announce('Angemeldet');
        refreshSession();
        resolve(true);
      });
      const form = el('form', { class: 'gt-form', novalidate: true, onsubmit: e => { e.preventDefault(); s.submit(); } }, userField(), f.wrap, btn);
      if (overlay) {
        const box = el('div', { class: 'gt-dlg', role: 'dialog', 'aria-modal': 'true', 'aria-labelledby': 'gt-h', 'aria-describedby': 'gt-d' },
          el('p', { class: 'ls-eyb' }, ic('lock', 16), el('span', {}, 'Anmeldung abgelaufen')),
          el('h2', { id: 'gt-h' }, 'Neu anmelden'),
          el('p', { id: 'gt-d', class: 'gt-lead' }, reason || 'Deine Anmeldung gilt nicht mehr: nach 30 Minuten ohne Aktivität, nach 12 Stunden oder weil sich jemand überall abgemeldet hat.',
            core.dirty() > 0 ? ' Deine ungespeicherten Änderungen bleiben erhalten.' : ''),
          form, lostPassword());
        close = modal(box, null);
      } else {
        document.body.classList.add('gate');
        document.body.classList.remove('ax', 'nav-open');
        document.getElementById('axbar').hidden = true;
        const app = document.getElementById('app');
        app.className = 'gt-page';
        app.replaceChildren(
          el('div', { class: 'gt-card' },
            el('div', { class: 'gt-brand' }, el('span', { class: 'ls-mark', 'aria-hidden': 'true' }, 'HD'),
              el('span', {}, el('b', {}, 'HostDeck'), el('small', {}, location.host))),
            el('h1', { tabindex: '-1' }, 'Anmelden'),
            el('p', { class: 'gt-lead' }, reason || 'Mit deinem Leitstand-Passwort.'),
            form, lostPassword()),
          el('p', { class: 'gt-note' }, ic('lock', 16),
            el('span', {}, 'Nach fünf falschen Passwörtern ist Anmelden von deiner Adresse aus 15 Minuten gesperrt. Jede Anmeldung steht im Protokoll.')));
        document.title = 'Anmelden · HostDeck Leitstand';
      }
      f.input.focus();
    });
    return gate.signing;
  }

  const CONFIRM_WHAT = {
    '/api/save': 'Speichern legt fest, was bei Backup und Umzug mitkommt.',
    '/api/backup/loeschen': 'Das Backup verschwindet vom Server.',
    '/api/update': 'Ein Update tauscht das Programm auf dem Server aus.',
    '/api/umzug/probelauf': 'Der Probelauf meldet sich am neuen Server an und prüft ihn; eingespielt wird nichts.',
    '/api/umzug/starten': 'Der Probe-Umzug spielt dein Backup auf dem neuen Server ein und startet dort deine Projekte.',
    '/api/umzug/probe-anhalten': 'Anhalten stoppt die Projekte der Probe-Kopie auf dem neuen Server; vorher zeigt HostDeck, was es anhält.',
    '/api/umzug/probe-entfernen': 'Entfernen löscht die Probe-Kopie auf dem neuen Server; vorher zeigt HostDeck, was es löscht, und fragt noch einmal.',
    '/api/umzug/umzugstag': 'Der Umzugstag stoppt deine Projekte hier, sichert sie ein letztes Mal und zieht alles auf den neuen Server.',
    '/api/umzug/zurueck': 'Der Rückweg startet die Projekte hier wieder, die der letzte Lauf aus gelassen hat.',
    '/api/umzug/abschliessen': 'Abschließen macht diesen Server zum abgelösten: Backups und Umzüge gehen hier dann nicht mehr.',
    '/api/umzug/uebernehmen': 'Übernehmen macht diesen Leitstand zum echten: Backups, Umzüge und Mitteilungen gehen dann hier.',
    '/api/zugang/freischalten': 'Freischalten macht den Leitstand unter deiner Adresse von außen erreichbar.',
    '/api/container/aktion': 'Starten, Stoppen und Neustarten ändern, was auf deinem Server läuft.',
    '/api/aktualisieren/starten': 'Aktualisieren holt oder baut neue Images und startet die Dienste damit neu.',
    '/api/aktualisieren/zurueck': 'Zurückstellen startet die Dienste wieder mit den Images von vorher.',
    '/api/domains/ordnen': 'Ordnen schreibt die Caddy-Dateien neu und lädt Caddy neu.',
    '/api/domains/aendern': 'Die Änderung schreibt eine Caddy-Datei und lädt Caddy neu.',
    '/api/aufraeumen/starten': 'Aufräumen löscht, was du gewählt hast; Daten kommen vorher in den Papierkorb.',
    '/api/aufraeumen/zurueckholen': 'Zurückholen legt den Eintrag aus dem Papierkorb wieder an.',
    '/api/platz/loeschen': 'Löschen entfernt, was die Vorschau gezeigt hat; endgültig lässt es sich nicht zurückholen.',
    '/api/sicherheit/aendern': 'Die Änderung schließt oder öffnet Ports von außen; ohne Lebenszeichen des Leitstands nimmt HostDeck sie nach 90 Sekunden zurück.',
    '/api/sicherheit/neu-laden': 'Neu laden setzt die Regeln von HostDeck wieder in Kraft.',
    '/api/leistung/aendern': 'Die Änderung schreibt eine Einstellung des Servers; den Vorher-Stand legt HostDeck ab.',
    '/api/ueberwachung/nachrichten/geraet': 'Danach bekommt dieses Gerät jede Warnung und Entwarnung als Mitteilung.',
    '/api/ueberwachung/nachrichten/geraet/entfernen': 'Danach bekommt das Gerät keine Mitteilungen mehr.',
    '/api/ueberwachung/nachrichten/ntfy': 'Danach gehen Warnungen auch über ntfy.',
    '/api/ueberwachung/nachrichten/herzschlag': 'Danach meldet sich HostDeck alle 5 Minuten bei dieser Adresse.',
    '/api/ueberwachung/nachrichten/aus': 'Danach geht über diesen Weg nichts mehr hinaus.',
    '/api/zugang/zuruecknehmen': 'Zurücknehmen schließt den Zugang von außen; danach geht der Leitstand nur noch über den Notausgang.',
  };
  // Bestätigen mit dem Passwort vor Schritten, die etwas ändern. Gilt fünf
  // Minuten; mit Link Angemeldete fragt der Dienst gar nicht erst.
  function confirmStep(path) {
    if (gate.confirming) return gate.confirming;
    gate.confirming = new Promise(resolve => {
      const f = pwField('gt-cf', 'Leitstand-Passwort');
      const btn = el('button', { type: 'submit', class: 'btn btn-p' }, 'Bestätigen');
      let close = null;
      const finish = (ok, d) => {
        s.stop();
        gate.confirming = null;
        close();
        if (ok) core.announce('Bestätigt, gilt fünf Minuten');
        if (!ok && d && d.anmelden) { signIn().then(() => resolve(false)); return; }
        resolve(ok);
      };
      const s = pwSubmit(f, btn, 'Bestätigen', '/api/bestaetigen', finish);
      const box = el('div', { class: 'gt-dlg', role: 'dialog', 'aria-modal': 'true', 'aria-labelledby': 'gt-cf-h', 'aria-describedby': 'gt-cf-d' },
        el('p', { class: 'ls-eyb' }, ic('lock', 16), el('span', {}, 'Bestätigen')),
        el('h2', { id: 'gt-cf-h' }, 'Mit Passwort bestätigen'),
        el('p', { id: 'gt-cf-d', class: 'gt-lead' }, (CONFIRM_WHAT[path] || 'Dieser Schritt ändert etwas auf dem Server.') + ' Gib dazu dein Leitstand-Passwort ein; die Bestätigung gilt dann fünf Minuten.'),
        el('form', { class: 'gt-form', novalidate: true, onsubmit: e => { e.preventDefault(); s.submit(); } }, userField(), f.wrap,
          el('div', { class: 'gt-acts' }, el('button', { type: 'button', class: 'btn', onclick: () => finish(false) }, 'Abbrechen'), btn)));
      close = modal(box, () => finish(false));
      f.input.focus();
    });
    return gate.confirming;
  }

  async function refreshSession() {
    const r = await core.call('/api/sitzung', null, true, true);
    gate.sess = r.ok && r.data && r.data.weg ? r.data : null;
    // Die Leiste trägt unten die Anmeldung; sie kommt auch ohne Neuzeichnen der Seite.
    if (core.work()) { core.rerender(); refreshChrome(); }
  }
  function via(s) { return s.weg === 'Passwort' ? 'mit Passwort' : 'mit Link vom Notausgang'; }

  // Einstellungen › Leitstand: wie du angemeldet bist, Abmelden.
  function acctSection() {
    const s = gate.sess;
    if (!s) return null;
    const others = (s.anmeldungen || 1) - 1;
    return sec('Anmeldung', el('div', { class: 'ls-stack sm' },
      el('p', {}, el('b', {}, 'Angemeldet ' + via(s)), ', seit ' + when(s.seit) + (s.eingang ? ', über ' + gateText(s.eingang) : '') + '.'),
      el('div', { class: 'ls-acts' },
        el('button', { type: 'button', class: 'btn', 'data-fid': 'acct-out', onclick: () => logout(false) }, ic('logout', 17), 'Abmelden'),
        el('button', { type: 'button', class: 'btn btn-g', 'data-fid': 'acct-out-all', onclick: () => logout(true) }, 'Überall abmelden')),
      el('p', { class: 'note' }, 'Überall abmelden beendet auch ' + (others > 0 ? 'die ' + plural(others, 'andere Anmeldung', 'anderen Anmeldungen') + ', etwa auf anderen Geräten.' : 'Anmeldungen auf anderen Geräten.'))),
    { cls: 'c6', end: el('a', { class: 'btn btn-sm btn-g', href: '#/protokoll' }, ic('protokoll', 16), 'Protokoll') });
  }
  async function logout(all) {
    if (core.dirty() > 0 && !confirm('Ungespeicherte Änderungen verwerfen und abmelden?')) return;
    const r = await core.call('/api/abmelden', all ? { ueberall: true } : {}, false, true);
    if (!r.ok) { core.toast('Abmelden ging nicht: ' + ((r.data && r.data.error) || 'Fehler ' + r.status)); return; }
    gate.sess = null;
    jr.list = null;
    core.signedOut(all ? 'Du bist überall abgemeldet.' : 'Du bist abgemeldet.');
  }

  // ---------- Protokoll ----------
  const KIND = {
    anmeldung: ['Anmeldung', 't-ok'], fehlversuch: ['Fehlversuch', 't-err'], gesperrt: ['Gesperrt', 't-err'],
    abmeldung: ['Abmeldung', 't-mut'], link: ['Einmal-Link', 't-sug'], bestaetigung: ['Bestätigt', 't-mut'],
    passwort: ['Passwort', 't-open'], auswahl: ['Auswahl', 't-sug'], lauf: ['Lauf', 't-sug'], update: ['Update', 't-open'], dienst: ['Dienst', 't-mut'],
    zugang: ['Von außen', 't-open'], download: ['Download', 't-open'], container: ['Container', 't-sug'], projekt: ['Projekt', 't-sug'], domain: ['Domain', 't-sug'],
    aufraeumen: ['Aufräumen', 't-sug'], speicher: ['Speicherplatz', 't-sug'], sicherheit: ['Sicherheit', 't-open'], leistung: ['Leistung', 't-sug'],
    ueberwachung: ['Überwachung', 't-open'],
  };
  const KIND_GROUPS = [
    ['alle', 'Alle', null],
    ['zugang', 'Zugang', ['anmeldung', 'fehlversuch', 'gesperrt', 'abmeldung', 'link', 'bestaetigung', 'passwort']],
    ['aenderungen', 'Änderungen', ['auswahl', 'lauf', 'download', 'container', 'projekt', 'domain', 'aufraeumen', 'speicher', 'sicherheit', 'leistung']],
    ['system', 'System', ['update', 'dienst', 'zugang']],
    ['warnungen', 'Warnungen', ['ueberwachung']],
  ];
  const jr = { list: null, error: '', loading: false, filter: 'alle', stale: false, q: '', at: 0 };
  async function loadJournal() {
    if (jr.loading) return;
    jr.loading = true;
    const r = await core.call('/api/protokoll', null, true);
    jr.loading = false;
    if (r.ok) { jr.list = (r.data && r.data.eintraege) || []; jr.error = ''; jr.at = Date.now(); }
    else jr.error = (r.data && r.data.error) || 'Das Protokoll ließ sich nicht laden (Fehler ' + r.status + ').';
    if (core.work()) core.rerender();
  }
  function journalFresh() {
    if (jr.stale || (!jr.list && !jr.error)) { jr.stale = false; loadJournal(); }
  }
  function dayLabel(d) {
    const day = x => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
    const diff = Math.round((day(new Date()) - day(d)) / 864e5);
    if (diff === 0) return 'Heute';
    if (diff === 1) return 'Gestern';
    return d.toLocaleDateString('de-DE', { weekday: 'long', day: 'numeric', month: 'long' });
  }
  function origin(e) {
    const a = e.adresse || '', g = e.eingang || '';
    if (!a && !g) return '';
    if (!g || a === g) return a === 'Notausgang' ? 'über den Notausgang' : 'von ' + a;
    return 'von ' + a + ' über ' + (g === 'Notausgang' ? 'den Notausgang' : g);
  }
  function jrow(e) {
    const k = KIND[e.art] || [e.art, 't-mut'];
    const d = new Date(e.zeit);
    const det = e.einzelheiten || [];
    const o = origin(e);
    return el('li', { class: 'ls-jr' },
      el('time', { class: 'ls-jt', datetime: e.zeit, title: d.toLocaleString('de-DE') }, hm(d)),
      el('span', { class: 'ls-jk' }, el('span', { class: 'tag ' + k[1] }, k[0])),
      el('div', { class: 'ls-jb' }, el('span', {}, e.text), o ? el('small', {}, o) : null,
        det.length ? el('details', {}, el('summary', {}, plural(det.length, 'Einzelheit', 'Einzelheiten')), el('ul', {}, det.map(x => el('li', {}, x)))) : null));
  }
  // Gleiche Einträge kurz hintereinander (gleiche Art, gleicher Text,
  // gleiche Herkunft, höchstens 30 Minuten Abstand) stehen als eine Zeile
  // „6× …“ mit den Zeiten zum Aufklappen (Idee 15).
  function jrRuns(items) {
    const out = [];
    for (const e of items) {
      const key = e.art + '|' + e.text + '|' + origin(e);
      const last = out[out.length - 1];
      const solo = (e.einzelheiten || []).length > 0;
      const near = last && Math.abs(new Date(last.list[last.list.length - 1].zeit) - new Date(e.zeit)) <= 30 * 60000;
      if (last && !solo && !last.solo && last.key === key && near) last.list.push(e);
      else out.push({ key, list: [e], solo });
    }
    return out.map(r => r.list.length === 1 ? jrow(r.list[0]) : jrBundle(r.list));
  }
  function jrBundle(list) {
    const e = list[0], k = KIND[e.art] || [e.art, 't-mut'];
    const a = new Date(list[list.length - 1].zeit), b = new Date(e.zeit);
    const o = origin(e);
    const span = hm(a) === hm(b) ? hm(b) : hm(a) + '–' + hm(b);
    return el('li', { class: 'ls-jr many' },
      el('time', { class: 'ls-jt', datetime: e.zeit, title: a.toLocaleString('de-DE') + ' bis ' + b.toLocaleString('de-DE') }, span),
      el('span', { class: 'ls-jk' }, el('span', { class: 'tag ' + k[1] }, k[0])),
      el('div', { class: 'ls-jb' }, el('span', {}, el('b', { class: 'ls-jn' }, list.length + '×'), ' ', e.text), o ? el('small', {}, o) : null,
        el('details', {}, el('summary', {}, 'Alle Zeiten'), el('ul', { class: 'ls-jtimes' }, list.map(x => el('li', {}, el('time', { datetime: x.zeit }, new Date(x.zeit).toLocaleTimeString('de-DE'))))))));
  }
  function pageProtokoll() {
    if (!core.svc()) {
      return [head({ i: 'protokoll', k: 'Protokoll', h: 'Das Protokoll gibt es, sobald HostDeck als Dienst läuft.', p: 'Bei hostdeck serve über den SSH-Tunnel schreibt HostDeck keins.' })];
    }
    journalFresh();
    const list = jr.list || [];
    const now = Date.now();
    const recent = list.filter(e => now - new Date(e.zeit).getTime() < 864e5);
    const fails = recent.filter(e => e.art === 'fehlversuch').length;
    const locks = recent.filter(e => e.art === 'gesperrt').length;
    const h = !jr.list ? (jr.error ? ['Das Protokoll ', em('err', 'ließ sich nicht laden'), '.'] : 'Protokoll')
      : fails ? [em('err', plural(fails, 'falsches Passwort', 'falsche Passwörter')), ' in den letzten 24 Stunden.']
        : ['Keine ', em('ok', 'falschen Passwörter'), ' in den letzten 24 Stunden.'];
    const group = KIND_GROUPS.find(g => g[0] === jr.filter) || KIND_GROUPS[0];
    const q = jr.q.trim().toLowerCase();
    const hit = e => !q || [e.text, origin(e), (KIND[e.art] || [e.art])[0], ...(e.einzelheiten || [])].join(' ').toLowerCase().includes(q);
    const shown = (group[2] ? list.filter(e => group[2].includes(e.art)) : list).filter(hit);
    const days = [];
    for (const e of shown) {
      const d = new Date(e.zeit);
      const k = d.toDateString();
      if (!days.length || days[days.length - 1].k !== k) days.push({ k, d, items: [] });
      days[days.length - 1].items.push(e);
    }
    const filter = el('div', { class: 'ls-chips ls-jf', role: 'group', 'aria-label': 'Einträge zeigen' },
      KIND_GROUPS.map(g => el('button', { type: 'button', class: 'ls-chip', 'aria-pressed': String(jr.filter === g[0]), 'data-fid': 'jf-' + g[0], onclick: () => { jr.filter = g[0]; core.rerender(); } },
        g[1], el('span', { class: 'n' }, String(g[2] ? list.filter(e => g[2].includes(e.art)).length : list.length)))));
    let body;
    if (!jr.list && jr.error) body = el('div', { class: 'band b-err', role: 'alert' }, el('p', {}, jr.error));
    else if (!jr.list) body = el('p', { class: 'ls-mut', 'aria-busy': 'true' }, 'Lädt das Protokoll …');
    else if (!shown.length) {
      body = el('div', { class: 'ls-empty' }, ic('protokoll', 24), el('div', {}, el('b', {}, list.length ? 'Hier gibt es nichts davon.' : 'Noch keine Einträge.'),
        el('p', {}, q ? 'Kein Eintrag passt zur Suche.' : list.length ? 'Wähle oben „Alle“, um alles zu sehen.' : 'Sobald sich jemand anmeldet oder etwas ändert, steht es hier.')));
    } else {
      body = days.map(dy => el('section', { class: 'ls-jday', 'aria-label': dayLabel(dy.d) },
        el('h3', {}, dayLabel(dy.d), el('small', {}, plural(dy.items.length, 'Eintrag', 'Einträge'))),
        el('ul', { class: 'ls-jl' }, jrRuns(dy.items))));
    }
    return [head({
      i: 'protokoll', k: 'Protokoll' + (jr.at ? ' · Stand ' + when(new Date(jr.at).toISOString()) : ''), h,
      p: 'Wer wann was gemacht hat: Jede Anmeldung, jeder Fehlversuch und jede Änderung über den Leitstand steht hier, mit Zeit und Adresse. Gleiche Einträge kurz hintereinander stehen in einer Zeile („6×“); „Alle Zeiten“ klappt sie auf. '
        + (locks ? 'Gesperrt wurde in dieser Zeit ' + (locks === 1 ? 'einmal' : locks + '-mal') + '. ' : '') + 'Die neuesten 500 Einträge, neueste zuerst.',
      a: el('button', { type: 'button', class: 'btn btn-sm', 'data-fid': 'jr-reload', disabled: jr.loading, onclick: loadJournal }, ic('refresh', 16), jr.loading ? 'Lädt …' : 'Neu laden'),
    }),
    sec(null, [el('div', { class: 'ls-jbar' }, filter,
      el('label', { class: 'ls-field ls-jq' }, ic('search', 16), el('span', { class: 'sr' }, 'Im Protokoll suchen'),
        el('input', { type: 'search', value: jr.q, placeholder: 'Suchen, etwa Adresse oder Projekt', autocomplete: 'off', 'data-fid': 'jr-q', oninput: e => { jr.q = e.target.value; core.rerender(); } })),
      jr.error && jr.list ? el('span', { class: 'ls-jerr', role: 'alert' }, jr.error) : null), body], { cls: 'ls-jsec' })];
  }

  // ---------- Updates ----------
  // Hochladen mit XMLHttpRequest, weil fetch keinen Fortschritt meldet.
  // Danach fragt die Seite, bis der Dienst die neue Version meldet oder das
  // Protokoll sagt, dass die alte zurück ist.
  const up = { phase: 'idle', file: null, pct: 0, error: '', to: '', from: '', started: 0, done: 0, xhr: null };
  const MAX_UPDATE = 96 * 1024 * 1024;
  function archOf(name) { const m = /(amd64|arm64)/i.exec(name); return m ? m[1].toLowerCase() : ''; }
  function pickFile(file) {
    if (!file) return;
    const arch = (core.svc() || {}).arch || '';
    up.file = file;
    up.error = '';
    if (up.phase === 'done' || up.phase === 'failed') up.phase = 'idle';
    if (!/\.hdu$/i.test(file.name)) up.error = 'Das ist keine Update-Datei von HostDeck. Gesucht ist eine Datei, die auf .hdu endet.';
    else if (file.size > MAX_UPDATE) up.error = 'Die Datei ist zu groß für ein Update von HostDeck.';
    else if (arch && archOf(file.name) && archOf(file.name) !== arch) up.error = 'Diese Datei ist für „' + archOf(file.name) + '“, dieser Server braucht „' + arch + '“.';
    core.rerender();
  }
  async function startUpdate() {
    if (!up.file || (up.error && up.phase !== 'failed') || up.phase === 'sending' || up.phase === 'installing') return;
    // Erst bestätigen, dann hochladen; sonst ginge die Datei umsonst über die Leitung.
    const s = await core.call('/api/sitzung', null, true);
    if (s.ok && !s.data.bestaetigt && !(await confirmStep('/api/update'))) return;
    send();
  }
  function send() {
    const sv = core.svc() || {};
    Object.assign(up, { phase: 'sending', pct: 0, error: '', from: sv.version || '' });
    core.rerender();
    core.announce('Lädt die Update-Datei hoch');
    const xhr = new XMLHttpRequest();
    up.xhr = xhr;
    xhr.open('POST', '/api/update');
    xhr.setRequestHeader('Content-Type', 'application/octet-stream');
    xhr.upload.addEventListener('progress', e => {
      if (!e.lengthComputable) return;
      up.pct = Math.min(100, Math.floor(e.loaded / e.total * 100));
      const bar = document.getElementById('up-bar');
      if (bar) { bar.firstChild.style.width = up.pct + '%'; bar.setAttribute('aria-valuenow', String(up.pct)); }
      const t = document.getElementById('up-pct');
      if (t) t.textContent = up.pct + ' %';
    });
    const fail = text => { up.xhr = null; up.phase = 'idle'; up.error = text; core.rerender(); };
    xhr.addEventListener('load', async () => {
      up.xhr = null;
      let d = {};
      try { d = JSON.parse(xhr.responseText) || {}; } catch (e) { d = {}; }
      if (xhr.status === 202) {
        Object.assign(up, { phase: 'installing', to: d.version || '', started: Date.now() });
        core.rerender();
        core.announce('Signatur geprüft, Version ' + up.to + ' wird eingespielt');
        watchInstall();
        return;
      }
      if (xhr.status === 403 && d.bestaetigen) { up.phase = 'idle'; core.rerender(); if (await confirmStep('/api/update')) send(); return; }
      if (xhr.status === 401 && d.anmelden) { up.phase = 'idle'; core.rerender(); if (await signIn()) startUpdate(); return; }
      fail(d.error || 'Das Update ging nicht (Fehler ' + xhr.status + '). Nichts wurde eingespielt.');
    });
    xhr.addEventListener('error', () => fail('Die Verbindung brach beim Hochladen ab. Nichts wurde eingespielt.'));
    xhr.addEventListener('abort', () => { up.xhr = null; up.phase = 'idle'; core.rerender(); core.announce('Hochladen abgebrochen'); });
    xhr.send(up.file);
  }
  function watchInstall() {
    let n = 0;
    const tick = async () => {
      if (up.phase !== 'installing') return;
      n++;
      // Während des Neustarts kann eine Anfrage hängen bleiben (alte
      // Verbindung); nach 8 Sekunden gilt sie als ohne Antwort, sonst wartet
      // die Seite für immer.
      const r = await Promise.race([core.call('/api/lauf', null, true, true),
        new Promise(res => setTimeout(() => res({ ok: false, status: 0, data: null }), 8000))]);
      if (r.ok && r.data && r.data.version === up.to) {
        Object.assign(up, { phase: 'done', done: Date.now(), file: null });
        jr.stale = true;
        core.refreshLauf();
        core.toast('Update fertig: Version ' + up.to + ' läuft');
        return;
      }
      // Hat HostDeck die alte Version zurückgeholt, steht das als letztes Update im Protokoll.
      if (r.ok && n % 3 === 0) {
        const j = await core.call('/api/protokoll', null, true, true);
        const last = j.ok && j.data ? (j.data.eintraege || []).find(e => e.art === 'update') : null;
        if (last && last.text.startsWith('Update auf ' + up.to + ' fehlgeschlagen')) {
          Object.assign(up, { phase: 'failed', error: last.text });
          jr.stale = true;
          core.rerender();
          core.announce('Das Update ist fehlgeschlagen');
          return;
        }
      }
      if (Date.now() - up.started > 180e3) {
        up.phase = 'failed';
        up.error = r.ok ? 'Nach drei Minuten läuft noch Version ' + r.data.version + '. Das Update ist nicht durchgegangen; Einzelheiten stehen im Protokoll.'
          : 'Der Leitstand antwortet seit drei Minuten nicht. Auf dem Server nachsehen: systemctl status hostdeck-dienst';
        jr.stale = true;
        core.rerender();
        return;
      }
      setTimeout(tick, 2000);
    };
    setTimeout(tick, 2500);
  }
  function upBox(sv) {
    if (up.phase === 'sending') {
      const fill = el('span');
      fill.style.width = up.pct + '%';
      return el('div', { class: 'up-run' },
        el('div', { class: 'up-row' }, el('b', {}, 'Lädt hoch …'), el('span', { class: 'ls-num', id: 'up-pct' }, up.pct + ' %')),
        el('div', { class: 'ls-meter big', id: 'up-bar', role: 'progressbar', 'aria-label': 'Hochladen', 'aria-valuemin': '0', 'aria-valuemax': '100', 'aria-valuenow': String(up.pct) }, fill),
        el('div', { class: 'up-row' }, el('small', { class: 'ls-mut' }, up.file.name + ' · ' + size(up.file.size)),
          el('button', { type: 'button', class: 'btn btn-sm', 'data-fid': 'up-stop', onclick: () => { if (up.xhr) up.xhr.abort(); } }, 'Abbrechen')));
    }
    if (up.phase === 'installing') {
      const steps = [
        ['Hochgeladen', size(up.file.size)],
        ['Signatur geprüft', 'die Datei stammt von HostDeck und ist unverändert'],
        ['Einspielen und neu starten', 'die bisherige Version ' + up.from + ' liegt als Rückfall bereit; der Leitstand ist kurz weg'],
        ['Neue Version antwortet', 'dann ist das Update fertig'],
      ];
      return el('ol', { class: 'ls-vsteps', 'aria-label': 'Fortschritt des Updates' }, steps.map(([t, d], i) => el('li', { class: i < 2 ? 'done' : i === 2 ? 'now' : '' },
        el('span', { class: 'dot' }, i < 2 ? ic('check', 14) : String(i + 1)),
        el('div', {}, el('b', {}, t, i < 2 ? el('span', { class: 'sr' }, ' (erledigt)') : i === 2 ? el('span', { class: 'sr' }, ' (läuft)') : null), el('span', {}, d)))));
    }
    if (up.phase === 'done') {
      return el('div', { class: 'ls-stack sm' },
        el('div', { class: 'ls-empty ok' }, ic('done', 26), el('div', {}, el('b', {}, 'Version ' + up.to + ' läuft.'),
          el('p', {}, 'Eingespielt um ' + hm(new Date(up.done)) + ' Uhr. Die vorige Version ' + up.from + ' liegt als Rückfall auf dem Server.'))),
        el('div', { class: 'ls-row-end' }, el('span', { class: 'ls-mut' }, 'Neu laden zeigt die Oberfläche der neuen Version.'),
          el('button', { type: 'button', class: 'btn btn-sm btn-p', 'data-fid': 'up-reload', onclick: () => location.reload() }, ic('refresh', 16), 'Seite neu laden')));
    }
    const arch = sv.arch || '';
    const why = sv.lauf ? 'Geht erst, wenn „' + sv.lauf.was + '“ fertig ist.' : '';
    const input = el('input', { type: 'file', class: 'sr', accept: '.hdu', 'data-fid': 'up-file', onchange: e => { pickFile(e.target.files[0]); e.target.value = ''; } });
    const drop = el('label', {
      class: 'up-drop',
      ondragenter: e => { e.preventDefault(); drop.classList.add('over'); },
      ondragover: e => { e.preventDefault(); },
      ondragleave: e => { if (!drop.contains(e.relatedTarget)) drop.classList.remove('over'); },
      ondrop: e => { e.preventDefault(); drop.classList.remove('over'); pickFile(e.dataTransfer.files[0]); },
    }, input, ic('upload', 24), el('b', {}, up.file ? 'Andere Datei wählen' : 'Update-Datei wählen'),
      el('span', {}, 'oder hierher ziehen. Sie endet auf .hdu' + (arch ? ' und hat „' + arch + '“ im Namen.' : '.')));
    return el('div', { class: 'ls-stack sm' },
      up.phase === 'failed' ? el('div', { class: 'band b-err', role: 'alert' }, el('p', {}, el('b', {}, 'Nicht eingespielt. '), up.error)) : null,
      up.file ? el('div', { class: 'up-file' + (up.phase === 'failed' ? ' was' : up.error ? ' bad' : '') }, ic(up.phase === 'failed' ? 'doc' : up.error ? 'warn' : 'file', 22),
        el('div', {}, el('b', { class: 'mono' }, up.file.name), el('small', {}, size(up.file.size) + (archOf(up.file.name) ? ' · für ' + archOf(up.file.name) : '')))) : null,
      up.error && up.phase !== 'failed' ? el('p', { class: 'gt-err', role: 'alert' }, ic('warn', 16), el('span', {}, up.error)) : null,
      drop,
      el('div', { class: 'ls-row-end' },
        el('span', { class: 'ls-mut' }, why || 'Dauert etwa eine Minute. Du bestätigst vorher mit deinem Passwort.'),
        el('button', { type: 'button', class: 'btn btn-p', 'data-fid': 'up-go', disabled: !up.file || !!up.error && up.phase !== 'failed' || !!why, onclick: startUpdate },
          ic('updates', 18), up.phase === 'failed' ? 'Nochmal versuchen' : 'Update einspielen')));
  }
  // Hinweis auf neue Versionen: einmal am Tag bei GitHub nachsehen,
  // nur anzeigen, nie selbst einspielen. Aus, bis man ihn einschaltet.
  const nv = { rep: null, loading: false, busy: false, error: '' };
  async function loadNv() {
    if (nv.loading) return;
    nv.loading = true;
    const r = await core.call('/api/versionshinweis', null, true);
    nv.loading = false;
    if (r.ok) { nv.rep = r.data; nv.error = ''; } else nv.error = (r.data && r.data.error) || 'Der Stand ist gerade nicht abrufbar (Fehler ' + r.status + ').';
    core.rerender();
  }
  async function setNv(on) {
    if (nv.busy) return;
    nv.busy = true;
    core.rerender();
    const r = await core.call('/api/versionshinweis', { an: on });
    nv.busy = false;
    if (r.ok) {
      nv.rep = r.data; nv.error = '';
      core.announce(on ? 'Hinweis auf neue Versionen eingeschaltet' : 'Hinweis auf neue Versionen ausgeschaltet');
      jr.stale = true;
      core.refreshLauf();
    } else nv.error = (r.data && r.data.error) || 'Das ging nicht (Fehler ' + r.status + ').';
    core.rerender();
    focusFid('nv-sw');
  }
  function nvBox() {
    if (!nv.rep) {
      if (!nv.loading && !nv.error) loadNv();
      return nv.error ? el('p', { class: 'band b-err', role: 'status' }, nv.error) : el('div', { class: 'ls-stack', 'aria-busy': 'true' }, skel('skel-t skel-w'));
    }
    const x = nv.rep;
    const on = x.an === true;
    const page = 'https://github.com/' + x.repo + '/releases';
    const ext = (href, text) => el('a', { href, target: '_blank', rel: 'noopener noreferrer' }, text);
    let state;
    if (!on) {
      state = el('p', { class: 'ls-mut' }, 'Aus. Neue Versionen findest du dann selbst unter ', ext(page, 'github.com/' + x.repo + '/releases'), '.');
    } else if (nv.busy) {
      state = el('p', { class: 'ls-mut', 'aria-busy': 'true' }, 'Sieht bei GitHub nach …');
    } else if (x.neuer) {
      state = el('div', { class: 'band b-warn band-row', role: 'status' },
        el('p', {}, el('b', {}, 'Version ' + x.neueste + ' ist da. '), 'Hier läuft ' + x.laeuft + '. Lade bei GitHub die Datei für „' + ((core.svc() || {}).arch || 'deinen Server') + '“ herunter und spiele sie oben ein.'),
        el('a', { class: 'btn btn-sm', href: x.link, target: '_blank', rel: 'noopener noreferrer', 'data-fid': 'nv-link' }, ic('right', 16), 'Bei GitHub ansehen'));
    } else if (x.fehler) {
      state = el('p', { class: 'gt-err', role: 'status' }, ic('warn', 16), el('span', {}, 'Nachsehen ging nicht' + (x.geprueft ? ' (' + when(x.geprueft) + ')' : '') + ': ' + x.fehler + '. HostDeck versucht es morgen wieder.'));
    } else if (!x.neueste) {
      state = el('p', { class: 'ls-mut' }, 'Noch nicht nachgesehen.');
    } else if (x.unbekannt) {
      state = el('p', {}, 'Neueste Version bei GitHub: ', el('b', {}, x.neueste), '. Hier läuft die Testversion ' + x.laeuft + '. ', ext(x.link, 'Bei GitHub ansehen'));
    } else {
      state = el('p', { class: 'ls-okline' }, ic('check', 16), el('span', {}, 'Du hast die neueste Version (' + x.laeuft + '). Zuletzt nachgesehen ' + when(x.geprueft) + '.'));
    }
    return el('div', { class: 'ls-stack sm' },
      el('div', { class: 'ls-swrow' },
        el('div', {}, el('b', { id: 'nv-t' }, 'Einmal am Tag bei GitHub nachsehen'),
          el('span', { id: 'nv-d' }, 'HostDeck zeigt dann hier und in der Glocke, wenn es eine neue Version gibt. Eingespielt wird nie von selbst. GitHub sieht dabei nur die Adresse dieses Servers und die Version von HostDeck.')),
        el('button', {
          type: 'button', class: 'ls-switch', role: 'switch', 'aria-checked': String(on), 'aria-labelledby': 'nv-t', 'aria-describedby': 'nv-d', 'data-fid': 'nv-sw', disabled: nv.busy,
          onclick: () => setNv(!on),
        }, el('span', { class: 'ls-switch-t', 'aria-hidden': 'true' }, on ? 'An' : 'Aus'))),
      nv.error ? el('p', { class: 'gt-err', role: 'alert' }, ic('warn', 16), el('span', {}, nv.error)) : null,
      state);
  }
  function pageUpdates() {
    const sv = core.svc();
    if (!sv) {
      return [head({ i: 'updates', k: 'Updates', h: 'Updates im Leitstand gibt es, sobald HostDeck als Dienst läuft.', p: 'Bei hostdeck serve tauschst du das Programm selbst aus.' })];
    }
    journalFresh();
    const arch = sv.arch || '';
    const h = up.phase === 'installing' ? ['Version ', em('acc', up.to), ' wird eingespielt …'] : ['Version ', em('acc', sv.version), ' läuft.'];
    const steps = el('ol', { class: 'ls-steps' },
      el('li', {}, el('b', {}, 'Update-Datei holen'), el('span', {}, 'Zu jeder neuen Version gibt es bei GitHub unter Releases eine Datei für „amd64“ und eine für „arm64“.' + (arch ? ' Dieser Server braucht „' + arch + '“.' : ''))),
      el('li', {}, el('b', {}, 'Hochladen und bestätigen'), el('span', {}, 'mit deinem Leitstand-Passwort. HostDeck prüft die Signatur, die Version und ob die Datei zu diesem Server passt.')),
      el('li', {}, el('b', {}, 'HostDeck spielt ein'), el('span', {}, 'Die laufende Version kommt beiseite, die neue startet. Antwortet sie nicht innerhalb einer Minute, holt HostDeck die alte zurück.')));
    const hist = (jr.list || []).filter(e => e.art === 'update').slice(0, 8);
    return [head({
      i: 'updates', k: 'Updates · Programm auf dem Server', h,
      p: 'Ein Update bringt eine neue Version von HostDeck auf den Server. HostDeck spielt nur Dateien ein, die mit dem Schlüssel von HostDeck unterschrieben sind.',
    }),
    el('div', { class: 'ls-grid eq' },
      sec('Update einspielen', upBox(sv), { cls: 'c7' }),
      sec('So läuft ein Update', steps, { cls: 'c5' }),
      sec('Neue Versionen', nvBox(), { cls: 'c12' }),
      sec('Bisherige Updates', hist.length ? el('ul', { class: 'ls-jl' }, hist.map(jrow))
        : el('p', { class: 'ls-mut' }, jr.list ? 'Über den Leitstand kam noch kein Update.' : 'Lädt …'), { cls: 'c12', sub: 'aus dem Protokoll' }))];
  }

  // ---------- Zugang von außen ----------
  // Freischalten über Caddy in drei Schritten: Adresse prüfen, sehen, was
  // sich an Caddy ändert, mit dem Passwort bestätigen. Den Ablauf führt der
  // Dienst; die Seite fragt nach dem Stand. Zwischendurch startet die
  // Website neu, dann scheitern ein, zwei Abfragen, das ist eingeplant.
  const zg = { rep: null, error: '', loading: false, timer: 0, input: null, plan: null, checkErr: '', checking: false, starting: false, watching: false, result: false, misses: 0, sure: false };
  async function loadAccess() {
    if (zg.loading) return;
    zg.loading = true;
    const r = await core.call('/api/zugang', null, true);
    zg.loading = false;
    clearTimeout(zg.timer);
    if (r.ok) {
      zg.rep = r.data;
      zg.error = '';
      zg.misses = 0;
    } else if (zg.watching && r.status !== 401 && r.status !== 403) {
      zg.misses++;
    } else {
      zg.error = (r.data && r.data.error) || 'Der Stand ließ sich nicht laden (Fehler ' + r.status + ').';
    }
    const job = zg.rep && zg.rep.auftrag;
    if (job && job.laeuft) zg.watching = true;
    if (zg.watching && (!job || job.laeuft || !r.ok)) {
      zg.timer = setTimeout(loadAccess, zg.misses ? 2000 : 1200);
    } else if (zg.watching) {
      zg.watching = false;
      zg.result = true;
      zg.plan = null;
      jr.stale = true;
      core.refreshLauf();
      const ok = !job.fehler;
      core.announce(job.aktion === 'freischalten'
        ? (ok ? 'Freigeschaltet: ' + job.adresse : 'Nicht freigeschaltet, alles ist wie vorher.')
        : (ok ? 'Zugang von außen zurückgenommen.' : 'Zurücknehmen ging nicht.'));
    }
    if (core.work()) core.rerender();
  }
  function zugangBadge() {
    const sv = core.svc();
    const z = sv && sv.zugang;
    return zg.watching || (z && z.laeuft) ? cnt('läuft', 'acc') : null;
  }
  async function checkAddress(e) {
    if (e) e.preventDefault();
    if (zg.checking) return;
    const v = (zg.input || '').trim();
    zg.plan = null;
    zg.sure = false;
    if (!v) { zg.checkErr = 'Bitte eine Adresse eingeben, etwa ' + ((zg.rep && zg.rep.stand.vorschlag) || 'hostdeck.example.de') + '.'; core.rerender(); focusFid('zg-addr'); return; }
    zg.checking = true;
    zg.checkErr = '';
    core.rerender();
    const r = await core.call('/api/zugang/pruefen', { adresse: v });
    zg.checking = false;
    if (r.ok) { zg.plan = r.data; zg.input = r.data.adresse; core.announce(r.data.adresse + ' zeigt auf diesen Server. Unten steht, was sich an Caddy ändert.'); }
    else if (!(r.data && r.data.abgebrochen)) zg.checkErr = (r.data && r.data.error) || 'Prüfen ging nicht (Fehler ' + r.status + ').';
    core.rerender();
    focusFid(r.ok ? 'zg-go' : 'zg-addr');
  }
  function focusFid(fid) {
    requestAnimationFrame(() => { const x = document.querySelector('[data-fid="' + fid + '"]'); if (x) x.focus(); });
  }
  async function startAccess(action) {
    if (zg.starting) return;
    zg.starting = true;
    zg.checkErr = '';
    core.rerender();
    const r = await core.call('/api/zugang/' + action, action === 'freischalten' ? { adresse: zg.plan.adresse } : {});
    zg.starting = false;
    if (r.status === 202) {
      zg.rep = r.data;
      zg.watching = true;
      zg.result = false;
      zg.sure = false;
      core.announce(action === 'freischalten' ? 'Freischalten läuft' : 'Zurücknehmen läuft');
      loadAccess();
      return;
    }
    if (!(r.data && r.data.abgebrochen)) zg.checkErr = (r.data && r.data.error) || 'Das ging nicht (Fehler ' + r.status + ').';
    core.rerender();
  }
  // Ein Schritt des Ablaufs mit Zeichen und Text; Farbe ist nie allein.
  const STEP_MARK = {
    ok: ['done', 'check', 'erledigt'], laeuft: ['now', null, 'läuft'], fehler: ['bad', 'x', 'ging schief'], zurueck: ['back', 'refresh', 'zurückgelegt'],
  };
  function jobSteps(job) {
    return el('ol', { class: 'ls-vsteps zg-steps', 'aria-label': 'Ablauf' }, job.schritte.map((s, i) => {
      const m = STEP_MARK[s.stand] || STEP_MARK.laeuft;
      return el('li', { class: m[0] },
        el('span', { class: 'dot' }, m[1] ? ic(m[1], 14) : String(i + 1)),
        el('div', {}, el('b', {}, s.name, el('span', { class: 'sr' }, ' (' + m[2] + ')')), s.detail ? el('span', {}, s.detail) : null));
    }));
  }
  function filePreview(plan) {
    return el('div', { class: 'zg-files' }, plan.dateien.map(f => el('figure', { class: 'zg-file' },
      el('figcaption', {}, el('b', { class: 'mono' }, f.pfad), f.hinweis ? el('span', {}, f.hinweis) : null),
      f.text ? el('pre', { tabindex: '0', 'aria-label': 'Inhalt für ' + f.pfad }, f.text.replace(/\n$/, '')) : null)));
  }
  function zgHead(h, p, a) { return head({ i: 'globelock', k: 'Zugang von außen · über Caddy', h, p, a }); }
  function pageZugang() {
    const sv = core.svc();
    if (!sv) {
      return [head({ i: 'globelock', k: 'Zugang von außen', h: 'Zugang von außen gibt es, sobald HostDeck als Dienst läuft.', p: 'Bei hostdeck serve erreichst du die Seite nur über deinen SSH-Tunnel.' })];
    }
    if (!zg.rep && !zg.error && !zg.loading) loadAccess();
    if (!zg.rep) {
      return [zgHead(zg.error ? 'Der Stand ließ sich nicht laden.' : 'Lädt …', zg.error || null,
        zg.error ? el('button', { type: 'button', class: 'btn btn-sm', onclick: () => { zg.error = ''; loadAccess(); } }, ic('refresh', 16), 'Nochmal') : null)];
    }
    const s = zg.rep.stand;
    const job = zg.rep.auftrag;
    if (job && (job.laeuft || zg.watching || zg.result)) return zugangJob(s, job);
    if (s.adresse) return zugangOpen(s);
    if (!s.caddy) {
      return [zgHead('Der Leitstand geht nur über den Notausgang.', s.problem || 'Auf diesem Server findet HostDeck keinen Caddy, über den es den Leitstand freischalten könnte.'),
        sec('So kommst du hinein', el('ol', { class: 'ls-steps' },
          el('li', {}, el('b', {}, 'Per SSH anmelden'), el('span', {}, 'und eingeben: sudo hostdeck notausgang')),
          el('li', {}, el('b', {}, 'Tunnel und Link'), el('span', {}, 'HostDeck zeigt dir den Befehl für den Tunnel und einen Link, der zwei Minuten gilt.'))), { cls: 'c12' })];
    }
    return zugangForm(s);
  }
  function zugangForm(s) {
    if (zg.input === null) zg.input = s.vorschlag || '';
    const err = zg.checkErr;
    const plan = zg.plan;
    const form = el('form', { class: 'zg-form', novalidate: true, onsubmit: checkAddress },
      el('div', { class: 'gt-fld' },
        el('label', { for: 'zg-addr' }, 'Adresse für den Leitstand'),
        el('div', { class: 'zg-row' },
          el('input', {
            class: 'fld mono', id: 'zg-addr', 'data-fid': 'zg-addr', type: 'text', inputmode: 'url', autocomplete: 'off', spellcheck: 'false', autocapitalize: 'off',
            value: zg.input, placeholder: s.vorschlag || 'hostdeck.example.de', 'aria-invalid': err ? 'true' : null, 'aria-describedby': err ? 'zg-addr-err zg-addr-h' : 'zg-addr-h',
            oninput: e => { zg.input = e.target.value; if (zg.plan || zg.checkErr) { zg.plan = null; zg.checkErr = ''; zg.sure = false; core.rerender(); } },
          }),
          el('button', { type: 'submit', class: 'btn' + (plan ? '' : ' btn-p'), 'data-fid': 'zg-check', disabled: zg.checking }, zg.checking ? 'Prüft …' : 'Prüfen')),
        el('p', { class: 'zg-help', id: 'zg-addr-h' }, 'Ein Name unter deiner Domain' + (s.vorschlag ? ', etwa ' + s.vorschlag : '') + '. Beim DNS-Anbieter braucht er einen A-Eintrag auf diesen Server, wie deine anderen Subdomains.'),
        err ? el('p', { class: 'gt-err', id: 'zg-addr-err', role: 'alert' }, ic('warn', 16), el('span', {}, err)) : null));
    const out = [form];
    if (plan) {
      out.push(el('div', { class: 'zg-ok', role: 'status' }, ic('done', 20),
        el('span', {}, el('b', {}, plan.adresse), ' zeigt auf ', el('span', { class: 'mono' }, plan.ips.join(', ')), ', also auf diesen Server.')));
      out.push(el('div', { class: 'zg-sub' }, el('h3', {}, 'Das ändert sich an Caddy'), filePreview(plan),
        el('p', { class: 'ls-mut' }, 'Vorher probt HostDeck an einer Kopie, dass caddy validate eine kaputte Datei ablehnt. Dann legt es die alten Fassungen beiseite (auch auf der Platte, falls der Server mittendrin ausfällt), prüft mit caddy validate, lädt neu, wartet auf das Zertifikat und vergleicht deine ' + plural(plan.seiten, 'Seite', 'Seiten') + ' mit vorher. Weicht etwas ab, legt HostDeck beide Dateien byte-gleich zurück.')));
      const sv = core.svc() || {};
      const why = sv.lauf ? 'Geht erst, wenn „' + sv.lauf.was + '“ fertig ist.' : '';
      out.push(el('div', { class: 'ls-row-end' }, el('span', { class: 'ls-mut' }, why || 'Dauert etwa eine Minute. Du bestätigst vorher mit deinem Passwort.'),
        el('button', { type: 'button', class: 'btn btn-p', 'data-fid': 'zg-go', disabled: zg.starting || !!why, onclick: () => startAccess('freischalten') }, ic('globelock', 18), zg.starting ? 'Startet …' : 'Freischalten')));
    }
    return [zgHead('Der Leitstand ist nur über den Notausgang erreichbar.',
      'Freischalten trägt den Leitstand bei Caddy ein. Danach öffnest du ihn unter deiner eigenen Adresse mit dem Leitstand-Passwort, auch am Handy und ohne SSH.'),
    el('div', { class: 'ls-grid' },
      sec('Freischalten', el('div', { class: 'ls-stack sm' }, out), { cls: 'c7' }),
      sec('Was dann gilt', el('ol', { class: 'ls-steps' },
        el('li', {}, el('b', {}, 'Nur mit Passwort'), el('span', {}, 'Von außen meldest du dich mit dem Leitstand-Passwort an. Nach fünf falschen Passwörtern ist eine Viertelstunde Pause; jeder Versuch steht im Protokoll.')),
        el('li', {}, el('b', {}, 'Einmal-Links nur am Notausgang'), el('span', {}, 'Der Link aus sudo hostdeck notausgang gilt nie über die neue Adresse.')),
        el('li', {}, el('b', {}, 'Rückweg jederzeit'), el('span', {}, 'Auf dieser Seite nimmst du den Zugang wieder zurück, mit derselben Prüfung. Der Notausgang bleibt immer.'))), { cls: 'c5' }))];
  }
  function zugangOpen(s) {
    const url = 'https://' + s.adresse;
    const here = gate.sess && gate.sess.eingang === s.adresse;
    const sv = core.svc() || {};
    const why = sv.lauf ? 'Geht erst, wenn „' + sv.lauf.was + '“ fertig ist.' : '';
    const back = zg.sure
      ? el('div', { class: 'zg-sure', role: 'group', 'aria-labelledby': 'zg-sure-t' },
        el('p', { id: 'zg-sure-t' }, el('b', {}, 'Wirklich zurücknehmen? '), here ? 'Du bist gerade über diese Adresse hier. Danach ist diese Seite weg; den Leitstand öffnest du dann nur noch über den Notausgang.' : 'Danach geht der Leitstand nur noch über den Notausgang.'),
        el('div', { class: 'gt-acts' },
          el('button', { type: 'button', class: 'btn', 'data-fid': 'zg-keep', onclick: () => { zg.sure = false; core.rerender(); focusFid('zg-back'); } }, 'Behalten'),
          el('button', { type: 'button', class: 'btn btn-err', 'data-fid': 'zg-back-go', disabled: zg.starting || !!why, onclick: () => startAccess('zuruecknehmen') }, zg.starting ? 'Startet …' : 'Zugang zurücknehmen')))
      : el('div', { class: 'zg-back' }, el('p', {}, why || 'Nimmt den Eintrag bei Caddy heraus, mit derselben Prüfung, und schließt den Eingang. Der Notausgang bleibt.'),
        el('button', { type: 'button', class: 'btn btn-err', 'data-fid': 'zg-back', disabled: !!why, onclick: () => { zg.sure = true; core.rerender(); focusFid('zg-keep'); } }, 'Zugang zurücknehmen'));
    return [zgHead(['Der Leitstand ist erreichbar unter ', em('acc', s.adresse), '.'],
      'Freigeschaltet ' + (s.seit ? 'seit ' + when(s.seit) + '. ' : '. ') + 'Dort meldest du dich mit deinem Leitstand-Passwort an; der Notausgang bleibt für den Notfall.',
      here ? null : el('a', { class: 'btn btn-sm', href: url, target: '_blank', rel: 'noopener' }, ic('next', 16), 'Öffnen')),
    zg.checkErr ? el('div', { class: 'band b-err', role: 'alert' }, el('p', {}, zg.checkErr)) : null,
    el('div', { class: 'ls-grid eq' },
      sec('So erreichst du den Leitstand', el('div', { class: 'ls-kvs flat' },
        el('div', { class: 'ls-kv' }, el('span', {}, 'Adresse'), el('span', { class: 'mono' }, url)),
        el('div', { class: 'ls-kv' }, el('span', {}, 'Anmelden'), el('span', {}, 'mit dem Leitstand-Passwort')),
        el('div', { class: 'ls-kv' }, el('span', {}, 'Notausgang'), el('span', {}, 'sudo hostdeck notausgang, falls die Adresse nicht geht')),
        s.caddyfile ? el('div', { class: 'ls-kv' }, el('span', {}, 'Caddy'), el('span', { class: 'mono' }, s.caddyfile + ' bindet hostdeck.caddy ein')) : null), { cls: 'c7' }),
      sec('Zurücknehmen', back, { cls: 'c5' }))].filter(Boolean);
  }
  function zugangJob(s, job) {
    const pub = job.aktion === 'freischalten';
    const run = job.laeuft || zg.watching;
    let h, p, band = null;
    if (run) {
      h = pub ? ['HostDeck schaltet ', em('acc', job.adresse), ' frei …'] : 'HostDeck nimmt den Zugang von außen zurück …';
      p = 'Das dauert etwa eine Minute. Zwischendurch startet die Website neu; diese Seite macht dann von selbst weiter.';
    } else if (!job.fehler) {
      h = pub ? ['Freigeschaltet: ', em('acc', job.adresse)] : 'Der Zugang von außen ist zurückgenommen.';
      p = pub ? 'Caddy hat das Zertifikat, und deine anderen Seiten antworten wie vorher.' : 'Caddy ist wieder wie vor dem Freischalten. Der Leitstand geht nur noch über den Notausgang.';
      if (pub) {
        band = el('div', { class: 'zg-done' }, ic('done', 26),
          el('div', {}, el('b', {}, 'https://' + job.adresse),
            el('p', {}, 'Dort meldest du dich mit deinem Leitstand-Passwort an, auch am Handy. Der Notausgang bleibt für den Notfall.')),
          el('a', { class: 'btn btn-p', href: 'https://' + job.adresse, target: '_blank', rel: 'noopener', 'data-fid': 'zg-open' }, 'Öffnen', ic('next', 18)));
      }
    } else {
      h = pub ? 'Nicht freigeschaltet.' : 'Nicht zurückgenommen.';
      // Nur der Dienst weiß, ob wirklich alles wie vorher ist (wie_vorher).
      p = job.wie_vorher
        ? 'Alles ist wie vorher: Caddy und deine Seiten laufen unverändert.'
        : 'Hier muss jemand nachsehen: HostDeck konnte nicht alles zurücklegen. Bitte die Meldung unten lesen und nichts weiter probieren.';
      band = el('div', { class: 'band b-err', role: 'alert' }, el('p', {}, job.fehler),
        job.wie_vorher ? null : el('p', {}, 'Was HostDeck sichern konnte, legt es beim nächsten Start des Dienstes selbst zurück. Am Server zeigt sudo hostdeck freischalten den Stand.'));
    }
    const done = !run && el('div', { class: 'ls-row-end' }, el('span', { class: 'ls-mut' }, 'Der Ablauf steht auch im Protokoll.'),
      el('button', { type: 'button', class: 'btn btn-sm', 'data-fid': 'zg-ack', onclick: () => { zg.result = false; core.rerender(); } }, job.fehler && pub ? 'Zurück zur Adresse' : 'Fertig'));
    const waiting = run && zg.misses ? el('p', { class: 'ls-note', role: 'status' }, ic('clock', 16), el('span', {}, 'Die Website startet gerade neu …')) : null;
    return [zgHead(h, p), band,
      sec(run ? 'Ablauf' : 'So lief es', el('div', { class: 'ls-stack sm' }, jobSteps(job), waiting, done || null), { cls: 'c12', sub: run ? 'aktualisiert sich von selbst' : null })].filter(Boolean);
  }

  // ---------- Einstellungen ----------
  // Darstellung: theme.js setzt sie vor dem Zeichnen aus der Kopie im
  // Browser (window.hostdeckLook); gemerkt wird sie beim Dienst, damit PC und
  // Handy gleich aussehen. Ohne Dienst bleibt nur die Kopie im Browser.
  const LOOK = window.hostdeckLook;
  const look = { synced: false, loading: false, saving: 0, msg: null, timer: 0 };
  async function lookLoad() {
    look.loading = true;
    const r = await core.call('/api/darstellung', null, true, true);
    look.loading = false;
    look.synced = true;
    const v = r.ok && r.data && r.data.darstellung;
    if (!v) return;
    const now = LOOK.current(), next = LOOK.clean(v);
    if (JSON.stringify(now) === JSON.stringify(next)) return;
    LOOK.apply(next);
    LOOK.save(next);
    renderLookBtn();
    if (ui.page === 'einstellungen' && core.work()) core.rerender();
  }
  // Eine Wahl ändern: sofort zeigen, im Browser merken, beim Dienst speichern.
  function setLook(patch, say) {
    const v = LOOK.clean(Object.assign({}, LOOK.current(), patch));
    LOOK.apply(v);
    LOOK.save(v);
    renderLookBtn();
    if (say) core.announce(say);
    if (core.svc()) {
      clearTimeout(look.timer);
      look.timer = setTimeout(async () => {
        look.saving++;
        const r = await core.call('/api/darstellung', { darstellung: LOOK.current() }, true);
        look.saving--;
        look.msg = r.ok ? null : { ok: false, text: 'Beim Dienst nicht gespeichert: ' + errText(r, 'Speichern') + ' In diesem Browser gilt die Wahl trotzdem.' };
        if (ui.page === 'einstellungen' && core.work()) core.rerender();
      }, 350);
    }
    if (ui.page === 'einstellungen' && core.work()) core.rerender();
  }
  // Oben rechts: Hell und Dunkel mit einem Klick tauschen.
  function renderLookBtn() {
    const box = document.getElementById('theme');
    if (!box) return;
    const dk = LOOK.isDark();
    const had = document.activeElement && document.activeElement.id === 'theme-btn';
    const label = dk ? 'Dunkel ist an. Zu hell wechseln' : 'Hell ist an. Zu dunkel wechseln';
    box.replaceChildren(el('button', {
      type: 'button', class: 'ls-ibtn', id: 'theme-btn', 'aria-label': label, title: label,
      onclick: () => setLook({ theme: dk ? 'light' : 'dark' }, dk ? 'Darstellung hell' : 'Darstellung dunkel'),
    }, ic(dk ? 'moon' : 'sun', 19)));
    if (had) document.getElementById('theme-btn').focus();
  }
  const ACCENTS = [['violett', 'Violett'], ['eisblau', 'Eisblau'], ['gruen', 'Grün'], ['gelb', 'Sonnengelb'], ['koralle', 'Koralle'], ['rosa', 'Rosa']];
  // Eine Wahl aus mehreren: Knöpfe mit aria-pressed; die gewählte trägt
  // Rahmen und Haken, nicht nur Farbe.
  function lookChoice(key, list, cls, pic) {
    const cur = LOOK.current()[key];
    return el('div', { class: 'ls-opts ' + cls, role: 'group', 'aria-label': list.label },
      list.items.map(([v, t, note]) => el('button', {
        type: 'button', class: 'ls-opt', 'data-fid': 'look-' + key + '-' + v, 'aria-pressed': String(cur === v),
        onclick: () => { setLook({ [key]: v }, list.label + ': ' + t); focusFid('look-' + key + '-' + v); },
      }, pic(v), el('span', { class: 'lb' }, cur === v ? ic('check', 15) : null, el('span', {}, t), v === LOOK.DEF[key] ? el('small', {}, 'Standard') : note ? el('small', {}, note) : null))));
  }
  function lookSwitch(key, on, off, title, note) {
    const cur = LOOK.current()[key];
    const isOn = cur === on;
    return el('div', { class: 'ls-swrow' },
      el('div', {}, el('b', { id: 'sw-' + key }, title), el('span', { id: 'swd-' + key }, note)),
      el('button', {
        type: 'button', class: 'ls-switch', role: 'switch', 'aria-checked': String(isOn), 'aria-labelledby': 'sw-' + key, 'aria-describedby': 'swd-' + key, 'data-fid': 'look-' + key,
        onclick: () => { setLook({ [key]: isOn ? off : on }, title + (isOn ? ': aus' : ': an')); focusFid('look-' + key); },
      }, el('span', { class: 'ls-switch-t', 'aria-hidden': 'true' }, isOn ? 'An' : 'Aus')));
  }
  // Systemeinstellung „weniger Transparenz“ (Windows: Transparenzeffekte aus).
  function reducedGlass() { return !!(window.matchMedia && matchMedia('(prefers-reduced-transparency: reduce)').matches); }
  function pageDarstellung() {
    const v = LOOK.current();
    const mini = cls => el('span', { class: 'ls-mini ' + cls, 'aria-hidden': 'true' }, el('i', { class: 'sb' }), el('span', { class: 'ct' }, el('i', { class: 'a' }), el('i')));
    const swatches = el('div', { class: 'ls-sw6', role: 'group', 'aria-label': 'Farbe' }, ACCENTS.map(([k, t]) => el('button', {
      type: 'button', class: 'ls-swc', 'data-fid': 'look-accent-' + k, 'aria-pressed': String(v.accent === k),
      onclick: () => { setLook({ accent: k }, 'Farbe: ' + t); focusFid('look-accent-' + k); },
    }, el('i', { class: 'sw-' + k, 'aria-hidden': 'true' }, v.accent === k ? ic('check', 18) : null), el('span', {}, t), el('small', {}, k === 'violett' ? 'Standard' : ' '))));
    const left = el('div', { class: 'ls-stack c8' },
      sec('Farbe', swatches, { sub: 'Eine Farbe für den ganzen Leitstand. Ohne Auswahl bleibt es Violett.' }),
      sec('Hintergrund', el('div', { class: 'ls-stack sm' },
        lookChoice('bg', { label: 'Hintergrund', items: [['flecken', 'Farbflecken'], ['berge', 'Berge'], ['hoehen', 'Höhenlinien']] }, 'o3',
          k => el('span', { class: 'ls-bgp bgp-' + k, 'aria-hidden': 'true' }, el('i'), el('i'))),
        lookSwitch('tz', true, false, 'Nach Tageszeit wechseln', 'Morgens heller, abends wärmer, nachts dunkler. Gilt für jeden Hintergrund.')),
      { sub: 'Liegt hinter dem Glas. Rot, Gelb und Grün bleiben immer gut lesbar.' }),
      sec('Glas', el('div', { class: 'ls-stack sm' },
        lookChoice('glas', { label: 'Glas', items: [['geraet', 'Wie das Gerät'], ['an', 'Immer Glas'], ['aus', 'Ohne Glas']] }, 'o3',
          k => el('span', { class: 'ls-glp glp-' + k, 'aria-hidden': 'true' }, el('i'))),
        reducedGlass() && v.glas === 'geraet' ? el('p', { class: 'ls-note' }, ic('info', 16), el('span', {}, 'Dein Gerät hat die Transparenz ausgeschaltet (unter Windows: Einstellungen › Personalisierung › Farben › Transparenzeffekte). Deshalb sind die Kacheln gerade deckend. Mit „Immer Glas“ siehst du das Glas trotzdem.')) : null),
      { sub: 'Durchsichtige Kacheln, hinter denen der Hintergrund weich durchscheint.' }),
      sec('Seitenleiste', lookChoice('leiste', { label: 'Seitenleiste', items: [['breit', 'Breit'], ['schmal', 'Schmal, Name unter dem Symbol']] }, 'o2',
        k => el('span', { class: 'ls-sdp', 'aria-hidden': 'true' }, el('i', { class: k === 'breit' ? 'w' : 'n' }, el('b'), el('b'), el('b')), el('i', { class: 'c' }))),
      { sub: 'Schmal gibt mehr Platz. Der Name steht dann klein unter jedem Symbol. Auf dem Handy gilt immer die Leiste unten.' }),
      sec('Hell oder Dunkel', lookChoice('theme', { label: 'Hell oder Dunkel', items: [['dark', 'Dunkel'], ['light', 'Hell'], ['system', 'Wie das Gerät']] }, 'o3',
        k => mini(k === 'dark' ? 'dk' : k === 'light' ? 'lt' : 'sp')),
      { sub: 'Oben neben der Glocke kannst du jederzeit kurz umschalten.' }),
      el('div', { class: 'ls-grid' },
        sec('Ecken', lookChoice('ecken', { label: 'Ecken', items: [['rund', 'Rund und weich'], ['weniger', 'Weniger rund']] }, 'o2',
          k => el('span', { class: 'ls-shape ' + k, 'aria-hidden': 'true' }, el('i'), el('i'))), { cls: 'c6' }),
        sec('Dichte', lookChoice('dichte', { label: 'Dichte', items: [['bequem', 'Bequem'], ['kompakt', 'Kompakt']] }, 'o2',
          k => el('span', { class: 'ls-dens ' + k, 'aria-hidden': 'true' }, el('i'), el('i'), el('i'), k === 'kompakt' ? [el('i'), el('i')] : null)), { cls: 'c6' })));
    // Vorschau aus dem echten Stand, nicht aus Beispielwerten.
    const m = model();
    const p = m.projects[0];
    const prev = el('div', { class: 'ls-prev', inert: true, 'aria-hidden': 'true' },
      el('div', { class: 'ls-prev-t' }, el('span', { class: 'ls-mark' }, 'HD'), el('span', {}, el('b', {}, core.hostName()), el('small', {}, 'Server')), st('ok', 'erreichbar')),
      p ? el('div', { class: 'ls-prev-p' }, el('span', { class: 'ico' }, ic('projekte', 18)), el('span', {}, el('b', {}, p.name), el('small', {}, p.ok ? 'läuft' : 'aus')),
        el('span', { class: 'ls-switch', 'aria-checked': String(!!p.ok) }, el('span', { class: 'ls-switch-t' }, p.ok ? 'An' : 'Aus'))) : null,
      el('div', { class: 'ls-acts' }, el('span', { class: 'btn btn-p btn-sm' }, 'Hauptknopf'), el('span', { class: 'btn btn-sm' }, 'Knopf'), el('span', { class: 'ls-cnt err' }, '1')));
    const right = el('div', { class: 'ls-stack c4' },
      sec('So sieht es aus', el('div', { class: 'ls-stack sm' }, prev,
        el('p', { class: 'ls-note' }, ic('info', 16), el('span', {}, 'Rot, Gelb und Grün bleiben immer für Warnungen und „läuft“. Die Farbe hier gilt für Schalter, Diagramme und Hervorhebungen.'))), { end: el('span', { class: 'ls-mut' }, 'Vorschau') }),
      sec('Weiteres', lookSwitch('bewegung', 'aus', 'an', 'Bewegungen reduzieren', 'Keine Animationen und Übergänge, auch nicht in Diagrammen.')),
      look.msg ? el('p', { class: 'band b-warn', role: 'status' }, look.msg.text) : null,
      el('p', { class: 'note' }, core.svc() ? 'Gespeichert beim Dienst: gilt auf jedem Gerät, mit dem du dich anmeldest.' : 'Gespeichert in diesem Browser.'));
    return [el('div', { class: 'ls-grid ls-set-grid' }, left, right)];
  }
  function pageEinstellungen(m, key) {
    const sv = core.svc();
    const tab = sv || key === 'leitstand' ? key || '' : '';
    const hd = head({ i: 'einstellungen', k: 'Einstellungen', h: 'Wie der Leitstand aussieht und wann er sich meldet.' });
    if (tab === 'mitteilungen') return [hd, el('div', { class: 'ls-grid' }, uwNotify())];
    if (tab === 'grenzen') {
      if (!mon.loading && (!mon.at || Date.now() - mon.at > 30000)) setTimeout(loadMon, 0);
      const w = mon.rep && mon.rep.warnungen;
      return [hd, el('div', { class: 'ls-grid' }, w ? uwLimits(w) : sec('Regeln', mon.error ? el('p', { class: 'band b-err', role: 'status' }, 'Nicht erreichbar: ' + mon.error) : el('span', { class: 'skel skel-t skel-w' }), { cls: 'c12' }))];
    }
    if (tab === 'leitstand') {
      const b = (sv && sv.bestandsaufnahme) || {};
      const c = core.discoverCtl('');
      const stand = sec('Bestandsaufnahme', el('div', { class: 'ls-stack sm' },
        el('p', {}, 'Stand ', el('b', {}, fmtDate(m.s.created)), b.laeuft ? ', wird gerade neu erfasst.' : b.naechste ? ', nächste ' + when(b.naechste) + '.' : '.'),
        b.fehler && !b.laeuft ? el('p', { class: 'band b-err', role: 'status' }, 'Letzte Bestandsaufnahme ging schief: ' + b.fehler) : null,
        sv ? el('div', { class: 'ls-acts' }, c.btn, c.why ? el('span', { class: 'ls-mut' }, c.why) : null) : el('p', { class: 'note' }, 'Neu erfassen: sudo ./hostdeck discover')), { cls: 'c6' });
      return [hd, el('div', { class: 'ls-grid' }, acctSection(), stand,
        sv ? null : sec('Erreichbar', el('p', {}, ic('lock', 16), ' Nur über deinen SSH-Tunnel erreichbar.'), { cls: 'c6' }))];
    }
    return [hd, ...pageDarstellung()];
  }

  // ---------- Suche ----------
  function openSearch() {
    if (ui.palette) return;
    const m = model();
    const items = Object.entries(PAGES).filter(([id]) => core.svc() || !SVC_PAGES.includes(id)).map(([id, t]) => ({ t, s: 'Bereich', h: '#/' + id, i: id }))
      .concat([{ t: 'Auswahl', s: 'Bereich', h: '#/auswahl', i: 'auswahl' }])
      .concat(m.projects.map(p => ({ t: p.name, s: 'Projekt', h: '#/projekte/' + encodeURIComponent(p.key), i: 'projekte', q: p.services.map(s => s.name).join(' ') })))
      .concat(m.routes.map(r => ({ t: r.hosts.join(', '), s: r.project ? 'Domain von ' + r.project.name : 'Domain', h: r.project ? '#/projekte/' + encodeURIComponent(r.project.key) : '#/domains', i: 'domains' })));
    // Befehle: dieselben Wege wie auf den Seiten. Starten, Stoppen und
    // Neustarten zeigen auch von hier erst die Vorschau.
    const cmds = [];
    if (core.svc()) {
      const lv = live() || [];
      for (const x of rowsAll(m)) {
        const cs = lv.filter(c => x.kind === 'p' ? c.projekt === x.name : c.name === x.name);
        const target = (x.kind === 'p' ? 'projekt:' : 'container:') + x.name;
        cmds.push({ t: 'Logs von ' + x.name, s: 'Befehl', h: pvHref(x, 'logs'), i: 'doc', q: cs.map(c => c.name).join(' ') });
        if (cs.some(c => !c.verwaltet_von && c.laeuft)) cmds.push({ t: x.name + ' neu starten …', s: 'Befehl · mit Vorschau', run: () => opRun(target, 'neustarten'), i: 'refresh' });
        if (cs.some(c => !c.verwaltet_von && !c.laeuft && !c.job)) cmds.push({ t: x.name + ' starten …', s: 'Befehl · mit Vorschau', run: () => opRun(target, 'starten'), i: 'play' });
      }
      cmds.push({ t: 'Backup starten', s: 'Befehl', h: '#/backup', i: 'backup' }, { t: 'Nach Updates sehen', s: 'Befehl', h: '#/updates', i: 'updates' });
    }
    const d = core.discoverCtl('', 'Neu erfassen');
    if (d && !d.why) cmds.push({ t: 'Bestandsaufnahme neu erfassen', s: 'Befehl', run: () => d.btn.click(), i: 'refresh' });
    cmds.push({ t: 'Darstellung hell', s: 'Befehl', run: () => setLook({ theme: 'light' }, 'Darstellung hell'), i: 'sun' }, { t: 'Darstellung dunkel', s: 'Befehl', run: () => setLook({ theme: 'dark' }, 'Darstellung dunkel'), i: 'moon' }, { t: 'Darstellung wie das Gerät', s: 'Befehl', run: () => setLook({ theme: 'system' }, 'Darstellung wie das Gerät'), i: 'monitor' });
    if (core.svc()) items.push({ t: 'Mitteilungen', s: 'Einstellungen', h: '#/einstellungen/mitteilungen', i: 'bell' }, { t: 'Grenzen der Warnungen', s: 'Einstellungen', h: '#/einstellungen/grenzen', i: 'einstellungen' });
    items.push({ t: 'Anmeldung und Bestandsaufnahme', s: 'Einstellungen', h: '#/einstellungen/leitstand', i: 'einstellungen' });
    items.push(...cmds);
    const prev = document.activeElement;
    let sel = 0, shown = items;
    const list = el('ul', { id: 'ls-pal-list', role: 'listbox', 'aria-label': 'Treffer' });
    const input = el('input', { type: 'text', role: 'combobox', 'aria-expanded': 'true', 'aria-controls': 'ls-pal-list', 'aria-autocomplete': 'list', placeholder: 'Projekt, Domain, Bereich oder Befehl', autocomplete: 'off', spellcheck: 'false' });
    const close = () => { layer.remove(); ui.palette = null; document.documentElement.classList.remove('locked'); if (prev && prev.focus) prev.focus(); };
    const goTo = it => { close(); if (it.run) it.run(); else location.hash = it.h; };
    const draw = () => {
      list.replaceChildren(...shown.slice(0, 40).map((it, k) => el('li', {}, el('button', { type: 'button', role: 'option', id: 'ls-pal-' + k, 'aria-selected': k === sel ? 'true' : 'false', tabindex: '-1', onclick: () => goTo(it) },
        ic(it.i, 17), el('span', {}, it.t), el('small', {}, it.s)))));
      if (!shown.length) list.append(el('li', { class: 'ls-pal-none' }, 'Nichts gefunden.'));
      input.setAttribute('aria-activedescendant', shown.length ? 'ls-pal-' + sel : '');
      const a = list.querySelector('[aria-selected="true"]');
      if (a) a.scrollIntoView({ block: 'nearest' });
    };
    input.addEventListener('input', () => {
      const q = input.value.trim().toLowerCase();
      shown = q ? items.filter(it => (it.t + ' ' + it.s + ' ' + (it.q || '')).toLowerCase().includes(q)) : items;
      sel = 0; draw();
    });
    const box = el('div', { class: 'ls-pal', role: 'dialog', 'aria-modal': 'true', 'aria-label': 'Suchen' },
      el('div', { class: 'ls-pi' }, ic('search', 18), input, el('kbd', {}, 'Esc')), list);
    box.addEventListener('keydown', e => {
      if (e.key === 'Escape') { e.preventDefault(); close(); }
      else if (e.key === 'ArrowDown') { e.preventDefault(); sel = Math.min(shown.length - 1, sel + 1); draw(); }
      else if (e.key === 'ArrowUp') { e.preventDefault(); sel = Math.max(0, sel - 1); draw(); }
      else if (e.key === 'Enter' && shown[sel]) { e.preventDefault(); goTo(shown[sel]); }
      else if (e.key === 'Tab') { e.preventDefault(); input.focus(); }
    });
    const layer = el('div', { class: 'ls-layer' }, el('div', { class: 'scrim', onclick: close }), box);
    ui.palette = layer;
    document.body.append(layer);
    document.documentElement.classList.add('locked');
    draw();
    input.focus();
  }

  // Kopie vom Umzugstag von Hand übernehmen (falls DNS das nicht erkennt).
  const copyTake = { sure: false, busy: false, error: '' };
  async function takeCopy() {
    copyTake.busy = true;
    copyTake.error = '';
    core.rerender();
    const r = await core.call('/api/umzug/uebernehmen', {});
    copyTake.busy = false;
    copyTake.sure = false;
    if (!r.ok) copyTake.error = (r.data && r.data.error) || 'Das ging nicht (Fehler ' + r.status + ').';
    else core.announce('Leitstand übernommen');
    core.refreshLauf();
    core.rerender();
  }
  // Probe-Kopie auf einem neuen Server: steht über jeder Seite.
  function copyBanner() {
    const sv = core.svc() || {};
    const a = sv.abgeloest;
    if (a) {
      // Abgelöster Server (2e): Wer hier landet, hat meist noch einen alten
      // Tab offen, dessen Verbindung der Browser festhält.
      return el('div', { class: 'ls-note ls-copy', role: 'note' }, ic('info', 16), el('span', {},
        el('b', {}, 'Dieser Server ist abgelöst'), a.am ? ' seit ' + when(a.am) : '', '. ',
        'Dein Leitstand läuft jetzt auf ' + (a.durch || 'dem neuen Server') + '. Landest du unter deiner Leitstand-Adresse hier, hält dein Browser noch die alte Verbindung: Schließe alle Tabs dieser Seite und öffne sie neu. ',
        'Dieser Server bleibt als Reserve, siehe ', el('a', { href: '#/umzug' }, 'Umzug'), '.'));
    }
    const k = sv.kopie;
    if (!k) return null;
    const from = k.von || 'dem alten Server';
    if (k.umzug) {
      return el('div', { class: 'ls-note ls-copy', role: 'note' }, ic('info', 16), el('span', {},
        el('b', {}, 'Umzug von ' + from), k.seit ? ', eingespielt ' + when(k.seit) : '', '. ',
        'Dieser Leitstand bleibt still (keine Backups, keine Mitteilungen), bis deine Leitstand-Adresse hierher zeigt; dann übernimmt er von selbst. ',
        copyTake.sure
          ? [el('button', { type: 'button', class: 'btn btn-sm btn-p', 'data-fid': 'cp-take-ok', disabled: copyTake.busy, onclick: takeCopy }, 'Ja, jetzt übernehmen'), ' ',
            el('button', { type: 'button', class: 'btn btn-sm', onclick: () => { copyTake.sure = false; core.rerender(); } }, 'Nein')]
          : el('button', { type: 'button', class: 'btn btn-sm', 'data-fid': 'cp-take', onclick: () => { copyTake.sure = true; core.rerender(); } }, 'Von Hand übernehmen'),
        copyTake.error ? el('span', { class: 'gt-err blk', role: 'alert' }, copyTake.error) : null));
    }
    return el('div', { class: 'ls-note ls-copy', role: 'note' }, ic('info', 16), el('span', {},
      el('b', {}, 'Probe-Kopie von ' + from), k.seit ? ', eingespielt ' + when(k.seit) : '', '. ',
      'Hier probierst du aus, wie alles auf dem neuen Server läuft. Backups, Umzüge und Mitteilungen sind aus; was du hier änderst, bleibt auf diesem Server und ist weg, wenn du die Kopie entfernst.'));
  }

  // ---------- Einstieg ----------
  function render(r) {
    const m = model();
    const page = r.page;
    // Beim Öffnen von Protokoll oder Updates das Protokoll frisch holen.
    if (SVC_PAGES.includes(page) && ui.page !== page) jr.stale = true;
    // Wer die Seite wechselt, liest kein Diagramm mehr (sonst bliebe das
    // Nachladen angehalten, wenn der Zeiger beim Wechsel darauf lag).
    if (ui.page !== page) { uw.hover = false; uw.sel = null; }
    ui.page = page;
    // Den Live-Stand schon einmal holen, damit Projekte gleich fertig dasteht.
    if (core.svc() && !op.rep && !op.loading && !op.at) setTimeout(loadOps, 0);
    let nodes;
    if (page === 'projekte') nodes = pageProjekte(m, r.key);
    else if (page === 'domains') nodes = pageDomains(m);
    else if (page === 'backup') nodes = pageBackup(m);
    else if (page === 'umzug') nodes = pageUmzug(m);
    else if (page === 'platz') nodes = pagePlatz(m, r.key);
    else if (page === 'sicherheit') nodes = pageSicherheit();
    else if (page === 'leistung') nodes = pageLeistung();
    else if (page === 'leistungstest') nodes = pageLeistungstest();
    else if (page === 'tuning') nodes = pageTuning();
    else if (page === 'ueberwachung') nodes = pageUeberwachung(r.key);
    else if (page === 'protokoll') nodes = pageProtokoll();
    else if (page === 'updates') nodes = pageUpdates();
    else if (page === 'zugang') nodes = pageZugang();
    else if (page === 'einstellungen') nodes = pageEinstellungen(m, r.key);
    else nodes = pageLage(m, r.key);
    const pk = page === 'projekte' && r.key ? pvSplit(rowsAll(m), r.key) : null;
    const title = pk ? pk.x.name + (pk.tab ? ' · ' + PV_TABS.find(t => t[0] === pk.tab)[1] : '') + ' · Projekte' : PAGES[page] || PAGES.lage;
    // Die Schublade gehört nicht in die Seite: app.js legt sie in #layer und
    // macht den Rest der Seite inert.
    // Unterseiten (Backup/Auswahl, Überwachung/Leistung): Reiter unter dem Kopf.
    const tabs = subTabs(page, r.key);
    if (tabs) { const hi = nodes.findIndex(n => n && n.classList && n.classList.contains('ls-head')); nodes.splice(hi + 1, 0, tabs); }
    const cp = copyBanner();
    if (cp) nodes.unshift(cp);
    const i = nodes.findIndex(n => n && n.classList && n.classList.contains('ls-layer'));
    const drawer = i >= 0 ? nodes.splice(i, 1)[0] : null;
    requestAnimationFrame(scrollRegions);
    return { nodes: nodes.filter(Boolean), title, drawer };
  }
  // Was seitlich scrollt (breite Tabellen, Fehlerzeilen), muss auch mit der
  // Tastatur erreichbar sein (WCAG 2.1.1): Fokus, Rolle und Name dazu.
  function scrollRegions() {
    for (const n of document.querySelectorAll('#page .ls-tw, #page pre')) {
      if (n.hasAttribute('tabindex') || n.scrollWidth <= n.clientWidth + 1) continue;
      const cap = n.querySelector('caption');
      n.setAttribute('tabindex', '0');
      n.setAttribute('role', 'region');
      n.setAttribute('aria-label', (cap && cap.textContent.trim()) || (n.tagName === 'PRE' ? 'Ausgabe' : 'Tabelle') + ', seitlich scrollbar');
    }
  }
  window.addEventListener('resize', () => requestAnimationFrame(scrollRegions));

  function init() {
    document.getElementById('menu-btn').addEventListener('click', () => openNav(!document.body.classList.contains('nav-open')));
    document.getElementById('search-btn').addEventListener('click', openSearch);
    document.addEventListener('keydown', e => {
      if ((e.ctrlKey || e.metaKey) && (e.key === 'k' || e.key === 'K')) { e.preventDefault(); openSearch(); }
      if (e.key === 'Escape' && document.body.classList.contains('nav-open')) { openNav(false); document.getElementById('menu-btn').focus(); }
    });
    document.addEventListener('pointerdown', e => {
      if (shell.bell && !e.target.closest('#bell')) { shell.bell = false; renderBell(); }
      if (ui.pmenu && !e.target.closest('.ls-rmenu-w')) { ui.pmenu = ''; if (core.work()) core.rerender(); }
    });
    // Das Zeilen-Menü steht fest am Knopf; beim Blättern schließt es.
    window.addEventListener('scroll', () => { if (ui.pmenu) { ui.pmenu = ''; if (core.work()) core.rerender(); } }, { passive: true });
    document.addEventListener('click', e => {
      if (e.target.closest('#nav a, #set a')) openNav(false);
      else if (document.body.classList.contains('nav-open') && !e.target.closest('#side, #menu-btn, #more-btn')) openNav(false);
    });
  }

  return { pages: Object.keys(PAGES), render, renderShell, init, openSearch, signIn, confirmStep, refreshSession, modalOpen, setLook, subTabs, helpParts };
};

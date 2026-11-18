// Darstellung setzen, bevor die Seite gezeichnet wird, damit nichts
// aufblitzt. Die Wahl liegt beim Dienst (Einstellungen › Darstellung); der
// Browser hält eine Kopie in hostdeck-darstellung. Ohne Wahl gilt: dunkel,
// Violett, Farbflecken nach Tageszeit getönt, breite Leiste.
'use strict';
(() => {
  const DEF = { theme: 'dark', accent: 'violett', bg: 'flecken', tz: true, leiste: 'breit', ecken: 'rund', dichte: 'bequem', bewegung: 'an', glas: 'geraet' };
  const OK = {
    theme: ['dark', 'light', 'system'], accent: ['violett', 'eisblau', 'gruen', 'gelb', 'koralle', 'rosa'], bg: ['flecken', 'berge', 'hoehen'],
    tz: [false, true], leiste: ['breit', 'schmal'], ecken: ['rund', 'weniger'], dichte: ['bequem', 'kompakt'], bewegung: ['an', 'aus'], glas: ['geraet', 'an', 'aus'],
  };
  // Umstieg von sbm (vor 1.0): Die Merker im Browser hießen sbm-…; einmal
  // umbenennen, damit Hilfe, Woche und erste Schritte bleiben.
  try {
    for (const k of Object.keys(localStorage)) {
      if (!k.startsWith('sbm-')) continue;
      const n = 'hostdeck-' + k.slice(4);
      if (localStorage.getItem(n) === null) localStorage.setItem(n, localStorage.getItem(k));
      localStorage.removeItem(k);
    }
  } catch (e) { /* gesperrt */ }
  const root = document.documentElement;
  const dark = window.matchMedia ? window.matchMedia('(prefers-color-scheme: dark)') : { matches: true };
  function clean(x) {
    const o = {};
    for (const k of Object.keys(DEF)) o[k] = x && OK[k].includes(x[k]) ? x[k] : DEF[k];
    return o;
  }
  function read() {
    let v = null;
    try { v = JSON.parse(localStorage.getItem('hostdeck-darstellung') || 'null'); } catch (e) { v = null; }
    if (!v) {
      // Frühere Fassung merkte sich nur Hell oder Dunkel.
      let t = '';
      try { t = localStorage.getItem('hostdeck-theme') || ''; } catch (e) { t = ''; }
      if (t === 'light' || t === 'dark') v = { theme: t };
    }
    return clean(v);
  }
  function save(v) {
    try { localStorage.setItem('hostdeck-darstellung', JSON.stringify(clean(v))); localStorage.removeItem('hostdeck-theme'); } catch (e) { /* gesperrt */ }
  }
  // Tageszeit: morgens heller, abends wärmer, nachts dunkler; am Tag nichts.
  function zone(h) { return h >= 5 && h < 10 ? 'morgen' : h >= 17 && h < 21 ? 'abend' : h >= 21 || h < 5 ? 'nacht' : ''; }
  let last = DEF;
  function apply(v) {
    last = clean(v);
    const set = (k, val) => { if (val) root.setAttribute(k, val); else root.removeAttribute(k); };
    set('data-theme', last.theme === 'system' ? '' : last.theme);
    set('data-accent', last.accent === 'violett' ? '' : last.accent);
    set('data-bg', last.bg === 'flecken' ? '' : last.bg);
    set('data-leiste', last.leiste === 'schmal' ? 'schmal' : '');
    set('data-ecken', last.ecken === 'weniger' ? 'weniger' : '');
    set('data-dichte', last.dichte === 'kompakt' ? 'kompakt' : '');
    set('data-bewegung', last.bewegung === 'aus' ? 'aus' : '');
    // Glas: ohne Wahl folgt es der Systemeinstellung „Transparenz“ (CSS).
    set('data-glas', last.glas === 'geraet' ? '' : last.glas);
    set('data-tz', last.tz ? zone(new Date().getHours()) : '');
    const m = document.querySelector('meta[name="theme-color"]');
    if (m) m.setAttribute('content', isDark() ? '#0b0b12' : '#eef0f6');
  }
  // Gerade dunkel? Bei „Wie das Gerät“ entscheidet das System.
  function isDark() { return last.theme === 'dark' || (last.theme === 'system' && dark.matches); }
  apply(read());
  setInterval(() => { if (last.tz) apply(last); }, 600000);
  if (dark.addEventListener) dark.addEventListener('change', () => apply(last));
  window.hostdeckLook = { DEF, OK, clean, read, save, apply, isDark, current: () => last };
})();

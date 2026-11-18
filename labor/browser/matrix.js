// Bildmatrix: Übersicht
// und Überwachung › Verlauf (1 Std, 24 Std, 30 Tage) in mehreren Breiten und
// Themen, Browser auf Europe/Berlin (Server auf UTC, Z8). Dazu die Antworten
// der API als JSON neben den Bildern, damit Befunde belegbar sind.
//
//   node labor/browser/matrix.js <zustand> <ordner> [voll]
//
// <zustand> ist ein Buchstabe a–f (nur Dateiname); „voll“ nimmt alle
// Breiten und Themen, sonst nur 1440 und 390 dunkel. Zustand f zeigt nur die
// Übersicht. Dateien: <zustand>-<seite>-<breite>-<thema>.jpg
'use strict';
const fs = require('fs');
const path = require('path');
const L = require('./lib');

const HOST = 'hostdeck.beispiel.test';
const PFLICHT = [[1440, 900, true], [390, 844, true]];
const VOLL = [[1920, 1080, true], [1440, 900, false], [1024, 768, true], [390, 844, false], [320, 700, true]];
const SEITEN = [['lage', '#/lage', null], ['uw-1h', '#/ueberwachung', '1h'], ['uw-1d', '#/ueberwachung', '1d'], ['uw-30d', '#/ueberwachung', '30d']];

// Messbare Punkte aus SK8 je Bild: waagerechtes Rollen, abgeschnittener
// Text, gleich hohe Karten einer Reihe, Seite mittig auf breiten Schirmen,
// Diagramme mit Linie und Mindesthöhe. Überlappung prüft der Blick aufs Bild.
function SK8() {
  const de = document.documentElement;
  const vis = e => { const r = e.getBoundingClientRect(); return r.width > 0 && r.height > 0 && getComputedStyle(e).visibility !== 'hidden'; };
  const cut = [], kurz = [];
  for (const e of document.querySelectorAll('main *, .ls-main *')) {
    if (!e.firstChild || !vis(e) || e.closest('svg')) continue;
    const s = getComputedStyle(e);
    // Nur für Screenreader (class sr: 1 px, clip): kein sichtbarer Text.
    if (e.getBoundingClientRect().width <= 1 || /rect\(0px, 0px, 0px, 0px\)/.test(s.clip)) continue;
    const clips = s.textOverflow === 'ellipsis' || ((s.overflowX === 'hidden' || s.overflow === 'hidden') && s.whiteSpace === 'nowrap');
    const clamp = s.webkitLineClamp && s.webkitLineClamp !== 'none' && e.scrollHeight > e.clientHeight + 1;
    if (!(((clips && e.scrollWidth > e.clientWidth + 1) || clamp) && e.textContent.trim())) continue;
    // Kurzfassungen mit Weg zum ganzen Text („Braucht dich“ auf der
    // Übersicht, Glocke): absichtlich auf zwei Zeilen gekürzt.
    if (e.closest('.ls-att-t, .ls-bellm-t')) { kurz.push(e.textContent.trim().slice(0, 60)); continue; }
    cut.push(e.textContent.trim().slice(0, 60) + (e.title ? ' [Titel]' : ''));
    if (cut.length >= 10) break;
  }
  const eq = [...document.querySelectorAll('.ls-grid.eq')].map(g => {
    const hs = [...g.children].filter(vis).map(c => Math.round(c.getBoundingClientRect().height));
    const row = [...g.children].filter(vis).map(c => Math.round(c.getBoundingClientRect().top));
    return hs.length < 2 || new Set(row).size > 1 || Math.max(...hs) - Math.min(...hs) <= 2;
  });
  const pg = document.querySelector('.ls-page');
  let centered = null;
  if (pg && innerWidth >= 1700) {
    const r = pg.getBoundingClientRect(), par = pg.parentElement.getBoundingClientRect();
    centered = Math.abs((r.left - par.left) - (par.right - r.right)) <= 4;
  }
  const charts = [...document.querySelectorAll('svg.uw-svg')].filter(vis).map(svg => ({ h: Math.round(svg.getBoundingClientRect().height), linie: !!svg.querySelector('path.uw-l') }));
  return { rollt: de.scrollWidth > de.clientWidth + 1, abgeschnitten: cut, kurzfassung: kurz, gleich_hoch: eq.every(Boolean), mittig: centered,
    diagramme: charts.length, diagramme_ok: charts.every(c => c.h >= 60 && c.linie) };
}

(async () => {
  const [zst, dir, voll] = process.argv.slice(2);
  if (!zst || !dir) throw new Error('Aufruf: matrix.js <zustand> <ordner> [voll]');
  fs.mkdirSync(dir, { recursive: true });
  const varianten = voll === 'voll' ? PFLICHT.concat(VOLL) : PFLICHT;
  const seiten = zst === 'f' ? SEITEN.slice(0, 1) : SEITEN;
  const fehler = [];
  const sk8 = {};
  let first = true;
  for (const [w, h, dark] of varianten) {
    const ctx = await L.context({ width: w, height: h, dark, tz: 'Europe/Berlin' });
    await L.login(ctx, HOST, 'leitstand-alt');
    if (first) {
      first = false;
      const dump = {};
      for (const p of ['/api/ueberwachung', '/api/domains/zustand', '/api/lauf', '/api/hinweise', '/api/ueberwachung/grenzen',
        '/api/ueberwachung/verlauf?zeitraum=1d&punkte=144&spitzen=1', '/api/ueberwachung/verlauf?zeitraum=30d&punkte=360&spitzen=1']) {
        const r = await L.api(ctx, HOST, p);
        dump[p] = r.data;
      }
      fs.writeFileSync(path.join(dir, zst + '-api.json'), JSON.stringify(dump, null, 1));
    }
    const page = await ctx.newPage();
    page.on('pageerror', e => fehler.push(w + ' ' + String(e)));
    page.on('console', m => { if (m.type() === 'error') fehler.push(w + ' ' + m.text()); });
    for (const [name, hash, range] of seiten) {
      await L.goto(page, HOST, hash, 3500);
      if (range) {
        const b = page.locator('[data-fid="uw-r-' + range + '"]');
        if (await b.count()) { await b.first().click(); await page.waitForTimeout(3000); }
        else fehler.push(w + ' kein Knopf uw-r-' + range);
      }
      const base = [zst, name, w, dark ? 'dunkel' : 'hell'].join('-');
      const file = path.join(dir, base + '.jpg');
      await page.screenshot({ path: file, fullPage: true, type: 'jpeg', quality: 82 });
      sk8[base] = await page.evaluate(SK8);
      if (name === 'lage' && w === 1440 && dark) {
        fs.writeFileSync(path.join(dir, zst + '-marken-lage.json'), JSON.stringify(await L.marks(page), null, 1));
      }
      if (name === 'uw-1h' && w === 1440 && dark) {
        fs.writeFileSync(path.join(dir, zst + '-marken-ueberwachung.json'), JSON.stringify(await L.marks(page), null, 1));
      }
    }
    await L.close(ctx);
  }
  fs.writeFileSync(path.join(dir, zst + '-sk8.json'), JSON.stringify(sk8, null, 1));
  if (fehler.length) console.log('Fehler: ' + fehler.join(' | '));
  console.log('Zustand ' + zst + ': ' + varianten.length + ' Varianten × ' + seiten.length + ' Seiten in ' + dir);
  process.exit(0);
})().catch(e => { console.error(e); process.exit(1); });

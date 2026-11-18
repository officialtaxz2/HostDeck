// Bedienung von
// Übersicht und Überwachung im echten Browser, dazu Zielgrößen, Fokus und
// Kontraste. Ändert die Grenzen kurz und setzt sie auf den Stand davor
// zurück. Ergebnis als Liste; Exit-Code 1, wenn ein Punkt fehlschlägt.
//   node labor/browser/bedienung.js [host]
'use strict';
const L = require('./lib');

const host = process.argv[2] || 'hostdeck.beispiel.test';
const results = [];
const ok = (name, pass, detail) => { results.push({ name, pass: !!pass, detail: detail || '' }); };

async function pressed(page, fid) { return page.getAttribute('[data-fid="' + fid + '"]', 'aria-pressed'); }

// Kontrast zweier CSS-Farben (WCAG 2.2, relative Leuchtdichte).
function contrast(a, b) {
  const lum = c => {
    const m = /rgba?\(([^)]+)\)/.exec(c);
    if (!m) return null;
    const [r, g, bl] = m[1].split(',').map(x => parseFloat(x) / 255);
    const f = v => v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4);
    return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(bl);
  };
  const x = lum(a), y = lum(b);
  if (x == null || y == null) return null;
  return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
}

(async () => {
  process.env.LAB_TZ = process.env.LAB_TZ || 'Europe/Berlin';
  for (const [width, height] of [[1440, 900], [390, 844]]) {
    const ctx = await L.context({ width, height, dark: true });
    await L.login(ctx, host, 'leitstand-alt');
    const page = await ctx.newPage();
    const errs = [];
    page.on('pageerror', e => errs.push(String(e)));
    // Der Wert außerhalb der Grenzen (unten) bekommt absichtlich 400 vom
    // Dienst; Chromium meldet das als „Failed to load resource“.
    let want400 = false;
    page.on('console', m => {
      if (m.type() !== 'error') return;
      if (want400 && /status of 400/.test(m.text()) && /\/api\/ueberwachung\/grenzen/.test((m.location() || {}).url || '')) return;
      errs.push(m.text() + ((m.location() || {}).url ? ' (' + m.location().url + ')' : ''));
    });
    const W = ' (' + width + ')';

    // Übersicht → Überwachung mit passendem Zeitraum.
    await L.goto(page, host, '#/lage', 4000);
    await page.click('[data-kpi="cpu"]');
    await page.waitForTimeout(1500);
    ok('Kachel Prozessor öffnet 24 Std' + W, page.url().endsWith('#/ueberwachung/1d') && await pressed(page, 'uw-r-1d') === 'true', page.url());
    if (width > 500) {
      await L.goto(page, host, '#/lage', 3000);
      await page.click('.ls-sec:has(.uw-fig) .ls-sec-h a[href="#/ueberwachung/1d"]');
      await page.waitForTimeout(1500);
      ok('Server-Verlauf öffnet 24 Std', page.url().endsWith('#/ueberwachung/1d') && await pressed(page, 'uw-r-1d') === 'true', page.url());
    }

    // Strukturvorschlag: Kachel Domains führt zu Domains; „Alle zeigen“ im
    // Lageplan klappt per Tastatur auf und zu, der Fokus bleibt am Knopf.
    await L.goto(page, host, '#/lage', 4000);
    if (await page.$('[data-kpi="domains"]')) {
      await page.click('[data-kpi="domains"]');
      await page.waitForTimeout(1200);
      ok('Kachel Domains öffnet Domains' + W, /#\/domains$/.test(page.url()), page.url());
      await L.goto(page, host, '#/lage', 3000);
    } else ok('Kachel Domains da' + W, false, 'keine Kachel');
    const fold = await page.$('[data-fid="plan-all"]');
    if (fold) {
      const rowsBefore = await page.$$eval('.ls-plan .ls-pr', x => x.length);
      await page.focus('[data-fid="plan-all"]');
      await page.keyboard.press('Enter');
      await page.waitForTimeout(600);
      const st1 = await page.evaluate(() => ({ exp: document.querySelector('[data-fid="plan-all"]').getAttribute('aria-expanded'), focus: document.activeElement && document.activeElement.dataset.fid, rows: document.querySelectorAll('.ls-plan .ls-pr').length }));
      ok('Lageplan „Alle zeigen“ per Tastatur' + W, st1.exp === 'true' && st1.focus === 'plan-all' && st1.rows > rowsBefore, JSON.stringify(st1) + ' vorher ' + rowsBefore);
      await page.keyboard.press('Space');
      await page.waitForTimeout(600);
      const st2 = await page.evaluate(() => ({ exp: document.querySelector('[data-fid="plan-all"]').getAttribute('aria-expanded'), focus: document.activeElement && document.activeElement.dataset.fid, rows: document.querySelectorAll('.ls-plan .ls-pr').length }));
      ok('Lageplan „Zusammenfassen“ per Tastatur' + W, st2.exp === 'false' && st2.focus === 'plan-all' && st2.rows === rowsBefore, JSON.stringify(st2));
    } else ok('Lageplan fasst Projekte ohne Domain zusammen' + W, false, 'kein Knopf');
    await L.goto(page, host, '#/ueberwachung/1d', 4000);

    // Zeitraum-Knöpfe schreiben die Adresse; Neuladen behält den Zeitraum.
    await page.click('[data-fid="uw-r-7d"]');
    await page.waitForTimeout(2500);
    ok('Zeitraum 7 Tage in der Adresse' + W, page.url().endsWith('#/ueberwachung/7d') && await pressed(page, 'uw-r-7d') === 'true', page.url());
    await page.reload();
    await page.waitForTimeout(4000);
    ok('Neuladen behält 7 Tage' + W, await pressed(page, 'uw-r-7d') === 'true');

    // Live ist das erste Feld der Zeitraum-Leiste .
    await page.click('[data-fid="uw-r-live"]');
    await page.waitForTimeout(1500);
    const liveOn = await pressed(page, 'uw-r-live') === 'true' && page.url().endsWith('#/ueberwachung/live');
    await page.click('[data-fid="uw-r-15m"]');
    await page.waitForTimeout(1500);
    ok('Live an und aus' + W, liveOn && await pressed(page, 'uw-r-live') === 'false');
    await page.click('[data-fid="uw-r-1d"]');
    await page.waitForTimeout(2500);

    // Zoom per Ziehen und per Tastatur (+, −, Escape).
    const svg = page.locator('svg.uw-svg.zoomable').first();
    if (width > 500) {
      const b = await svg.boundingBox();
      await page.mouse.move(b.x + b.width * 0.3, b.y + b.height * 0.5);
      await page.mouse.down();
      await page.mouse.move(b.x + b.width * 0.6, b.y + b.height * 0.5, { steps: 8 });
      await page.mouse.up();
      await page.waitForTimeout(800);
      const zoomed = await page.locator('[data-fid="uw-unzoom"]').count() === 1;
      if (zoomed) await page.click('[data-fid="uw-unzoom"]');
      await page.waitForTimeout(500);
      ok('Zoom per Ziehen und zurück', zoomed && await page.locator('[data-fid="uw-unzoom"]').count() === 0);
    }
    await svg.focus();
    await page.keyboard.press('+');
    await page.waitForTimeout(600);
    const kz = await page.locator('[data-fid="uw-unzoom"]').count() === 1;
    const kept = await page.evaluate(() => document.activeElement && document.activeElement.classList.contains('uw-svg'));
    await page.keyboard.press('-');
    await page.waitForTimeout(600);
    const kout = await page.locator('[data-fid="uw-unzoom"]').count() === 0;
    await page.keyboard.press('+');
    await page.waitForTimeout(400);
    await page.keyboard.press('Escape');
    await page.waitForTimeout(600);
    ok('Zoom per Tastatur (+, −, Escape), Fokus bleibt' + W, kz && kept && kout && await page.locator('[data-fid="uw-unzoom"]').count() === 0);
    await page.keyboard.press('ArrowLeft');
    // Anzeige der Werte: Zahl da, kein Programmwort („null“).
    const junk = /\b(null|undefined|NaN)\b/;
    const read = await page.locator('.uw-read').first().innerText();
    ok('Pfeiltaste zeigt Werte' + W, /\d/.test(read) && !junk.test(read), read.replace(/\s+/g, ' ').slice(0, 120));
    const box = await svg.boundingBox();
    await page.mouse.move(box.x + box.width * 0.4, box.y + box.height / 2);
    await page.waitForTimeout(300);
    const hov = await page.$$eval('.uw-read', xs => xs.map(x => x.innerText));
    ok('Zeiger zeigt Werte ohne Programmwort' + W, hov.length > 0 && /\d/.test(hov[0]) && !hov.some(t => junk.test(t)), hov[0].replace(/\s+/g, ' ').slice(0, 120));

    // Fokus sichtbar auf dem Diagramm.
    await svg.focus();
    const ring = await svg.evaluate(e => { const s = getComputedStyle(e); return [s.outlineStyle, s.outlineWidth, s.boxShadow]; });
    ok('Fokus auf dem Diagramm sichtbar' + W, (ring[0] !== 'none' && parseFloat(ring[1]) >= 2) || (ring[2] && ring[2] !== 'none'), ring.join(' '));

    // Sortieren: groß gezeigte Zahl absteigend, Name aufsteigend.
    const order = async () => page.$$eval('.uw-row[data-gruppe]', xs => xs.map(x => x.textContent.trim()));
    await page.click('[data-fid="uw-s-name"]');
    await page.waitForTimeout(600);
    const names = await page.$$eval('[data-fid^="uw-g-"] b', xs => xs.map(x => x.textContent.trim()));
    const sorted = names.slice().sort((a, b) => a.localeCompare(b, 'de'));
    ok('Sortieren nach Name' + W, await pressed(page, 'uw-s-name') === 'true' && names.length > 1 && names.join('|') === sorted.join('|'), names.join(', '));
    await page.click('[data-fid="uw-s-ram"]');
    await page.waitForTimeout(600);
    ok('Sortieren nach Arbeitsspeicher' + W, await pressed(page, 'uw-s-ram') === 'true' && (await order()).length > 1);
    await page.click('[data-fid="uw-s-cpu"]');
    await page.waitForTimeout(600);

    // Projekte auf- und zuklappen, auch per Tastatur.
    const tg = page.locator('[data-fid^="uw-g-"]').first();
    const fid = await tg.getAttribute('data-fid');
    await tg.focus();
    await page.keyboard.press('Enter');
    await page.waitForTimeout(500);
    const open = await page.getAttribute('[data-fid="' + fid + '"]', 'aria-expanded') === 'true';
    await page.keyboard.press('Enter');
    await page.waitForTimeout(500);
    ok('Projekt auf- und zuklappen per Tastatur' + W, open && await page.getAttribute('[data-fid="' + fid + '"]', 'aria-expanded') === 'false');

    // Zielgrößen (WCAG 2.5.8: mindestens 24 × 24 px).
    const small = await page.$$eval('[data-fid^="uw-r-"], [data-fid="lv-pause"], [data-fid^="uw-s-"], [data-fid="uw-ref"], [data-fid^="uw-g-"], [data-fid="uw-lim-edit"]',
      xs => xs.map(x => [x.getAttribute('data-fid'), x.getBoundingClientRect()]).filter(([, r]) => r.width && (r.width < 24 || r.height < 24)).map(([f, r]) => f + ' ' + Math.round(r.width) + '×' + Math.round(r.height)));
    ok('Zielgrößen ≥ 24 px' + W, small.length === 0, small.join(', '));

    // Kontraste der Tagesbalken gegen die Karte (≥ 3:1, 1.4.11).
    const bars = await page.evaluate(() => {
      const out = {};
      for (const k of ['ok', 'warn', 'err', 'pz', 'na']) {
        const i = document.querySelector('.ls-upb i.' + k);
        if (!i) continue;
        let p = i.parentElement, bg = 'rgba(0, 0, 0, 0)';
        while (p && (bg === 'rgba(0, 0, 0, 0)' || bg === 'transparent')) { bg = getComputedStyle(p).backgroundColor; p = p.parentElement; }
        const s = getComputedStyle(i);
        // Farbe: Fläche, sonst die erste Farbe eines Verlaufs (Punkt „na“),
        // sonst der Umriss (box-shadow, Pause „pz“).
        const grad = (s.backgroundImage.match(/rgba?\([^)]*\)/) || [])[0];
        const ring = (s.boxShadow.match(/rgba?\([^)]*\)/) || [])[0];
        out[k] = [s.backgroundColor !== 'rgba(0, 0, 0, 0)' ? s.backgroundColor : grad || ring || s.borderTopColor, bg, s.backgroundImage];
      }
      return out;
    });
    for (const [k, [fg, bg, img]] of Object.entries(bars)) {
      const c = contrast(fg, bg);
      ok('Tagesbalken „' + k + '“ Kontrast ≥ 3:1' + W, c != null && c >= 3, (c ? c.toFixed(2) : '?') + ' ' + fg + ' auf ' + bg + (img && img !== 'none' ? ' (Muster)' : ''));
    }

    // Grenzen ändern: ungültige Eingabe, Wert außerhalb, gültig, zurück.
    if (width > 500) {
      const before = (await L.api(ctx, host, '/api/ueberwachung', null)).data;
      const g0 = before.warnungen.grenzen.prozessor_gelb;
      await page.click('[data-fid="uw-lim-edit"]');
      await page.waitForTimeout(500);
      await page.fill('[data-fid="uw-l-prozessor_gelb"]', 'abc');
      await page.click('[data-fid="uw-lim-save"]');
      await page.waitForTimeout(600);
      const msg1 = await page.locator('.op-res.is-err').innerText().catch(() => '');
      const foc = await page.evaluate(() => document.activeElement && document.activeElement.getAttribute('data-fid'));
      ok('Grenzen: keine Zahl → Meldung, Fokus im Feld', /keine Zahl/.test(msg1) && foc === 'uw-l-prozessor_gelb', msg1 + ' · Fokus ' + foc);
      await page.fill('[data-fid="uw-l-prozessor_gelb"]', '10');
      want400 = true;
      await page.click('[data-fid="uw-lim-save"]');
      await page.waitForTimeout(1500);
      want400 = false;
      const msg2 = await page.locator('.op-res.is-err').innerText().catch(() => '');
      ok('Grenzen: Wert außerhalb → Meldung vom Dienst', msg2.length > 0 && await page.locator('[data-fid="uw-lim-save"]').count() === 1, msg2);
      await page.fill('[data-fid="uw-l-prozessor_gelb"]', String(g0 === 95 ? 90 : 95));
      await page.click('[data-fid="uw-lim-save"]');
      await page.waitForTimeout(2000);
      const msg3 = await page.locator('.op-res.is-ok').innerText().catch(() => '');
      ok('Grenzen: gültig → übernommen', /Übernommen/.test(msg3), msg3);
      await page.click('[data-fid="uw-lim-edit"]');
      await page.waitForTimeout(500);
      await page.fill('[data-fid="uw-l-prozessor_gelb"]', String(g0));
      await page.click('[data-fid="uw-lim-save"]');
      await page.waitForTimeout(2000);
      const after = (await L.api(ctx, host, '/api/ueberwachung', null)).data;
      ok('Grenzen zurückgesetzt', after.warnungen.grenzen.prozessor_gelb === g0, String(after.warnungen.grenzen.prozessor_gelb));
    }

    // Aktualisieren.
    await page.click('[data-fid="uw-ref"]');
    await page.waitForTimeout(2500);
    ok('Aktualisieren' + W, await page.locator('[data-fid="uw-ref"]').isEnabled());

    // Seiten ohne Konsolenfehler.
    for (const hash of ['#/lage', '#/projekte', '#/domains', '#/leistung', '#/platz', '#/backup', '#/ueberwachung/30d']) {
      await L.goto(page, host, hash, 3000);
    }
    ok('Konsole ohne Fehler' + W, errs.length === 0, errs.slice(0, 3).join(' | '));
    await L.close(ctx);
  }
  const bad = results.filter(r => !r.pass);
  for (const r of results) console.log((r.pass ? 'ok   ' : 'FEHL ') + r.name + (r.detail ? ' · ' + r.detail : ''));
  console.log(bad.length ? bad.length + ' von ' + results.length + ' Punkten fehlgeschlagen.' : 'Alle ' + results.length + ' Punkte ok.');
  process.exit(bad.length ? 1 : 0);
})().catch(e => { console.error(e); process.exit(2); });

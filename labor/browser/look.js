// Bildschirmfoto mit gewählter Darstellung (Einstellungen › Darstellung).
//   node labor/browser/look.js <host> '#/lage' <datei.png> [breite] [json] [scrollY] [speicher]
// json etwa '{"theme":"light","accent":"gruen"}'; scrollY > 0 macht ein
// Bild des Fensters nach dem Scrollen statt der ganzen Seite. speicher
// legt weitere Einträge in localStorage, etwa '{"hostdeck-erste-schritte":"aus"}'.
'use strict';
const L = require('./lib');
(async () => {
  const [host, hash, file, w, json, sy, more] = process.argv.slice(2);
  const width = Number(w || 1440);
  const look = JSON.parse(json || '{}');
  const ctx = await L.context({ width, height: width > 500 ? 900 : 844, dark: look.theme !== 'light' });
  await ctx.addInitScript(([v, extra]) => {
    try { localStorage.setItem('hostdeck-darstellung', v); for (const [k, x] of Object.entries(extra)) localStorage.setItem(k, x); } catch (e) { /* */ }
  }, [JSON.stringify(look), JSON.parse(more || '{}')]);
  await L.login(ctx, host, host.startsWith('hostdeck.') ? 'leitstand-alt' : 'leitstand-neu');
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', e => errs.push(String(e)));
  page.on('console', m => { if (m.type() === 'error') errs.push(m.text()); });
  // Der Dienst soll die Wahl aus dem Bild nicht überschreiben.
  await page.route('**/api/darstellung', r => r.request().method() === 'GET' ? r.fulfill({ contentType: 'application/json', body: '{"darstellung":null}' }) : r.fulfill({ contentType: 'application/json', body: '{}' }));
  await L.goto(page, host, hash, 4000);
  if (Number(sy) > 0) {
    await page.evaluate(y => window.scrollTo(0, y), Number(sy));
    await page.waitForTimeout(400);
    await page.screenshot({ path: file });
  } else await page.screenshot({ path: file, fullPage: true });
  if (errs.length) console.log('JS-Fehler: ' + errs.join(' | '));
  await L.close(ctx);
  process.exit(0);
})().catch(e => { console.error(e); process.exit(1); });

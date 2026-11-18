// Bildschirmfoto einer Seite des Leitstands (für Prüfung und Bericht).
//   node labor/browser/bild.js <host> '#/umzug' <datei.png> [breite] [hell]
'use strict';
const L = require('./lib');
(async () => {
  const [host, hash, file, w, light] = process.argv.slice(2);
  const width = Number(w || 1440);
  const ctx = await L.context({ width, height: width > 500 ? 900 : 844, dark: light !== 'hell' });
  await L.login(ctx, host, host.startsWith('hostdeck.') ? 'leitstand-alt' : 'leitstand-neu');
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', e => errs.push(String(e)));
  page.on('console', m => { if (m.type() === 'error') errs.push(m.text()); });
  await L.goto(page, host, hash, 4000);
  await page.screenshot({ path: file, fullPage: true });
  if (errs.length) console.log('JS-Fehler: ' + errs.join(' | '));
  await L.close(ctx);
  process.exit(0);
})().catch(e => { console.error(e); process.exit(1); });

// Führt eine Folge von Schritten im Leitstand aus, wie ein Mensch mit der
// Maus: Seite öffnen, Knopf klicken, tippen, Bild machen.
//   node labor/browser/klick.js <host> '<json>'   (Liste von Schritten)
// Schritte: {"geh":"#/umzug"}, {"klick":"text=Zur Auswahl"} (Playwright-
// Selektor), {"tipp":["#mv-pw","geheim"]}, {"warte":2000}, {"bild":"datei.png"},
// {"text":"#lt-now-h"} (gibt den Text aus), {"breite":390}
'use strict';
const L = require('./lib');
(async () => {
  const [host, json, light] = process.argv.slice(2);
  const steps = JSON.parse(json);
  const w0 = (steps.find(s => s.breite) || {}).breite || 1440;
  const ctx = await L.context({ width: w0, height: w0 > 500 ? 900 : 844, dark: light !== 'hell' });
  await L.login(ctx, host, host.startsWith('hostdeck.') ? 'leitstand-alt' : 'leitstand-neu');
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', e => errs.push(String(e)));
  page.on('console', m => { if (m.type() === 'error') errs.push(m.text()); });
  for (const s of steps) {
    if (s.geh) await L.goto(page, host, s.geh, s.ms || 3000);
    if (s.klick) { await page.locator(s.klick).first().click({ timeout: 10000 }); await page.waitForTimeout(s.ms || 800); }
    if (s.tipp) { await page.locator(s.tipp[0]).first().fill(s.tipp[1]); }
    if (s.warte) await page.waitForTimeout(s.warte);
    if (s.bild) await page.screenshot({ path: s.bild, fullPage: true });
    if (s.text) console.log(s.text + ': ' + (await page.locator(s.text).first().innerText().catch(e => '(fehlt) ' + e.message.split('\n')[0])).replace(/\s+/g, ' '));
    if (s.bestaetige) await L.confirm(ctx, host, host.startsWith('hostdeck.') ? 'leitstand-alt' : 'leitstand-neu');
  }
  if (errs.length) console.log('JS-Fehler: ' + errs.join(' | '));
  await L.close(ctx);
  process.exit(0);
})().catch(e => { console.error(String(e)); process.exit(1); });

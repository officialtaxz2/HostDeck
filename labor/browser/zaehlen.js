// Zählt auf Seiten des Leitstands, wie viel auf einmal zu sehen ist
// („nicht unübersichtlicher“): Hauptknöpfe (.btn-p im
// Hauptbereich, Dialoge ausgenommen) und sichtbare Abschnitte (section.ls-sec
// plus offene details), in 1440 und 390 px.
//   node labor/browser/zaehlen.js <host> '#/umzug,#/backup' [--ui <git-ref>]
// Mit --ui liefert der Browser die Oberfläche (index.html, leitstand.js,
// app.js, app.css, theme.js) aus diesem Git-Stand statt vom Server; so
// misst man den alten Stand im selben Labor-Zustand.
'use strict';
const { execFileSync } = require('child_process');
const path = require('path');
const L = require('./lib');

const args = process.argv.slice(2);
const host = args[0];
const hashes = (args[1] || '#/umzug').split(',');
const ui = args.indexOf('--ui') >= 0 ? args[args.indexOf('--ui') + 1] : '';
const REPO = path.join(__dirname, '..', '..');
const FILES = ['index.html', 'leitstand.js', 'app.js', 'app.css', 'theme.js'];

function fromGit(ref, f) {
  return execFileSync('git', ['show', ref + ':internal/web/static/' + f], { cwd: REPO, maxBuffer: 16 << 20 });
}

(async () => {
  const out = { ui: ui || 'Server', seiten: {} };
  for (const w of [1440, 390]) {
    const ctx = await L.context({ width: w, height: w > 500 ? 900 : 844, dark: w > 500 });
    if (ui) {
      const files = Object.fromEntries(FILES.map(f => [f, fromGit(ui, f)]));
      await ctx.route('**/*', (route) => {
        const u = new URL(route.request().url());
        const f = u.pathname === '/' ? 'index.html' : u.pathname.slice(1);
        if (files[f]) {
          const type = f.endsWith('.js') ? 'text/javascript' : f.endsWith('.css') ? 'text/css' : 'text/html; charset=utf-8';
          return route.fulfill({ status: 200, contentType: type, body: files[f] });
        }
        return route.continue();
      });
      await ctx.addInitScript(() => { try { navigator.serviceWorker.register = () => Promise.reject(new Error('aus')); } catch (e) { /* egal */ } });
    }
    await L.login(ctx, host, host.startsWith('hostdeck.') ? 'leitstand-alt' : 'leitstand-neu');
    const page = await ctx.newPage();
    for (const h of hashes) {
      await L.goto(page, host, h, 4000);
      const n = await page.evaluate(() => {
        const vis = e => !!(e.offsetWidth || e.offsetHeight || e.getClientRects().length) && getComputedStyle(e).visibility !== 'hidden';
        const main = document.querySelector('main') || document.body;
        const inDlg = e => !!e.closest('dialog, [role=dialog], .modal');
        const btnP = [...main.querySelectorAll('.btn-p')].filter(e => vis(e) && !inDlg(e));
        const secs = [...main.querySelectorAll('section.ls-sec')].filter(e => vis(e) && !inDlg(e));
        const det = [...main.querySelectorAll('details[open]')].filter(e => vis(e) && !inDlg(e));
        return { hauptknoepfe: btnP.length, knoepfe: btnP.map(e => (e.innerText || '').trim()).slice(0, 6), abschnitte: secs.length + det.length, sec: secs.length, offen: det.length };
      });
      out.seiten[w + ' ' + h] = n;
    }
    await L.close(ctx);
  }
  console.log(JSON.stringify(out, null, 1));
  process.exit(0);
})().catch(e => { console.error(String(e)); process.exit(1); });

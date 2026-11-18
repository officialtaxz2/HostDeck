// Werte je Projekt und Container nachbilden und alle Seiten nach kaputtem
// Text absuchen.
//   node labor/browser/werte.js [host] [--ui <git-stand>]
// Das Labor misst je Container nichts (cgroup v1). Ohne diese Werte bleibt
// ein ganzer Zweig der Oberfläche unsichtbar: Auf einem echten Server stand
// dort einmal „[object HTMLElement]“. Das Skript reichert die Antworten der
// Überwachung im Browser an (Stand, Verlauf, Live), geht alle Seiten durch,
// bleibt 8 Sekunden in Live und meldet „[object“, „undefined“, „NaN“ und
// JavaScript-Fehler. Ausgang 1, wenn etwas gefunden wurde. Mit --ui kommt
// die Oberfläche aus einem Git-Stand (wie bei zaehlen.js); so lässt sich
// zeigen, dass die Prüfung einen alten Fehler findet (etwa --ui 30f8eef).
'use strict';
const { execFileSync } = require('child_process');
const path = require('path');
const L = require('./lib');
const REPO = path.join(__dirname, '..', '..');
const FILES = ['index.html', 'leitstand.js', 'app.js', 'app.css', 'theme.js'];

const PAGES = ['lage', 'projekte', 'projekte/gruppe:spiel', 'projekte/gruppe:web', 'domains', 'ueberwachung/1h', 'ueberwachung/1d', 'ueberwachung/30d',
  'leistung', 'backup', 'auswahl', 'umzug', 'platz', 'sicherheit', 'protokoll', 'updates', 'zugang', 'einstellungen', 'ueberwachung/live'];
const BAD = /\[object|undefined|NaN/;

(async () => {
  const args = process.argv.slice(2);
  const ui = args.indexOf('--ui') >= 0 ? args[args.indexOf('--ui') + 1] : '';
  const host = args[0] && !args[0].startsWith('--') ? args[0] : 'hostdeck.beispiel.test';
  const ctx = await L.context({ width: 1440, height: 900, dark: true });
  if (ui) {
    const files = Object.fromEntries(FILES.map(f => [f, execFileSync('git', ['show', ui + ':internal/web/static/' + f], { cwd: REPO, maxBuffer: 16 << 20 })]));
    await ctx.route('**/*', route => {
      const u = new URL(route.request().url());
      const f = u.pathname === '/' ? 'index.html' : u.pathname.slice(1);
      if (!files[f]) return route.continue();
      const type = f.endsWith('.js') ? 'text/javascript' : f.endsWith('.css') ? 'text/css' : 'text/html; charset=utf-8';
      return route.fulfill({ status: 200, contentType: type, body: files[f] });
    });
    await ctx.addInitScript(() => { try { navigator.serviceWorker.register = () => Promise.reject(new Error('aus')); } catch (e) { /* egal */ } });
  }
  await L.login(ctx, host, 'leitstand-alt');
  await ctx.addInitScript(() => {
    const of = window.fetch;
    const rnd = () => 0.5 + Math.random() * 3;
    // Namen aus dem Stand der Überwachung: Projekte und Container.
    let names = { p: [], c: [] };
    const add = v => {
      for (const p of names.p) { v['p/' + p + '/cpu'] = rnd(); v['p/' + p + '/ram'] = 1e8 * rnd(); }
      for (const c of names.c) { v['c/' + c + '/cpu'] = rnd(); v['c/' + c + '/ram'] = 5e7 * rnd(); }
    };
    window.fetch = async (u, o) => {
      const res = await of(u, o);
      const url = String(u);
      if (!url.includes('/api/ueberwachung') || !res.ok) return res;
      let j;
      try { j = await res.clone().json(); } catch (e) { return res; }
      if (/\/api\/ueberwachung(\?|$)/.test(url) && j.info) {
        const cs = j.info.container || [];
        names = { p: [...new Set(cs.map(c => c.projekt).filter(Boolean))], c: cs.map(c => c.name) };
        add(j.werte || (j.werte = {}));
      } else if (url.includes('/verlauf') && j.zeiten && j.serien) {
        const n = j.zeiten.length;
        const v = {};
        add(v);
        for (const k of Object.keys(v)) j.serien[k] = Array.from({ length: n }, () => v[k] * (0.5 + Math.random()));
      } else if (url.includes('/live') && Array.isArray(j.messungen)) {
        for (const f of j.messungen) add(f.v || (f.v = {}));
      } else return res;
      window.__werte = (window.__werte || 0) + 1;
      return new Response(JSON.stringify(j), { status: res.status, headers: res.headers });
    };
  });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', e => errs.push(String(e)));
  page.on('console', m => { if (m.type() === 'error') errs.push(m.text()); });
  let bad = 0;
  for (const p of PAGES) {
    errs.length = 0;
    await L.goto(page, host, '#/' + p, p.endsWith('live') ? 8000 : 3500);
    const found = await page.evaluate(re => {
      const out = [];
      const w = document.createTreeWalker(document.querySelector('main') || document.body, NodeFilter.SHOW_TEXT);
      for (let n = w.nextNode(); n; n = w.nextNode()) if (new RegExp(re).test(n.nodeValue)) out.push(n.nodeValue.trim().slice(0, 80));
      for (const e of document.querySelectorAll('[aria-label],[title]')) for (const a of ['aria-label', 'title']) { const v = e.getAttribute(a); if (v && new RegExp(re).test(v)) out.push(a + ': ' + v.slice(0, 80)); }
      return { out, werte: window.__werte || 0 };
    }, BAD.source);
    const ok = !found.out.length && !errs.length;
    if (!ok) bad++;
    console.log((ok ? 'ok    ' : 'FEHLER') + ' #/' + p + ' (angereichert: ' + found.werte + ')' + (found.out.length ? ' Text: ' + found.out.join(' | ') : '') + (errs.length ? ' JS: ' + errs.join(' | ') : ''));
  }
  await L.close(ctx);
  process.exit(bad ? 1 : 0);
})().catch(e => { console.error(e); process.exit(1); });

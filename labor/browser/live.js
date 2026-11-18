// Gate 3: Live und
// Spielserver im echten Browser. Prüft den Zustand neben dem Titel, dass
// neue Werte spätestens nach 2 Sekunden sichtbar sind, dass die Maus nichts
// anhält, Anhalten und Weiter, das Neuladen von #/ueberwachung/live, die
// Karten der Spielserver (labor/spiele.sh) und „Gerade aktiv“. Für „Gerade
// aktiv“ ergänzt ein zweiter Durchgang Werte je Container in der Antwort,
// weil das Labor (cgroup v1) keine misst. Exit-Code 1 bei einem Fehler.
//   node labor/browser/live.js [host]
'use strict';
const L = require('./lib');

const host = process.argv[2] || 'hostdeck.beispiel.test';
const results = [];
const ok = (name, pass, detail) => { results.push({ name, pass: !!pass, detail: detail || '' }); };
const text = (page, sel) => page.$eval(sel, e => e.textContent.trim()).catch(() => '');

(async () => {
  for (const [width, height] of [[1440, 900], [390, 844]]) {
    const W = ' (' + width + ' px)';
    const ctx = await L.context({ width, height, dark: width > 500 });
    await L.login(ctx, host, 'leitstand-alt');
    const page = await ctx.newPage();
    const errs = [];
    page.on('pageerror', e => errs.push(String(e)));
    page.on('console', m => { if (m.type() === 'error') errs.push(m.text()); });
    let asks = 0;
    page.on('request', r => { if (r.url().includes('/api/ueberwachung/live')) asks++; });
    await L.goto(page, host, '#/ueberwachung/live', 3000);

    ok('Live gewählt' + W, await page.getAttribute('[data-fid="uw-r-live"]', 'aria-pressed') === 'true');
    // Bis die schnellen Messungen kommen, dauert es eine Sekunde oder zwei.
    for (let i = 0; i < 10 && (await page.getAttribute('.lv-state', 'data-zustand')) !== 'on'; i++) await L.sleep(500);
    ok('Zustand „Live“ mit Alter' + W, (await page.getAttribute('.lv-state', 'data-zustand')) === 'on' && /^Live\s*vor \d+ s$/.test(await text(page, '.lv-state')), await text(page, '.lv-state'));
    ok('Zustand angesagt (role status)' + W, (await text(page, '.ls-head [role="status"]')) === 'Live');
    ok('Kacheln mit Wert jetzt' + W, (await page.$$('.lv-kpis .ls-kpi')).length === 5 && /Prozessor jetzt \d/.test(await page.getAttribute('[data-kpi="cpu"]', 'aria-label') || ''));
    ok('Wert am Ende der Linie' + W, (await page.$$('.lv-svg .uw-end')).length >= 4);
    // Neue Werte: Der letzte Zeitpunkt rückt binnen 2 Sekunden weiter.
    const n0 = asks;
    const end0 = await page.$eval('[data-fid="lv-cpu"] .uw-end', e => e.textContent).catch(() => '');
    await L.sleep(2200);
    ok('fragt jede Sekunde' + W, asks - n0 >= 2, (asks - n0) + ' Abfragen in 2,2 s');
    const st1 = await text(page, '.lv-state');
    ok('Alter zählt und bleibt frisch' + W, /vor [123] s/.test(st1), st1);
    // Maus auf dem Diagramm hält nichts an: Die Abfragen laufen weiter.
    await page.$eval('[data-fid="lv-cpu"]', e => e.scrollIntoView({ block: 'center' }));
    const box = await page.$eval('[data-fid="lv-cpu"]', e => { const r = e.getBoundingClientRect(); return { x: r.left + (r.width - 64 * r.width / (e.viewBox.baseVal.width || r.width)) * 0.98, y: r.top + r.height / 2 }; });
    await page.mouse.move(box.x, box.y);
    const n1 = asks;
    await L.sleep(2200);
    const read = await text(page, '[data-fid="lv-cpu"] ~ .lv-cap, .lv-fig .uw-read');
    ok('Maus hält nichts an' + W, asks - n1 >= 2 && (await page.getAttribute('.lv-state', 'data-zustand')) === 'on', (asks - n1) + ' Abfragen');
    ok('Lesezeile zeigt die gewählte Zeit' + W, /\d\d:\d\d:\d\d/.test(read), read.slice(0, 80));
    await page.mouse.move(2, 2);
    // Anhalten hält alles: keine Abfrage, Kacheln stehen.
    await page.click('[data-fid="lv-pause"]');
    const kpi0 = await page.getAttribute('[data-kpi="cpu"]', 'aria-label');
    const n2 = asks;
    await L.sleep(2500);
    ok('Anhalten: grau mit Uhrzeit' + W, (await page.getAttribute('.lv-state', 'data-zustand')) === 'paused' && /Angehalten\s*um \d\d:\d\d:\d\d/.test(await text(page, '.lv-state')), await text(page, '.lv-state'));
    ok('Anhalten: keine Abfrage, Kacheln stehen' + W, asks === n2 && kpi0 === await page.getAttribute('[data-kpi="cpu"]', 'aria-label'));
    await page.click('[data-fid="lv-pause"]');
    await L.sleep(1800);
    ok('Weiter: fragt wieder' + W, asks > n2 && (await page.getAttribute('.lv-state', 'data-zustand')) === 'on' && end0 !== '');
    // Neuladen behält Live.
    await page.reload();
    await L.sleep(3500);
    ok('#/ueberwachung/live überlebt Neuladen' + W, await page.getAttribute('[data-fid="uw-r-live"]', 'aria-pressed') === 'true');

    // Spielserver aus labor/spiele.sh: vier laufen, einer ist aus.
    const cards = await page.$$eval('.gs', es => es.map(e => ({ name: e.querySelector('h3').textContent, z: e.dataset.zustand, off: e.classList.contains('off'), t: e.textContent })));
    const by = n => cards.find(c => c.name === n);
    ok('Spielserver: fünf Karten' + W, cards.length === 5, cards.map(c => c.name).join(', '));
    ok('Minecraft im Panel mit Spielern' + W, by('Survival mit Freunden') && /3 von 20 Spielern/.test(by('Survival mit Freunden').t) && /im Panel/.test(by('Survival mit Freunden').t));
    ok('Left 4 Dead 2 mit Karte' + W, by('Labor Coop') && /Left 4 Dead 2/.test(by('Labor Coop').t) && /Karte c1m1_hotel/.test(by('Labor Coop').t));
    ok('Standardname durch Welt ersetzt' + W, !!by('Welt kreativ'));
    ok('Compose-Minecraft als Projekt' + W, by('Compose Welt') && /Projekt minecraft/.test(by('Compose Welt').t));
    ok('Gestoppter Server gestrichelt „aus“' + W, by('Altes Event') && by('Altes Event').off && /startest du im Panel/.test(by('Altes Event').t));
    ok('Kachel Spielserver' + W, /4 von 5 laufen/.test(await page.getAttribute('[data-kpi="gs"]', 'aria-label') || ''), await page.getAttribute('[data-kpi="gs"]', 'aria-label'));
    // Gerade aktiv ohne Messung je Container: laufende Gruppen, Spielserver einzeln.
    const rows = await page.$$eval('.lv-list > li[data-gruppe]', es => es.map(e => e.dataset.gruppe));
    ok('Gerade aktiv: Spielserver einzeln' + W, rows.some(k => k === 'g:0f3a9c1e-3333-4a6b-9c1d-000000000003') && rows.includes('p:minecraft') && !rows.some(k => k.startsWith('g:minecraft')), rows.join(' '));
    ok('Kein seitliches Scrollen' + W, await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1));
    ok('keine JS-Fehler' + W, !errs.length, errs.join(' | '));
    await L.close(ctx);
  }

  // Zweiter Durchgang: Werte je Container in der Antwort (wie auf einem
  // Server mit cgroup v2), damit „Gerade aktiv“ sortiert und Balken zeigt.
  {
    const ctx = await L.context({ width: 1440, height: 900, dark: false });
    await L.login(ctx, host, 'leitstand-alt');
    const page = await ctx.newPage();
    // Im Browser selbst, weil die Namen des Labors nur dort aufgelöst werden.
    await page.addInitScript(() => {
      const f0 = window.fetch;
      window.fetch = async (u, o) => {
        const r = await f0(u, o);
        if (!String(u).includes('/api/ueberwachung/live')) return r;
        const d = await r.clone().json();
        for (const f of d.messungen || []) {
          f.v['c/0f3a9c1e-3333-4a6b-9c1d-000000000003/cpu'] = 12; f.v['c/0f3a9c1e-3333-4a6b-9c1d-000000000003/ram'] = 2.1e9;
          f.v['c/0f3a9c1e-5555-4a6b-9c1d-000000000005/cpu'] = 4; f.v['c/0f3a9c1e-5555-4a6b-9c1d-000000000005/ram'] = 0.8e9;
          f.v['p/app/cpu'] = 1.5; f.v['p/app/ram'] = 0.3e9;
        }
        return new Response(JSON.stringify(d), { status: r.status, headers: r.headers });
      };
    });
    await L.goto(page, host, '#/ueberwachung/live', 4500);
    const rows = await page.$$eval('.lv-list > li[data-gruppe]', es => es.map(e => [e.dataset.gruppe, e.dataset.wert, e.querySelector('.num b').textContent]));
    ok('Gerade aktiv: nach Prozessor jetzt', rows.length >= 3 && rows[0][0] === 'g:0f3a9c1e-3333-4a6b-9c1d-000000000003' && rows[1][0] === 'g:0f3a9c1e-5555-4a6b-9c1d-000000000005' && rows[2][0] === 'p:app', JSON.stringify(rows.slice(0, 4)));
    ok('Gerade aktiv: Abzeichen „Spiel“', await page.$eval('.lv-list > li[data-gruppe^="g:"] .gs-tag', e => !e.hidden).catch(() => false));
    // Aufklappen eines Projekts zeigt seine Container.
    const btn = await page.$('.lv-list [data-fid="lv-g-p:app"]');
    if (btn) { await btn.click(); await L.sleep(1200); }
    ok('Projekt klappt auf', btn && (await page.$$('.lv-list > li.sub')).length >= 1);
    await page.screenshot({ path: L.LAUF + '/bilder/live-aktiv-1440-hell.png', fullPage: false });
    await L.close(ctx);
  }

  // Zeiträume: Der Wert am Linienende ist „Zuletzt“, nicht das
  // Mittel des laufenden Punkts.
  for (const rng of ['6h', '30d']) {
    const ctx = await L.context({ width: 1440, height: 900, dark: false });
    await L.login(ctx, host, 'leitstand-alt');
    const page = await ctx.newPage();
    await L.goto(page, host, '#/ueberwachung/' + rng, 4000);
    const end = await page.$eval('[data-fid="uw-cpu"] .uw-end', e => e.textContent.trim()).catch(() => '');
    const last = await page.evaluate(() => {
      const svg = document.querySelector('[data-fid="uw-cpu"]');
      const fig = svg && svg.closest('section, figure, .ls-card, div');
      const sp = fig ? [...fig.querySelectorAll('dd span')].find(x => /^Zuletzt/.test(x.textContent.trim())) : null;
      return sp ? sp.querySelector('b').textContent.trim() : '';
    });
    ok('Linienende = Zuletzt (' + rng + ')', end !== '' && end === last, 'Ende ' + end + ', Zuletzt ' + last);
    await L.close(ctx);
  }

  let bad = 0;
  for (const r of results) { if (!r.pass) bad++; console.log((r.pass ? 'ok    ' : 'FEHLER') + ' ' + r.name + (r.detail && !r.pass ? ' — ' + r.detail : '')); }
  console.log(results.length - bad + ' von ' + results.length + ' in Ordnung');
  process.exit(bad ? 1 : 0);
})().catch(e => { console.error(e); process.exit(1); });

// „Bewusst so lassen“ mit echten Mausklicks prüfen.
//   node labor/browser/sogelassen.js [host]
// Braucht eine Warnung, die sich so lassen lässt, und einen Port, den HostDeck
// zum Schließen vorschlägt. Im Labor etwa:
//   docker exec hostdecklab-alt docker run -d --name labor-db -p 3306:3306 \
//     public.ecr.aws/docker/library/busybox:1.37 nc -lk -p 3306 -e cat
// (die Domain leer.beispiel.test zeigt im Labor ohnehin ins Leere).
// Je Breite (1440 dunkel, 390 hell): Übersicht › „Bewusst so lassen“ → die
// Warnung verschwindet aus „Braucht dich“ und der Glocke, die Zeile
// „lässt du bewusst so“ erscheint, „Rückgängig“ holt sie zurück. Bei 1440
// zusätzlich: Überwachung › „Bewusst so gelassen“ › „Wieder warnen“,
// Sicherheit › „Bewusst offen lassen“ (Hinweis zu den Ports verschwindet,
// „Wieder vorschlagen“ hat den Fokus) und zurück, und das Protokoll nennt
// beides. Am Ende ist alles wie vorher. Ausgang 1 bei einem Fehler.
'use strict';
const L = require('./lib');

(async () => {
  const host = process.argv[2] || 'hostdeck.beispiel.test';
  let bad = 0;
  const check = (ok, what) => { console.log((ok ? 'ok     ' : 'FEHLER ') + what); if (!ok) bad++; };
  for (const w of [1440, 390]) {
    const ctx = await L.context({ width: w, height: w > 500 ? 900 : 844, dark: w > 500 });
    await L.login(ctx, host, 'leitstand-alt');
    const hints = async () => (await L.api(ctx, host, '/api/hinweise')).data;
    const before = await hints();
    const target = (before.hinweise || []).find(h => h.so_lassen);
    if (!target) { check(false, w + ': keine Warnung mit „Bewusst so lassen“ ' + JSON.stringify(before)); await L.close(ctx); continue; }
    const key = target.schluessel;
    const page = await ctx.newPage();
    const errs = [];
    page.on('pageerror', e => errs.push(String(e)));
    await L.goto(page, host, '#/lage', 5000);
    const bell = async () => page.evaluate(() => (document.getElementById('bell-btn') || {}).ariaLabel || '');
    const bell0 = await bell();
    const btn = page.locator('[data-fid="keep-' + key + '"]').first();
    check(await btn.count() === 1, w + ': Knopf „Bewusst so lassen“ an ' + key);
    await btn.scrollIntoViewIfNeeded();
    await btn.click();
    await L.sleep(1500);
    let h = await hints();
    check(!(h.hinweise || []).some(x => x.schluessel === key) && (h.so_gelassen || []).some(x => x.schluessel === key), w + ': Dienst führt es als so gelassen');
    check(await page.locator('[data-fid="keep-' + key + '"]').count() === 0, w + ': Warnung ist aus „Braucht dich“ weg');
    check(await page.locator('.ls-kept').count() > 0, w + ': Zeile „lässt du bewusst so“ steht da');
    check(await bell() !== bell0, w + ': Glocke zählt anders (' + bell0 + ' → ' + await bell() + ')');
    const undo = page.locator('[data-fid="toast-act"]').first();
    check(await undo.count() === 1 && await page.evaluate(() => document.activeElement && document.activeElement.dataset.fid === 'toast-act'), w + ': Rückgängig da und hat den Fokus');
    await undo.click();
    await L.sleep(1500);
    h = await hints();
    check((h.hinweise || []).some(x => x.schluessel === key) && !(h.so_gelassen || []).length, w + ': Rückgängig holt die Warnung zurück');
    check(await page.locator('[data-fid="keep-' + key + '"]').count() === 1, w + ': Knopf ist wieder da');

    if (w > 500) {
      await page.locator('[data-fid="keep-' + key + '"]').first().click();
      await L.sleep(1200);
      await L.goto(page, host, '#/ueberwachung', 4000);
      const un = page.locator('[data-fid="unkeep-' + key + '"]').first();
      check(await page.locator('.uw-kept').count() === 1 && await un.count() === 1, w + ': Überwachung zeigt „Bewusst so gelassen“');
      await un.click();
      await L.sleep(1500);
      h = await hints();
      check((h.hinweise || []).some(x => x.schluessel === key), w + ': „Wieder warnen“ wirkt');

      await L.goto(page, host, '#/sicherheit', 4000);
      const sec = (await L.api(ctx, host, '/api/sicherheit')).data;
      const port = sec.ports.find(p => p.vorschlag && !p.geschlossen);
      if (!port) check(false, w + ': kein Port mit Vorschlag (siehe Kopf der Datei)');
      else {
        const hadPorts = (h.hinweise || []).some(x => x.schluessel === 'ports');
        await page.locator('[data-fid="sc-keep-' + port.schluessel + '"]').click();
        await L.sleep(2000);
        const st = await page.evaluate(k => {
          const b = document.querySelector('[data-fid="sc-unkeep-' + k + '"]');
          const row = b && b.closest('tr');
          return { text: row ? row.textContent : '', fokus: document.activeElement === b, kopf: (document.querySelector('.ls-head') || {}).textContent || '' };
        }, port.schluessel);
        check(/bewusst offen gelassen/.test(st.text) && st.fokus, w + ': Port bewusst offen, „Wieder vorschlagen“ hat den Fokus');
        check(/bewusst offen/.test(st.kopf), w + ': Kopf nennt den bewusst offenen Port');
        h = await hints();
        if (hadPorts) check(!(h.hinweise || []).some(x => x.schluessel === 'ports'), w + ': Hinweis zu den Ports ist weg');
        await page.locator('[data-fid="sc-unkeep-' + port.schluessel + '"]').click();
        await L.sleep(2000);
        const back = (await L.api(ctx, host, '/api/sicherheit')).data.ports.find(p => p.schluessel === port.schluessel);
        check(back && back.vorschlag && !back.bewusst_offen, w + ': „Wieder vorschlagen“ wirkt');
      }
      const pr = (await L.api(ctx, host, '/api/protokoll?n=30')).data;
      const txt = JSON.stringify(pr);
      check(/Bleibt bewusst so/.test(txt) && /Warnt wieder/.test(txt) && (!port || /Bleibt bewusst offen/.test(txt)), w + ': Protokoll nennt es');
    }
    check(!errs.length, w + ': keine JavaScript-Fehler ' + errs.join(' | '));
    await L.close(ctx);
  }
  process.exit(bad ? 1 : 0);
})().catch(e => { console.error(e); process.exit(1); });

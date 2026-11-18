// Seitenfenster der Übersicht mit echten Mausklicks prüfen.
//   node labor/browser/seitenfenster.js [host]
// Je Breite (1440, 390): Kachel anklicken, das Fenster muss oben liegen
// (Knopf unten und Fenstermitte gehören zum Fenster, nicht zur
// Abdunklung), ein Klick hinein lässt es offen, „Neu starten“ öffnet die
// Bestätigung (die über allem liegt, Esc schließt sie), ein Klick daneben
// schließt das Fenster. Danach 35 Sekunden offen lassen: Das Auffrischen
// zeichnet es neu, ohne dass es noch einmal hereinrutscht oder den Fokus
// verliert. Ausgang 1 bei einem Fehler.
'use strict';
const L = require('./lib');

(async () => {
  const host = process.argv[2] || 'hostdeck.beispiel.test';
  let bad = 0;
  const check = (ok, what) => { console.log((ok ? 'ok     ' : 'FEHLER ') + what); if (!ok) bad++; };
  for (const w of [1440, 390]) {
    const ctx = await L.context({ width: w, height: w > 500 ? 900 : 844, dark: w > 500 });
    await L.login(ctx, host, 'leitstand-alt');
    const page = await ctx.newPage();
    const errs = [];
    page.on('pageerror', e => errs.push(String(e)));
    await L.goto(page, host, '#/lage', 5000);
    const link = page.locator('a.ov-pt-l[href^="#/lage/"]').first();
    await link.scrollIntoViewIfNeeded();
    await link.click();
    await L.sleep(800);
    const hit = await page.evaluate(() => {
      const d = document.querySelector('.ov-drw');
      if (!d) return null;
      const at = (x, y) => { const h = document.elementFromPoint(x, y); return !!(h && d.contains(h)); };
      const r = d.getBoundingClientRect();
      const b = d.querySelector('.dfoot .btn, .dfoot a') || d;
      const br = b.getBoundingClientRect();
      return { mitte: at(r.left + r.width / 2, r.top + r.height / 2), knopf: at(br.left + br.width / 2, br.top + br.height / 2) };
    });
    check(hit && hit.mitte && hit.knopf, w + ': Fenster liegt über der Abdunklung ' + JSON.stringify(hit));
    const box = await page.locator('.ov-drw').boundingBox();
    await page.mouse.click(box.x + box.width / 2, box.y + 200);
    await L.sleep(500);
    check(await page.locator('.ov-drw').count() === 1, w + ': Klick ins Fenster lässt es offen');
    const restart = page.locator('.ov-drw button', { hasText: 'Neu starten' }).first();
    if (await restart.count()) {
      await restart.click();
      await L.sleep(1500);
      const dlg = await page.evaluate(() => {
        const g = document.querySelector('.gt-dlg');
        if (!g) return null;
        const r = g.getBoundingClientRect();
        const h = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
        return !!(h && g.contains(h));
      });
      check(dlg === true, w + ': Bestätigung zu „Neu starten“ liegt oben');
      await page.keyboard.press('Escape');
      await L.sleep(500);
      check(await page.locator('.gt-dlg').count() === 0 && await page.locator('.ov-drw').count() === 1, w + ': Esc schließt nur die Bestätigung');
    }
    // Daneben klicken (auf der Abdunklung) schließt; auf dem Handy füllt das
    // Fenster die ganze Breite, dort schließt das Kreuz.
    if (box.x > 40) await page.mouse.click(box.x / 2, 300);
    else await page.locator('.ov-drw button[aria-label="Schließen"]').first().click();
    await L.sleep(500);
    check(await page.locator('.ov-drw').count() === 0, w + ': ' + (box.x > 40 ? 'Klick daneben' : 'Kreuz') + ' schließt');
    if (w > 500) {
      await link.click();
      await L.sleep(800);
      await page.evaluate(() => { document.querySelector('.ov-drw').__alt = true; window.__neu = 0; window.__anim = 0;
        new MutationObserver(() => { const x = document.querySelector('.ov-drw'); if (x && !x.__alt) { x.__alt = true; window.__neu++; window.__anim += x.getAnimations().length; } }).observe(document.getElementById('layer'), { childList: true, subtree: true }); });
      await L.sleep(35000);
      const r = await page.evaluate(() => ({ neu: window.__neu, anim: window.__anim, fokus: !!document.activeElement.closest('.ov-drw') }));
      check(r.neu > 0 && r.anim === 0 && r.fokus, w + ': Auffrischen ohne Hereinrutschen und mit Fokus ' + JSON.stringify(r));
    }
    check(!errs.length, w + ': keine JavaScript-Fehler ' + errs.join(' | '));
    await L.close(ctx);
  }
  process.exit(bad ? 1 : 0);
})().catch(e => { console.error(e); process.exit(1); });

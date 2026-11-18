// Gate 2: Bilder von Live und
// 6 Std in fünf Breiten, hell und dunkel; meldet seitliches Scrollen und
// JS-Fehler. Ablage: labor/lauf/bilder/gate2/.
//   node labor/browser/bilder-live.js [host]
'use strict';
const fs = require('fs');
const L = require('./lib');
const host = process.argv[2] || 'hostdeck.beispiel.test';
(async () => {
  const dir = L.LAUF + '/bilder/gate2';
  fs.mkdirSync(dir, { recursive: true });
  let bad = 0;
  for (const theme of ['hell', 'dunkel']) {
    for (const width of [1920, 1440, 1024, 390, 320]) {
      const ctx = await L.context({ width, height: width > 500 ? 1000 : 844, dark: theme === 'dunkel' });
      await L.login(ctx, host, 'leitstand-alt');
      const page = await ctx.newPage();
      const errs = [];
      page.on('pageerror', e => errs.push(String(e)));
      for (const [hash, name] of [['#/ueberwachung/live', 'live'], ['#/ueberwachung/6h', '6h']]) {
        await L.goto(page, host, hash, 5000);
        const over = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
        await page.screenshot({ path: dir + '/' + name + '-' + width + '-' + theme + '.png', fullPage: true });
        if (over > 1 || errs.length) bad++;
        console.log(name + ' ' + width + ' ' + theme + ': ' + (over > 1 ? 'seitlich ' + over + ' px' : 'kein seitliches Scrollen') + (errs.length ? ' · JS-Fehler ' + errs.join(' | ') : ''));
      }
      await L.close(ctx);
    }
  }
  process.exit(bad ? 1 : 0);
})().catch(e => { console.error(e); process.exit(1); });

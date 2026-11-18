// Prüfer für Zustands-Widersprüche: geht im aktuellen Zustand
// alle Seiten des Leitstands durch (dazu Glocke und Handy-Breite), sammelt
// jede Statusmarke mit Stufe und Text und prüft die Regeln:
//   I1  Was absichtlich ist (--ruhig <Muster>), ist nirgends gelb oder rot.
//   I2  „Domains antworten“ ist überall dieselbe Zahl (Domains-Seite,
//       Überwachung, API).
//   I3  Im Alltag keine Auswahl-, Backup- oder Umzugspunkte in „Braucht dich“.
//   I6  Jede gelbe oder rote Meldung in „Braucht dich“ hat einen Knopf.
//   I7  Ruhende Container heißen überall gleich: Je Grund (von dir gestoppt,
//       seit dem letzten Lauf aus, angelegt) dieselbe Zahl in der Kachel
//       „Container“ der Übersicht, in „Aus“ auf Projekte und in der API.
//   I8  „Alle N Projekte laufen“ nur, wenn N alle Projekte sind.
//   I10 Projektwort: data-zustand je Projekt gleich in Lageplan, Projekte
//       nach Last, Überwachung › Projekte, Seite Projekte und API (gruppen).
//   I11 Containerzahlen: Kachel „Container“ und Projekte-Kacheln gegen
//       die Summe des Dienstes; ein Job zählt nirgends als „läuft nicht“.
//   I12 Domainmenge: Lageplan = Seiten = Domains-Seite = API, keine doppelt,
//       Zähler „N Domains“ = Zeilen, Kachel „Domains“ = API; eine laufende Warnung zu einer Domain
//       nur, wenn die Domain in der API steht.
//   I13 Domainzustand: Wort und Farbe je Domain überall gleich und = API.
//   I14 Kennzahlen: Kachel-Spitze = API (24 Std); Spitzen über die
//       Zeiträume monoton; das nach Zeit gewichtete Mittel der Punkte (wie
//       im Zoom) = Mittel des Dienstes.
//   I15 Farben folgen den Grenzen des Dienstes: Prozessor und Arbeitsspeicher
//       in Kachel, Leistung und Kopf der Überwachung (15 Min) passen zu Wert
//       und Grenze, in der Übersicht erst nach 60 s Wartezeit der Regel; mit --grenzen verschiebt der Prüfer die Prozessor-Grenze
//       (50 oder 100 %), prüft noch einmal und setzt sie zurück.
//   I16 Absichtliches (--ruhig) auch rückblickend nie rot oder gelb:
//       Tagesbalken, Verfügbarkeit, Verlauf der Warnungen.
//   I17 Gesamtstatus: Kopf grün nur ohne gelbe oder rote Marke (unter
//       Überwachung nur ihr eigenes Abzeichen); Leertext
//       „Alles läuft“ nur, wenn nichts aus ist.
//   I18 Zeiten: jede sichtbare Uhrzeit, die nicht von heute ist, hat ein
//       Datum oder „gestern“; Texte des Dienstes enthalten keine Uhrzeit.
//   I19 Teile ≤ Ganzes: Prozessor der Container zusammen ≤ Server (plus
//       Toleranz), Verfügbarkeit ≤ 100 %, Ausfall ≤ Messzeit.
//   I20 Projektzahl (Datenprüfung H1): Übersicht (Kachel Projekte und
//       Zustand), Seite Projekte (Kopf, Filter „Alle“) und Seitenleiste
//       zählen dieselben Einträge, auch jeden Server, den ein Panel startet.
//   I22 Ohne Prozessor je Container (cgroup v1) zeigt „Wer braucht gerade
//       was“ Zahlen von Docker und den Hinweis dazu.
//   I21 Bewusst so gelassen (Datenprüfung M8): Eine so gelassene Domain
//       zählt in keinem Abzeichen, keiner Kachel und keinem Zustand; ihre
//       Zeile steht grau mit „bewusst so gelassen“.
//
//   Zeitzone des Browsers: LAB_TZ (etwa Europe/Berlin), der Server läuft
//   im Labor auf UTC.
//   node labor/browser/widerspruch.js --zustand blog-gestoppt --ruhig 'blog' [--host hostdeck.beispiel.test]
// Ausgabe: labor/lauf/pruefer/<zustand>.json und .md
'use strict';
const fs = require('fs');
const path = require('path');
const L = require('./lib');

const args = process.argv.slice(2);
const opt = (k, d) => { const i = args.indexOf('--' + k); return i >= 0 ? args[i + 1] : d; };
const ZUSTAND = opt('zustand', 'stand');
const HOST = opt('host', 'hostdeck.beispiel.test');
const PW = opt('passwort', HOST.startsWith('hostdeck.') ? 'leitstand-alt' : 'leitstand-neu');
const RUHIG = opt('ruhig', '') ? new RegExp(opt('ruhig'), 'i') : null;
const GRENZEN = args.includes('--grenzen');
const PAGES = (opt('seiten', '') || '#/lage,#/projekte,#/domains,#/ueberwachung,#/backup,#/umzug,#/auswahl,#/auswahl/bereich/caddy,#/protokoll').split(',');

// I15: Farben folgen den Grenzen. Liest Kachel (Übersicht, Leistung) und
// Kopf der Überwachung (15 Min) samt Wert; mit --grenzen wird die Grenze
// für den Prozessor so verschoben, dass die Farbe wechseln muss (50 % oder
// 100 %), geprüft und wieder zurückgesetzt.
async function colorsAt(ctx, page) {
  const g = ((await L.api(ctx, HOST, '/api/ueberwachung')).data || {}).warnungen;
  const res = { grenzen: g ? g.grenzen : null, seiten: {} };
  for (const p of ['#/lage', '#/leistung', '#/ueberwachung/15m']) {
    // Neu laden, nicht nur die Adresse wechseln: Der Leitstand hält den
    // Stand der Überwachung 30 Sekunden; nach einer Änderung über die API
    // (nicht über das Formular) wäre er sonst noch der alte.
    await page.goto('about:blank');
    // Wartezeiten der Regeln kurz vor dem Laden: Die Übersicht färbt
    // Prozessor und Arbeitsspeicher erst, wenn eine seit 60 s läuft.
    if (p === '#/lage') {
      const w = ((await L.api(ctx, HOST, '/api/ueberwachung')).data || {}).warnungen || {};
      const d = key => {
        if ((w.aktiv || []).some(a => a.schluessel === key && !a.gut_seit)) return { aktiv: true };
        const x = (w.wartet || []).find(y => y.schluessel === key);
        return x ? { sek: (Date.now() - Date.parse(x.seit)) / 1000 } : null;
      };
      res.dauer = { prozessor: d('prozessor'), speicher: d('speicher') };
    }
    await L.goto(page, HOST, p, 4000);
    res.seiten[p] = await page.evaluate(() => {
      const k = id => { const e = document.querySelector('.ls-kpi[data-kpi="' + id + '"]'); return e ? { stufe: ['err', 'warn'].find(c => e.classList.contains(c)) || '', wert: e.dataset.wert != null ? Number(e.dataset.wert) : null } : null; };
      const ems = [...document.querySelectorAll('.ls-head .ls-status em')].map(e => ({ text: (e.textContent || '').trim(), stufe: ['err', 'warn', 'ok'].find(c => e.classList.contains(c)) || '' }));
      return { cpu: k('cpu'), ram: k('ram'), platte: k('platte'), kopf: ems };
    });
  }
  const h = (await L.api(ctx, HOST, '/api/ueberwachung/verlauf?zeitraum=15m&punkte=360&spitzen=1&serien=s/')).data || {};
  res.mittel15 = ((h.kennzahlen || {})['s/cpu'] || {}).mittel;
  return res;
}
async function limitsCheck(ctx) {
  const page = await ctx.newPage();
  const out = {};
  try {
    out.vorher = await colorsAt(ctx, page);
    const g = out.vorher.grenzen;
    if (GRENZEN && g) {
      const cpu = (out.vorher.seiten['#/lage'].cpu || {}).wert;
      const lim = cpu != null && cpu > 50 && g.prozessor_gelb <= 50 ? 100 : 50;
      const set = await L.api(ctx, HOST, '/api/ueberwachung/grenzen', { grenzen: Object.assign({}, g, { prozessor_gelb: lim }) });
      if (set.status !== 200) out.fehler = 'Setzen ging nicht: ' + set.status + ' ' + JSON.stringify(set.data).slice(0, 200);
      else {
        // Die Übersicht färbt erst nach einer Minute Wartezeit.
        if (cpu != null && cpu > lim) await new Promise(r => setTimeout(r, 75000));
        try { out.nachher = await colorsAt(ctx, page); } finally { await L.api(ctx, HOST, '/api/ueberwachung/grenzen', { grenzen: g }); }
      }
    }
  } finally {
    await page.close();
  }
  return out;
}

(async () => {
  const out = { zustand: ZUSTAND, zeit: new Date().toISOString(), seiten: {}, api: {}, verstoesse: [] };
  const peak1d = async ctx => (((await L.api(ctx, HOST, '/api/ueberwachung/verlauf?zeitraum=1d&punkte=144&spitzen=1&serien=s/cpu')).data || {}).kennzahlen || {})['s/cpu'];
  for (const [w, dark] of [[1440, true], [390, false]]) {
    const ctx = await L.context({ width: w, height: w > 500 ? 900 : 844, dark });
    await L.login(ctx, HOST, PW);
    // Spitze 24 Std vor den Seiten: Bei einem jungen Verlauf wächst sie,
    // bis der Prüfer die API am Ende liest (I14 Kachel).
    if (w === 1440) out.api['spitze 1d vorher'] = await peak1d(ctx);
    const page = await ctx.newPage();
    const errs = [];
    page.on('pageerror', e => errs.push(String(e)));
    page.on('console', m => { if (m.type() === 'error') errs.push(m.text()); });
    for (const p of PAGES) {
      await L.goto(page, HOST, p, 3500);
      const mk = await L.marks(page);
      // Glocke öffnen (nur breit, da steht sie oben).
      if (w > 500 && p === '#/lage') {
        const bell = await page.$('#bell-btn');
        if (bell) {
          await bell.click();
          await page.waitForTimeout(300);
          mk.glocke = await page.$$eval('#bell-menu .ls-bellm-i', es => es.map(e => ({ stufe: e.className, text: (e.innerText || '').replace(/\s+/g, ' ').trim() })));
          await page.keyboard.press('Escape');
        }
      }
      const scroll = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1);
      // Für I20: Zählungen der Projekte an jeder Stelle.
      const zahlen = await page.evaluate(() => {
        const t = e => (e ? e.innerText || e.textContent || '' : '').replace(/\s+/g, ' ').trim();
        return {
          kachel: t(document.querySelector('#ov-pr .ov-count')),
          zustand: [...document.querySelectorAll('.ov-kl li')].map(t),
          filter: [...document.querySelectorAll('.ls-chip[data-pf]')].map(e => [e.dataset.pf, t(e)]),
          leiste: [...document.querySelectorAll('nav a[href^="#/projekte/gruppe:"]')].map(t),
          wer: t(document.querySelector('#uw-wer')),
        };
      });
      out.seiten[w + ' ' + p] = Object.assign(mk, { waagerecht: scroll, zahlen });
    }
    out.seiten[w + ' js-fehler'] = errs;
    if (w === 1440) {
      for (const a of ['/api/hinweise', '/api/domains/zustand', '/api/ueberwachung', '/api/lauf', '/api/container']) out.api[a] = (await L.api(ctx, HOST, a)).data;
      for (const r of ['15m', '1h', '6h', '1d', '7d', '30d']) out.api['verlauf ' + r] = (await L.api(ctx, HOST, '/api/ueberwachung/verlauf?zeitraum=' + r + '&punkte=' + (r === '1d' ? 144 : 360) + '&spitzen=1&serien=s/')).data;
      out.grenzen = await limitsCheck(ctx);
    }
    await L.close(ctx);
  }

  const v = (regel, wo, text) => out.verstoesse.push({ regel, wo, text });
  for (const [wo, s] of Object.entries(out.seiten)) {
    if (Array.isArray(s)) { if (s.length) v('JS', wo, s.join(' | ').slice(0, 400)); continue; }
    if (s.waagerecht) v('SK7', wo, 'waagerechte Seitenrolle');
    for (const m of s.marken.concat((s.glocke || []).map(g => ({ art: 'glocke-eintrag', stufe: /err/.test(g.stufe) ? 'err' : /warn/.test(g.stufe) ? 'warn' : '', text: g.text })))) {
      const hot = m.stufe === 'err' || m.stufe === 'warn';
      if (hot && RUHIG && (RUHIG.test(m.text) || RUHIG.test(m.umfeld || '') || RUHIG.test(m.titel || ''))) v('I1', wo, m.art + ' ' + m.stufe + ': ' + (m.text + ' · ' + (m.umfeld || '')).slice(0, 240));
      if (m.art === 'braucht-dich' && hot && !/Ansehen|Zum|[Ll]esen|Domains|Logs|Neu laden|Bestätigen|Entscheiden|Prüfen|Starten|Auswahl|Backup|Umzug|Öffnen|Weiter|Aufräumen/i.test(m.text)) v('I6', wo, 'ohne Knopf: ' + m.text.slice(0, 200));
    }
  }
  // I2: gleiche Zahl. Domains-Seite (Kopf) gegen API.
  const z = (out.api['/api/domains/zustand'] || {}).zustand || [];
  const zOk = z.filter(d => d.zustand === 'antwortet').length;
  // Der Satz unter der Überschrift (bis 14.11. las der Prüfer hier nur die
  // Überschrift „Domains“, die Regel griff nie).
  const head = (out.seiten['1440 #/domains'] || {}).status || '';
  const m = /^(\d+) von|^Alle (\d+)/.exec(head);
  if (m && Number(m[1] || m[2]) !== zOk) v('I2', 'Domains-Kopf', head + ' gegen API ' + zOk + ' antworten');
  const warn = (out.api['/api/ueberwachung'] || {}).warnungen || {};
  const uw = warn.seiten || [];
  // I1 auch in der API: keine laufende Warnung zu etwas Absichtlichem.
  if (RUHIG) for (const a of warn.aktiv || []) if (!a.gut_seit && (RUHIG.test(a.titel || '') || RUHIG.test(a.schluessel || ''))) v('I1', 'Warnung ' + a.schluessel, a.stufe + ': ' + a.titel);
  for (const sc of uw) {
    const d = z.find(x => x.domain === sc.domain);
    if (!d) continue;
    if (!!sc.pausiert !== (d.zustand === 'pausiert')) v('I2', 'Überwachung ' + sc.domain, 'pausiert ' + !!sc.pausiert + ' gegen Zustand ' + d.zustand);
  }
  // I7: gleiche Wörter und Zahlen je Grund. Gezählt wird wie im Leitstand:
  // eigene Container ohne Jobs, die nicht laufen.
  const GR = ['von dir gestoppt', 'seit dem letzten Lauf aus', 'angelegt'];
  const cont = ((out.api['/api/container'] || {}).container || []).filter(c => !c.verwaltet_von && !c.job);
  const fromApi = {};
  for (const c of cont) {
    if (c.laeuft || c.zustand === 'restarting') continue;
    // Klasse vom Dienst (eine Quelle): von dir vor angelegt.
    const w = c.klasse === 'von-dir' ? GR[0] : c.klasse === 'letzter-lauf' ? GR[1] : c.klasse === 'angelegt' ? GR[2] : '';
    if (w) fromApi[w] = (fromApi[w] || 0) + 1;
  }
  const reasons = t => { const r = {}; for (const w of GR) { const m = new RegExp('(\\d+) ' + w + '(?! seit)').exec(t || ''); if (m) r[w] = Number(m[1]); } return r; };
  const tile = (page, label) => ((out.seiten['1440 ' + page] || {}).kacheln || []).find(t => t.startsWith(label + ' '));
  const norm = r => JSON.stringify(GR.filter(w => r[w]).map(w => [w, r[w]]));
  const want = norm(fromApi);
  for (const [page, label] of [['#/lage', 'Container'], ['#/projekte', 'Aus']]) {
    const t = tile(page, label);
    if (t === undefined) continue;
    // Bei Fehlern (rot) steht dort, was kaputt ist, nicht die Gründe.
    if (/unerwartet|Neustart-Schleife/.test(t)) continue;
    const got = norm(reasons(t));
    if (got !== want) v('I7', page + ' Kachel ' + label, t.slice(0, 160) + ' gegen API ' + want);
  }
  // I8: „Alle N Projekte laufen“ zählt alle Projekte, sonst steht „Dazu …“.
  const projs = new Set(((out.api['/api/container'] || {}).container || []).filter(c => !c.verwaltet_von && c.projekt).map(c => c.projekt));
  const kopf = (out.seiten['1440 #/projekte'] || {}).status || '';
  const mk8 = /Alle (\d+) Projekte? laufen\.(.*)/.exec(kopf);
  if (mk8 && !/Dazu /.test(mk8[2]) && Number(mk8[1]) !== projs.size) v('I8', 'Projekte-Kopf', kopf + ' gegen ' + projs.size + ' Projekte');
  // I3: Alltag.
  const lauf = out.api['/api/lauf'] || {};
  if (lauf.alltag) {
    for (const h of ((out.api['/api/hinweise'] || {}).hinweise || [])) if (/Auswahl|Backup|Umzug/.test(h.titel) && !/Umzug abschließen/.test(h.titel)) v('I3', 'Hinweis', h.titel);
  }
  rules10to19(out, v);
  rules20to21(out, v);
  const dir = path.join(L.LAUF, 'pruefer');
  fs.mkdirSync(dir, { recursive: true });
  fs.writeFileSync(path.join(dir, ZUSTAND + '.json'), JSON.stringify(out, null, 1));
  const md = ['# Prüfer: ' + ZUSTAND, '', 'Zeit: ' + out.zeit, '', out.verstoesse.length ? '| Regel | Wo | Was |\n|---|---|---|\n' + out.verstoesse.map(x => '| ' + x.regel + ' | ' + x.wo + ' | ' + x.text.replace(/\|/g, '/') + ' |').join('\n') : 'Keine Verstöße.', ''];
  const hotList = [];
  for (const [wo, s] of Object.entries(out.seiten)) if (!Array.isArray(s)) for (const mk of s.marken) if (mk.stufe === 'err' || mk.stufe === 'warn') hotList.push('- ' + wo + ' · ' + mk.art + ' ' + mk.stufe + ': ' + mk.text.slice(0, 120));
  md.push('## Gelbe und rote Marken', '', hotList.length ? hotList.join('\n') : 'keine', '');
  fs.writeFileSync(path.join(dir, ZUSTAND + '.md'), md.join('\n'));
  console.log(md.slice(0, 5).join('\n'));
  process.exit(0);
})().catch(e => { console.error(e); process.exit(1); });

// Regeln I10 bis I19.
function rules10to19(out, v) {
  const S = out.seiten;
  const pagesAt = w => Object.entries(S).filter(([k, s]) => k.startsWith(w + ' ') && !Array.isArray(s));
  const api = out.api;
  const rep = api['/api/container'] || {};
  const groups = new Map((rep.gruppen || []).map(g => [g.schluessel, g]));
  // I10: Projektwort überall gleich und gleich der Gruppe des Dienstes.
  const words = new Map();
  for (const [wo, s] of pagesAt(1440)) for (const p of s.projekte || []) {
    if (p.zustand == null) continue;
    const k = p.projekt;
    if (!words.has(k)) words.set(k, []);
    words.get(k).push([wo.split(' ')[1] + ' ' + p.wo, p.zustand]);
  }
  for (const [k, list] of words) {
    const g = groups.get('p:' + k);
    const want = g ? g.wort : list[0][1];
    for (const [wo, z] of list) if (z !== want) v('I10', wo + ' ' + k, '„' + z + '“ gegen „' + want + '“' + (g ? ' (API)' : ''));
  }
  // I11: Zahlen der Container gegen die Summe des Dienstes.
  const sm = rep.summe;
  if (sm) {
    const own = sm.gesamt - sm.jobs, ok = sm.laufen - sm.unhealthy;
    const k = ((S['1440 #/lage'] || {}).kpis || []).find(x => x.id === 'container');
    if (k) {
      const m = /(\d+)\s*von (\d+) gesund/.exec(k.text);
      if (!m || Number(m[1]) !== ok || Number(m[2]) !== own) v('I11', 'Übersicht Kachel Container', k.text + ' gegen API ' + ok + ' von ' + own);
    }
    const t = ((S['1440 #/projekte'] || {}).kacheln || []);
    const num = label => { const x = t.find(y => y.startsWith(label + ' ')); const m = x && /^\D*?(\d+)/.exec(x.slice(label.length)); return m ? Number(m[1]) : null; };
    const c1 = num('Container'), c2 = num('Container laufen');
    if (c1 != null && c1 !== own) v('I11', 'Projekte Kachel Container', c1 + ' gegen API ' + own);
    if (c2 != null && c2 !== ok) v('I11', 'Projekte Kachel Container laufen', c2 + ' gegen API ' + ok);
    // Summe der Gruppen = Summe (ohne verwaltete).
    const sumG = (rep.gruppen || []).filter(g => !g.verwaltet).reduce((a, g) => a + g.gesamt, 0);
    if (sumG !== sm.gesamt) v('I11', 'API gruppen', 'Summe der Gruppen ' + sumG + ' gegen summe ' + sm.gesamt);
    // Ein erledigter Job ist nie „läuft nicht“: Gruppen nur aus Jobs sind neutral.
    for (const g of rep.gruppen || []) if (g.jobs && g.gesamt === g.jobs && g.stufe !== 'idle') v('I11', 'Gruppe ' + g.schluessel, 'nur Jobs, aber Stufe ' + g.stufe);
  }
  // I12, I13: Domains.
  const z = (api['/api/domains/zustand'] || {}).zustand || [];
  const want = new Map(z.map(d => [d.domain, d]));
  const warn = (api['/api/ueberwachung'] || {}).warnungen || {};
  if (api['/api/domains/zustand']) for (const a of warn.aktiv || []) {
    const h = /^seite:(.+)$/.exec(a.schluessel || '');
    if (h && !want.has(h[1])) v('I12', 'Warnung ' + a.schluessel, 'läuft, aber die Domain steht nicht mehr in der API (' + a.titel + ')');
  }
  // Kachel „Domains“ der Übersicht: „N von M antworten“ = API.
  const dk = ((S['1440 #/lage'] || {}).kpis || []).find(x => x.id === 'domains');
  const kept = keptDomains(api);
  if (dk && z.length) {
    const live = z.filter(d => d.zustand !== 'aus' && !kept.has(d.domain));
    const ok = live.filter(d => d.zustand === 'antwortet').length;
    const total = live.filter(d => !['pausiert', 'startet', 'wird-geprueft'].includes(d.zustand)).length;
    const m = /(\d+)\s*von (\d+) antwor/.exec(dk.text);
    if (total && (!m || Number(m[1]) !== ok || Number(m[2]) !== total)) v('I12', 'Kachel Domains', '„' + dk.text.slice(0, 60) + '“ gegen API ' + ok + ' von ' + total);
    const bad = live.some(d => d.stufe === 'err') ? 'err' : live.some(d => d.stufe === 'warn') ? 'warn' : '';
    if (dk.stufe !== bad) v('I12', 'Kachel Domains', 'Stufe „' + dk.stufe + '“, API „' + bad + '“');
  }
  const places = { lageplan: ['#/ueberwachung'], seiten: ['#/ueberwachung'], domains: ['#/domains'] };
  for (const [wo, [pg]] of Object.entries(places)) {
    const s = S['1440 ' + pg];
    if (!s) continue;
    const list = (s.domains || []).filter(d => d.wo === wo);
    if (!list.length && !z.length) continue;
    const seen = new Map();
    for (const d of list) seen.set(d.domain, (seen.get(d.domain) || 0) + 1);
    for (const [d, n] of seen) if (n > 1) v('I12', wo, d + ' steht ' + n + '-mal da');
    for (const d of want.keys()) if (!seen.has(d)) v('I12', wo, d + ' fehlt (steht in der API)');
    for (const d of seen.keys()) if (!want.has(d)) v('I12', wo, d + ' steht da, aber nicht in der API');
    if (wo === 'seiten' && s.seitenSub) {
      const m = /^(\d+) Domains?/.exec(s.seitenSub);
      if (m && Number(m[1]) !== list.length) v('I12', 'Seiten-Zähler', s.seitenSub + ' gegen ' + list.length + ' Zeilen');
    }
    for (const d of list) {
      const a = want.get(d.domain);
      if (!a) continue;
      if (d.zustand != null && d.zustand !== a.zustand) v('I13', wo + ' ' + d.domain, 'Zustand ' + d.zustand + ' gegen API ' + a.zustand);
      // So gelassen: grau statt der Farbe der API.
      const wantSt = kept.has(d.domain) && (a.stufe === 'err' || a.stufe === 'warn') ? 'idle' : a.stufe;
      if (d.stufe && d.stufe !== wantSt) v('I13', wo + ' ' + d.domain, 'Farbe ' + d.stufe + ' gegen ' + wantSt + (wantSt !== a.stufe ? ' (so gelassen)' : ' (API)'));
    }
  }
  // I14: Kennzahlen.
  const ks = r => ((api['verlauf ' + r] || {}).kennzahlen || {})['s/cpu'];
  const order = ['15m', '1h', '6h', '1d', '7d', '30d'];
  for (let i = 1; i < order.length; i++) {
    const a = ks(order[i - 1]), b = ks(order[i]);
    if (a && b && a.spitze > b.spitze + 1e-6) v('I14', 'Spitze ' + order[i - 1] + ' > ' + order[i], a.spitze + ' > ' + b.spitze);
  }
  const k1d = ks('1d');
  const cpuK = ((S['1440 #/lage'] || {}).kpis || []).find(x => x.id === 'cpu');
  if (k1d && cpuK) {
    const m = /Spitze 24 Std ([\d,]+)\s%/.exec(cpuK.text);
    const shown = m ? Number(m[1].replace(',', '.')) : null;
    const round = x => x < 10 ? Math.round(x * 10) / 10 : Math.round(x);
    // Die Kachel zeigt die Spitze beim Laden der Seite: zwischen der vor den
    // Seiten und der am Ende.
    const lo = round(((api['spitze 1d vorher'] || {}).spitze) ?? k1d.spitze), hi = round(k1d.spitze);
    if (shown == null || shown < Math.min(lo, hi) - 0.051 || shown > Math.max(lo, hi) + 0.051) v('I14', 'Kachel Prozessor', (m ? m[0] : cpuK.text.slice(0, 80)) + ' gegen API ' + (lo !== hi ? lo + ' bis ' : '') + k1d.spitze.toFixed(2));
  }
  for (const r of order) {
    const h = api['verlauf ' + r];
    const k = ks(r);
    if (!h || !k || !h.serien || !h.serien['s/cpu']) continue;
    // Wie uwStatsP im Leitstand: jeder Punkt so lange, wie er steht, der
    // letzte bis zur jüngsten Messung, der erste ab Beginn der Reihe.
    const t = h.zeiten, vs = h.serien['s/cpu'], cTo = h.grob_bis || 0, cStep = h.grob_schritt || h.schritt;
    let sum = 0, n = 0;
    vs.forEach((x, i) => {
      if (x == null) return;
      const cap = t[i] <= cTo ? cStep : h.schritt;
      let wt = Math.min(cap, i + 1 < t.length ? t[i + 1] - t[i] : h.bis ? h.bis - t[i] : cap);
      const b0 = k.seit || h.beginn || 0;
      if (t[i] < b0) wt -= b0 - t[i];
      wt = Math.max(1, wt);
      sum += x * wt; n += wt;
    });
    if (n && Math.abs(sum / n - k.mittel) > Math.max(0.3, k.mittel * 0.1)) v('I14', 'Mittel ' + r, 'Punkte ' + (sum / n).toFixed(2) + ' gegen Dienst ' + k.mittel.toFixed(2));
  }
  // I15: Farben folgen den Grenzen, vor und nach dem Verschieben.
  for (const [when, c] of Object.entries(out.grenzen || {})) {
    if (when === 'fehler') { v('I15', 'Grenzen', c); continue; }
    const g = c.grenzen;
    if (!g) continue;
    // Übersicht: gelb erst, wenn die Regel warnt oder ihre Wartezeit
    // seit 60 s läuft; knapp darunter (52 bis 60 s vor dem Laden) gilt
    // beides.
    const lasts = d => !d ? 'nein' : d.aktiv || d.sek >= 60 ? 'ja' : d.sek >= 52 ? 'offen' : 'nein';
    for (const [pg, x] of Object.entries(c.seiten)) {
      const dauer = pg === '#/lage' ? c.dauer || {} : null;
      if (x.cpu && x.cpu.wert != null) {
        const l = dauer ? lasts(dauer.prozessor) : 'ja';
        const want = x.cpu.wert > g.prozessor_gelb && l !== 'nein' ? 'warn' : '';
        if (x.cpu.stufe !== want && !(l === 'offen' && x.cpu.stufe === '')) v('I15', when + ' ' + pg + ' Prozessor', x.cpu.wert.toFixed(1) + ' % bei Grenze ' + g.prozessor_gelb + ' %: Stufe „' + x.cpu.stufe + '“, erwartet „' + want + '“');
      }
      if (x.ram && x.ram.wert != null && pg === '#/lage') {
        const free = 1 - x.ram.wert / 100;
        const l = lasts(dauer.speicher);
        const want = l === 'nein' ? '' : free < g.speicher_rot ? 'err' : free < g.speicher_gelb ? 'warn' : '';
        if (x.ram.stufe !== want && !(l === 'offen' && x.ram.stufe === '')) v('I15', when + ' ' + pg + ' Arbeitsspeicher', x.ram.wert.toFixed(1) + ' % belegt: Stufe „' + x.ram.stufe + '“, erwartet „' + want + '“');
      }
      if (pg === '#/ueberwachung/15m' && c.mittel15 != null) {
        const em = x.kopf.find(e => /\s%$/.test(e.text) && !/belegt/.test(e.text));
        const want = c.mittel15 > g.prozessor_gelb ? 'warn' : 'ok';
        if (em && em.stufe !== want) v('I15', when + ' Kopf Überwachung', 'Mittel ' + c.mittel15.toFixed(1) + ' % bei Grenze ' + g.prozessor_gelb + ' %: „' + em.stufe + '“, erwartet „' + want + '“');
      }
    }
  }
  if (GRENZEN && out.grenzen && !out.grenzen.nachher && !out.grenzen.fehler) v('I15', 'Grenzen', 'nicht verschoben');
  // I16: Absichtliches rückblickend.
  if (RUHIG) {
    for (const [wo, s] of pagesAt(1440)) {
      for (const d of s.domains || []) if (RUHIG.test(d.domain) && d.wo === 'seiten') {
        const bad = (d.balken || []).filter(c => c === 'err' || c === 'warn').length;
        if (bad) v('I16', wo + ' ' + d.domain, bad + ' Tagesbalken gelb oder rot');
        if (d.verfuegbarkeit != null && Number(d.verfuegbarkeit) < 100 - 1e-6) v('I16', wo + ' ' + d.domain, 'Verfügbarkeit ' + d.verfuegbarkeit + ' %');
      }
      for (const e of s.ereignisse || []) if (RUHIG.test(e.text) && (e.stufe === 'err' || e.stufe === 'warn') && !e.pause) v('I16', wo + ' Verlauf', e.stufe + ': ' + e.text.slice(0, 160));
    }
    const sites = ((api['/api/ueberwachung'] || {}).warnungen || {}).seiten || [];
    for (const sc of sites) if (RUHIG.test(sc.domain) && sc.verfuegbarkeit != null && sc.verfuegbarkeit < 100 - 1e-6) v('I16', 'API ' + sc.domain, 'Verfügbarkeit ' + sc.verfuegbarkeit);
  }
  // I17: Gesamtstatus.
  for (const [wo, s] of Object.entries(S)) {
    if (Array.isArray(s) || !/#\/lage$|#\/ueberwachung$/.test(wo)) continue;
    // Der Kopf der Überwachung spricht nur für die Überwachung: Abzeichen
    // anderer Seiten (etwa „Backup 1“) zählen dort nicht, auf der Übersicht
    // (Gesamtstatus) schon.
    const own = m => m.art !== 'abzeichen' || /#\/lage$/.test(wo) || /^Überwachung\b/.test(m.umfeld || '');
    const hot = s.marken.filter(m => (m.stufe === 'err' || m.stufe === 'warn') && m.art !== 'glocke' && own(m));
    if (s.kopf === 'ok' && hot.length) v('I17', wo, 'Kopf grün, aber ' + hot.length + ' gelbe/rote Marke(n): ' + hot.slice(0, 3).map(m => m.art + ' ' + m.text.slice(0, 60)).join(' | '));
    if (sm && (s.leer || []).some(t => /Alles läuft/.test(t)) && (sm.von_dir || sm.letzter_lauf || sm.angelegt || sm.unerwartet)) v('I17', wo, 'Leertext „Alles läuft“, obwohl Container aus sind');
  }
  // I18: Zeiten.
  const today = new Date().toLocaleDateString('de-DE', { timeZone: process.env.LAB_TZ || undefined });
  for (const [wo, s] of Object.entries(S)) {
    if (Array.isArray(s)) continue;
    for (const t of s.zeiten || []) {
      const d = new Date(t.datetime);
      if (isNaN(d)) continue;
      const day = d.toLocaleDateString('de-DE', { timeZone: process.env.LAB_TZ || undefined });
      if (day !== today && !/\d{1,2}\.\d{1,2}\.|gestern|morgen/i.test(t.text)) v('I18', wo, '„' + t.text + '“ ist vom ' + day + ', ohne Datum');
      // Die gezeigte Uhrzeit passt zur Browser-Zeit.
      // Eine Spanne („09:59–10:03“, „zwischen 10:50 und 11:00“) nennt den
      // Zeitpunkt als eine ihrer Grenzen.
      const hms = t.text.match(/\d{2}:\d{2}/g) || [];
      const want = d.toLocaleTimeString('de-DE', { hour: '2-digit', minute: '2-digit', timeZone: process.env.LAB_TZ || undefined });
      if (hms.length && !hms.includes(want)) v('I18', wo, '„' + t.text + '“ gegen ' + want + ' in Browser-Zeit');
    }
  }
  const texts = [];
  for (const h of ((api['/api/hinweise'] || {}).hinweise || [])) texts.push(['Hinweis', h.titel + ' ' + (h.text || '')]);
  const wv = (api['/api/ueberwachung'] || {}).warnungen || {};
  for (const a of wv.aktiv || []) texts.push(['Warnung', a.titel + ' ' + (a.text || '')]);
  // Vom Verlauf zeigt der Leitstand nur den Titel; Texte älterer Versionen
  // (Altbestand) dürfen noch Uhrzeiten tragen.
  for (const e of (wv.verlauf || []).slice(0, 30)) texts.push(['Ereignis', e.titel]);
  for (const [wo, t] of texts) if (/\b\d{1,2}:\d{2}\b/.test(t)) v('I18', wo, 'Uhrzeit im Text des Dienstes: ' + t.slice(0, 160));
  // I19: Teile ≤ Ganzes.
  const mv = (api['/api/ueberwachung'] || {}).werte || {};
  const cpuSum = Object.entries(mv).filter(([k]) => /^c\/.*\/cpu$/.test(k)).reduce((a, [, x]) => a + x, 0);
  if (mv['s/cpu'] != null && cpuSum > mv['s/cpu'] * 1.15 + 3) v('I19', 'Prozessor', 'Container zusammen ' + cpuSum.toFixed(1) + ' % gegen Server ' + mv['s/cpu'].toFixed(1) + ' %');
  for (const sc of wv.seiten || []) {
    if (sc.verfuegbarkeit != null && (sc.verfuegbarkeit > 100 + 1e-9 || sc.verfuegbarkeit < 0)) v('I19', 'Verfügbarkeit ' + sc.domain, String(sc.verfuegbarkeit));
    if (sc.ausfall_s > sc.messzeit_s) v('I19', 'Ausfall ' + sc.domain, sc.ausfall_s + ' s > Messzeit ' + sc.messzeit_s + ' s');
  }
}

// Domains, die du bewusst so gelassen hast (Warnung seite:<domain>).
function keptDomains(api) {
  const k = new Set();
  for (const h of ((api['/api/hinweise'] || {}).so_gelassen || [])) { const m = /^seite:(.+)$/.exec(h.schluessel || ''); if (m) k.add(m[1]); }
  for (const a of (((api['/api/ueberwachung'] || {}).warnungen || {}).aktiv || [])) { const m = /^seite:(.+)$/.exec(a.schluessel || ''); if (m && a.so_gelassen) k.add(m[1]); }
  return k;
}

// Regeln I20 und I21 (Datenprüfung 18.11.2026).
function rules20to21(out, v) {
  const S = out.seiten, api = out.api;
  // I20: Projektzahl. Die Kachel der Übersicht nennt „N laufen · M aus · …“;
  // die Summe ist die Zahl der Einträge, die der Filter „Alle“ zeigt.
  const num = (t, re) => { const m = re.exec(t || ''); return m ? Number(m[1]) : 0; };
  for (const w of [1440, 390]) {
    const ov = (S[w + ' #/lage'] || {}).zahlen, pr = (S[w + ' #/projekte'] || {}).zahlen;
    if (!ov || !pr || !ov.kachel) continue;
    const run = num(ov.kachel, /^(\d+) (?:läuft|laufen)/);
    const sum = run + num(ov.kachel, /(\d+) aus\b/) + num(ov.kachel, /(\d+) Jobs? erledigt/) + num(ov.kachel, /(\d+) angelegt/);
    const all = (pr.filter.find(([id]) => id === 'alle') || [])[1];
    const nAll = all ? num(all, /(\d+)\s*$/) : null;
    if (nAll != null && nAll !== sum) v('I20', w + ' Projekte Filter Alle', all + ' gegen Übersicht „' + ov.kachel + '“ (' + sum + ')');
    const head = (S[w + ' #/projekte'] || {}).status || '';
    const hm = /^(?:Alle )?(\d+) Projekte? (?:läuft|laufen)/.exec(head);
    if (hm && Number(hm[1]) !== run) v('I20', w + ' Projekte-Kopf', head.slice(0, 120) + ' gegen Übersicht ' + run + ' laufen');
    const zl = (ov.zustand || []).find(t => /^Projekte laufen/.test(t));
    const zm = zl && /(\d+)\s*\/\s*(\d+)/.exec(zl);
    if (zm && Number(zm[1]) !== run) v('I20', w + ' Zustand', zl + ' gegen Übersicht ' + run + ' laufen');
    if (pr.leiste && pr.leiste.length) {
      const ls = pr.leiste.reduce((a, t) => a + num(t, /(\d+)\s*$/), 0);
      if (nAll != null && ls !== nAll) v('I20', w + ' Seitenleiste', pr.leiste.join(', ') + ' gegen Alle ' + nAll);
    }
  }
  // I22: cgroup v1. Ohne c/<name>/cpu müssen die Kacheln Zahlen von Docker
  // tragen und der Hinweis stehen.
  const werte = (api['/api/ueberwachung'] || {}).werte || {};
  const cg = Object.keys(werte).some(k => k.startsWith('c/') && k.endsWith('/cpu'));
  const runC = ((api['/api/container'] || {}).container || []).filter(c => c.laeuft && c.cpu != null);
  if (!cg && runC.length) for (const w of [1440, 390]) {
    const wer = ((S[w + ' #/ueberwachung'] || {}).zahlen || {}).wer || '';
    if (!wer) continue;
    if (!/keinen Verlauf je Container/.test(wer)) v('I22', w + ' Wer braucht gerade was', 'Hinweis zu cgroup v1 fehlt');
    if (!/\d\s?%/.test(wer)) v('I22', w + ' Wer braucht gerade was', 'keine Zahl, obwohl Docker Werte liefert: ' + wer.slice(0, 160));
  }
  // I21: so gelassene Domains.
  const kept = keptDomains(api);
  if (!kept.size) return;
  const z = (api['/api/domains/zustand'] || {}).zustand || [];
  const otherBad = z.filter(d => !kept.has(d.domain) && (d.stufe === 'err' || d.stufe === 'warn'));
  for (const [wo, s] of Object.entries(S)) {
    if (Array.isArray(s)) continue;
    for (const m of s.marken) {
      if (m.art === 'abzeichen' && /^Domains\b/.test(m.umfeld || '') && !otherBad.length) v('I21', wo, 'Abzeichen ' + m.umfeld + ', obwohl nur so gelassene Domains nicht antworten');
      // Die Zeile „Bewusst so gelassen: …“ unter einer Karte, die aus einem
      // anderen Grund gelb ist, zählt nicht.
      const own = t => String(t || '').split(/Bewusst so gelassen/i)[0];
      if ((m.stufe === 'err' || m.stufe === 'warn') && [...kept].some(d => (own(m.text) + ' ' + own(m.umfeld)).includes(d))) v('I21', wo, m.art + ' ' + m.stufe + ': ' + (m.text + ' · ' + (m.umfeld || '')).slice(0, 200));
    }
    for (const d of s.domains || []) if (kept.has(d.domain) && d.wo === 'domains') {
      const a = z.find(x => x.domain === d.domain);
      if (a && (a.stufe === 'err' || a.stufe === 'warn') && d.stufe !== 'idle') v('I21', wo + ' ' + d.domain, 'Zeile ' + d.stufe + ' statt grau');
    }
  }
}

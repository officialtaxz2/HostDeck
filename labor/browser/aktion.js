// Ruft die API eines Labor-Leitstands auf, wie es die Oberfläche tut:
// anmelden, bei „bitte bestätigen“ mit dem Passwort bestätigen, dann noch einmal.
//   node labor/browser/aktion.js <host> GET  /api/lauf
//   node labor/browser/aktion.js <host> POST /api/container/vorschau '{"ziel":"projekt:blog","aktion":"stoppen"}'
//   node labor/browser/aktion.js <host> STOP projekt:blog     (Vorschau + Aktion)
//   node labor/browser/aktion.js <host> START projekt:blog
'use strict';
const L = require('./lib');
(async () => {
  const [host, method, p, body] = process.argv.slice(2);
  const pw = host.startsWith('hostdeck.') ? 'leitstand-alt' : 'leitstand-neu';
  const ctx = await L.context();
  await L.login(ctx, host, pw);
  const call = async (path, b) => {
    let r = await L.api(ctx, host, path, b);
    if (r.status === 403 && r.data && r.data.bestaetigen) { await L.confirm(ctx, host, pw); r = await L.api(ctx, host, path, b); }
    return r;
  };
  let r;
  if (method === 'STOP' || method === 'START' || method === 'RESTART') {
    const aktion = { STOP: 'stoppen', START: 'starten', RESTART: 'neustarten' }[method];
    const v = await call('/api/container/vorschau', { ziel: p, aktion });
    if (!v.ok) { console.log(JSON.stringify(v)); process.exit(1); }
    r = await call('/api/container/aktion', { ziel: p, aktion, container: v.data.container });
  } else {
    r = await call(p, method === 'GET' ? undefined : (body ? JSON.parse(body) : {}));
  }
  console.log(JSON.stringify(r, null, 1));
  await L.close(ctx);
  process.exit(r.ok ? 0 : 1);
})().catch(e => { console.error(String(e)); process.exit(2); });

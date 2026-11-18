// Service Worker des Leitstands: zeigt Mitteilungen von HostDeck (Web Push).
// Er speichert nichts zwischen; der Leitstand lädt immer frisch vom Server.
'use strict';
self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', ev => ev.waitUntil(self.clients.claim()));
self.addEventListener('push', ev => {
  let d = {};
  try { d = ev.data ? ev.data.json() : {}; } catch (e) { d = { titel: 'HostDeck', text: ev.data ? ev.data.text() : '' }; }
  const url = typeof d.link === 'string' && d.link.startsWith(self.location.origin + '/') ? d.link : '/#/ueberwachung';
  ev.waitUntil(self.registration.showNotification(d.titel || 'HostDeck', {
    body: d.text || '', tag: d.tag || undefined, renotify: !!d.tag, icon: '/icon-192.png', badge: '/icon-192.png', data: { url },
  }));
});
self.addEventListener('notificationclick', ev => {
  ev.notification.close();
  const url = (ev.notification.data && ev.notification.data.url) || '/';
  ev.waitUntil(self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then(list => {
    for (const c of list) {
      if ('focus' in c) { if ('navigate' in c) c.navigate(url).catch(() => {}); return c.focus(); }
    }
    return self.clients.openWindow(url);
  }));
});

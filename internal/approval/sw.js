'use strict';
self.addEventListener('push', event => {
  event.waitUntil((async () => {
    // The push carries no content; ask the server what it is about.
    let body = NOTIFICATION_BODY;
    try {
      const response = await fetch('/approvals/latest', {credentials: 'same-origin', cache: 'no-store', redirect: 'error'});
      if (response.ok) {
        const latest = await response.json();
        if (typeof latest.body === 'string' && latest.body) body = latest.body;
      }
    } catch (_) {}
    return self.registration.showNotification('opsgate', {body, tag: 'opsgate-approval'});
  })());
});
self.addEventListener('notificationclick', event => {
  event.notification.close();
  // The push carries no content; the server picks the newest waiting item.
  const target = '/approvals/next';
  event.waitUntil((async () => {
    const windows = await self.clients.matchAll({type: 'window', includeUncontrolled: true});
    for (const client of windows) {
      const url = new URL(client.url);
      // Only pages this worker controls (under /approvals) can be navigated.
      if (url.origin === self.location.origin && url.pathname.startsWith('/approvals') && 'navigate' in client) {
        await client.focus();
        return client.navigate(target);
      }
    }
    return self.clients.openWindow(target);
  })());
});

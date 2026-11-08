'use strict';
(() => {
  const enable = document.getElementById('enable-push');
  const disable = document.getElementById('disable-push');
  const status = document.getElementById('push-status');
  const registration = 'serviceWorker' in navigator
    ? navigator.serviceWorker.register('/approvals/sw.js', {scope: '/approvals'})
    : null;
  if (registration) registration.catch(() => {
    if (status) status.textContent = 'Cannot register notifications on this device.';
  });
  if (!enable || !disable) return;
  if (!registration || !('PushManager' in window) || !('Notification' in window)) {
    enable.disabled = disable.disabled = true;
    status.textContent = 'Notifications are unavailable. On iPhone, add opsgate to the Home Screen first.';
    return;
  }
  async function post(operation, subscription) {
    const response = await fetch('/approvals/push/' + operation, {
      method: 'POST', credentials: 'same-origin',
      headers: {'Content-Type': 'application/json', 'X-CSRF-Token': document.getElementById('push-csrf').value},
      body: JSON.stringify(subscription)
    });
    if (!response.ok || response.redirected) throw new Error('Cannot update notifications');
  }
  enable.addEventListener('click', async () => {
    enable.disabled = disable.disabled = true;
    try {
      // Keep the permission prompt directly within the user's gesture.
      if (await Notification.requestPermission() !== 'granted') {
        status.textContent = 'Notification permission was not granted.';
        return;
      }
      await registration;
      const worker = await navigator.serviceWorker.ready;
      const response = await fetch('/approvals/push/key', {credentials: 'same-origin'});
      if (!response.ok || response.redirected) throw new Error('Cannot load notification key');
      const {key} = await response.json();
      const encoded = key.replace(/-/g, '+').replace(/_/g, '/');
      const bytes = Uint8Array.from(atob(encoded + '='.repeat((4 - encoded.length % 4) % 4)), c => c.charCodeAt(0));
      const subscription = await worker.pushManager.subscribe({userVisibleOnly: true, applicationServerKey: bytes});
      await post('subscribe', subscription.toJSON());
      status.textContent = 'Notifications enabled on this device.';
    } catch (_) {
      status.textContent = 'Cannot enable notifications. Reopen the inbox after signing in, or check browser permissions.';
    } finally { enable.disabled = disable.disabled = false; }
  });
  disable.addEventListener('click', async () => {
    enable.disabled = disable.disabled = true;
    try {
      const worker = await registration;
      const subscription = await worker.pushManager.getSubscription();
      if (subscription) {
        await post('unsubscribe', {endpoint: subscription.endpoint});
        await subscription.unsubscribe();
      }
      status.textContent = 'Notifications disabled on this device.';
    } catch (_) { status.textContent = 'Cannot disable notifications. Reopen the inbox after signing in.'; }
    finally { enable.disabled = disable.disabled = false; }
  });
})();

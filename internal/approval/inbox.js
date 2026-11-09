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
  // iPhone and iPad deliver Web Push only to apps added to the Home Screen.
  const apple = /iPhone|iPad|iPod/.test(navigator.userAgent) || (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1);
  const installed = navigator.standalone === true || window.matchMedia('(display-mode: standalone)').matches;
  const hint = document.getElementById('install-hint');
  if (apple && !installed && hint) hint.hidden = false;
  const test = document.getElementById('test-push');
  if (test) test.addEventListener('click', async () => {
    test.disabled = true;
    try {
      const response = await fetch('/approvals/push/test', {
        method: 'POST', credentials: 'same-origin',
        headers: {'X-CSRF-Token': document.getElementById('push-csrf').value}
      });
      if (response.status === 429) { status.textContent = 'Wait a few seconds before sending another test.'; return; }
      if (!response.ok || response.redirected) throw new Error('test failed');
      const {devices} = await response.json();
      status.textContent = devices
        ? 'Sent to ' + devices + ' device(s). If nothing arrives within a minute, disable and enable notifications again on this device. / ' + devices + '대에 보냈습니다. 1분 안에 오지 않으면 이 기기에서 알림을 껐다가 다시 켜세요.'
        : 'No device has notifications on. Enable them first. / 알림을 켠 기기가 없습니다. 먼저 알림을 켜세요.';
    } catch (_) {
      status.textContent = 'Cannot send a test notification. Reopen the inbox after signing in.';
    } finally { test.disabled = false; }
  });
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

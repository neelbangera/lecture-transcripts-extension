// Paste into the DevTools console of an authenticated Leccap lecture page
// BEFORE clicking Show Transcript, then click Show Transcript once.
// It prints the activation -> second-stable-snapshot time in milliseconds,
// using the plan's compound completion rule and 1500ms debounce.
(() => {
  const DEBOUNCE_MS = 1500, TIMEOUT_MS = 30000;
  const container = () => document.querySelector('.transcript-viewer');
  const openSignal = () => [...document.querySelectorAll('#sourcebar button')]
    .some(b => b.getAttribute('title') === 'Hide Transcript');
  const snapshot = () => {
    const c = container();
    if (!c) return null;
    const rows = [...c.querySelectorAll('.transcript-row .transcript-text')]
      .map(el => el.textContent).join('\n');
    return rows.trim() === '' ? null : rows;
  };
  const t0 = performance.now();
  let last = null, lastAt = null, done = false;
  const obs = new MutationObserver(() => { lastAt = performance.now(); });
  const iv = setInterval(() => {
    const now = performance.now();
    if (now - t0 > TIMEOUT_MS) { finish('TIMEOUT'); return; }
    if (!openSignal()) return;
    const snap = snapshot();
    if (snap === null) return;
    if (last !== null && snap === last && lastAt !== null && now - lastAt >= DEBOUNCE_MS) {
      finish(now - t0);
    } else if (snap !== last) { last = snap; lastAt = now; }
  }, 200);
  function finish(result) {
    clearInterval(iv); obs.disconnect();
    console.log(result === 'TIMEOUT'
      ? 'RENDER-TIME: TIMEOUT after 30000ms'
      : `RENDER-TIME: ${Math.round(result)} ms (activation -> second stable snapshot)`);
  }
  console.log('Armed. Now click "Show Transcript".');
})();

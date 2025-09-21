(() => {
  const COOKIE_NAME = 'pb_theme';
  const root = document.documentElement;
  if (!root) return;

  const cookies = document.cookie ? document.cookie.split(';') : [];
  const prefix = `${COOKIE_NAME}=`;
  let stored = '';
  for (const raw of cookies) {
    const entry = raw.trim();
    if (entry && entry.startsWith(prefix)) {
      stored = decodeURIComponent(entry.slice(prefix.length));
      break;
    }
  }

  const isExplicit = stored === 'light' || stored === 'dark';
  const prefersDark = !isExplicit && typeof window !== 'undefined'
    && typeof window.matchMedia === 'function'
    && window.matchMedia('(prefers-color-scheme: dark)').matches;

  const theme = isExplicit ? stored : (prefersDark ? 'dark' : 'light');
  const preference = isExplicit ? stored : 'auto';

  root.setAttribute('data-theme', theme);
  root.setAttribute('data-theme-preference', preference);
  root.classList.toggle('theme-dark', theme === 'dark');
  root.classList.toggle('theme-light', theme !== 'dark');
})();

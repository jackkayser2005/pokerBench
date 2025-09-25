(() => {
  const init = () => {
    const header = document.querySelector('.site-header');
    if (header) {
      let lastScroll = window.scrollY;
      const threshold = 16;
      let ticking = false;

      const applyScrollState = () => {
        const current = window.scrollY;
        const goingDown = current > lastScroll + 6;
        const goingUp = current < lastScroll - 6;

        if (current <= threshold) {
          header.classList.remove('site-header--hidden');
          header.classList.remove('site-header--shadow');
        } else {
          header.classList.add('site-header--shadow');
          if (goingDown) {
            header.classList.add('site-header--hidden');
          } else if (goingUp) {
            header.classList.remove('site-header--hidden');
          }
        }

        lastScroll = current;
        ticking = false;
      };

      const onScroll = () => {
        if (!ticking) {
          window.requestAnimationFrame(applyScrollState);
          ticking = true;
        }
      };

      window.addEventListener('scroll', onScroll, { passive: true });
      header.addEventListener('mouseenter', () => header.classList.remove('site-header--hidden'));
      header.addEventListener('focusin', () => header.classList.remove('site-header--hidden'));
      applyScrollState();
    }

    const navLinks = document.querySelectorAll('.topnav__links a[href]');
    if (navLinks.length) {
      const pathParts = location.pathname.split('/');
      const currentSlug = (pathParts[pathParts.length - 1] || '').toLowerCase();
      navLinks.forEach(link => {
        const href = link.getAttribute('href') || '';
        const hrefParts = href.split('/');
        const slug = (hrefParts[hrefParts.length - 1] || '').toLowerCase();
        if (!slug) return;
        if (currentSlug === slug) {
          link.classList.add('active');
          link.setAttribute('aria-current', 'page');
        } else {
          link.classList.remove('active');
          link.removeAttribute('aria-current');
        }
      });
    }

    const navToggle = document.querySelector('.topnav__toggle');
    const navHost = navToggle?.closest('.topnav');
    const navMenuId = navToggle?.getAttribute('aria-controls');
    const navMenu = navMenuId ? document.getElementById(navMenuId) : null;
    const navMobileMedia = typeof window.matchMedia === 'function'
      ? window.matchMedia('(max-width: 720px)')
      : null;

    if (navToggle && navHost && navMenu) {
      const navSettingsToggle = document.querySelector('.nav-settings');
      const navSettingsPanel = document.getElementById('navSettingsPanel');
      const navSettingsHost = navSettingsToggle?.closest('.topnav__actions');
      const isMobileMode = () => (navMobileMedia ? navMobileMedia.matches : window.innerWidth <= 720);
      let isMenuOpen = false;

      const applyMenuAttributes = () => {
        const body = document.body;
        if (!isMobileMode()) {
          isMenuOpen = false;
          navHost.classList.remove('topnav--collapsible', 'topnav--menu-open');
          navMenu.removeAttribute('data-menu-open');
          navMenu.removeAttribute('aria-hidden');
          navToggle.setAttribute('aria-expanded', 'false');
          navToggle.setAttribute('aria-label', 'Menu');
          navToggle.setAttribute('title', 'Menu');
          navToggle.hidden = true;
          navToggle.setAttribute('aria-hidden', 'true');
          body?.classList.remove('has-open-mobile-nav');
          return;
        }

        navToggle.hidden = false;
        navToggle.removeAttribute('aria-hidden');
        navHost.classList.add('topnav--collapsible');
        if (isMenuOpen) {
          navHost.classList.add('topnav--menu-open');
          navMenu.setAttribute('data-menu-open', 'true');
          navMenu.removeAttribute('aria-hidden');
          navToggle.setAttribute('aria-expanded', 'true');
          navToggle.setAttribute('aria-label', 'Close menu');
          navToggle.setAttribute('title', 'Close menu');
          body?.classList.add('has-open-mobile-nav');
        } else {
          navHost.classList.remove('topnav--menu-open');
          navMenu.setAttribute('data-menu-open', 'false');
          navMenu.setAttribute('aria-hidden', 'true');
          navToggle.setAttribute('aria-expanded', 'false');
          navToggle.setAttribute('aria-label', 'Open menu');
          navToggle.setAttribute('title', 'Open menu');
          body?.classList.remove('has-open-mobile-nav');
        }
      };

      const closeSettingsPanel = () => {
        if (navSettingsHost?.classList.contains('is-open')) {
          navSettingsHost.classList.remove('is-open');
          navSettingsToggle?.setAttribute('aria-expanded', 'false');
          if (navSettingsPanel) {
            navSettingsPanel.hidden = true;
          }
        }
      };

      const openMenu = options => {
        if (!isMobileMode() || isMenuOpen) return;
        closeSettingsPanel();
        isMenuOpen = true;
        applyMenuAttributes();
        if (options?.focusFirst) {
          const firstLink = navMenu.querySelector('a');
          firstLink?.focus?.({ preventScroll: true });
        }
      };

      const closeMenu = options => {
        const wasOpen = isMenuOpen;
        isMenuOpen = false;
        applyMenuAttributes();
        if (wasOpen && !options?.skipFocus && isMobileMode()) {
          navToggle.focus?.({ preventScroll: true });
        }
      };

      navToggle.addEventListener('click', event => {
        event.preventDefault();
        const focusFirst = event.detail === 0;
        if (isMenuOpen) {
          closeMenu();
        } else {
          openMenu({ focusFirst });
        }
      });

      navMenu.addEventListener('click', event => {
        if (!isMobileMode()) return;
        if (event.target.closest('a')) {
          closeMenu({ skipFocus: true });
        }
      });

      document.addEventListener('click', event => {
        if (!isMenuOpen || !isMobileMode()) return;
        if (navHost.contains(event.target)) return;
        closeMenu({ skipFocus: true });
      });

      document.addEventListener('keydown', event => {
        if (event.key === 'Escape' && isMenuOpen && isMobileMode()) {
          closeMenu();
        }
      });

      const handleMediaChange = () => {
        if (!isMobileMode()) {
          closeMenu({ skipFocus: true });
        } else {
          applyMenuAttributes();
        }
      };

      if (navMobileMedia) {
        if (typeof navMobileMedia.addEventListener === 'function') {
          navMobileMedia.addEventListener('change', handleMediaChange);
        } else if (typeof navMobileMedia.addListener === 'function') {
          navMobileMedia.addListener(handleMediaChange);
        }
      } else {
        window.addEventListener('resize', handleMediaChange);
      }

      applyMenuAttributes();
    }

    const TOOLTIP_COOKIE = 'pb_tooltips';
    const TOOLTIP_COOKIE_MAX_AGE = 60 * 60 * 24 * 365;
    const THEME_COOKIE = 'pb_theme';
    const THEME_COOKIE_MAX_AGE = TOOLTIP_COOKIE_MAX_AGE;

    const getCookie = name => {
      if (!name || !document.cookie) return '';
      const prefix = `${name}=`;
      const parts = document.cookie.split(';');
      for (const part of parts) {
        const trimmed = part.trim();
        if (trimmed && trimmed.startsWith(prefix)) {
          return decodeURIComponent(trimmed.slice(prefix.length));
        }
      }
      return '';
    };

    const setCookie = (name, value, maxAge) => {
      if (!name) return;
      const safeValue = encodeURIComponent(value ?? '');
      const age = Number.isFinite(maxAge) ? `; max-age=${Math.max(0, Math.trunc(maxAge))}` : '';
      document.cookie = `${name}=${safeValue}${age}; path=/; SameSite=Lax`;
    };

    let tooltipToggle = null;
    let tooltipHint = null;
    let tooltipsEnabled = getCookie(TOOLTIP_COOKIE) !== 'off';

    const VALID_THEME_PREFERENCES = new Set(['auto', 'light', 'dark']);
    const sanitizeThemePreference = value => (VALID_THEME_PREFERENCES.has(value) ? value : 'auto');
    const themeMedia = typeof window.matchMedia === 'function'
      ? window.matchMedia('(prefers-color-scheme: dark)')
      : null;
    let themePreference = sanitizeThemePreference(getCookie(THEME_COOKIE));
    let themeToggle = null;
    let themeHint = null;

    const getResolvedTheme = () => {
      if (themePreference === 'dark') return 'dark';
      if (themePreference === 'light') return 'light';
      return themeMedia?.matches ? 'dark' : 'light';
    };

    const themeLabels = {
      auto: 'Auto',
      light: 'Light',
      dark: 'Dark',
    };

    const syncTooltipsPreference = () => {
      if (document.body) {
        document.body.classList.toggle('tooltips-disabled', !tooltipsEnabled);
      }
      if (tooltipToggle) {
        tooltipToggle.setAttribute('aria-checked', tooltipsEnabled ? 'true' : 'false');
        tooltipToggle.classList.toggle('is-off', !tooltipsEnabled);
        tooltipToggle.setAttribute('title', tooltipsEnabled ? 'Disable guided tips' : 'Enable guided tips');
      }
      if (tooltipHint) {
        tooltipHint.textContent = tooltipsEnabled ? 'On' : 'Off';
      }
      if (!tooltipsEnabled && document.activeElement?.classList?.contains('inline-tip')) {
        document.activeElement.blur();
      }
    };

    const syncThemePreference = () => {
      const resolved = getResolvedTheme();
      const root = document.documentElement;
      if (root) {
        root.setAttribute('data-theme', resolved);
        root.setAttribute('data-theme-preference', themePreference);
        root.classList.toggle('theme-dark', resolved === 'dark');
        root.classList.toggle('theme-light', resolved !== 'dark');
      }
      if (document.body) {
        document.body.classList.toggle('theme-dark', resolved === 'dark');
        document.body.classList.toggle('theme-light', resolved !== 'dark');
      }

      const label = themeLabels[themePreference] || themePreference;
      const resolvedLabel = resolved.charAt(0).toUpperCase() + resolved.slice(1);
      const hintText = themePreference === 'auto' ? `${label} · ${resolvedLabel}` : label;
      const description = themePreference === 'auto'
        ? `${label} (currently ${resolvedLabel})`
        : label;

      if (themeToggle) {
        const controlLabel = `Theme: ${description}. Click to change.`;
        themeToggle.setAttribute('aria-label', controlLabel);
        themeToggle.setAttribute('title', controlLabel);
        themeToggle.setAttribute('data-theme-preference', themePreference);
        themeToggle.setAttribute('data-theme-resolved', resolved);
      }
      if (themeHint) {
        themeHint.textContent = hintText;
        themeHint.setAttribute('data-theme-preference', themePreference);
        themeHint.setAttribute('aria-label', `Theme preference: ${description}`);
      }
    };

    syncTooltipsPreference();
    syncThemePreference();

    const handleThemeMediaChange = () => {
      if (themePreference === 'auto') {
        syncThemePreference();
      }
    };

    if (themeMedia) {
      if (typeof themeMedia.addEventListener === 'function') {
        themeMedia.addEventListener('change', handleThemeMediaChange);
      } else if (typeof themeMedia.addListener === 'function') {
        themeMedia.addListener(handleThemeMediaChange);
      }
    }

    const settingsToggle = document.querySelector('.nav-settings');
    const settingsPanel = document.getElementById('navSettingsPanel');
    const actionsHost = settingsToggle?.closest('.topnav__actions');
    if (settingsToggle && settingsPanel && actionsHost) {
      const closePanel = () => {
        actionsHost.classList.remove('is-open');
        settingsToggle.setAttribute('aria-expanded', 'false');
        settingsPanel.hidden = true;
      };

      const openPanel = () => {
        actionsHost.classList.add('is-open');
        settingsToggle.setAttribute('aria-expanded', 'true');
        settingsPanel.hidden = false;
      };

      const togglePanel = () => {
        const isOpen = actionsHost.classList.contains('is-open');
        if (isOpen) {
          closePanel();
        } else {
          openPanel();
          settingsPanel.focus?.({ preventScroll: true });
        }
      };

      settingsToggle.addEventListener('click', event => {
        event.preventDefault();
        togglePanel();
      });

      document.addEventListener('click', event => {
        if (!actionsHost.contains(event.target)) {
          closePanel();
        }
      });

      document.addEventListener('keydown', event => {
        if (event.key === 'Escape') closePanel();
      });

      tooltipToggle = settingsPanel.querySelector('[data-setting="tooltips"]');
      tooltipHint = settingsPanel.querySelector('[data-setting-state="tooltips"]');
      syncTooltipsPreference();

      if (tooltipToggle) {
        tooltipToggle.addEventListener('click', () => {
          tooltipsEnabled = !tooltipsEnabled;
          syncTooltipsPreference();
          setCookie(TOOLTIP_COOKIE, tooltipsEnabled ? 'on' : 'off', TOOLTIP_COOKIE_MAX_AGE);
        });
      }

      themeToggle = settingsPanel.querySelector('[data-setting="theme"]');
      themeHint = settingsPanel.querySelector('[data-setting-state="theme"]');
      syncThemePreference();

      if (themeToggle) {
        const themeOrder = ['auto', 'light', 'dark'];
        themeToggle.addEventListener('click', event => {
          const currentIndex = Math.max(0, themeOrder.indexOf(themePreference));
          const step = event.shiftKey ? -1 : 1;
          const nextIndex = (currentIndex + step + themeOrder.length) % themeOrder.length;
          themePreference = themeOrder[nextIndex];
          syncThemePreference();
          setCookie(THEME_COOKIE, themePreference, THEME_COOKIE_MAX_AGE);
        });
      }

      closePanel();
    }
  };

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init, { once: true });
  } else {
    init();
  }
})();

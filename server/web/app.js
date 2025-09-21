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

      closePanel();
    }
  };

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init, { once: true });
  } else {
    init();
  }
})();

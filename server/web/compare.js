(() => {
  const ensureNamespace = () => {
    if (!window.PokerBench) window.PokerBench = {};
    if (!window.PokerBench.CompareDrawer) window.PokerBench.CompareDrawer = {};
    return window.PokerBench.CompareDrawer;
  };

  const escapeHtml = (value = '') => String(value)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');

  const numberFormatter = new Intl.NumberFormat();
  const dateTimeFormatter = new Intl.DateTimeFormat([], {
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit'
  });

  const formatNumber = (value, placeholder = '—') => {
    const num = Number(value);
    if (!Number.isFinite(num)) return placeholder;
    return numberFormatter.format(num);
  };

  const formatPercent = (wins, hands) => {
    const w = Number(wins);
    const h = Number(hands);
    if (!Number.isFinite(w) || !Number.isFinite(h) || h <= 0) return '—';
    const pct = Math.max(0, Math.min(100, (w / h) * 100));
    const rounded = Math.round(pct * 10) / 10;
    return `${rounded.toFixed(1)}%`;
  };

  const formatDateTime = (iso) => {
    if (!iso) return '—';
    const date = new Date(iso);
    if (Number.isNaN(date.getTime())) return '—';
    return dateTimeFormatter.format(date);
  };

  const formatChips = (value) => {
    const num = Number(value);
    if (!Number.isFinite(num) || num === 0) return '0';
    const prefix = num > 0 ? '+' : '−';
    return `${prefix}${formatNumber(Math.abs(num))}`;
  };

  const chipClass = (value) => {
    const num = Number(value);
    if (!Number.isFinite(num) || num === 0) return 'neutral';
    return num > 0 ? 'positive' : 'negative';
  };

  class Drawer {
    constructor(root, options = {}) {
      if (!root) throw new Error('CompareDrawer requires a root element');
      this.root = root;
      this.options = options;
      this.cache = new Map();
      this.currentKey = '';
      this.active = false;
      this.lastMeta = null;
      this.build();
      this.close({ silent: true });
    }

    build() {
      if (!this.root.classList.contains('compare-drawer')) {
        this.root.classList.add('compare-drawer');
      }
      this.root.innerHTML = `
        <div class="compare-drawer__header">
          <div>
            <div class="compare-drawer__eyebrow">Matchup insights</div>
            <h2 class="compare-drawer__title">No matchup selected</h2>
          </div>
          <button type="button" class="compare-drawer__close" aria-label="Close comparison">
            <span aria-hidden="true">×</span>
          </button>
        </div>
        <div class="compare-drawer__body">
          <div class="compare-drawer__status" data-section="status"></div>
          <div class="compare-drawer__summary" data-section="summary" hidden></div>
          <div class="compare-drawer__matches" data-section="matches" hidden>
            <div class="compare-drawer__section-title">Recent matches</div>
            <ul class="compare-drawer__match-list"></ul>
          </div>
        </div>
      `;
      this.titleEl = this.root.querySelector('.compare-drawer__title');
      this.defaultTitle = 'No matchup selected';
      this.titleEl.textContent = this.defaultTitle;
      this.statusEl = this.root.querySelector('[data-section="status"]');
      this.summaryEl = this.root.querySelector('[data-section="summary"]');
      this.matchesEl = this.root.querySelector('[data-section="matches"]');
      this.matchListEl = this.root.querySelector('.compare-drawer__match-list');
      this.closeBtn = this.root.querySelector('.compare-drawer__close');
      if (this.closeBtn) {
        this.closeBtn.addEventListener('click', () => this.close());
      }
    }

    open(message, detail) {
      this.active = true;
      this.root.hidden = false;
      this.root.classList.add('is-open');
      if (message !== undefined) {
        this.renderIntro(message, detail);
      }
    }

    close(options = {}) {
      this.active = false;
      this.currentKey = '';
      this.root.classList.remove('is-open');
      this.root.hidden = true;
      this.statusEl.hidden = false;
      this.statusEl.innerHTML = '';
      this.summaryEl.hidden = true;
      this.summaryEl.innerHTML = '';
      this.matchesEl.hidden = true;
      this.matchListEl.innerHTML = '';
      this.titleEl.textContent = this.defaultTitle;
      if (!options.silent && typeof this.options.onClose === 'function') {
        this.options.onClose();
      }
    }

    renderIntro(message = 'Select two bots to compare.', detail = '') {
      this.titleEl.textContent = this.defaultTitle;
      const lines = [`<p>${escapeHtml(message)}</p>`];
      if (detail) lines.push(`<p class="compare-drawer__muted">${escapeHtml(detail)}</p>`);
      this.statusEl.hidden = false;
      this.statusEl.innerHTML = lines.join('');
      this.summaryEl.hidden = true;
      this.summaryEl.innerHTML = '';
      this.matchesEl.hidden = true;
      this.matchListEl.innerHTML = '';
    }

    showIntro(message = 'Select two bots to compare.', detail = '') {
      this.open();
      this.renderIntro(message, detail);
    }

    showPending(selection = []) {
      this.open();
      if (!selection.length) {
        this.showIntro();
        return;
      }
      const first = selection[0] || {};
      const name = first.short || first.name || 'First bot';
      const detail = first.company ? `${first.company} selected` : '';
      this.titleEl.textContent = 'Select opponent';
      const body = [`<p>${escapeHtml(name)} selected. Choose another bot to compare.</p>`];
      if (detail) body.push(`<p class="compare-drawer__muted">${escapeHtml(detail)}</p>`);
      this.statusEl.hidden = false;
      this.statusEl.innerHTML = body.join('');
      this.summaryEl.hidden = true;
      this.summaryEl.innerHTML = '';
      this.matchesEl.hidden = true;
      this.matchListEl.innerHTML = '';
    }

    showLoading(meta = {}) {
      const aLabel = meta.aShort || meta.aName || 'Bot A';
      const bLabel = meta.bShort || meta.bName || 'Bot B';
      this.open();
      this.titleEl.textContent = `${aLabel} vs ${bLabel}`;
      this.statusEl.hidden = false;
      this.statusEl.innerHTML = `<p>Loading head-to-head results…</p>`;
      this.summaryEl.hidden = true;
      this.summaryEl.innerHTML = '';
      this.matchesEl.hidden = true;
      this.matchListEl.innerHTML = '';
    }

    showError(message = 'Comparison data is unavailable right now.') {
      this.open();
      this.titleEl.textContent = 'Matchup unavailable';
      this.statusEl.hidden = false;
      this.statusEl.innerHTML = `<p>${escapeHtml(message)}</p>`;
      this.summaryEl.hidden = true;
      this.summaryEl.innerHTML = '';
      this.matchesEl.hidden = true;
      this.matchListEl.innerHTML = '';
    }

    showMatchup({ aId, bId, meta = {} }) {
      const a = Number(aId);
      const b = Number(bId);
      if (!Number.isFinite(a) || !Number.isFinite(b) || a <= 0 || b <= 0 || a === b) {
        this.showError('Select two different bots to compare.');
        return;
      }
      this.lastMeta = meta;
      const key = `${a}|${b}`;
      this.currentKey = key;
      this.showLoading(meta);
      if (this.cache.has(key)) {
        const cached = this.cache.get(key);
        this.renderData(cached, meta);
        return;
      }
      const url = `/api/matchup?a=${encodeURIComponent(a)}&b=${encodeURIComponent(b)}`;
      fetch(url)
        .then(res => {
          if (!res.ok) throw new Error('failed');
          return res.json();
        })
        .then(data => {
          this.cache.set(key, data);
          if (this.currentKey === key) {
            this.renderData(data, meta);
          }
        })
        .catch(() => {
          if (this.currentKey === key) {
            this.showError('Comparison data is unavailable right now. Please try again soon.');
          }
        });
    }

    renderBotCard(bot = {}) {
      const name = bot.name || 'Unknown bot';
      const company = bot.company || '';
      const elo = Number(bot.elo);
      const eloDisplay = Number.isFinite(elo) ? Math.round(elo) : '—';
      return `
        <div class="compare-drawer__bot-card">
          <div class="compare-drawer__bot-name">${escapeHtml(name)}</div>
          ${company ? `<div class="compare-drawer__bot-meta">${escapeHtml(company)}</div>` : ''}
          <div class="compare-drawer__bot-metric"><span>Elo</span><strong>${escapeHtml(String(eloDisplay))}</strong></div>
        </div>
      `;
    }

    renderWinStat(label, opponent, pct, wins, hands) {
      const metaLine = Number(hands) > 0
        ? `${formatNumber(wins)} wins • ${formatNumber(hands)} hands`
        : 'No completed hands yet';
      return `
        <div class="compare-drawer__stat">
          <div class="compare-drawer__stat-label">${escapeHtml(label)} vs ${escapeHtml(opponent)}</div>
          <div class="compare-drawer__stat-value">${escapeHtml(pct)}</div>
          <div class="compare-drawer__stat-meta">${escapeHtml(metaLine)}</div>
        </div>
      `;
    }

    renderHandsStat(hands, matches) {
      const matchLine = Number(matches) > 0
        ? `${formatNumber(matches)} total matches`
        : 'No completed matches yet';
      return `
        <div class="compare-drawer__stat compare-drawer__stat--neutral">
          <div class="compare-drawer__stat-label">Hands played</div>
          <div class="compare-drawer__stat-value">${escapeHtml(formatNumber(hands))}</div>
          <div class="compare-drawer__stat-meta">${escapeHtml(matchLine)}</div>
        </div>
      `;
    }

    renderMatch(match, orientation) {
      const participants = Array.isArray(match.participants) ? match.participants : [];
      const a = participants.find(p => Number(p.bot_id) === orientation.aId) || {};
      const b = participants.find(p => Number(p.bot_id) === orientation.bId) || {};
      const handsText = Number(match.hands) > 0 ? `${formatNumber(match.hands)} hands` : 'Hands pending';
      const created = formatDateTime(match.created_at);
      const replayHref = `/web/replay.html?match_id=${encodeURIComponent(match.match_id)}`;
      const aChips = Number.isFinite(Number(a.net_chips)) ? formatChips(a.net_chips) : '0';
      const bChips = Number.isFinite(Number(b.net_chips)) ? formatChips(b.net_chips) : '0';
      return `
        <li class="compare-drawer__match-item">
          <a class="compare-drawer__match" href="${escapeHtml(replayHref)}">
            <div class="compare-drawer__match-head">
              <span class="compare-drawer__match-date">${escapeHtml(created)}</span>
              <span class="compare-drawer__match-hands">${escapeHtml(handsText)}</span>
            </div>
            <div class="compare-drawer__match-line">
              <span class="compare-drawer__match-name">${escapeHtml(orientation.aName)}</span>
              <span class="compare-drawer__chips compare-drawer__chips--${chipClass(a.net_chips)}">${escapeHtml(aChips)}</span>
            </div>
            <div class="compare-drawer__match-line">
              <span class="compare-drawer__match-name">${escapeHtml(orientation.bName)}</span>
              <span class="compare-drawer__chips compare-drawer__chips--${chipClass(b.net_chips)}">${escapeHtml(bChips)}</span>
            </div>
          </a>
        </li>
      `;
    }

    renderData(data = {}, meta = {}) {
      this.statusEl.hidden = true;
      this.statusEl.innerHTML = '';
      const bots = Array.isArray(data.bots) ? data.bots : [];
      const summary = data.summary || {};
      const aId = Number(data.a_id ?? meta.aId);
      const bId = Number(data.b_id ?? meta.bId);
      const botMap = new Map(bots.map(b => [Number(b.id), b]));
      const aBot = botMap.get(aId) || botMap.values().next().value || {};
      const remainingBots = bots.filter(b => Number(b.id) !== Number(aBot.id));
      const bBot = botMap.get(bId) || remainingBots[0] || {};
      const aName = meta.aShort || meta.aName || aBot.name || 'Bot A';
      const bName = meta.bShort || meta.bName || bBot.name || 'Bot B';
      const aCompany = meta.aCompany || aBot.company || '';
      const bCompany = meta.bCompany || bBot.company || '';

      this.titleEl.textContent = `${aName} vs ${bName}`;

      const hands = Number(summary.hands || 0);
      const matches = Number(summary.matches || 0);
      const aWins = Number(summary.a_wins || 0);
      const bWins = Number(summary.b_wins || 0);
      const aPct = formatPercent(aWins, hands);
      const bPct = formatPercent(bWins, hands);

      this.summaryEl.hidden = false;
      this.summaryEl.innerHTML = `
        <div class="compare-drawer__pair">
          ${this.renderBotCard({ name: aName, company: aCompany, elo: aBot.elo })}
          <div class="compare-drawer__versus">vs</div>
          ${this.renderBotCard({ name: bName, company: bCompany, elo: bBot.elo })}
        </div>
        <div class="compare-drawer__stat-grid">
          ${this.renderWinStat(aName, bName, aPct, aWins, hands)}
          ${this.renderWinStat(bName, aName, bPct, bWins, hands)}
          ${this.renderHandsStat(hands, matches)}
        </div>
      `;

      const orientation = {
        aId,
        bId,
        aName,
        bName
      };
      const matchesList = Array.isArray(data.recent_matches) ? data.recent_matches.slice(0, 10) : [];
      if (matchesList.length === 0) {
        this.matchesEl.hidden = false;
        this.matchListEl.innerHTML = '<li class="compare-drawer__empty">No recent matches recorded for this pairing yet.</li>';
        return;
      }
      this.matchesEl.hidden = false;
      this.matchListEl.innerHTML = matchesList.map(match => this.renderMatch(match, orientation)).join('');
    }
  }

  const ns = ensureNamespace();
  ns.create = (root, options) => new Drawer(root, options);
})();

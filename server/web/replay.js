const ReplayPage = (() => {
  const $ = (selector, root = document) => root.querySelector(selector);
  const $$ = (selector, root = document) => Array.from(root.querySelectorAll(selector));
  const q = new URLSearchParams(window.location.search);

  const INTRO_HOLD_FACTOR = 1.8;
  const SPEED_BASE = 1500;
  const FLIP_DELAY = 80;

  const state = {
    matchId: q.get('match_id'),
    matchList: [],
    currentMatch: null,
    rows: [],
    index: 0,
    playing: false,
    timer: null,
    speed: SPEED_BASE,
    modelA: 'A',
    modelB: 'B',
    holeMode: 'both',
    prevBoardKey: '',
    prevHoles: { SB: '', BB: '' },
    prevHandId: null,
    dealerSeat: 'SB',
    winnerSeat: null,
    startStacks: { SB: 0, BB: 0 },
    baseEquity: { SB: 0, BB: 0 },
    actionButtons: [],
  };

  const els = {};

  const SUIT_INFO = {
    club: { name: 'club', glyph: '♣', aria: 'clubs' },
    diamond: { name: 'diamond', glyph: '♦', aria: 'diamonds' },
    heart: { name: 'heart', glyph: '♥', aria: 'hearts' },
    spade: { name: 'spade', glyph: '♠', aria: 'spades' }
  };

  const SUIT_ALIASES = new Map([
    ['c', 'club'], ['club', 'club'], ['clubs', 'club'], ['♣', 'club'], ['♣️', 'club'], ['♧', 'club'],
    ['d', 'diamond'], ['diamond', 'diamond'], ['diamonds', 'diamond'], ['♦', 'diamond'], ['♦️', 'diamond'], ['♢', 'diamond'],
    ['h', 'heart'], ['heart', 'heart'], ['hearts', 'heart'], ['♥', 'heart'], ['♥️', 'heart'], ['♡', 'heart'],
    ['s', 'spade'], ['spade', 'spade'], ['spades', 'spade'], ['♠', 'spade'], ['♠️', 'spade'], ['♤', 'spade'],
  ]);

  const NUMBER_WORDS = { 2: 'two', 3: 'three', 4: 'four', 5: 'five', 6: 'six', 7: 'seven', 8: 'eight', 9: 'nine', 10: 'ten' };

  const rankLookup = new Map();
  const registerRank = (text, aria, ...keys) => {
    const info = { text, aria };
    keys.forEach(key => {
      if (key != null) rankLookup.set(String(key).toUpperCase(), info);
    });
  };
  registerRank('A', 'ace', 'A', 'ACE', '1', '14');
  registerRank('K', 'king', 'K', 'KING', '13');
  registerRank('Q', 'queen', 'Q', 'QUEEN', '12');
  registerRank('J', 'jack', 'J', 'JACK', '11');
  registerRank('10', 'ten', 'T', '10', 'TEN', '0');
  for (let n = 2; n <= 9; n += 1) {
    registerRank(String(n), NUMBER_WORDS[n], String(n));
  }

  function normalizeRank(value) {
    if (value == null) return { text: '', aria: '' };
    if (typeof value === 'number' && Number.isFinite(value)) {
      const info = rankLookup.get(String(Math.trunc(value)));
      if (info) return { ...info };
    }
    const raw = String(value).trim();
    if (!raw) return { text: '', aria: '' };
    const direct = rankLookup.get(raw.toUpperCase());
    if (direct) return { ...direct };
    return { text: raw.toUpperCase(), aria: raw.toLowerCase() };
  }

  function normalizeSuit(value) {
    if (value == null) return { name: '', glyph: '', aria: '' };
    const raw = String(value).trim();
    if (!raw) return { name: '', glyph: '', aria: '' };
    const lowered = raw.toLowerCase().replace(/\ufe0f/g, '');
    const direct = SUIT_ALIASES.get(lowered)
      || SUIT_ALIASES.get(lowered.slice(-1))
      || SUIT_ALIASES.get(lowered.charAt(0));
    const info = direct ? SUIT_INFO[direct] : null;
    return info ? { ...info } : { name: '', glyph: '', aria: '' };
  }

  function cardParts(card) {
    if (card == null) {
      return { rank: '', suitName: '', suitGlyph: '', label: '', aria: '' };
    }
    let raw = '';
    if (typeof card === 'string' || typeof card === 'number') {
      raw = String(card).trim();
    } else if (typeof card === 'object') {
      const rawFields = ['raw', 'code', 'card'];
      for (const key of rawFields) {
        if (typeof card[key] === 'string') {
          raw = card[key].trim();
          break;
        }
      }
    }

    let rankSrc = '';
    let suitSrc = '';
    if (typeof card === 'object' && card) {
      rankSrc = card.rank ?? card.r ?? card.value ?? card.face ?? '';
      suitSrc = card.suit ?? card.s ?? card.suit_key ?? card.suitName ?? '';
    }

    if (!rankSrc || !suitSrc) {
      const candidate = (raw || '').replace(/\s+/g, '');
      const match = candidate.match(/^(.+?)([cdhsCDHS♣♦♥♠])$/);
      if (match) {
        if (!rankSrc) rankSrc = match[1];
        if (!suitSrc) suitSrc = match[2];
      } else if (!rankSrc && candidate.length >= 2) {
        rankSrc = candidate.slice(0, -1);
        suitSrc = suitSrc || candidate.slice(-1);
      }
    }

    const fallbackRaw = raw ? raw.replace(/[^A-Za-z0-9]/g, '').toUpperCase() : '';
    const rankInfo = normalizeRank(rankSrc || fallbackRaw);
    const suitInfo = normalizeSuit(suitSrc);
    const rankText = rankInfo.text || (fallbackRaw ? fallbackRaw.slice(0, 2) : '');
    const glyph = suitInfo.glyph;
    const label = rankText ? (glyph ? `${rankText}${glyph}` : rankText) : (raw || fallbackRaw);
    const ariaParts = [];
    if (rankInfo.aria) {
      ariaParts.push(rankInfo.aria.charAt(0).toUpperCase() + rankInfo.aria.slice(1));
    }
    if (suitInfo.aria) {
      ariaParts.push(`of ${suitInfo.aria}`);
    }
    const aria = ariaParts.join(' ').trim();
    return {
      rank: rankText,
      suitName: suitInfo.name,
      suitGlyph: glyph,
      label: label || '',
      aria: aria || (label ? `Card ${label}` : ''),
    };
  }

  function setCards(el, arr) {
    if (!el) return;
    el.innerHTML = '';
    const list = Array.isArray(arr) ? arr : (arr != null ? [arr] : []);
    const slotCount = parseInt(el.dataset.cardSlots ?? el.getAttribute('data-card-slots') ?? '0', 10);
    const maxCards = Number.isFinite(slotCount) && slotCount > 0 ? Math.min(list.length, slotCount) : list.length;
    const cardsToRender = list.slice(0, maxCards);

    cardsToRender.forEach((entry, idx) => {
      const { rank, suitName, suitGlyph, label, aria } = cardParts(entry);
      const card = document.createElement('div');
      card.className = 'cardx deal';
      if (suitName) card.classList.add(suitName);
      if (label) card.setAttribute('title', label);
      if (aria) {
        card.setAttribute('role', 'img');
        card.setAttribute('aria-label', aria);
      }
      card.style.animationDelay = `${idx * 60}ms`;
      if (suitGlyph) {
        card.style.setProperty('--card-glyph', `\'${suitGlyph}\'`);
      } else {
        card.style.removeProperty('--card-glyph');
      }

      const back = document.createElement('div');
      back.className = 'face back';
      const front = document.createElement('div');
      front.className = 'face front';

      const top = document.createElement('div');
      top.className = 'num-box top suit';
      top.textContent = (rank || label || '').slice(0, 3);
      const bottom = document.createElement('div');
      bottom.className = 'num-box bottom suit';
      bottom.textContent = (rank || label || '').slice(0, 3);
      const center = document.createElement('div');
      center.className = 'suit main';

      front.appendChild(top);
      front.appendChild(bottom);
      front.appendChild(center);
      card.appendChild(back);
      card.appendChild(front);
      el.appendChild(card);
    });

    if (Number.isFinite(slotCount) && slotCount > 0) {
      for (let idx = cardsToRender.length; idx < slotCount; idx += 1) {
        const placeholder = document.createElement('div');
        placeholder.className = 'cardx cardx--placeholder';
        placeholder.dataset.placeholder = 'true';
        placeholder.setAttribute('aria-hidden', 'true');
        const shell = document.createElement('div');
        shell.className = 'cardx__placeholder';
        placeholder.appendChild(shell);
        el.appendChild(placeholder);
      }
    }
  }

  function applyFlip(el, reveal, delayStep = FLIP_DELAY) {
    if (!el) return;
    const cards = $$('.cardx', el);
    cards.forEach((card, idx) => {
      if (card.dataset && card.dataset.placeholder === 'true') {
        return;
      }
      if (card._flipTimer) {
        window.clearTimeout(card._flipTimer);
        card._flipTimer = null;
      }
      if (reveal) {
        card.classList.remove('flip');
        const delay = idx * delayStep;
        card._flipTimer = window.setTimeout(() => {
          card.classList.add('flip');
          card._flipTimer = null;
        }, delay);
      } else {
        card.classList.remove('flip');
      }
    });
  }

  function setRangeProgress(el) {
    if (!el) return;
    const min = Number(el.min ?? 0);
    const max = Number(el.max ?? 100);
    const value = Number(el.value ?? min);
    let pct = 0;
    if (Number.isFinite(min) && Number.isFinite(max) && max > min) {
      pct = (value - min) / (max - min);
    }
    pct = Math.max(0, Math.min(1, pct));
    el.style.setProperty('--progress', `${pct}`);
  }

  function formatNumber(value, precision) {
    const num = Number(value);
    if (!Number.isFinite(num)) return '0';
    const opts = {};
    if (typeof precision === 'number') {
      opts.minimumFractionDigits = precision;
      opts.maximumFractionDigits = precision;
    } else if (Math.abs(num % 1) > 1e-6) {
      opts.minimumFractionDigits = 1;
      opts.maximumFractionDigits = 1;
    }
    return num.toLocaleString(undefined, opts);
  }

  const fmtChips = (n) => formatNumber(n);

  function fmtSigned(n, zeroLabel = '±0', precision) {
    const num = Number(n);
    if (!Number.isFinite(num) || Math.abs(num) < 1e-6) {
      return zeroLabel;
    }
    const sign = num >= 0 ? '+' : '−';
    const text = formatNumber(Math.abs(num), precision);
    return `${sign}${text}`;
  }

  function formatPercent(value) {
    const num = Number(value);
    if (!Number.isFinite(num)) return null;
    return `${(num * 100).toFixed(1)}%`;
  }

  function computePotOddsValue(row) {
    const direct = Number(row?.pot_odds);
    if (Number.isFinite(direct) && direct >= 0) {
      return direct;
    }
    const toCall = Number(row?.to_call);
    const pot = Number(row?.pot);
    if (!Number.isFinite(toCall) || toCall <= 0) {
      return null;
    }
    const potSize = Number.isFinite(pot) && pot >= 0 ? pot : 0;
    const denom = potSize + toCall;
    if (denom <= 0) return null;
    return toCall / denom;
  }

  function computeRequiredEquityValue(row) {
    const direct = Number(row?.required_equity);
    if (Number.isFinite(direct) && direct >= 0) {
      return direct;
    }
    const odds = computePotOddsValue(row);
    return Number.isFinite(odds) ? odds : null;
  }

  function formatCardLabels(cards) {
    const arr = Array.isArray(cards) ? cards : [];
    if (!arr.length) return '';
    return arr.map((entry) => {
      const parts = cardParts(entry);
      if (parts.rank && parts.suitGlyph) return `${parts.rank}${parts.suitGlyph}`;
      return parts.label || parts.rank || '??';
    }).join(' ');
  }

  function isHandStart(idx) {
    if (!state.rows.length) return false;
    if (idx <= 0) return true;
    return state.rows[idx - 1]?.hand_id !== state.rows[idx]?.hand_id;
  }

  function isAOnSB(handId) {
    return /A$/i.test(String(handId || ''));
  }

  function seatForLabel(label, row) {
    if (!label) return null;
    const seatLabel = String(label).toUpperCase();
    const aIsSB = isAOnSB(row?.hand_id);
    if (seatLabel === 'A') return aIsSB ? 'SB' : 'BB';
    if (seatLabel === 'B') return aIsSB ? 'BB' : 'SB';
    if (seatLabel === 'SB' || seatLabel === 'BB') return seatLabel;
    return null;
  }

  function labelName(label) {
    if (!label) return '';
    const upper = String(label).toUpperCase();
    if (upper === 'A') return state.modelA || 'A';
    if (upper === 'B') return state.modelB || 'B';
    return label;
  }

  function describeActor(row) {
    if (!row) return '';
    const seat = seatForLabel(row.actor_label, row);
    const name = labelName(row.actor_label);
    if (!name) return '';
    return seat ? `${name} (${seat})` : name;
  }

  function fmtAction(row) {
    if (!row) return '—';
    const parts = [];
    const actor = describeActor(row);
    if (actor) parts.push(actor);
    if (row.action) parts.push(row.action);
    if (row.amount != null && row.amount !== '') {
      const word = row.action && row.action.toLowerCase() === 'call' ? 'for' : 'to';
      parts.push(`${word} ${fmtChips(row.amount)}`);
    }
    return parts.join(' ') || '—';
  }

  function shouldShowHole(seatKey) {
    const mode = state.holeMode;
    if (mode === 'both') return true;
    if (mode === 'sb') return seatKey === 'SB';
    if (mode === 'bb') return seatKey === 'BB';
    return false;
  }

  function computeEquity(row, seatKey) {
    const pot = Number(row?.pot ?? 0);
    const sbStack = Number(row?.sb_stack ?? 0);
    const bbStack = Number(row?.bb_stack ?? 0);
    const half = pot / 2;
    const sbEquity = sbStack + half;
    const bbEquity = bbStack + half;
    const total = sbEquity + bbEquity;
    const equity = seatKey === 'SB' ? sbEquity : bbEquity;
    const base = state.baseEquity[seatKey] ?? equity;
    const share = total > 0 ? Math.max(0, Math.min(1, equity / total)) : 0.5;
    const delta = equity - base;
    return { equity, share, delta };
  }

  function updateDeltaElement(el, value, { zeroLabel = '±0', precision } = {}) {
    if (!el) return;
    const num = Number(value);
    if (!Number.isFinite(num) || Math.abs(num) < 1e-6) {
      el.textContent = zeroLabel;
      el.classList.remove('positive', 'negative');
      return;
    }
    const positive = num >= 0;
    el.textContent = fmtSigned(num, zeroLabel, precision);
    el.classList.toggle('positive', positive);
    el.classList.toggle('negative', !positive);
  }

  function updateStatus(mode) {
    if (!els.status) return;
    const base = ['pill'];
    const isPlaying = mode === 'playing';
    const resolvedMode = mode === 'complete' ? 'complete' : (isPlaying ? 'playing' : 'paused');
    if (els.playBtn) {
      els.playBtn.textContent = isPlaying ? 'Playing' : 'Play';
      els.playBtn.setAttribute('aria-pressed', isPlaying ? 'true' : 'false');
    }
    if (els.pauseBtn) {
      const isPaused = resolvedMode !== 'playing';
      els.pauseBtn.textContent = isPaused ? 'Paused' : 'Pause';
      els.pauseBtn.setAttribute('aria-pressed', isPaused ? 'true' : 'false');
    }
    if (mode === 'playing') {
      els.status.textContent = 'Playing';
      els.status.className = base.concat('warn').join(' ');
    } else if (mode === 'complete') {
      els.status.textContent = 'Complete';
      els.status.className = base.concat('ok').join(' ');
    } else {
      els.status.textContent = 'Paused';
      els.status.className = base.concat('ghost').join(' ');
    }
  }

  function updateDealerIndicator() {
    const seat = state.dealerSeat;
    if (els.sbDealer) {
      const isDealer = seat === 'SB';
      els.sbDealer.hidden = !isDealer;
      els.sbDealer.setAttribute('aria-hidden', isDealer ? 'false' : 'true');
    }
    if (els.bbDealer) {
      const isDealer = seat === 'BB';
      els.bbDealer.hidden = !isDealer;
      els.bbDealer.setAttribute('aria-hidden', isDealer ? 'false' : 'true');
    }
    if (els.sbZone) {
      els.sbZone.classList.toggle('seat-card--dealer', seat === 'SB');
    }
    if (els.bbZone) {
      els.bbZone.classList.toggle('seat-card--dealer', seat === 'BB');
    }
  }

  function updateWinnerGlow() {
    if (els.sbZone) {
      els.sbZone.classList.toggle('seat-card--winner', state.winnerSeat === 'SB');
    }
    if (els.bbZone) {
      els.bbZone.classList.toggle('seat-card--winner', state.winnerSeat === 'BB');
    }
  }

  function updateSeatFocus(activeSeat) {
    if (els.sbZone) {
      els.sbZone.classList.toggle('seat-card--active', activeSeat === 'SB');
    }
    if (els.bbZone) {
      els.bbZone.classList.toggle('seat-card--active', activeSeat === 'BB');
    }
  }

  function updateSeat(row, seatKey) {
    const isSB = seatKey === 'SB';
    const zone = isSB ? els.sbZone : els.bbZone;
    const holeEl = isSB ? els.sbHole : els.bbHole;
    const holeTextEl = isSB ? els.sbHoleText : els.bbHoleText;
    const stackEl = isSB ? els.sbStack : els.bbStack;
    const deltaEl = isSB ? els.sbDelta : els.bbDelta;
    const evAmountEl = isSB ? els.sbEvAmount : els.bbEvAmount;
    const evShareEl = isSB ? els.sbEvValue : els.bbEvValue;
    const evDeltaEl = isSB ? els.sbEvDelta : els.bbEvDelta;
    const evMeter = isSB ? els.sbEvMeter : els.bbEvMeter;
    const evFill = isSB ? els.sbEvFill : els.bbEvFill;

    const cards = isSB ? row?.sb_hole : row?.bb_hole;
    const key = (Array.isArray(cards) ? cards : []).join(',');
    if (key !== state.prevHoles[seatKey]) {
      setCards(holeEl, cards);
      state.prevHoles[seatKey] = key;
    }
    const show = shouldShowHole(seatKey) && Array.isArray(cards) && cards.length > 0;
    applyFlip(holeEl, show);
    if (holeTextEl) {
      if (Array.isArray(cards) && cards.length) {
        holeTextEl.textContent = show ? formatCardLabels(cards) : 'Hidden';
      } else {
        holeTextEl.textContent = '—';
      }
    }

    const stack = Number(isSB ? row?.sb_stack : row?.bb_stack);
    if (stackEl) {
      stackEl.textContent = fmtChips(Number.isFinite(stack) ? stack : 0);
    }
    const base = state.startStacks[seatKey] ?? stack;
    updateDeltaElement(deltaEl, Number.isFinite(stack) ? stack - base : 0, { precision: 0 });

    const eq = computeEquity(row, seatKey);
    if (evAmountEl) {
      evAmountEl.textContent = fmtChips(eq.equity);
    }
    if (evShareEl) {
      evShareEl.textContent = `${(eq.share * 100).toFixed(1)}%`;
    }
    updateDeltaElement(evDeltaEl, eq.delta, { precision: 1 });
    if (evMeter) {
      evMeter.style.setProperty('--ev-pct', `${Math.max(0, Math.min(100, eq.share * 100))}%`);
    }
    if (evFill) {
      evFill.style.width = `${Math.max(0, Math.min(100, eq.share * 100))}%`;
    }
    if (zone) {
      zone.setAttribute('data-equity', eq.share.toFixed(3));
    }
  }

  function updateInsights(row) {
    const potOddsValue = computePotOddsValue(row);
    const potOdds = formatPercent(potOddsValue);
    const reqEqValue = computeRequiredEquityValue(row);
    const reqEq = formatPercent(reqEqValue);
    const toCall = Number(row?.to_call);
    const minRaise = Number(row?.min_raise_to);
    const maxRaise = Number(row?.max_raise_to);

    if (els.potOdds) {
      els.potOdds.textContent = potOdds ?? '—';
      els.potOdds.parentElement?.classList.toggle('is-empty', !potOdds);
    }
    if (els.requiredEq) {
      els.requiredEq.textContent = reqEq ?? '—';
      els.requiredEq.parentElement?.classList.toggle('is-empty', !reqEq);
    }
    if (els.toCall) {
      const text = Number.isFinite(toCall) ? fmtChips(toCall) : '—';
      els.toCall.textContent = text;
      els.toCall.parentElement?.classList.toggle('is-empty', !Number.isFinite(toCall));
    }
    if (els.minRaise) {
      const text = Number.isFinite(minRaise) ? fmtChips(minRaise) : '—';
      els.minRaise.textContent = text;
      els.minRaise.parentElement?.classList.toggle('is-empty', !Number.isFinite(minRaise));
    }
    if (els.raiseWindow) {
      let text = '—';
      if (Number.isFinite(minRaise) && Number.isFinite(maxRaise)) {
        text = `${fmtChips(minRaise)} – ${fmtChips(maxRaise)}`;
      } else if (Number.isFinite(minRaise)) {
        text = `≥ ${fmtChips(minRaise)}`;
      } else if (Number.isFinite(maxRaise)) {
        text = `≤ ${fmtChips(maxRaise)}`;
      }
      els.raiseWindow.textContent = text;
      const hasValue = Number.isFinite(minRaise) || Number.isFinite(maxRaise);
      els.raiseWindow.parentElement?.classList.toggle('is-empty', !hasValue);
    }

    if (els.solverText) {
      const solver = row?.solver;
      const solverVersion = row?.solver_version;
      const bestAction = row?.eval_best_action;
      const bestTo = row?.eval_best_to;
      const gap = Number(row?.eval_gap_bb);
      const prob = Number(row?.eval_correct_prob);
      const isTop = typeof row?.eval_is_top === 'boolean' ? row.eval_is_top : null;
      const parts = [];
      if (solver) {
        parts.push(solverVersion ? `${solver} ${solverVersion}` : solver);
      }
      if (bestAction) {
        const move = bestTo != null ? `${bestAction} to ${fmtChips(bestTo)}` : bestAction;
        parts.push(move);
      }
      if (Number.isFinite(gap)) {
        parts.push(`${gap.toFixed(2)} bb gap`);
      }
      if (Number.isFinite(prob)) {
        parts.push(`${Math.round(prob * 100)}% top action`);
      } else if (isTop != null) {
        parts.push(isTop ? 'Top action' : 'Off-tree');
      }
      const text = parts.join(' • ');
      els.solverText.textContent = text || '—';
      els.solverText.parentElement?.classList.toggle('is-empty', !text);
    }
  }

  function addPressFeedback(btn) {
    if (!btn) return;
    const clearPress = () => {
      btn.classList.remove('is-pressing');
    };
    btn.addEventListener('pointerdown', () => {
      btn.classList.add('is-pressing');
    });
    ['pointerup', 'pointerleave', 'pointercancel'].forEach(evt => {
      btn.addEventListener(evt, clearPress);
    });
    btn.addEventListener('blur', clearPress);
    btn.addEventListener('keydown', (ev) => {
      if (ev.key === ' ' || ev.key === 'Enter') {
        btn.classList.add('is-pressing');
      }
    });
    btn.addEventListener('keyup', clearPress);
    btn.addEventListener('click', () => {
      btn.classList.remove('did-press');
      void btn.offsetWidth;
      btn.classList.add('did-press');
      window.setTimeout(() => {
        btn.classList.remove('did-press');
      }, 280);
    });
  }

  function updateHandDisplay(row) {
    if (!row) {
      if (els.hand) els.hand.textContent = '—';
      if (els.caption) els.caption.textContent = 'Waiting for first hand…';
      if (els.log) els.log.textContent = '—';
      if (els.boardText) els.boardText.textContent = 'Board: —';
      if (els.turn) els.turn.textContent = 'Turn: —';
      updateSeatFocus(null);
      return;
    }

    const handLabel = row.hand_id ? `Hand ${row.hand_id}` : 'Hand';
    const street = row.street || '—';
    if (els.hand) {
      els.hand.textContent = `${handLabel} — ${street}`;
    }

    const actionText = fmtAction(row);
    if (els.caption) {
      els.caption.textContent = actionText;
    }
    if (els.log) {
      els.log.textContent = actionText;
    }

    const boardArr = Array.isArray(row.board) ? row.board : [];
    const boardKey = boardArr.join(',');
    if (boardKey !== state.prevBoardKey) {
      state.prevBoardKey = boardKey;
      setCards(els.board, boardArr);
      applyFlip(els.board, true, 120);
    }
    if (els.boardText) {
      const text = boardArr.length ? formatCardLabels(boardArr) : '—';
      els.boardText.textContent = `Board: ${text || '—'}`;
    }

    const aIsSB = isAOnSB(row.hand_id);
    const sbName = aIsSB ? state.modelA : state.modelB;
    const bbName = aIsSB ? state.modelB : state.modelA;
    if (els.sbName) els.sbName.textContent = sbName || 'SB';
    if (els.bbName) els.bbName.textContent = bbName || 'BB';

    if (els.turn) {
      const actor = describeActor(row);
      els.turn.textContent = actor ? `Turn: ${actor}` : 'Turn: —';
      els.turn.className = 'pill ghost';
    }

    updateSeat(row, 'SB');
    updateSeat(row, 'BB');

    updateInsights(row);

    const actorSeat = seatForLabel(row.actor_label, row);
    updateSeatFocus(actorSeat);
  }

  function updateHandState(row) {
    if (!row) return;
    if (state.prevHandId !== row.hand_id) {
      state.prevHandId = row.hand_id;
      state.winnerSeat = null;
      updateWinnerGlow();
      const nextDealer = isAOnSB(row.hand_id) ? 'SB' : 'BB';
      if (nextDealer === 'SB' || nextDealer === 'BB') {
        state.dealerSeat = nextDealer;
      }
      updateDealerIndicator();
      if (els.handBanner) {
        els.handBanner.textContent = '';
      }
    }

    const endOfHand = state.index === state.rows.length - 1 || state.rows[state.index + 1]?.hand_id !== row.hand_id;
    if (endOfHand) {
      if (!state.winnerSeat && row.winner_seat) {
        const seat = String(row.winner_seat).toUpperCase();
        if (seat === 'SB' || seat === 'BB') {
          state.winnerSeat = seat;
        }
      }
      if (!state.winnerSeat && (row.action || '').toLowerCase() === 'fold') {
        const winnerLabel = row.actor_label === 'A' ? 'B' : row.actor_label === 'B' ? 'A' : null;
        const seat = seatForLabel(winnerLabel, row);
        if (seat) state.winnerSeat = seat;
      }
      updateWinnerGlow();
      if (els.handBanner) {
        if (state.index === state.rows.length - 1) {
          els.handBanner.textContent = 'Match complete.';
        } else {
          let message = 'Hand complete.';
          if (state.winnerSeat) {
            const seat = state.winnerSeat;
            const winnerLabel = seat === 'SB' ? (isAOnSB(row.hand_id) ? 'A' : 'B') : (isAOnSB(row.hand_id) ? 'B' : 'A');
            const winnerName = labelName(winnerLabel);
            message = `${winnerName} wins — ${seat}`;
          }
          els.handBanner.textContent = message;
        }
      }
    } else if (els.handBanner) {
      els.handBanner.textContent = '';
    }
  }

  function updateTimeline() {
    if (!els.timeline) return;
    const max = Math.max(0, state.rows.length - 1);
    els.timeline.max = String(max);
    els.timeline.value = String(Math.min(state.index, max));
    els.timeline.disabled = state.rows.length <= 1;
    setRangeProgress(els.timeline);
    if (els.count) {
      const current = state.rows.length ? state.index + 1 : 0;
      els.count.textContent = `${current}/${state.rows.length}`;
    }
  }

  function updateActionHighlight() {
    state.actionButtons.forEach(btn => {
      if (!btn) return;
      const idx = Number(btn.dataset.index);
      const isActive = idx === state.index;
      if (isActive) {
        btn.classList.add('is-active');
        btn.setAttribute('aria-current', 'true');
        const behavior = state.playing ? 'smooth' : 'instant';
        btn.scrollIntoView({ block: 'nearest', inline: 'nearest', behavior: behavior === 'instant' ? 'auto' : behavior });
      } else {
        btn.classList.remove('is-active');
        btn.removeAttribute('aria-current');
      }
    });
  }

  function draw() {
    if (!state.rows.length) {
      updateHandDisplay(null);
      updateTimeline();
      updateActionHighlight();
      return;
    }
    state.index = Math.max(0, Math.min(state.index, state.rows.length - 1));
    const row = state.rows[state.index];
    updateHandDisplay(row);
    updateHandState(row);
    if (els.pot) {
      els.pot.textContent = fmtChips(row?.pot ?? 0);
    }
    updateTimeline();
    updateActionHighlight();

    if (state.playing && state.index === state.rows.length - 1) {
      finishPlayback();
    }
  }

  function step(delta = 1) {
    if (!state.rows.length) return;
    state.index = Math.max(0, Math.min(state.index + delta, state.rows.length - 1));
    draw();
  }

  function scheduleNext() {
    window.clearTimeout(state.timer);
    state.timer = null;
    if (!state.playing) return;
    const delay = Math.round(state.speed * (isHandStart(state.index) ? INTRO_HOLD_FACTOR : 1));
    state.timer = window.setTimeout(() => {
      state.index = Math.min(state.index + 1, state.rows.length - 1);
      draw();
      if (state.playing && state.index < state.rows.length - 1) {
        scheduleNext();
      }
    }, delay);
  }

  function play() {
    if (!state.rows.length) return;
    if (state.index >= state.rows.length - 1) {
      state.index = 0;
      draw();
    }
    if (state.playing) return;
    state.playing = true;
    updateStatus('playing');
    scheduleNext();
  }

  function finishPlayback() {
    state.playing = false;
    window.clearTimeout(state.timer);
    state.timer = null;
    updateStatus('complete');
  }

  function pause() {
    state.playing = false;
    window.clearTimeout(state.timer);
    state.timer = null;
    updateStatus('paused');
  }

  async function getJSON(url, fallback) {
    try {
      const res = await fetch(url, { cache: 'no-store' });
      if (res.ok) return await res.json();
    } catch (err) {
      console.warn('Fetch failed', err);
    }
    if (fallback) {
      try {
        const res = await fetch(fallback, { cache: 'no-store' });
        if (res.ok) return await res.json();
      } catch (err) {
        console.warn('Fallback fetch failed', err);
      }
    }
    return null;
  }

  function dateShort(iso) {
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return '';
    return d.toLocaleString([], { year: 'numeric', month: 'short', day: '2-digit', hour: '2-digit', minute: '2-digit' });
  }

  function trimName(name) {
    if (!name) return '';
    return name.length > 36 ? `${name.slice(0, 33)}…` : name;
  }

  function updatePlayerMap() {
    if (!els.map) return;
    const frag = document.createDocumentFragment();
    const pillA = document.createElement('span');
    pillA.className = 'pill ghost';
    pillA.textContent = `A • ${trimName(state.modelA || 'Player A')}`;
    frag.appendChild(pillA);
    const pillB = document.createElement('span');
    pillB.className = 'pill ghost';
    pillB.textContent = `B • ${trimName(state.modelB || 'Player B')}`;
    frag.appendChild(pillB);
    els.map.innerHTML = '';
    els.map.appendChild(frag);
  }

  function updateMatchMeta() {
    if (!els.matchMeta) return;
    const row = state.currentMatch;
    if (!row) {
      els.matchMeta.textContent = 'No matches recorded yet. Run a duel to start a replay.';
      return;
    }
    const parts = [];
    if (Number.isFinite(row.sb) && Number.isFinite(row.bb)) {
      parts.push(`Blinds ${fmtChips(row.sb)}/${fmtChips(row.bb)}`);
    }
    if (Number.isFinite(row.start_stack)) {
      parts.push(`Start stack ${fmtChips(row.start_stack)}`);
    }
    if (Number.isFinite(row.duel_seeds)) {
      parts.push(`${row.duel_seeds} seeds`);
    }
    if (row.created_at) {
      parts.push(`Played ${dateShort(row.created_at)}`);
    }
    if (row.seedpack_name) {
      const version = row.seedpack_version ? ` ${row.seedpack_version}` : '';
      parts.push(`Seedpack ${row.seedpack_name}${version}`);
    }
    els.matchMeta.textContent = parts.join(' • ');
  }

  function buildMatchOptions() {
    if (!els.matchSelect) return;
    const frag = document.createDocumentFragment();
    state.matchList.slice(0, 60).forEach(row => {
      const opt = document.createElement('option');
      opt.value = String(row.id);
      const when = row.created_at ? dateShort(row.created_at) : '';
      opt.textContent = `#${row.id}${when ? ` • ${when}` : ''}`;
      frag.appendChild(opt);
    });
    els.matchSelect.innerHTML = '';
    els.matchSelect.appendChild(frag);
    if (state.matchId) {
      els.matchSelect.value = String(state.matchId);
    }
  }

  function resolveCurrentMatch() {
    const current = state.matchList.find(row => String(row.id) === String(state.matchId)) || state.matchList[0] || null;
    state.currentMatch = current;
    if (current) {
      state.matchId = current.id;
      state.modelA = current.model_a || 'A';
      state.modelB = current.model_b || 'B';
      try {
        localStorage.setItem(`replay_models_${state.matchId}`, JSON.stringify({ A: state.modelA, B: state.modelB }));
      } catch (err) {
        console.warn('Unable to store replay model cache', err);
      }
    } else if (state.matchId) {
      try {
        const cached = localStorage.getItem(`replay_models_${state.matchId}`);
        if (cached) {
          const parsed = JSON.parse(cached);
          if (parsed?.A && parsed?.B) {
            state.modelA = parsed.A;
            state.modelB = parsed.B;
          }
        }
      } catch (err) {
        console.warn('Unable to read replay model cache', err);
      }
    }
    updatePlayerMap();
    updateMatchMeta();
  }

  async function populateMatches({ refresh = false } = {}) {
    if (!refresh && state.matchList.length) {
      resolveCurrentMatch();
      buildMatchOptions();
      return state.currentMatch;
    }
    const data = await getJSON('/api/matches', '/web/data/matches.json');
    state.matchList = Array.isArray(data?.rows) ? data.rows : [];
    if (!state.matchId && state.matchList[0]) {
      state.matchId = state.matchList[0].id;
    }
    buildMatchOptions();
    resolveCurrentMatch();
    return state.currentMatch;
  }

  function renderEmptyActionList(message) {
    if (!els.actionList) return;
    els.actionList.innerHTML = '';
    const empty = document.createElement('div');
    empty.className = 'action-list__empty';
    empty.textContent = message;
    els.actionList.appendChild(empty);
    state.actionButtons = [];
  }

  function buildActionButton(row, idx) {
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'action-list__item';
    btn.dataset.index = String(idx);
    btn.setAttribute('role', 'listitem');
    if (isHandStart(idx)) btn.classList.add('is-hand-start');

    const step = document.createElement('span');
    step.className = 'action-list__step mono';
    step.textContent = `#${idx + 1}`;

    const hand = document.createElement('span');
    hand.className = 'action-list__hand mono';
    hand.textContent = row.hand_id || '—';

    const street = document.createElement('span');
    street.className = 'action-list__street';
    street.textContent = row.street || '—';

    const action = document.createElement('span');
    action.className = 'action-list__text';
    action.textContent = fmtAction(row);

    const pot = document.createElement('span');
    pot.className = 'action-list__pot mono';
    pot.textContent = fmtChips(row.pot ?? 0);

    btn.append(step, hand, street, action, pot);
    return btn;
  }

  function buildActionList() {
    if (!els.actionList) return;
    if (!state.rows.length) {
      renderEmptyActionList('No action logs available for this match yet.');
      return;
    }
    const frag = document.createDocumentFragment();
    state.actionButtons = [];
    state.rows.forEach((row, idx) => {
      const btn = buildActionButton(row, idx);
      frag.appendChild(btn);
      state.actionButtons.push(btn);
    });
    els.actionList.innerHTML = '';
    els.actionList.appendChild(frag);
  }

  function resetStateForMatch() {
    state.playing = false;
    window.clearTimeout(state.timer);
    state.timer = null;
    state.rows = [];
    state.index = 0;
    state.prevBoardKey = '';
    state.prevHoles = { SB: '', BB: '' };
    state.prevHandId = null;
    state.winnerSeat = null;
    state.dealerSeat = 'SB';
    state.actionButtons = [];
    updateDealerIndicator();
    updateWinnerGlow();
    renderEmptyActionList('Loading actions…');
    setCards(els.board, []);
    setCards(els.sbHole, []);
    setCards(els.bbHole, []);
    updateHandDisplay(null);
    updateTimeline();
    updateStatus('paused');
  }

  async function loadMatchLogs() {
    if (!state.matchId) {
      renderEmptyActionList('Select a match to view its action log.');
      return;
    }
    const requestedMatchId = state.matchId;
    resetStateForMatch();
    const data = await getJSON(`/api/match-logs?match_id=${encodeURIComponent(requestedMatchId)}`, `/web/data/match-logs-${encodeURIComponent(requestedMatchId)}.json`);
    if (state.matchId !== requestedMatchId) {
      return;
    }
    state.rows = Array.isArray(data?.rows) ? data.rows : [];
    if (!state.rows.length) {
      renderEmptyActionList('No actions recorded for this match yet.');
      draw();
      return;
    }
    const first = state.rows[0];
    state.startStacks.SB = Number(first?.sb_stack ?? 0);
    state.startStacks.BB = Number(first?.bb_stack ?? 0);
    const startPot = Number(first?.pot ?? 0) / 2;
    state.baseEquity.SB = state.startStacks.SB + startPot;
    state.baseEquity.BB = state.startStacks.BB + startPot;
    buildActionList();
    draw();
    play();
  }

  function cacheElements() {
    els.matchSelect = $('#matchSel');
    els.map = $('#map');
    els.matchMeta = $('#matchMeta');
    els.board = $('#board');
    els.boardText = $('#boardText');
    els.sbZone = $('#sbZone');
    els.bbZone = $('#bbZone');
    els.sbHole = $('#sb_hole');
    els.bbHole = $('#bb_hole');
    els.sbHoleText = $('#sbHoleText');
    els.bbHoleText = $('#bbHoleText');
    els.sbStack = $('#sb_stack_tag');
    els.bbStack = $('#bb_stack_tag');
    els.sbDelta = $('#sb_delta');
    els.bbDelta = $('#bb_delta');
    els.sbName = $('#sbName');
    els.bbName = $('#bbName');
    els.sbDealer = $('#sbDealer');
    els.bbDealer = $('#bbDealer');
    els.sbEvAmount = $('#sb_ev_amount');
    els.bbEvAmount = $('#bb_ev_amount');
    els.sbEvValue = $('#sb_ev_value');
    els.bbEvValue = $('#bb_ev_value');
    els.sbEvDelta = $('#sb_ev_delta');
    els.bbEvDelta = $('#bb_ev_delta');
    els.sbEvMeter = $('#sb_ev_meter');
    els.bbEvMeter = $('#bb_ev_meter');
    els.sbEvFill = $('#sb_ev_meter .ev-meter__fill');
    els.bbEvFill = $('#bb_ev_meter .ev-meter__fill');
    els.status = $('#status');
    els.turn = $('#turn');
    els.caption = $('#caption');
    els.log = $('#log');
    els.hand = $('#hand');
    els.handBanner = $('#handBanner');
    els.pot = $('#pot');
    els.timeline = $('#timeline');
    els.count = $('#count');
    els.speedSlider = $('#speedSlider');
    els.speedValue = $('#speedValue');
    els.holesSelect = $('#holesSelect');
    els.playBtn = $('#play');
    els.pauseBtn = $('#pause');
    els.nextBtn = $('#next');
    els.actionList = $('#actionList');
    els.potOdds = $('#potOdds');
    els.requiredEq = $('#requiredEq');
    els.toCall = $('#toCall');
    els.minRaise = $('#minRaise');
    els.raiseWindow = $('#raiseWindow');
    els.solverText = $('#solverText');
  }

  function attachListeners() {
    [els.playBtn, els.pauseBtn, els.nextBtn].forEach(addPressFeedback);

    if (els.playBtn) {
      els.playBtn.addEventListener('click', () => {
        play();
      });
    }
    if (els.pauseBtn) {
      els.pauseBtn.addEventListener('click', () => {
        pause();
      });
    }
    if (els.nextBtn) {
      els.nextBtn.addEventListener('click', () => {
        pause();
        step(+1);
      });
    }
    if (els.speedSlider) {
      const updateSpeed = (value) => {
        const v = Math.max(1, parseInt(value || '1', 10) || 1);
        state.speed = SPEED_BASE / v;
        if (els.speedValue) {
          els.speedValue.textContent = `${v}×`;
        }
        setRangeProgress(els.speedSlider);
        if (state.playing) {
          scheduleNext();
        }
      };
      updateSpeed(els.speedSlider.value);
      els.speedSlider.addEventListener('input', (ev) => {
        updateSpeed(ev.target.value);
        setRangeProgress(ev.target);
      });
    }
    if (els.holesSelect) {
      state.holeMode = els.holesSelect.value || 'both';
      els.holesSelect.addEventListener('change', (ev) => {
        state.holeMode = ev.target.value || 'both';
        draw();
      });
    }
    if (els.timeline) {
      els.timeline.addEventListener('input', (ev) => {
        const value = parseInt(ev.target.value || '0', 10) || 0;
        pause();
        state.index = value;
        draw();
        setRangeProgress(ev.target);
      });
    }
    if (els.matchSelect) {
      els.matchSelect.addEventListener('change', (ev) => {
        state.matchId = ev.target.value;
        try {
          history.replaceState(null, '', `?match_id=${state.matchId}`);
        } catch (err) {
          console.warn('Unable to update history', err);
        }
        resolveCurrentMatch();
        loadMatchLogs();
      });
    }
    if (els.actionList) {
      els.actionList.addEventListener('click', (ev) => {
        const btn = ev.target.closest('.action-list__item');
        if (!btn) return;
        const idx = parseInt(btn.dataset.index || '0', 10);
        if (Number.isInteger(idx)) {
          pause();
          state.index = Math.max(0, Math.min(idx, state.rows.length - 1));
          draw();
        }
      });
    }

    window.addEventListener('keydown', (ev) => {
      const target = ev.target;
      if (target && ['INPUT', 'SELECT', 'TEXTAREA'].includes(target.tagName)) return;
      if (ev.key === 'ArrowRight') {
        ev.preventDefault();
        pause();
        step(+1);
      } else if (ev.key === 'ArrowLeft') {
        ev.preventDefault();
        pause();
        step(-1);
      } else if (ev.code === 'Space') {
        ev.preventDefault();
        if (state.playing) {
          pause();
        } else {
          play();
        }
      }
    });
  }

  async function init() {
    cacheElements();
    attachListeners();
    updateDealerIndicator();
    updateStatus('paused');
    await populateMatches();
    await loadMatchLogs();
  }

  return { init };
})();

if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', () => ReplayPage.init(), { once: true });
} else {
  ReplayPage.init();
}

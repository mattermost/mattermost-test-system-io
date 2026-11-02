(function () {
  var main = document.getElementById('tsio-page-main');
  if (!main) return;

  var PAGE_SIZE = 100;
  var SEARCH_DEBOUNCE_MS = 500;
  var MIN_SEARCH = 2;

  var identity = {
    repository: main.getAttribute('data-orch-repository') || '',
    commit_sha: main.getAttribute('data-orch-commit') || '',
    gh_run_id: main.getAttribute('data-orch-gh-run-id') || '',
    name: main.getAttribute('data-orch-name') || '',
    gh_run_attempt: main.getAttribute('data-orch-gh-run-attempt') || '1',
  };
  var isLive = main.getAttribute('data-orch-live') === '1';
  var unitPage = main.getAttribute('data-orch-page') || '1';

  var statusFilter = 'all';
  var searchQuery = '';
  var effectiveSearch = '';
  var searchTimer = null;
  var currentPage = 1;

  var statusBtn = document.getElementById('connection-status');
  var statusDot = document.getElementById('connection-dot');
  var statusPing = document.getElementById('connection-ping');
  var ws = null;
  var reconnectTimer = null;
  var lastHTML = '';
  var refreshInFlight = false;
  var refreshQueued = false;
  var forceQueued = false;

  var lightbox = {
    el: document.getElementById('run-lightbox'),
    img: document.getElementById('run-lightbox-img'),
    count: document.getElementById('run-lightbox-count'),
    keys: [],
    index: 0,
  };

  function dispatchInner() {
    return document.getElementById('run-dispatch-inner');
  }
  function unitList() {
    return document.getElementById('run-unit-list');
  }
  function allUnits() {
    var list = unitList();
    return list ? Array.prototype.slice.call(list.querySelectorAll(':scope > .run-unit')) : [];
  }

  function setStatus(state) {
    if (!statusBtn || !statusDot) return;
    statusBtn.classList.remove('connected', 'disconnected');
    if (state === 'connected') {
      statusBtn.classList.add('connected');
      statusBtn.title = 'Live updates active';
      statusBtn.setAttribute('aria-label', 'Live updates active');
      if (statusPing) statusPing.classList.remove('hidden');
    } else if (state === 'disconnected') {
      statusBtn.classList.add('disconnected');
      statusBtn.title = 'Disconnected - click to reconnect';
      statusBtn.setAttribute('aria-label', 'Disconnected - click to reconnect');
      if (statusPing) statusPing.classList.add('hidden');
    } else {
      statusBtn.title = 'Connecting to server...';
      statusBtn.setAttribute('aria-label', 'Connecting to server...');
      if (statusPing) statusPing.classList.add('hidden');
    }
  }

  function liveFragmentURL() {
    var params = new URLSearchParams();
    params.set('repository', identity.repository);
    params.set('commit_sha', identity.commit_sha);
    params.set('gh_run_id', identity.gh_run_id);
    params.set('name', identity.name);
    params.set('gh_run_attempt', identity.gh_run_attempt);
    if (unitPage && unitPage !== '1') params.set('page', unitPage);
    return '/reports/fragment/run-dispatch-live?' + params.toString();
  }

  function matchesFilter(el) {
    var attrs = (el.getAttribute('data-filter') || '').split(/\s+/);
    if (statusFilter !== 'all' && attrs.indexOf(statusFilter) === -1) return false;
    if (effectiveSearch) {
      var hay = el.getAttribute('data-search') || '';
      if (hay.indexOf(effectiveSearch) === -1) return false;
    }
    return true;
  }

  function updateFilterButtons() {
    var root = dispatchInner();
    if (!root) return;
    var buttons = root.querySelectorAll('.run-filter-btn');
    for (var i = 0; i < buttons.length; i++) {
      var btn = buttons[i];
      var f = btn.getAttribute('data-filter') || 'all';
      if (f === statusFilter) btn.classList.add('is-active');
      else btn.classList.remove('is-active');
    }
  }

  function renderPagers(matchedCount, totalPages) {
    var tops = [document.getElementById('run-pager-top'), document.getElementById('run-pager-bottom')];
    for (var i = 0; i < tops.length; i++) {
      var el = tops[i];
      if (!el) continue;
      if (totalPages <= 1) {
        el.classList.add('hidden');
        el.innerHTML = '';
        continue;
      }
      el.classList.remove('hidden');
      var start = (currentPage - 1) * PAGE_SIZE + 1;
      var end = Math.min(currentPage * PAGE_SIZE, matchedCount);
      el.innerHTML =
        '<span>Showing ' +
        start +
        ' to ' +
        end +
        ' of ' +
        matchedCount +
        ' specs</span>' +
        '<span class="pager-actions">' +
        '<button type="button" class="btn-page" data-page="' +
        (currentPage - 1) +
        '"' +
        (currentPage <= 1 ? ' disabled' : '') +
        '>Previous</button>' +
        '<button type="button" class="btn-page" data-page="' +
        (currentPage + 1) +
        '"' +
        (currentPage >= totalPages ? ' disabled' : '') +
        '>Next</button>' +
        '</span>';
    }
  }

  function applyListView() {
    var units = allUnits();
    var matched = [];
    for (var i = 0; i < units.length; i++) {
      if (matchesFilter(units[i])) matched.push(units[i]);
      else {
        units[i].classList.add('is-hidden');
        units[i].classList.remove('is-page-visible');
      }
    }
    var totalPages = Math.max(1, Math.ceil(matched.length / PAGE_SIZE));
    if (currentPage > totalPages) currentPage = totalPages;
    if (currentPage < 1) currentPage = 1;
    var start = (currentPage - 1) * PAGE_SIZE;
    var end = start + PAGE_SIZE;
    for (var j = 0; j < matched.length; j++) {
      if (j >= start && j < end) {
        matched[j].classList.remove('is-hidden');
        matched[j].classList.add('is-page-visible');
      } else {
        matched[j].classList.add('is-hidden');
        matched[j].classList.remove('is-page-visible');
      }
    }
    var empty = document.getElementById('run-filter-empty');
    if (empty) {
      if (matched.length === 0 && units.length > 0) empty.classList.remove('hidden');
      else empty.classList.add('hidden');
    }
    updateFilterButtons();
    renderPagers(matched.length, totalPages);
  }

  function setFilter(next) {
    if (statusFilter === next && next !== 'all') statusFilter = 'all';
    else statusFilter = next;
    currentPage = 1;
    applyListView();
  }

  function toggleUnit(unit) {
    if (!unit || unit.getAttribute('data-can-expand') !== '1') return;
    unit.classList.toggle('is-open');
    var detail = unit.querySelector(':scope > .run-unit-detail');
    if (detail) detail.classList.toggle('hidden', !unit.classList.contains('is-open'));
  }

  function toggleTest(test) {
    if (!test || test.getAttribute('data-can-expand') !== '1') return;
    test.classList.toggle('is-open');
    var body = test.querySelector(':scope > .run-test-body');
    if (body) body.classList.toggle('hidden', !test.classList.contains('is-open'));
  }

  function openLightbox(keys, index) {
    if (!lightbox.el || !keys || !keys.length) return;
    lightbox.keys = keys;
    lightbox.index = index || 0;
    showLightboxImage();
    lightbox.el.hidden = false;
    lightbox.el.classList.remove('hidden');
    document.body.style.overflow = 'hidden';
  }

  function closeLightbox() {
    if (!lightbox.el) return;
    lightbox.el.hidden = true;
    lightbox.el.classList.add('hidden');
    document.body.style.overflow = '';
  }

  function showLightboxImage() {
    if (!lightbox.img) return;
    var key = lightbox.keys[lightbox.index];
    lightbox.img.src = '/files/' + key;
    if (lightbox.count) {
      lightbox.count.textContent = lightbox.index + 1 + ' / ' + lightbox.keys.length;
    }
    var prev = lightbox.el.querySelector('.run-lightbox-prev');
    var next = lightbox.el.querySelector('.run-lightbox-next');
    if (prev) prev.style.display = lightbox.keys.length > 1 ? '' : 'none';
    if (next) next.style.display = lightbox.keys.length > 1 ? '' : 'none';
  }

  function bindDispatchClicks(root) {
    if (!root) return;
    root.addEventListener('click', function (ev) {
      var t = ev.target;
      if (!(t instanceof Element)) return;

      var filterBtn = t.closest('.run-filter-btn');
      if (filterBtn && root.contains(filterBtn)) {
        ev.preventDefault();
        setFilter(filterBtn.getAttribute('data-filter') || 'all');
        return;
      }

      var pagerBtn = t.closest('.btn-page[data-page]');
      if (pagerBtn) {
        ev.preventDefault();
        if (pagerBtn.disabled) return;
        var p = parseInt(pagerBtn.getAttribute('data-page'), 10);
        if (!isNaN(p) && p >= 1) {
          currentPage = p;
          applyListView();
        }
        return;
      }

      var shot = t.closest('.run-shot-thumb');
      if (shot) {
        ev.preventDefault();
        ev.stopPropagation();
        var gallery = shot.closest('.run-shots');
        var keys = [];
        var idx = 0;
        if (gallery) {
          var thumbs = gallery.querySelectorAll('.run-shot-thumb');
          for (var i = 0; i < thumbs.length; i++) {
            keys.push(thumbs[i].getAttribute('data-shot-key'));
            if (thumbs[i] === shot) idx = i;
          }
        } else {
          keys = [shot.getAttribute('data-shot-key')];
        }
        openLightbox(keys, idx);
        return;
      }

      var testHead = t.closest('.run-test-head.is-expandable');
      if (testHead) {
        ev.preventDefault();
        ev.stopPropagation();
        toggleTest(testHead.closest('.run-test'));
        return;
      }

      var unitRow = t.closest('.run-unit-row.is-expandable');
      if (unitRow) {
        ev.preventDefault();
        toggleUnit(unitRow.closest('.run-unit'));
      }
    });

    root.addEventListener('keydown', function (ev) {
      if (ev.key !== 'Enter' && ev.key !== ' ') return;
      var t = ev.target;
      if (!(t instanceof Element)) return;
      if (t.classList.contains('run-unit-row') && t.classList.contains('is-expandable')) {
        ev.preventDefault();
        toggleUnit(t.closest('.run-unit'));
      } else if (t.classList.contains('run-test-head') && t.classList.contains('is-expandable')) {
        ev.preventDefault();
        toggleTest(t.closest('.run-test'));
      }
    });
  }

  function bindSearch() {
    var input = document.getElementById('run-spec-search');
    var clear = document.getElementById('run-spec-search-clear');
    if (!input) return;
    input.addEventListener('input', function () {
      searchQuery = input.value;
      if (clear) {
        if (searchQuery) clear.classList.remove('hidden');
        else clear.classList.add('hidden');
      }
      clearTimeout(searchTimer);
      searchTimer = setTimeout(function () {
        var trimmed = searchQuery.trim().toLowerCase();
        effectiveSearch = trimmed.length === 0 || trimmed.length >= MIN_SEARCH ? trimmed : '';
        currentPage = 1;
        applyListView();
      }, SEARCH_DEBOUNCE_MS);
    });
    if (clear) {
      clear.addEventListener('click', function () {
        input.value = '';
        searchQuery = '';
        effectiveSearch = '';
        clear.classList.add('hidden');
        currentPage = 1;
        applyListView();
        input.focus();
      });
    }
  }

  function initListUI() {
    var dispatch = document.getElementById('run-dispatch');
    if (dispatch) {
      var ps = parseInt(dispatch.getAttribute('data-page-size'), 10);
      if (!isNaN(ps) && ps > 0) PAGE_SIZE = ps;
    }
    bindDispatchClicks(document.getElementById('run-dispatch'));
    bindSearch();
    applyListView();
  }

  function refreshUnits(opts) {
    opts = opts || {};
    if (!identity.gh_run_id) return;
    if (refreshInFlight) {
      refreshQueued = true;
      forceQueued = forceQueued || !!opts.forceReload;
      return;
    }
    refreshInFlight = true;
    fetch(liveFragmentURL(), { credentials: 'same-origin' })
      .then(function (res) {
        if (!res.ok) throw new Error('fragment failed');
        return res.json();
      })
      .then(function (data) {
        if (data.terminal || opts.forceReload) {
          location.reload();
          return;
        }
        var html = data.html || '';
        var inner = dispatchInner();
        if (!inner) {
          if ((data.count || 0) > 0) location.reload();
          return;
        }
        if (html === lastHTML) return;
        inner.innerHTML = html;
        lastHTML = html;
        bindSearch();
        applyListView();
      })
      .catch(function () {})
      .finally(function () {
        refreshInFlight = false;
        if (refreshQueued) {
          // Rerun with any force flag from calls made while in flight.
          var next = { forceReload: forceQueued };
          refreshQueued = false;
          forceQueued = false;
          refreshUnits(next);
        }
      });
  }

  function seedSnapshot() {
    var inner = dispatchInner();
    if (inner) lastHTML = inner.innerHTML;
  }

  function shouldRefresh(type) {
    if (!type) return false;
    if (type.indexOf('orchestration.') === 0) return true;
    if (type === 'home.live.changed') return true;
    return false;
  }

  function isTerminal(msg) {
    if (!msg || !msg.type) return false;
    if (msg.type === 'orchestration.run.completed' || msg.type === 'orchestration.run.timed_out') {
      return true;
    }
    if (msg.type === 'home.live.changed') {
      return !!(msg.payload && msg.payload.terminal);
    }
    return false;
  }

  function connect() {
    if (!statusBtn || !statusDot) return;
    if (reconnectTimer) {
      clearTimeout(reconnectTimer);
      reconnectTimer = null;
    }
    if (ws) {
      // Detach handlers so the old socket can't reconnect or refresh.
      ws.onopen = ws.onmessage = ws.onclose = ws.onerror = null;
      try {
        ws.close();
      } catch (_) {}
      ws = null;
    }
    setStatus('connecting');
    var proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    ws = new WebSocket(proto + '//' + location.host + '/api/v1/ws');
    ws.onopen = function () {
      setStatus('connected');
      if (identity.gh_run_id && identity.repository && identity.commit_sha && identity.name) {
        ws.send(JSON.stringify({ type: 'subscribe.orchestration', identity: identity }));
      }
      if (isLive) refreshUnits();
    };
    ws.onmessage = function (ev) {
      try {
        var msg = JSON.parse(ev.data);
        if (!shouldRefresh(msg.type)) return;
        refreshUnits({ forceReload: isTerminal(msg) });
      } catch (_) {}
    };
    ws.onclose = function () {
      setStatus('disconnected');
      ws = null;
      if (!reconnectTimer) {
        reconnectTimer = setTimeout(function () {
          reconnectTimer = null;
          connect();
        }, 5000);
      }
    };
    ws.onerror = function () {
      setStatus('disconnected');
    };
  }

  if (lightbox.el) {
    lightbox.el.querySelector('.run-lightbox-close').addEventListener('click', closeLightbox);
    lightbox.el.querySelector('.run-lightbox-prev').addEventListener('click', function () {
      lightbox.index = (lightbox.index - 1 + lightbox.keys.length) % lightbox.keys.length;
      showLightboxImage();
    });
    lightbox.el.querySelector('.run-lightbox-next').addEventListener('click', function () {
      lightbox.index = (lightbox.index + 1) % lightbox.keys.length;
      showLightboxImage();
    });
    lightbox.el.addEventListener('click', function (ev) {
      if (ev.target === lightbox.el) closeLightbox();
    });
    document.addEventListener('keydown', function (ev) {
      if (lightbox.el.classList.contains('hidden')) return;
      if (ev.key === 'Escape') closeLightbox();
      else if (ev.key === 'ArrowLeft') {
        lightbox.index = (lightbox.index - 1 + lightbox.keys.length) % lightbox.keys.length;
        showLightboxImage();
      } else if (ev.key === 'ArrowRight') {
        lightbox.index = (lightbox.index + 1) % lightbox.keys.length;
        showLightboxImage();
      }
    });
  }

  if (statusBtn) {
    statusBtn.addEventListener('click', function () {
      if (statusBtn.classList.contains('disconnected')) connect();
    });
    connect();
  }

  initListUI();
  seedSnapshot();
  document.addEventListener('visibilitychange', function () {
    if (!document.hidden && isLive) refreshUnits();
  });
})();

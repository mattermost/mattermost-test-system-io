(function () {
  function formatTime(dateString) {
    var date = new Date(dateString);
    if (isNaN(date.getTime())) return dateString;
    var diffMs = Date.now() - date.getTime();
    var diffMins = Math.floor(diffMs / 60000);
    if (diffMins < 1) return 'just now';
    if (diffMins < 60) return diffMins + 'm ago';
    var diffHours = Math.floor(diffMins / 60);
    if (diffHours < 24) return diffHours + 'h ago';
    var diffDays = Math.floor(diffHours / 24);
    if (diffDays < 7) return diffDays + 'd ago';
    return date.toLocaleDateString();
  }

  function applyRelativeTimes(root) {
    var scope = root || document;
    scope.querySelectorAll('time.run-time[datetime]').forEach(function (el) {
      var iso = el.getAttribute('datetime');
      if (iso) el.textContent = formatTime(iso);
    });
  }

  var main = document.getElementById('tsio-page-main');
  var repoSlug = main && main.getAttribute('data-repository');
  var branchSelect = document.getElementById('filter-branch');

  function navigateWithBranchFilter(branchFilter) {
    var params = new URLSearchParams(location.search);
    if (branchFilter) params.set('branch_filter', branchFilter);
    else params.delete('branch_filter');
    params.delete('page');
    var q = params.toString();
    location.href = location.pathname + (q ? '?' + q : '');
  }

  function selectedBranchFilter() {
    if (branchSelect && branchSelect.value) return branchSelect.value;
    return new URLSearchParams(location.search).get('branch_filter') || '';
  }

  function liveFragmentURL() {
    var params = new URLSearchParams();
    if (repoSlug) params.set('repository', repoSlug);
    var branch = selectedBranchFilter();
    if (branch) params.set('branch_filter', branch);
    var q = params.toString();
    return q ? '/reports/fragment/home-live?' + q : '/reports/fragment/home-live';
  }

  if (branchSelect) {
    branchSelect.addEventListener('change', function () {
      navigateWithBranchFilter(branchSelect.value);
    });
  }

  var statusBtn = document.getElementById('connection-status');
  var statusDot = document.getElementById('connection-dot');
  var statusPing = document.getElementById('connection-ping');

  var ws = null;
  var reconnectTimer = null;
  var lastLiveHTML = '';
  var refreshInFlight = false;
  var refreshQueued = false;

  function liveInner() {
    return document.getElementById('run-card-live-inner');
  }

  function liveCard() {
    return document.getElementById('run-card-live');
  }

  function reloadForStructuralChange() {
    location.reload();
  }

  function isTerminalEvent(msg) {
    if (!msg || !msg.type) return false;
    if (msg.type === 'home.live.changed') {
      return !!(msg.payload && msg.payload.terminal);
    }
    if (msg.type === 'report_updated') return true;
    return false;
  }

  function shouldRefreshFromEvent(type) {
    if (!type) return false;
    if (type === 'home.live.changed') return true;
    if (type === 'report_created' || type === 'report_updated') return true;
    return false;
  }

  function refreshLiveRows(opts) {
    opts = opts || {};
    if (refreshInFlight) {
      refreshQueued = true;
      return;
    }
    refreshInFlight = true;
    fetch(liveFragmentURL(), { credentials: 'same-origin' })
      .then(function (res) {
        if (!res.ok) throw new Error('live fragment failed');
        return res.json();
      })
      .then(function (data) {
        var html = data.html || '';
        var count = typeof data.count === 'number' ? data.count : 0;
        var inner = liveInner();
        var card = liveCard();

        if (!card && count > 0) {
          reloadForStructuralChange();
          return;
        }
        if (card && count === 0) {
          reloadForStructuralChange();
          return;
        }
        if (opts.forceStaticRefresh) {
          reloadForStructuralChange();
          return;
        }

        if (!inner) {
          lastLiveHTML = '';
          return;
        }

        if (html === lastLiveHTML) {
          return;
        }

        inner.innerHTML = html;
        lastLiveHTML = html;
        var countEl = document.getElementById('run-card-live-count');
        if (countEl) countEl.textContent = String(count);
        applyRelativeTimes(inner);
      })
      .catch(function () {})
      .finally(function () {
        refreshInFlight = false;
        if (refreshQueued) {
          refreshQueued = false;
          refreshLiveRows(opts);
        }
      });
  }

  function seedLiveSnapshot() {
    var inner = liveInner();
    if (inner) {
      lastLiveHTML = inner.innerHTML;
    }
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

  function connect() {
    if (!statusBtn || !statusDot) return;
    if (ws) {
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
      if (liveCard()) {
        refreshLiveRows();
      }
    };
    ws.onmessage = function (ev) {
      try {
        var msg = JSON.parse(ev.data);
        if (!shouldRefreshFromEvent(msg.type)) return;
        refreshLiveRows({ forceStaticRefresh: isTerminalEvent(msg) });
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

  if (statusBtn) {
    statusBtn.addEventListener('click', function () {
      if (statusBtn.classList.contains('disconnected')) {
        connect();
      }
    });
    connect();
  }

  seedLiveSnapshot();
  document.addEventListener('visibilitychange', function () {
    if (!document.hidden && liveCard()) {
      refreshLiveRows();
      applyRelativeTimes();
    }
  });

  applyRelativeTimes();
})();

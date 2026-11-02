(function () {
  var root = document.documentElement;
  var buttons = document.querySelectorAll('.theme-btn[data-theme]');
  var stored = null;
  try {
    stored = localStorage.getItem('theme');
  } catch (_) {}

  function resolved(theme) {
    if (theme === 'system' || !theme) {
      return window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches
        ? 'dark'
        : 'light';
    }
    return theme;
  }

  function apply(theme) {
    var effective = resolved(theme);
    root.classList.toggle('dark', effective === 'dark');
    buttons.forEach(function (btn) {
      btn.classList.toggle('active', btn.getAttribute('data-theme') === theme);
    });
  }

  var initial = stored === 'dark' || stored === 'light' || stored === 'system' ? stored : 'system';
  apply(initial);

  buttons.forEach(function (btn) {
    btn.addEventListener('click', function () {
      var theme = btn.getAttribute('data-theme');
      try {
        localStorage.setItem('theme', theme);
      } catch (_) {}
      apply(theme);
    });
  });

  if (window.matchMedia) {
    window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', function () {
      var current = null;
      try {
        current = localStorage.getItem('theme');
      } catch (_) {}
      if (!current || current === 'system') {
        apply('system');
      }
    });
  }
})();

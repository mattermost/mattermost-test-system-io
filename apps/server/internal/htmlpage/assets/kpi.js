(function () {
  var form = document.getElementById('kpi-filter-form');
  if (!form) return;

  function navigateFromForm() {
    var params = new URLSearchParams(location.search);
    var controls = form.querySelectorAll('select[data-kpi-filter]');
    controls.forEach(function (el) {
      var name = el.getAttribute('name');
      if (!name) return;
      var val = (el.value || '').trim();
      if (!val || (name === 'window' && val === '30d')) {
        params.delete(name);
      } else {
        params.set(name, val);
      }
    });
    params.delete('page');
    var q = params.toString();
    location.href = location.pathname + (q ? '?' + q : '');
  }

  form.querySelectorAll('select[data-kpi-filter]').forEach(function (sel) {
    sel.addEventListener('change', navigateFromForm);
  });
})();

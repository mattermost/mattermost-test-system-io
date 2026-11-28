(function () {
  var controls = document.querySelectorAll('select[data-kpi-filter]');
  if (!controls.length) return;

  controls.forEach(function (sel) {
    sel.addEventListener('change', function () {
      var params = new URLSearchParams(location.search);
      var name = sel.getAttribute('name');
      if (!name) return;
      var val = (sel.value || '').trim();
      if (!val || (name === 'window' && val === '30d')) {
        params.delete(name);
      } else {
        params.set(name, val);
      }
      params.delete('page');
      var q = params.toString();
      location.href = location.pathname + (q ? '?' + q : '');
    });
  });
})();

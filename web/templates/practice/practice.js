document.addEventListener('keydown', function (e) {
  if ((e.ctrlKey || e.metaKey) && (e.key === 'k' || e.key === 'K')) {
    var box = document.getElementById('search');
    if (!box) return;
    e.preventDefault();
    box.focus();
    box.select();
  }
});

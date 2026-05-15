// BackendLab dashboard progressive enhancement. Every page works without
// this script: forms submit normally and active runs offer a refresh link.
(function () {
  "use strict";

  // Ask before submitting forms that carry data-confirm (cancel, resubmit).
  document.addEventListener("submit", function (event) {
    var form = event.target;
    if (!form || !form.getAttribute) {
      return;
    }
    var message = form.getAttribute("data-confirm");
    if (message && !window.confirm(message)) {
      event.preventDefault();
    }
  });

  // Reload the page of an active run periodically. The reload is postponed
  // while a form control has focus so it does not interrupt the operator.
  var main = document.querySelector("main[data-refresh]");
  if (!main) {
    return;
  }
  var seconds = parseInt(main.getAttribute("data-refresh"), 10);
  if (!(seconds > 0)) {
    return;
  }
  function tick() {
    var active = document.activeElement;
    if (active && active !== document.body && active.closest && active.closest("form")) {
      window.setTimeout(tick, seconds * 1000);
      return;
    }
    window.location.reload();
  }
  window.setTimeout(tick, seconds * 1000);
})();

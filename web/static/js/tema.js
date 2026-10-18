// Applies the saved theme before the first paint, so a light-theme user never sees a dark flash.
(function () {
  try {
    var t = localStorage.getItem("consola-tema");
    if (t === "light" || t === "dark") {
      document.documentElement.setAttribute("data-bs-theme", t);
    }
  } catch (e) {
    // Storage blocked (private window): keep the default theme.
  }
})();

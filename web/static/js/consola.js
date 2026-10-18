// Theme toggle: switches between dark and light and remembers the choice in this browser.
(function () {
  var root = document.documentElement;
  var btn = document.getElementById("cambiar-tema");
  if (!btn) return;

  function sync() {
    var dark = root.getAttribute("data-bs-theme") !== "light";
    var label = dark ? "Cambiar a tema claro" : "Cambiar a tema oscuro";
    btn.setAttribute("aria-label", label);
    btn.setAttribute("title", label);
    btn.querySelector(".bi").className = "bi " + (dark ? "bi-sun" : "bi-moon-stars");
    var meta = document.querySelector('meta[name="theme-color"]');
    if (meta) meta.setAttribute("content", dark ? "#0d1117" : "#f6f8fa");
  }

  btn.addEventListener("click", function () {
    var next = root.getAttribute("data-bs-theme") === "light" ? "dark" : "light";
    root.setAttribute("data-bs-theme", next);
    try {
      localStorage.setItem("consola-tema", next);
    } catch (e) {
      // Storage blocked: the change still applies to this page.
    }
    sync();
  });

  sync();
})();

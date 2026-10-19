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

// Running indicator: forms marked with data-operacion run something on the VM, which can
// take minutes. While the request is pending, show it and block a second submission.
// The button is not disabled: a disabled submit button would drop its name/value.
(function () {
  var status = document.getElementById("estado-ejecucion");
  var timer = document.getElementById("cronometro");
  var interval = null;
  var pending = []; // [form, button, original label]

  document.querySelectorAll("form[data-operacion]").forEach(function (form) {
    form.addEventListener("submit", function (event) {
      // Without event.submitter (old browsers) the button is a guess: the first one that runs something.
      var button = event.submitter || form.querySelector("[type=submit]:not([data-local])");
      // A button marked data-local (a preview, for example) runs nothing on the VM.
      if (button && button.hasAttribute("data-local")) return;
      if (form.dataset.enviando === "1") {
        event.preventDefault();
        return;
      }
      form.dataset.enviando = "1";
      document.body.classList.add("is-running");
      if (button) {
        pending.push([form, button, button.innerHTML]);
        button.setAttribute("aria-disabled", "true");
        button.textContent = "Ejecutando…";
      }
      // Announced once by screen readers; the timer next to it is visual only.
      // A button can name its own operation (one form, "Instalar" and "Eliminar").
      var operacion = (button && button.dataset.operacion) || form.dataset.operacion;
      if (status) status.textContent = "Ejecutando «" + operacion + "»…";
      var start = Date.now();
      function tick() {
        if (timer) timer.textContent = Math.floor((Date.now() - start) / 1000) + " s";
      }
      tick();
      interval = setInterval(tick, 1000);
    });
  });

  // Going Back restores this page from the browser cache exactly as it was left, still
  // "running". Reset it so the form can be used again.
  window.addEventListener("pageshow", function (event) {
    if (!event.persisted) return;
    clearInterval(interval);
    document.body.classList.remove("is-running");
    if (status) status.textContent = "";
    if (timer) timer.textContent = "";
    pending.forEach(function (p) {
      delete p[0].dataset.enviando;
      p[1].removeAttribute("aria-disabled");
      p[1].innerHTML = p[2];
    });
    pending = [];
  });
})();

// Copy buttons: data-copiar="<id>" copies the text of that element.
(function () {
  var notice = document.getElementById("aviso-copia");
  document.querySelectorAll("[data-copiar]").forEach(function (button) {
    var label = button.innerHTML;
    function say(text) {
      if (notice) notice.textContent = text;
    }
    button.addEventListener("click", function () {
      var source = document.getElementById(button.dataset.copiar);
      if (!source) return;
      if (!navigator.clipboard) {
        say("Este navegador no permite copiar automáticamente. Selecciona el texto de la salida y cópialo con Ctrl + C.");
        return;
      }
      var text = Array.prototype.map.call(source.querySelectorAll(".t-cmd, .t-out, .t-meta"), function (el) {
        return el.textContent.trim();
      }).join("\n");
      navigator.clipboard.writeText(text).then(
        function () {
          button.innerHTML = '<i class="bi bi-clipboard-check" aria-hidden="true"></i> Copiado';
          say("Salida copiada al portapapeles.");
          setTimeout(function () {
            button.innerHTML = label;
          }, 2000);
        },
        function () {
          say("No se pudo copiar. Selecciona el texto de la salida y cópialo con Ctrl + C.");
        }
      );
    });
  });
})();

// After a rejected form comes back, put the keyboard on the first invalid field.
(function () {
  var first = document.querySelector('[aria-invalid="true"]');
  if (first) first.focus();
})();

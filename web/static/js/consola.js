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

// Reusable page behaviour: consola.js binds it once, and a module that replaces part of the
// document (usuarios.js) calls window.consola.enlazar() again on the new nodes.
window.consola = window.consola || {};

// Running indicator: forms marked with data-operacion run something on the VM, which can
// take minutes. While the request is pending, show it and block a second submission.
// The button is not disabled: a disabled submit button would drop its name/value.
(function () {
  var status = document.getElementById("estado-ejecucion");
  var timer = document.getElementById("cronometro");
  var interval = null;
  var pending = []; // [form, button, original label]

  function ejecutando(operacion) {
    clearInterval(interval);
    document.body.classList.add("is-running");
    // Announced once by screen readers; the timer next to it is visual only.
    if (status) status.textContent = "Ejecutando «" + (operacion || "la operación") + "»…";
    var start = Date.now();
    function tick() {
      if (timer) timer.textContent = Math.floor((Date.now() - start) / 1000) + " s";
    }
    tick();
    interval = setInterval(tick, 1000);
  }

  function terminado() {
    clearInterval(interval);
    interval = null;
    document.body.classList.remove("is-running");
    if (status) status.textContent = "";
    if (timer) timer.textContent = "";
  }

  function enlazarEjecucion(root) {
    root.querySelectorAll("form[data-operacion]").forEach(function (form) {
      if (form.dataset.enlazado === "1") return;
      // A form the users page sends with fetch drives the indicator itself (usuarios.js).
      if (form.hasAttribute("data-crud") && window.fetch) return;
      form.dataset.enlazado = "1";
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
        if (button) {
          pending.push([form, button, button.innerHTML]);
          button.setAttribute("aria-disabled", "true");
          button.textContent = "Ejecutando…";
        }
        // A button can name its own operation (one form, "Instalar" and "Eliminar").
        ejecutando((button && button.dataset.operacion) || form.dataset.operacion);
      });
    });
  }

  // Going Back restores this page from the browser cache exactly as it was left, still
  // "running". Reset it so the form can be used again.
  window.addEventListener("pageshow", function (event) {
    if (!event.persisted) return;
    terminado();
    pending.forEach(function (p) {
      delete p[0].dataset.enviando;
      p[1].removeAttribute("aria-disabled");
      p[1].innerHTML = p[2];
    });
    pending = [];
  });

  window.consola.ejecutando = ejecutando;
  window.consola.terminado = terminado;
  window.consola.enlazarEjecucion = enlazarEjecucion;
  enlazarEjecucion(document);
})();

// Copy buttons: data-copiar="<id>" copies the text of that element.
(function () {
  function enlazarCopiar(root) {
    root.querySelectorAll("[data-copiar]").forEach(function (button) {
      if (button.dataset.enlazado === "1") return;
      button.dataset.enlazado = "1";
      var label = button.innerHTML;
      function say(text) {
        var notice = document.getElementById("aviso-copia"); // re-read: a swap may replace it
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
  }

  // Bind every behaviour a module needs after it replaced part of the document.
  window.consola.enlazar = function (root) {
    root = root || document;
    window.consola.enlazarEjecucion(root);
    enlazarCopiar(root);
  };

  enlazarCopiar(document);
})();

// After a rejected form comes back, put the keyboard on the first invalid field.
(function () {
  var first = document.querySelector('[aria-invalid="true"]');
  if (first) first.focus();
})();

// Ctrl+K palette (templates/_paleta.html): a filterable list of pages, module actions and
// targets. Choosing an item follows its link or presses its button; nothing runs on a VM.
(function () {
  var dialog = document.getElementById("paleta");
  var opener = document.getElementById("abrir-paleta");
  if (!dialog || typeof dialog.showModal !== "function") return;
  var input = document.getElementById("paleta-texto");
  var options = Array.prototype.slice.call(dialog.querySelectorAll('[role="option"]'));
  var empty = document.getElementById("paleta-vacio");
  var status = document.getElementById("paleta-estado");
  var active = null;
  var announce = null; // the result count is read once the user stops typing
  var lastPointer = ""; // Chrome sends mousemove after scrolling, without the pointer moving
  var mac = /Mac|iPhone|iPad/.test(navigator.platform);

  // Accent- and case-insensitive: "preparacion" finds "Preparación". Each typed word must
  // start a word of the option (its text plus data-claves), so "tema" does not find "sistema".
  function plain(s) {
    return s.normalize("NFD").replace(/[\u0300-\u036f]/g, "").toLowerCase();
  }
  options.forEach(function (o) {
    o.palabras = plain(o.textContent + " " + (o.dataset.claves || "")).split(/[^a-z0-9]+/).filter(Boolean);
  });
  function matches(o, w) {
    return o.palabras.some(function (p) { return p.indexOf(w) === 0; });
  }

  function visible() {
    return options.filter(function (o) { return !o.hidden; });
  }

  function select(o) {
    if (active) active.setAttribute("aria-selected", "false");
    active = o;
    if (o) {
      o.setAttribute("aria-selected", "true");
      input.setAttribute("aria-activedescendant", o.id);
      o.scrollIntoView({ block: "nearest" });
    } else {
      input.removeAttribute("aria-activedescendant");
    }
  }

  function filter(quiet) {
    var words = plain(input.value).split(/[^a-z0-9]+/).filter(Boolean);
    options.forEach(function (o) {
      o.hidden = !words.every(function (w) { return matches(o, w); });
    });
    var shown = visible();
    empty.hidden = shown.length > 0;
    clearTimeout(announce);
    status.textContent = "";
    if (!quiet) {
      announce = setTimeout(function () {
        status.textContent = shown.length === 1 ? "1 resultado" : shown.length + " resultados";
      }, 300);
    }
    select(shown[0] || null);
  }

  function open() {
    if (dialog.open) return;
    // The mobile menu (an offcanvas) would keep the focus inside itself: close it first.
    var menu = document.querySelector(".offcanvas.show, .offcanvas-lg.show");
    if (menu && window.bootstrap) {
      var oc = window.bootstrap.Offcanvas.getInstance(menu);
      if (oc) oc.hide();
    }
    input.value = "";
    filter(true);
    dialog.showModal();
    input.focus();
  }

  // Opens an option without simulating a click inside it.
  function activate(o) {
    if (!o) return;
    var target = o.querySelector("a, button");
    dialog.close();
    if (target.tagName === "A") {
      window.location.assign(target.href);
    } else if (target.dataset.paleta === "tema") {
      var tema = document.getElementById("cambiar-tema");
      if (tema) tema.click();
    } else if (o.hasAttribute("data-actual")) {
      // Already the target: nothing to change.
    } else if (target.form) {
      // A target: POST /destino with its name (form.submit where requestSubmit is missing).
      if (target.form.requestSubmit) {
        target.form.requestSubmit(target);
      } else {
        var field = document.createElement("input");
        field.type = "hidden";
        field.name = target.name;
        field.value = target.value;
        target.form.appendChild(field);
        target.form.submit();
      }
    }
  }

  document.addEventListener("keydown", function (e) {
    // Cmd+K on a Mac (Ctrl+K there deletes to the end of the line); Ctrl+K elsewhere. The
    // key code also covers keyboard layouts where K is another letter.
    var mod = mac ? e.metaKey : e.ctrlKey;
    if (mod && !e.altKey && !e.shiftKey && (e.code === "KeyK" || e.key.toLowerCase() === "k")) {
      e.preventDefault();
      if (dialog.open) dialog.close();
      else open();
    }
  });

  input.addEventListener("input", function () { filter(false); });
  input.addEventListener("keydown", function (e) {
    if (e.isComposing || e.keyCode === 229) return; // Enter that ends an IME composition
    var shown = visible();
    var i = shown.indexOf(active);
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      if (!shown.length) return;
      var step = e.key === "ArrowDown" ? 1 : -1;
      select(shown[(i + step + shown.length) % shown.length]);
    } else if (e.key === "Home" || e.key === "End") {
      if (!shown.length || input.value) return; // with text, Home/End move the cursor
      e.preventDefault();
      select(e.key === "Home" ? shown[0] : shown[shown.length - 1]);
    } else if (e.key === "Enter") {
      e.preventDefault();
      activate(active);
    }
  });

  options.forEach(function (o) {
    o.addEventListener("click", function (e) {
      e.preventDefault(); // the link or button inside does not act by itself; activate does
      activate(o);
    });
    o.addEventListener("mousemove", function (e) {
      var at = e.clientX + "," + e.clientY;
      if (at === lastPointer) return;
      lastPointer = at;
      if (active !== o) select(o);
    });
  });

  // A click on the dark backdrop (outside the box) closes it.
  dialog.addEventListener("click", function (e) {
    if (e.target === dialog) dialog.close();
  });

  if (opener) {
    opener.hidden = false;
    opener.addEventListener("click", open);
  }
})();

// Arriving at a form field through a link (#paquete, from the palette): put the cursor in it.
(function () {
  function focusTarget() {
    var id;
    try {
      id = decodeURIComponent(location.hash.slice(1));
    } catch (e) {
      return; // a malformed address: nothing to focus
    }
    var el = id && document.getElementById(id);
    if (el && /^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName)) el.focus();
  }
  window.addEventListener("hashchange", focusTarget);
  focusTarget();
})();

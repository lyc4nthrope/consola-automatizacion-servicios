// Users page (CRUD) without reloading the document. Every form and link marked data-crud is
// sent with fetch and the page content is replaced with the fragment the console returns
// (X-Consola-Parcial). Without JavaScript, or when fetch fails, the same links and forms
// navigate normally: the server keeps answering the classic POST → redirect → result page.
(function () {
  var contenedor = document.getElementById("contenido");
  if (!contenedor || !window.fetch || !document.querySelector("[data-crud]")) return;

  function cabeceras() {
    return { "Accept": "text/html", "X-Consola-Parcial": "1" };
  }

  function ocupado(activo) {
    contenedor.setAttribute("aria-busy", activo ? "true" : "false");
  }

  function reenlazar() {
    if (window.consola && window.consola.enlazar) window.consola.enlazar(contenedor);
  }

  // Where the keyboard goes after the content changed: the invalid field, the operation's
  // outcome, the first field of an opened panel, the accounts block, or the container itself.
  function enfocar() {
    var destino = contenedor.querySelector('[aria-invalid="true"]') ||
      contenedor.querySelector("#resultado-operacion") ||
      contenedor.querySelector("#cuentas form input:not([type=hidden])") ||
      contenedor.querySelector("#cuentas-titulo") ||
      contenedor;
    if (destino !== contenedor && !/^(INPUT|TEXTAREA|SELECT|BUTTON|A)$/.test(destino.tagName) &&
      destino.getAttribute("tabindex") === null) {
      destino.setAttribute("tabindex", "-1");
    }
    destino.focus();
  }

  function interceptable(evento) {
    return evento.button === 0 && !evento.metaKey && !evento.ctrlKey && !evento.shiftKey && !evento.altKey;
  }

  function navegar(url) {
    window.location.assign(url);
  }

  function pedir(url, opciones, esEnvio) {
    ocupado(true);
    return fetch(url, opciones)
      .then(function (respuesta) {
        // A flow that ends elsewhere (the one-time key download) redirects: follow it as a
        // normal navigation instead of dropping a whole page inside this one.
        if (respuesta.redirected) {
          navegar(respuesta.url);
          return null;
        }
        // A fragment always comes as HTML, even when the form was rejected (422). Anything
        // else is a plain-text error: fall back to the normal page instead of injecting it.
        var tipo = respuesta.headers.get("Content-Type") || "";
        if (tipo.indexOf("text/html") === -1) {
          if (esEnvio) window.location.reload();
          else navegar(url);
          return null;
        }
        return respuesta.text();
      })
      .then(function (html) {
        if (html === null) return;
        contenedor.innerHTML = html;
        reenlazar();
        enfocar();
      })
      .catch(function () {
        // No network, no fetch or a proxy in between: the classic navigation still works.
        if (esEnvio) window.location.reload();
        else navegar(url);
      })
      .finally(function () {
        ocupado(false);
      });
  }

  document.addEventListener("submit", function (evento) {
    var form = evento.target;
    if (!form.matches || !form.matches("form[data-crud]")) return;
    evento.preventDefault();
    if (form.dataset.enviando === "1") return;
    form.dataset.enviando = "1";

    var boton = evento.submitter;
    // getAttribute, not the formAction property: without a formaction attribute the property
    // resolves to the document's URL in Chrome, which would post to the wrong route.
    var destino = (boton && boton.getAttribute("formaction")) || form.action;
    var cabecerasEnvio = cabeceras();
    var cuerpo;
    try {
      // The encoding a plain form submit uses. Go's ParseForm reads it directly, and it keeps
      // the request smaller than a multipart body.
      cuerpo = new URLSearchParams(new FormData(form, boton || undefined)).toString();
      cabecerasEnvio["Content-Type"] = "application/x-www-form-urlencoded;charset=UTF-8";
    } catch (e) {
      // Old browser without URLSearchParams(FormData): send multipart; the server reads both.
      cuerpo = new FormData(form, boton || undefined);
    }
    var operacion = (boton && boton.dataset.operacion) || form.dataset.operacion;
    if (window.consola && window.consola.ejecutando) window.consola.ejecutando(operacion);

    pedir(destino, {
      method: "POST",
      body: cuerpo,
      headers: cabecerasEnvio,
      credentials: "same-origin"
    }, true).finally(function () {
      delete form.dataset.enviando;
      if (window.consola && window.consola.terminado) window.consola.terminado();
      // The answer is always the accounts page (the result is shown in line), even when the
      // form was submitted from the edit panel's address.
      if (window.location.pathname !== "/usuarios" && !window.location.pathname.startsWith("/usuarios/llaves/")) {
        history.replaceState({ crud: 1 }, "", "/usuarios");
      }
    });
  });

  document.addEventListener("click", function (evento) {
    var enlace = evento.target.closest ? evento.target.closest("a[data-crud]") : null;
    if (!enlace || !interceptable(evento)) return;
    evento.preventDefault();
    history.pushState({ crud: 1 }, "", enlace.href);
    pedir(enlace.href, { headers: cabeceras(), credentials: "same-origin" });
  });

  window.addEventListener("popstate", function () {
    pedir(window.location.href, { headers: cabeceras(), credentials: "same-origin" });
  });
})();

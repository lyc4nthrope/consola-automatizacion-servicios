// Usuarios page actions. The CSP (default-src 'self') forbids inline scripts and inline
// event handlers, so everything lives in this file, loaded from /static/.
(function () {
  var select = document.getElementById("eliminar");
  var form = document.getElementById("form-eliminar");
  var btnEditar = document.getElementById("btnEditar");
  var btnEliminar = document.getElementById("btnEliminar");

  if (btnEditar && select) {
    btnEditar.addEventListener("click", function () {
      var usuario = select.value;
      if (!usuario) {
        window.alert("Selecciona un usuario primero.");
        return;
      }
      window.location.href = "/usuarios/editar?usuario=" + encodeURIComponent(usuario);
    });
  }

  if (btnEliminar && form && select) {
    btnEliminar.addEventListener("click", function (event) {
      // Without JavaScript the GET keeps working and shows the server-side confirmation.
      event.preventDefault();
      var usuario = select.value;
      if (!usuario) {
        window.alert("Selecciona un usuario primero.");
        return;
      }
      var archivos = document.getElementById("archivos");
      var mensaje = "¿Eliminar a " + usuario + "? Esta acción no se puede deshacer.";
      if (archivos && archivos.checked) {
        mensaje += " También se borrarán su carpeta /home/" + usuario + " y sus archivos.";
      }
      if (!window.confirm(mensaje)) {
        return;
      }
      // Confirm accepted: turn this GET (preview) into the real POST and let it submit.
      form.method = "post";
      form.action = "/usuarios/eliminar";
      select.name = "usuario";
      form.setAttribute("data-operacion", "Eliminar el usuario");
      if (form.requestSubmit) {
        form.requestSubmit(btnEliminar);
      } else {
        form.submit();
      }
    });
  }
})();

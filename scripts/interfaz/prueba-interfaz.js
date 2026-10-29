// Browser check of the whole assignment: a real Chromium uses every screen of the console
// the way a person does (choose the target, fill the fields, press the buttons, download
// the keys) and each effect is verified inside the VMs over SSH. A screenshot of every
// result goes to SALIDA.
//
//   cd scripts/interfaz && npm install && cd ../.. && node scripts/interfaz/prueba-interfaz.js
//
// Environment (defaults match config.example.json and the lab VMs):
//   CONSOLA   console URL                      http://127.0.0.1:8090
//   VM_IP     test VM (target "pruebas")       192.168.40.57
//   BAL_IP    balancer VM (target "balanceador") 192.168.40.53
//   BACKEND   backend of the balancer          http_back
//   KEY       console private key              keys/consola_ed25519
//   SALIDA    screenshots folder               evidencias/interfaz
//   CHROMIUM  Chromium executable              Playwright's own browser
//
// Same requirements as scripts/prueba-completa.sh (see docs/PRUEBA-COMPLETA.md). Take a
// snapshot of both VMs first.
"use strict";

const { chromium } = require("playwright-core");
const { execFileSync } = require("child_process");
const fs = require("fs");
const os = require("os");
const path = require("path");

const CONSOLA = process.env.CONSOLA || "http://127.0.0.1:8090";
const VM_IP = process.env.VM_IP || "192.168.40.57";
const BAL_IP = process.env.BAL_IP || "192.168.40.53";
const BACKEND = process.env.BACKEND || "http_back";
const KEY = process.env.KEY || "keys/consola_ed25519";
const SALIDA = process.env.SALIDA || "evidencias/interfaz";
const LENTO = 240000; // apt and deployments can take minutes

const resultados = [];
let n = 0;

// ssh runs one command line in a VM as root with the console key; it returns stdout, or
// null when the command fails.
function ssh(ip, comando, llave = KEY, usuario = "root") {
  try {
    return execFileSync("ssh", ["-i", llave, "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=no",
      "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=10", `${usuario}@${ip}`, comando],
    { stdio: ["ignore", "pipe", "ignore"], timeout: 60000 }).toString().trim();
  } catch {
    return null;
  }
}
const vm = (c) => ssh(VM_IP, c);

function verificar(nombre, condicion) {
  resultados.push({ nombre, ok: !!condicion });
  console.log(`  ${condicion ? "\x1b[32mOK\x1b[0m   " : "\x1b[31mFALLA\x1b[0m"} ${nombre}`);
}

async function captura(page, nombre) {
  n++;
  const archivo = path.join(SALIDA, `${String(n).padStart(2, "0")}_${nombre}.png`);
  await page.screenshot({ path: archivo, fullPage: true });
}

// resultado waits for the outcome box (result page or the inline result of the users
// page) and reports whether it says the operation succeeded.
async function resultado(page, nombre, esperado = true) {
  const caja = page.locator(".outcome").first();
  await caja.waitFor({ timeout: LENTO });
  const ok = (await caja.getAttribute("class")).includes("outcome-ok");
  const texto = (await caja.locator(".outcome-text").innerText().catch(() => "")).replace(/\s+/g, " ");
  verificar(`${nombre} — ${texto.slice(0, 140)}`, ok === esperado);
  await captura(page, nombre.toLowerCase().replace(/[^a-z0-9]+/g, "-").slice(0, 40));
  return ok;
}

async function pulsar(page, nombre, alcance = page) {
  await Promise.all([
    page.waitForLoadState("load", { timeout: LENTO }),
    alcance.getByRole("button", { name: nombre, exact: true }).click(),
  ]);
}

async function destino(page, nombre) {
  await page.goto(`${CONSOLA}/`);
  await page.selectOption("#destino", nombre);
  await pulsar(page, "Usar destino");
}

// abrirAlta opens the "Nuevo usuario" panel (a <details>) as a person does.
async function abrirAlta(page) {
  const panel = page.locator("details#nuevo-usuario");
  if ((await panel.getAttribute("open")) === null) await panel.locator("summary").click();
  return panel.locator("form[action='/usuarios/crear']");
}

function titulo(t) {
  console.log(`\n== ${t}`);
}

(async () => {
  fs.mkdirSync(SALIDA, { recursive: true });
  const opciones = {};
  if (process.env.CHROMIUM) opciones.executablePath = process.env.CHROMIUM;
  else {
    const propio = path.join(os.homedir(), ".cache/ms-playwright/chromium-1148/chrome-linux/chrome");
    if (fs.existsSync(propio)) opciones.executablePath = propio;
  }
  const navegador = await chromium.launch(opciones);
  const contexto = await navegador.newContext({ acceptDownloads: true, viewport: { width: 1280, height: 900 } });
  const page = await contexto.newPage();
  page.setDefaultTimeout(LENTO);
  const llaves = fs.mkdtempSync(path.join(os.tmpdir(), "consola-llaves-"));
  let hostAntes; // read in 3.1.4: before "Conectar por SSH" the console key is not authorized yet

  try {
    titulo("Panel y canal (Guest Additions → SSH)");
    await destino(page, "pruebas");
    verificar("El panel muestra la VM de pruebas como destino actual", (await page.locator("body").innerText()).includes(VM_IP));
    await captura(page, "panel");
    await pulsar(page, "Probar conexión");
    await resultado(page, "Probar conexión");
    await page.goto(`${CONSOLA}/usuarios`);
    const conectar = page.getByRole("button", { name: "Conectar por SSH", exact: true });
    if (await conectar.count()) {
      await pulsar(page, "Conectar por SSH");
      await resultado(page, "Conectar por SSH");
    }
    await page.goto(`${CONSOLA}/`);
    verificar("El panel muestra el canal SSH", /\bSSH\b/.test(await page.locator(".status-bar, footer, header").first().innerText().catch(() => "")) || (await page.content()).includes(">SSH<"));

    titulo("3.1.1 Instalar y eliminar aplicaciones");
    vm("apt-get remove -y tree");
    await page.goto(`${CONSOLA}/preparacion`);
    await page.fill("#paquete", "tree; rm -rf /");
    await pulsar(page, "Instalar");
    verificar("«tree; rm -rf /» se rechaza junto al campo", await page.locator("#paquete-error").isVisible());
    await captura(page, "validacion-paquete");
    await page.fill("#paquete", "tree");
    await pulsar(page, "Instalar");
    await resultado(page, "Instalar tree");
    verificar("En la VM: which tree lo encuentra", vm("which tree"));
    await page.goto(`${CONSOLA}/preparacion`);
    await page.fill("#paquete", "tree");
    await pulsar(page, "Eliminar");
    await resultado(page, "Eliminar tree");
    verificar("En la VM: tree ya no está", vm("which tree") === null);

    titulo("3.1.2 Llaves de root (desde Preparación)");
    await page.goto(`${CONSOLA}/preparacion`);
    await pulsar(page, "Generar llaves de root");
    await page.waitForURL(/\/usuarios\/llaves\/listo\//, { timeout: LENTO });
    const consejo = await page.locator("body").innerText();
    verificar("La página recomienda guardar la llave con chmod 600 y no compartirla", consejo.includes("chmod 600") && consejo.includes("No la compartas"));
    await captura(page, "llave-root-lista");
    const [descarga] = await Promise.all([page.waitForEvent("download"), page.getByRole("link", { name: "Descargar llave privada" }).click()]);
    const llaveRoot = path.join(llaves, "root");
    await descarga.saveAs(llaveRoot);
    fs.chmodSync(llaveRoot, 0o600);
    verificar("Con la llave descargada se entra como root", ssh(VM_IP, "true", llaveRoot) !== null);
    verificar("sshd: root solo con llave", /^permitrootlogin (without-password|prohibit-password)$/m.test(vm("sshd -T") || ""));

    titulo("3.1.3 Mensaje de entrada /etc/issue");
    await page.goto(`${CONSOLA}/preparacion`);
    for (const id of ["dato-equipo", "dato-ip", "dato-fecha", "dato-hora"]) await page.check(`#${id}`);
    await page.fill("#mensaje", "Prueba de interfaz - Computacion en la Nube");
    await pulsar(page, "Vista previa");
    verificar("La vista previa muestra el mensaje sin tocar la VM", (await page.locator("#vista-previa").innerText()).includes("Prueba de interfaz") && !(vm("cat /etc/issue") || "").includes("Prueba de interfaz"));
    await captura(page, "issue-vista-previa");
    await pulsar(page, "Instalar en la VM");
    await resultado(page, "Instalar /etc/issue");
    verificar("En la VM: /etc/issue tiene el mensaje", (vm("cat /etc/issue") || "").includes("Prueba de interfaz"));
    await page.goto(`${CONSOLA}/preparacion`);
    await pulsar(page, "Restaurar el mensaje original");
    await resultado(page, "Restaurar /etc/issue");
    verificar("En la VM: /etc/issue volvió al original", !(vm("cat /etc/issue") || "x").includes("Prueba de interfaz"));

    titulo("3.1.4 Nombre de host y /etc/hosts");
    hostAntes = vm("hostname");
    verificar("Se leyó el nombre de host original por SSH", !!hostAntes);
    await page.goto(`${CONSOLA}/preparacion`);
    await page.fill("#hostname", "prueba-interfaz");
    await pulsar(page, "Cambiar nombre");
    await resultado(page, "Cambiar nombre de host");
    verificar("En la VM: hostname y la línea 127.0.1.1", vm("hostname") === "prueba-interfaz" && /^127\.0\.1\.1\s+prueba-interfaz/m.test(vm("cat /etc/hosts") || ""));
    await page.goto(`${CONSOLA}/preparacion`);
    await page.fill("#hostname", hostAntes);
    await pulsar(page, "Cambiar nombre");
    await resultado(page, `Volver al nombre ${hostAntes}`);

    titulo("3.2.1 Crear usuario con sudo y llave");
    vm("pkill -u prueba1; userdel -r prueba1");
    await page.goto(`${CONSOLA}/usuarios`);
    let alta = await abrirAlta(page);
    await alta.locator("#usuario").fill("prueba1");
    await alta.locator("#contrasena").fill("Clave-Segura-2026");
    await alta.locator("#sudo").check();
    await alta.locator("#llave").check();
    await captura(page, "crear-usuario-formulario");
    await alta.getByRole("button", { name: "Crear usuario", exact: true }).click();
    await page.waitForURL(/\/usuarios\/llaves\/listo\//, { timeout: LENTO });
    await captura(page, "llave-usuario-lista");
    const [descargaU] = await Promise.all([page.waitForEvent("download"), page.getByRole("link", { name: "Descargar llave privada" }).click()]);
    const llaveU = path.join(llaves, "prueba1");
    await descargaU.saveAs(llaveU);
    fs.chmodSync(llaveU, 0o600);
    verificar("En la VM: prueba1 está en el grupo sudo", (vm("id prueba1") || "").includes("(sudo)"));
    verificar("prueba1 entra con su llave", ssh(VM_IP, "true", llaveU, "prueba1") !== null);
    verificar("sshd: prueba1 sin contraseña", /^passwordauthentication no$/m.test(vm("sshd -T -C user=prueba1,host=x,addr=127.0.0.1") || ""));

    titulo("3.2.2 Eliminar usuario (con y sin archivos)");
    for (let i = 0; i < 20 && vm("pgrep -u prueba1"); i++) await page.waitForTimeout(1000);
    await page.goto(`${CONSOLA}/usuarios`);
    await page.locator("a[href='/usuarios?confirmar=prueba1']").click();
    const confirmar = page.locator("#confirmar-eliminar");
    await confirmar.waitFor();
    await confirmar.locator("#archivos").uncheck();
    await captura(page, "eliminar-confirmacion");
    await confirmar.getByRole("button", { name: "Eliminar", exact: true }).click();
    await resultado(page, "Eliminar prueba1 sin archivos");
    verificar("En la VM: prueba1 no existe y /home/prueba1 sigue", vm("getent passwd prueba1") === null && vm("test -d /home/prueba1") !== null);
    vm("rm -rf /home/prueba1; pkill -u prueba2; userdel -r prueba2");
    await page.goto(`${CONSOLA}/usuarios`);
    alta = await abrirAlta(page);
    await alta.locator("#usuario").fill("prueba2");
    await alta.locator("#contrasena").fill("Clave-Segura-2026");
    await alta.locator("#sudo").uncheck();
    await alta.locator("#llave").uncheck();
    await alta.getByRole("button", { name: "Crear usuario", exact: true }).click();
    await resultado(page, "Crear prueba2 (sin sudo ni llave)");
    await page.locator("a[href='/usuarios?confirmar=prueba2']").click();
    await confirmar.waitFor();
    await confirmar.locator("#archivos").check();
    await confirmar.getByRole("button", { name: "Eliminar", exact: true }).click();
    await resultado(page, "Eliminar prueba2 con archivos");
    verificar("En la VM: prueba2 y /home/prueba2 ya no existen", vm("getent passwd prueba2") === null && vm("test -d /home/prueba2") === null);

    titulo("3.3 systemd: desplegar Bookzilla y los 6 botones");
    vm("systemctl disable --now bookzilla fecha; rm -rf /opt/bookzilla /opt/fecha /etc/systemd/system/bookzilla.service /etc/systemd/system/fecha.service /var/log/consola-fecha.log; systemctl daemon-reload");
    await page.goto(`${CONSOLA}/servicios`);
    await page.selectOption("#aplicacion", "bookzilla");
    await page.fill("#servicio", "bookzilla");
    await page.fill("#ejecutable", "bookzilla");
    await page.fill("#puerto", "8080");
    await page.uncheck("#oneshot");
    await pulsar(page, "Desplegar Servicio");
    await resultado(page, "Desplegar bookzilla");
    verificar("En la VM: /opt/bookzilla/bookzilla instalado y la unidad habilitada", vm("test -x /opt/bookzilla/bookzilla && systemctl is-enabled bookzilla") === "enabled");
    const acciones = [
      ["start", () => vm("systemctl is-active bookzilla") === "active"],
      ["status", null],
      ["stop", () => vm("systemctl is-active bookzilla") === null],
      ["status", null],
      ["restart", () => vm("systemctl is-active bookzilla") === "active"],
      ["disable", () => vm("systemctl is-enabled bookzilla") === null],
      ["enable", () => vm("systemctl is-enabled bookzilla") === "enabled"],
    ];
    for (const [accion, comprobar] of acciones) {
      await page.goto(`${CONSOLA}/servicios`);
      await page.fill("#servicio-accion", "bookzilla");
      await page.selectOption("#accion", accion);
      await pulsar(page, "Ejecutar Acción");
      await resultado(page, `Botón ${accion}`);
      if (comprobar) verificar(`En la VM: efecto de ${accion}`, comprobar());
    }
    await page.waitForTimeout(1500);
    verificar(`Bookzilla responde en http://${VM_IP}:8080`, (await (await fetch(`http://${VM_IP}:8080/`)).text()).toLowerCase().includes("bookzilla"));

    titulo("3.3 systemd: script de fecha (tarea única)");
    await page.goto(`${CONSOLA}/servicios`);
    await page.selectOption("#aplicacion", "fecha");
    await page.fill("#servicio", "fecha");
    await page.fill("#ejecutable", "fecha.sh");
    await page.fill("#puerto", "");
    await page.check("#oneshot");
    await pulsar(page, "Desplegar Servicio");
    await resultado(page, "Desplegar fecha (oneshot)");
    for (const vez of [1, 2]) {
      await page.goto(`${CONSOLA}/servicios`);
      await page.fill("#servicio-accion", "fecha");
      await page.selectOption("#accion", "start");
      await pulsar(page, "Ejecutar Acción");
      await resultado(page, `start de fecha (${vez})`);
    }
    verificar("En la VM: cada start agregó una línea", vm("wc -l < /var/log/consola-fecha.log") === "2");

    titulo("3.4 HAProxy en la VM balanceador");
    await destino(page, "balanceador");
    await pulsar(page, "Probar conexión");
    await resultado(page, "Probar conexión con el balanceador");
    await page.goto(`${CONSOLA}/balanceador`);
    await page.fill("#backend", BACKEND);
    await page.fill("#ip", VM_IP);
    await page.fill("#puerto", "8080");
    await pulsar(page, "Agregar Servidor");
    await resultado(page, `Agregar ${VM_IP}:8080 a ${BACKEND}`);
    verificar("En el balanceador: haproxy.cfg tiene el servidor", (ssh(BAL_IP, "cat /etc/haproxy/haproxy.cfg") || "").includes(`${VM_IP}:8080 check`));
    let entrega = false;
    for (let i = 0; i < 12 && !entrega; i++) {
      try {
        const r = await fetch(`http://${BAL_IP}/`, { signal: AbortSignal.timeout(3000) });
        entrega = (await r.text()).toLowerCase().includes("bookzilla");
      } catch { /* a stopped lab server does not answer */ }
    }
    verificar("El balanceador (puerto 80) entrega Bookzilla", entrega);
    await page.goto(`${CONSOLA}/balanceador`);
    await page.fill("#backend-quitar", BACKEND);
    await page.fill("#ip-quitar", VM_IP);
    await pulsar(page, "Quitar Servidor");
    await resultado(page, `Quitar ${VM_IP} de ${BACKEND}`);
    verificar("En el balanceador: el servidor ya no está", !(ssh(BAL_IP, "cat /etc/haproxy/haproxy.cfg") || `${VM_IP}:8080`).includes(`${VM_IP}:8080`));

    titulo("Registro de operaciones");
    await destino(page, "pruebas");
    verificar("El panel lista las últimas operaciones", (await page.locator("body").innerText()).includes("Quitar servidor") || (await page.locator("a[href^='/resultado/']").count()) > 0);
    await captura(page, "panel-final");
  } catch (e) {
    verificar(`La prueba se detuvo: ${e.message.split("\n")[0]}`, false);
    await captura(page, "detenida").catch(() => {});
  } finally {
    await navegador.close();
    fs.rmSync(llaves, { recursive: true, force: true });
  }

  const fallas = resultados.filter((r) => !r.ok).length;
  console.log(`\nResultado: ${resultados.length - fallas} OK, ${fallas} FALLA · capturas en ${SALIDA}`);
  process.exit(fallas ? 1 : 0);
})();

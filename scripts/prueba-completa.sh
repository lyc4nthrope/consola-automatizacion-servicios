#!/usr/bin/env bash
# End-to-end check of the whole console against a real VM: every module of the
# assignment, driven through the console's own HTTP forms (as the browser does),
# with each effect verified inside the VM over SSH.
#
# Usage (from the repository root, with the console already running on CONSOLA):
#   scripts/prueba-completa.sh
#
# Environment (defaults match config.example.json and the lab VM):
#   CONSOLA   console URL                     http://127.0.0.1:8090
#   VM_IP     IP of the test VM               192.168.40.57
#   KEY       console private key             keys/consola_ed25519
#   LOGS      console operation log           logs/operaciones.jsonl
#
# Requirements: the test VM is the current target and answers over SSH with KEY,
# aplicaciones/bookzilla/ holds the Linux Bookzilla binary and aplicaciones/fecha/
# holds fecha.sh. Take a snapshot of the VM first: the run installs packages,
# creates and deletes users and changes /etc/issue (it restores the host name and
# /etc/issue at the end).
set -u

CONSOLA=${CONSOLA:-http://127.0.0.1:8090}
VM_IP=${VM_IP:-192.168.40.57}
KEY=${KEY:-keys/consola_ed25519}
LOGS=${LOGS:-logs/operaciones.jsonl}
TMP=$(mktemp -d)
exec 3>&1 # the real output, for messages printed while a check hides its own
trap 'rm -rf "$TMP"' EXIT

pass=0
fail=0
ok() { pass=$((pass + 1)); printf '  \033[32mOK\033[0m   %s\n' "$1"; }
ko() { fail=$((fail + 1)); printf '  \033[31mFALLA\033[0m %s\n' "$1"; }
check() { if eval "$2" >/dev/null 2>&1; then ok "$1"; else ko "$1"; fi; }
title() { printf '\n== %s\n' "$1"; }

# vm runs a command in the VM as root with the console key. ssh joins its arguments into
# one line for the remote shell, so a command with quoted words goes as one string.
# If SSH itself fails (exit 255) the run stops: otherwise every "! vm ..." check would
# pass on an unreachable VM.
vm() {
	ssh -i "$KEY" -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=10 "root@$VM_IP" "$@" 2>/dev/null
	local code=$?
	[ "$code" -eq 255 ] && kill -TERM $$
	return "$code"
}
trap 'printf "\n\033[31mSSH a root@%s con %s falló: la prueba se detiene.\033[0m\n" "$VM_IP" "$KEY" >&3; exit 3' TERM

# post sends a form like the browser. It prints the HTTP status and keeps the
# Location header in $TMP/location.
post() {
	local path=$1
	shift
	local args=()
	for kv in "$@"; do args+=(--data-urlencode "$kv"); done
	curl -s -o "$TMP/body" -D "$TMP/headers" -w '%{http_code}' -X POST \
		-H "Origin: $CONSOLA" -H 'Sec-Fetch-Site: same-origin' ${args[@]+"${args[@]}"} "$CONSOLA$path"
	grep -i '^location:' "$TMP/headers" | tr -d '\r' | cut -d' ' -f2 >"$TMP/location"
}

# last_ok reports whether the newest operation in the log succeeded and prints its message.
last_ok() {
	python3 -I -c 'import json,sys
e=json.loads(open(sys.argv[1]).read().splitlines()[-1])
print(e.get("message",""))
sys.exit(0 if e.get("ok") else 1)' "$LOGS"
}

# last_channel prints the channel of the newest operation in the log.
last_channel() {
	python3 -I -c 'import json,sys; print(json.loads(open(sys.argv[1]).read().splitlines()[-1]).get("channel",""))' "$LOGS"
}

# op posts a form, expects the redirect to a result and that the operation succeeded.
op() {
	local name=$1
	shift
	local code
	code=$(post "$@")
	local msg
	if [ "$code" = 303 ] && msg=$(last_ok); then ok "$name — $msg"; else ko "$name (HTTP $code) — $(last_ok 2>/dev/null)"; fi
}

# rejected posts a form that must be refused next to the field, without running anything.
rejected() {
	local name=$1
	shift
	local before after code
	before=$(wc -l <"$LOGS")
	code=$(post "$@")
	after=$(wc -l <"$LOGS")
	if [ "$code" = 422 ] && [ "$before" = "$after" ]; then ok "$name (rechazado, nada ejecutado)"; else ko "$name (HTTP $code, operaciones nuevas: $((after - before)))"; fi
}

title "0. Requisitos"
check "La consola responde en $CONSOLA" "curl -sf $CONSOLA/ -o /dev/null"
check "La VM $VM_IP responde por SSH con la llave de la consola" "vm true"
check "aplicaciones/bookzilla/bookzilla existe" "test -f aplicaciones/bookzilla/bookzilla"
check "aplicaciones/fecha/fecha.sh existe" "test -f aplicaciones/fecha/fecha.sh"
if [ "$fail" -gt 0 ]; then
	printf '\nFaltan requisitos: la prueba se detiene.\n'
	exit 2
fi
HOST_ANTES=$(vm hostname)

title "1. Canal: Probar conexión y Conectar por SSH (Guest Additions → SSH)"
op "Probar conexión" /diagnostico volver=/
check "Primer contacto por Guest Additions" "[ \"\$(last_channel)\" = 'Guest Additions' ]"
op "Conectar por SSH (bootstrap con la llave de la consola)" /usuarios/conectar-ssh
op "Probar conexión otra vez" /diagnostico volver=/
check "Desde ahora las operaciones van por SSH" "[ \"\$(last_channel)\" = SSH ]"

title "2. Validación antes de ejecutar"
rejected "Paquete «tree; rm -rf /»" /preparacion/instalar "paquete=tree; rm -rf /"
rejected "Hostname «Mi_Servidor»" /preparacion/hostname hostname=Mi_Servidor
rejected "Eliminar openssh-server (canal de la consola)" /preparacion/eliminar paquete=openssh-server
rejected "Servicio con inyección en el ejecutable" /servicios/desplegar aplicacion=bookzilla servicio=bookzilla "ejecutable=x'; rm -rf / #"
rejected "Acción systemd fuera de las 6" /servicios/accion servicio-accion=cron accion=mask

title "3.1.1 Instalar y eliminar aplicaciones"
vm apt-get remove -y tree >/dev/null 2>&1
check "Antes: tree no está" "! vm which tree"
op "Instalar tree" /preparacion/instalar paquete=tree
check "Después: which tree lo encuentra" "vm which tree"
op "Eliminar tree" /preparacion/eliminar paquete=tree
check "Después: tree ya no está" "! vm which tree"
post /preparacion/instalar paquete=paquete-que-no-existe >/dev/null
check "Paquete inexistente: falla con mensaje claro" "! last_ok && last_ok | grep -q 'no existe'"

title "3.1.2 Llaves de root y descarga"
code=$(post /usuarios/llaves-root)
loc=$(cat "$TMP/location")
token=$(printf '%s' "$loc" | sed -n 's#.*/usuarios/llaves/listo/\([^?]*\).*#\1#p')
if [ "$code" = 303 ] && [ -n "$token" ]; then ok "Generar llaves de root (página de descarga)"; else ko "Generar llaves de root (HTTP $code, Location $loc)"; fi
check "La página de la llave recomienda su uso correcto (chmod 600)" "curl -s '$CONSOLA$loc' | grep -q 'chmod 600'"
curl -s -o "$TMP/root_key" "$CONSOLA/usuarios/llaves/$token"
chmod 600 "$TMP/root_key"
check "La llave descargada es una llave privada" "grep -q 'PRIVATE KEY' $TMP/root_key"
check "La descarga es de una sola vez" "! curl -sf -o /dev/null '$CONSOLA/usuarios/llaves/$token'"
check "Con la llave descargada se entra como root" "ssh -i $TMP/root_key -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null root@$VM_IP true"
check "sshd: root solo con llave (prohibit-password)" "vm sshd -T | grep -qi '^permitrootlogin without-password\|^permitrootlogin prohibit-password'"

title "3.1.3 Mensaje de entrada /etc/issue"
op "Instalar mensaje con hostname, IP, fecha y hora" /preparacion/issue dato-equipo=on dato-ip=on dato-fecha=on dato-hora=on "mensaje=Prueba completa - Computacion en la Nube"
check "/etc/issue tiene el mensaje y los códigos \\n \\4 \\d \\t" "vm \"grep -q 'Prueba completa' /etc/issue && grep -qF '\\\\4' /etc/issue\""
op "Restaurar el mensaje original" /preparacion/issue/restaurar
check "/etc/issue volvió al original" "! vm \"grep -q 'Prueba completa' /etc/issue\""

title "3.1.4 Nombre de host y /etc/hosts"
op "Cambiar nombre a prueba-completa" /preparacion/hostname hostname=prueba-completa
check "hostname = prueba-completa" "[ \"\$(vm hostname)\" = prueba-completa ]"
check "/etc/hosts: 127.0.1.1 prueba-completa" "vm \"grep -Eq '^127[.]0[.]1[.]1[[:space:]]+prueba-completa' /etc/hosts\""
op "Volver al nombre $HOST_ANTES" /preparacion/hostname "hostname=$HOST_ANTES"

title "3.2.1 Crear usuario con sudo y llave"
vm userdel -r prueba1 >/dev/null 2>&1
code=$(post /usuarios/crear usuario=prueba1 contrasena=Clave-Segura-2026 sudo=on llave=on)
loc=$(cat "$TMP/location")
token=$(printf '%s' "$loc" | sed -n 's#.*/usuarios/llaves/listo/\([^?]*\).*#\1#p')
if [ "$code" = 303 ] && [ -n "$token" ]; then ok "Crear prueba1 (sudo + llave)"; else ko "Crear prueba1 (HTTP $code, Location $loc)"; fi
curl -s -o "$TMP/u_key" "$CONSOLA/usuarios/llaves/$token"
chmod 600 "$TMP/u_key"
check "prueba1 está en el grupo sudo" "vm id prueba1 | grep -q '(sudo)'"
check "prueba1 entra con su llave" "ssh -i $TMP/u_key -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null prueba1@$VM_IP true"
check "sshd: prueba1 sin contraseña (passwordauthentication no)" "vm sshd -T -C user=prueba1,host=x,addr=127.0.0.1 | grep -qi '^passwordauthentication no'"

title "3.2.2 Eliminar usuarios, con y sin archivos"
# The SSH login above leaves the user's session for a few seconds; userdel refuses
# while it runs (the console says so), so wait for it to end, as an admin would.
for _ in $(seq 1 20); do vm "pgrep -u prueba1" >/dev/null || break; sleep 1; done
op "Eliminar prueba1 sin borrar archivos" /usuarios/eliminar usuario=prueba1
check "prueba1 ya no existe y /home/prueba1 sigue" "! vm getent passwd prueba1 && vm test -d /home/prueba1"
check "Se borró el drop-in de sshd de prueba1" "! vm ls /etc/ssh/sshd_config.d/ | grep -q prueba1"
vm rm -rf /home/prueba1
vm userdel -r prueba2 >/dev/null 2>&1
code=$(post /usuarios/crear usuario=prueba2 contrasena=Clave-Segura-2026)
check "Crear prueba2 (sin sudo ni llave)" "[ $code = 303 ] && vm getent passwd prueba2"
op "Eliminar prueba2 borrando sus archivos" /usuarios/eliminar usuario=prueba2 archivos=on
check "prueba2 y /home/prueba2 ya no existen" "! vm getent passwd prueba2 && ! vm test -d /home/prueba2"
rejected "Eliminar root" /usuarios/eliminar usuario=root

title "3.3 systemd: desplegar Bookzilla y los 6 botones"
vm "systemctl disable --now bookzilla fecha; rm -rf /opt/bookzilla /opt/fecha /etc/systemd/system/bookzilla.service /etc/systemd/system/fecha.service; systemctl daemon-reload" >/dev/null 2>&1
check "Antes: /opt/bookzilla no existe" "! vm test -e /opt/bookzilla"
op "Desplegar bookzilla (primer despliegue)" /servicios/desplegar aplicacion=bookzilla servicio=bookzilla ejecutable=bookzilla puerto=8080
check "El binario está en /opt/bookzilla, de root y ejecutable" "vm 'test -x /opt/bookzilla/bookzilla && [ \"\$(stat -c %U /opt/bookzilla/bookzilla)\" = root ]'"
check "La unidad existe y está habilitada" "[ \"\$(vm systemctl is-enabled bookzilla)\" = enabled ]"
op "start" /servicios/accion servicio-accion=bookzilla accion=start
sleep 2
check "Corre como el usuario bookzilla" "[ \"\$(vm ps -o user:32= -C bookzilla | head -1)\" = bookzilla ]"
check "Bookzilla responde en http://$VM_IP:8080 con su catálogo" "curl -sf http://$VM_IP:8080/ | grep -qi 'bookzilla'"
op "status (en ejecución)" /servicios/accion servicio-accion=bookzilla accion=status
op "stop" /servicios/accion servicio-accion=bookzilla accion=stop
op "status de un servicio detenido no es un error" /servicios/accion servicio-accion=bookzilla accion=status
check "El mensaje dice que está detenido" "last_ok | grep -q 'detenido'"
op "restart" /servicios/accion servicio-accion=bookzilla accion=restart
check "Después de restart está activo" "[ \"\$(vm systemctl is-active bookzilla)\" = active ]"
op "disable" /servicios/accion servicio-accion=bookzilla accion=disable
check "is-enabled = disabled" "[ \"\$(vm systemctl is-enabled bookzilla)\" = disabled ]"
op "enable" /servicios/accion servicio-accion=bookzilla accion=enable
check "is-enabled = enabled" "[ \"\$(vm systemctl is-enabled bookzilla)\" = enabled ]"
op "Volver a desplegar bookzilla (reinicia el que corre)" /servicios/desplegar aplicacion=bookzilla servicio=bookzilla ejecutable=bookzilla puerto=8080
sleep 2
check "Sigue respondiendo después del nuevo despliegue" "curl -sf http://$VM_IP:8080/ -o /dev/null"
rejected "stop sobre ssh (protegido)" /servicios/accion servicio-accion=ssh accion=stop

title "3.3 systemd: script de fecha como oneshot"
vm rm -f /var/log/consola-fecha.log
op "Desplegar fecha como tarea única" /servicios/desplegar aplicacion=fecha servicio=fecha ejecutable=fecha.sh oneshot=on
op "start (1)" /servicios/accion servicio-accion=fecha accion=start
op "start (2)" /servicios/accion servicio-accion=fecha accion=start
check "Cada start agregó una línea (2 en total)" "[ \"\$(vm 'wc -l < /var/log/consola-fecha.log')\" = 2 ]"

title "3.4 HAProxy: agregar y quitar servidores"
vm "systemctl stop haproxy; apt-get purge -y haproxy; rm -rf /etc/haproxy" >/dev/null 2>&1
op "Instalar haproxy con la consola (configuración predeterminada)" /preparacion/instalar paquete=haproxy
check "haproxy.cfg predeterminado: sin frontend ni backend" "! vm \"grep -Eq '^(frontend|backend)' /etc/haproxy/haproxy.cfg\""
op "Agregar $VM_IP:8080 al back-end web_servers (crea frontend y backend)" /balanceador/agregar backend=web_servers ip=$VM_IP puerto=8080
check "haproxy.cfg tiene el servidor" "vm \"grep -q 'server srv-${VM_IP//./-}-8080 $VM_IP:8080 check' /etc/haproxy/haproxy.cfg\""
sleep 2
check "El balanceador (puerto 80) entrega Bookzilla" "curl -sf http://$VM_IP/ | grep -qi 'bookzilla'"
op "Agregar 127.0.0.1:8080 (segundo servidor)" /balanceador/agregar backend=web_servers ip=127.0.0.1 puerto=8080
post /balanceador/agregar backend=web_servers ip=127.0.0.1 puerto=8080 >/dev/null
check "Agregar uno repetido falla y no cambia nada" "! last_ok && last_ok | grep -q 'ya está'"
op "Quitar $VM_IP" /balanceador/quitar backend-quitar=web_servers ip-quitar=$VM_IP
check "haproxy.cfg ya no tiene $VM_IP y guarda la copia anterior" "! vm \"grep -q 'server srv-${VM_IP//./-}-' /etc/haproxy/haproxy.cfg\" && vm test -f /etc/haproxy/haproxy.cfg.consola-anterior"
check "El balanceador sigue respondiendo con el servidor que queda" "curl -sf http://$VM_IP/ -o /dev/null"
post /balanceador/quitar backend-quitar=web_servers ip-quitar=127.0.0.1 >/dev/null
check "Quitar el último servidor es rechazado (evita 503)" "! last_ok && last_ok | grep -q 'único servidor'"

title "Registro de operaciones"
check "logs/operaciones.jsonl solo lo lee el usuario (600)" "[ \"\$(stat -c %a $LOGS)\" = 600 ]"

printf '\nResultado: %d OK, %d FALLA\n' "$pass" "$fail"
[ "$fail" -eq 0 ]

package llaves

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// NombreDropInRoot is the drop-in file the console drops into
// /etc/ssh/sshd_config.d/ so Debian's default sshd lets root log in with a key
// while still refusing password logins.
const NombreDropInRoot = "10-consola-root.conf"

// ContenidoDropInRoot returns the exact lines of that drop-in: root may log in
// only with a key (prohibit-password), and the file ends with a newline the
// way sshd.d fragments are expected to.
func ContenidoDropInRoot() string {
	return "PermitRootLogin prohibit-password\n"
}

// reTokenDescarga accepts only the download tokens the console generates:
// "32 lowercase hex characters". Because a token names one file under
// keys/descargas, refusing every other shape guarantees a requested download
// can never point elsewhere on the host.
var reTokenDescarga = regexp.MustCompile(`^[0-9a-f]{32}$`)

// DownloadExpiry is how long a generated private key waits in keys/descargas
// before the next download attempt refuses it instead of serving stale key
// bytes.
const DownloadExpiry = 15 * time.Minute

// DownloadDir returns the directory under Dir where one-time downloads live:
// keys/descargas.
func DownloadDir() string {
	return filepath.Join(Dir, "descargas")
}

// NewDownloadToken returns a fresh 128-bit random token for naming one
// private-key download, in the same shape reTokenDescarga accepts.
func NewDownloadToken() (string, error) {
	vale := make([]byte, 16)
	if _, err := rand.Read(vale); err != nil {
		return "", fmt.Errorf("no se pudo generar el token de descarga: %w", err)
	}
	return hex.EncodeToString(vale), nil
}

// DownloadPath returns the local path of a one-time download, refusing tokens
// outside the generated form. Tokens are validated here so every caller --
// serving, deleting, reading -- reaches only files named with a real token.
func DownloadPath(token string) (string, error) {
	if !reTokenDescarga.MatchString(token) {
		return "", fmt.Errorf("el token de descarga no es válido")
	}
	return filepath.Join(DownloadDir(), token), nil
}

// PrepareDownload creates the local file that will hold a recovered private
// key and returns its path. The keys/descargas directory is created with mode
// 0700 and the file itself is pre-created empty with mode 0600 (kept after the
// copy lands), so a half-finished copy never leaves readable key bytes. A
// ".borrando" leftover from an interrupted download is also cleaned here.
func PrepareDownload(token string) (string, error) {
	ruta, err := DownloadPath(token)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(DownloadDir(), 0o700); err != nil {
		return "", fmt.Errorf("no se pudo preparar la carpeta de descargas: %w", err)
	}
	_ = os.Remove(ruta + ".borrando")
	f, err := os.OpenFile(ruta, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", fmt.Errorf("no se pudo preparar la descarga %s: %w", ruta, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("no se pudo cerrar la descarga %s: %w", ruta, err)
	}
	return ruta, nil
}

// ReadDeleteDownload returns the private key the token named and makes sure it
// can be served exactly once. The file is first renamed out of its token name
// (a second reader then sees nothing, on any platform), then read, then
// deleted. Downloads older than DownloadExpiry are refused and their file is
// deleted.
func ReadDeleteDownload(token string) ([]byte, error) {
	ruta, err := DownloadPath(token)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(ruta)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("la llave privada ya no está disponible (se descargó ya, caducó o nunca se generó)")
	}
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer la descarga: %w", err)
	}
	if time.Since(info.ModTime()) > DownloadExpiry {
		_ = os.Remove(ruta)
		return nil, fmt.Errorf("la descarga caducó: descarga tus llaves otra vez para recibir una nueva")
	}
	porBorrar := ruta + ".borrando"
	_ = os.Remove(porBorrar)
	if err := os.Rename(ruta, porBorrar); err != nil {
		return nil, fmt.Errorf("no se pudo apartar la llave para una sola descarga: %w", err)
	}
	data, err := os.ReadFile(porBorrar)
	if err != nil {
		_ = os.Remove(porBorrar)
		return nil, fmt.Errorf("no se pudo leer la llave privada: %w", err)
	}
	if err := os.Remove(porBorrar); err != nil {
		return nil, fmt.Errorf("no se pudo borrar la llave privada después de leerla; genera las llaves otra vez y borra %s a mano: %w", porBorrar, err)
	}
	return data, nil
}

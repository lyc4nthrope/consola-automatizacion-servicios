package servicios

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Empaquetar comprime el directorio dir en formato .tar.gz escribiendo en w.
func Empaquetar(dir string, w io.Writer) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("no se puede acceder al directorio: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("la ruta no es un directorio: %s", dir)
	}

	gw := gzip.NewWriter(w)
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}

		if relPath == "." {
			return nil
		}

		relPath = filepath.ToSlash(relPath)

		if strings.HasPrefix(relPath, "..") || strings.Contains(relPath, "/../") {
			return fmt.Errorf("ruta invalida o peligrosa: %s", relPath)
		}

		// Omitir enlaces simbólicos por seguridad
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}

		header, err := tar.FileInfoHeader(info, info.Name())
		if err != nil {
			return err
		}

		header.Name = relPath

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = io.Copy(tw, file)
		return err
	})
}

// Aplicaciones lista las carpetas disponibles dentro del directorio raiz de aplicaciones.
func Aplicaciones(raiz string) ([]string, error) {
	entries, err := os.ReadDir(raiz)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}

	var apps []string
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			apps = append(apps, entry.Name())
		}
	}
	return apps, nil
}

package servicios_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/lyc4nthrope/consola-automatizacion-servicios/internal/servicios"
)

func TestEmpaquetarYAplicaciones(t *testing.T) {
	tempDir := t.TempDir()

	appDir := filepath.Join(tempDir, "bookzilla")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatalf("error creando directorio de prueba: %v", err)
	}

	binario := filepath.Join(appDir, "bookzilla")
	if err := os.WriteFile(binario, []byte("#!/bin/sh\necho ok"), 0755); err != nil {
		t.Fatalf("error creando ejecutable: %v", err)
	}

	apps, err := servicios.Aplicaciones(tempDir)
	if err != nil {
		t.Fatalf("error al listar aplicaciones: %v", err)
	}
	if len(apps) != 1 || apps[0] != "bookzilla" {
		t.Fatalf("se esperaba ['bookzilla'], se obtuvo: %v", apps)
	}

	var buf bytes.Buffer
	if err := servicios.Empaquetar(appDir, &buf); err != nil {
		t.Fatalf("error al empaquetar: %v", err)
	}

	gr, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatalf("error al leer gzip: %v", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	archivos := make(map[string]bool)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("error leyendo tar: %v", err)
		}
		archivos[header.Name] = true
	}

	if !archivos["bookzilla"] {
		t.Fatalf("el ejecutable no está dentro del archivo comprimido: %v", archivos)
	}
}

package servicios

import (
	"strings"
	"testing"
)

func TestUnitFileForAService(t *testing.T) {
	got := UnitFile(Config{Name: "bookzilla", Description: "Bookzilla", Exec: "/opt/bookzilla/bookzilla", Port: "8080"})
	for _, want := range []string{
		"Description=Bookzilla (desplegado por la consola)\n",
		"User=bookzilla\nGroup=bookzilla\n",
		"WorkingDirectory=/var/lib/bookzilla\n",
		"ExecStart=/opt/bookzilla/bookzilla\n",
		"Environment=PORT=8080\n",
		"Restart=on-failure\n",
		"WantedBy=multi-user.target\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("unit lacks %q:\n%s", want, got)
		}
	}
}

func TestUnitFileForOneshot(t *testing.T) {
	got := UnitFile(Config{Name: "fecha", Description: "Fecha", Exec: "/opt/fecha/fecha.sh", Oneshot: true})
	if !strings.Contains(got, "Type=oneshot\nExecStart=/opt/fecha/fecha.sh\n") || strings.Contains(got, "User=") {
		t.Fatalf("unit:\n%s", got)
	}
}

func TestValidation(t *testing.T) {
	for _, ok := range []string{"bookzilla", "fecha", "mi-app_2"} {
		if err := ValidateName(ok); err != nil {
			t.Errorf("name %q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Bookzilla", "1app", "ssh", "systemd-x", "a b", "x;reboot"} {
		if ValidateName(bad) == nil {
			t.Errorf("name %q accepted", bad)
		}
	}
	for _, ok := range []string{"/opt/bookzilla/bookzilla", "/usr/bin/python3", "/opt/fecha/fecha.sh"} {
		if err := ValidateExec(ok); err != nil {
			t.Errorf("exec %q: %v", ok, err)
		}
	}
	// Each of these could inject a second line or a shell command into the unit.
	for _, bad := range []string{"", "bookzilla", "/opt/a b", "/opt/x'; rm -rf / #", "/opt/x\nExecStartPre=/bin/sh", "/opt/$HOME", "/opt/%h", "/opt/../etc/x", "/opt/"} {
		if ValidateExec(bad) == nil {
			t.Errorf("exec %q accepted", bad)
		}
	}
	if ValidateDescription("Bookzilla (v2)") != nil || ValidateDescription("x\n[Service]") == nil || ValidateDescription("") == nil {
		t.Error("description rules")
	}
}

func TestProtectedAndActions(t *testing.T) {
	for _, p := range []string{"ssh", "ssh.service", "systemd-logind", "getty@tty1", "haproxy"} {
		if !IsProtected(p) {
			t.Errorf("%q not protected", p)
		}
	}
	if IsProtected("bookzilla") || len(Actions) != 6 || Actions["reload"] {
		t.Error("protection or actions list")
	}
}

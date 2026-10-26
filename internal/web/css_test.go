package web

import (
	"io/fs"
	"regexp"
	"testing"

	assets "github.com/lyc4nthrope/consola-automatizacion-servicios/web"
)

// An animation named like a keyword of the animation shorthand ("running", "infinite",
// "alternate"...) is read as that keyword, so it never runs: the progress bar was named
// "running" and never moved.
func TestAnimationNamesAreNotCSSKeywords(t *testing.T) {
	css, err := fs.ReadFile(assets.Static, "static/css/consola.css")
	if err != nil {
		t.Fatal(err)
	}
	keywords := map[string]bool{
		"running": true, "paused": true, "infinite": true, "normal": true, "reverse": true,
		"alternate": true, "alternate-reverse": true, "none": true, "forwards": true,
		"backwards": true, "both": true, "ease": true, "ease-in": true, "ease-out": true,
		"ease-in-out": true, "linear": true, "step-start": true, "step-end": true,
		"initial": true, "inherit": true, "unset": true, "revert": true,
	}
	names := regexp.MustCompile(`@keyframes\s+([A-Za-z0-9_-]+)`).FindAllStringSubmatch(string(css), -1)
	if len(names) == 0 {
		t.Fatal("no @keyframes found; the test is not reading the stylesheet")
	}
	for _, n := range names {
		if keywords[n[1]] {
			t.Errorf("@keyframes %s: the name is a CSS keyword, so the animation never runs", n[1])
		}
	}
}

// Bootstrap's reboot gives every <legend> a heading size and float: left. The legend of the
// /etc/issue checkboxes is a field label: same size as the others and on its own row.
func TestIssueLegendLooksLikeAFieldLabel(t *testing.T) {
	css, err := fs.ReadFile(assets.Static, "static/css/consola.css")
	if err != nil {
		t.Fatal(err)
	}
	rule := regexp.MustCompile(`\.issue-datos legend \{([^}]*)\}`).FindStringSubmatch(string(css))
	if rule == nil {
		t.Fatal("no .issue-datos legend rule")
	}
	for _, want := range []string{"font-size: inherit;", "float: none;", "grid-column: 1 / -1;"} {
		if !regexp.MustCompile(regexp.QuoteMeta(want)).MatchString(rule[1]) {
			t.Errorf(".issue-datos legend lacks %q", want)
		}
	}
}

package web

import (
	"strings"
	"testing"
)

// The served apps' stylesheet is cut from the workbench page by its section
// comments; renaming one must fail here, not as an unstyled app shell.
func TestGuiCSS(t *testing.T) {
	css, err := GuiCSS()
	if err != nil {
		t.Fatal(err)
	}
	s := string(css)
	for _, want := range []string{"--sig:", ".btn", ".kchip", ".g-app", ".g-result", ".g-chunk", ".g-answer"} {
		if !strings.Contains(s, want) {
			t.Errorf("gui.css lacks %q", want)
		}
	}
	for _, not := range []string{".hist li", "/* app frame */", "</style>"} {
		if strings.Contains(s, not) {
			t.Errorf("gui.css includes %q (page-only CSS)", not)
		}
	}
}

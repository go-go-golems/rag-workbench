// Package web embeds the browser assets: the ported workbench page and the
// window.claude runtime shim.
package web

import (
	"embed"
	"fmt"
	"strings"
)

// Workbench holds workbench/{index.html, app.js, agent.js, claude.js}.
//
//go:embed workbench
var Workbench embed.FS

// AppShell holds app-shell/{index.html, shell.js}: the page `ragwb serve` mounts an
// app's gui in, with a host.rag backed by the server's session (RemoteRag).
//
//go:embed app-shell
var AppShell embed.FS

// GuiCSS returns the stylesheet the gui components need, cut from the workbench
// page so both pages share one source: the base section (tokens, buttons,
// chips) up to "/* app frame */" plus "/* generative UI */" up to "/* agent */".
func GuiCSS() ([]byte, error) {
	b, err := Workbench.ReadFile("workbench/index.html")
	if err != nil {
		return nil, err
	}
	s := string(b)
	open := strings.Index(s, "\n<style>\n")
	if open < 0 {
		return nil, fmt.Errorf("gui css: no <style> block in workbench/index.html")
	}
	s = s[open+len("\n<style>\n"):]
	if end := strings.Index(s, "</style>"); end >= 0 {
		s = s[:end]
	}
	cut := func(from, to string) (string, error) {
		i := 0
		if from != "" {
			if i = strings.Index(s, from); i < 0 {
				return "", fmt.Errorf("gui css: marker %q not found", from)
			}
		}
		j := strings.Index(s[i:], to)
		if j < 0 {
			return "", fmt.Errorf("gui css: marker %q not found", to)
		}
		return s[i : i+j], nil
	}
	base, err := cut("", "/* app frame */")
	if err != nil {
		return nil, err
	}
	gui, err := cut("/* generative UI */", "/* agent */")
	if err != nil {
		return nil, err
	}
	return []byte(base + "\n" + gui), nil
}

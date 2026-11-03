// Package web embeds the browser assets: the ported workbench page and the
// window.claude runtime shim.
package web

import "embed"

// Workbench holds workbench/{index.html, app.js, agent.js, claude.js}.
//
//go:embed workbench
var Workbench embed.FS

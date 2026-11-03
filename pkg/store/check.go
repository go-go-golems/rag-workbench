package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"

	"github.com/dop251/goja"
	"github.com/go-go-golems/go-go-goja/pkg/jsverbs"
)

// VerbScanOptions are the jsverbs options for an app directory: workbench
// scripts (*.wb.js) are run by `ragwb run`, not scanned for verbs.
func VerbScanOptions() jsverbs.ScanOptions {
	o := jsverbs.DefaultScanOptions()
	o.Exclude = append(o.Exclude, "**/*.wb.js")
	return o
}

// Problem is one finding about a saved file.
type Problem struct {
	Path     string `json:"path"`
	Line     int    `json:"line,omitempty"`
	Severity string `json:"severity"` // error | warning
	Message  string `json:"message"`
}

// Verb is one command jsverbs found in the app.
type Verb struct {
	Name   string `json:"name"`   // full command path, e.g. "notes search"
	Source string `json:"source"` // file#function
}

// CheckResult says whether a saved file parses and how to run it.
type CheckResult struct {
	Role     string    `json:"role"`
	OK       bool      `json:"ok"`
	Problems []Problem `json:"problems"`
	Verbs    []Verb    `json:"verbs,omitempty"` // every verb in the app (after this save)
	Run      []string  `json:"run,omitempty"`   // commands that run this file
}

var lineRE = regexp.MustCompile(`Line (\d+):(\d+)`)

// syntaxProblem compiles src the way it will run and reports a syntax error, if any.
// Workbench scripts are async function bodies (runner.workbenchHarness); .js files are
// CommonJS modules; .json must parse.
func syntaxProblem(p, role string, src []byte) *Problem {
	switch role {
	case "workbench":
		_, err := goja.Compile(p, "(async function (rag, gui, log, kit) {\"use strict\";\n"+string(src)+"\n})", false)
		return jsProblem(p, err, 1)
	case "verbs", "library":
		_, err := goja.Compile(p, "(function (exports, require, module, __filename, __dirname) {"+string(src)+"\n})", false)
		return jsProblem(p, err, 0)
	case "data":
		if filepath.Ext(p) == ".json" && !json.Valid(src) {
			var v any
			err := json.Unmarshal(src, &v)
			return &Problem{Path: p, Severity: "error", Message: "invalid JSON: " + err.Error()}
		}
	}
	return nil
}

func jsProblem(p string, err error, lineOffset int) *Problem {
	if err == nil {
		return nil
	}
	pr := &Problem{Path: p, Severity: "error", Message: err.Error()}
	var se *goja.CompilerSyntaxError
	if errors.As(err, &se) {
		pr.Message = se.Message
	}
	if m := lineRE.FindStringSubmatch(err.Error()); m != nil {
		if n, _ := strconv.Atoi(m[1]); n-lineOffset > 0 {
			pr.Line = n - lineOffset
		}
	}
	return pr
}

// Check reports on a file that was just saved: its syntax, the app's verbs as
// jsverbs sees them (bad __verb__ metadata surfaces here), and how to run it.
func (a *Apps) Check(app, p string, src []byte) CheckResult {
	role := Role(p)
	res := CheckResult{Role: role, OK: true, Problems: []Problem{}}
	if pr := syntaxProblem(p, role, src); pr != nil {
		res.Problems = append(res.Problems, *pr)
	}
	dir, err := a.AppDir(app)
	if err != nil {
		res.Problems = append(res.Problems, Problem{Path: p, Severity: "error", Message: err.Error()})
		res.OK = false
		return res
	}
	switch role {
	case "workbench":
		res.Run = []string{fmt.Sprintf("ragwb run %s", filepath.Join(dir, filepath.FromSlash(p)))}
	case "verbs", "library":
		reg, err := jsverbs.ScanDir(dir, VerbScanOptions())
		var se *jsverbs.ScanError
		switch {
		case errors.As(err, &se):
			for _, d := range se.Diagnostics {
				res.Problems = append(res.Problems, Problem{Path: d.Path, Severity: string(d.Severity), Message: diagMessage(d)})
			}
		case err != nil:
			res.Problems = append(res.Problems, Problem{Path: p, Severity: "error", Message: "jsverbs: " + err.Error()})
		default:
			for _, v := range reg.Verbs() {
				res.Verbs = append(res.Verbs, Verb{Name: v.FullPath(), Source: v.SourceRef()})
				if role == "verbs" && v.File != nil && filepath.ToSlash(v.File.RelPath) == p {
					res.Run = append(res.Run, fmt.Sprintf("ragwb verbs --dir %s %s", dir, v.FullPath()))
				}
			}
			for _, d := range reg.ErrorDiagnostics() {
				res.Problems = append(res.Problems, Problem{Path: d.Path, Severity: string(d.Severity), Message: diagMessage(d)})
			}
			sort.Slice(res.Verbs, func(i, j int) bool { return res.Verbs[i].Name < res.Verbs[j].Name })
		}
	}
	for _, pr := range res.Problems {
		if pr.Severity == "error" {
			res.OK = false
		}
	}
	return res
}

func diagMessage(d jsverbs.Diagnostic) string {
	if d.Symbol != "" {
		return d.Symbol + ": " + d.Message
	}
	return d.Message
}

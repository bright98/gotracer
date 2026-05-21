package report

import (
	"html/template"
	"io"
	"time"

	"github.com/bright98/gotracer/findings"
)

// Meta holds trace-level metadata shown in the HTML report header.
type Meta struct {
	Source     string        // URL or file path
	CapturedAt time.Time     // zero for file analysis
	Duration   time.Duration // 0 for file analysis
	RuleCount  int
}

type htmlData struct {
	Meta       Meta
	Findings   []findings.Finding
	ErrorCount int
	WarnCount  int
	InfoCount  int
}

// WriteHTML writes a self-contained HTML report to w.
func WriteHTML(w io.Writer, fs []findings.Finding, meta Meta) error {
	d := htmlData{Meta: meta, Findings: fs}
	for _, f := range fs {
		switch f.Severity {
		case findings.Error:
			d.ErrorCount++
		case findings.Warn:
			d.WarnCount++
		default:
			d.InfoCount++
		}
	}
	return htmlTmpl.Execute(w, d)
}

var htmlTmpl = template.Must(template.New("report").Funcs(template.FuncMap{
	"sevClass": func(s findings.Severity) string {
		switch s {
		case findings.Error:
			return "error"
		case findings.Warn:
			return "warn"
		default:
			return "info"
		}
	},
	"roundDur": func(d time.Duration) string {
		return d.Round(time.Millisecond).String()
	},
	"formatTime": func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.UTC().Format("2006-01-02 15:04:05 UTC")
	},
}).Parse(htmlTmplStr))

const htmlTmplStr = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>gotracer report</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:ui-monospace,'Cascadia Code','Source Code Pro',Menlo,monospace;background:#0d1117;color:#c9d1d9;padding:2rem;font-size:14px;line-height:1.5}
h1{font-size:1.4rem;color:#e6edf3;margin-bottom:.75rem}
.meta{color:#8b949e;margin-bottom:1.5rem;font-size:.9rem}
.meta span{margin-right:2rem}
.summary{display:flex;gap:.6rem;margin-bottom:1.5rem;flex-wrap:wrap}
.badge{padding:.2rem .85rem;border-radius:4px;font-weight:600;font-size:.82rem}
.b-error{background:#3d1217;color:#f85149;border:1px solid #6e1e24}
.b-warn{background:#272115;color:#e3b341;border:1px solid #5a4313}
.b-info{background:#0d2137;color:#58a6ff;border:1px solid #1a4a7a}
.b-ok{background:#0d2b1a;color:#3fb950;border:1px solid #1a5a30}
.findings{display:flex;flex-direction:column;gap:.35rem}
details{border-radius:6px;overflow:hidden;border:1px solid #30363d}
summary{padding:.5rem 1rem;cursor:pointer;display:grid;grid-template-columns:5.5rem 16rem 7rem 1fr 1.2rem;gap:.75rem;align-items:center;background:#161b22;user-select:none;list-style:none}
summary::-webkit-details-marker{display:none}
details[open] summary{background:#1c2128;border-bottom:1px solid #30363d}
.expand-icon{color:#8b949e;font-size:.8rem;transition:transform .15s}
details[open] .expand-icon{transform:rotate(90deg)}
.sev{font-weight:700;font-size:.78rem;text-align:center;padding:.15rem .3rem;border-radius:3px;letter-spacing:.04em}
.sev-error{background:#3d1217;color:#f85149;border:1px solid #6e1e24}
.sev-warn{background:#272115;color:#e3b341;border:1px solid #5a4313}
.sev-info{background:#0d2137;color:#58a6ff;border:1px solid #1a4a7a}
.rule{color:#79c0ff;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;display:flex;align-items:center;gap:.4rem}
.count{font-size:.75rem;background:#1c2b3a;color:#58a6ff;border:1px solid #1a4a7a;border-radius:3px;padding:.1rem .35rem;white-space:nowrap;flex-shrink:0}
.ts{color:#8b949e;font-size:.85rem}
.msg{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.body{padding:.85rem 1.25rem;background:#0d1117}
.body-row{display:flex;gap:.75rem;margin-bottom:.55rem}
.body-label{color:#8b949e;width:7rem;flex-shrink:0;font-size:.85rem;padding-top:.05rem}
.body-val{color:#c9d1d9;font-size:.85rem;white-space:pre-wrap;word-break:break-word}
.stack-trace{font-family:inherit;font-size:.8rem;color:#a5d6ff;background:#010409;border:1px solid #30363d;border-radius:4px;padding:.6rem .85rem;margin:0;overflow-x:auto;white-space:pre}
</style>
</head>
<body>
<h1>gotracer report</h1>
<div class="meta">
  <span>source: {{.Meta.Source}}</span>{{with formatTime .Meta.CapturedAt}}
  <span>captured: {{.}}</span>{{end}}{{if .Meta.Duration}}
  <span>duration: {{roundDur .Meta.Duration}}</span>{{end}}
  <span>rules: {{.Meta.RuleCount}}</span>
  <span>findings: {{len .Findings}}</span>
</div>
<div class="summary">
  {{if .ErrorCount}}<span class="badge b-error">{{.ErrorCount}} ERROR</span>{{end}}
  {{if .WarnCount}}<span class="badge b-warn">{{.WarnCount}} WARN</span>{{end}}
  {{if .InfoCount}}<span class="badge b-info">{{.InfoCount}} INFO</span>{{end}}
  {{if not .Findings}}<span class="badge b-ok">no findings</span>{{end}}
</div>
{{if .Findings}}<div class="findings">
  {{range .Findings}}<details>
    <summary>
      <span class="sev sev-{{sevClass .Severity}}">{{.Severity}}</span>
      <span class="rule">{{.Rule}}{{if gt .Count 1}}<span class="count">{{.Count}}×</span>{{end}}</span>
      <span class="ts">{{roundDur .Timestamp}}</span>
      <span class="msg">{{.Message}}</span>
      <span class="expand-icon">▶</span>
    </summary>
    <div class="body">
      {{if .Detail}}<div class="body-row">
        <span class="body-label">detail</span>
        <span class="body-val">{{.Detail}}</span>
      </div>{{end}}
      {{if .Suggestion}}<div class="body-row">
        <span class="body-label">suggestion</span>
        <span class="body-val">{{.Suggestion}}</span>
      </div>{{end}}
      {{if .GoroutineID}}<div class="body-row">
        <span class="body-label">goroutine</span>
        <span class="body-val">{{.GoroutineID}}</span>
      </div>{{end}}
      {{if .Stack}}<div class="body-row">
        <span class="body-label">stack</span>
        <pre class="stack-trace">{{range .Stack}}{{.}}
{{end}}</pre>
      </div>{{end}}
    </div>
  </details>
  {{end}}
</div>{{end}}
</body>
</html>`

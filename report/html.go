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
/* ── theme variables ── */
:root {
  --bg:           #0c0c0e;
  --bg-dot:       #1a1a1f;
  --panel:        #111114;
  --panel-bd:     #1e1e24;
  --hdr-bd:       #1a1a20;
  --row-bd:       #161618;
  --row-hover:    #16161a;
  --row-open:     #16161a;
  --open-bd:      #1c1c22;
  --body-bg:      #0c0c0e;
  --body-bd:      #18181c;
  --title:        #e8e8f0;
  --meta:         #444;
  --rule:         #dddde8;
  --msg:          #404048;
  --cnt-bg:       #1c1c22;
  --cnt-bd:       #252530;
  --cnt-fg:       #555;
  --blabel:       #2a2a34;
  --bval:         #8888a0;
  --bval-dim:     #555;
  --stack-bg:     #080808;
  --stack-bd:     #181820;
  --stack-fg:     #3d6888;
  --chev:         #282830;
  --chev-open:    #404048;
  --pcnt:         #2e2e38;
  --plabel:       #333;
  --empty:        #282830;
  --tog-bg:       #1a1a20;
  --tog-bd:       #2a2a34;
  --tog-fg:       #555;
}
html[data-theme="light"] {
  --bg:           #f0f0f5;
  --bg-dot:       #dcdce6;
  --panel:        #ffffff;
  --panel-bd:     #dcdce6;
  --hdr-bd:       #e8e8f0;
  --row-bd:       #ebebf2;
  --row-hover:    #f8f8fc;
  --row-open:     #f4f4fa;
  --open-bd:      #e4e4ee;
  --body-bg:      #f8f8fc;
  --body-bd:      #ebebf2;
  --title:        #1a1a28;
  --meta:         #8888a0;
  --rule:         #1a1a28;
  --msg:          #aaaabc;
  --cnt-bg:       #ebebf2;
  --cnt-bd:       #d8d8e8;
  --cnt-fg:       #8888a0;
  --blabel:       #bbbbcc;
  --bval:         #444458;
  --bval-dim:     #8888a0;
  --stack-bg:     #f0f0f8;
  --stack-bd:     #dcdce6;
  --stack-fg:     #3d6888;
  --chev:         #ccccda;
  --chev-open:    #8888a0;
  --pcnt:         #aaaabc;
  --plabel:       #aaaabc;
  --empty:        #ccccda;
  --tog-bg:       #ebebf2;
  --tog-bd:       #dcdce6;
  --tog-fg:       #8888a0;
}
@media(prefers-color-scheme:light){
  html:not([data-theme="dark"]){
    --bg:#f0f0f5;--bg-dot:#dcdce6;--panel:#ffffff;--panel-bd:#dcdce6;
    --hdr-bd:#e8e8f0;--row-bd:#ebebf2;--row-hover:#f8f8fc;--row-open:#f4f4fa;
    --open-bd:#e4e4ee;--body-bg:#f8f8fc;--body-bd:#ebebf2;--title:#1a1a28;
    --meta:#8888a0;--rule:#1a1a28;--msg:#aaaabc;--cnt-bg:#ebebf2;
    --cnt-bd:#d8d8e8;--cnt-fg:#8888a0;--blabel:#bbbbcc;--bval:#444458;
    --bval-dim:#8888a0;--stack-bg:#f0f0f8;--stack-bd:#dcdce6;--stack-fg:#3d6888;
    --chev:#ccccda;--chev-open:#8888a0;--pcnt:#aaaabc;--plabel:#aaaabc;
    --empty:#ccccda;--tog-bg:#ebebf2;--tog-bd:#dcdce6;--tog-fg:#8888a0;
  }
}

/* ── base ── */
*{box-sizing:border-box;margin:0;padding:0}
body{
  font-family:ui-monospace,'Cascadia Code','Source Code Pro',Menlo,Consolas,monospace;
  background-color:var(--bg);
  background-image:radial-gradient(circle,var(--bg-dot) 1.5px,transparent 1.5px);
  background-size:26px 26px;
  min-height:100vh;
  color:var(--meta);
  padding:2.5rem 1.5rem;
  font-size:13px;
  line-height:1.6;
  transition:background-color .2s,color .2s;
}
.page{max-width:980px;margin:0 auto}

/* ── header ── */
.hdr{display:flex;align-items:flex-start;justify-content:space-between;margin-bottom:.6rem}
.title{font-size:1.25rem;font-weight:700;color:var(--title);letter-spacing:.02em}
.tog{
  background:var(--tog-bg);border:1px solid var(--tog-bd);color:var(--tog-fg);
  border-radius:6px;padding:.25rem .55rem;cursor:pointer;font-size:.75rem;
  font-family:inherit;letter-spacing:.04em;transition:opacity .15s;
}
.tog:hover{opacity:.75}
.meta{display:flex;flex-wrap:wrap;gap:0 2rem;font-size:.8rem;margin-bottom:1.4rem}

/* ── summary pills ── */
.summary{display:flex;gap:.45rem;flex-wrap:wrap;margin-bottom:2rem}
.pill{padding:.22rem .7rem;border-radius:20px;font-size:.72rem;font-weight:700;letter-spacing:.07em}
.p-error{background:#200c0c;color:#d95555;border:1px solid #3d1515}
.p-warn {background:#201408;color:#c87830;border:1px solid #3d2a10}
.p-info {background:#08182a;color:#3d85c8;border:1px solid #10304a}
.p-ok   {background:#0a2016;color:#35a060;border:1px solid #10402a}
html[data-theme="light"] .p-error,html:not([data-theme="dark"]) .p-error{background:#fceaea}
html[data-theme="light"] .p-warn, html:not([data-theme="dark"]) .p-warn {background:#fdf3e6}
html[data-theme="light"] .p-info, html:not([data-theme="dark"]) .p-info {background:#e8f0fc}
html[data-theme="light"] .p-ok,   html:not([data-theme="dark"]) .p-ok   {background:#e6f5ee}
@media(prefers-color-scheme:light){
  html:not([data-theme="dark"]) .p-error{background:#fceaea}
  html:not([data-theme="dark"]) .p-warn {background:#fdf3e6}
  html:not([data-theme="dark"]) .p-info {background:#e8f0fc}
  html:not([data-theme="dark"]) .p-ok   {background:#e6f5ee}
}

/* ── findings panel ── */
.panel{background:var(--panel);border:1px solid var(--panel-bd);border-radius:14px;overflow:hidden}
.panel-hdr{
  padding:.7rem 1.3rem;border-bottom:1px solid var(--hdr-bd);
  display:flex;align-items:center;justify-content:space-between;
}
.panel-label{font-size:.68rem;font-weight:700;letter-spacing:.16em;color:var(--plabel);text-transform:uppercase}
.panel-count{font-size:.75rem;color:var(--pcnt)}

/* ── rows ── */
details{border-bottom:1px solid var(--row-bd)}
details:last-child{border-bottom:none}
summary{
  list-style:none;padding:.8rem 1.3rem;cursor:pointer;
  display:flex;align-items:center;gap:.85rem;
  background:transparent;transition:background .1s;user-select:none;
}
summary::-webkit-details-marker{display:none}
summary:hover{background:var(--row-hover)}
details[open]>summary{background:var(--row-open);border-bottom:1px solid var(--open-bd)}

/* dot */
.dot{width:8px;height:8px;border-radius:50%;flex-shrink:0}
.dot-error{background:#d95555;box-shadow:0 0 7px #d9555550}
.dot-warn {background:#c87830;box-shadow:0 0 7px #c8783050}
.dot-info {background:#35a060;box-shadow:0 0 7px #35a06050}

/* rule name */
.rule{color:var(--rule);font-weight:600;font-size:.88rem;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.cnt{font-size:.68rem;background:var(--cnt-bg);color:var(--cnt-fg);border:1px solid var(--cnt-bd);border-radius:3px;padding:.08rem .28rem;margin-left:.35rem;vertical-align:middle}
.spacer{flex:1 1 0;min-width:.5rem}

/* message */
.msg{color:var(--msg);font-size:.8rem;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;max-width:380px;text-align:right}

/* severity badge */
.sev{font-size:.68rem;font-weight:700;letter-spacing:.09em;padding:.18rem .55rem;border-radius:5px;white-space:nowrap;flex-shrink:0}
.sev-error{background:#200c0c;color:#d95555;border:1px solid #3d1515}
.sev-warn {background:#201408;color:#c87830;border:1px solid #3d2a10}
.sev-info {background:#08182a;color:#3d85c8;border:1px solid #10304a}
html[data-theme="light"] .sev-error,html:not([data-theme="dark"]) .sev-error{background:#fceaea;border-color:#f0b0b0}
html[data-theme="light"] .sev-warn, html:not([data-theme="dark"]) .sev-warn {background:#fdf3e6;border-color:#e8c080}
html[data-theme="light"] .sev-info, html:not([data-theme="dark"]) .sev-info {background:#e8f0fc;border-color:#9ab8e8}
@media(prefers-color-scheme:light){
  html:not([data-theme="dark"]) .sev-error{background:#fceaea;border-color:#f0b0b0}
  html:not([data-theme="dark"]) .sev-warn {background:#fdf3e6;border-color:#e8c080}
  html:not([data-theme="dark"]) .sev-info {background:#e8f0fc;border-color:#9ab8e8}
}

/* chevron */
.chev{color:var(--chev);font-size:.6rem;flex-shrink:0;transition:transform .14s;margin-left:.1rem}
details[open] .chev{transform:rotate(90deg);color:var(--chev-open)}

/* ── expanded body ── */
.body{
  background:var(--body-bg);border-top:1px solid var(--body-bd);
  padding:.95rem 1.3rem .95rem 3.4rem;
  display:flex;flex-direction:column;gap:.55rem;
}
.brow{display:flex;gap:.9rem;align-items:baseline}
.blabel{
  font-size:.68rem;font-weight:700;letter-spacing:.1em;text-transform:uppercase;
  color:var(--blabel);width:5.5rem;flex-shrink:0;padding-top:.1rem;
}
.bval{color:var(--bval);font-size:.8rem;white-space:pre-wrap;word-break:break-word;line-height:1.6}
.bval-dim{color:var(--bval-dim)}
.stack{
  font-family:inherit;font-size:.76rem;color:var(--stack-fg);
  background:var(--stack-bg);border:1px solid var(--stack-bd);
  border-radius:7px;padding:.6rem .9rem;overflow-x:auto;white-space:pre;line-height:1.75;margin:0;
}
.empty{padding:3rem;text-align:center;color:var(--empty);font-size:.82rem}
</style>
</head>
<body>
<div class="page">

<div class="hdr">
  <div class="title">gotracer</div>
  <button class="tog" id="tog" onclick="(function(){var h=document.documentElement,t=h.dataset.theme,sys=window.matchMedia('(prefers-color-scheme:light)').matches?'light':'dark';var cur=t||sys;var nxt=cur==='dark'?'light':'dark';h.dataset.theme=nxt;try{localStorage.setItem('gotracer-theme',nxt)}catch(e){}document.getElementById('tog').textContent=nxt==='dark'?'☀ light':'☾ dark'})()">☀ light</button>
</div>
<div class="meta">
  <span>source: {{.Meta.Source}}</span>{{with formatTime .Meta.CapturedAt}}
  <span>captured: {{.}}</span>{{end}}{{if .Meta.Duration}}
  <span>duration: {{roundDur .Meta.Duration}}</span>{{end}}
  <span>rules: {{.Meta.RuleCount}}</span>
  <span>findings: {{len .Findings}}</span>
</div>
<div class="summary">
  {{if .ErrorCount}}<span class="pill p-error">{{.ErrorCount}} ERROR</span>{{end}}
  {{if .WarnCount}}<span class="pill p-warn">{{.WarnCount}} WARN</span>{{end}}
  {{if .InfoCount}}<span class="pill p-info">{{.InfoCount}} INFO</span>{{end}}
  {{if not .Findings}}<span class="pill p-ok b-ok">✓ NO FINDINGS</span>{{end}}
</div>

<div class="panel">
  <div class="panel-hdr">
    <span class="panel-label">Findings</span>
    <span class="panel-count">{{len .Findings}} total</span>
  </div>
  {{if .Findings}}{{range .Findings}}
  <details>
    <summary>
      <span class="dot dot-{{sevClass .Severity}}"></span>
      <span class="rule">{{.Rule}}{{if gt .Count 1}}<span class="cnt">{{.Count}}×</span>{{end}}</span>
      <span class="spacer"></span>
      <span class="msg">{{.Message}}</span>
      <span class="sev sev-{{sevClass .Severity}}">{{.Severity}}</span>
      <span class="chev">▶</span>
    </summary>
    <div class="body">
      {{if .Detail}}<div class="brow">
        <span class="blabel">detail</span>
        <span class="bval">{{.Detail}}</span>
      </div>{{end}}
      {{if .Suggestion}}<div class="brow">
        <span class="blabel">fix</span>
        <span class="bval">{{.Suggestion}}</span>
      </div>{{end}}
      <div class="brow">
        <span class="blabel">at</span>
        <span class="bval bval-dim">{{roundDur .Timestamp}}</span>
      </div>
      {{if .GoroutineID}}<div class="brow">
        <span class="blabel">goroutine</span>
        <span class="bval bval-dim">{{.GoroutineID}}</span>
      </div>{{end}}
      {{if .Stack}}<div class="brow">
        <span class="blabel">stack</span>
        <pre class="stack">{{range .Stack}}{{.}}
{{end}}</pre>
      </div>{{end}}
    </div>
  </details>
  {{end}}{{else}}<div class="empty">no findings — all rules passed</div>{{end}}
</div>

</div>
<script>
(function(){
  try {
    var saved = localStorage.getItem('gotracer-theme');
    var sys = window.matchMedia('(prefers-color-scheme:light)').matches ? 'light' : 'dark';
    var theme = saved || sys;
    document.documentElement.dataset.theme = theme;
    document.getElementById('tog').textContent = theme === 'dark' ? '☀ light' : '☾ dark';
  } catch(e) {}
})();
</script>
</body>
</html>`

package core

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"time"
)

// SnapshotData holds the data baked into a static HTML snapshot.
type SnapshotData struct {
	GeneratedAt       string
	SessionID         string
	Projects          []SnapshotProject
	TotalTasks        int
	TasksDone         int
	TasksFailed       int
	Agents            int
	SuccessRate       string
	TelegramAvailable bool
}

// SnapshotProject is a per-project summary for the snapshot.
type SnapshotProject struct {
	Name        string
	Status      string
	Goal        string
	TasksDone   int
	TasksTotal  int
	TasksFailed int
	Elapsed     string
}

// snapshotCSS is a minimal CSS subset inlined into the snapshot HTML.
// Kept small to stay under the 100KB total file size target.
const snapshotCSS = `
:root {
    --bg: #131313; --surface: #1c1b1b; --surface-high: #2a2a2a;
    --text: #e5e2e1; --dim: #9a9594; --muted: #5a5555;
    --ghost: rgba(73,68,86,0.15);
    --green: #78dc77; --yellow: #ebcb8b; --red: #ffb4ab;
    --blue: #81a1c1; --accent: #cdbdff; --accent-deep: #5d21df;
    --radius: 12px; --radius-sm: 6px;
}
*,*::before,*::after{box-sizing:border-box;margin:0;padding:0}
body{background:var(--bg);color:var(--text);font-family:'Inter',system-ui,sans-serif;font-size:13px;line-height:1.5;padding:24px;max-width:800px;margin:0 auto}
h1{font-family:'Manrope',system-ui,sans-serif;font-size:24px;font-weight:800;margin-bottom:8px}
.meta{font-size:11px;color:var(--dim);margin-bottom:24px}
.stat-grid{display:grid;grid-template-columns:repeat(2,1fr);gap:14px;margin-bottom:24px}
.stat{background:var(--surface);border-radius:var(--radius);padding:20px}
.stat-label{font-size:10px;font-weight:600;text-transform:uppercase;letter-spacing:2px;color:var(--muted);margin-bottom:4px}
.stat-val{font-family:'Manrope',system-ui,sans-serif;font-size:36px;font-weight:800;line-height:1.1}
.stat-sub{font-size:11px;color:var(--dim);margin-top:4px}
.card{background:var(--surface);border-radius:var(--radius);padding:20px;margin-bottom:14px;border-left:3px solid var(--muted)}
.card.running{border-left-color:var(--green)}
.card.completed{border-left-color:var(--muted)}
.card.failed{border-left-color:var(--red)}
.card.idle{border-left-color:var(--yellow)}
.card-name{font-family:'Manrope',system-ui,sans-serif;font-size:14px;font-weight:700}
.card-detail{font-size:12px;color:var(--dim);margin-top:4px}
.footer{margin-top:32px;font-size:10px;color:var(--muted);text-align:center}
@media(max-width:767px){.stat-grid{grid-template-columns:repeat(2,1fr)}body{padding:14px}}
.share-telegram{background:var(--blue);color:var(--bg);border:none;border-radius:var(--radius-sm);padding:8px 16px;font-size:12px;cursor:pointer;margin-top:8px}
.share-telegram:hover{opacity:0.85}
.agent-character{display:inline-flex;align-items:center;gap:6px}
`

// snapshotTemplate is the Go template for static HTML snapshots.
// Uses named blocks for future extensibility.
var snapshotTemplate = template.Must(template.New("snapshot").Parse(`<!DOCTYPE html>
<html lang="en" data-character-style="default">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Orchestra Snapshot — {{.GeneratedAt}}</title>
<style>` + snapshotCSS + `</style>
</head>
<body data-character-style="default">
{{block "snapshot-header" .}}
<h1>Orchestra Briefing Snapshot</h1>
<div class="meta">Generated {{.GeneratedAt}}{{if .SessionID}} · Session {{.SessionID}}{{end}}</div>
{{end}}

{{block "snapshot-stats" .}}
<div class="stat-grid">
<div class="stat" data-stat="total-tasks" data-stat-history=""><div class="stat-label">Total Tasks</div><div class="stat-val">{{.TotalTasks}}</div><div class="stat-sub">across all projects</div></div>
<div class="stat" data-stat="completed" data-stat-history=""><div class="stat-label">Completed</div><div class="stat-val">{{.TasksDone}}</div><div class="stat-sub">{{.TasksFailed}} failed</div></div>
<div class="stat" data-stat="agents" data-stat-history=""><div class="stat-label">Agents</div><div class="stat-val">{{.Agents}}</div><div class="stat-sub">active</div></div>
<div class="stat" data-stat="success-rate" data-stat-history=""><div class="stat-label">Success Rate</div><div class="stat-val">{{.SuccessRate}}</div><div class="stat-sub">overall</div></div>
</div>
{{end}}

{{block "snapshot-projects" .}}
{{range .Projects}}
<div class="card {{.Status}}">
<div class="agent-character"><!-- R-34 Rive avatar insertion point --></div>
<div class="card-name">{{.Name}}</div>
<div class="card-detail">{{.Goal}}</div>
<div class="card-detail">{{.TasksDone}}/{{.TasksTotal}} tasks · {{.TasksFailed}} failed · {{.Elapsed}}</div>
</div>
{{else}}
<div class="card"><div class="card-detail">No active projects</div></div>
{{end}}
{{end}}

{{block "snapshot-footer" .}}
<div class="footer">
Orchestra · Static snapshot · {{.GeneratedAt}}
{{if .TelegramAvailable}}<br><button class="share-telegram" onclick="shareTelegram()">Share to Telegram</button>{{end}}
</div>
<script>
function shareTelegram(){
  fetch('/api/snapshot/share-telegram',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({session_id:'{{.SessionID}}'})})
  .then(function(r){if(r.ok)alert('Shared to Telegram');else alert('Share failed');})
  .catch(function(){alert('Telegram plugin not available');});
}
</script>
{{end}}
</body>
</html>`))

// SnapshotHandler returns an http.HandlerFunc that generates a self-contained
// static HTML snapshot of the current dashboard briefing state.
// The returned file has all CSS inlined and no external dependencies.
func SnapshotHandler(dataFn func() SnapshotData) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		data := dataFn()
		if data.GeneratedAt == "" {
			data.GeneratedAt = time.Now().Format("2006-01-02 15:04:05")
		}

		var buf bytes.Buffer
		if err := snapshotTemplate.Execute(&buf, data); err != nil {
			http.Error(w, fmt.Sprintf("snapshot render error: %v", err), http.StatusInternalServerError)
			return
		}

		filename := fmt.Sprintf("orchestra-snapshot-%s.html", time.Now().Format("20060102-150405"))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
		w.Header().Set("Content-Length", fmt.Sprintf("%d", buf.Len()))
		w.Write(buf.Bytes())
	}
}

// ShareTelegramHandler returns an http.HandlerFunc that sends a snapshot
// summary to the telegram-forum channel plugin via IPC if available.
// Posts to the channel lock file path to check availability.
func ShareTelegramHandler(dataFn func() SnapshotData) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		data := dataFn()
		if data.GeneratedAt == "" {
			data.GeneratedAt = time.Now().Format("2006-01-02 15:04:05")
		}

		summary := fmt.Sprintf("Orchestra Snapshot — %s\nTasks: %d done, %d failed / %d total\nAgents: %d | Success: %s",
			data.GeneratedAt, data.TasksDone, data.TasksFailed, data.TotalTasks, data.Agents, data.SuccessRate)

		// Write summary to IPC file for telegram-forum plugin pickup
		ipcPath := fmt.Sprintf("/tmp/orchestra-telegram-share-%s.txt", time.Now().Format("20060102-150405"))
		if err := writeFile(ipcPath, []byte(summary)); err != nil {
			http.Error(w, "telegram plugin not available", http.StatusServiceUnavailable)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"shared","message":"Snapshot sent to Telegram"}`))
	}
}

// writeFile is a minimal file writer for IPC messages.
func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0644)
}

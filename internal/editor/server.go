// Package editor serves a local web UI over the destination tree: browse
// directories as thumbnail grids, tick photos and delete them. A deletion
// removes the local file and queues the same path for deletion from the
// phone on the next sync (see package deletions).
package editor

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"html/template"
	"image/jpeg"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"git.bytestone.uk/hum3/lofigui"

	"git.bytestone.uk/hum3/photo-importer/internal/deletions"
)

// ThumbSize is the bounding box of a thumbnail in pixels.
const ThumbSize = 256

// Editor is the destination-tree editor: one root, one deletion queue.
type Editor struct {
	Root     string
	Queue    *deletions.Queue
	CacheDir string // thumbnail cache
	PageSize int

	ctrl  *lofigui.Controller
	mu    sync.Mutex
	flash string // one-shot message shown on the next page render
}

// New returns an editor over root. Thumbnails are cached under cacheDir.
func New(root string, q *deletions.Queue, cacheDir string) (*Editor, error) {
	fi, err := os.Stat(root)
	if err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("root is not a directory: %s", root)
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("thumbnail cache: %w", err)
	}
	ctrl, err := lofigui.NewController(lofigui.ControllerConfig{TemplateString: pageTemplate, Name: "photo-importer"})
	if err != nil {
		return nil, err
	}
	return &Editor{Root: root, Queue: q, CacheDir: cacheDir, PageSize: 60, ctrl: ctrl}, nil
}

// Handler routes the editor's pages.
func (e *Editor) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", e.browse)
	mux.HandleFunc("GET /thumb", e.thumb)
	mux.HandleFunc("GET /file", e.file)
	mux.HandleFunc("POST /delete", e.delete)
	mux.HandleFunc("GET /queue", e.queue)
	mux.HandleFunc("GET /favicon.ico", lofigui.ServeFavicon)
	mux.HandleFunc("GET /assets/bulma.min.css", lofigui.ServeBulma)
	return mux
}

// safeRel normalises a user-supplied relative path so it can never
// escape the root: it is cleaned as if rooted at "/" then unrooted.
func safeRel(p string) string {
	return strings.TrimPrefix(path.Clean("/"+filepath.ToSlash(p)), "/")
}

func (e *Editor) abs(rel string) string {
	return filepath.Join(e.Root, filepath.FromSlash(rel))
}

var imageExt = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true}

func isImage(name string) bool { return imageExt[strings.ToLower(filepath.Ext(name))] }

type entry struct {
	Rel  string // relative to root
	Name string
	Size int64
}

// listDir returns the subdirectories (sorted) and files (newest name
// first) directly under rel.
func (e *Editor) listDir(rel string) (dirs []string, files []entry, err error) {
	ents, err := os.ReadDir(e.abs(rel))
	if err != nil {
		return nil, nil, err
	}
	for _, d := range ents {
		if d.IsDir() {
			dirs = append(dirs, d.Name())
			continue
		}
		info, err := d.Info()
		if err != nil {
			continue
		}
		files = append(files, entry{Rel: path.Join(rel, d.Name()), Name: d.Name(), Size: info.Size()})
	}
	sort.Strings(dirs)
	sort.Slice(files, func(i, j int) bool { return files[i].Name > files[j].Name })
	return dirs, files, nil
}

func (e *Editor) setFlash(msg string) {
	e.mu.Lock()
	e.flash = msg
	e.mu.Unlock()
}

func (e *Editor) takeFlash() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	m := e.flash
	e.flash = ""
	return m
}

func (e *Editor) render(w http.ResponseWriter, r *http.Request, title string, content string) {
	ctx := e.ctrl.StateDict(r)
	ctx["title"] = title
	ctx["content"] = template.HTML(content) //nolint:gosec // built from escaped fragments
	ctx["queue_len"] = e.Queue.Len()
	if err := e.ctrl.RenderTemplate(w, ctx); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func browseURL(dir string, page int) string {
	v := url.Values{}
	if dir != "" {
		v.Set("dir", dir)
	}
	if page > 1 {
		v.Set("page", strconv.Itoa(page))
	}
	if len(v) == 0 {
		return "/"
	}
	return "/?" + v.Encode()
}

func (e *Editor) browse(w http.ResponseWriter, r *http.Request) {
	dir := safeRel(r.URL.Query().Get("dir"))
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	page = max(page, 1)
	dirs, files, err := e.listDir(dir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	pages := max(1, (len(files)+e.PageSize-1)/e.PageSize)
	page = min(page, pages)
	start := (page - 1) * e.PageSize
	end := min(start+e.PageSize, len(files))

	c := lofigui.NewContext()
	if msg := e.takeFlash(); msg != "" {
		c.HTML(msg)
	}
	c.HTML(breadcrumb(dir))
	if len(dirs) > 0 {
		var sb strings.Builder
		sb.WriteString(`<div class="buttons">`)
		for _, d := range dirs {
			fmt.Fprintf(&sb, `<a class="button is-link is-light" href="%s">%s/</a>`,
				html.EscapeString(browseURL(path.Join(dir, d), 1)), html.EscapeString(d))
		}
		sb.WriteString(`</div>`)
		c.HTML(sb.String())
	}
	if len(files) == 0 {
		c.HTML(`<p class="has-text-grey">No files in this directory.</p>`)
		e.render(w, r, "photo-importer: "+dir, c.Buffer())
		return
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, `<form method="post" action="/delete"><input type="hidden" name="dir" value="%s"><input type="hidden" name="page" value="%d">`,
		html.EscapeString(dir), page)
	fmt.Fprintf(&sb, `<div class="level"><div class="level-left"><p class="level-item">%d file(s), page %d of %d</p>%s</div>`+
		`<div class="level-right"><button class="button is-danger level-item" type="submit">Delete selected</button></div></div>`,
		len(files), page, pages, pager(dir, page, pages))
	sb.WriteString(`<div class="grid is-col-min-12">`)
	for _, f := range files[start:end] {
		id := "f-" + hashKey(f.Rel)
		sb.WriteString(`<div class="cell tile-cell"><div class="box p-2">`)
		fmt.Fprintf(&sb, `<label for="%s">`, id)
		if isImage(f.Name) {
			fmt.Fprintf(&sb, `<img class="thumb" loading="lazy" src="/thumb?p=%s" alt="%s">`,
				url.QueryEscape(f.Rel), html.EscapeString(f.Name))
		} else {
			fmt.Fprintf(&sb, `<div class="thumb placeholder"><span class="tag is-dark">%s</span></div>`,
				html.EscapeString(strings.ToUpper(strings.TrimPrefix(filepath.Ext(f.Name), "."))))
		}
		sb.WriteString(`</label>`)
		fmt.Fprintf(&sb, `<div class="is-flex is-justify-content-space-between is-align-items-center">`+
			`<label class="checkbox is-size-7" for="%s"><input type="checkbox" id="%s" name="p" value="%s"> %s</label>`+
			`<a class="is-size-7" href="/file?p=%s" target="_blank">open</a></div>`,
			id, id, html.EscapeString(f.Rel), html.EscapeString(f.Name), url.QueryEscape(f.Rel))
		fmt.Fprintf(&sb, `<p class="is-size-7 has-text-grey">%s</p>`, humanSize(f.Size))
		sb.WriteString(`</div></div>`)
	}
	sb.WriteString(`</div></form>`)
	c.HTML(sb.String())
	e.render(w, r, "photo-importer: "+dir, c.Buffer())
}

func breadcrumb(dir string) string {
	var sb strings.Builder
	sb.WriteString(`<nav class="breadcrumb" aria-label="breadcrumbs"><ul><li><a href="/">root</a></li>`)
	if dir != "" {
		parts := strings.Split(dir, "/")
		for i, p := range parts {
			fmt.Fprintf(&sb, `<li><a href="%s">%s</a></li>`,
				html.EscapeString(browseURL(strings.Join(parts[:i+1], "/"), 1)), html.EscapeString(p))
		}
	}
	sb.WriteString(`</ul></nav>`)
	return sb.String()
}

func pager(dir string, page, pages int) string {
	if pages <= 1 {
		return ""
	}
	var sb strings.Builder
	if page > 1 {
		fmt.Fprintf(&sb, `<a class="button is-small level-item" href="%s">&laquo; newer</a>`, html.EscapeString(browseURL(dir, page-1)))
	}
	if page < pages {
		fmt.Fprintf(&sb, `<a class="button is-small level-item" href="%s">older &raquo;</a>`, html.EscapeString(browseURL(dir, page+1)))
	}
	return sb.String()
}

// delete removes the selected files locally and queues them for deletion
// from the phone. The queue is written first: a file deleted locally but
// not queued would silently survive on the phone.
func (e *Editor) delete(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	dir := safeRel(r.Form.Get("dir"))
	page, _ := strconv.Atoi(r.Form.Get("page"))
	var done []string
	var errs []string
	for _, p := range r.Form["p"] {
		rel := safeRel(p)
		if rel == "" {
			continue
		}
		if fi, err := os.Stat(e.abs(rel)); err != nil || fi.IsDir() {
			errs = append(errs, fmt.Sprintf("%s: not a file in the destination", rel))
			continue
		}
		if err := e.Queue.Add(rel); err != nil {
			errs = append(errs, fmt.Sprintf("%s: queue: %v", rel, err))
			continue
		}
		if err := os.Remove(e.abs(rel)); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", rel, err))
			continue
		}
		done = append(done, rel)
	}
	var sb strings.Builder
	if len(done) > 0 {
		fmt.Fprintf(&sb, `<div class="notification is-warning"><strong>Deleted %d file(s)</strong> locally and queued for deletion from the phone on the next sync (queue now %d).<br><small>%s</small></div>`,
			len(done), e.Queue.Len(), html.EscapeString(strings.Join(done, ", ")))
	}
	if len(errs) > 0 {
		fmt.Fprintf(&sb, `<div class="notification is-danger">%s</div>`, html.EscapeString(strings.Join(errs, "; ")))
	}
	if sb.Len() > 0 {
		e.setFlash(sb.String())
	}
	http.Redirect(w, r, browseURL(dir, max(page, 1)), http.StatusSeeOther)
}

func (e *Editor) queue(w http.ResponseWriter, r *http.Request) {
	paths := e.Queue.Paths()
	var sb strings.Builder
	fmt.Fprintf(&sb, `<h2 class="title is-4">Deletion queue</h2><p>%d file(s) will be deleted from the phone on the next <code>photo-importer sync</code> (after confirmation).</p>`, len(paths))
	if len(paths) > 0 {
		sb.WriteString(`<ul class="mt-3">`)
		for _, p := range paths {
			fmt.Fprintf(&sb, `<li><code>%s</code></li>`, html.EscapeString(p))
		}
		sb.WriteString(`</ul>`)
	}
	fmt.Fprintf(&sb, `<p class="mt-4 has-text-grey is-size-7">%s</p>`, html.EscapeString(e.Queue.FilePath()))
	e.render(w, r, "photo-importer: deletion queue", sb.String())
}

// file serves the original file for the "open" link.
func (e *Editor) file(w http.ResponseWriter, r *http.Request) {
	rel := safeRel(r.URL.Query().Get("p"))
	if rel == "" {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, e.abs(rel))
}

func hashKey(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(h[:8])
}

// thumb serves a cached thumbnail, generating it on first request. The
// cache key covers path, size and mtime so an edited file re-renders.
func (e *Editor) thumb(w http.ResponseWriter, r *http.Request) {
	rel := safeRel(r.URL.Query().Get("p"))
	if rel == "" || !isImage(rel) {
		http.NotFound(w, r)
		return
	}
	src := e.abs(rel)
	fi, err := os.Stat(src)
	if err != nil || fi.IsDir() {
		http.NotFound(w, r)
		return
	}
	key := hashKey(rel, strconv.FormatInt(fi.Size(), 10), fi.ModTime().UTC().Format("20060102T150405.000"), strconv.Itoa(ThumbSize))
	cached := filepath.Join(e.CacheDir, key+".jpg")
	if _, err := os.Stat(cached); err != nil {
		img, err := thumbnail(src, ThumbSize)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		tmp := cached + ".tmp"
		f, err := os.Create(tmp)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 85}); err != nil {
			_ = f.Close()
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := f.Close(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := os.Rename(tmp, cached); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeFile(w, r, cached)
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

const pageTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.title}}</title>
  <link rel="stylesheet" href="/assets/bulma.min.css">
  <style>
    .thumb { display: block; width: 100%; aspect-ratio: 1 / 1; object-fit: contain; background: #eee; cursor: pointer; }
    .placeholder { display: flex; align-items: center; justify-content: center; }
    .tile-cell .box:has(input:checked) { outline: 3px solid #f14668; }
  </style>
</head>
<body>
  <nav class="navbar is-light" role="navigation" aria-label="main navigation">
    <div class="navbar-brand"><a class="navbar-item has-text-weight-bold" href="/">photo-importer editor</a></div>
    <div class="navbar-menu is-active"><div class="navbar-end">
      <a class="navbar-item" href="/queue">Deletion queue ({{.queue_len}})</a>
    </div></div>
  </nav>
  <section class="section pt-4">
    <div class="container is-fluid">
      {{.content}}
    </div>
  </section>
</body>
</html>`

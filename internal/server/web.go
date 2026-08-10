// Web-Frontend des Dienstes: serverseitig gerenderte Startseite mit
// SEO-Metadaten (Canonical, Open Graph, JSON-LD, robots.txt, sitemap.xml)
// und Hydration — der eingebettete Zustand (#goteach-state) wird von
// assets/app.js gelesen, das Formular funktioniert auch ohne JavaScript.
package server

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strings"
)

//go:embed assets
var assetsFS embed.FS

// homeTmpl wird einmal beim Start geparst; Template-Fehler schlagen damit
// bereits in den Tests fehl, nicht erst pro Request.
var homeTmpl = template.Must(template.ParseFS(assetsFS, "assets/index.html.tmpl"))

// homeData füttert index.html.tmpl.
type homeData struct {
	Canonical string
	Base      string
	Katago    bool
	Auth      bool
	MaxVisits int
	State     template.JS
	LDJSON    template.JS
}

// baseURL rekonstruiert die absolute Basis-URL aus dem Request; hinter
// Vercels Proxy steckt das Schema in X-Forwarded-Proto.
func baseURL(r *http.Request) string {
	proto := r.Header.Get("X-Forwarded-Proto")

	if proto == "" {
		if r.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}

	return proto + "://" + r.Host
}

// handleHome bedient GET /: HTML für Browser (Accept: text/html),
// sonst die JSON-Dienstinfo — bestehende API-Clients sehen keine Änderung.
func handleHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)

		return
	}

	if !allowGetHead(w, r) {
		return
	}

	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		renderHome(w, r)

		return
	}

	serveInfo(w)
}

func renderHome(w http.ResponseWriter, r *http.Request) {
	base := baseURL(r)
	canonical := base + "/"

	// Hydration-Zustand für assets/app.js; json.Marshal maskiert <, > und &,
	// daher kann der Inhalt kein </script> enthalten.
	state, err := json.Marshal(map[string]any{
		"katago":        engineAvailable(),
		"synthetic":     !engineAvailable(),
		"authRequired":  authEnabled(),
		"maxVisits":     maxVisits,
		"maxSGFBytes":   maxSGFBytes,
		"defaultVisits": defaultVisits,
	})

	if err != nil {
		httpError(w, http.StatusInternalServerError, "Zustand: %v", err)

		return
	}

	ld, err := json.Marshal(map[string]any{
		"@context":            "https://schema.org",
		"@type":               "WebApplication",
		"name":                "goteach",
		"url":                 canonical,
		"inLanguage":          "de",
		"applicationCategory": "EducationalApplication",
		"operatingSystem":     "Web",
		"description": "Go-Partien (SGF) analysieren: für jeden Zug eine " +
			"deutsche Lehreinheit mit Bewertung, Punktverlust, Gewinnchancen " +
			"und Engine-Erstwahl.",
		"offers": map[string]any{
			"@type": "Offer", "price": "0", "priceCurrency": "EUR",
		},
	})

	if err != nil {
		httpError(w, http.StatusInternalServerError, "JSON-LD: %v", err)

		return
	}

	// Erst in einen Puffer rendern: Template-Fehler führen zu einem sauberen
	// 500 statt zu einer halb geschriebenen 200-Antwort.
	var buf bytes.Buffer

	err = homeTmpl.Execute(&buf, homeData{
		Canonical: canonical,
		Base:      base,
		Katago:    engineAvailable(),
		Auth:      authEnabled(),
		MaxVisits: maxVisits,
		State:     template.JS(state),
		LDJSON:    template.JS(ld),
	})

	if err != nil {
		log.Printf("goteach-server: Template: %v", err)
		httpError(w, http.StatusInternalServerError, "Template-Fehler")

		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}

// xmlEscape genügt für URLs in sitemap.xml (Host stammt aus dem Request).
var xmlEscape = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func handleRobots(w http.ResponseWriter, r *http.Request) {
	if !allowGetHead(w, r) {
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "User-agent: *\nAllow: /\n\nSitemap: %s/sitemap.xml\n",
		baseURL(r))
}

func handleSitemap(w http.ResponseWriter, r *http.Request) {
	if !allowGetHead(w, r) {
		return
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>%s/</loc></url>
</urlset>
`, xmlEscape.Replace(baseURL(r)))
}

// allowGetHead lässt nur GET/HEAD durch; sonst 405 mit Allow-Header.
func allowGetHead(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}

	w.Header().Set("Allow", "GET, HEAD")
	httpError(w, http.StatusMethodNotAllowed, "GET erwartet")

	return false
}

// handleAsset liefert eine eingebettete statische Datei aus assets/.
func handleAsset(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !allowGetHead(w, r) {
			return
		}

		data, err := assetsFS.ReadFile("assets/" + name)

		if err != nil {
			http.NotFound(w, r)

			return
		}

		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_, _ = w.Write(data)
	}
}

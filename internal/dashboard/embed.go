package dashboard

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"
)

//go:embed assets
var assetsFS embed.FS

//go:embed templates
var templatesFS embed.FS

func Templates() (*template.Template, error) {
	return template.New("").Funcs(templateFuncs()).ParseFS(templatesFS, "templates/*.html", "templates/components/*.html")
}

func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"safeHTML":      func(s string) template.HTML { return template.HTML(s) },
		"gt":            func(a, b int) bool { return a > b },
		"avatarSrc":     AvatarSrc,
		"statusRingCSS": StatusRingCSS,
	}
}

// TemplateNames returns the list of defined template names (useful for diagnostics).
func TemplateNames() ([]string, error) {
	tmpl, err := Templates()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, t := range tmpl.Templates() {
		if t.Name() != "" {
			names = append(names, t.Name())
		}
	}
	return names, nil
}

func StaticHandler() http.Handler {
	sub, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		panic("dashboard: embedded assets directory missing: " + err.Error())
	}
	return http.FileServer(http.FS(sub))
}

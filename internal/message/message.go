package message

import (
	"embed"
	"fmt"
	"html/template"
	"strings"

	"github.com/am-kenny/ampulsar/internal/domain"
)

type StreamEvent struct {
	domain.Session

	Timestamp int64
}

var funcMap = template.FuncMap{
	"trimRedDot": func(s string) string {
		return strings.TrimPrefix(s, "🔴")
	},
}

//go:embed templates/*.gotmpl
var templateFS embed.FS

var templates = template.Must(template.New("").Funcs(funcMap).ParseFS(templateFS, "templates/*.gotmpl"))

func render(messageType, style, lang string, e StreamEvent) (string, error) {
	name := fmt.Sprintf("%s_%s_%s.html.gotmpl", messageType, style, lang)

	t := templates.Lookup(name)
	if t == nil {
		return "", fmt.Errorf("no template found for %s", name)
	}

	var buf strings.Builder
	if err := t.Execute(&buf, e); err != nil {
		return "", err
	}

	return buf.String(), nil
}

func FormatLive(style, lang string, e StreamEvent) (string, error) {
	return render("live", style, lang, e)
}

func FormatWentOffline(style, lang string, e StreamEvent) (string, error) {
	return render("offline", style, lang, e)
}

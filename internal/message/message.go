package message

import (
	"embed"
	"fmt"
	"html/template"
	"strings"
	"time"

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
	"hms": func(d time.Duration) string {
		total := int(d.Seconds())
		h := total / 3600
		m := (total % 3600) / 60
		s := total % 60
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	},
}

//go:embed templates/*.gotmpl
var templateFS embed.FS

var templates = template.Must(template.New("").Funcs(funcMap).ParseFS(templateFS, "templates/*.gotmpl"))

// render looks up template by type, style, language and executes it against event
func render(messageType, style, lang string, e StreamEvent) (string, error) {
	name := fmt.Sprintf("%s_%s_%s.html.gotmpl", messageType, style, lang)

	t := templates.Lookup(name)
	if t == nil {
		return "", fmt.Errorf("message: no template found for %s", name)
	}

	return execute(t, e)
}

// Render parses templateText and executes it against event
func Render(templateText string, e StreamEvent) (string, error) {
	t, err := template.New("").Funcs(funcMap).Parse(templateText)
	if err != nil {
		return "", fmt.Errorf("message: parse template: %w", err)
	}

	return execute(t, e)
}

// execute executes provided template against event
func execute(t *template.Template, e StreamEvent) (string, error) {
	var buf strings.Builder
	if err := t.Execute(&buf, e); err != nil {
		return "", fmt.Errorf("message: execute template: %w", err)
	}
	return buf.String(), nil
}

func FormatLive(style, lang string, e StreamEvent) (string, error) {
	return render("live", style, lang, e)
}

func FormatWentOffline(style, lang string, e StreamEvent) (string, error) {
	return render("offline", style, lang, e)
}

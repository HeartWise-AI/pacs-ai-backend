package logredact

import (
	"log"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5/middleware"
)

// AccessLogFormatter is chi's default access log line with the request URI
// redacted (UIDs hashed, patient demographics removed).
type AccessLogFormatter struct {
	middleware.DefaultLogFormatter
}

// NewAccessLogFormatter writes to stdout like chi's middleware.Logger.
func NewAccessLogFormatter() *AccessLogFormatter {
	return &AccessLogFormatter{middleware.DefaultLogFormatter{
		Logger:  log.New(NewWriter(os.Stdout), "", log.LstdFlags),
		NoColor: true,
	}}
}

// NewLogEntry logs a redacted copy of the request; the handler still gets
// the original.
func (f *AccessLogFormatter) NewLogEntry(r *http.Request) middleware.LogEntry {
	redacted := *r
	redacted.RequestURI = RequestURI(r.RequestURI)
	if r.URL != nil {
		u := *r.URL
		u.RawQuery = Query(r.URL.RawQuery)
		u.Path, u.RawPath = Path(r.URL.Path), ""
		redacted.URL = &u
	}
	return f.DefaultLogFormatter.NewLogEntry(&redacted)
}

// AccessLogger replaces middleware.Logger.
func AccessLogger() func(http.Handler) http.Handler {
	return middleware.RequestLogger(NewAccessLogFormatter())
}

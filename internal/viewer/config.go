package viewer

import (
	"net/url"
	"path"
)

// ViewerOptions controls whether and how the local viewer starts.
type ViewerOptions struct {
	Enabled          bool
	OpenBrowser      bool
	Port             int
	InitialSessionID string
	CurrentRuntimeID string
}

// NormalizeViewerOptions clamps invalid values and keeps the runtime contract small.
func NormalizeViewerOptions(opts ViewerOptions) ViewerOptions {
	if opts.Port < 0 {
		opts.Port = 0
	}
	return opts
}

// InitialPath returns the route opened in the browser.
func (o ViewerOptions) InitialPath() string {
	if o.CurrentRuntimeID != "" {
		if o.InitialSessionID != "" {
			return path.Join("/r", url.PathEscape(o.CurrentRuntimeID), "sessions", url.PathEscape(o.InitialSessionID))
		}
		return path.Join("/r", url.PathEscape(o.CurrentRuntimeID))
	}
	if o.InitialSessionID == "" {
		return "/"
	}
	return path.Join("/sessions", url.PathEscape(o.InitialSessionID))
}

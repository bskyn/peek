package cli

import (
	"github.com/bskyn/peek/internal/store"
	"github.com/bskyn/peek/internal/usage"
)

func newUsageAnnotator(st *store.Store, sessionID string, replay bool) *usage.Annotator {
	annotator := usage.NewAnnotator()
	if replay {
		return annotator
	}

	events, err := st.GetEvents(sessionID)
	if err != nil {
		return annotator
	}
	annotator.Observe(events)
	return annotator
}

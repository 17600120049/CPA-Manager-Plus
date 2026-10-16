package bootstrap

import (
	"errors"
	"fmt"
)

type HistoricalSourceState string

const (
	HistoryFullRaw    HistoricalSourceState = "full_raw"
	HistoryRawDeleted HistoricalSourceState = "raw_deleted"
	HistoryIncomplete HistoricalSourceState = "incomplete"
)

type HistoricalAuthorityStatus string

const (
	HistoryAvailable     HistoricalAuthorityStatus = "available"
	HistoryUnavailable   HistoricalAuthorityStatus = "unavailable"
	HistoryIndeterminate HistoricalAuthorityStatus = "indeterminate"
)

// HistoricalEvidence prevents raw-deleted/incomplete history from silently
// falling back to current pricing or reconstructed partial authority.
type HistoricalEvidence struct {
	SourceState              HistoricalSourceState
	AuthorityStatus          HistoricalAuthorityStatus
	RetainedEvidenceComplete bool
}

func (h HistoricalEvidence) Validate() error {
	switch h.SourceState {
	case HistoryFullRaw, HistoryRawDeleted, HistoryIncomplete:
	default:
		return fmt.Errorf("unknown historical source state %q", h.SourceState)
	}
	switch h.AuthorityStatus {
	case HistoryAvailable, HistoryUnavailable, HistoryIndeterminate:
	default:
		return fmt.Errorf("unknown historical authority status %q", h.AuthorityStatus)
	}
	if h.SourceState == HistoryIncomplete && h.AuthorityStatus == HistoryAvailable {
		return errors.New("incomplete historical source cannot claim available authority")
	}
	if h.SourceState == HistoryRawDeleted && h.AuthorityStatus == HistoryAvailable && !h.RetainedEvidenceComplete {
		return errors.New("raw-deleted history requires complete retained evidence")
	}
	return nil
}

func (h HistoricalEvidence) MigrationReady() bool {
	if h.Validate() != nil || h.AuthorityStatus != HistoryAvailable {
		return false
	}
	switch h.SourceState {
	case HistoryFullRaw:
		return true
	case HistoryRawDeleted:
		return h.RetainedEvidenceComplete
	default:
		return false
	}
}

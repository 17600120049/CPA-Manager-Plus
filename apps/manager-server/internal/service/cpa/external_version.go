package cpa

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// External management speaks the CPA v8 contract, so a CPA that advertises v8 or
// later is qualified for writes and anything below it is refused for writes
// while safe reads keep working. A CPA that omits the version header is never
// assumed to be new enough: the class is reported instead of trusted.
const (
	VersionSupportSupported   = "supported"
	VersionSupportUnsupported = "unsupported"
	VersionSupportUnknown     = "unknown"
)

const (
	// ReasonExternalVersionUnsupported is the stable code of every write this
	// Manager refused because the user's CPA did not prove a v8 contract. It
	// covers both an old CPA and one whose version could not be read.
	ReasonExternalVersionUnsupported = "external_cpa_version_unsupported"
)

// ErrExternalVersionUnsupported reports a user's CPA that did not advertise the
// v8 contract this Manager writes. It blocks writes and leaves safe reads alone:
// the configuration is readable, but every write would target a surface CPA does
// not serve.
var ErrExternalVersionUnsupported = errors.New("external cpa version unsupported")

// ExternalVersionReport is the version an External CPA advertised together with
// the class of support this Manager can prove for it. The report never carries a
// credential.
type ExternalVersionReport struct {
	Version      string `json:"version"`
	Support      string `json:"support"`
	WriteBlocked bool   `json:"writeBlocked"`
	Reason       string `json:"reason,omitempty"`
}

// ClassifyExternalVersion maps a CPA version header to its support class. The v8
// major and every later one are the contract this Manager writes; an older major
// is unsupported, and a version this Manager cannot parse is unknown. Both of the
// latter refuse writes, because neither proves the surface a write would target,
// and neither blocks a read.
func ClassifyExternalVersion(version string) ExternalVersionReport {
	report := ExternalVersionReport{Version: strings.TrimSpace(version), Support: VersionSupportUnknown}
	major, ok := majorVersion(report.Version)
	if ok && major >= 8 {
		report.Support = VersionSupportSupported
		return report
	}
	if ok {
		report.Support = VersionSupportUnsupported
	}
	report.WriteBlocked = true
	report.Reason = ReasonExternalVersionUnsupported
	return report
}

// ObserveExternalVersion reads the version a user's CPA advertises through the
// same authenticated management read every other observation uses, and classifies
// it. A CPA that refuses the credential is the typed ErrManagementRejected
// blocker; one that cannot be reached is ErrProofUnavailable. Both are returned
// instead of a classification: neither is evidence about the version.
func ObserveExternalVersion(ctx context.Context, baseURL string, managementKey string) (ExternalVersionReport, error) {
	version, err := ObserveManagementAPI(ctx, baseURL, managementKey)
	if err != nil {
		return ExternalVersionReport{}, externalVersionProbeError(err)
	}
	return ClassifyExternalVersion(version), nil
}

// externalVersionProbeError types one failed probe. ObserveManagementAPI reports
// a status as plain text, so a 401 is recognized here as CPA's own refusal and
// everything else stays an unavailable proof: neither may become a version class.
func externalVersionProbeError(err error) error {
	var status *managementStatusError
	if errors.As(err, &status) {
		if status.code == http.StatusUnauthorized {
			return fmt.Errorf("%w: %w", ErrManagementRejected, err)
		}
		return fmt.Errorf("%w: %w", ErrProofUnavailable, err)
	}
	if errors.Is(err, ErrManagementRejected) || errors.Is(err, ErrProofUnavailable) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrProofUnavailable, err)
}

// majorVersion reads the leading major number of a CPA version tag. CPA versions
// are tags such as v8.0.13; a bare 8.0.13 is accepted as well.
func majorVersion(version string) (int, bool) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(version), "v")
	if trimmed == "" {
		return 0, false
	}
	end := strings.IndexAny(trimmed, ".-+ ")
	if end < 0 {
		end = len(trimmed)
	}
	major, err := strconv.Atoi(trimmed[:end])
	if err != nil || major <= 0 {
		return 0, false
	}
	return major, true
}

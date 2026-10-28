package cpa

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

// validExternalUsageVersionSupport is the support class of an observed CPA
// version. External management speaks the v8 contract, so anything below it is
// refused for writes while safe reads keep working, and a version this Manager
// was never qualified against is reported rather than trusted.
const (
	VersionSupportSupported   = "supported"
	VersionSupportUnverified  = "unverified"
	VersionSupportUnsupported = "unsupported"
	VersionSupportUnknown     = "unknown"
)

// ErrExternalVersionUnsupported reports a user's CPA that advertises a version
// below the v8 contract this Manager writes. It blocks writes and leaves safe
// reads alone: the configuration is readable, but every write would target a
// surface that CPA does not serve.
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

// ClassifyExternalVersion maps a CPA version header to its support class. Only
// the v8 major is qualified; a version this Manager cannot parse is reported as
// unknown and never blocks a read.
func ClassifyExternalVersion(version string) ExternalVersionReport {
	report := ExternalVersionReport{Version: strings.TrimSpace(version), Support: VersionSupportUnknown}
	major, ok := majorVersion(report.Version)
	if !ok {
		return report
	}
	switch {
	case major == 8:
		report.Support = VersionSupportSupported
	case major < 8:
		report.Support = VersionSupportUnsupported
		report.WriteBlocked = true
		report.Reason = "external_cpa_version_unsupported"
	default:
		report.Support = VersionSupportUnverified
	}
	return report
}

// ObserveExternalVersion reads the version a user's CPA advertises through the
// same authenticated management read every other observation uses, and classifies
// it. A CPA that does not answer is not evidence of an unsupported version, so the
// error is returned separately and the caller decides; a CPA that omits the
// header is reported as unknown rather than guessed.
func ObserveExternalVersion(ctx context.Context, baseURL string, managementKey string) (ExternalVersionReport, error) {
	version, err := ObserveManagementAPI(ctx, baseURL, managementKey)
	if err != nil {
		return ExternalVersionReport{}, err
	}
	return ClassifyExternalVersion(version), nil
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

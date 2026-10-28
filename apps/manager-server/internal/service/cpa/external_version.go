package cpa

import (
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

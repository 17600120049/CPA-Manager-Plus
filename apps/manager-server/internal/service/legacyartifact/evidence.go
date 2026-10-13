package legacyartifact

// ValidatedHopEvidence reserves the future Manager-owner-qualified capability.
// No production path in this foundation creates a valid instance. Package-private
// receipt validation provides diagnostics only; HMAC files cannot mint provenance.
type ValidatedHopEvidence struct {
	operationID          string
	release              string
	sourceCommit         string
	asset                string
	executableSHA256     string
	inputManifestDigest  string
	outputManifestDigest string
	inputFamily          string
	outputFamily         string
	processEvidence      ProcessEvidence
	checkpointEvidence   CheckpointEvidence
	validated            bool
}

// OperationID returns the unique operation identifier issued by the authority.
func (e *ValidatedHopEvidence) OperationID() string {
	if e == nil {
		return ""
	}
	return e.operationID
}

// Release returns the pinned release version (e.g. v1.14.4).
func (e *ValidatedHopEvidence) Release() string {
	if e == nil {
		return ""
	}
	return e.release
}

// SourceCommit returns the git commit SHA of the pinned release.
func (e *ValidatedHopEvidence) SourceCommit() string {
	if e == nil {
		return ""
	}
	return e.sourceCommit
}

// Asset returns the exact archive asset name used for the execution.
func (e *ValidatedHopEvidence) Asset() string {
	if e == nil {
		return ""
	}
	return e.asset
}

// ExecutableSHA256 returns the cryptographic digest of the executed binary.
func (e *ValidatedHopEvidence) ExecutableSHA256() string {
	if e == nil {
		return ""
	}
	return e.executableSHA256
}

// InputManifestDigest returns the SHA256 digest of the input bundle manifest.
func (e *ValidatedHopEvidence) InputManifestDigest() string {
	if e == nil {
		return ""
	}
	return e.inputManifestDigest
}

// OutputManifestDigest returns the SHA256 digest of the output bundle manifest.
func (e *ValidatedHopEvidence) OutputManifestDigest() string {
	if e == nil {
		return ""
	}
	return e.outputManifestDigest
}

// InputFamily returns the detected schema family of the input source.
func (e *ValidatedHopEvidence) InputFamily() string {
	if e == nil {
		return ""
	}
	return e.inputFamily
}

// OutputFamily returns the detected schema family of the output bundle.
func (e *ValidatedHopEvidence) OutputFamily() string {
	if e == nil {
		return ""
	}
	return e.outputFamily
}

// ProcessEvidence returns the process execution proof.
func (e *ValidatedHopEvidence) ProcessEvidence() ProcessEvidence {
	if e == nil {
		return ProcessEvidence{}
	}
	return e.processEvidence
}

// CheckpointEvidence returns the verified checkpoint migration inspection.
func (e *ValidatedHopEvidence) CheckpointEvidence() CheckpointEvidence {
	if e == nil {
		return CheckpointEvidence{}
	}
	return e.checkpointEvidence
}

// IsValid checks the reserved capability state. Production issuance is unavailable.
func (e *ValidatedHopEvidence) IsValid() bool {
	return e != nil && e.validated
}

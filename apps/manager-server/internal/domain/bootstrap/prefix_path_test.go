package bootstrap

import "testing"

func TestPrefixCanonicalPathFields(t *testing.T) {
	base := PrefixOperation{
		OperationID:    "operation-2000",
		InstallationID: "install-a", CurrentInstallationID: "install-a",
		ExpectedRoutingRevision: 9, CurrentRoutingRevision: 9,
		PreviousVerifiedEffectivePath: "/old-control",
		DesiredControlBasePath:        "/new-control",
		VerifiedEffectivePath:         "/new-control",
		Impact:                        PrefixControlRouteOnly, Stage: PrefixRouteActivated, Result: PrefixPending,
	}
	for _, tc := range []struct {
		path  string
		valid bool
	}{
		{"/control", true}, {"/control/sub-path", true}, {"/AZaz09-._~$&+,/:;=@", true},
		{"/控制", false}, {"/café", false}, {"/😀", false},
		{"/control[admin]", false}, {"/control!", false}, {"/control%", false},
		{"/control\"", false}, {"/control`", false}, {"/control{admin}", false},
		{"/", false}, {"/control/", false}, {"/control//x", false},
		{"/control/../x", false}, {"/control/%2fx", false}, {"/control/%252ex", false},
		{"/control\\x", false}, {"/control x", false}, {"/control?x", false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			for _, field := range []string{"desired", "previous", "effective"} {
				op := base
				switch field {
				case "desired":
					op.DesiredControlBasePath = tc.path
				case "previous":
					op.PreviousVerifiedEffectivePath = tc.path
				case "effective":
					op.VerifiedEffectivePath = tc.path
				}
				if got := op.Validate() == nil; got != tc.valid {
					t.Errorf("operation %s path %q: valid=%v, want %v", field, tc.path, got, tc.valid)
				}
			}
			for _, field := range []string{"current", "previous_snapshot"} {
				o := PrefixRecoveryObservation{
					CurrentInstallationID: "install-a", CurrentRoutingRevision: 9,
					CurrentEffectivePath:      "/new-control",
					PreviousSnapshotQualified: true, PreviousSnapshotEffectivePath: "/old-control",
				}
				if field == "current" {
					o.CurrentEffectivePath = tc.path
				} else {
					o.PreviousSnapshotEffectivePath = tc.path
				}
				if got := o.valid(); got != tc.valid {
					t.Errorf("observation %s path %q: valid=%v, want %v", field, tc.path, got, tc.valid)
				}
			}
		})
	}
}

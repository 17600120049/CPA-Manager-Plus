package freshstrategy

import (
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/domain/bootstrap"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
)

// PreflightExternal applies the same accepted consumer qualification and static
// credential rules as Execute before the single irreversible attempt CAS.
// It deliberately performs no network I/O and grants no additional authority.
func (s *Service) PreflightExternal(request Request) (string, error) {
	if request.Strategy != bootstrap.ConnectExternal {
		return "", &Blocker{Code: "fresh_strategy_invalid"}
	}
	qualification := s.options.Qualification
	if s.production {
		qualification = s.consumers[consumerKey{bootstrap.ConnectExternal, bootstrap.AdoptSourceNone}].Qualification
	}
	if qualification.ValidateForStrategy(bootstrap.ConnectExternal, bootstrap.AdoptSourceNone) != nil {
		return "", &Blocker{Code: "required_v8_profile_qualification_required"}
	}
	baseURL, err := cpa.NormalizeV8BaseURL(request.BaseURL)
	if err != nil || !cpa.ValidV8ManagementKey(request.ManagementKey) {
		return "", &Blocker{Code: cpa.V8InvalidConnection.Error()}
	}
	return baseURL, nil
}

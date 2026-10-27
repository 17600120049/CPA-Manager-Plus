package ingress

import (
	"context"
	"sync"
	"time"
)

const (
	// DefaultGatewayAdmissionTTL bounds how long one positive private Manager
	// answer may be reused before Ingress re-reads it. Every Ingress process
	// starts with no confirmation, so a restart always reconfirms.
	DefaultGatewayAdmissionTTL = 15 * time.Second
	// DefaultGatewayAdmissionDeniedTTL bounds how long one negative answer is
	// reused. It keeps a Gateway-closed installation from turning client
	// retries into a stream of authenticated Destination reads while still
	// recovering quickly once the installation becomes admissible.
	DefaultGatewayAdmissionDeniedTTL = time.Second
	// DefaultGatewayAdmissionQueryTimeout bounds one Manager admission read. It
	// runs detached from the triggering client request so one client leaving or
	// timing out can never truncate a read that other queued clients depend on.
	DefaultGatewayAdmissionQueryTimeout = 2 * time.Second
)

// GatewayAdmission is the private Manager answer for one Fresh installation
// generation. It is a boolean posture, never a credential: it carries no key,
// management secret or destination address.
type GatewayAdmission struct {
	InstallationID string `json:"installationId"`
	Generation     string `json:"generation"`
	Admitted       bool   `json:"admitted"`
}

// FreshAuthority is the private Manager routing-authority reply. It repeats the
// one durable routing projection and adds the ephemeral admission posture. A
// Manager that does not send gatewayAdmission is an older Manager and never
// admits Gateway traffic.
type FreshAuthority struct {
	FreshRouteState
	GatewayAdmission *GatewayAdmission `json:"gatewayAdmission"`
}

// AdmittedFor accepts the Manager answer only when it repeats the live routing
// projection exactly, that projection is committed, and the admission is
// present and bound to the same installation and generation. Any absence,
// unknown shape or mismatch fails closed.
func (f FreshAuthority) AdmittedFor(state FreshRouteState) (GatewayAdmission, bool) {
	if f.FreshRouteState != state || !f.Committed || f.GatewayAdmission == nil {
		return GatewayAdmission{}, false
	}
	admission := *f.GatewayAdmission
	if !admission.Admitted || admission.InstallationID != state.InstallationID || admission.Generation != state.Generation {
		return GatewayAdmission{}, false
	}
	return admission, true
}

// GatewayAdmitter is the only Ingress-side authority for Fresh Gateway
// traffic. A positive Manager answer is cached for at most ttl and is still
// re-bound to the live routing projection on every request, so a changed
// projection withdraws admission immediately. Re-reads are serialized on mu so
// a closed Gateway produces at most one Manager read per denial window instead
// of one per queued client request.
type GatewayAdmitter struct {
	routes       *FreshRoutes
	query        func(context.Context) (FreshAuthority, error)
	ttl          time.Duration
	deniedTTL    time.Duration
	queryTimeout time.Duration
	now          func() time.Time

	mu        sync.Mutex
	confirmed *GatewayAdmission
	expires   time.Time
	denied    time.Time
}

func NewGatewayAdmitter(routes *FreshRoutes, query func(context.Context) (FreshAuthority, error), ttl time.Duration) *GatewayAdmitter {
	return &GatewayAdmitter{
		routes:       routes,
		query:        query,
		ttl:          ttl,
		deniedTTL:    DefaultGatewayAdmissionDeniedTTL,
		queryTimeout: DefaultGatewayAdmissionQueryTimeout,
		now:          time.Now,
	}
}

// Admitted reports whether Fresh client Gateway traffic may be forwarded. It is
// uncached for negatives beyond deniedTTL and never admits without a current
// committed projection or a positive, consistently bound Manager answer.
func (a *GatewayAdmitter) Admitted(ctx context.Context) bool {
	if a == nil || a.routes == nil || a.query == nil || a.ttl <= 0 {
		return false
	}
	state := a.routes.State()
	if state.InstallationID == "" || state.Generation == "" || !state.Committed {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	if a.confirmed != nil && now.Before(a.expires) && a.confirmed.InstallationID == state.InstallationID && a.confirmed.Generation == state.Generation {
		return true
	}
	a.confirmed = nil
	if now.Before(a.denied) {
		return false
	}
	// One refresh serves every queued client, so it must not inherit the
	// lifetime of whichever request happened to trigger it: a client that
	// disconnects or times out would otherwise fail the shared read and deny
	// all Gateway traffic for the next denial window.
	queryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), a.queryTimeout)
	reply, err := a.query(queryCtx)
	cancel()
	if err == nil {
		if admission, ok := reply.AdmittedFor(state); ok {
			a.confirmed = &admission
			a.expires = now.Add(a.ttl)
			a.denied = time.Time{}
			return true
		}
	}
	if err != nil && ctx.Err() != nil {
		// The caller is already gone, so a failure observed on its behalf is not
		// evidence about Manager: leave the next caller a fresh attempt instead
		// of a cached denial.
		return false
	}
	a.denied = now.Add(a.deniedTTL)
	return false
}

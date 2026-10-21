package cpa

import (
	"context"
	"net/http"
	"strconv"
)

func (t *Transport) WriteH1Retry(ctx context.Context, parent string, step H1Step, management string, value int) error {
	if t.h1 == nil || value < 0 || value > 10 || step.Scope != "request-retry" {
		return ErrProofUnavailable
	}
	v, e := t.h1.Parent(ctx, t, parent)
	if e != nil {
		return e
	}
	if v.H1.Parent.Intent.Domain != "config" || v.H1.Parent.Intent.Retry != value {
		return ErrSourceConflict
	}
	if e = t.h1.Reserved(ctx, t, parent, step); e != nil {
		return e
	}
	code, _, e := t.request(ctx, http.MethodPut, retryPath, management, []byte(strconv.Itoa(value)))
	if e != nil || code != 200 {
		return ErrApplyUnknown
	}
	return nil
}

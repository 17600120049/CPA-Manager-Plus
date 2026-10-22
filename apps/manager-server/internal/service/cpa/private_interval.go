package cpa

import (
 "bytes"
 "context"
 "encoding/json"
 "net/http"
)

// ReadInterval preserves missing, explicit null, and explicit zero independently
// from the B2 request-retry scalar. The raw-source snapshot brackets CPA GET.
func (t *Transport) ReadInterval(ctx context.Context, management string) (RetrySource, string, *int, error) {
 a, err := t.RetrySource(ctx)
 if err != nil { return a, "unknown", nil, err }
 code, body, err := t.request(ctx, http.MethodGet, intervalPath, management, nil)
 if err != nil { return a, "unknown", nil, err }
 presence := "missing"
 var value *int
 switch code {
 case 404:
 case 200:
  presence = "null"
  if !bytes.Equal(bytes.TrimSpace(body), []byte("null")) {
   var n int
   if json.Unmarshal(body, &n) != nil || n < 0 || n > 300 { return a, "unknown", nil, ErrSourceConflict }
   presence, value = "present", &n
  }
 default: return a, "unknown", nil, ErrProofUnavailable
 }
 z, err := t.RetrySource(ctx)
 if err != nil || a != z { return z, "unknown", nil, ErrSourceConflict }
 return z, presence, value, nil
}

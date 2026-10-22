package privateconfig

import (
 "os"
 "strings"
 "testing"
)

func TestH1IntervalScopeBoundariesAndCrossEditRestart(t *testing.T) {
 f := h1Fresh(t)
 interval := H1Intent{RequestID:strings.Repeat("6",32), Domain:"config", Mode:"interval", MaxRetryInterval:0, MaxRetryIntervalSet:true}
 if !interval.valid() || interval.scopes()[0] != "max-retry-interval" { t.Fatal("interval identity rejected") }
 for _, n := range []int{-1,301} {
  bad := interval; bad.MaxRetryInterval=n
  if bad.valid() { t.Fatal("out-of-range H1 grant accepted", n) }
 }
 absent := interval; absent.MaxRetryIntervalSet=false
 if absent.valid() { t.Fatal("zero without explicit scope accepted") }
 f.h1Claim(interval)
 q:=f.h1Begin(0)
 if q.Scope!="max-retry-interval" { t.Fatal("scope drift") }
 before,err:=os.ReadFile(f.a.config)
 if err!=nil {t.Fatal(err)}
 after:=f.changed("routing","retry","max-retry-interval")
 if !validChange(before,after,Operation{Scope:"max-retry-interval",MaxRetryInterval:0,MaxRetryIntervalSet:true}) {t.Fatal("B3 scalar was not a valid isolated change")}
 if validChange(before,after,Operation{Scope:"request-retry",Retry:0}) {t.Fatal("B3 accepted as B2")}
 f.write(after)
 f.commit(q)
 f.h1Finish()
 v:=f.observe()
 if v.CommittedMaxRetryInterval==nil || *v.CommittedMaxRetryInterval!=0 || v.CommittedRetry!=nil {t.Fatal("B3 overwrote B2 committed slot")}
 f.h1Claim(retryIntent("7",0))
 q=f.h1Begin(0)
 f.write(f.changed("routing","retry","request-retry"))
 f.commit(q)
 f.h1Finish()
 v=f.observe()
 if v.CommittedRetry==nil || *v.CommittedRetry!=0 || v.CommittedMaxRetryInterval==nil || *v.CommittedMaxRetryInterval!=0 { t.Fatal("B2/B3 committed slots crossed") }
 fresh:=New(f.a.root,f.a.config,f.a.grant,f.a.namespace,7,uint32(os.Geteuid()))
 f.a=fresh
 v=f.observe()
 if v.CommittedRetry==nil || v.CommittedMaxRetryInterval==nil { t.Fatal("lost B2/B3 across reload") }
 if f.a.CheckStartup()==nil {t.Fatal("old claim was incorrectly valid for restart")}
}

func TestIntervalRejectsOpaqueDrift(t *testing.T) {
 good:=[]byte("routing:\n  retry:\n    request-retry: 2\n")
 changed:=[]byte("routing:\n  retry:\n    request-retry: 3\n    max-retry-interval: 10\n")
 op:=Operation{Scope:"max-retry-interval",MaxRetryInterval:10,MaxRetryIntervalSet:true}
 if validChange(good,changed,op) {t.Fatal("unrelated B2 drift permitted")}
 if validChange(good,[]byte("routing:\n  retry:\n    request-retry: 2\n    max-retry-interval: 1.5\n"),op) {t.Fatal("non-integer accepted")}
}

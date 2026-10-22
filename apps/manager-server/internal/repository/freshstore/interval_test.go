package freshstore

import (
 "strings"
 "testing"
)

func TestIntervalJournalHasOwnIdentityAndRequiresH1(t *testing.T) {
 id := strings.Repeat("a",32)
 entry:=RetryOperation{RequestID:id,Field:"max-retry-interval",Value:300,SourceID:"private-source-id",Generation:"7",Revision:2,SHA256:strings.Repeat("b",64),State:"unknown",ParentID:"c:"+id,ClaimRevision:3,IntentDigest:strings.Repeat("c",64),Attempted:true}
 journal:=&RetryJournal{Latest:id,Operations:map[string]RetryOperation{id:entry}}
 if !validInterval(journal) || validRetry(journal) {t.Fatal("B3 journal not isolated from B2")}
 entry.Value=301; journal.Operations[id]=entry
 if validInterval(journal) {t.Fatal("interval greater than 300 admitted")}
 entry.Value=0;entry.ParentID="";entry.Attempted=false;entry.ClaimRevision=0;entry.IntentDigest=""
 journal.Operations[id]=entry
 if validInterval(journal) {t.Fatal("B3 admitted outside H1")}
}

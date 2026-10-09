package premium

import (
	"context"
	"os"
	"testing"
)

func TestFreeLiveCatalog(t *testing.T) {
	if os.Getenv("MAWG_FREE_LIVE") != "1" {
		t.Skip("opt-in unauthenticated catalog probe")
	}
	id, err := UUID()
	if err != nil {
		t.Fatal(err)
	}
	country, protocol, err := NewClient().FreeService(context.Background(), id)
	if err != nil {
		t.Logf("Live catalog: %v; config issuance not attempted", err)
		return
	}
	t.Logf("Live catalog: country=%s Free protocol=%s available=true; config issuance not attempted", country, protocol)
}

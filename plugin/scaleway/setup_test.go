package coredns_scaleway

import (
	"testing"

	"github.com/coredns/caddy"
)

func TestParseSettings(t *testing.T) {
	config := `
    scaleway domain domain2 {
        project_id pid
        access_key akey
        secret_key skey
    }
`
	c := caddy.NewTestController("scaleway", config)
	s, err := parse(c)
	if err != nil {
		t.Fatal(err)
	}

	if s.ProjectID == nil || *s.ProjectID != "pid" {
		t.Fatalf("project id is wrong or nil")
	}
	if s.AccessKey == nil || *s.AccessKey != "akey" {
		t.Fatalf("access key is wrong or nil")
	}
	if s.SecretKey == nil || *s.SecretKey != "skey" {
		t.Fatalf("secret key is wrong or nil")
	}
	if len(s.zones) != 2 {
		t.Fatalf("wrong number of exportedZones")
	}
	if s.zones[0] != "domain" {
		t.Fatalf("wrong first domain: %s", s.zones[0])
	}
	if s.zones[1] != "domain2" {
		t.Fatalf("wrong second domain: %s", s.zones[1])
	}
}

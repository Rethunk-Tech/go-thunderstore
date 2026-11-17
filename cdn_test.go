package thunderstore

import (
	"net/url"
	"testing"
)

func TestOnHostClearsUserinfo(t *testing.T) {
	got, ok := OnHost("https://u:p@evil.example/x", "https://cdn.example")
	if !ok {
		t.Fatal("ok=false")
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if u.User != nil {
		t.Fatalf("userinfo in %q", got)
	}
}

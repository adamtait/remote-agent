package scaffold_test

import (
	"testing"

	"github.com/adamtait/remote-agent/cws/internal/scaffold"
)

func TestStaticProber_ReturnsCannedValues(t *testing.T) {
	p := &scaffold.StaticProber{
		Project:       "my-proj",
		Region:        "us-central1",
		GitUserName_:  "Alice",
		GitUserEmail_: "alice@example.com",
		GitOriginURL_: "https://github.com/a/b.git",
		HasGcloud_:    true,
		HasAuth_:      true,
		HasDocker_:    false,
		Billing:       map[string]bool{"my-proj": true},
	}

	if p.GcloudProject() != "my-proj" {
		t.Errorf("project: got %q", p.GcloudProject())
	}
	if p.GcloudRegion() != "us-central1" {
		t.Errorf("region: got %q", p.GcloudRegion())
	}
	if p.GitUserName() != "Alice" {
		t.Errorf("git user: got %q", p.GitUserName())
	}
	if p.GitUserEmail() != "alice@example.com" {
		t.Errorf("git email: got %q", p.GitUserEmail())
	}
	if p.GitOriginURL() != "https://github.com/a/b.git" {
		t.Errorf("origin: got %q", p.GitOriginURL())
	}
	if !p.HasGcloud() {
		t.Error("HasGcloud: got false, want true")
	}
	if !p.HasGcloudAuth() {
		t.Error("HasGcloudAuth: got false")
	}
	if p.HasDocker() {
		t.Error("HasDocker: got true, want false")
	}
	if !p.BillingEnabled("my-proj") {
		t.Error("billing: got false")
	}
	if p.BillingEnabled("other") {
		t.Error("billing(other): got true")
	}
}

func TestGcloudRegionWithFallback_ReturnsProbed(t *testing.T) {
	p := &scaffold.StaticProber{Region: "europe-west1"}
	if got := scaffold.GcloudRegionWithFallback(p); got != "europe-west1" {
		t.Errorf("got %q, want europe-west1", got)
	}
}

func TestGcloudRegionWithFallback_FallsBackToDefault(t *testing.T) {
	p := &scaffold.StaticProber{}
	if got := scaffold.GcloudRegionWithFallback(p); got != "us-central1" {
		t.Errorf("got %q, want us-central1", got)
	}
}

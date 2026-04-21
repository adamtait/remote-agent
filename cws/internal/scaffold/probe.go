package scaffold

import (
	"os/exec"
	"strings"
)

// Prober discovers environment values used to pre-fill setup prompts and
// templates. All methods return zero values (empty string, false) when the
// underlying tool is missing or the value is not set — no errors are
// surfaced; setup treats absence as "no default".
type Prober interface {
	GcloudProject() string
	GcloudRegion() string
	GitUserName() string
	GitUserEmail() string
	GitOriginURL() string
	HasGcloud() bool
	HasGcloudAuth() bool
	HasDocker() bool
	BillingEnabled(projectID string) bool
}

// StaticProber is a test double returning canned values.
// Fields whose name would collide with a Prober method are suffixed with `_`.
type StaticProber struct {
	Project       string
	Region        string
	GitUserName_  string
	GitUserEmail_ string
	GitOriginURL_ string
	HasGcloud_    bool
	HasAuth_      bool
	HasDocker_    bool
	Billing       map[string]bool
}

func (p *StaticProber) GcloudProject() string         { return p.Project }
func (p *StaticProber) GcloudRegion() string          { return p.Region }
func (p *StaticProber) GitUserName() string           { return p.GitUserName_ }
func (p *StaticProber) GitUserEmail() string          { return p.GitUserEmail_ }
func (p *StaticProber) GitOriginURL() string          { return p.GitOriginURL_ }
func (p *StaticProber) HasGcloud() bool               { return p.HasGcloud_ }
func (p *StaticProber) HasGcloudAuth() bool           { return p.HasAuth_ }
func (p *StaticProber) HasDocker() bool               { return p.HasDocker_ }
func (p *StaticProber) BillingEnabled(id string) bool { return p.Billing[id] }

// RealProber shells out to gcloud/git/docker via the system PATH. All probes
// are best-effort: any error or non-zero exit yields the zero value.
type RealProber struct{}

func (RealProber) run(name string, args ...string) string {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (RealProber) exists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func (r RealProber) GcloudProject() string {
	return r.run("gcloud", "config", "get-value", "project", "--quiet")
}

func (r RealProber) GcloudRegion() string {
	return r.run("gcloud", "config", "get-value", "compute/region", "--quiet")
}

func (r RealProber) GitUserName() string  { return r.run("git", "config", "--get", "user.name") }
func (r RealProber) GitUserEmail() string { return r.run("git", "config", "--get", "user.email") }
func (r RealProber) GitOriginURL() string { return r.run("git", "remote", "get-url", "origin") }

func (r RealProber) HasGcloud() bool { return r.exists("gcloud") }
func (r RealProber) HasDocker() bool { return r.exists("docker") }

func (r RealProber) HasGcloudAuth() bool {
	return r.run("gcloud", "auth", "list", "--filter=status:ACTIVE", "--format=value(account)") != ""
}

func (r RealProber) BillingEnabled(projectID string) bool {
	out := r.run("gcloud", "beta", "billing", "projects", "describe", projectID,
		"--format=value(billingEnabled)")
	return strings.EqualFold(out, "true")
}

// GcloudRegionWithFallback returns the probed region, or "us-central1" if empty.
func GcloudRegionWithFallback(p Prober) string {
	if r := p.GcloudRegion(); r != "" {
		return r
	}
	return "us-central1"
}

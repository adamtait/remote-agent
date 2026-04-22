// ws/internal/config/merge_test.go
package config_test

import (
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/internal/config"
)

func TestMerge_HardcodedDefaults(t *testing.T) {
	project := &config.ProjectConfig{ProjectID: "proj"}
	global := &config.GlobalConfig{}
	cfg, err := config.Merge(project, global)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MachineType != "e2-standard-4" {
		t.Errorf("machine_type: got %q, want e2-standard-4", cfg.MachineType)
	}
	if cfg.DiskSizeGB != 50 {
		t.Errorf("disk_size_gb: got %d, want 50", cfg.DiskSizeGB)
	}
	if cfg.ImageTag != "latest" {
		t.Errorf("image_tag: got %q, want latest", cfg.ImageTag)
	}
	if cfg.ClusterName != "claude-dev-cluster" {
		t.Errorf("cluster_name: got %q, want claude-dev-cluster", cfg.ClusterName)
	}
	if cfg.ConfigName != "claude-dev-config" {
		t.Errorf("config_name: got %q, want claude-dev-config", cfg.ConfigName)
	}
	if cfg.ServiceAccountName != "claude-workstation-sa" {
		t.Errorf("service_account_name: got %q, want claude-workstation-sa", cfg.ServiceAccountName)
	}
}

func TestMerge_ProjectConfigOverridesDefaults(t *testing.T) {
	project := &config.ProjectConfig{
		ProjectID:   "my-proj",
		Region:      "europe-west1",
		ClusterName: "my-cluster",
		MachineType: "e2-standard-8",
		DiskSizeGB:  100,
	}
	global := &config.GlobalConfig{}
	cfg, err := config.Merge(project, global)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Region != "europe-west1" {
		t.Errorf("region: got %q, want europe-west1", cfg.Region)
	}
	if cfg.ClusterName != "my-cluster" {
		t.Errorf("cluster_name: got %q, want my-cluster", cfg.ClusterName)
	}
	if cfg.MachineType != "e2-standard-8" {
		t.Errorf("machine_type: got %q, want e2-standard-8", cfg.MachineType)
	}
}

func TestMerge_GlobalProjectOverrideWinsOverProjectConfig(t *testing.T) {
	project := &config.ProjectConfig{ProjectID: "my-proj"}
	global := &config.GlobalConfig{
		Projects: map[string]config.GlobalProjectOverride{
			"my-proj": {ImageName: "custom-image", ImageTag: "v2"},
		},
	}
	cfg, err := config.Merge(project, global)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ImageName != "custom-image" {
		t.Errorf("image_name: got %q, want custom-image", cfg.ImageName)
	}
	if cfg.ImageTag != "v2" {
		t.Errorf("image_tag: got %q, want v2", cfg.ImageTag)
	}
}

func TestMerge_GlobalDefaultsForGitIdentity(t *testing.T) {
	project := &config.ProjectConfig{ProjectID: "p"}
	global := &config.GlobalConfig{
		Defaults: config.GlobalDefaults{
			GitUserName:  "Alice",
			GitUserEmail: "alice@example.com",
		},
	}
	cfg, err := config.Merge(project, global)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GitUserName != "Alice" {
		t.Errorf("git_user_name: got %q, want Alice", cfg.GitUserName)
	}
}

func TestWorkstationName_Sanitized(t *testing.T) {
	cases := []struct {
		projectID  string
		username   string
		wantPrefix string
	}{
		{"my-project", "adam", "my-project-adam"},
		{"My_Project!", "Adam.Tait", "my-project-adam-tait"},
		{"proj", "user", "proj-user"},
	}
	for _, tc := range cases {
		got := config.WorkstationName(tc.projectID, tc.username)
		if !strings.HasPrefix(got, tc.wantPrefix) {
			t.Errorf("WorkstationName(%q, %q) = %q, want prefix %q", tc.projectID, tc.username, got, tc.wantPrefix)
		}
		if len(got) > 63 {
			t.Errorf("WorkstationName result too long: %d chars", len(got))
		}
	}
}

func TestWorkstationName_TruncatedAt63(t *testing.T) {
	long := strings.Repeat("a", 40)
	name := config.WorkstationName(long, long)
	if len(name) > 63 {
		t.Errorf("expected ≤63 chars, got %d", len(name))
	}
}

func TestMerge_AutoSleepFromGlobalDefaults(t *testing.T) {
	h := 2
	project := &config.ProjectConfig{ProjectID: "p"}
	global := &config.GlobalConfig{
		Defaults: config.GlobalDefaults{AutoSleepHours: &h},
	}
	cfg, err := config.Merge(project, global)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AutoSleepHours == nil || *cfg.AutoSleepHours != 2 {
		t.Errorf("AutoSleepHours: got %v, want &2", cfg.AutoSleepHours)
	}
}

func TestMerge_AutoSleepNeverSleep(t *testing.T) {
	zero := 0
	project := &config.ProjectConfig{ProjectID: "p", AutoSleepHours: &zero}
	global := &config.GlobalConfig{}
	cfg, err := config.Merge(project, global)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AutoSleepHours == nil || *cfg.AutoSleepHours != 0 {
		t.Errorf("AutoSleepHours: got %v, want &0 (never sleep)", cfg.AutoSleepHours)
	}
}

func TestMerge_AutoShutdownFromProjectConfig(t *testing.T) {
	project := &config.ProjectConfig{ProjectID: "p", AutoShutdownHours: 24}
	global := &config.GlobalConfig{}
	cfg, err := config.Merge(project, global)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AutoShutdownHours != 24 {
		t.Errorf("AutoShutdownHours: got %d, want 24", cfg.AutoShutdownHours)
	}
}

func TestMerge_ProjectConfigOverridesGlobalDefaultsForAutoSleep(t *testing.T) {
	projectH := 1
	globalH := 4
	project := &config.ProjectConfig{ProjectID: "p", AutoSleepHours: &projectH}
	global := &config.GlobalConfig{
		Defaults: config.GlobalDefaults{AutoSleepHours: &globalH},
	}
	cfg, err := config.Merge(project, global)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AutoSleepHours == nil || *cfg.AutoSleepHours != 1 {
		t.Errorf("AutoSleepHours: got %v, want &1 (project should override global default)", cfg.AutoSleepHours)
	}
}

func TestMerge_GlobalProjectOverrideWinsForAutoShutdown(t *testing.T) {
	project := &config.ProjectConfig{ProjectID: "my-proj", AutoShutdownHours: 12}
	global := &config.GlobalConfig{
		Projects: map[string]config.GlobalProjectOverride{
			"my-proj": {AutoShutdownHours: 48},
		},
	}
	cfg, err := config.Merge(project, global)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AutoShutdownHours != 48 {
		t.Errorf("AutoShutdownHours: got %d, want 48 (global override should win)", cfg.AutoShutdownHours)
	}
}

func TestMerge_NilAutoSleepDoesNotOverrideNonNil(t *testing.T) {
	globalH := 3
	project := &config.ProjectConfig{ProjectID: "p"} // AutoSleepHours nil
	global := &config.GlobalConfig{
		Defaults: config.GlobalDefaults{AutoSleepHours: &globalH},
	}
	cfg, err := config.Merge(project, global)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AutoSleepHours == nil || *cfg.AutoSleepHours != 3 {
		t.Errorf("AutoSleepHours: got %v, want &3 (nil in project must not overwrite global default)", cfg.AutoSleepHours)
	}
}

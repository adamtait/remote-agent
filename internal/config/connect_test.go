package config

import "testing"

func TestMerge_ConnectLoginCommand_ProjectOverridesUser(t *testing.T) {
	u := &UserConfig{Connect: ConnectConfig{LoginCommand: "user-cmd"}}

	// project value wins when set
	cfg, err := Merge(u, &ProjectConfig{Connect: ConnectConfig{LoginCommand: "proj-cmd"}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Connect.LoginCommand != "proj-cmd" {
		t.Errorf("project override: got %q, want proj-cmd", cfg.Connect.LoginCommand)
	}

	// falls back to user when project is empty
	cfg, err = Merge(u, &ProjectConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Connect.LoginCommand != "user-cmd" {
		t.Errorf("user fallback: got %q, want user-cmd", cfg.Connect.LoginCommand)
	}

	// unset everywhere → empty (plain login shell)
	cfg, err = Merge(&UserConfig{}, &ProjectConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Connect.LoginCommand != "" {
		t.Errorf("unset: got %q, want empty", cfg.Connect.LoginCommand)
	}
}

func TestValidate_ConnectLoginCommand_RejectsMultiline(t *testing.T) {
	cfg := fullyValidConfig()
	cfg.Connect.LoginCommand = "a\nb"
	if err := Validate(cfg, nil); err == nil {
		t.Fatal("expected multi-line login_command to fail validation")
	}
}

// fullyValidConfig returns a Config that passes Validate, so a test can flip one
// field to assert a single rule.
func fullyValidConfig() *Config {
	sleep := 3
	return &Config{
		Git: GitConfig{UserName: "u", UserEmail: "e@x"},
		Gcloud: GcloudConfig{Project: "p", Workstation: WorkstationConfig{
			Region: "r", MachineType: "m", DiskSizeGB: 10, ClusterName: "cl",
			ConfigName: "cfg", ServiceAccountName: "sa", ImageName: "img",
			ImageTag: "t", RepoName: "repo", AutoSleepHours: &sleep, AutoShutdownHours: 10,
		}},
	}
}

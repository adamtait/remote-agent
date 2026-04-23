// ws/internal/scaffold/templates_compat.go
// Temporary stubs for types/functions removed from templates.go.
// Task 8 will rewrite scaffold.go and delete this file.
package scaffold

// ProbedValues carries values discovered from the user's environment.
// Kept temporarily so scaffold.go compiles; Task 8 removes it.
type ProbedValues struct {
	ProjectID     string
	Region        string
	GitUserName   string
	GitUserEmail  string
	GithubRepoURL string
}

// UserProjectValues holds project-config values the user explicitly chose.
// Kept temporarily so scaffold.go compiles; Task 8 removes it.
type UserProjectValues struct {
	ProjectID     string
	GithubRepoURL string
	Region        string
	ClusterName   string
	ConfigName    string
	MachineType   string
	DiskSizeGB    string
}

// UserGlobalValues holds global-config values the user explicitly chose.
// Kept temporarily so scaffold.go compiles; Task 8 removes it.
type UserGlobalValues struct {
	Region       string
	GitUserName  string
	GitUserEmail string
}

// RenderProjectTemplate is a stub; Task 8 replaces calls with RenderProjectConfig.
func RenderProjectTemplate(_ ProbedValues, _ UserProjectValues) string { return "" }

// RenderGlobalTemplate is a stub; Task 8 replaces calls with RenderUserConfig.
func RenderGlobalTemplate(_ ProbedValues, _ UserGlobalValues) string { return "" }

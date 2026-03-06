package gradle

const (
	SourceStatusAttached          = "attached"
	SourceStatusDownloaded        = "downloaded"
	SourceStatusNotFound          = "not_found"
	SourceStatusUnresolvedVersion = "unresolved_version"
	SourceStatusDownloadFailed    = "download_failed"

	ResolutionTypeDeclared = "declared"
	ResolutionTypeResolved = "resolved"

	DependencyKindDirect     = "direct"
	DependencyKindTransitive = "transitive"
)

type Module struct {
	Name string
	Path string
}

type Dependency struct {
	ModuleName     string
	GroupID        string
	ArtifactID     string
	Version        string
	Scope          string
	Type           string
	Kind           string
	BinaryJarPath  string
	SourceJarPath  string
	SourceStatus   string
	ResolutionType string
	MetadataJSON   string
	Confidence     float64
}

type SourceOptions struct {
	FetchMissingSources bool
	Offline             bool
	DownloadTimeoutSec  int
	ExtraSourceDir      string
}

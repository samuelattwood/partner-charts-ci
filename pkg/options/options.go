package options

import (
	"github.com/rancher/charts-build-scripts/pkg/charts"

	"helm.sh/helm/v3/pkg/chart"
)

const (
	PackageOptionsFile    = "package.yaml"
	UpstreamOptionsFile   = "upstream.yaml"
	IndexFile             = "index.yaml"
	RepositoryAssetsDir   = "assets"
	RepositoryChartsDir   = "charts"
	RepositoryPackagesDir = "packages"
)

type ChartSourceMetadata struct {
	DisplayName string
	FileName    string
	Name        string
	PackageYaml PackageYaml
	Source      string
	Url         string
	Vendor      string
	Version     string
}

type PackageWrapper struct {
	Name           string
	Path           string
	Package        *charts.Package
	SourceMetadata *ChartSourceMetadata
	UpstreamYaml   *UpstreamYaml
}
type PackageYaml struct {
	Commit         string `json:"commit,omitempty"`
	PackageVersion string `json:"packageVersion,omitempty"`
	SubDirectory   string `json:"subdirectory,omitempty"`
	Url            string `json:"url"`
}

type UpstreamYaml struct {
	AHPackageName   string         `json:"ArtifactHubPackage"`
	AHRepoName      string         `json:"ArtifactHubRepo"`
	ChartYaml       chart.Metadata `json:"Chart.yaml"`
	DisplayName     string         `json:"DisplayName"`
	GitBranch       string         `json:"GitBranch"`
	GitHubRelease   bool           `json:"GitHubRelease"`
	GitRepoUrl      string         `json:"GitRepo"`
	GitSubDirectory string         `json:"GitSubdirectory"`
	HelmChart       string         `json:"HelmChart"`
	HelmRepoUrl     string         `json:"HelmRepo"`
	ReleaseName     string         `json:"ReleaseName"`
	Vendor          string         `json:"Vendor"`
}

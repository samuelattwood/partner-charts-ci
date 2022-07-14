package options

import (
	"helm.sh/helm/v3/pkg/chart"
)

const (
	PackageOptionsFile    = "package.yaml"
	UpstreamOptionsFile   = "upstream.yaml"
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
	GitHubRelease   bool           `json:"GitHubRelease`
	GitRepoUrl      string         `json:"GitRepo"`
	GitSubDirectory string         `json:"GitSubdirectory"`
	HelmChart       string         `json:"HelmChart"`
	HelmRepoUrl     string         `json:"HelmRepo"`
	ReleaseName     string         `json:"ReleaseName"`
	Vendor          string         `json:"Vendor"`
}

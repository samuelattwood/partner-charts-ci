package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"

	"github.com/go-git/go-git/v5"
	"github.com/rancher/charts-build-scripts/pkg/charts"
	"github.com/sirupsen/logrus"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/repo"

	"sigs.k8s.io/yaml"
)

const (
	artifactHubApi        = "https://artifacthub.io/api/v1/packages/helm"
	repositoryPackagesDir = "packages"
	repositoryChartsDir   = "charts"
	packageOptionsFile    = "package.yaml"
	upstreamOptionsFile   = "upstream.yaml"
)

type ArtifactHubApiHelmRepo struct {
	DisplayName    string `json:"display_name,omitempty"`
	Name           string `json:"name"`
	OrgDisplayName string `json:"organization_display_name,omitempty"`
	OrgName        string `json:"organization_name,omitempty"`
	Url            string `json:"url"`
}

type ArtifactHubApiHelm struct {
	ContentUrl     string                 `json:"content_url"`
	Name           string                 `json:"name"`
	NormalizedName string                 `json:"normalized_name"`
	Repository     ArtifactHubApiHelmRepo `json:"repository"`
	Version        string                 `json:"version"`
}

type ChartSourceMetadata struct {
	DisplayName string
	FileName    string
	Name        string
	Org         string
	PackageYaml PackageYaml
	Source      string
	Url         string
	Version     string
}

type PackageYaml struct {
	Url            string `json:"url"`
	SubDirectory   string `json:"subdirectory,omitempty"`
	Commit         string `json:"commit,omitempty"`
	PackageVersion string `json:"packageVersion,omitempty"`
}

type UpstreamYaml struct {
	ChartYaml       chart.Metadata `json:"Chart.yaml"`
	DisplayName     string         `json:"DisplayName"`
	AHPackageName   string         `json:"ArtifactHubPackage"`
	AHRepoName      string         `json:"ArtifactHubRepo"`
	HelmRepoUrl     string         `json:"HelmRepo"`
	HelmRepoChart   string         `json:"Chart"`
	GitRepoUrl      string         `json:"GitRepo"`
	GitBranch       string         `json:"GitBranch"`
	GitSubDirectory string         `json:"GitSubdirectory"`
	ReleaseName     string         `json:"ReleaseName"`
}

func getRepoRoot() string {
	repoRoot, err := os.Getwd()
	if err != nil {
		logrus.Debug(err)
	}

	return repoRoot
}

func fetch_upstream_helmrepo(upstreamYaml UpstreamYaml) ChartSourceMetadata {
	url := fmt.Sprintf("%s/index.yaml", upstreamYaml.HelmRepoUrl)

	indexYaml := repo.NewIndexFile()
	chartSourceMeta := ChartSourceMetadata{}

	chartSourceMeta.Source = "Helm Repo"

	resp, err := http.Get(url)
	if err != nil {
		logrus.Debug(err)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		logrus.Debug(err)
	}

	err = yaml.Unmarshal([]byte(body), indexYaml)
	if err != nil {
		logrus.Debug(err)
	}

	indexYaml.SortEntries()

	chartEntries := indexYaml.Entries[upstreamYaml.HelmRepoChart]
	latestEntry := chartEntries[0]

	chartSourceMeta.Name = latestEntry.Metadata.Name
	chartSourceMeta.DisplayName = latestEntry.Metadata.Name
	logrus.Infoln(latestEntry.URLs[0])
	chartSourceMeta.Url = latestEntry.URLs[0]
	logrus.Infoln(latestEntry.Version)
	chartSourceMeta.Version = latestEntry.Version
	chartSourceMeta.PackageYaml = PackageYaml{
		Url: chartSourceMeta.Url,
	}

	return chartSourceMeta
}

func fetch_upstream_artifacthub(upstreamYaml UpstreamYaml) ChartSourceMetadata {
	url := fmt.Sprintf("%s/%s/%s", artifactHubApi, upstreamYaml.AHRepoName, upstreamYaml.AHPackageName)

	apiResp := ArtifactHubApiHelm{}
	chartSourceMeta := ChartSourceMetadata{}

	chartSourceMeta.Source = "ArtifactHub"

	resp, err := http.Get(url)
	if err != nil {
		logrus.Debug(err)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		logrus.Debug(err)
	}

	json.Unmarshal([]byte(body), &apiResp)

	chartSourceMeta.DisplayName = apiResp.Name
	chartSourceMeta.Name = apiResp.NormalizedName
	chartSourceMeta.Org = apiResp.Repository.OrgName
	chartSourceMeta.Url = apiResp.ContentUrl
	chartSourceMeta.Version = apiResp.Version
	chartSourceMeta.PackageYaml = PackageYaml{
		Url: chartSourceMeta.Url,
	}

	return chartSourceMeta
}

func fetch_upstream_git(packageName string, upstreamYaml UpstreamYaml) ChartSourceMetadata {
	cloneOptions := git.CloneOptions{}
	cloneOptions.URL = upstreamYaml.GitRepoUrl
	cloneOptions.Depth = 1
	if upstreamYaml.GitBranch != "" {
		cloneOptions.RemoteName = upstreamYaml.GitBranch
	}

	gitDirectory := getRepoRoot() + "/" + repositoryPackagesDir + "/" + packageName + "/clone"
	r, err := git.PlainClone(gitDirectory, false, &cloneOptions)
	if err != nil {
		logrus.Debug(err)
	}

	ref, err := r.Head()
	if err != nil {
		logrus.Debug(err)
	}

	sourcePath := gitDirectory
	if upstreamYaml.GitSubDirectory != "" {
		sourcePath = sourcePath + "/" + upstreamYaml.GitSubDirectory
	}
	helmChart, err := loader.Load(sourcePath)
	if err != nil {
		logrus.Debug(err)
	}

	chartSourceMeta := ChartSourceMetadata{
		DisplayName: helmChart.Metadata.Name,
		Name:        helmChart.Metadata.Name,
		Url:         upstreamYaml.GitRepoUrl,
		Version:     helmChart.Metadata.Version,
		Source:      "Git",
		PackageYaml: PackageYaml{
			Url:          upstreamYaml.GitRepoUrl,
			Commit:       ref.Hash().String(),
			SubDirectory: upstreamYaml.GitSubDirectory,
		},
	}

	err = os.RemoveAll(gitDirectory)
	if err != nil {
		logrus.Debug(err)
	}

	return chartSourceMeta

}

func fetch_upstream(packageName string, upstreamYaml UpstreamYaml) (ChartSourceMetadata, error) {
	if upstreamYaml.AHPackageName != "" && upstreamYaml.AHRepoName != "" {
		return fetch_upstream_artifacthub(upstreamYaml), nil
	} else if upstreamYaml.HelmRepoUrl != "" {
		return fetch_upstream_helmrepo(upstreamYaml), nil
	} else if upstreamYaml.GitRepoUrl != "" {
		return fetch_upstream_git(packageName, upstreamYaml), nil
	} else {
		err := errors.New("no repo url Found")
		return ChartSourceMetadata{}, err
	}
}

func overlayChartMetadata(chartMetadata chart.Metadata, overlay chart.Metadata) *chart.Metadata {
	if overlay.Name != "" {
		chartMetadata.Name = overlay.Name
	}
	if overlay.Home != "" {
		chartMetadata.Home = overlay.Home
	}
	if overlay.Sources != nil {
		chartMetadata.Sources = overlay.Sources
	}
	if overlay.Version != "" {
		chartMetadata.Version = overlay.Version
	}
	if overlay.Description != "" {
		chartMetadata.Description = overlay.Description
	}
	if overlay.Keywords != nil {
		chartMetadata.Keywords = overlay.Keywords
	}
	if overlay.Maintainers != nil {
		chartMetadata.Maintainers = overlay.Maintainers
	}
	if overlay.Icon != "" {
		chartMetadata.Icon = overlay.Icon
	}
	if overlay.APIVersion != "" {
		chartMetadata.APIVersion = overlay.APIVersion
	}
	if overlay.Condition != "" {
		chartMetadata.Condition = overlay.Condition
	}
	if overlay.Tags != "" {
		chartMetadata.Tags = overlay.Tags
	}
	if overlay.AppVersion != "" {
		chartMetadata.AppVersion = overlay.AppVersion
	}
	if overlay.Deprecated {
		chartMetadata.Deprecated = overlay.Deprecated
	}
	if overlay.Annotations != nil {
		chartMetadata.Annotations = overlay.Annotations
	}
	if overlay.KubeVersion != "" {
		chartMetadata.KubeVersion = overlay.KubeVersion
	}
	if overlay.Dependencies != nil {
		chartMetadata.Dependencies = overlay.Dependencies
	}
	if overlay.Type != "" {
		chartMetadata.Type = overlay.Type
	}

	return &chartMetadata
}

func parseUpstreamYaml(filePath string) (UpstreamYaml, error) {
	upstreamYamlFile, err := ioutil.ReadFile(filePath)
	upstreamYaml := UpstreamYaml{}
	if err != nil {
		logrus.Debug(err)
	} else {
		err = yaml.Unmarshal(upstreamYamlFile, &upstreamYaml)
	}

	return upstreamYaml, err
}

func loadAndOverlayChart(packageName string, upstreamYaml UpstreamYaml) (*chart.Chart, error) {
	packagePath := getRepoRoot() + "/" + repositoryPackagesDir + "/" + packageName
	chartSourceMeta, err := fetch_upstream(packagePath, upstreamYaml)
	if err != nil {
		err := errors.New("package yaml does not contain required values")
		return nil, err
	}
	createPackageYaml(packagePath, chartSourceMeta)
	packages, _ := charts.GetPackages(getRepoRoot(), packageName)
	err = packages[0].Prepare()
	if err != nil {
		logrus.Errorln("Chart prepare failed. Cleaning up and skipping...")
		packages[0].Clean()
		return nil, err
	}

	chartSourceMeta.FileName = packagePath + "/" + repositoryChartsDir

	if _, err := os.Stat(chartSourceMeta.FileName + "/Chart.yaml.orig"); !os.IsNotExist(err) {
		os.Remove(chartSourceMeta.FileName + "/Chart.yaml.orig")
	}

	logrus.Infof("\n  Source: %s\n  Organization: %s\n  Chart: %s\n  Version: %s\n  URL: %s  \n",
		chartSourceMeta.Source, chartSourceMeta.Org, chartSourceMeta.Name, chartSourceMeta.Version, chartSourceMeta.Url)

	helmChart, err := loader.Load(chartSourceMeta.FileName)
	if err != nil {
		logrus.Debug(err)
	}

	helmChart.Metadata = overlayChartMetadata(*helmChart.Metadata, upstreamYaml.ChartYaml)

	err = packages[0].GeneratePatch()
	if err != nil {
		logrus.Debug(err)
	}
	/*
		err = packages[0].Clean()
		if err != nil {
			logrus.Debug(err)
		}
	*/

	err = packages[0].GenerateCharts(true)

	if helmChart.Metadata.Annotations == nil {
		helmChart.Metadata.Annotations = make(map[string]string)
	}

	if upstreamYaml.DisplayName != "" {
		chartSourceMeta.DisplayName = upstreamYaml.DisplayName
	}
	if upstreamYaml.ReleaseName != "" {
		chartSourceMeta.Name = upstreamYaml.ReleaseName
	}

	if _, ok := helmChart.Metadata.Annotations["catalog.cattle.io/certified"]; !ok {
		helmChart.Metadata.Annotations["catalog.cattle.io/certified"] = "partner"
	}
	if _, ok := helmChart.Metadata.Annotations["catalog.cattle.io/display-name"]; !ok {
		helmChart.Metadata.Annotations["catalog.cattle.io/display-name"] = chartSourceMeta.DisplayName
	}
	if _, ok := helmChart.Metadata.Annotations["catalog.cattle.io/release-name"]; !ok {
		helmChart.Metadata.Annotations["catalog.cattle.io/release-name"] = chartSourceMeta.Name
	}

	return helmChart, err
}

func fetchPackages(packageList []string) {
	skipped := make([]string, 0)
	for _, currentPackage := range packageList {
		packagePath := repositoryPackagesDir + "/" + currentPackage
		upstreamYamlPath := packagePath + "/" + upstreamOptionsFile
		if _, err := os.Stat(upstreamYamlPath); os.IsNotExist(err) {
			continue
		}
		logrus.Infof("Parsing %s\n", currentPackage)

		upstreamYaml, err := parseUpstreamYaml(upstreamYamlPath)
		if err != nil {
			logrus.Debug(err)
			continue
		}

		helmChart, err := loadAndOverlayChart(currentPackage, upstreamYaml)
		if err != nil {
			logrus.Debug(err)
			skipped = append(skipped, currentPackage)
			continue
		}

		err = chartutil.SaveDir(helmChart, packagePath)
		if err != nil {
			logrus.Debug(err)
			continue
		}

		sourcePath := packagePath + "/" + helmChart.Metadata.Name
		targetPath := packagePath + "/" + repositoryChartsDir
		if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
			os.RemoveAll(targetPath)
		}
		err = os.Rename(sourcePath, targetPath)
		if err != nil {
			logrus.Debug(err)
		}
	}
	if len(skipped) > 0 {
		logrus.Errorf("Skipped due to error: %v", skipped)
	}
}

func createPackageYaml(packagePath string, chartSourceMeta ChartSourceMetadata) {
	filePath := packagePath + "/" + packageOptionsFile
	packageYaml, err := yaml.Marshal(&chartSourceMeta.PackageYaml)
	if err != nil {
		logrus.Debug(err)
	}

	err = ioutil.WriteFile(filePath, packageYaml, 0644)
	if err != nil {
		logrus.Debug(err)
	}
}

func main() {
	packageList, err := charts.ListPackages(getRepoRoot(), "")
	if err != nil {
		logrus.Debug(err)
	}

	fetchPackages(packageList)
}

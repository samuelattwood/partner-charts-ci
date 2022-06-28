package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/rancher/charts-build-scripts/pkg/charts"
	"github.com/sirupsen/logrus"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/repo"

	"sigs.k8s.io/yaml"
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
	Source      string
	Url         string
	Version     string
}

type PackageYaml struct {
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

func fetch_upstream_helmrepo(packagePath string, packageYaml PackageYaml) ChartSourceMetadata {
	url := fmt.Sprintf("%s/index.yaml", packageYaml.HelmRepoUrl)

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

	chartEntries := indexYaml.Entries[packageYaml.HelmRepoChart]
	latestEntry := chartEntries[0]

	chartSourceMeta.Name = latestEntry.Metadata.Name
	chartSourceMeta.DisplayName = latestEntry.Metadata.Name
	fmt.Println(latestEntry.URLs[0])
	chartSourceMeta.Url = latestEntry.URLs[0]
	fmt.Println(latestEntry.Version)
	chartSourceMeta.Version = latestEntry.Version

	contentResp, err := http.Get(chartSourceMeta.Url)
	if err != nil {
		logrus.Debug(err)
	}

	defer contentResp.Body.Close()

	chartUrlSplit := strings.Split(chartSourceMeta.Url, "/")
	chartSourceMeta.FileName = packagePath + chartUrlSplit[len(chartUrlSplit)-1]

	chartTgz, err := os.Create(chartSourceMeta.FileName)
	if err != nil {
		logrus.Debug(err)
	}

	defer chartTgz.Close()

	_, err = io.Copy(chartTgz, contentResp.Body)
	if err != nil {
		logrus.Debug(err)
	}

	return chartSourceMeta
}

func fetch_upstream_artifacthub(packagePath string, packageYaml PackageYaml) ChartSourceMetadata {
	url := fmt.Sprintf("https://artifacthub.io/api/v1/packages/helm/%s/%s", packageYaml.AHRepoName, packageYaml.AHPackageName)

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

	contentResp, err := http.Get(chartSourceMeta.Url)
	if err != nil {
		logrus.Debug(err)
	}

	defer contentResp.Body.Close()

	chartUrlSplit := strings.Split(chartSourceMeta.Url, "/")
	chartSourceMeta.FileName = packagePath + chartUrlSplit[len(chartUrlSplit)-1]

	chartTgz, err := os.Create(chartSourceMeta.FileName)
	if err != nil {
		logrus.Debug(err)
	}

	defer chartTgz.Close()

	_, err = io.Copy(chartTgz, contentResp.Body)
	if err != nil {
		logrus.Debug(err)
	}

	return chartSourceMeta
}

func fetch_upstream_git(packagePath string, packageYaml PackageYaml) ChartSourceMetadata {
	cloneOptions := git.CloneOptions{}
	cloneOptions.URL = packageYaml.GitRepoUrl
	cloneOptions.Depth = 1
	if packageYaml.GitBranch != "" {
		cloneOptions.RemoteName = packageYaml.GitBranch
	}

	gitDirectory := packagePath + "clone/"
	_, err := git.PlainClone(gitDirectory, false, &cloneOptions)
	if err != nil {
		logrus.Debug(err)
	}

	sourcePath := gitDirectory
	targetPath := packagePath + "upstream"
	if packageYaml.GitSubDirectory != "" {
		sourcePath = sourcePath + packageYaml.GitSubDirectory
	}
	err = os.Rename(sourcePath, targetPath)
	if err != nil {
		logrus.Debug(err)
	}
	err = os.RemoveAll(gitDirectory)
	if err != nil {
		logrus.Debug(err)
	}

	chartSourceMeta := ChartSourceMetadata{}
	chartSourceMeta.FileName = targetPath

	helmChart, err := loader.Load(chartSourceMeta.FileName)
	if err != nil {
		logrus.Debug(err)
	}

	chartSourceMeta.DisplayName = helmChart.Metadata.Name
	chartSourceMeta.Name = helmChart.Metadata.Name
	chartSourceMeta.Url = packageYaml.GitRepoUrl
	chartSourceMeta.Version = helmChart.Metadata.Version
	chartSourceMeta.Source = "Git"

	return chartSourceMeta

}

func fetch_upstream(packagePath string, packageYaml PackageYaml) (ChartSourceMetadata, error) {
	if packageYaml.AHPackageName != "" && packageYaml.AHRepoName != "" {
		return fetch_upstream_artifacthub(packagePath, packageYaml), nil
	} else if packageYaml.HelmRepoUrl != "" {
		return fetch_upstream_helmrepo(packagePath, packageYaml), nil
	} else if packageYaml.GitRepoUrl != "" {
		return fetch_upstream_git(packagePath, packageYaml), nil
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

func parsePackageYaml(filePath string) (PackageYaml, error) {
	packageYamlFile, err := ioutil.ReadFile(filePath)
	packageYaml := PackageYaml{}
	if err != nil {
		logrus.Debug(err)
	} else {
		err = yaml.Unmarshal(packageYamlFile, &packageYaml)
	}

	return packageYaml, err
}

func loadAndOverlayChart(packagePath string, packageYaml PackageYaml) (*chart.Chart, error) {
	chartSourceMeta, err := fetch_upstream(packagePath, packageYaml)
	if err != nil {
		err := errors.New("package yaml does not contain required values")
		return nil, err
	}

	fmt.Printf("  Source: %s\n  Organization: %s\n  Chart: %s\n  Version: %s\n  URL: %s  \n",
		chartSourceMeta.Source, chartSourceMeta.Org, chartSourceMeta.Name, chartSourceMeta.Version, chartSourceMeta.Url)

	helmChart, err := loader.Load(chartSourceMeta.FileName)
	if err != nil {
		logrus.Debug(err)
	}

	if fi, _ := os.Stat(chartSourceMeta.FileName); fi.IsDir() {
		err = os.RemoveAll(chartSourceMeta.FileName)
	} else {
		err = os.Remove(chartSourceMeta.FileName)
	}

	helmChart.Metadata = overlayChartMetadata(*helmChart.Metadata, packageYaml.ChartYaml)

	if helmChart.Metadata.Annotations == nil {
		helmChart.Metadata.Annotations = make(map[string]string)
	}

	if packageYaml.DisplayName != "" {
		chartSourceMeta.DisplayName = packageYaml.DisplayName
	}
	if packageYaml.ReleaseName != "" {
		chartSourceMeta.Name = packageYaml.ReleaseName
	}

	if _, ok := helmChart.Metadata.Annotations["catalogrus.cattle.io/certified"]; !ok {
		helmChart.Metadata.Annotations["catalogrus.cattle.io/certified"] = "partner"
	}
	if _, ok := helmChart.Metadata.Annotations["catalogrus.cattle.io/display-name"]; !ok {
		helmChart.Metadata.Annotations["catalogrus.cattle.io/display-name"] = chartSourceMeta.DisplayName
	}
	if _, ok := helmChart.Metadata.Annotations["catalogrus.cattle.io/release-name"]; !ok {
		helmChart.Metadata.Annotations["catalogrus.cattle.io/release-name"] = chartSourceMeta.Name
	}

	return helmChart, err
}

func fetchPackages(packageList []string) {
	for _, currentPackage := range packageList {
		fmt.Printf("Parsing %s\n", currentPackage)
		packagePath := "packages/" + currentPackage + "/"
		packageYamlPath := packagePath + "package.yaml"

		packageYaml, err := parsePackageYaml(packageYamlPath)
		if err != nil {
			logrus.Debug(err)
			continue
		}

		helmChart, err := loadAndOverlayChart(packagePath, packageYaml)
		if err != nil {
			logrus.Debug(err)
			continue
		}

		err = chartutil.SaveDir(helmChart, packagePath)
		if err != nil {
			logrus.Debug(err)
			continue
		}

		sourcePath := packagePath + helmChart.Metadata.Name
		targetPath := packagePath + "charts"
		if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
			os.RemoveAll(targetPath)
		}
		err = os.Rename(sourcePath, targetPath)
		if err != nil {
			logrus.Debug(err)
		}
	}

}

func main() {
	workingDir, err := os.Getwd()
	if err != nil {
		logrus.Debug(err)
		os.Exit(1)
	}

	packageList, err := charts.ListPackages(workingDir, "")
	if err != nil {
		logrus.Debug(err)
	}

	fetchPackages(packageList)
}

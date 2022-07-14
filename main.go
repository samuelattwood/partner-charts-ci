package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rancher/charts-build-scripts/pkg/charts"
	"github.com/samuelattwood/partner-charts-ci/pkg/export"
	"github.com/samuelattwood/partner-charts-ci/pkg/fetcher"
	"github.com/samuelattwood/partner-charts-ci/pkg/options"
	"github.com/samuelattwood/partner-charts-ci/pkg/parse"
	"github.com/sirupsen/logrus"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/repo"
)

const (
	packageEnvVariable = "PACKAGE"
)

func getRepoRoot() string {
	repoRoot, err := os.Getwd()
	if err != nil {
		logrus.Debug(err)
	}

	return repoRoot
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

func loadAndOverlayChart(packageName string, upstreamYaml *options.UpstreamYaml) (*chart.Chart, error) {
	packagePath := filepath.Join(getRepoRoot(), options.RepositoryPackagesDir, packageName)
	chartSourceMeta, err := fetcher.FetchUpstream(upstreamYaml)
	if err != nil {
		return nil, err
	}
	parse.CreatePackageYaml(packagePath, &chartSourceMeta)
	packages, err := charts.GetPackages(getRepoRoot(), packageName)
	if err != nil {
		logrus.Warnln(err)
	}

	err = packages[0].Prepare()
	if err != nil {
		logrus.Errorln("Chart prepare failed. Cleaning up and skipping...")
		packages[0].Clean()
		return nil, err
	}

	chartSourceMeta.FileName = filepath.Join(packagePath, options.RepositoryChartsDir)

	if _, err := os.Stat(chartSourceMeta.FileName + "/Chart.yaml.orig"); !os.IsNotExist(err) {
		os.Remove(chartSourceMeta.FileName + "/Chart.yaml.orig")
	}

	logrus.Infof("\n  Source: %s\n  Vendor: %s\n  Chart: %s\n  Version: %s\n  URL: %s  \n",
		chartSourceMeta.Source, chartSourceMeta.Vendor, chartSourceMeta.Name, chartSourceMeta.Version, chartSourceMeta.Url)

	export.StandardizeChartDirectory(chartSourceMeta.FileName, "")
	helmChart, err := loader.Load(chartSourceMeta.FileName)
	if err != nil {
		logrus.Debug(err)
	}

	helmChart.Metadata = overlayChartMetadata(*helmChart.Metadata, upstreamYaml.ChartYaml)

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

	export.StandardizeChartDirectory(chartSourceMeta.FileName, "")

	err = packages[0].GeneratePatch()
	if err != nil {
		logrus.Errorln(err)
	}

	err = packages[0].Clean()
	if err != nil {
		logrus.Debug(err)
	}

	assetsPath := filepath.Join(getRepoRoot(), options.RepositoryAssetsDir, chartSourceMeta.Vendor)
	chartsPath := filepath.Join(getRepoRoot(), options.RepositoryChartsDir, chartSourceMeta.Vendor,
		helmChart.Metadata.Name, helmChart.Metadata.Version)
	indexFilePath := filepath.Join(getRepoRoot(), "index.yaml")

	_, err = chartutil.Save(helmChart, assetsPath)
	if err != nil {
		return helmChart, fmt.Errorf("unable to save chart to %s", assetsPath)
	}
	logrus.Info(chartsPath)
	export.ExportChartDirectory(helmChart, chartsPath)

	helmIndexYaml, _ := repo.LoadIndexFile(indexFilePath)
	newHelmIndexYaml, _ := repo.IndexDirectory(getRepoRoot()+"/"+options.RepositoryAssetsDir, options.RepositoryAssetsDir)
	helmIndexYaml.Merge(newHelmIndexYaml)
	helmIndexYaml.SortEntries()

	err = chartutil.SaveDir(helmChart, chartsPath)
	helmIndexYaml.WriteFile(getRepoRoot()+"/index.yaml", 0644)

	return helmChart, err
}

func fetchPackages(packageList []string) {
	skipped := make([]string, 0)
	for _, currentPackage := range packageList {
		packagePath := filepath.Join(options.RepositoryPackagesDir, currentPackage)
		upstreamYamlPath := filepath.Join(packagePath, options.UpstreamOptionsFile)
		if _, err := os.Stat(upstreamYamlPath); os.IsNotExist(err) {
			continue
		}
		logrus.Infof("Parsing %s\n", currentPackage)

		upstreamYaml, err := parse.ParseUpstreamYaml(upstreamYamlPath)

		if err != nil {
			logrus.Error(err)
			continue
		}

		_, err = loadAndOverlayChart(currentPackage, &upstreamYaml)
		if err != nil {
			logrus.Error(err)
			skipped = append(skipped, currentPackage)
			continue
		}

	}
	if len(skipped) > 0 {
		logrus.Errorf("Skipped due to error: %v", skipped)
	}
}

func main() {
	logrus.SetLevel(logrus.DebugLevel)
	currentPackage := os.Getenv(packageEnvVariable)
	packageDirectory := filepath.Join(getRepoRoot(), options.RepositoryPackagesDir)
	packageList, err := parse.ListPackages(packageDirectory, currentPackage)
	if err != nil {
		logrus.Debug(err)
	}

	fetchPackages(packageList)
}

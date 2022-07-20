package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/rancher/charts-build-scripts/pkg/charts"
	"github.com/samuelattwood/partner-charts-ci/pkg/export"
	"github.com/samuelattwood/partner-charts-ci/pkg/fetcher"
	"github.com/samuelattwood/partner-charts-ci/pkg/options"
	"github.com/samuelattwood/partner-charts-ci/pkg/parse"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"

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

func commitChanges() error {
	commitOptions := git.CommitOptions{
		Author: &object.Signature{
			Name:  "partner-charts-ci",
			Email: "partner-charts-ci@suse.com",
			When:  time.Now(),
		},
	}

	r, err := git.PlainOpen(getRepoRoot())
	if err != nil {
		return err
	}

	wt, err := r.Worktree()
	if err != nil {
		return err
	}

	wt.Add(options.IndexFile)
	wt.Add(options.RepositoryPackagesDir)
	wt.Add(options.RepositoryChartsDir)

	wt.Commit("Automated Update", &commitOptions)

	return nil
}

func applyChartAnnotations(chartMetadata *chart.Metadata, chartSourceMetadata *options.ChartSourceMetadata) {
	if chartMetadata.Annotations == nil {
		chartMetadata.Annotations = make(map[string]string)
	}

	if _, ok := chartMetadata.Annotations["catalog.cattle.io/certified"]; !ok {
		chartMetadata.Annotations["catalog.cattle.io/certified"] = "partner"
	}
	if _, ok := chartMetadata.Annotations["catalog.cattle.io/display-name"]; !ok {
		chartMetadata.Annotations["catalog.cattle.io/display-name"] = chartSourceMetadata.DisplayName
	}
	if _, ok := chartMetadata.Annotations["catalog.cattle.io/release-name"]; !ok {
		chartMetadata.Annotations["catalog.cattle.io/release-name"] = chartSourceMetadata.Name
	}
}

func overlayChartMetadata(chartMetadata *chart.Metadata, overlay chart.Metadata) {
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

}

func cleanPackage(packageName string, chartSourceMetadata *options.ChartSourceMetadata) (*charts.Package, error) {
	currentPackage, err := generatePackageFromMetadata(packageName, chartSourceMetadata)
	if err != nil {
		err = fmt.Errorf("unable to generate package from metadata")
		return nil, err
	}

	err = currentPackage.Clean()
	if err != nil {
		err = fmt.Errorf("unable to clean up package")
		return currentPackage, err
	}

	return currentPackage, nil
}

func patchPackage(packageName string, chartSourceMetadata *options.ChartSourceMetadata) (*charts.Package, error) {
	currentPackage, err := generatePackageFromMetadata(packageName, chartSourceMetadata)
	if err != nil {
		err = fmt.Errorf("unable to generate package from metadata")
		return nil, err
	}

	err = currentPackage.GeneratePatch()
	if err != nil {
		err = fmt.Errorf("unable to generate patch files")
		return currentPackage, err
	}

	return currentPackage, nil
}

func preparePackage(packageName string, chartSourceMetadata *options.ChartSourceMetadata) (*charts.Package, error) {
	currentPackage, err := generatePackageFromMetadata(packageName, chartSourceMetadata)
	if err != nil {
		err = fmt.Errorf("unable to generate package from metadata")
		return nil, err
	}

	err = currentPackage.Prepare()
	if err != nil {
		err = fmt.Errorf("unable to prepare package. cleaning up and skipping")
		currentPackage.Clean()
		return currentPackage, err
	}

	if _, err := os.Stat(chartSourceMetadata.FileName + "/Chart.yaml.orig"); !os.IsNotExist(err) {
		os.Remove(chartSourceMetadata.FileName + "/Chart.yaml.orig")
	}

	return currentPackage, nil
}

func generatePackageFromMetadata(packageName string, chartSourceMetadata *options.ChartSourceMetadata) (*charts.Package, error) {
	packagePath := filepath.Join(getRepoRoot(), options.RepositoryPackagesDir, packageName)

	parse.CreatePackageYaml(packagePath, chartSourceMetadata)
	packages, err := charts.GetPackages(getRepoRoot(), packageName)
	if err != nil {
		logrus.Debugln(err)
	}

	return packages[0], nil
}

func generateChartSourceMetadata(packageName string, upstreamYaml *options.UpstreamYaml) (*options.ChartSourceMetadata, error) {
	packagePath := filepath.Join(getRepoRoot(), options.RepositoryPackagesDir, packageName)
	chartSourceMeta, err := fetcher.FetchUpstream(upstreamYaml)
	if err != nil {
		return nil, err
	}
	chartSourceMeta.FileName = filepath.Join(packagePath, options.RepositoryChartsDir)

	logrus.Infof("\n  Source: %s\n  Vendor: %s\n  Chart: %s\n  Version: %s\n  URL: %s  \n",
		chartSourceMeta.Source, chartSourceMeta.Vendor, chartSourceMeta.Name, chartSourceMeta.Version, chartSourceMeta.Url)

	return &chartSourceMeta, nil
}

func loadAndOverlayChart(packageName string, upstreamYaml *options.UpstreamYaml) (*chart.Chart, error) {
	chartSourceMeta, err := generateChartSourceMetadata(packageName, upstreamYaml)
	if err != nil {
		return nil, err
	}
	currentPackage, err := preparePackage(packageName, chartSourceMeta)
	if err != nil {
		return nil, err
	}

	export.StandardizeChartDirectory(chartSourceMeta.FileName, "")

	helmChart, err := loader.Load(chartSourceMeta.FileName)
	if err != nil {
		logrus.Debug(err)
	}

	overlayChartMetadata(helmChart.Metadata, upstreamYaml.ChartYaml)

	if upstreamYaml.DisplayName != "" {
		chartSourceMeta.DisplayName = upstreamYaml.DisplayName
	}
	if upstreamYaml.ReleaseName != "" {
		chartSourceMeta.Name = upstreamYaml.ReleaseName
	}

	applyChartAnnotations(helmChart.Metadata, chartSourceMeta)

	export.StandardizeChartDirectory(chartSourceMeta.FileName, "")

	err = currentPackage.GeneratePatch()
	if err != nil {
		logrus.Debug(err)
	}

	err = currentPackage.Clean()
	if err != nil {
		logrus.Debug(err)
	}

	err = writeChart(helmChart, chartSourceMeta)
	if err != nil {
		logrus.Debug(err)
	}

	err = writeIndex()
	if err != nil {
		logrus.Debug(err)
	}

	return helmChart, err
}

func writeChart(helmChart *chart.Chart, chartSourceMetadata *options.ChartSourceMetadata) error {
	assetsPath := filepath.Join(getRepoRoot(), options.RepositoryAssetsDir, strings.ToLower(chartSourceMetadata.Vendor))
	chartsPath := filepath.Join(getRepoRoot(), options.RepositoryChartsDir, strings.ToLower(chartSourceMetadata.Vendor),
		helmChart.Metadata.Name, helmChart.Metadata.Version)
	_, err := chartutil.Save(helmChart, assetsPath)
	if err != nil {
		error := fmt.Errorf("unable to save chart to %s", assetsPath)
		return error
	}
	err = chartutil.SaveDir(helmChart, chartsPath)
	if err != nil {
		error := fmt.Errorf("unable to save chart to %s", chartsPath)
		return error
	}

	logrus.Info(chartsPath)
	export.ExportChartDirectory(helmChart, chartsPath)

	return nil
}

func writeIndex() error {
	indexFilePath := filepath.Join(getRepoRoot(), options.IndexFile)
	if _, err := os.Stat(indexFilePath); os.IsNotExist(err) {
		err = repo.NewIndexFile().WriteFile(indexFilePath, 0644)
		if err != nil {
			return err
		}
	}

	helmIndexYaml, err := repo.LoadIndexFile(indexFilePath)
	if err != nil {
		return err
	}
	newHelmIndexYaml, err := repo.IndexDirectory(getRepoRoot()+"/"+options.RepositoryAssetsDir, options.RepositoryAssetsDir)
	if err != nil {
		return err
	}
	helmIndexYaml.Merge(newHelmIndexYaml)
	helmIndexYaml.SortEntries()
	err = helmIndexYaml.WriteFile(indexFilePath, 0644)
	if err != nil {
		return err
	}

	return nil
}

func fetchUpstreams(upstreams map[string]options.UpstreamYaml) {
	skipped := make([]string, 0)
	for currentPackage, currentUpstream := range upstreams {
		_, err := loadAndOverlayChart(currentPackage, &currentUpstream)
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

func loadUpstreams(packageList []string) map[string]options.UpstreamYaml {
	upstreams := make(map[string]options.UpstreamYaml)
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
		upstreams[currentPackage] = upstreamYaml
	}
	return upstreams
}

func generatePackageList() []string {
	currentPackage := os.Getenv(packageEnvVariable)
	packageDirectory := filepath.Join(getRepoRoot(), options.RepositoryPackagesDir)
	packageList, err := parse.ListPackages(packageDirectory, currentPackage)
	if err != nil {
		logrus.Error(err)
	}

	return packageList
}

func listPackages(c *cli.Context) {
	packageList := generatePackageList()
	for _, pkg := range packageList {
		fmt.Println(pkg)
	}
}

func patchCharts(c *cli.Context) {
	packageList := generatePackageList()
	upstreams := loadUpstreams(packageList)
	for currentPackage, currentUpstream := range upstreams {
		chartSourceMeta, _ := generateChartSourceMetadata(currentPackage, &currentUpstream)
		patchPackage(currentPackage, chartSourceMeta)
	}
}

func cleanCharts(c *cli.Context) {
	packageList := generatePackageList()
	upstreams := loadUpstreams(packageList)
	for currentPackage, currentUpstream := range upstreams {
		chartSourceMeta, _ := generateChartSourceMetadata(currentPackage, &currentUpstream)
		cleanPackage(currentPackage, chartSourceMeta)
	}
}

func prepareCharts(c *cli.Context) {
	packageList := generatePackageList()
	upstreams := loadUpstreams(packageList)
	for currentPackage, currentUpstream := range upstreams {
		chartSourceMeta, _ := generateChartSourceMetadata(currentPackage, &currentUpstream)
		preparePackage(currentPackage, chartSourceMeta)
	}
}

func runGambit(c *cli.Context) {
	packageList := generatePackageList()
	upstreams := loadUpstreams(packageList)
	fetchUpstreams(upstreams)
	commitChanges()
}

func main() {
	if len(os.Getenv("DEBUG")) > 0 {
		logrus.SetLevel(logrus.DebugLevel)
	}

	app := cli.NewApp()
	app.Name = "partner-charts-ci"
	app.Usage = "Assists in submission and maintenance of partner Helm charts"

	app.Commands = []cli.Command{
		{
			Name:   "list",
			Usage:  "Print a list of all tracked upstreams in current repository",
			Action: listPackages,
		},
		{
			Name:   "prepare",
			Usage:  "Pull chart from upstream and prepare for alteration via patch",
			Action: prepareCharts,
		},
		{
			Name:   "patch",
			Usage:  "Generate patch files",
			Action: patchCharts,
		},
		{
			Name:   "clean",
			Usage:  "Clean up ephemeral chart directory",
			Action: cleanCharts,
		},
		{
			Name:   "run",
			Usage:  "Run full CI suite",
			Action: runGambit,
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		logrus.Fatal(err)
	}

}

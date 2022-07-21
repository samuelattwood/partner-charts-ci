package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/rancher/charts-build-scripts/pkg/charts"
	"github.com/rancher/charts-build-scripts/pkg/filesystem"
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
	commitAuthorName   = "partner-charts-ci"
	commitAuthorEmail  = "partner-charts-ci@suse.com"
	packageEnvVariable = "PACKAGE"
)

func getRepoRoot() string {
	repoRoot, err := os.Getwd()
	if err != nil {
		logrus.Fatal(err)
	}

	return repoRoot
}

func commitChanges() error {
	commitOptions := git.CommitOptions{
		Author: &object.Signature{
			Name:  commitAuthorName,
			Email: commitAuthorEmail,
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
	wt.Add(options.RepositoryAssetsDir)
	wt.Add(options.RepositoryChartsDir)
	wt.Add(options.RepositoryPackagesDir)

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

func cleanPackage(packageWrapper *options.PackageWrapper) error {
	err := generatePackageFromMetadata(packageWrapper)
	if err != nil {
		err = fmt.Errorf("unable to generate package from metadata for clean")
		return err
	}
	currentPackage := packageWrapper.Package
	err = currentPackage.Clean()
	if err != nil {
		err = fmt.Errorf("unable to clean up package")
		return err
	}

	return nil
}

func patchPackage(packageWrapper *options.PackageWrapper) error {
	err := generatePackageFromMetadata(packageWrapper)
	if err != nil {
		err = fmt.Errorf("unable to generate package from metadata for patch")
		return err
	}
	currentPackage := packageWrapper.Package

	err = currentPackage.GeneratePatch()
	if err != nil {
		err = fmt.Errorf("unable to generate patch files")
		return err
	}

	return nil
}

func preparePackage(packageWrapper *options.PackageWrapper) error {
	err := generatePackageFromMetadata(packageWrapper)
	if err != nil {
		err = fmt.Errorf("unable to generate package from metadata for prepare")
		return err
	}
	currentPackage := packageWrapper.Package

	err = currentPackage.Prepare()
	if err != nil {
		err = fmt.Errorf("unable to prepare package. cleaning up and skipping")
		currentPackage.Clean()
		return err
	}

	patchOrigPath := path.Join(packageWrapper.SourceMetadata.FileName, "Chart.yaml.orig")
	if _, err := os.Stat(patchOrigPath); !os.IsNotExist(err) {
		os.Remove(patchOrigPath)
	}

	return nil
}

func generatePackageFromMetadata(packageWrapper *options.PackageWrapper) error {
	parse.CreatePackageYaml(packageWrapper)
	packagesPath := filepath.Join(getRepoRoot(), options.RepositoryPackagesDir)
	packageRelativePath := strings.TrimPrefix(packageWrapper.Path, packagesPath)
	rootFs := filesystem.GetFilesystem(getRepoRoot())
	currentPackage, err := charts.GetPackage(rootFs, packageRelativePath)
	if err != nil {
		return err
	}

	packageWrapper.Package = currentPackage

	return nil
}

func generateChartSourceMetadata(packageWrapper *options.PackageWrapper) error {
	err := fetcher.FetchUpstream(packageWrapper)
	if err != nil {
		return err
	}
	packageWrapper.SourceMetadata.FileName = filepath.Join(packageWrapper.Path, options.RepositoryChartsDir)

	logrus.Infof("\n  Source: %s\n  Vendor: %s\n  Chart: %s\n  Version: %s\n  URL: %s  \n",
		packageWrapper.SourceMetadata.Source, packageWrapper.SourceMetadata.Vendor, packageWrapper.SourceMetadata.Name,
		packageWrapper.SourceMetadata.Version, packageWrapper.SourceMetadata.Url)

	return nil
}

func loadAndOverlayChart(packageWrapper *options.PackageWrapper) (*chart.Chart, error) {
	err := generateChartSourceMetadata(packageWrapper)
	if err != nil {
		return nil, err
	}
	err = preparePackage(packageWrapper)
	if err != nil {
		return nil, err
	}

	currentPackage := packageWrapper.Package
	chartSourceMeta := packageWrapper.SourceMetadata
	upstreamYaml := packageWrapper.UpstreamYaml

	export.StandardizeChartDirectory(chartSourceMeta.FileName, "")

	helmChart, err := loader.Load(chartSourceMeta.FileName)
	if err != nil {
		return nil, err
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
		return nil, err
	}

	err = currentPackage.Clean()
	if err != nil {
		logrus.Debug(err)
	}

	err = writeChart(helmChart, chartSourceMeta)
	if err != nil {
		return nil, err
	}

	err = writeIndex()
	if err != nil {
		return nil, err
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

func fetchUpstreams(packageWrapperList []*options.PackageWrapper) {
	skipped := make([]string, 0)
	for _, currentPackage := range packageWrapperList {
		_, err := loadAndOverlayChart(currentPackage)
		if err != nil {
			logrus.Error(err)
			skipped = append(skipped, currentPackage.Name)
			continue
		}
	}

	if len(skipped) > 0 {
		logrus.Errorf("Skipped due to error: %v", skipped)
	}
}

func parseUpstreams(packageList []*options.PackageWrapper) {
	for _, currentPackage := range packageList {
		upstreamYamlPath := filepath.Join(currentPackage.Path, options.UpstreamOptionsFile)
		if _, err := os.Stat(upstreamYamlPath); os.IsNotExist(err) {
			continue
		}
		logrus.Infof("Parsing %s\n", currentPackage.Name)

		upstreamYaml, err := parse.ParseUpstreamYaml(upstreamYamlPath)
		if err != nil {
			logrus.Error(err)
			continue
		}
		currentPackage.UpstreamYaml = &upstreamYaml
	}
}

func generatePackageList() []*options.PackageWrapper {
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
	for _, currentPackage := range packageList {
		fmt.Println(currentPackage.Name)
	}
}

func patchCharts(c *cli.Context) {
	packageList := generatePackageList()
	parseUpstreams(packageList)
	for _, currentPackage := range packageList {
		err := generateChartSourceMetadata(currentPackage)
		if err != nil {
			logrus.Error(err)
			continue
		}
		patchPackage(currentPackage)
	}
}

func cleanCharts(c *cli.Context) {
	packageList := generatePackageList()
	parseUpstreams(packageList)
	for _, currentPackage := range packageList {
		err := generateChartSourceMetadata(currentPackage)
		if err != nil {
			logrus.Error(err)
			continue
		}
		cleanPackage(currentPackage)
	}
}

func commitCharts(c *cli.Context) {
	commitChanges()
}

func prepareCharts(c *cli.Context) {
	packageList := generatePackageList()
	parseUpstreams(packageList)
	for _, currentPackage := range packageList {
		fmt.Println(currentPackage.Name)
		err := generateChartSourceMetadata(currentPackage)
		if err != nil {
			logrus.Error(err)
			continue
		}
		err = preparePackage(currentPackage)
		if err != nil {
			logrus.Error(err)
		}
	}
}

func runGambit(c *cli.Context) {
	packageList := generatePackageList()
	parseUpstreams(packageList)
	fetchUpstreams(packageList)
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
			Name:   "commit",
			Usage:  "Stage and commit changes",
			Action: commitCharts,
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

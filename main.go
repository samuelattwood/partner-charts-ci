package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/rancher/charts-build-scripts/pkg/charts"
	"github.com/rancher/charts-build-scripts/pkg/filesystem"
	"github.com/samuelattwood/partner-charts-ci/pkg/conform"
	"github.com/samuelattwood/partner-charts-ci/pkg/fetcher"
	"github.com/samuelattwood/partner-charts-ci/pkg/parse"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/repo"
)

const (
	commitAuthorEmail     = "partner-charts-ci@suse.com"
	commitAuthorName      = "partner-charts-ci"
	indexFile             = "index.yaml"
	packageEnvVariable    = "PACKAGE"
	repositoryAssetsDir   = "assets"
	repositoryChartsDir   = "charts"
	repositoryPackagesDir = "packages"
)

type PackageWrapper struct {
	Name           string
	Path           string
	Package        *charts.Package
	LatestStored   string
	PackageYaml    *parse.PackageYaml
	SourceMetadata *fetcher.ChartSourceMetadata
	UpstreamYaml   *parse.UpstreamYaml
}

func getRepoRoot() string {
	repoRoot, err := os.Getwd()
	if err != nil {
		logrus.Fatal(err)
	}

	return repoRoot
}

func commitChanges(updatedList []PackageWrapper) error {
	var additions, updates string
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

	wt.Add(indexFile)
	wt.Add(repositoryAssetsDir)
	wt.Add(repositoryChartsDir)
	wt.Add(repositoryPackagesDir)

	commitMessage := "CI Updated Charts"
	for _, pkg := range updatedList {
		lineItem := fmt.Sprintf("  - %s/%s (%s)\n",
			strings.ToLower(pkg.SourceMetadata.Vendor), pkg.Name, pkg.SourceMetadata.Version)
		if pkg.LatestStored == "" {
			additions += lineItem
		} else {
			updates += lineItem
		}
	}

	if additions != "" {
		commitMessage += fmt.Sprintf("\nAdded:\n%s", additions)
	}
	if updates != "" {
		commitMessage += fmt.Sprintf("\nUpdated:\n%s", updates)
	}

	wt.Commit(commitMessage, &commitOptions)

	return nil
}

func cleanPackage(packageWrapper PackageWrapper) error {
	var err error
	packageWrapper.PackageYaml, err = generatePackageYaml(packageWrapper.Path, *packageWrapper.SourceMetadata)
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

func patchPackage(packageWrapper PackageWrapper) error {
	var err error
	packageWrapper.PackageYaml, err = generatePackageYaml(packageWrapper.Path, *packageWrapper.SourceMetadata)
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

func preparePackage(packageWrapper PackageWrapper) error {
	var err error
	packageWrapper.PackageYaml, err = generatePackageYaml(packageWrapper.Path, *packageWrapper.SourceMetadata)
	if err != nil {
		err = fmt.Errorf("unable to generate package from metadata for prepare")
		return err
	}
	currentPackage := packageWrapper.Package

	conform.LinkOverlayFiles(packageWrapper.Path)

	err = currentPackage.Prepare()
	if err != nil {
		err = fmt.Errorf("unable to prepare package. cleaning up and skipping")
		currentPackage.Clean()
		return err
	}

	patchOrigPath := path.Join(packageWrapper.Path, repositoryChartsDir, "Chart.yaml.orig")
	if _, err := os.Stat(patchOrigPath); !os.IsNotExist(err) {
		os.Remove(patchOrigPath)
	}

	return nil
}

func generatePackage(packagePath string) (*charts.Package, error) {
	packagesPath := filepath.Join(getRepoRoot(), repositoryPackagesDir)
	packageRelativePath := strings.TrimPrefix(packagePath, packagesPath)
	rootFs := filesystem.GetFilesystem(getRepoRoot())
	pkg, err := charts.GetPackage(rootFs, packageRelativePath)
	if err != nil {
		return nil, err
	}

	return pkg, nil
}

func generatePackageYaml(packagePath string, sourceMetadata fetcher.ChartSourceMetadata) (*parse.PackageYaml, error) {
	packageYaml := parse.PackageYaml{
		Commit:       sourceMetadata.Commit,
		SubDirectory: sourceMetadata.SubDirectory,
		Url:          sourceMetadata.Url,
	}

	return &packageYaml, nil
}

func generateChartSourceMetadata(upstreamYaml parse.UpstreamYaml) (*fetcher.ChartSourceMetadata, error) {
	sourceMetadata, err := fetcher.FetchUpstream(upstreamYaml)
	if err != nil {
		return nil, err
	}

	return &sourceMetadata, nil
}

func initializeChart(packageWrapper PackageWrapper) (*chart.Chart, error) {
	err := preparePackage(packageWrapper)
	if err != nil {
		return nil, err
	}

	chartDirectoryPath := path.Join(packageWrapper.Path, repositoryChartsDir)
	conform.StandardizeChartDirectory(chartDirectoryPath, "")

	helmChart, err := loader.Load(chartDirectoryPath)
	if err != nil {
		return nil, err
	}

	return helmChart, nil
}

func conformChart(packageWrapper PackageWrapper) error {
	helmChart, err := initializeChart(packageWrapper)
	if err != nil {
		return err
	}

	conform.OverlayChartMetadata(helmChart.Metadata, packageWrapper.UpstreamYaml.ChartYaml)
	conform.ApplyChartAnnotations(helmChart.Metadata, packageWrapper.SourceMetadata)

	err = packageWrapper.Package.GeneratePatch()
	if err != nil {
		return err
	}

	err = packageWrapper.Package.Clean()
	if err != nil {
		logrus.Debug(err)
	}

	err = saveChart(helmChart, packageWrapper.SourceMetadata)
	if err != nil {
		return err
	}

	err = writeIndex()
	if err != nil {
		return err
	}

	return err
}

func saveChart(helmChart *chart.Chart, sourceMetadata *fetcher.ChartSourceMetadata) error {
	assetsPath := filepath.Join(
		getRepoRoot(),
		repositoryAssetsDir,
		strings.ToLower(sourceMetadata.Vendor))

	chartsPath := filepath.Join(
		getRepoRoot(),
		repositoryChartsDir,
		strings.ToLower(sourceMetadata.Vendor),
		helmChart.Metadata.Name,
		helmChart.Metadata.Version)

	err := conform.ExportChartAsset(helmChart, assetsPath)
	if err != nil {
		return err
	}

	err = conform.ExportChartDirectory(helmChart, chartsPath)
	if err != nil {
		return err
	}

	return nil
}

func getLatestStoredVersion(releaseName string) (string, error) {
	helmIndexYaml, err := readIndex()
	var latestVersion string
	if err != nil {
		return "", err
	}
	if val, ok := helmIndexYaml.Entries[releaseName]; ok {
		latestVersion = val[0].Version
	}

	return latestVersion, nil
}

func readIndex() (*repo.IndexFile, error) {
	indexFilePath := filepath.Join(getRepoRoot(), indexFile)
	helmIndexYaml, err := repo.LoadIndexFile(indexFilePath)
	return helmIndexYaml, err
}

func writeIndex() error {
	indexFilePath := filepath.Join(getRepoRoot(), indexFile)
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

	assetsDirectoryPath := filepath.Join(getRepoRoot(), repositoryAssetsDir)
	newHelmIndexYaml, err := repo.IndexDirectory(assetsDirectoryPath, repositoryAssetsDir)
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

func fetchUpstreams(packageWrapperList []PackageWrapper) {
	skippedList := make([]string, 0)
	for _, currentPackage := range packageWrapperList {
		err := conformChart(currentPackage)
		if err != nil {
			logrus.Error(err)
			skippedList = append(skippedList, currentPackage.Name)
			continue
		}
	}

	if len(skippedList) > 0 {
		logrus.Errorf("Skipped due to error: %v", skippedList)
	}

}

func parseUpstream(packagePath string) (*parse.UpstreamYaml, error) {
	upstreamYaml, err := parse.ParseUpstreamYaml(packagePath)
	if err != nil {
		return nil, err
	}

	return &upstreamYaml, nil
}

func generatePackageList(checkEnvVariable bool) []PackageWrapper {
	var currentPackage string
	if checkEnvVariable {
		currentPackage = os.Getenv(packageEnvVariable)
	}
	packageDirectory := filepath.Join(getRepoRoot(), repositoryPackagesDir)
	packageMap, err := parse.ListPackages(packageDirectory, currentPackage)
	if err != nil {
		logrus.Error(err)
	}

	packageNames := make([]string, 0, len(packageMap))
	for packageName := range packageMap {
		packageNames = append(packageNames, packageName)
	}

	sort.Strings(packageNames)

	packageList := make([]PackageWrapper, 0)
	for _, packageName := range packageNames {
		packageWrapper := PackageWrapper{
			Name: packageName,
			Path: packageMap[packageName],
		}
		packageList = append(packageList, packageWrapper)
	}

	return packageList
}

func populatePackages(onlyUpdates bool, print bool) ([]PackageWrapper, error) {
	packageList := make([]PackageWrapper, 0)
	for _, packageWrapper := range generatePackageList(true) {
		var err error
		packageWrapper.UpstreamYaml, err = parseUpstream(packageWrapper.Path)
		if err != nil {
			return nil, err
		}

		packageWrapper.SourceMetadata, err = generateChartSourceMetadata(*packageWrapper.UpstreamYaml)
		if err != nil {
			return nil, err
		}

		if print {
			logrus.Infof("Parsing %s\n", packageWrapper.SourceMetadata.Name)
			logrus.Infof("\n  Source: %s\n  Vendor: %s\n  Chart: %s\n  Version: %s\n  URL: %s  \n",
				packageWrapper.SourceMetadata.Source, packageWrapper.SourceMetadata.Vendor, packageWrapper.SourceMetadata.Name,
				packageWrapper.SourceMetadata.Version, packageWrapper.SourceMetadata.Url)
		}

		packageWrapper.LatestStored, err = getLatestStoredVersion(packageWrapper.SourceMetadata.Name)
		if err != nil {
			return nil, err
		}

		if onlyUpdates && packageWrapper.LatestStored == packageWrapper.SourceMetadata.Version {
			if print {
				logrus.Infof("%s/%s (%s) is up-to-date\n",
					packageWrapper.SourceMetadata.Vendor, packageWrapper.SourceMetadata.Name, packageWrapper.SourceMetadata.Version)
			}
			continue
		}

		packageWrapper.PackageYaml, err = generatePackageYaml(packageWrapper.Path, *packageWrapper.SourceMetadata)
		if err != nil {
			return nil, err
		}

		err = packageWrapper.PackageYaml.WritePackageYaml(packageWrapper.Path)
		if err != nil {
			return nil, err
		}

		packageWrapper.Package, err = generatePackage(packageWrapper.Path)
		if err != nil {
			return nil, err
		}

		if packageWrapper.UpstreamYaml.DisplayName != "" {
			packageWrapper.SourceMetadata.DisplayName = packageWrapper.UpstreamYaml.DisplayName
		}
		if packageWrapper.UpstreamYaml.ReleaseName != "" {
			packageWrapper.SourceMetadata.Name = packageWrapper.UpstreamYaml.ReleaseName
		}

		packageList = append(packageList, packageWrapper)

	}

	return packageList, nil
}

func listPackages(c *cli.Context) {
	packageList := generatePackageList(false)
	vendorSorted := make([]string, 0)
	for _, packageWrapper := range packageList {
		packagesPath := filepath.Join(getRepoRoot(), repositoryPackagesDir)
		packageParentPath := filepath.Dir(packageWrapper.Path)
		packageRelativePath := filepath.Base(packageWrapper.Path)
		if packagesPath != packageParentPath {
			packageRelativePath = filepath.Join(filepath.Base(packageParentPath), packageRelativePath)
		}
		vendorSorted = append(vendorSorted, packageRelativePath)
	}

	sort.Strings(vendorSorted)
	for _, pkg := range vendorSorted {
		fmt.Println(pkg)
	}
}

func patchCharts(c *cli.Context) {
	packageList, err := populatePackages(false, false)
	if err != nil {
		logrus.Fatal(err)
	}
	for _, packageWrapper := range packageList {
		err := patchPackage(packageWrapper)
		if err != nil {
			logrus.Error(err)
		}
	}
}

func cleanCharts(c *cli.Context) {
	packageList, err := populatePackages(false, false)
	if err != nil {
		logrus.Fatal(err)
	}
	for _, packageWrapper := range packageList {
		err = cleanPackage(packageWrapper)
		if err != nil {
			logrus.Error(err)
		}
	}
}

func commitCharts(c *cli.Context) {
	commitChanges(make([]PackageWrapper, 0))
}

func prepareCharts(c *cli.Context) {
	packageList, err := populatePackages(false, false)
	if err != nil {
		logrus.Fatal(err)
	}
	for _, packageWrapper := range packageList {
		err = preparePackage(packageWrapper)
		if err != nil {
			logrus.Error(err)
		}
	}
}

func runGambit(c *cli.Context) {
	packageList, err := populatePackages(true, true)
	if err != nil {
		logrus.Fatal(err)
	}

	if len(packageList) > 0 {
		fetchUpstreams(packageList)
		commitChanges(packageList)
	}

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

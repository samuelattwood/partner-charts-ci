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
	//commitAuthorEmail sets the email value for the automated git commit
	commitAuthorEmail = "partner-charts-ci@suse.com"
	//commitAuthorName sets the name for the automated git cmmit
	commitAuthorName = "partner-charts-ci"
	//indexFile sets the filename for the repo index yaml
	indexFile = "index.yaml"
	//packageEnvVariable sets the environment variable to check for a package name
	packageEnvVariable = "PACKAGE"
	//repositoryAssetsDir sets the directory name for chart asset files
	repositoryAssetsDir = "assets"
	//repositoryChartsDir sets the directory name for stored charts
	repositoryChartsDir = "charts"
	//repositoryPackagesDir sets the directory name for package configurations
	repositoryPackagesDir = "packages"
)

//PackageWrapper is a representation of relevant package metadata
type PackageWrapper struct {
	//Path stores the package path in current repository
	Path string
	//Package represents the current package being operated on
	Package *charts.Package
	//LatestStored stores the latest version of the chart currently in the repo
	LatestStored string
	//ManualUpdate evaluates true if package does not provide upstream yaml for automated update
	ManualUpdate bool
	//PackageYaml represents the yaml structure to be consumed by the existing charts-build-scripts
	PackageYaml *parse.PackageYaml
	//SourceMetadata represents metadata fetched from the upstream repository
	SourceMetadata *fetcher.ChartSourceMetadata
	//UpstreamYaml represents the values set in the package's upstream.yaml file
	UpstreamYaml *parse.UpstreamYaml
}

//Fetches absolute repository root path
func getRepoRoot() string {
	repoRoot, err := os.Getwd()
	if err != nil {
		logrus.Fatal(err)
	}

	return repoRoot
}

//Commits changes to index file, assets, charts, and packages
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
			strings.ToLower(pkg.SourceMetadata.Vendor),
			pkg.SourceMetadata.Name,
			pkg.SourceMetadata.Version)
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

//Cleans up ephemeral chart directory files from package prepare
func cleanPackage(packageWrapper PackageWrapper) error {
	err := packageWrapper.Package.Clean()
	if err != nil {
		err = fmt.Errorf("unable to clean up package")
		return err
	}

	return nil
}

//Generates patch files from prepared chart
func patchPackage(packageWrapper PackageWrapper) error {
	err := packageWrapper.Package.GeneratePatch()
	if err != nil {
		err = fmt.Errorf("unable to generate patch files")
		return err
	}

	return nil
}

//Prepares package for modification via patch
func preparePackage(packageWrapper PackageWrapper) error {
	conform.LinkOverlayFiles(packageWrapper.Path)

	err := packageWrapper.Package.Prepare()
	if err != nil {
		err = fmt.Errorf("unable to prepare package. cleaning up and skipping")
		packageWrapper.Package.Clean()
		return err
	}

	patchOrigPath := path.Join(packageWrapper.Path, repositoryChartsDir, "Chart.yaml.orig")
	if _, err := os.Stat(patchOrigPath); !os.IsNotExist(err) {
		os.Remove(patchOrigPath)
	}

	return nil
}

//Creates Package object to operate upon based on package path
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

//Generates package yaml file representation for consumption by charts-build-scripts
func generatePackageYaml(packagePath string, sourceMetadata fetcher.ChartSourceMetadata) (*parse.PackageYaml, error) {
	packageYaml := parse.PackageYaml{
		Commit:       sourceMetadata.Commit,
		SubDirectory: sourceMetadata.SubDirectory,
		Url:          sourceMetadata.Url,
	}

	return &packageYaml, nil
}

//Generates source metadata representation based on upstream repository
func generateChartSourceMetadata(upstreamYaml parse.UpstreamYaml) (*fetcher.ChartSourceMetadata, error) {
	sourceMetadata, err := fetcher.FetchUpstream(upstreamYaml)
	if err != nil {
		return nil, err
	}

	return &sourceMetadata, nil
}

//Prepares and standardizes chart, then returns loaded chart object
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

//Mutates chart with necessary alterations for repository
func conformChart(packageWrapper PackageWrapper) error {
	helmChart, err := initializeChart(packageWrapper)
	if err != nil {
		return err
	}

	if packageWrapper.ManualUpdate {
		packageWrapper.SourceMetadata.Vendor = helmChart.Name()
		packageWrapper.SourceMetadata.Name = helmChart.Name()
	} else {
		conform.OverlayChartMetadata(helmChart.Metadata, packageWrapper.UpstreamYaml.ChartYaml)
		conform.ApplyChartAnnotations(helmChart.Metadata, packageWrapper.SourceMetadata)
	}

	//Generate final chart version. Primarily for backwards-compatibility
	packageVersion, err := conform.GeneratePackageVersion(
		helmChart.Metadata.Version, packageWrapper.Package.PackageVersion, packageWrapper.Package.Version)
	if err != nil {
		return err
	}
	packageWrapper.SourceMetadata.Version = packageVersion
	helmChart.Metadata.Version = packageVersion

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

//Saves chart to disk as asset gzip and directory
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

//Fetches latest stored version of chart from current index, if any
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

//Reads in current index yaml
func readIndex() (*repo.IndexFile, error) {
	indexFilePath := filepath.Join(getRepoRoot(), indexFile)
	helmIndexYaml, err := repo.LoadIndexFile(indexFilePath)
	return helmIndexYaml, err
}

//Writes out modified index file
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

//Fetches metadata from upstream repositories
func fetchUpstreams(packageWrapperList []PackageWrapper) {
	skippedList := make([]string, 0)
	for _, currentPackage := range packageWrapperList {
		err := conformChart(currentPackage)
		if err != nil {
			logrus.Error(err)
			skippedList = append(skippedList, currentPackage.SourceMetadata.Name)
			continue
		}
	}

	if len(skippedList) > 0 {
		logrus.Errorf("Skipped due to error: %v", skippedList)
	}

}

//Reads in upstream yaml file
func parseUpstream(packagePath string) (*parse.UpstreamYaml, error) {
	upstreamYaml, err := parse.ParseUpstreamYaml(packagePath)
	if err != nil {
		return nil, err
	}

	return &upstreamYaml, nil
}

//Generates list of package paths with upstream yaml available
func generatePackageList() []PackageWrapper {
	currentPackage := os.Getenv(packageEnvVariable)
	packageDirectory := filepath.Join(getRepoRoot(), repositoryPackagesDir)
	packageMap, err := parse.ListPackages(packageDirectory, currentPackage)
	if err != nil {
		logrus.Error(err)
	}

	packageNames := make([]string, 0, len(packageMap))
	for packageName := range packageMap {
		packageNames = append(packageNames, packageName)
	}

	//Support fallback for existing packages without upstream yaml
	if len(packageNames) == 0 {
		packageNames, err = charts.ListPackages(getRepoRoot(), currentPackage)
		if err != nil {
			logrus.Error(err)
		}
	}

	sort.Strings(packageNames)

	packageList := make([]PackageWrapper, 0)
	for _, packageName := range packageNames {
		var packageWrapper PackageWrapper
		if _, ok := packageMap[packageName]; !ok {
			packageWrapper.ManualUpdate = true
			packageMap[packageName] = path.Join(getRepoRoot(), repositoryPackagesDir, packageName)
		}

		packageWrapper.Path = packageMap[packageName]
		packageList = append(packageList, packageWrapper)
	}

	return packageList
}

//Populates package wrapper with relevant data from upstream, checks for updates,
//writes out package yaml file, and generates package object
//If onlyUpdates, function only returns packages with an available update
func populatePackage(packageWrapper *PackageWrapper, onlyUpdates bool) (bool, error) {
	var err error
	packageWrapper.UpstreamYaml, err = parseUpstream(packageWrapper.Path)
	if err != nil {
		return false, err
	}

	packageWrapper.SourceMetadata, err = generateChartSourceMetadata(*packageWrapper.UpstreamYaml)
	if err != nil {
		return false, err
	}

	packageWrapper.LatestStored, err = getLatestStoredVersion(packageWrapper.SourceMetadata.Name)
	if err != nil {
		return false, err
	}

	packageWrapper.PackageYaml, err = generatePackageYaml(packageWrapper.Path, *packageWrapper.SourceMetadata)
	if err != nil {
		return false, err
	}

	if packageWrapper.UpstreamYaml.PackageVersion != nil {
		packageWrapper.PackageYaml.PackageVersion = *packageWrapper.UpstreamYaml.PackageVersion
	}

	if packageWrapper.UpstreamYaml.Version != "" {
		packageWrapper.PackageYaml.Version = packageWrapper.UpstreamYaml.Version
	}

	err = packageWrapper.PackageYaml.WritePackageYaml(packageWrapper.Path)
	if err != nil {
		return false, err
	}

	packageWrapper.Package, err = generatePackage(packageWrapper.Path)
	if err != nil {
		return false, err
	}

	if packageWrapper.UpstreamYaml.DisplayName != "" {
		packageWrapper.SourceMetadata.DisplayName = packageWrapper.UpstreamYaml.DisplayName
	}
	if packageWrapper.UpstreamYaml.ReleaseName != "" {
		packageWrapper.SourceMetadata.Name = packageWrapper.UpstreamYaml.ReleaseName
	}

	packageWrapper.SourceMetadata.Version, err = conform.GeneratePackageVersion(
		packageWrapper.SourceMetadata.Version, packageWrapper.Package.PackageVersion, packageWrapper.Package.Version)
	if err != nil {
		return false, err
	}

	if onlyUpdates && packageWrapper.LatestStored == packageWrapper.SourceMetadata.Version {
		return false, nil
	}

	return true, nil

}

//Populates list of package wrappers, handles manual and automatic variation
//If print, function will print information during processing
func populatePackages(onlyUpdates bool, print bool) ([]PackageWrapper, error) {
	packageList := make([]PackageWrapper, 0)
	for _, packageWrapper := range generatePackageList() {
		if packageWrapper.ManualUpdate {
			var err error
			packageWrapper.Package, err = generatePackage(packageWrapper.Path)
			if err != nil {
				logrus.Error(err)
				continue
			}
			packageWrapper.SourceMetadata = &fetcher.ChartSourceMetadata{}
		} else {
			updated, err := populatePackage(&packageWrapper, onlyUpdates)
			if err != nil {
				logrus.Error(err)
				continue
			}
			if print {
				logrus.Infof("Parsing %s\n", packageWrapper.SourceMetadata.Name)
				logrus.Infof("\n  Source: %s\n  Vendor: %s\n  Chart: %s\n  Version: %s\n  URL: %s  \n",
					packageWrapper.SourceMetadata.Source, packageWrapper.SourceMetadata.Vendor, packageWrapper.SourceMetadata.Name,
					packageWrapper.SourceMetadata.Version, packageWrapper.SourceMetadata.Url)
				if !updated {
					logrus.Infof("%s/%s (%s) is up-to-date\n",
						packageWrapper.SourceMetadata.Vendor, packageWrapper.SourceMetadata.Name, packageWrapper.SourceMetadata.Version)
				}
			}

			if onlyUpdates && !updated {
				continue
			}
		}
		packageList = append(packageList, packageWrapper)

	}

	return packageList, nil
}

func generateChanges(commit bool) {
	packageList, err := populatePackages(true, true)
	if err != nil {
		logrus.Fatal(err)
	}

	if len(packageList) > 0 {
		fetchUpstreams(packageList)
		if commit {
			commitChanges(packageList)
		}
	}
}

//CLI function call - Prints list of available packages to STDout
func listPackages(c *cli.Context) {
	packageList := generatePackageList()
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

//CLI function call - Generates patch files for package(s)
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

//CLI function call - Cleans package object(s)
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

//CLI function call - Prepares package(s) for modification via patch
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

//CLI function call - Generates all changes for available packages,
//Checking against upstream version, prepare, patch, clean, and index update
//Does not commit
func stageUpdates(c *cli.Context) {
	generateChanges(false)
}

//CLI function call - Generates automated commit
func autoUpdate(c *cli.Context) {
	generateChanges(true)
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
			Name:   "auto",
			Usage:  "Generate and commit changes",
			Action: autoUpdate,
		},
		{
			Name:   "stage",
			Usage:  "Stage All changes. Does not commit",
			Action: stageUpdates,
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		logrus.Fatal(err)
	}

}

package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/go-git/go-git/v5"
	"github.com/rancher/charts-build-scripts/pkg/charts"
	"github.com/rancher/charts-build-scripts/pkg/filesystem"
	"github.com/samuelattwood/partner-charts-ci/pkg/conform"
	"github.com/samuelattwood/partner-charts-ci/pkg/fetcher"
	"github.com/samuelattwood/partner-charts-ci/pkg/parse"
	"github.com/samuelattwood/partner-charts-ci/pkg/validate"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/repo"
)

const (
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
	configOptionsFile     = "configuration.yaml"
)

// PackageWrapper is a representation of relevant package metadata
type PackageWrapper struct {
	//Indicator to generate patch files
	GenPatch bool
	//Path stores the package path in current repository
	Path string
	//LatestStored stores the latest version of the chart currently in the repo
	LatestStored repo.ChartVersion
	//ManualUpdate evaluates true if package does not provide upstream yaml for automated update
	ManualUpdate bool
	//Force only pulling the latest version
	OnlyLatest bool
	//Indicator to write chart to disk
	Save bool
	//SourceMetadata represents metadata fetched from the upstream repository
	SourceMetadata *fetcher.ChartSourceMetadata
	//Filtered subset of versions to-be-fetched
	FetchVersions repo.ChartVersions
	//UpstreamYaml represents the values set in the package's upstream.yaml file
	UpstreamYaml *parse.UpstreamYaml
}

// Generates patch files from prepared chart
func (packageWrapper PackageWrapper) patch() error {
	var err error
	var packageYaml *parse.PackageYaml
	if !packageWrapper.ManualUpdate {
		packageYaml, err = writePackageYaml(
			packageWrapper.Path,
			packageWrapper.UpstreamYaml.PackageVersion,
			packageWrapper.SourceMetadata.Commit,
			packageWrapper.SourceMetadata.SubDirectory,
			packageWrapper.SourceMetadata.Versions[0].URLs[0],
			true,
		)
		if err != nil {
			return err
		}
	}
	pkg, err := generatePackage(packageWrapper.Path)
	if err != nil {
		return err
	}
	err = pkg.GeneratePatch()
	if err != nil {
		logrus.Error(err)
		err = fmt.Errorf("unable to generate patch files")
		return err
	}

	if !packageWrapper.ManualUpdate {
		packageYaml.Remove()
	}

	return nil
}

// Populates package wrapper with relevant data from upstream, checks for updates,
// writes out package yaml file, and generates package object
// Returns true if newer package version is available
func (packageWrapper *PackageWrapper) populate() (bool, error) {
	var err error
	if packageWrapper.ManualUpdate {
		//Preparing the chart to pull the actual release-name and version
		err := prepareManualPackage(packageWrapper.Path)
		if err != nil {
			return false, err
		}

		pkg, err := generatePackage(packageWrapper.Path)
		if err != nil {
			return false, err
		}

		chartPath := path.Join(packageWrapper.Path, repositoryChartsDir)
		helmChart, err := loader.Load(chartPath)
		if err != nil {
			return false, err
		}

		sourceMetadata := fetcher.ChartSourceMetadata{
			Vendor:   path.Base(packageWrapper.Path),
			Name:     helmChart.Name(),
			Source:   "direct",
			Versions: make(repo.ChartVersions, 1),
		}

		chartVersion := repo.ChartVersion{
			Metadata: &chart.Metadata{
				Name: sourceMetadata.Name,
			},
			URLs: make([]string, 1),
		}

		chartVersion.URLs[0] = pkg.Upstream.GetOptions().URL
		chartVersion.Version, err = conform.GeneratePackageVersion(
			helmChart.Metadata.Version,
			pkg.PackageVersion,
			pkg.Version)
		if err != nil {
			return false, err
		}

		packageWrapper.SourceMetadata = &sourceMetadata
		packageWrapper.SourceMetadata.Versions[0] = &chartVersion

		packageWrapper.FetchVersions, err = filterVersions(
			packageWrapper.SourceMetadata.Versions,
			"",
			nil)
		if err != nil {
			return false, err
		}

		packageWrapper.LatestStored, err = getLatestStoredVersion(helmChart.Name())
		if err != nil {
			return false, err
		}

		err = cleanPackage(packageWrapper.Path, true)
		if err != nil {
			return false, err
		}

	} else {
		packageWrapper.UpstreamYaml, err = parseUpstream(packageWrapper.Path)
		if err != nil {
			return false, err
		}

		packageWrapper.SourceMetadata, err = generateChartSourceMetadata(*packageWrapper.UpstreamYaml)
		if err != nil {
			return false, err
		}

		if packageWrapper.OnlyLatest {
			packageWrapper.UpstreamYaml.Fetch = "latest"
			packageWrapper.UpstreamYaml.TrackVersions = make([]string, 0)
		}

		packageWrapper.FetchVersions, err = filterVersions(
			packageWrapper.SourceMetadata.Versions,
			packageWrapper.UpstreamYaml.Fetch,
			packageWrapper.UpstreamYaml.TrackVersions)
		if err != nil {
			return false, err
		}

		packageWrapper.LatestStored, err = getLatestStoredVersion(packageWrapper.SourceMetadata.Name)
		if err != nil {
			return false, err
		}

		if packageWrapper.UpstreamYaml.DisplayName != "" {
			packageWrapper.SourceMetadata.DisplayName = packageWrapper.UpstreamYaml.DisplayName
		}
		if packageWrapper.UpstreamYaml.ReleaseName != "" {
			packageWrapper.SourceMetadata.ReleaseName = packageWrapper.UpstreamYaml.ReleaseName
		} else {
			packageWrapper.SourceMetadata.ReleaseName = packageWrapper.SourceMetadata.Name
		}
	}

	if len(packageWrapper.FetchVersions) == 0 {
		return false, nil
	}

	return true, nil

}

func (packageWrapper PackageWrapper) hide() error {
	chartName := packageWrapper.LatestStored.Name

	allStoredVersions, err := getStoredVersions(chartName)
	if err != nil {
		return err
	}

	helmIndexYaml, err := readIndex()
	if err != nil {
		return err
	}

	indexFilePath := filepath.Join(getRepoRoot(), indexFile)

	for _, version := range allStoredVersions {
		logrus.Infof("Hiding %s (%s)\n", version.Name, version.Version)
		assetsPath := filepath.Join(
			getRepoRoot(),
			repositoryAssetsDir,
			packageWrapper.SourceMetadata.ParsedVendor,
		)

		versionPath := path.Join(
			getRepoRoot(),
			repositoryChartsDir,
			packageWrapper.SourceMetadata.ParsedVendor,
			chartName,
			version.Version,
		)
		helmChart, err := loader.Load(versionPath)
		if err != nil {
			return err
		}

		helmChart.Metadata.Annotations["catalog.cattle.io/hidden"] = "true"

		err = os.RemoveAll(versionPath)
		if err != nil {
			return err
		}

		err = conform.ExportChartAsset(helmChart, assetsPath)
		if err != nil {
			return err
		}
		conform.ExportChartDirectory(helmChart, versionPath)
		if err != nil {
			return err
		}

	}

	delete(helmIndexYaml.Entries, chartName)

	err = helmIndexYaml.WriteFile(indexFilePath, 0644)
	if err != nil {
		return err
	}

	err = writeIndex()

	return err
}

// Fetches absolute repository root path
func getRepoRoot() string {
	repoRoot, err := os.Getwd()
	if err != nil {
		logrus.Fatal(err)
	}

	return repoRoot
}

func getRelativePath(packagePath string) string {
	packagesPath := filepath.Join(getRepoRoot(), repositoryPackagesDir)
	return strings.TrimPrefix(packagePath, packagesPath)
}

func gitCleanup() error {
	r, err := git.PlainOpen(getRepoRoot())
	if err != nil {
		return err
	}

	wt, err := r.Worktree()
	if err != nil {
		return err
	}

	cleanOptions := git.CleanOptions{
		Dir: true,
	}

	branch, err := r.Head()
	if err != nil {
		return err
	}

	logrus.Debugf("Branch: %s\n", branch.Name())
	checkoutOptions := git.CheckoutOptions{
		Branch: branch.Name(),
		Force:  true,
	}

	err = wt.Clean(&cleanOptions)
	if err != nil {
		return err
	}

	err = wt.Checkout(&checkoutOptions)

	return err
}

// Commits changes to index file, assets, charts, and packages
func commitChanges(updatedList []PackageWrapper) error {
	var additions, updates string
	commitOptions := git.CommitOptions{}

	r, err := git.PlainOpen(getRepoRoot())
	if err != nil {
		return err
	}

	wt, err := r.Worktree()
	if err != nil {
		return err
	}

	logrus.Info("Committing changes")

	for _, packageWrapper := range updatedList {
		assetsPath := path.Join(
			repositoryAssetsDir,
			packageWrapper.SourceMetadata.ParsedVendor)

		chartsPath := path.Join(
			repositoryChartsDir,
			packageWrapper.SourceMetadata.ParsedVendor,
			packageWrapper.SourceMetadata.Versions[0].Name)

		packagesPath := path.Join(
			repositoryPackagesDir,
			packageWrapper.SourceMetadata.ParsedVendor,
			packageWrapper.SourceMetadata.Versions[0].Name)

		wt.Add(assetsPath)
		wt.Add(chartsPath)
		wt.Add(packagesPath)
	}

	wt.Add(indexFile)

	commitMessage := "CI Updated Charts"
	for _, packageWrapper := range updatedList {
		lineItem := fmt.Sprintf("  %s/%s:\n",
			packageWrapper.SourceMetadata.ParsedVendor,
			packageWrapper.SourceMetadata.Name)
		for _, version := range packageWrapper.FetchVersions {
			lineItem += fmt.Sprintf("    - %s\n", version.Version)
		}
		if packageWrapper.LatestStored.Digest == "" {
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

// Cleans up ephemeral chart directory files from package prepare
func cleanPackage(packagePath string, manualUpdate bool) error {
	var err error
	var packageYaml *parse.PackageYaml
	packageName := strings.TrimPrefix(getRelativePath(packagePath), "/")
	logrus.Debugf("Cleaning package %s", packageName)
	logrus.Debugf("Generating package yaml if it does not exist")
	//packageYaml, err = generatePackageYaml(packageWrapper, *packageWrapper.SourceMetadata.Versions[0], false)
	if !manualUpdate {
		packageYaml, err = writePackageYaml(
			packagePath,
			0,
			"",
			"",
			"https://.tgz",
			false,
		)
		if err != nil {
			return err
		}
	}
	logrus.Debugf("Generating package %s\n", packageName)
	pkg, err := generatePackage(packagePath)
	if err != nil {
		return err
	}
	logrus.Infof("Cleaning package %s\n", packageName)
	err = pkg.Clean()
	if err != nil {
		return err
	}
	if !manualUpdate {
		packageYaml.Remove()
	}

	return nil
}

// Prepares package for modification via patch
func prepareManualPackage(packagePath string) error {
	logrus.Debugf("Generated package from %s", packagePath)
	pkg, err := generatePackage(packagePath)
	if err != nil {
		logrus.Error(err)
	}

	conform.LinkOverlayFiles(packagePath)

	err = pkg.Prepare()
	if err != nil {
		err = cleanPackage(packagePath, true)
		if err != nil {
			logrus.Error(err)
		}
		logrus.Error("Unable to prepare package. Cleaning up and skipping...")
		return err
	}

	patchOrigPath := path.Join(packagePath, repositoryChartsDir, "Chart.yaml.orig")
	if _, err := os.Stat(patchOrigPath); !os.IsNotExist(err) {
		os.Remove(patchOrigPath)
	}

	return nil
}

// Prepares package for modification via patch
func preparePackage(packagePath string, sourceMetadata *fetcher.ChartSourceMetadata, chartVersion *repo.ChartVersion) error {
	logrus.Debugf("Generated package from %s", packagePath)
	pkg, err := generatePackage(packagePath)
	if err != nil {
		logrus.Error(err)
	}

	chartVersion.Metadata.Version, err = conform.GeneratePackageVersion(
		chartVersion.Metadata.Version, pkg.PackageVersion, pkg.Version)
	if err != nil {
		logrus.Error(err)
	}

	conform.LinkOverlayFiles(packagePath)

	err = pkg.Prepare()
	if err != nil {
		logrus.Error("Unable to prepare package. Cleaning up and skipping...")
		cleanPackage(packagePath, false)
		return err
	}

	patchOrigPath := path.Join(packagePath, repositoryChartsDir, "Chart.yaml.orig")
	if _, err := os.Stat(patchOrigPath); !os.IsNotExist(err) {
		os.Remove(patchOrigPath)
	}

	return nil
}

// Creates Package object to operate upon based on package path
func generatePackage(packagePath string) (*charts.Package, error) {
	logrus.Debugf("Generating package from %s\n", packagePath)
	packageRelativePath := getRelativePath(packagePath)
	rootFs := filesystem.GetFilesystem(getRepoRoot())
	pkg, err := charts.GetPackage(rootFs, packageRelativePath)
	if err != nil {
		return nil, err
	}

	return pkg, nil
}

func writePackageYaml(packagePath string, packageVersion int, commit string, subdirectory string, url string, overWrite bool) (*parse.PackageYaml, error) {
	logrus.Debugf("Generating package yaml in %s\n", packagePath)
	packageYaml := parse.PackageYaml{
		Commit:         commit,
		PackageVersion: packageVersion,
		Path:           packagePath,
		SubDirectory:   subdirectory,
		Url:            url,
	}

	logrus.Debugf("Writing package yaml in %s\n", packagePath)
	err := packageYaml.Write(overWrite)
	if err != nil {
		logrus.Error(err)
	}

	return &packageYaml, nil
}

func collectTrackedVersions(upstreamVersions repo.ChartVersions, tracked []string) map[string]repo.ChartVersions {
	trackedVersions := make(map[string]repo.ChartVersions)

	for _, trackedVersion := range tracked {
		versionList := make(repo.ChartVersions, 0)
		for _, version := range upstreamVersions {
			semVer, err := semver.NewVersion(version.Version)
			if err != nil {
				logrus.Errorf("%s: %s", version.Version, err)
				continue
			}
			trackedSemVer, err := semver.NewVersion(trackedVersion)
			if err != nil {
				logrus.Errorf("%s: %s", version.Version, err)
				continue
			}
			logrus.Debugf("Comparing upstream version %s (%s) to stored version %s\n", version.Name, version.Version, trackedVersion)
			if trackedSemVer.Major() == semVer.Major() && trackedSemVer.Minor() == semVer.Minor() {
				logrus.Debugf("Appending version %s tracking %s\n", version.Version, trackedVersion)
				versionList = append(versionList, version)
			}
		}
		trackedVersions[trackedVersion] = versionList
	}

	return trackedVersions
}

func collectNonStoredVersions(versions repo.ChartVersions, storedVersions repo.ChartVersions, fetch string) repo.ChartVersions {
	nonStoredVersions := make(repo.ChartVersions, 0)
	for i, version := range versions {
		stored := false
		logrus.Debugf("Checking if version %s is stored\n", version.Version)
		for _, storedVersion := range storedVersions {
			if storedVersion.Version == version.Version {
				logrus.Debugf("Found version %s\n", storedVersion.Version)
				stored = true
			}
		}
		if stored && i == 0 && (strings.ToLower(fetch) == "" || strings.ToLower(fetch) == "latest") {
			logrus.Debugf("Latest version already stored")
			break
		}
		if !stored {
			if fetch == strings.ToLower("newer") {
				var semVer, storedSemVer *semver.Version
				semVer, err := semver.NewVersion(version.Version)
				if err != nil {
					logrus.Error(err)
					continue
				}
				if len(storedVersions) > 0 {
					storedSemVer, err = semver.NewVersion(storedVersions[0].Version)
					if err != nil {
						logrus.Error(err)
						continue
					}
					if semVer.GreaterThan(storedSemVer) {
						nonStoredVersions = append(nonStoredVersions, version)
					}
				} else {
					nonStoredVersions = append(nonStoredVersions, version)
				}
			} else if fetch == strings.ToLower("all") {
				nonStoredVersions = append(nonStoredVersions, version)
			} else {
				nonStoredVersions = append(nonStoredVersions, version)
				break
			}
		}
	}

	return nonStoredVersions
}

func filterVersions(upstreamVersions repo.ChartVersions, fetch string, tracked []string) (repo.ChartVersions, error) {
	logrus.Debugf("Filtering versions for %s\n", upstreamVersions[0].Name)
	filteredVersions := make(repo.ChartVersions, 0)
	allStoredVersions, err := getStoredVersions(upstreamVersions[0].Name)
	if len(tracked) > 0 {
		allTrackedVersions := collectTrackedVersions(upstreamVersions, tracked)
		storedTrackedVersions := collectTrackedVersions(allStoredVersions, tracked)
		if err != nil {
			return filteredVersions, err
		}
		for _, trackedVersion := range tracked {
			nonStoredVersions := collectNonStoredVersions(allTrackedVersions[trackedVersion], storedTrackedVersions[trackedVersion], fetch)
			filteredVersions = append(filteredVersions, nonStoredVersions...)
		}
	} else {
		filteredVersions = collectNonStoredVersions(upstreamVersions, allStoredVersions, fetch)
	}

	return filteredVersions, nil
}

// Generates source metadata representation based on upstream repository
func generateChartSourceMetadata(upstreamYaml parse.UpstreamYaml) (*fetcher.ChartSourceMetadata, error) {
	sourceMetadata, err := fetcher.FetchUpstream(upstreamYaml)
	if err != nil {
		return nil, err
	}

	return &sourceMetadata, nil
}

// Prepares and standardizes chart, then returns loaded chart object
func initializeChart(packagePath string, sourceMetadata fetcher.ChartSourceMetadata, chartVersion repo.ChartVersion, manualUpdate bool) (*chart.Chart, error) {
	var err error
	if manualUpdate {
		err = prepareManualPackage(packagePath)

	} else {
		err = preparePackage(packagePath, &sourceMetadata, &chartVersion)
	}
	if err != nil {
		return nil, err
	}

	chartDirectoryPath := path.Join(packagePath, repositoryChartsDir)
	conform.StandardizeChartDirectory(chartDirectoryPath, "")

	helmChart, err := loader.Load(chartDirectoryPath)
	if err != nil {
		return nil, err
	}

	helmChart.Metadata.Version = chartVersion.Version

	return helmChart, nil
}

// Mutates chart with necessary alterations for repository
func conformPackage(packageWrapper PackageWrapper) error {
	var err error
	var packageYaml *parse.PackageYaml
	logrus.Debugf("Conforming package from %s\n", packageWrapper.Path)
	for _, chartVersion := range packageWrapper.FetchVersions {
		logrus.Debugf("Conforming package %s (%s)\n", chartVersion.Name, chartVersion.Version)
		if !packageWrapper.ManualUpdate {
			packageYaml, err = writePackageYaml(
				packageWrapper.Path,
				packageWrapper.UpstreamYaml.PackageVersion,
				packageWrapper.SourceMetadata.Commit,
				packageWrapper.SourceMetadata.SubDirectory,
				chartVersion.URLs[0],
				true,
			)
			if err != nil {
				return err
			}
		}
		helmChart, err := initializeChart(
			packageWrapper.Path,
			*packageWrapper.SourceMetadata,
			*chartVersion,
			packageWrapper.ManualUpdate,
		)
		if err != nil {
			return err
		}

		if packageWrapper.ManualUpdate {
			packageWrapper.SourceMetadata.Name = helmChart.Name()
			chartVersion.Version = helmChart.Metadata.Version
		} else {
			conform.OverlayChartMetadata(helmChart.Metadata, packageWrapper.UpstreamYaml.ChartYaml)
			conform.ApplyChartAnnotations(helmChart.Metadata, packageWrapper.SourceMetadata)
			if helmChart.Metadata.KubeVersion != "" && packageWrapper.UpstreamYaml.ChartYaml.KubeVersion != "" {
				helmChart.Metadata.Annotations["catalog.cattle.io/kube-version"] = packageWrapper.UpstreamYaml.ChartYaml.KubeVersion
				helmChart.Metadata.KubeVersion = packageWrapper.UpstreamYaml.ChartYaml.KubeVersion
			} else if helmChart.Metadata.KubeVersion != "" {
				helmChart.Metadata.Annotations["catalog.cattle.io/kube-version"] = helmChart.Metadata.KubeVersion
			} else if packageWrapper.UpstreamYaml.ChartYaml.KubeVersion != "" {
				helmChart.Metadata.Annotations["catalog.cattle.io/kube-version"] = packageWrapper.UpstreamYaml.ChartYaml.KubeVersion
			}
		}

		pkg, err := generatePackage(packageWrapper.Path)
		if err != nil {
			return err
		}

		if packageWrapper.GenPatch {
			err = pkg.GeneratePatch()
			if err != nil {
				return err
			}

			err = cleanPackage(packageWrapper.Path, packageWrapper.ManualUpdate)
			if err != nil {
				logrus.Debug(err)
			}

		}

		conform.RemoveOverlayFiles(packageWrapper.Path)

		if packageWrapper.Save {
			err = saveChart(helmChart, packageWrapper.SourceMetadata)
			if err != nil {
				return err
			}
		}

		if !packageWrapper.ManualUpdate {
			packageYaml.Remove()
		}
	}

	return err
}

// Saves chart to disk as asset gzip and directory
func saveChart(helmChart *chart.Chart, sourceMetadata *fetcher.ChartSourceMetadata) error {
	assetsPath := filepath.Join(
		getRepoRoot(),
		repositoryAssetsDir,
		sourceMetadata.ParsedVendor)

	chartsPath := filepath.Join(
		getRepoRoot(),
		repositoryChartsDir,
		sourceMetadata.ParsedVendor,
		helmChart.Metadata.Name,
		helmChart.Metadata.Version)

	logrus.Debugf("Exporting chart assets to %s\n", assetsPath)
	err := conform.ExportChartAsset(helmChart, assetsPath)
	if err != nil {
		return err
	}

	logrus.Debugf("Exporting chart to %s\n", chartsPath)
	err = conform.ExportChartDirectory(helmChart, chartsPath)
	if err != nil {
		return err
	}

	return nil
}

func getStoredVersions(chartName string) (repo.ChartVersions, error) {
	helmIndexYaml, err := readIndex()
	storedVersions := repo.ChartVersions{}
	if err != nil {
		return storedVersions, err
	}
	if val, ok := helmIndexYaml.Entries[chartName]; ok {
		storedVersions = append(storedVersions, val...)
	}

	return storedVersions, nil
}

// Fetches latest stored version of chart from current index, if any
func getLatestStoredVersion(chartName string) (repo.ChartVersion, error) {
	helmIndexYaml, err := readIndex()
	latestVersion := repo.ChartVersion{}
	if err != nil {
		return latestVersion, err
	}
	if val, ok := helmIndexYaml.Entries[chartName]; ok {
		latestVersion = *val[0]
	}

	return latestVersion, nil
}

// Reads in current index yaml
func readIndex() (*repo.IndexFile, error) {
	indexFilePath := filepath.Join(getRepoRoot(), indexFile)
	helmIndexYaml, err := repo.LoadIndexFile(indexFilePath)
	return helmIndexYaml, err
}

// Writes out modified index file
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

// Fetches metadata from upstream repositories.
// Return list of skipped packages
func fetchUpstreams(packageWrapperList []PackageWrapper) []string {
	skippedList := make([]string, 0)
	for _, packageWrapper := range packageWrapperList {
		err := conformPackage(packageWrapper)
		if err != nil {
			logrus.Error(err)
			skippedList = append(skippedList, packageWrapper.SourceMetadata.Name)
			continue
		}
	}

	return skippedList
}

// Reads in upstream yaml file
func parseUpstream(packagePath string) (*parse.UpstreamYaml, error) {
	upstreamYaml, err := parse.ParseUpstreamYaml(packagePath)
	if err != nil {
		return nil, err
	}

	return &upstreamYaml, nil
}

// Generates list of package paths with upstream yaml available
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

// Populates list of package wrappers, handles manual and automatic variation
// If print, function will print information during processing
func populatePackages(onlyUpdates bool, onlyLatest bool, print bool) ([]PackageWrapper, error) {
	packageList := make([]PackageWrapper, 0)
	for _, packageWrapper := range generatePackageList() {
		logrus.Debugf("Populating package from %s\n", packageWrapper.Path)
		if onlyLatest {
			packageWrapper.OnlyLatest = true
		}
		updated, err := packageWrapper.populate()
		if err != nil {
			logrus.Error(err)
			continue
		}
		if print {
			logrus.Infof("Parsing %s\n", packageWrapper.SourceMetadata.Name)
			if len(packageWrapper.FetchVersions) == 0 {
				logrus.Infof("%s/%s is up-to-date\n",
					packageWrapper.SourceMetadata.Vendor, packageWrapper.SourceMetadata.Name)
			}
			for _, version := range packageWrapper.FetchVersions {
				logrus.Infof("\n  Source: %s\n  Vendor: %s\n  Chart: %s\n  Version: %s\n  URL: %s  \n",
					packageWrapper.SourceMetadata.Source, packageWrapper.SourceMetadata.Vendor, packageWrapper.SourceMetadata.Name,
					version.Version, version.URLs[0])
			}
		}

		if onlyUpdates && !updated {
			continue
		}
		packageList = append(packageList, packageWrapper)
	}

	return packageList, nil
}

// func generateChanges(genpatch bool, save bool, commit bool, onlyUpdates bool, print bool) {
func generateChanges(auto bool, stage bool) {
	var packageList []PackageWrapper
	var err error
	if auto || stage {
		packageList, err = populatePackages(true, false, true)
		for i := range packageList {
			packageList[i].GenPatch = true
			packageList[i].Save = true
		}
	} else {
		packageList, err = populatePackages(false, true, true)
	}
	if err != nil {
		logrus.Fatal(err)
	}

	if len(packageList) > 0 {
		skippedList := fetchUpstreams(packageList)
		if len(skippedList) > 0 {
			logrus.Errorf("Skipped due to error: %v", skippedList)
		}
		if len(skippedList) >= len(packageList) {
			logrus.Fatalf("All packages skipped. Exiting...")
		}
		if auto || stage {
			err = writeIndex()
			if err != nil {
				logrus.Error(err)
			}
		}
		if auto {
			commitChanges(packageList)
		}
	}
}

// CLI function call - Prints list of available packages to STDout
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

// CLI function call - Generates patch files for package(s)
func patchCharts(c *cli.Context) {
	packageList, err := populatePackages(false, false, true)
	if err != nil {
		logrus.Fatal(err)
	}
	for _, packageWrapper := range packageList {
		err := packageWrapper.patch()
		if err != nil {
			logrus.Error(err)
		}
	}
}

func hideChart(c *cli.Context) {
	if os.Getenv(packageEnvVariable) == "" {
		logrus.Fatal("Please set PACKAGE environment variable")
	}
	packageList, err := populatePackages(false, false, false)
	if err != nil {
		logrus.Fatal(err)
	}

	if len(packageList) == 1 {
		err = packageList[0].hide()
		if err != nil {
			logrus.Fatal(err)
		}
	}
}

// CLI function call - Cleans package object(s)
func cleanCharts(c *cli.Context) {
	packageList := generatePackageList()
	for _, packageWrapper := range packageList {
		err := cleanPackage(packageWrapper.Path, packageWrapper.ManualUpdate)
		if err != nil {
			logrus.Error(err)
		}
	}
}

// CLI function call - Prepares package(s) for modification via patch
func prepareCharts(c *cli.Context) {
	generateChanges(false, false)
}

// CLI function call - Generates all changes for available packages,
// Checking against upstream version, prepare, patch, clean, and index update
// Does not commit
func stageChanges(c *cli.Context) {
	generateChanges(false, true)
}

func unstageChanges(c *cli.Context) {
	err := gitCleanup()
	if err != nil {
		logrus.Error(err)
	}
}

// CLI function call - Generates automated commit
func autoUpdate(c *cli.Context) {
	generateChanges(true, false)
}

// CLI function call - Validates repo against released
func validateRepo(c *cli.Context) {
	validatePaths := map[string]validate.DirectoryComparison{
		"assets": {},
		"charts": {},
	}
	directoryComparison := validate.DirectoryComparison{}

	configYamlPath := path.Join(getRepoRoot(), configOptionsFile)
	if _, err := os.Stat(configYamlPath); os.IsNotExist(err) {
		logrus.Fatalf("Unable to read %s\n", configOptionsFile)
	}
	configYaml, err := validate.ReadConfig(configYamlPath)
	if err != nil {
		logrus.Fatal(err)
	}

	if len(configYaml.Validate) == 0 || configYaml.Validate[0].Branch == "" || configYaml.Validate[0].Url == "" {
		logrus.Fatal("Invalid validation configuration")
	}

	cloneDir, err := os.MkdirTemp("", "gitRepo")
	if err != nil {
		logrus.Fatal(err)
	}

	err = validate.CloneRepo(configYaml.Validate[0].Url, configYaml.Validate[0].Branch, cloneDir)
	if err != nil {
		logrus.Fatal(err)
	}

	for dirPath := range validatePaths {
		upstreamPath := path.Join(cloneDir, dirPath)
		updatePath := path.Join(getRepoRoot(), dirPath)
		newComparison, err := validate.CompareDirectories(upstreamPath, updatePath)
		if err != nil {
			logrus.Error(err)
		}
		directoryComparison.Merge(newComparison)
		validatePaths[dirPath] = newComparison
	}

	err = os.RemoveAll(cloneDir)
	if err != nil {
		logrus.Error(err)
	}

	if len(directoryComparison.Modified) > 0 {
		outString := ""
		for dirPath := range validatePaths {
			if len(validatePaths[dirPath].Modified) > 0 {
				outString += fmt.Sprintf("\n - %s", dirPath)
				stringJoiner := fmt.Sprintf("\n - %s", dirPath)
				fileList := strings.Join(validatePaths[dirPath].Modified[:], stringJoiner)
				outString += fileList
			}
		}
		logrus.Fatalf("Files Modified:%s", outString)
	}

	if len(directoryComparison.Added) > 0 {
		outString := ""
		for dirPath := range validatePaths {
			if len(validatePaths[dirPath].Added) > 0 {
				outString += fmt.Sprintf("\n - %s", dirPath)
				stringJoiner := fmt.Sprintf("\n - %s", dirPath)
				fileList := strings.Join(validatePaths[dirPath].Added[:], stringJoiner)
				outString += fileList
			}
		}
		logrus.Infof("Files Added:%s", outString)
	}

	if len(directoryComparison.Removed) > 0 {
		outString := ""
		for dirPath := range validatePaths {
			if len(validatePaths[dirPath].Removed) > 0 {
				outString += fmt.Sprintf("\n - %s", dirPath)
				stringJoiner := fmt.Sprintf("\n - %s", dirPath)
				fileList := strings.Join(validatePaths[dirPath].Removed[:], stringJoiner)
				outString += fileList
			}
		}
		logrus.Warnf("Files Removed:%s", outString)
	}

	logrus.Infof("Successfully validated\n  Upstream: %s\n  Branch: %s\n",
		configYaml.Validate[0].Url, configYaml.Validate[0].Branch)

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
			Usage:  "Stage all changes. Does not commit",
			Action: stageChanges,
		},
		{
			Name:   "unstage",
			Usage:  "Un-Stage all non-committed changes. Deletes all untracked files.",
			Action: unstageChanges,
		},
		{
			Name:   "hide",
			Usage:  "Apply 'catalog.cattle.io/hidden' annotation to all stored versions of chart",
			Action: hideChart,
		},
		{
			Name:   "validate",
			Usage:  "Check repo against released charts",
			Action: validateRepo,
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		logrus.Fatal(err)
	}

}

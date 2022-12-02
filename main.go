package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
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
	annotationAutoInstall  = "catalog.cattle.io/auto-install"
	annotationCertified    = "catalog.cattle.io/certified"
	annotationDisplayName  = "catalog.cattle.io/display-name"
	annotationExperimental = "catalog.cattle.io/experimental"
	annotationFeatured     = "catalog.cattle.io/featured"
	annotationHidden       = "catalog.cattle.io/hidden"
	annotationKubeVersion  = "catalog.cattle.io/kube-version"
	annotationNamespace    = "catalog.cattle.io/namespace"
	annotationReleaseName  = "catalog.cattle.io/release-name"
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
	featuredMax           = 5
)

var (
	Version = "v0.0.0"
	Commit  = "HEAD"
)

// PackageWrapper is a representation of relevant package metadata
type PackageWrapper struct {
	//Additional Chart annotations
	Annotations map[string]string
	//Chart Display Name
	DisplayName string
	//Filtered subset of versions to-be-fetched
	FetchVersions repo.ChartVersions
	//Indicator to generate patch files
	GenPatch bool
	//Path stores the package path in current repository
	Path string
	//LatestStored stores the latest version of the chart currently in the repo
	LatestStored repo.ChartVersion
	//ManualUpdate evaluates true if package does not provide upstream yaml for automated update
	ManualUpdate bool
	//Chart name
	Name string
	//Untracked upstream versions newer than latest tracked
	NewerUntracked []*semver.Version
	//Force only pulling the latest version
	OnlyLatest bool
	//Indicator to write chart to disk
	Save bool
	//SourceMetadata represents metadata fetched from the upstream repository
	SourceMetadata *fetcher.ChartSourceMetadata
	//UpstreamYaml represents the values set in the package's upstream.yaml file
	UpstreamYaml *parse.UpstreamYaml
	//Chart vendor
	Vendor string
	//Formatted version of chart vendor
	ParsedVendor string
}

type PackageList []PackageWrapper

func (p PackageList) Len() int {
	return len(p)
}

func (p PackageList) Swap(i, j int) {
	p[i], p[j] = p[j], p[i]
}

func (p PackageList) Less(i, j int) bool {
	if p[i].SourceMetadata != nil && p[j].SourceMetadata != nil {
		if p[i].ParsedVendor != p[j].ParsedVendor {
			return p[i].ParsedVendor < p[j].ParsedVendor
		}
		return p[i].Name < p[j].Name
	}

	return false
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
			packageWrapper.FetchVersions[0].URLs[0],
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

func (packageWrapper *PackageWrapper) populateManual() (bool, error) {
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
		Source:   "direct",
		Versions: make(repo.ChartVersions, 1),
	}

	packageWrapper.Name = helmChart.Name()
	packageWrapper.Vendor, packageWrapper.ParsedVendor = parseVendor("", packageWrapper.Name, packageWrapper.Path)

	chartVersion := repo.ChartVersion{
		Metadata: &chart.Metadata{
			Name: packageWrapper.Name,
		},
		URLs: make([]string, 1),
	}

	fixedVersion := ""
	if pkg.Version != nil {
		fixedVersion = pkg.Version.String()
	}

	chartVersion.URLs[0] = pkg.Upstream.GetOptions().URL
	chartVersion.Version, err = conform.GeneratePackageVersion(
		helmChart.Metadata.Version,
		pkg.PackageVersion,
		fixedVersion)
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
	if len(packageWrapper.FetchVersions) == 0 {
		return false, nil
	}

	return true, nil

}

// Populates package wrapper with relevant data from upstream, checks for updates,
// writes out package yaml file, and generates package object
// Returns true if newer package version is available
func (packageWrapper *PackageWrapper) populate() (bool, error) {
	var err error
	if packageWrapper.ManualUpdate {
		return packageWrapper.populateManual()
	} else {
		packageWrapper.UpstreamYaml, err = parseUpstream(packageWrapper.Path)
		if err != nil {
			return false, err
		}

		sourceMetadata, err := generateChartSourceMetadata(*packageWrapper.UpstreamYaml)
		if err != nil {
			return false, err
		}

		packageWrapper.SourceMetadata = sourceMetadata
		packageWrapper.Name = sourceMetadata.Versions[0].Name
		packageWrapper.Vendor, packageWrapper.ParsedVendor = parseVendor(packageWrapper.UpstreamYaml.Vendor, packageWrapper.Name, packageWrapper.Path)

		if packageWrapper.OnlyLatest {
			packageWrapper.UpstreamYaml.Fetch = "latest"
			if packageWrapper.UpstreamYaml.TrackVersions != nil {
				packageWrapper.UpstreamYaml.TrackVersions = []string{packageWrapper.UpstreamYaml.TrackVersions[0]}
			}
		}

		packageWrapper.FetchVersions, err = filterVersions(
			packageWrapper.SourceMetadata.Versions,
			packageWrapper.UpstreamYaml.Fetch,
			packageWrapper.UpstreamYaml.TrackVersions)
		if err != nil {
			return false, err
		}

		packageWrapper.LatestStored, err = getLatestStoredVersion(packageWrapper.Name)
		if err != nil {
			return false, err
		}

		if packageWrapper.UpstreamYaml.DisplayName != "" {
			packageWrapper.DisplayName = packageWrapper.UpstreamYaml.DisplayName
		} else {
			packageWrapper.DisplayName = packageWrapper.Name
		}

	}
	if len(packageWrapper.FetchVersions) == 0 {
		return false, nil
	}

	return true, nil
}

func (packageWrapper PackageWrapper) annotate(annotation, value string, remove, onlyLatest bool) error {
	var versionsToUpdate repo.ChartVersions
	chartName := packageWrapper.LatestStored.Name

	allStoredVersions, err := getStoredVersions(chartName)
	if err != nil {
		return err
	}

	if onlyLatest {
		versionsToUpdate = repo.ChartVersions{allStoredVersions[0]}
	} else {
		versionsToUpdate = allStoredVersions
	}

	for _, version := range versionsToUpdate {
		modified := false

		assetsPath := filepath.Join(
			getRepoRoot(),
			repositoryAssetsDir,
			packageWrapper.ParsedVendor,
		)

		versionPath := path.Join(
			getRepoRoot(),
			repositoryChartsDir,
			packageWrapper.ParsedVendor,
			chartName,
		)
		helmChart, err := loader.Load(versionPath)
		if err != nil {
			return err
		}

		if remove {
			modified = conform.RemoveChartAnnotations(helmChart, map[string]string{annotation: value})
		} else {
			modified = conform.ApplyChartAnnotations(helmChart, map[string]string{annotation: value}, true)
		}

		if modified {

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

			err = removeVersionFromIndex(chartName, *version)
			if err != nil {
				return err
			}
		}

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
	packagePath = filepath.ToSlash(packagePath)
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
func commitChanges(updatedList PackageList) error {
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

	opts := git.AddOptions{
		All: true,
	}

	for _, packageWrapper := range updatedList {
		assetsPath := path.Join(
			repositoryAssetsDir,
			packageWrapper.ParsedVendor)

		chartsPath := path.Join(
			repositoryChartsDir,
			packageWrapper.ParsedVendor,
			packageWrapper.Name)

		packagesPath := path.Join(
			repositoryPackagesDir,
			packageWrapper.ParsedVendor,
			packageWrapper.Name)

		opts.Path = assetsPath
		wt.AddWithOptions(&opts)
		opts.Path = chartsPath
		wt.AddWithOptions(&opts)
		opts.Path = packagesPath
		wt.AddWithOptions(&opts)

	}

	wt.Add(indexFile)

	commitMessage := "Charts CI\n```"
	sort.Sort(updatedList)
	for _, packageWrapper := range updatedList {
		lineItem := fmt.Sprintf("  %s/%s:\n",
			packageWrapper.ParsedVendor,
			packageWrapper.Name)
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

	commitMessage += "```"

	wt.Commit(commitMessage, &commitOptions)

	return nil
}

// Cleans up ephemeral chart directory files from package prepare
func cleanPackage(packagePath string, manualUpdate bool) error {
	packageName := strings.TrimPrefix(getRelativePath(packagePath), "/")
	logrus.Infof("Cleaning package %s\n", packageName)
	if manualUpdate {
		logrus.Debugf("Generating package %s\n", packageName)
		pkg, err := generatePackage(packagePath)
		if err != nil {
			return err
		}
		err = pkg.Clean()
		if err != nil {
			return err
		}
	} else {
		os.RemoveAll(path.Join(packagePath, repositoryChartsDir))
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

// Prepares package for modification via patch and overlay
func preparePackage(packagePath string, sourceMetadata *fetcher.ChartSourceMetadata, chartVersion *repo.ChartVersion) error {
	var chart *chart.Chart
	var err error
	logrus.Debugf("Preparing package from %s", packagePath)

	if sourceMetadata.Source == "Git" {
		chart, err = fetcher.LoadChartFromGit(chartVersion.URLs[0], sourceMetadata.SubDirectory, sourceMetadata.Commit)
	} else {
		chart, err = fetcher.LoadChartFromUrl(chartVersion.URLs[0])
	}
	if err != nil {
		return err
	}

	exportPath := path.Join(packagePath, repositoryChartsDir)
	err = conform.ExportChartDirectory(chart, exportPath)
	if err != nil {
		logrus.Error(err)
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
			logrus.Debugf("Comparing upstream version %s (%s) to tracked version %s\n", version.Name, version.Version, trackedVersion)
			if semVer.Major() == trackedSemVer.Major() && semVer.Minor() == trackedSemVer.Minor() {
				logrus.Debugf("Appending version %s tracking %s\n", version.Version, trackedVersion)
				versionList = append(versionList, version)
			} else if semVer.Major() < trackedSemVer.Major() || (semVer.Major() == trackedSemVer.Major() && semVer.Minor() < trackedSemVer.Minor()) {
				break
			}
		}
		trackedVersions[trackedVersion] = versionList
	}

	return trackedVersions
}

func collectNonStoredVersions(versions repo.ChartVersions, storedVersions repo.ChartVersions, fetch string) repo.ChartVersions {
	nonStoredVersions := make(repo.ChartVersions, 0)
	for i, version := range versions {
		parsedVersion, err := semver.NewVersion(version.Version)
		if err != nil {
			logrus.Error(err)
		}
		stored := false
		logrus.Debugf("Checking if version %s is stored\n", version.Version)
		for _, storedVersion := range storedVersions {
			strippedStoredVersion := conform.StripPackageVersion(storedVersion.Version)
			if storedVersion.Version == parsedVersion.String() {
				logrus.Debugf("Found version %s\n", storedVersion.Version)
				stored = true
				break
			} else if strippedStoredVersion == parsedVersion.String() {
				logrus.Debugf("Found modified version %s\n", storedVersion.Version)
				stored = true
				break
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
						logrus.Debugf("Version: %s > %s\n", semVer.String(), storedSemVer.String())
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

func stripPreRelease(versions repo.ChartVersions) repo.ChartVersions {
	strippedVersions := make(repo.ChartVersions, 0)
	for _, version := range versions {
		semVer, err := semver.NewVersion(version.Version)
		if err != nil {
			logrus.Error(err)
			continue
		}
		if semVer.Prerelease() == "" {
			strippedVersions = append(strippedVersions, version)
		}
	}

	return strippedVersions
}

func checkNewerUntracked(tracked []string, upstreamVersions repo.ChartVersions) []string {
	newerUntracked := make([]string, 0)
	latestTracked := getLatestTracked(tracked)
	logrus.Debugf("Tracked Versions: %s\n", tracked)
	logrus.Debugf("Checking for versions newer than latest tracked %s\n", latestTracked)
	if len(tracked) == 0 {
		return newerUntracked
	}
	for _, upstreamVersion := range upstreamVersions {
		semVer, err := semver.NewVersion(upstreamVersion.Version)
		if err != nil {
			logrus.Error(err)
		}
		if semVer.Major() > latestTracked.Major() || (semVer.Major() == latestTracked.Major() && semVer.Minor() > latestTracked.Minor()) {
			logrus.Debugf("Found version %s newer than latest tracked %s", semVer.String(), latestTracked.String())
			newerUntracked = append(newerUntracked, semVer.String())
		} else if semVer.Major() == latestTracked.Major() && semVer.Minor() == latestTracked.Minor() {
			break
		}
	}

	return newerUntracked

}

func filterVersions(upstreamVersions repo.ChartVersions, fetch string, tracked []string) (repo.ChartVersions, error) {
	logrus.Debugf("Filtering versions for %s\n", upstreamVersions[0].Name)
	upstreamVersions = stripPreRelease(upstreamVersions)
	if len(tracked) > 0 {
		if newerUntracked := checkNewerUntracked(tracked, upstreamVersions); len(newerUntracked) > 0 {
			logrus.Warnf("Newer untracked version available: %s (%s)", upstreamVersions[0].Name, strings.Join(newerUntracked, ", "))
		} else {
			logrus.Debug("No newer untracked versions found")
		}
	}
	if len(upstreamVersions) == 0 {
		err := fmt.Errorf("No versions available in upstream or all versions are marked pre-release")
		return repo.ChartVersions{}, err
	}
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

func parseVendor(upstreamYamlVendor, chartName, packagePath string) (string, string) {
	var vendor, vendorPath string
	packagePath = filepath.ToSlash(packagePath)
	packageRelativePath := getRelativePath(packagePath)
	if len(strings.Split(packageRelativePath, "/")) > 2 {
		vendorPath = strings.TrimPrefix(filepath.Dir(packageRelativePath), "/")
	} else {
		vendorPath = strings.TrimPrefix(packageRelativePath, "/")
	}

	if upstreamYamlVendor != "" {
		vendor = upstreamYamlVendor
	} else if len(vendorPath) > 0 {
		vendor = vendorPath
	} else {
		vendor = chartName
	}

	parsedVendor := strings.ReplaceAll(strings.ToLower(vendor), " ", "-")

	return vendor, parsedVendor
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

	err = conform.ApplyOverlayFiles(packagePath)
	if err != nil {
		return nil, err
	}

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
	logrus.Debugf("Conforming package from %s\n", packageWrapper.Path)
	for _, chartVersion := range packageWrapper.FetchVersions {
		logrus.Debugf("Conforming package %s (%s)\n", chartVersion.Name, chartVersion.Version)
		helmChart, err := initializeChart(
			packageWrapper.Path,
			*packageWrapper.SourceMetadata,
			*chartVersion,
			packageWrapper.ManualUpdate,
		)
		if err != nil {
			return err
		}

		if autoInstall := packageWrapper.UpstreamYaml.AutoInstall; autoInstall != "" {
			packageWrapper.Annotations[annotationAutoInstall] = autoInstall
		}

		if packageWrapper.UpstreamYaml.Experimental {
			packageWrapper.Annotations[annotationExperimental] = "true"
		}

		if packageWrapper.UpstreamYaml.Hidden {
			packageWrapper.Annotations[annotationHidden] = "true"
		}

		if !packageWrapper.UpstreamYaml.RemoteDependencies {
			for _, d := range helmChart.Metadata.Dependencies {
				d.Repository = fmt.Sprintf("file://./charts/%s", d.Name)
			}
		}

		if packageWrapper.ManualUpdate {
			packageWrapper.Name = helmChart.Name()
			chartVersion.Version = helmChart.Metadata.Version
			pkg, err := generatePackage(packageWrapper.Path)
			if err != nil {
				return err
			}

			if packageWrapper.GenPatch {
				err = pkg.GeneratePatch()
				if err != nil {
					return err
				}

			}

		} else {
			packageWrapper.Annotations[annotationCertified] = "partner"
			packageWrapper.Annotations[annotationDisplayName] = packageWrapper.DisplayName
			if packageWrapper.UpstreamYaml.ReleaseName != "" {
				packageWrapper.Annotations[annotationReleaseName] = packageWrapper.UpstreamYaml.ReleaseName
			} else {
				packageWrapper.Annotations[annotationReleaseName] = packageWrapper.Name
			}

			conform.OverlayChartMetadata(helmChart, packageWrapper.UpstreamYaml.ChartYaml)

			if val, ok := getByAnnotation(annotationFeatured, "")[packageWrapper.Name]; ok {
				logrus.Debugf("Migrating featured annotation to latest version %s\n", packageWrapper.Name)
				featuredIndex := val[0].Annotations[annotationFeatured]
				packageWrapper.annotate(annotationFeatured, "", true, false)
				packageWrapper.Annotations[annotationFeatured] = featuredIndex
			}

			if packageWrapper.UpstreamYaml.Namespace != "" {
				packageWrapper.Annotations[annotationNamespace] = packageWrapper.UpstreamYaml.Namespace
			}
			if helmChart.Metadata.KubeVersion != "" && packageWrapper.UpstreamYaml.ChartYaml.KubeVersion != "" {
				packageWrapper.Annotations[annotationKubeVersion] = packageWrapper.UpstreamYaml.ChartYaml.KubeVersion
				helmChart.Metadata.KubeVersion = packageWrapper.UpstreamYaml.ChartYaml.KubeVersion
			} else if helmChart.Metadata.KubeVersion != "" {
				packageWrapper.Annotations[annotationKubeVersion] = helmChart.Metadata.KubeVersion
			} else if packageWrapper.UpstreamYaml.ChartYaml.KubeVersion != "" {
				packageWrapper.Annotations[annotationKubeVersion] = packageWrapper.UpstreamYaml.ChartYaml.KubeVersion
			}

			if packageVersion := packageWrapper.UpstreamYaml.PackageVersion; packageVersion != 0 {
				helmChart.Metadata.Version, err = conform.GeneratePackageVersion(helmChart.Metadata.Version, &packageVersion, "")
				if err != nil {
					logrus.Error(err)
				}
			}

			conform.ApplyChartAnnotations(helmChart, packageWrapper.Annotations, false)

		}

		if packageWrapper.Save {
			err = cleanPackage(packageWrapper.Path, packageWrapper.ManualUpdate)
			if err != nil {
				logrus.Debug(err)
			}

			assetsPath := filepath.Join(
				getRepoRoot(),
				repositoryAssetsDir,
				packageWrapper.ParsedVendor)

			chartsPath := filepath.Join(
				getRepoRoot(),
				repositoryChartsDir,
				packageWrapper.ParsedVendor,
				helmChart.Metadata.Name)

			if _, err := os.Stat(chartsPath); !os.IsNotExist(err) {
				os.RemoveAll(chartsPath)
			}

			err = saveChart(helmChart, assetsPath, chartsPath)
			if err != nil {
				return err
			}
		}

	}

	return err
}

// Saves chart to disk as asset gzip and directory
func saveChart(helmChart *chart.Chart, assetsPath, chartsPath string) error {

	logrus.Debugf("Exporting chart assets to %s\n", assetsPath)
	err := conform.ExportChartAsset(helmChart, assetsPath)
	if err != nil {
		return err
	}

	assetFile := fmt.Sprintf("%s-%s.tgz", helmChart.Name(), helmChart.Metadata.Version)
	assetFile = path.Join(assetsPath, assetFile)

	err = conform.Gunzip(assetFile, chartsPath)
	if err != nil {
		logrus.Error(err)
	}

	logrus.Debugf("Exporting chart to %s\n", chartsPath)
	err = conform.ExportChartDirectory(helmChart, chartsPath)
	if err != nil {
		return err
	}

	return nil
}

func getLatestTracked(tracked []string) *semver.Version {
	var latestTracked *semver.Version
	for _, version := range tracked {
		semVer, err := semver.NewVersion(version)
		if err != nil {
			logrus.Error(err)
		}
		if latestTracked == nil || semVer.GreaterThan(latestTracked) {
			latestTracked = semVer
		}
	}

	return latestTracked
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

func getByAnnotation(annotation, value string) map[string]repo.ChartVersions {
	indexYaml, err := readIndex()
	if err != nil {
		logrus.Fatal(err)
	}
	matchedVersions := make(map[string]repo.ChartVersions)

	for chartName := range indexYaml.Entries {
		entries := indexYaml.Entries[chartName]
		for _, version := range entries {
			appendVersion := false
			if _, ok := version.Annotations[annotation]; ok {
				if value != "" {
					if version.Annotations[annotation] == value {
						appendVersion = true
					}
				} else {
					appendVersion = true
				}
			}
			if appendVersion {
				if _, ok := matchedVersions[chartName]; !ok {
					matchedVersions[chartName] = repo.ChartVersions{version}
				} else {
					matchedVersions[chartName] = append(matchedVersions[chartName], version)
				}
			}
		}
	}

	return matchedVersions
}

func removeVersionFromIndex(chartName string, version repo.ChartVersion) error {
	entryIndex := -1
	indexYaml, err := readIndex()
	if err != nil {
		return err
	}
	if _, ok := indexYaml.Entries[chartName]; !ok {
		return fmt.Errorf("%s not present in index entries", chartName)
	}

	indexEntries := indexYaml.Entries[chartName]

	for i, entryVersion := range indexEntries {
		if entryVersion.Version == version.Version {
			entryIndex = i
			break
		}
	}

	if entryIndex >= 0 {
		entries := make(repo.ChartVersions, 0)
		entries = append(entries, indexEntries[:entryIndex]...)
		entries = append(entries, indexEntries[entryIndex+1:]...)
		indexYaml.Entries[chartName] = entries
	} else {
		return fmt.Errorf("version %s not found for chart %s in index", version.Version, chartName)
	}

	indexFilePath := filepath.Join(getRepoRoot(), indexFile)
	err = indexYaml.WriteFile(indexFilePath, 0644)

	return err
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
func fetchUpstreams(packageList PackageList) []string {
	skippedList := make([]string, 0)
	for _, packageWrapper := range packageList {
		err := conformPackage(packageWrapper)
		if err != nil {
			logrus.Error(err)
			skippedList = append(skippedList, packageWrapper.Name)
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
func generatePackageList(currentPackage string) PackageList {
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

	packageList := make(PackageList, 0)
	for _, packageName := range packageNames {
		var packageWrapper PackageWrapper
		//If name is not present in map, fall back to package.yaml method
		if _, ok := packageMap[packageName]; !ok {
			//ManualUpdate indicates usage of package.yaml method
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
func populatePackages(currentPackage string, onlyUpdates bool, onlyLatest bool, print bool) (PackageList, error) {
	packageList := make(PackageList, 0)
	for _, packageWrapper := range generatePackageList(currentPackage) {
		packageWrapper.Annotations = make(map[string]string)
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
			logrus.Infof("Parsed %s/%s\n", packageWrapper.ParsedVendor, packageWrapper.Name)
			if len(packageWrapper.FetchVersions) == 0 {
				logrus.Infof("%s (%s) is up-to-date\n",
					packageWrapper.Vendor, packageWrapper.Name)
			}
			for _, version := range packageWrapper.FetchVersions {
				logrus.Infof("\n  Source: %s\n  Vendor: %s\n  Chart: %s\n  Version: %s\n  URL: %s  \n",
					packageWrapper.SourceMetadata.Source, packageWrapper.Vendor, packageWrapper.Name,
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
	currentPackage := os.Getenv(packageEnvVariable)
	var packageList PackageList
	var err error
	if auto || stage {
		packageList, err = populatePackages(currentPackage, true, false, true)
		for i := range packageList {
			packageList[i].GenPatch = true
			packageList[i].Save = true
		}
	} else {
		packageList, err = populatePackages(currentPackage, false, true, true)
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
	packageList := generatePackageList(os.Getenv(packageEnvVariable))
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
	currentPackage := os.Getenv(packageEnvVariable)
	packageList, err := populatePackages(currentPackage, false, false, true)
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

// CLI function call - Appends annotaion to feature chart in Rancher UI
func addFeaturedChart(c *cli.Context) {
	if len(c.Args()) != 2 {
		logrus.Fatalf("Please provide the chart name and featured number (1 - %d) as arguments\n", featuredMax)
	}
	featuredChart := c.Args().Get(0)
	featuredNumber, err := strconv.Atoi(c.Args().Get(1))
	if err != nil {
		logrus.Fatal(err)
	}
	if featuredNumber < 1 || featuredNumber > featuredMax {
		logrus.Fatalf("Featured number must be between %d and %d\n", 1, featuredMax)
	}

	packageList := generatePackageList(featuredChart)
	if len(packageList) == 0 {
		logrus.Fatalf("Package '%s' not available\n", featuredChart)
	}

	packageList, err = populatePackages(featuredChart, false, false, false)
	if err != nil {
		logrus.Fatal(err)
	}

	featuredVersions := getByAnnotation(annotationFeatured, c.Args().Get(1))

	if len(featuredVersions) > 0 {
		for chartName := range featuredVersions {
			logrus.Errorf("%s already featured at index %d\n", chartName, featuredNumber)
		}
	} else {
		err = packageList[0].annotate(annotationFeatured, c.Args().Get(1), false, true)
		if err != nil {
			logrus.Fatal(err)
		}
	}
}

// CLI function call - Appends annotaion to feature chart in Rancher UI
func removeFeaturedChart(c *cli.Context) {
	if len(c.Args()) != 1 {
		logrus.Fatal("Please provide the chart name as argument")
	}
	featuredChart := c.Args().Get(0)
	packageMap, err := parse.ListPackages(repositoryPackagesDir, "")
	if err != nil {
		logrus.Fatal(err)
	}
	if _, ok := packageMap[featuredChart]; !ok {
		logrus.Fatalf("Package '%s' not available\n", featuredChart)
	}

	packageList, err := populatePackages(featuredChart, false, false, false)
	if err != nil {
		logrus.Fatal(err)
	}

	err = packageList[0].annotate(annotationFeatured, "", true, false)
	if err != nil {
		logrus.Fatal(err)
	}
}

func listFeaturedCharts(c *cli.Context) {
	indexConflict := false
	featuredSorted := make([]string, featuredMax)
	featuredVersions := getByAnnotation(annotationFeatured, "")

	for chartName, chartVersion := range featuredVersions {
		featuredIndex, err := strconv.Atoi(chartVersion[0].Annotations[annotationFeatured])
		if err != nil {
			logrus.Fatal(err)
		}
		if featuredSorted[featuredIndex] != "" {
			indexConflict = true
			featuredSorted[featuredIndex] += fmt.Sprintf(", %s", chartName)
		} else {
			featuredSorted[featuredIndex] = chartName
		}
	}
	if indexConflict {
		logrus.Errorf("Multiple charts given same featured index")
	}

	for i, chartName := range featuredSorted {
		if featuredSorted[i] != "" {
			fmt.Printf("%d: %s\n", i, chartName)
		}
	}

}

// CLI function call - Appends annotation to hide chart in Rancher UI
func hideChart(c *cli.Context) {
	if len(c.Args()) < 1 {
		logrus.Fatal("Provide package name(s) as argument")
	}
	for _, currentPackage := range c.Args() {
		packageList, err := populatePackages(currentPackage, false, false, false)
		if err != nil {
			logrus.Error(err)
		}

		if len(packageList) == 1 {
			err = packageList[0].annotate(annotationHidden, "true", false, false)
			if err != nil {
				logrus.Error(err)
			}
		}
	}
}

// CLI function call - Cleans package object(s)
func cleanCharts(c *cli.Context) {
	packageList := generatePackageList(os.Getenv(packageEnvVariable))
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

	excludeFiles := make(map[string]struct{})
	var exclude = struct{}{}
	excludeFiles["README.md"] = exclude

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
		if _, err := os.Stat(updatePath); os.IsNotExist(err) {
			logrus.Infof("Directory '%s' not in source. Skipping...", dirPath)
			continue
		}
		if _, err := os.Stat(upstreamPath); os.IsNotExist(err) {
			logrus.Infof("Directory '%s' not in upstream. Skipping...", dirPath)
			continue
		}
		newComparison, err := validate.CompareDirectories(upstreamPath, updatePath, excludeFiles)
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

	logrus.Infof("Successfully validated\n  Upstream: %s\n  Branch: %s\n",
		configYaml.Validate[0].Url, configYaml.Validate[0].Branch)

}

func main() {
	if len(os.Getenv("DEBUG")) > 0 {
		logrus.SetLevel(logrus.DebugLevel)
	}

	app := cli.NewApp()
	app.Name = "partner-charts-ci"
	app.Version = fmt.Sprintf("%s (%s)", Version, Commit)
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
			Name:  "feature",
			Usage: "Manipulate charts featured in Rancher UI",
			Subcommands: []cli.Command{
				{
					Name:   "list",
					Usage:  "List currently featured charts",
					Action: listFeaturedCharts,
				},
				{
					Name:   "add",
					Usage:  "Add featured annotation to chart",
					Action: addFeaturedChart,
				},
				{
					Name:   "remove",
					Usage:  "Remove featured annotation from chart",
					Action: removeFeaturedChart,
				},
			},
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

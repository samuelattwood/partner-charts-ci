package parse

import (
	"io/ioutil"
	"os"
	"path"
	"path/filepath"

	"github.com/sirupsen/logrus"

	"helm.sh/helm/v3/pkg/chart"

	"sigs.k8s.io/yaml"
)

const (
	PackageOptionsFile  = "package.yaml"
	UpstreamOptionsFile = "upstream.yaml"
)

type PackageYaml struct {
	Commit         string `json:"commit,omitempty"`
	PackageVersion int    `json:"packageVersion,omitempty"`
	Path           string `json:"-"`
	SubDirectory   string `json:"subdirectory,omitempty"`
	Url            string `json:"url"`
	Version        string `json:"version,omitempty"`
}

type UpstreamYaml struct {
	AHPackageName   string         `json:"ArtifactHubPackage"`
	AHRepoName      string         `json:"ArtifactHubRepo"`
	ChartYaml       chart.Metadata `json:"ChartMetadata"`
	DisplayName     string         `json:"DisplayName"`
	Fetch           string         `json:"Fetch"`
	GitBranch       string         `json:"GitBranch"`
	GitHubRelease   bool           `json:"GitHubRelease"`
	GitRepoUrl      string         `json:"GitRepo"`
	GitSubDirectory string         `json:"GitSubdirectory"`
	HelmChart       string         `json:"HelmChart"`
	HelmRepoUrl     string         `json:"HelmRepo"`
	TrackVersions   []string       `json:"TrackVersions"`
	ReleaseName     string         `json:"ReleaseName"`
	Vendor          string         `json:"Vendor"`
}

func (packageYaml PackageYaml) Write(overWrite bool) error {
	filePath := path.Join(packageYaml.Path, PackageOptionsFile)
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		if !overWrite {
			return nil
		} else {
			packageYaml.Remove()
		}
	}

	packageYamlFile, err := yaml.Marshal(packageYaml)
	if err != nil {
		return err
	}

	err = ioutil.WriteFile(filePath, packageYamlFile, 0644)
	if err != nil {
		return err
	}

	return nil
}

func (packageYaml PackageYaml) Remove() error {
	logrus.Debugf("Removing package yaml from %s\n", packageYaml.Path)
	filePath := path.Join(packageYaml.Path, PackageOptionsFile)
	err := os.Remove(filePath)
	if err != nil {
		return err
	}

	return nil
}

func ListPackages(packageDirectory string, currentPackage string) (map[string]string, error) {
	packageList := make(map[string]string)
	var searchDirectory string

	if currentPackage != "" {
		searchDirectory = filepath.Join(packageDirectory, currentPackage)
	} else {
		searchDirectory = packageDirectory
	}

	if _, err := os.Stat(searchDirectory); os.IsNotExist(err) {
		return packageList, err
	}

	findPackage := func(path string, info os.FileInfo, err error) error {
		if err != nil {
			logrus.Error(err)
		}

		if !info.IsDir() && info.Name() == UpstreamOptionsFile {
			packagePath := filepath.Dir(path)
			packageName := filepath.Base(packagePath)
			packageList[packageName] = packagePath
		}

		return nil
	}

	return packageList, filepath.Walk(searchDirectory, findPackage)
}

func ParseUpstreamYaml(packagePath string) (UpstreamYaml, error) {
	upstreamYamlPath := filepath.Join(packagePath, UpstreamOptionsFile)
	logrus.Debugf("attempting to parse %s", upstreamYamlPath)
	upstreamYamlFile, err := ioutil.ReadFile(upstreamYamlPath)
	upstreamYaml := UpstreamYaml{}
	if err != nil {
		logrus.Debug(err)
	} else {
		err = yaml.Unmarshal(upstreamYamlFile, &upstreamYaml)
	}

	return upstreamYaml, err
}

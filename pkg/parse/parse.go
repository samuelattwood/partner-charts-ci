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
	PackageVersion string `json:"packageVersion,omitempty"`
	SubDirectory   string `json:"subdirectory,omitempty"`
	Url            string `json:"url"`
}

type UpstreamYaml struct {
	AHPackageName   string         `json:"ArtifactHubPackage"`
	AHRepoName      string         `json:"ArtifactHubRepo"`
	ChartYaml       chart.Metadata `json:"Chart.yaml"`
	DisplayName     string         `json:"DisplayName"`
	GitBranch       string         `json:"GitBranch"`
	GitHubRelease   bool           `json:"GitHubRelease"`
	GitRepoUrl      string         `json:"GitRepo"`
	GitSubDirectory string         `json:"GitSubdirectory"`
	HelmChart       string         `json:"HelmChart"`
	HelmRepoUrl     string         `json:"HelmRepo"`
	ReleaseName     string         `json:"ReleaseName"`
	Vendor          string         `json:"Vendor"`
}

func GeneratePackageYaml(packageYamlValues map[string]string) PackageYaml {
	return PackageYaml{
		Commit:         packageYamlValues["Commit"],
		PackageVersion: packageYamlValues["PackageVersion"],
		SubDirectory:   packageYamlValues["SubDirectory"],
		Url:            packageYamlValues["Url"],
	}
}

func (packageYaml PackageYaml) WritePackageYaml(packagePath string) error {
	filePath := path.Join(packagePath, PackageOptionsFile)
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

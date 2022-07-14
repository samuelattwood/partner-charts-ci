package parse

import (
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/samuelattwood/partner-charts-ci/pkg/options"
	"github.com/sirupsen/logrus"

	"sigs.k8s.io/yaml"
)

func CreatePackageYaml(packagePath string, chartSourceMeta *options.ChartSourceMetadata) {
	filePath := packagePath + "/" + options.PackageOptionsFile
	packageYaml, err := yaml.Marshal(&chartSourceMeta.PackageYaml)
	if err != nil {
		logrus.Debug(err)
	}

	err = ioutil.WriteFile(filePath, packageYaml, 0644)
	if err != nil {
		logrus.Debug(err)
	}
}

func ListPackages(packageDirectory string, currentPackage string) ([]string, error) {
	var packageList []string
	var searchDirectory string

	if currentPackage != "" {
		searchDirectory = filepath.Join(packageDirectory, currentPackage)
	} else {
		searchDirectory = packageDirectory
	}

	if _, err := os.Stat(packageDirectory); os.IsNotExist(err) {
		logrus.Error(err)
	}

	findPackage := func(path string, info os.FileInfo, err error) error {
		if err != nil {
			logrus.Error(err)
		}

		if !info.IsDir() && info.Name() == options.UpstreamOptionsFile {
			packagePath := filepath.Dir(path)
			packageName := filepath.Base(packagePath)
			packageList = append(packageList, packageName)
		}

		return nil
	}

	return packageList, filepath.Walk(searchDirectory, findPackage)

}

func ParseUpstreamYaml(filePath string) (options.UpstreamYaml, error) {
	upstreamYamlFile, err := ioutil.ReadFile(filePath)
	upstreamYaml := options.UpstreamYaml{}
	if err != nil {
		logrus.Debug(err)
	} else {
		err = yaml.Unmarshal(upstreamYamlFile, &upstreamYaml)
	}

	return upstreamYaml, err
}

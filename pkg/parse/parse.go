package parse

import (
	"io/ioutil"
	"os"
	"path"
	"path/filepath"

	"github.com/samuelattwood/partner-charts-ci/pkg/options"
	"github.com/sirupsen/logrus"

	"sigs.k8s.io/yaml"
)

func CreatePackageYaml(packageWrapper *options.PackageWrapper) {
	filePath := path.Join(packageWrapper.Path, options.PackageOptionsFile)
	packageYaml, err := yaml.Marshal(packageWrapper.SourceMetadata.PackageYaml)
	if err != nil {
		logrus.Debug(err)
	}

	err = ioutil.WriteFile(filePath, packageYaml, 0644)
	if err != nil {
		logrus.Debug(err)
	}
}

func ListPackages(packageDirectory string, currentPackage string) ([]*options.PackageWrapper, error) {
	var packageList []*options.PackageWrapper
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

		if !info.IsDir() && info.Name() == options.UpstreamOptionsFile {
			packagePath := filepath.Dir(path)
			packageName := filepath.Base(packagePath)
			packageList = append(packageList, &options.PackageWrapper{Name: packageName, Path: packagePath})
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

package conform

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
)

const (
	overlayDir   = "overlay"
	generatedDir = "generated-changes"
)

func GetFileList(searchPath string, relative bool) ([]string, []string, error) {
	fileList := make([]string, 0)
	dirList := make([]string, 0)
	walkFunc := func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		filePath := path
		if relative {
			filePath, err = filepath.Rel(searchPath, path)
			if err != nil {
				return err
			}
		}
		if info.IsDir() && path != searchPath {
			dirList = append(dirList, filePath)
		} else if !info.IsDir() {
			fileList = append(fileList, filePath)
		}

		return nil
	}

	err := filepath.Walk(searchPath, walkFunc)
	if err != nil {
		return nil, nil, err
	}

	return dirList, fileList, nil
}

func LinkOverlayFiles(packagePath string) error {
	overlayPath := filepath.Join(packagePath, overlayDir)
	if _, err := os.Stat(overlayPath); !os.IsNotExist(err) {
		dirList, fileList, err := GetFileList(overlayPath, true)
		if err != nil {
			return err
		}
		if len(dirList) == 0 {
			dirList = append(dirList, "")
		}
		for _, dir := range dirList {
			generatedPath := filepath.Join(packagePath, generatedDir, overlayDir, dir)
			if _, err := os.Stat(generatedPath); os.IsNotExist(err) {
				os.MkdirAll(generatedPath, 0755)
			}
		}

		for _, file := range fileList {
			depth := len(strings.Split(file, "/")) + 1
			pathPrefix := strings.Repeat("../", depth)
			generatedPath := filepath.Join(packagePath, generatedDir, overlayDir, file)
			if _, err := os.Stat(generatedPath); !os.IsNotExist(err) {
				err = os.Remove(generatedPath)
				if err != nil {
					logrus.Error(err)
				}
			}
			symLinkPath := filepath.Join(pathPrefix, overlayDir, file)
			err = os.Symlink(symLinkPath, generatedPath)
			if err != nil {
				logrus.Error(err)
			}
		}

	}

	return nil
}

func RemoveOverlayFiles(packagePath string) error {
	overlayPath := filepath.Join(packagePath, overlayDir)
	if _, err := os.Stat(overlayPath); !os.IsNotExist(err) {
		_, fileList, err := GetFileList(overlayPath, true)
		if err != nil {
			return err
		}
		for _, file := range fileList {
			generatedPath := filepath.Join(packagePath, generatedDir, overlayDir, file)
			if _, err := os.Stat(generatedPath); !os.IsNotExist(err) {
				err = os.Remove(generatedPath)
				if err != nil {
					logrus.Error(err)
				}
			}
		}
	}

	return nil
}

// Load and Unloads Chart to ensure consistent layout for overlay
func StandardizeChartDirectory(sourcePath string, targetPath string) error {
	if _, err := os.Stat(sourcePath); os.IsNotExist(err) {
		return fmt.Errorf("%s does not exist", sourcePath)
	}

	helmChart, err := loader.Load(sourcePath)
	if err != nil {
		logrus.Debug(err)
	}

	if targetPath == "" {
		targetPath = sourcePath
		os.RemoveAll(sourcePath)
	}

	return ExportChartDirectory(helmChart, targetPath)

}

func ExportChartAsset(helmChart *chart.Chart, targetPath string) error {
	_, err := chartutil.Save(helmChart, targetPath)
	if err != nil {
		return err
	}

	return nil
}

func ExportChartDirectory(chart *chart.Chart, targetPath string) error {
	tempDir, err := os.MkdirTemp("", "chartDir")
	if err != nil {
		return err
	}

	err = chartutil.SaveDir(chart, tempDir)
	if err != nil {
		err = fmt.Errorf("unable to save chart to %s", tempDir)
		return err
	}

	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		os.RemoveAll(targetPath)
	}

	err = os.MkdirAll(filepath.Dir(targetPath), 0755)
	if err != nil {
		return err
	}

	chartOutputPath := filepath.Join(tempDir, chart.Metadata.Name)
	err = os.Rename(chartOutputPath, targetPath)
	if err != nil {
		return err
	}

	err = os.RemoveAll(tempDir)
	if err != nil {
		return err
	}

	return nil
}

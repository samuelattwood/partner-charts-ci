package export

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
)

//Load and Unloads Chart to ensure consistent layout for overlay
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

	os.MkdirAll(filepath.Dir(targetPath), 0755)

	chartOutputPath := filepath.Join(tempDir, chart.Metadata.Name)
	os.Rename(chartOutputPath, targetPath)
	os.RemoveAll(tempDir)

	return nil
}

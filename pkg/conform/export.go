package conform

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
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

func ApplyOverlayFiles(packagePath string) error {
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
			generatedPath := filepath.Join(packagePath, "charts", dir)
			if _, err := os.Stat(generatedPath); os.IsNotExist(err) {
				os.MkdirAll(generatedPath, 0755)
			}
		}

		for _, filePath := range fileList {
			srcPath := filepath.Join(overlayPath, filePath)
			if _, err := os.Stat(srcPath); os.IsNotExist(err) {
				return err
			}

			srcFile, err := os.Open(srcPath)
			if err != nil {
				return err
			}
			defer srcFile.Close()

			generatedPath := filepath.Join(packagePath, "charts", filePath)
			if _, err := os.Stat(generatedPath); !os.IsNotExist(err) {
				logrus.Warnf("Replacing %s with overlay file", filePath)
				err = os.Remove(generatedPath)
				if err != nil {
					return err
				}
			}
			dstFile, err := os.Create(generatedPath)
			if err != nil {
				return err
			}
			defer dstFile.Close()

			if _, err = io.Copy(dstFile, srcFile); err != nil {
				return err
			}
		}

	}

	return nil

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
	wd, err := os.Getwd()
	if err != nil {
		return err
	}

	tempDir, err := os.MkdirTemp(wd, "chartDir")
	if err != nil {
		return err
	}

	tgz, err := chartutil.Save(chart, tempDir)
	if err != nil {
		err = fmt.Errorf("unable to save chart archive to %s", tempDir)
		return err
	}

	chartOutputPath := filepath.Join(tempDir, chart.Name())

	if err = Gunzip(tgz, chartOutputPath); err != nil {
		return err
	}

	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		os.RemoveAll(targetPath)
	}

	if err = os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return err
	}

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

func ExportDependenciesToDirectory(chart *chart.Chart, targetPath string) error {
	for _, c := range chart.Dependencies() {
		logrus.Debugf("Saving dependency %s to %s\n", c.Name(), targetPath)
		err := chartutil.SaveDir(c, targetPath)
		if err != nil {
			return err
		}
	}

	return nil
}

func stripRootPath(path string) string {
	newPath := filepath.ToSlash(path)
	rootPath := strings.Split(newPath, "/")[0]
	newPath = strings.TrimPrefix(newPath, "/")
	newPath = strings.TrimPrefix(newPath, rootPath)
	newPath = strings.TrimPrefix(newPath, "/")

	return filepath.FromSlash(newPath)
}

func Gunzip(path string, outPath string) error {
	if !strings.HasSuffix(path, ".tgz") && !strings.HasPrefix(path, ".gz") {
		return fmt.Errorf("Expecting file of type .gz or .tgz")
	}

	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("%s does not exist or is inaccessible", path)
	}

	gzipFile, err := os.Open(path)
	if err != nil {
		return err
	}
	defer gzipFile.Close()

	gzipReader, err := gzip.NewReader(gzipFile)
	if err != nil {
		return err
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)

	for {
		h, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		filePath := filepath.Join(outPath, stripRootPath(h.Name))
		parentPath := filepath.Dir(filePath)
		if _, err := os.Stat(parentPath); os.IsNotExist(err) {
			if err = os.MkdirAll(parentPath, 0755); err != nil {
				return err
			}
		}

		if h.Typeflag == tar.TypeDir {
			if err = os.MkdirAll(filePath, os.FileMode(h.Mode)); err != nil {
				return err
			}
		} else if h.Typeflag == tar.TypeReg {
			f, err := os.Create(filePath)
			if err != nil {
				return err
			}
			defer f.Close()

			if _, err = io.Copy(f, tarReader); err != nil {
				return err
			}

			if err = os.Chmod(filePath, os.FileMode(h.Mode)); err != nil {
				return err
			}
		} else if h.Name != "pax_global_header" {
			return fmt.Errorf("unknown file type for %s", h.Name)
		}

	}

	return nil
}

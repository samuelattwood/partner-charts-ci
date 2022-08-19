package validate

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/sirupsen/logrus"

	"sigs.k8s.io/yaml"
)

type ConfigurationYaml struct {
	Validate []ValidateUpstream
}

type ValidateUpstream struct {
	Url    string
	Branch string
}

type DirectoryComparison struct {
	Unchanged []string
	Modified  []string
	Added     []string
	Removed   []string
	Match     bool
}

func (directoryComparison *DirectoryComparison) Merge(newComparison DirectoryComparison) {
	directoryComparison.Unchanged = append(directoryComparison.Unchanged, newComparison.Unchanged...)
	directoryComparison.Modified = append(directoryComparison.Modified, newComparison.Modified...)
	directoryComparison.Added = append(directoryComparison.Added, newComparison.Added...)
	directoryComparison.Removed = append(directoryComparison.Removed, newComparison.Removed...)

	if !newComparison.Match {
		directoryComparison.Match = false
	}

}

func ReadConfig(configYamlPath string) (ConfigurationYaml, error) {
	upstreamYamlFile, err := os.ReadFile(configYamlPath)
	configYaml := ConfigurationYaml{}
	if err != nil {
		logrus.Debug(err)
	} else {
		err = yaml.Unmarshal(upstreamYamlFile, &configYaml)
	}

	return configYaml, err
}

func CloneRepo(url string, branch string, targetDir string) error {
	branchReference := fmt.Sprintf("refs/heads/%s", branch)
	cloneOptions := git.CloneOptions{
		URL:           url,
		ReferenceName: plumbing.ReferenceName(branchReference),
		SingleBranch:  true,
		Depth:         1,
	}

	_, err := git.PlainClone(targetDir, false, &cloneOptions)
	if err != nil {
		return err
	}

	return nil
}

func ChecksumFile(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	hash := fmt.Sprintf("%x", h.Sum(nil))

	return hash, nil
}

func CompareDirectories(leftPath, rightPath string, exclude map[string]struct{}) (DirectoryComparison, error) {
	directoryComparison := DirectoryComparison{
		Match: true,
	}
	checkedSet := make(map[string]struct{})
	var checked = struct{}{}

	compareLeft := func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			logrus.Error(err)
		}
		relativePath := strings.TrimPrefix(filePath, leftPath)
		checkedSet[relativePath] = checked

		if _, ok := exclude[info.Name()]; !ok && !info.IsDir() {
			rightFilePath := path.Join(rightPath, relativePath)
			if _, err := os.Stat(rightFilePath); os.IsNotExist(err) {
				directoryComparison.Removed = append(directoryComparison.Removed, relativePath)
				return nil
			}
			leftCheckSum, err := ChecksumFile(filePath)
			if err != nil {
				logrus.Error(err)
			}
			rightCheckSum, err := ChecksumFile(rightFilePath)
			if err != nil {
				logrus.Error(err)
			}

			if leftCheckSum != rightCheckSum {
				directoryComparison.Modified = append(directoryComparison.Modified, relativePath)
			} else {
				directoryComparison.Unchanged = append(directoryComparison.Unchanged, relativePath)
			}
		}

		return nil
	}

	compareRight := func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			logrus.Error(err)
		}
		relativePath := strings.TrimPrefix(filePath, rightPath)

		if _, ok := checkedSet[relativePath]; !ok && !info.IsDir() {
			directoryComparison.Added = append(directoryComparison.Added, relativePath)
		}

		return nil
	}

	filepath.Walk(leftPath, compareLeft)
	filepath.Walk(rightPath, compareRight)

	if len(directoryComparison.Modified)+len(directoryComparison.Added)+len(directoryComparison.Removed) > 0 {
		directoryComparison.Match = false
	}

	return directoryComparison, nil
}

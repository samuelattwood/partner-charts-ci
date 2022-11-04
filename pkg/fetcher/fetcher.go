package fetcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/google/go-github/v45/github"
	"github.com/samuelattwood/partner-charts-ci/pkg/parse"
	"github.com/sirupsen/logrus"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/repo"

	"sigs.k8s.io/yaml"
)

const (
	artifactHubApi = "https://artifacthub.io/api/v1/packages/helm"
)

type ArtifactHubApiHelmRepo struct {
	OrgDisplayName string `json:"organization_display_name,omitempty"`
	OrgName        string `json:"organization_name,omitempty"`
	Url            string `json:"url"`
}

type ArtifactHubApiHelm struct {
	ContentUrl string                 `json:"content_url"`
	Repository ArtifactHubApiHelmRepo `json:"repository"`
	Version    string                 `json:"version"`
}

type ChartSourceMetadata struct {
	Commit       string
	Source       string
	SubDirectory string
	Versions     repo.ChartVersions
}

// Constructs Chart Metadata for latest version published to Helm Repository
func fetchUpstreamHelmrepo(upstreamYaml parse.UpstreamYaml) (ChartSourceMetadata, error) {
	url := fmt.Sprintf("%s/index.yaml", upstreamYaml.HelmRepoUrl)

	indexYaml := repo.NewIndexFile()
	chartSourceMeta := ChartSourceMetadata{}

	chartSourceMeta.Source = "HelmRepo"

	resp, err := http.Get(url)
	if err != nil {
		logrus.Debug(err)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logrus.Debug(err)
	}

	err = yaml.Unmarshal([]byte(body), indexYaml)
	if err != nil {
		logrus.Debug(err)
	}
	if _, ok := indexYaml.Entries[upstreamYaml.HelmChart]; !ok {
		return chartSourceMeta, fmt.Errorf("Helm chart: %s/%s not found", upstreamYaml.HelmRepoUrl, upstreamYaml.HelmChart)
	}

	indexYaml.SortEntries()
	upstreamVersions := indexYaml.Entries[upstreamYaml.HelmChart]

	for i := range upstreamVersions {
		chartUrl := upstreamVersions[i].URLs[0]
		if !strings.HasPrefix(chartUrl, "http") {
			upstreamVersions[i].URLs[0] = upstreamYaml.HelmRepoUrl + "/" + chartUrl
		}
	}

	chartSourceMeta.Versions = indexYaml.Entries[upstreamYaml.HelmChart]

	return chartSourceMeta, nil
}

// Constructs Chart Metadata for latest version published to ArtifactHub
func fetchUpstreamArtifacthub(upstreamYaml parse.UpstreamYaml) (ChartSourceMetadata, error) {
	url := fmt.Sprintf("%s/%s/%s", artifactHubApi, upstreamYaml.AHRepoName, upstreamYaml.AHPackageName)

	apiResp := ArtifactHubApiHelm{}
	chartSourceMeta := ChartSourceMetadata{}

	chartSourceMeta.Source = "ArtifactHub"

	resp, err := http.Get(url)
	if err != nil {
		logrus.Debug(err)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logrus.Debug(err)
	}

	json.Unmarshal([]byte(body), &apiResp)
	if apiResp.ContentUrl == "" {
		return chartSourceMeta, fmt.Errorf("ArtifactHub package: %s/%s not found", upstreamYaml.AHRepoName, upstreamYaml.AHPackageName)
	}

	versionMetadata := chart.Metadata{
		Version: apiResp.Version,
	}

	version := repo.ChartVersion{
		Metadata: &versionMetadata,
		URLs:     []string{apiResp.ContentUrl},
	}

	versions := repo.ChartVersions{&version}

	chartSourceMeta.Versions = versions

	return chartSourceMeta, nil
}

func getGitHubUserAndRepo(gitUrl string) (string, string, error) {
	if !strings.HasPrefix(gitUrl, "https://github.com") {
		err := fmt.Errorf("%s is not a GitHub URL", gitUrl)
		return "", "", err
	}

	baseUrl := strings.TrimPrefix(gitUrl, "https://")
	baseUrl = strings.TrimSuffix(baseUrl, ".git")
	split := strings.Split(baseUrl, "/")

	return split[1], split[2], nil

}

func fetchGitHubRelease(repoUrl string) (string, error) {
	var releaseCommit string
	client := github.NewClient(nil)
	gitHubUser, gitHubRepo, err := getGitHubUserAndRepo(repoUrl)
	if err != nil {
		return "", err
	}
	ctx := context.Background()
	opt := &github.ListOptions{}
	latestRelease, _, err := client.Repositories.GetLatestRelease(ctx, gitHubUser, gitHubRepo)
	if err != nil {
		return "", err
	}
	tags, _, _ := client.Repositories.ListTags(ctx, gitHubUser, gitHubRepo, opt)
	for _, tag := range tags {
		if tag.GetName() == *latestRelease.TagName {
			releaseCommit = *tag.GetCommit().SHA
			break
		}
	}

	logrus.Debugf("Fetching GitHub Release: %s (%s)\n", *latestRelease.Name, releaseCommit)

	return releaseCommit, nil
}

// Constructs Chart Metadata for latest version published to Git Repository
func fetchUpstreamGit(upstreamYaml parse.UpstreamYaml) (ChartSourceMetadata, error) {
	var upstreamCommit string
	cloneOptions := git.CloneOptions{
		URL: upstreamYaml.GitRepoUrl,
	}

	if upstreamYaml.GitBranch != "" {
		branchReference := fmt.Sprintf("refs/heads/%s", upstreamYaml.GitBranch)
		cloneOptions.ReferenceName = plumbing.ReferenceName(branchReference)
	}
	if !upstreamYaml.GitHubRelease {
		cloneOptions.Depth = 1
	}

	tempDir, err := os.MkdirTemp("", "gitRepo")
	if err != nil {
		return ChartSourceMetadata{}, err
	}
	r, err := git.PlainClone(tempDir, false, &cloneOptions)
	if err != nil {
		return ChartSourceMetadata{}, err
	}

	if upstreamYaml.GitHubRelease {
		logrus.Debug("Fetching GitHub Release")
		upstreamCommit, err = fetchGitHubRelease(upstreamYaml.GitRepoUrl)
		if err != nil {
			return ChartSourceMetadata{}, err
		}

		wt, err := r.Worktree()
		if err != nil {
			return ChartSourceMetadata{}, err
		}

		err = wt.Checkout(&git.CheckoutOptions{
			Hash: plumbing.NewHash(upstreamCommit),
		})
		if err != nil {
			return ChartSourceMetadata{}, err
		}
	} else {
		ref, err := r.Head()
		if err != nil {
			return ChartSourceMetadata{}, err
		}

		upstreamCommit = ref.Hash().String()
	}

	sourcePath := tempDir
	if upstreamYaml.GitSubDirectory != "" {
		sourcePath = filepath.Join(sourcePath, upstreamYaml.GitSubDirectory)
		if _, err := os.Stat(sourcePath); os.IsNotExist(err) {
			err = fmt.Errorf("git subdirectory '%s' does not exist", upstreamYaml.GitSubDirectory)
			return ChartSourceMetadata{}, err
		}
	}
	logrus.Debugf("Git Temp Directory: %s\n", sourcePath)
	helmChart, err := loader.Load(sourcePath)
	if err != nil {
		return ChartSourceMetadata{}, err
	}

	version := repo.ChartVersion{
		Metadata: helmChart.Metadata,
		URLs:     []string{upstreamYaml.GitRepoUrl},
	}

	versions := repo.ChartVersions{&version}

	chartSourceMeta := ChartSourceMetadata{
		Commit:       upstreamCommit,
		Source:       "Git",
		SubDirectory: upstreamYaml.GitSubDirectory,
		Versions:     versions,
	}

	err = os.RemoveAll(tempDir)
	if err != nil {
		logrus.Debug(err)
	}

	return chartSourceMeta, nil
}

func FetchUpstream(upstreamYaml parse.UpstreamYaml) (ChartSourceMetadata, error) {
	if upstreamYaml.AHRepoName != "" && upstreamYaml.AHPackageName != "" {
		return fetchUpstreamArtifacthub(upstreamYaml)
	} else if upstreamYaml.HelmRepoUrl != "" && upstreamYaml.HelmChart != "" {
		return fetchUpstreamHelmrepo(upstreamYaml)
	} else if upstreamYaml.GitRepoUrl != "" {
		return fetchUpstreamGit(upstreamYaml)
	} else {
		err := errors.New("no valid repo options found")
		return ChartSourceMetadata{}, err
	}
}

func LoadChartFromUrl(url string) (*chart.Chart, error) {
	logrus.Debugf("Loading chart from %s\n", url)
	resp, err := http.Get(url)
	if err != nil {
		logrus.Errorf("Unable to fetch url %s", url)
		return nil, err
	}

	defer resp.Body.Close()

	chart, err := loader.LoadArchive(resp.Body)
	if err != nil {
		logrus.Error(err)
		return nil, err
	}

	return chart, nil
}

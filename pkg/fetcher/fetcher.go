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
	DisplayName    string `json:"display_name,omitempty"`
	Name           string `json:"name"`
	OrgDisplayName string `json:"organization_display_name,omitempty"`
	OrgName        string `json:"organization_name,omitempty"`
	Url            string `json:"url"`
}

type ArtifactHubApiHelm struct {
	ContentUrl     string                 `json:"content_url"`
	Name           string                 `json:"name"`
	NormalizedName string                 `json:"normalized_name"`
	Repository     ArtifactHubApiHelmRepo `json:"repository"`
	Version        string                 `json:"version"`
}

type ChartSourceMetadata struct {
	Commit       string
	DisplayName  string
	Name         string
	Source       string
	SubDirectory string
	Vendor       string
	ParsedVendor string
	ReleaseName  string
	Versions     repo.ChartVersions
}

func parseVendor(vendor string) string {
	return strings.ReplaceAll(strings.ToLower(vendor), " ", "-")
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

	chartSourceMeta.Name = upstreamVersions[0].Metadata.Name
	chartSourceMeta.DisplayName = upstreamVersions[0].Metadata.Name
	chartSourceMeta.Versions = indexYaml.Entries[upstreamYaml.HelmChart]

	if upstreamYaml.Vendor != "" {
		chartSourceMeta.Vendor = upstreamYaml.Vendor
	} else {
		chartSourceMeta.Vendor = chartSourceMeta.Name
	}

	chartSourceMeta.ParsedVendor = parseVendor(chartSourceMeta.Vendor)

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

	if upstreamYaml.Vendor != "" {
		chartSourceMeta.Vendor = upstreamYaml.Vendor
	} else if apiResp.Repository.OrgName != "" {
		chartSourceMeta.Vendor = apiResp.Repository.OrgName
	} else {
		chartSourceMeta.Vendor = apiResp.NormalizedName
	}

	chartSourceMeta.ParsedVendor = parseVendor(chartSourceMeta.Vendor)

	versionMetadata := chart.Metadata{
		Name:    apiResp.Name,
		Version: apiResp.Version,
	}

	version := repo.ChartVersion{
		Metadata: &versionMetadata,
		URLs:     []string{apiResp.ContentUrl},
	}

	versions := repo.ChartVersions{&version}

	chartSourceMeta.DisplayName = apiResp.Name
	chartSourceMeta.Name = apiResp.NormalizedName
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

	return releaseCommit, nil
}

// Constructs Chart Metadata for latest version published to Git Repository
func fetchUpstreamGit(upstreamYaml parse.UpstreamYaml) (ChartSourceMetadata, error) {
	var upstreamCommit string
	cloneOptions := git.CloneOptions{
		URL: upstreamYaml.GitRepoUrl,
	}

	if upstreamYaml.GitBranch != "" {
		cloneOptions.RemoteName = upstreamYaml.GitBranch
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
	helmChart, err := loader.Load(sourcePath)
	if err != nil {
		logrus.Debug(err)
	}

	version := repo.ChartVersion{
		Metadata: helmChart.Metadata,
		URLs:     []string{upstreamYaml.GitRepoUrl},
	}

	versions := repo.ChartVersions{&version}

	chartSourceMeta := ChartSourceMetadata{
		Commit:       upstreamCommit,
		DisplayName:  helmChart.Metadata.Name,
		Name:         helmChart.Metadata.Name,
		Source:       "Git",
		SubDirectory: upstreamYaml.GitSubDirectory,
		Versions:     versions,
	}

	if upstreamYaml.Vendor != "" {
		chartSourceMeta.Vendor = upstreamYaml.Vendor
	} else {
		chartSourceMeta.Vendor = chartSourceMeta.Name
	}

	chartSourceMeta.ParsedVendor = parseVendor(chartSourceMeta.Vendor)

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

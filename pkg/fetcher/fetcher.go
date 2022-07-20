package fetcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/google/go-github/v45/github"
	"github.com/samuelattwood/partner-charts-ci/pkg/options"
	"github.com/sirupsen/logrus"

	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/repo"

	"sigs.k8s.io/yaml"
)

const (
	artifactHubApi        = "https://artifacthub.io/api/v1/packages/helm"
	repositoryPackagesDir = "packages"
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

//Constructs Chart Metadata for latest version published to Helm Repository
func fetchUpstreamHelmrepo(upstreamYaml *options.UpstreamYaml) (options.ChartSourceMetadata, error) {
	url := fmt.Sprintf("%s/index.yaml", upstreamYaml.HelmRepoUrl)

	indexYaml := repo.NewIndexFile()
	chartSourceMeta := options.ChartSourceMetadata{}

	chartSourceMeta.Source = "HelmRepo"

	resp, err := http.Get(url)
	if err != nil {
		logrus.Debug(err)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		logrus.Debug(err)
	}

	err = yaml.Unmarshal([]byte(body), indexYaml)
	if err != nil {
		logrus.Debug(err)
	}
	if _, ok := indexYaml.Entries[upstreamYaml.HelmChart]; !ok {
		return options.ChartSourceMetadata{}, fmt.Errorf("Helm chart: %s/%s not found",
			upstreamYaml.HelmRepoUrl, upstreamYaml.HelmChart)
	}

	indexYaml.SortEntries()

	chartEntries := indexYaml.Entries[upstreamYaml.HelmChart]
	latestEntry := chartEntries[0]

	chartUrl := latestEntry.URLs[0]
	if !strings.HasPrefix(chartUrl, "http") {
		chartUrl = upstreamYaml.HelmRepoUrl + "/" + latestEntry.URLs[0]
	}

	chartSourceMeta.Name = latestEntry.Metadata.Name
	chartSourceMeta.DisplayName = latestEntry.Metadata.Name
	chartSourceMeta.Url = chartUrl
	chartSourceMeta.Version = latestEntry.Version
	chartSourceMeta.PackageYaml = options.PackageYaml{
		Url: chartSourceMeta.Url,
	}

	if upstreamYaml.Vendor != "" {
		chartSourceMeta.Vendor = upstreamYaml.Vendor
	} else {
		chartSourceMeta.Vendor = chartSourceMeta.Name
	}

	return chartSourceMeta, nil
}

//Constructs Chart Metadata for latest version published to ArtifactHub
func fetchUpstreamArtifacthub(upstreamYaml *options.UpstreamYaml) (options.ChartSourceMetadata, error) {
	url := fmt.Sprintf("%s/%s/%s", artifactHubApi, upstreamYaml.AHRepoName, upstreamYaml.AHPackageName)

	apiResp := ArtifactHubApiHelm{}
	chartSourceMeta := options.ChartSourceMetadata{}

	chartSourceMeta.Source = "ArtifactHub"

	resp, err := http.Get(url)
	if err != nil {
		logrus.Debug(err)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		logrus.Debug(err)
	}

	json.Unmarshal([]byte(body), &apiResp)
	if apiResp.ContentUrl == "" {
		return options.ChartSourceMetadata{}, fmt.Errorf("ArtifactHub package: %s/%s not found",
			upstreamYaml.AHRepoName, upstreamYaml.AHPackageName)
	}

	if upstreamYaml.Vendor != "" {
		chartSourceMeta.Vendor = upstreamYaml.Vendor
	} else if apiResp.Repository.OrgName != "" {
		chartSourceMeta.Vendor = apiResp.Repository.OrgName
	} else {
		chartSourceMeta.Vendor = apiResp.NormalizedName
	}

	chartSourceMeta.DisplayName = apiResp.Name
	chartSourceMeta.Name = apiResp.NormalizedName
	chartSourceMeta.Url = apiResp.ContentUrl
	chartSourceMeta.Version = apiResp.Version
	chartSourceMeta.PackageYaml = options.PackageYaml{
		Url: chartSourceMeta.Url,
	}

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

func fetchGitHubRelease(upstreamYaml *options.UpstreamYaml) (string, error) {
	var releaseCommit string
	client := github.NewClient(nil)
	gitHubUser, gitHubRepo, err := getGitHubUserAndRepo(upstreamYaml.GitRepoUrl)
	if err != nil {
		logrus.Error(err)
	}
	ctx := context.Background()
	opt := &github.ListOptions{}
	releases, _, _ := client.Repositories.ListReleases(ctx, gitHubUser, gitHubRepo, opt)

	for _, rel := range releases {
		if !*rel.Prerelease {
			releaseCommit = *rel.TargetCommitish
			break
		}
	}

	return releaseCommit, nil
}

//Constructs Chart Metadata for latest version published to Git Repository
func fetchUpstreamGit(upstreamYaml *options.UpstreamYaml) (options.ChartSourceMetadata, error) {
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
		return options.ChartSourceMetadata{}, err
	}

	r, err := git.PlainClone(tempDir, false, &cloneOptions)
	if err != nil {
		return options.ChartSourceMetadata{}, err
	}

	if upstreamYaml.GitHubRelease {
		upstreamCommit, err = fetchGitHubRelease(upstreamYaml)
		if err != nil {
			logrus.Debug(err)
		}

		wt, err := r.Worktree()
		if err != nil {
			logrus.Error(err)

		}

		err = wt.Checkout(&git.CheckoutOptions{
			Hash: plumbing.NewHash(upstreamCommit),
		})
		if err != nil {
			logrus.Error(err)
		}
	} else {
		ref, err := r.Head()
		if err != nil {
			logrus.Debug(err)
		}

		upstreamCommit = ref.Hash().String()
	}

	sourcePath := tempDir
	if upstreamYaml.GitSubDirectory != "" {
		sourcePath = filepath.Join(sourcePath, upstreamYaml.GitSubDirectory)
	}
	helmChart, err := loader.Load(sourcePath)
	if err != nil {
		logrus.Debug(err)
	}

	chartSourceMeta := options.ChartSourceMetadata{
		DisplayName: helmChart.Metadata.Name,
		Name:        helmChart.Metadata.Name,
		Url:         upstreamYaml.GitRepoUrl,
		Version:     helmChart.Metadata.Version,
		Source:      "Git",
		PackageYaml: options.PackageYaml{
			Url:          upstreamYaml.GitRepoUrl,
			Commit:       upstreamCommit,
			SubDirectory: upstreamYaml.GitSubDirectory,
		},
	}

	if upstreamYaml.Vendor != "" {
		chartSourceMeta.Vendor = upstreamYaml.Vendor
	} else {
		chartSourceMeta.Vendor = chartSourceMeta.Name
	}

	err = os.RemoveAll(tempDir)
	if err != nil {
		logrus.Debug(err)
	}

	return chartSourceMeta, nil
}

func FetchUpstream(upstreamYaml *options.UpstreamYaml) (options.ChartSourceMetadata, error) {
	if upstreamYaml.AHRepoName != "" && upstreamYaml.AHPackageName != "" {
		return fetchUpstreamArtifacthub(upstreamYaml)
	} else if upstreamYaml.HelmRepoUrl != "" && upstreamYaml.HelmChart != "" {
		return fetchUpstreamHelmrepo(upstreamYaml)
	} else if upstreamYaml.GitRepoUrl != "" {
		return fetchUpstreamGit(upstreamYaml)
	} else {
		err := errors.New("no valid repo options found")
		return options.ChartSourceMetadata{}, err
	}
}

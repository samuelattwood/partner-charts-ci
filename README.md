# partner-charts-ci

This tool is intended to aid in ingest, generation, and maintenance of the Rancher partner Helm chart repository. It permits fetching the latest published chart from a Helm Repo, Git Repo, or Artifact Hub, automatically setting necessary alterations, and updating the repo index and assets.

### Configuration File

The tool reads a configuration yaml, `upstream.yaml`, to know where to fetch the upstream chart. This file is also able to define any alterations for valid variables in the Chart.yaml as described by [Helm](https://helm.sh/docs/topics/charts/#the-chart-file-structure).


Options for `upstream.yaml`
| Variable | Requires | Description |
| ------------- | ------------- |------------- |
| ArtifactHubPackage | ArtifactHubRepo | Defines the package to pull from the defined ArtifactHubRepo
| ArtifactHubRepo | ArtifactHubPackage | Defines the repo to access on Artifact Hub
| Chart.yaml | | Allows setting/overriding the value of any valid Chart.yaml variable
| DisplayName | | Sets the name the chart will be listed under in the Rancher UI
| GitBranch | GitRepo | Defines which branch to pull from the upstream GitRepo
| GitHubRelease | GitRepo | If true, will pull latest GitHub release from repo. Requires GitHub URL
| GitRepo | | Defines the git repo to pull from
| GitSubdirectory | GitRepo | Allows selection of a subdirectory of the upstream git repo to pull the chart from
| HelmChart | HelmRepo | Defines which chart to pull from the upstream Helm repo
| HelmRepo | HelmChart | Defines the upstream Helm repo to pull from
| PackageVersion | | Used to generate new patch version of chart
| ReleaseName | | Sets the value of the release-name Rancher annotation
| Vendor | | Sets the vendor name providing the chart
| Version | | Allows for overriding of upstream chart version

### Artifact Hub
```yaml
---
ArtifactHubRepo: kubewarden
ArtifactHubPackage: kubewarden-controller
Vendor: SUSE
Chart.yaml:
  kubeVersion: '1.21-0 - 1.24-0'
  icon: https://www.kubewarden.io/images/icon-kubewarden.svg
```

### Git Repo
```yaml
---
GitRepo: https://github.com/kubewarden/helm-charts.git
GitBranch: main
GitSubdirectory: charts/kubewarden-controller
Vendor: SUSE
Chart.yaml:
  kubeVersion: '1.21-0 - 1.24-0'
  icon: https://www.kubewarden.io/images/icon-kubewarden.svg
```

### GitHub Release
```yaml
---
GitRepo: https://github.com/kubewarden/helm-charts.git
GitHubRelease: true
GitSubdirectory: charts/kubewarden-controller
Vendor: SUSE
Chart.yaml:
  kubeVersion: '1.21-0 - 1.24-0'
  icon: https://www.kubewarden.io/images/icon-kubewarden.svg
```

### Helm Repo
```yaml
---
HelmRepo: https://charts.kubewarden.io
HelmChart: kubewarden-controller
Vendor: SUSE
Chart.yaml:
  kubeVersion: '1.21-0 - 1.24-0'
  icon: https://www.kubewarden.io/images/icon-kubewarden.svg
```

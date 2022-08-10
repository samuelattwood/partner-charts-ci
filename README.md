# partner-charts-ci

This tool is intended to aid in ingest, generation, and maintenance of the Rancher partner Helm chart repository. It permits fetching the latest published chart from a Helm Repo, Git Repo, or Artifact Hub, automatically setting necessary alterations, and updating the repo index and assets.

### Submission Process
1. Fork the [Rancher Partner Charts](https://github.com/rancher/partner-charts/) repository
2. Clone your fork
3. Ensure the 'main-source' branch is checked out
4. Create subdirectories in **packages** in the form of *vendor/chart*
5. Create your **upstream.yaml**
6. Commit your **upstream.yaml**
7. Push your commit and open a pull request

```bash
git clone -b main-source git@github.com:samuelattwood/partner-charts.git
cd partner-charts
mkdir -p packages/suse/kubewarden-controller
cat <<EOF > packages/suse/kubewarden-controller/upstream.yaml
---
HelmRepo: https://charts.kubewarden.io
HelmChart: kubewarden-controller
Vendor: SUSE
ChartMetadata:
  kubeVersion: '1.21-0 - 1.24-0'
  icon: https://www.kubewarden.io/images/icon-kubewarden.svg
EOF

git add packages/suse/kubewarden-controller
git commit -m "Submitting suse/kubewarden-controller"
git push origin main-source

# Open Your Pull Request
```

### Using the tool
If you would like to test your configuration using this tool, simply download the latest release for your architecture. The 'auto' function is what will be run to generate new versions.

The example below downloads the macOS Universal Binary and assumes we have already committed an **upstream.yaml** to **packages/suse/kubewarden-controller/upstream.yaml**
```bash
git clone -b main-source git@github.com:samuelattwood/partner-charts.git
cd partner-charts
curl -L -o partner-charts-ci https://github.com/samuelattwood/partner-charts-ci/releases/latest/download/partner-charts-ci-darwin-universal
chmod +x partner-charts-ci
export PACKAGE=suse/kubewarden-controller
./partner-charts-ci auto
```

### Configuration File

The tool reads a configuration yaml, `upstream.yaml`, to know where to fetch the upstream chart. This file is also able to define any alterations for valid variables in the Chart.yaml as described by [Helm](https://helm.sh/docs/topics/charts/#the-chart-file-structure).


Options for `upstream.yaml`
| Variable | Requires | Description |
| ------------- | ------------- |------------- |
| ArtifactHubPackage | ArtifactHubRepo | Defines the package to pull from the defined ArtifactHubRepo
| ArtifactHubRepo | ArtifactHubPackage | Defines the repo to access on Artifact Hub
| ChartMetadata | | Allows setting/overriding the value of any valid Chart.yaml variable
| DisplayName | | Sets the name the chart will be listed under in the Rancher UI
| Fetch | HelmChart, HelmRepo | Selects set of charts to pull from upstream.<br />- **latest** will pull only the latest chart version *default*<br />- **newer** will pull all newer versions than currently stored<br />- **all** will pull all versions
| GitBranch | GitRepo | Defines which branch to pull from the upstream GitRepo
| GitHubRelease | GitRepo | If true, will pull latest GitHub release from repo. Requires GitHub URL
| GitRepo | | Defines the git repo to pull from
| GitSubdirectory | GitRepo | Allows selection of a subdirectory of the upstream git repo to pull the chart from
| HelmChart | HelmRepo | Defines which chart to pull from the upstream Helm repo
| HelmRepo | HelmChart | Defines the upstream Helm repo to pull from
| PackageVersion | | Used to generate new patch version of chart
| ReleaseName | | Sets the value of the release-name Rancher annotation
| TrackVersions | HelmChart, HelmRepo | Allows selection of multiple *Major.Minor* versions to track from upstream independently.
| Vendor | | Sets the vendor name providing the chart
| Version | | Allows for overriding of upstream chart version

### Helm Repo
```yaml
---
HelmRepo: https://charts.kubewarden.io
HelmChart: kubewarden-controller
Vendor: SUSE
Fetch: newer
TrackVersions:
  - 0.4
  - 1.0
  - 1.1
ChartMetadata:
  kubeVersion: '1.21-0 - 1.24-0'
  icon: https://www.kubewarden.io/images/icon-kubewarden.svg
```

### Artifact Hub
```yaml
---
ArtifactHubRepo: kubewarden
ArtifactHubPackage: kubewarden-controller
Vendor: SUSE
ChartMetadata:
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
ChartMetadata:
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
ChartMetadata:
  kubeVersion: '1.21-0 - 1.24-0'
  icon: https://www.kubewarden.io/images/icon-kubewarden.svg
```

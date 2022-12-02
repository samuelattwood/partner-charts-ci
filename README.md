# partner-charts-ci

This tool is intended to aid in ingest, generation, and maintenance of the Rancher partner Helm chart repository. It permits fetching the latest published chart from a Helm Repo, Git Repo, or Artifact Hub, automatically setting necessary alterations, and updating the repo index and assets.

## Building
Binaries are provided for macOS (Universal) and Linux (x86_64).

Ensure your host has Golang 1.18 or newer then simply build with
```bash
make build
```

#### macOS Universal Build
```bash
make build-darwin-universal
```

## CI Process
The majority of the day-to-day CI operation is handled by the 'auto' subcommand which will run a full check against all configured charts, download any updates, and form a commit with the changes.

#### 1. Clone your fork of the [Rancher Partner Charts](github.com/rancher/partner-charts) repository
```bash
git clone -b main-source git@github.com:<your_github>/partner-charts.git
````
#### 2. Ensure that `git status` is reporting a clean working tree
```bash
➜  partner-charts git:(main-source) git status
On branch main-source
Your branch is up to date with 'origin/main-source'.

nothing to commit, working tree clean
```
#### 3. Pull the latest CI build
```bash
scripts/pull-ci-scripts
```
#### 4. Run the auto function
```bash
bin/partner-charts-ci auto
```
#### 5. Run a validation
```bash
bin/partner-charts-ci validate
```
#### 6. Checkout the 'main' branch
```bash
git checkout main
```
#### 7. Remove the current `index.yaml` and `assets`
```bash
rm -r assets index.yaml
```
#### 8. Copy in the updated `index.yaml` and `assets`
```bash
git checkout main-source -- index.yaml assets
```
#### 9. Add, commit, and push your changes
```bash
git add index.yaml assets
git commit -m "Release Partner Charts"
git push origin main
git push origin main-source
```
#### 10. Open a Pull-Request for both your `main-source` and `main` branches
- The `main-source` PR message should auto-populate with the list of additions/updates
- For the `main` PR you should include the PR number for the related `main-source` PR

## Featuring or Hiding a chart
Featuring and hiding charts is done by appending the `catalog.cattle.io/featured` or `catalog.cattle.io/hidden` chart annotation, respectively. 
The CI tool is able to perform these changes for you, to easly update the asset gzip, the charts directory, and the index.yaml.

If you open a PR after modifying an existing chart, the `validation` stage will expectedly fail, as the main goal is to ensure no accidental modification of already released charts.

In order to avoid this, somewhere in the title of the PR, include the string `[modified charts]`. This will cause the PR check to skip that part of the validation. For example, when you open the PR you could title it "Hiding suse/kubewarden-controller chart [modified charts]".

To view the currently featured charts
```bash
bin/partner-charts-ci feature list
```
To feature a chart
```bash
bin/partner-charts-ci feature add suse/kubewarden-controller 2
```
To remove the featured annotation
```bash
bin/partner-charts-ci feature remove suse/kubewarden-controller
```
To hide the chart
```bash
bin/partner-charts-ci hide suse/kubewarden-controller
```

After any of those changes, simply add, commit, push - and open a PR with "[modified charts]" in the title
```bash
git add index.yaml assets charts
git commit -m "Hiding suse/kubewarden-controller"
git push origin main-source

# Open a Pull Request
```



## Chart Submission Process
1. Fork the [Rancher Partner Charts](https://github.com/rancher/partner-charts/) repository
2. Clone your fork
3. Ensure the 'main-source' branch is checked out
4. Create subdirectories in **packages** in the form of *vendor/chart*
5. Create your **upstream.yaml**
6. Create any add-on files like your app-readme.md and questions.yaml in an 'overlay' subdirectory (Optional)
6. Commit your packages directory
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
DisplayName: Kubewarden Controller
ChartMetadata:
  kubeVersion: '>=1.21-0'
  icon: https://www.kubewarden.io/images/icon-kubewarden.svg
EOF

mkdir packages/suse/kubewarden-controller/overlay
echo "Example app-readme.md" > packages/suse/kubewarden-controller/overlay/app-readme.md

git add packages/suse/kubewarden-controller
git commit -m "Submitting suse/kubewarden-controller"
git push origin main-source

# Open Your Pull Request
```

### Using the tool
If you would like to test your configuration using this tool, simply run the provided script to download the tool. The 'auto' function is what will be run to generate new versions.

The example below assumes we have already committed an **upstream.yaml** to **packages/suse/kubewarden-controller/upstream.yaml**
```bash
git clone -b main-source git@github.com:samuelattwood/partner-charts.git
cd partner-charts
scripts/pull-ci-scripts
export PACKAGE=suse/kubewarden-controller
bin/partner-charts-ci auto
```

## Command Reference
Some commands respect the `PACKAGE` environment variable. This can be used to specify a chart in the format as output by the `list` command, `<vendor>/<chart>`. This environment variable may also be set to just the top level `<vendor>` directory to apply to all charts contained within that vendor.
| Command | Description |
| ------------- | ------------- |
| list | Lists all charts found with an **upstream.yaml** file in the `packages` directory. If `PACKAGE` environment variable is set, will only list chart(s) that match
| prepare | Included for backwards-compatability. Prepares a copy of the chart in the chart's `packages` directory for modification via GNU patch
| patch | Included for backwards-compatability. Generates patch files after alterations made following `prepare` command
| clean | Included for backwards-compatability. Cleans chart created from `prepare` command
| auto | Automated CI process. Checks all configured charts for updates in upstream, downloads updates, makes necessary alterations, stores chart assets, updates index, and commits changes. If `PACKAGE` environment variable is set, will only check and update specified chart(s)
| stage | Does everything auto does except create the final commit. Useful for testing. If `PACKAGE` environment variable is set, will only check and updated specified chart(s)
| unstage | Equivalent to running `git clean -d -f && git checkout -f .`
| hide | Alters existing chart to add `catalog.cattle.io/hidden: "true"` annotation in index and assets. Accepts one chart name as argument, in the format as printed by `list`
| [feature](#feature) | Alters existing chart to add, remove, or list charts with `catalog.cattle.io/featured` annotation
| validate | Validates current repository against configured released repo in `configuration.yaml` to ensure released assets are not being modified

### Subcommands
#### `feature`
| Command | Arguments | Description |
| ------------- | ------------- | ------------- |
| list | N/A | Lists the current charts with the featured annotation and their associated index. Listed name is the chart name as listed in the `index.yaml`, not the chart name in the `<vendor>/<chart>` format
| add | Accepts two arguemnts. The chart name in the format as printed by the standard `list` command, `<vendor>/<chart>`, and the index to be featured at (1-5) | Adds the `catalog.cattle.io/featured: <index>` annotaton to a given chart
| remove | Accepts one chart name as argument, in the format as printed by the standard `list` command, `<vendor>/<chart>` | Removes the `catalog.cattle.io/featured` annotation from a given chart

### Overlay
Any files placed in the *packages/vendor/chart/overlay* directory will be overlayed onto the chart. This allows for adding or overwriting files within the chart as needed. The primary intended purpose is for adding the app-readme.md and questions.yaml files.

### Configuration File

The tool reads a configuration yaml, `upstream.yaml`, to know where to fetch the upstream chart. This file is also able to define any alterations for valid variables in the Chart.yaml as described by [Helm](https://helm.sh/docs/topics/charts/#the-chart-file-structure).


Options for `upstream.yaml`
| Variable | Requires | Description |
| ------------- | ------------- |------------- |
| ArtifactHubPackage | ArtifactHubRepo | Defines the package to pull from the defined ArtifactHubRepo
| ArtifactHubRepo | ArtifactHubPackage | Defines the repo to access on Artifact Hub
| AutoInstall | | Allows setting a required additional chart to deploy prior to current chart, such as a dedicated CRDs chart
| ChartMetadata | | Allows setting/overriding the value of any valid Chart.yaml variable
| DisplayName | | Sets the name the chart will be listed under in the Rancher UI
| Experimental | | Adds the 'experimental' annotation which adds a flag on the UI entry
| Fetch | HelmChart, HelmRepo | Selects set of charts to pull from upstream.<br />- **latest** will pull only the latest chart version *default*<br />- **newer** will pull all newer versions than currently stored<br />- **all** will pull all versions
| GitBranch | GitRepo | Defines which branch to pull from the upstream GitRepo
| GitHubRelease | GitRepo | If true, will pull latest GitHub release from repo. Requires GitHub URL
| GitRepo | | Defines the git repo to pull from
| GitSubdirectory | GitRepo | Allows selection of a subdirectory of the upstream git repo to pull the chart from
| HelmChart | HelmRepo | Defines which chart to pull from the upstream Helm repo
| HelmRepo | HelmChart | Defines the upstream Helm repo to pull from
| Hidden | | Adds the 'hidden' annotation which hides the chart from the Rancher UI
| Namespace | | Addes the 'namespace' annotation which hard-codes a deployment namespace for the chart
| PackageVersion | | Used to generate new patch version of chart
| ReleaseName | | Sets the value of the release-name Rancher annotation. Defaults to the chart name
| TrackVersions | HelmChart, HelmRepo | Allows selection of multiple *Major.Minor* versions to track from upstream independently.
| Vendor | | Sets the vendor name providing the chart

### Helm Repo
```yaml
---
HelmRepo: https://charts.kubewarden.io
HelmChart: kubewarden-controller
Vendor: SUSE
DisplayName: Kubewarden Controller
Fetch: newer
TrackVersions:
  - 0.4
  - 1.0
  - 1.1
ChartMetadata:
  kubeVersion:  '>=1.21-0'
  icon: https://www.kubewarden.io/images/icon-kubewarden.svg
```

### Artifact Hub
```yaml
---
ArtifactHubRepo: kubewarden
ArtifactHubPackage: kubewarden-controller
Vendor: SUSE
DisplayName: Kubewarden Controller
ChartMetadata:
  kubeVersion: '>=1.21-0'
  icon: https://www.kubewarden.io/images/icon-kubewarden.svg
```

### Git Repo
```yaml
---
GitRepo: https://github.com/kubewarden/helm-charts.git
GitBranch: main
GitSubdirectory: charts/kubewarden-controller
Vendor: SUSE
DisplayName: Kubewarden Controller
ChartMetadata:
  kubeVersion: '>=1.21-0'
  icon: https://www.kubewarden.io/images/icon-kubewarden.svg
```

### GitHub Release
```yaml
---
GitRepo: https://github.com/kubewarden/helm-charts.git
GitHubRelease: true
GitSubdirectory: charts/kubewarden-controller
Vendor: SUSE
DisplayName: Kubewarden Controller
ChartMetadata:
  kubeVersion: '>=1.21-0'
  icon: https://www.kubewarden.io/images/icon-kubewarden.svg
```

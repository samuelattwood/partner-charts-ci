package conform

import (
	"fmt"
	"math"

	"github.com/blang/semver"
	"github.com/samuelattwood/partner-charts-ci/pkg/fetcher"

	"helm.sh/helm/v3/pkg/chart"
)

var (
	PatchNumMultiplier = uint64(math.Pow10(2))
	MaxPatchNum        = PatchNumMultiplier - 1
)

func OverlayChartMetadata(chartMetadata *chart.Metadata, overlay chart.Metadata) {
	if overlay.Name != "" {
		chartMetadata.Name = overlay.Name
	}
	if overlay.Home != "" {
		chartMetadata.Home = overlay.Home
	}
	if overlay.Sources != nil {
		chartMetadata.Sources = overlay.Sources
	}
	if overlay.Version != "" {
		chartMetadata.Version = overlay.Version
	}
	if overlay.Description != "" {
		chartMetadata.Description = overlay.Description
	}
	if overlay.Keywords != nil {
		chartMetadata.Keywords = overlay.Keywords
	}
	if overlay.Maintainers != nil {
		chartMetadata.Maintainers = overlay.Maintainers
	}
	if overlay.Icon != "" {
		chartMetadata.Icon = overlay.Icon
	}
	if overlay.APIVersion != "" {
		chartMetadata.APIVersion = overlay.APIVersion
	}
	if overlay.Condition != "" {
		chartMetadata.Condition = overlay.Condition
	}
	if overlay.Tags != "" {
		chartMetadata.Tags = overlay.Tags
	}
	if overlay.AppVersion != "" {
		chartMetadata.AppVersion = overlay.AppVersion
	}
	if overlay.Deprecated {
		chartMetadata.Deprecated = overlay.Deprecated
	}
	if overlay.Annotations != nil {
		chartMetadata.Annotations = overlay.Annotations
	}
	/* Leaving in place, commented, to match upstream Helm metadata
	   Annotation 'catalog.cattle.io/kube-version' is prefered
	if overlay.KubeVersion != "" {
		chartMetadata.KubeVersion = overlay.KubeVersion
	}
	*/
	if overlay.Dependencies != nil {
		chartMetadata.Dependencies = overlay.Dependencies
	}
	if overlay.Type != "" {
		chartMetadata.Type = overlay.Type
	}

}

func ApplyChartAnnotations(chartMetadata *chart.Metadata, chartSourceMetadata *fetcher.ChartSourceMetadata) {
	if chartMetadata.Annotations == nil {
		chartMetadata.Annotations = make(map[string]string)
	}

	if _, ok := chartMetadata.Annotations["catalog.cattle.io/certified"]; !ok {
		chartMetadata.Annotations["catalog.cattle.io/certified"] = "partner"
	}
	if _, ok := chartMetadata.Annotations["catalog.cattle.io/display-name"]; !ok {
		chartMetadata.Annotations["catalog.cattle.io/display-name"] = chartSourceMetadata.DisplayName
	}
	if _, ok := chartMetadata.Annotations["catalog.cattle.io/release-name"]; !ok {
		chartMetadata.Annotations["catalog.cattle.io/release-name"] = chartSourceMetadata.Name
	}
}

func GeneratePackageVersion(upstreamChartVersion string, packageVersion *int, version *semver.Version) (string, error) {
	if version != nil {
		return version.String(), nil
	}
	if packageVersion != nil {
		chartVersion, err := semver.Make(upstreamChartVersion)
		if err != nil {
			return "", err
		}

		if uint64(*packageVersion) >= MaxPatchNum {
			return "", fmt.Errorf("package version %d is greater than maximum of %d", *packageVersion, MaxPatchNum)
		}

		chartVersion.Patch = PatchNumMultiplier*chartVersion.Patch + uint64(*packageVersion)

		return chartVersion.String(), nil
	}

	chartVersion, err := semver.Make(upstreamChartVersion)
	if err != nil {
		return "", err
	}

	return chartVersion.String(), nil
}

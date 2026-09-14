package structs

type Region string

const (
	CN Region = "cn"
	US Region = "us"
	EU Region = "eu"
)

// allRegions is the single source of truth for the regions this tool supports.
// The remote sources.json only ships cn/us/eu, so anything added here must also
// exist remotely.
var allRegions = []Region{US, CN, EU}

// AllRegions returns the supported regions in menu order.
func AllRegions() []Region {
	out := make([]Region, len(allRegions))
	copy(out, allRegions)
	return out
}

// AllRegionStrings returns the supported regions in menu order as strings.
func AllRegionStrings() []string {
	out := make([]string, 0, len(allRegions))
	for _, r := range allRegions {
		out = append(out, string(r))
	}
	return out
}

// StringToRegion resolves a region string to a Region. The second return value
// is false when the input is not a supported region.
func StringToRegion(region string) (Region, bool) {
	for _, r := range allRegions {
		if string(r) == region {
			return r, true
		}
	}
	return "", false
}

// RegistrySources is a map of regions to registry regions
type RegistrySources map[Region]RegistryRegionSources

// RegistryRegionSources is a map of package managers to urls
type RegistryRegionSources map[string][]string

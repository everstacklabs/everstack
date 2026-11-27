package apikey

// Surface is an externally reachable capability of a bot key.
type Surface string

const (
	SurfaceInference Surface = "inference"
	SurfaceMCP       Surface = "mcp"
	SurfaceTelemetry Surface = "telemetry"
	SurfaceA2A       Surface = "a2a"
)

func ValidSurface(s Surface) bool {
	switch s {
	case SurfaceInference, SurfaceMCP, SurfaceTelemetry, SurfaceA2A:
		return true
	}
	return false
}

// Allowed preserves SQL NULL (ordinary keys) versus an empty, restricted set.
func Allowed(surfaces []string, s Surface) bool {
	if surfaces == nil {
		return true
	}
	if !ValidSurface(s) {
		return false
	}
	for _, allowed := range surfaces {
		if allowed == string(s) {
			return true
		}
	}
	return false
}

// StoredHashPrefix marks the stored hash of a surface-limited key. A gateway
// built before surface limits existed looks keys up by the bare hash and has
// no idea a key can be limited: if it found a bot key it would treat it as an
// ordinary key with full access. With the prefix, such a build (an old replica
// during a rollout, or the image after a rollback) simply does not find the
// key and refuses it. Builds that know about surfaces look up both forms.
const StoredHashPrefix = "bot:"

// StoredHash is the value kept in api_keys.hash for a key with these surfaces.
func StoredHash(hash string, surfaces []string) string {
	if surfaces == nil {
		return hash
	}
	return StoredHashPrefix + hash
}

//go:build !linux

package transcode

// lowPriority is a no-op outside Linux (development builds); the container is Linux.
func lowPriority(bin string, args []string) (name string, argv []string) {
	return bin, args
}

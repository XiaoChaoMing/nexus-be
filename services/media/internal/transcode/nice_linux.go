//go:build linux

package transcode

// _niceLevel lowers the encoder's CPU priority so live transcoding keeps up while a job runs.
const _niceLevel = "10"

// lowPriority runs bin through nice(1); nice execs bin, so killing the process kills the encoder.
func lowPriority(bin string, args []string) (name string, argv []string) {
	return "nice", append([]string{"-n", _niceLevel, bin}, args...)
}

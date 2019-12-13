package compose

import (
	"fmt"
	"strings"
)

type Volume struct {
	Source   string
	Target   string
	ReadOnly bool
}

func NewVolume(volumeString string) (Volume, error) {
	paths := strings.Split(volumeString, ":")
	if len(paths) < 2 || len(paths) > 3 {
		return Volume{}, fmt.Errorf("invalid volume path: %v", volumeString)
	}
	return Volume{
		Source:   paths[0],
		Target:   paths[1],
		ReadOnly: isReadOnly(paths),
	}, nil
}

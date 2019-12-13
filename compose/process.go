package compose

import "strings"

type Process struct {
	ID string
	Driver
	Status
	OnStop     string
	PID        int
	StopSignal string
}


type Driver int64

func (driver Driver) String() string {
	switch driver {
	case DOCKER:
		return "DOCKER"
	case EXEC:
		return "EXEC"
	default:
		return "UNKNOWN_DRIVER"
	}
}

func DriverFromString(str string) Driver {
	switch strings.ToUpper(str) {
	case "DOCKER":
		return DOCKER
	case "EXEC":
		return EXEC
	default:
		return UNKNOWN_DRIVER
	}
}

type Status int64

func (status Status) String() string {
	switch status {
	case RUNNING:
		return "RUNNING"
	case STOPPED:
		return "STOPPED"
	default:
		return "UNKNOWN_DRIVER"
	}
}

const (
	UNKNOWN_DRIVER Driver = 0
	DOCKER         Driver = 1
	EXEC           Driver = 2

	UNKNOWN_STATUS Status = 0
	RUNNING        Status = 1
	STOPPED        Status = 2
)

package compose

import (
	"fmt"
	"github.com/docker/docker/api/types/strslice"
	"github.com/docker/go-connections/nat"
	"strings"
)

type Service struct {
	Image      string
	Entrypoint string
	Env        []string
	Volumes    []string
	Driver     string
	Command    string
	OnStop     string `yaml:"on_stop"`
	Ports      []string
	DependsOn  []string `yaml:"depends_on"`
	RestartPolicy RestartPolicy `yaml:"restart"`
	StopSignal string `yaml:"stop_signal"`
}

func (s *Service) GetImage() string {
	if len(strings.Split(s.Image, "/")) != 0 {
		return s.Image
	}
	return "docker.io/library/" + s.Image
}

func (s *Service) GetEntrypoint() strslice.StrSlice {
	if s.Entrypoint == "" {
		return nil
	}
	return strslice.StrSlice{s.Entrypoint}
}

func (s *Service) GetPortBindings() (nat.PortMap, error) {
	portmap := nat.PortMap{}
	for _, port := range s.Ports {
		pb, err := NewPortBinding(port)
		if err != nil {
			return nil, err
		}
		if _, ok := portmap[pb.Container]; !ok {
			portmap[pb.Container] = []nat.PortBinding{}
		}
		portmap[pb.Container] = append(portmap[pb.Container], nat.PortBinding{
			HostIP:   "0.0.0.0",
			HostPort: pb.Host,
		})
	}
	return portmap, nil
}


type Binding struct {
	Container nat.Port
	Host      string
}

func NewPortBinding(portStr string) (Binding, error) {
	bindings := strings.Split(portStr, ":")
	if len(bindings) != 2 {
		return Binding{}, fmt.Errorf("could not parse port string as port binding: %v", portStr)
	}
	return Binding{
		Container: asNatPort(bindings[0]),
		Host:      bindings[1],
	}, nil
}

func asNatPort(binding string) nat.Port {
	if strings.Contains(binding, "/tcp") || strings.Contains(binding, "/udp") {
		return nat.Port(binding)
	}
	return nat.Port(binding) + "/tcp"
}

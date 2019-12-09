package main

import (
	"bytes"
	"context"
	"fmt"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/strslice"
	"github.com/docker/docker/client"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"os"
)

type Definition struct {
	Services map[string]Service
}

type Service struct {
	Image string
	Entrypoint string
	Env []string
}

func (s *Service) GetEntrypoint() strslice.StrSlice {
	if s.Entrypoint == "" {
		return nil
	}
	return strslice.StrSlice{ s.Entrypoint }
}

func main() {
	if len(os.Args) < 2 {
		help()
		return
	}

	switch os.Args[1] {
	case "start":
		if err := start(); err != nil {
			fmt.Println(err)
		}
	case "ps":
		if err := ps(); err != nil {
			fmt.Println(err)
		}
	default:
		help()
	}
}

func help() {
	fmt.Println(`Please specify one of the following commands:
	start - (start the containers and executables specified in config)
	ps    - (list all running containers and executables specified in config)
`)
}

func ps() error {
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		return err
	}
	return printRunningContainers(cli)
}

func printRunningContainers(cli *client.Client) error {
	containers, err := cli.ContainerList(context.Background(), types.ContainerListOptions{})
	if err != nil {
		return err
	}
	fmt.Println(createPrintFormatString(containers))
	return nil
}

func createPrintFormatString(containers []types.Container) string {
	buffer := bytes.NewBufferString(fmt.Sprintf("\r%15s | %30s | %10s\n", "Id", "Name", "Status"))
	buffer.WriteString("-------------------------------------------------------------------------\n")
	for _, c := range containers {
		buffer.WriteString(fmt.Sprintf("%15s | %30s | %10s\n", c.ID[:10], shortened(c.Image), c.Status))
	}
	return buffer.String()
}

func shortened(str string) string {
	if len(str) < 30 {
		return str
	}
	return "..." + str[len(str)-27:]
}

func start() error {
	data, err := ioutil.ReadFile("config.yaml")
	if err != nil {
		return err
	}

	var definition Definition
	if err := yaml.Unmarshal(data, &definition); err != nil {
		return err
	}

	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		return err
	}

	netdriver, err := cli.NetworkCreate(context.Background(), "gompose-network", types.NetworkCreate{
		CheckDuplicate: true,
		Attachable:     true,
	})
	if err != nil {
		return err
	}

	for name, service := range definition.Services {
		c, err := cli.ContainerCreate(
			context.Background(),
			&container.Config{
				Hostname:        name,
				Image:           service.Image,
				Env: service.Env,
				Entrypoint: service.GetEntrypoint(),

			},
			&container.HostConfig{
				Mounts: []mount.Mount{
					{
						Target: "",
						Source: "",
						Type: "volume",
						ReadOnly: false,
					},
				},
			},
			nil,
			name,
		)
		if err != nil {
			return err
		}
		if err := cli.NetworkConnect(context.Background(), netdriver.ID, c.ID, nil); err != nil {
			return err
		}
		if err := cli.ContainerStart(context.Background(), c.ID, types.ContainerStartOptions{}); err != nil {
			return err
		}
	}
	return printRunningContainers(cli)
}

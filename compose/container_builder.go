package compose

import (
	"context"
	"fmt"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
)

type ContainerBuilder struct {
	err        error
	ctx        context.Context
	config     *container.Config
	hostconfig *container.HostConfig
	name       string
}

func NewContainerBuilder(name string) ContainerBuilder {
	return ContainerBuilder{
		ctx:        context.Background(),
		config:     &container.Config{},
		hostconfig: &container.HostConfig{},
		name:       name,
	}
}

func (builder ContainerBuilder) SetConfig(service Service) ContainerBuilder {
	builder.config = &container.Config{
		Hostname:   builder.name,
		Image:      service.Image,
		Env:        service.Env,
		Entrypoint: service.GetEntrypoint(),
	}
	return builder
}

func (builder ContainerBuilder) AddRestartPolicy(service Service) ContainerBuilder {
	if 	service.RestartPolicy.Condition == "" {
		return builder
	}
	builder.hostconfig.RestartPolicy = service.RestartPolicy.ToDockerPolicy()
	return builder
}

func (builder ContainerBuilder) AddVolumes(service Service, volumes map[string]string) ContainerBuilder {
	mounts, err := parseVolumes(service, volumes)
	if err != nil {
		builder.err = err
		return builder
	}
	builder.hostconfig.Mounts = append(builder.hostconfig.Mounts, mounts...)
	return builder
}

func (builder ContainerBuilder) AddPortBindings(service Service) ContainerBuilder {
	if builder.err != nil {
		return builder
	}
	binds, err := service.GetPortBindings()
	if err != nil {
		builder.err = err
		return builder
	}
	builder.hostconfig.PortBindings = binds
	return builder
}

func parseVolumes(service Service, volumes map[string]string) ([]mount.Mount, error) {
	var mounts []mount.Mount
	for _, v := range service.Volumes {
		vol, err := NewVolume(v)
		if err != nil {
			return nil, err
		}
		source, ok := volumes[vol.Source]
		if !ok {
			return nil, fmt.Errorf("could not find volume source in parsed Volumes: %v", vol.Source)
		}

		mounts = append(mounts, mount.Mount{
			Target:   vol.Target,
			Source:   source,
			Type:     "volume",
			ReadOnly: vol.ReadOnly,
		})
	}
	return mounts, nil
}

func (builder ContainerBuilder) Build(cli *client.Client) (container.ContainerCreateCreatedBody, error) {
	return cli.ContainerCreate(builder.ctx, builder.config, builder.hostconfig, nil, builder.name)
}

func isReadOnly(vol []string) bool {
	if len(vol) == 3 {
		return vol[2] == "ro"
	}
	return false
}

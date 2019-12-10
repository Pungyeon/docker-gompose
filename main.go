package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/strslice"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

func HandleErrors(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

type Definition struct {
	Services map[string]Service
}

type Service struct {
	Image string
	Entrypoint string
	Env []string
	Volumes []string
	Driver string
	Command string
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

	app, err := NewApp()
	if err != nil {
		panic(err)
	}
	if err := app.Run(os.Args[1]); err != nil {
		log.Println(err)
	}
	if err := app.Save(); err != nil {
		log.Println(err)
	}
}

type Driver int64

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

const (
	UNKNOWN_DRIVER Driver = 0
	DOCKER Driver = 1
	EXEC Driver = 2

	UNKNOWN_STATUS Status = 0
	RUNNING Status = 1
	STOPPED Status = 2
)

type Process struct {
	ID string
	Driver
	Status
}

type Volume struct {
	Source string
	Target string
	ReadOnly bool
}

func NewVolume(volumeString string) (Volume, error) {
	paths := strings.Split(volumeString, ":")
	if len(paths) < 2 || len(paths) > 3 {
		return Volume{}, fmt.Errorf("invalid volume path: %v", volumeString)
	}
	return Volume{
		Source: paths[0],
		Target: paths[1],
		ReadOnly: isReadOnly(paths),
	}, nil
}

type App struct {
	cli        *client.Client
	Volumes    map[string]string
	NetworkID string
	Containers map[string]Process
	Processes map[string]Process
}

func NewApp() (*App, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		return &App{}, err
	}
	return loadLockFile(cli)
}

func loadLockFile(cli *client.Client) (*App, error) {
	lock, err := ioutil.ReadFile(".gompose.lock")
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("no lock file found, creating new environment")
			return &App{
				cli:        cli,
				Volumes:    map[string]string{},
				Containers: map[string]Process{},
				Processes: map[string]Process{},
			}, nil
		}
		return &App{}, err
	}
	var app *App
	if err := json.Unmarshal(lock, &app); err != nil {
		return &App{}, err
	}
	app.cli = cli
	return app, nil
}

func (app *App) Save() error {
	data, err := json.Marshal(app)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(".gompose.lock", data, 0700)
}

func (app *App) Run(cmd string) error {
	switch cmd {
	case "start":
		return app.start()
	case "ps":
		return app.ps()
	case "clean", "rm":
		app.clean()
		return nil
	case "stop":
		return app.stop()
	default:
		help()
		return nil
	}
}

func help() {
	fmt.Println(`Please specify one of the following commands:
	start - (start the Containers and executables specified in config)
	ps    - (list all running Containers and executables specified in config)
	rm - (clean all networks, Volumes and Containers)
	stop - (stop all running containers)
`)
}

func (app *App) stop() error {
	duration := time.Second * 15
	for name, proc := range app.Containers {
		if err := app.cli.ContainerStop(context.Background(), proc.ID, &duration); err != nil {
			log.Println(err)
		} else {
			proc.Status = STOPPED
			app.Containers[name] = proc
		}
	}
	for name, proc := range app.Processes {
		fmt.Printf("\rStopping %s (PID: %s) [PENDING]", name, proc.ID)
		if err := exec.Command(fmt.Sprintf("kill %s", proc.ID)).Start(); err != nil {
			log.Println(err)
		} else {
			fmt.Printf("\rStopping %s (PID: %s) [STOPPED]\n", name, proc.ID)
		}
	}
	return nil
}

func (app *App) clean() {
	duration := time.Second*15
	for name, proc := range app.Containers {
		fmt.Printf("\rStopping %s [PENDING]", name)
		if err := app.cli.ContainerStop(context.Background(), proc.ID, &duration); err != nil {
			log.Println(err)
		}
		fmt.Printf("\rRemoving %s [STOPPED]", name)
		if err := app.cli.ContainerRemove(context.Background(), proc.ID, types.ContainerRemoveOptions{}); err != nil {
			log.Println(err)
		}
		fmt.Printf("\rRemoving %s [REMOVED]\n", name)
		delete(app.Containers, name)
	}

	if app.NetworkID != "" {
		fmt.Printf("\rRemoving Network: %s [PENDING]", app.NetworkID)
		if err := app.cli.NetworkRemove(context.Background(), app.NetworkID); err != nil {
			log.Println(err)
		}
		fmt.Printf("\rRemoving Network: %s [REMOVED]\n", app.NetworkID)
		app.NetworkID = ""
	}

	for name, id:= range app.Volumes {
		fmt.Printf("\rRemoving Volume: %s [PENDING]", id)
		if err := app.cli.VolumeRemove(context.Background(), id, false); err != nil {
			log.Println(err)
		}
		fmt.Printf("\rRemoving Volume: %s [REMOVED]\n", id)
		delete(app.Volumes, name)
	}
}

func (app *App) ps() error {
	containers, err := app.cli.ContainerList(context.Background(), types.ContainerListOptions{

	})
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

func (app *App) start() error {
	data, err := ioutil.ReadFile("config.yaml")
	if err != nil {
		return err
	}

	var definition Definition
	return HandleErrors(
		yaml.Unmarshal(data, &definition),
		app.createNetworks(),
		app.registerVolumes(definition.Services),
		app.createProcesses(definition.Services),
		app.ps(),
	)
}

func (app *App) createNetworks() error {
	log.Println("Creating network: gompose-network")
	netdriver, err := app.cli.NetworkCreate(context.Background(), "gompose-network", types.NetworkCreate{
		CheckDuplicate: true,
		Attachable:     true,
	})
	if err != nil {
		return err
	}
	app.NetworkID = netdriver.ID
	return nil
}

func (app *App) registerVolumes(services map[string]Service) error {
	for _, service := range services {
		for _, v := range service.Volumes {
			vol, err := NewVolume(v)
			if err != nil {
				return err
			}
			if _, ok := app.Volumes[vol.Source]; !ok {
				log.Printf("creating local volume: %s\n", vol.Source)
				v, err := app.cli.VolumeCreate(context.Background(), volume.VolumeCreateBody{
					Driver:     "local",
				})
				if err != nil {
					return err
				}
				app.Volumes[vol.Source] = v.Name
			}
		}
	}
	return nil
}

func logContainerStatus(name, status string, final bool) {
	fmt.Printf("\rCreating container: %30s [%s]", shortened(name), status)
	if final {
		fmt.Printf("\n")
	}
}

func (app *App) createProcesses(services map[string]Service) error {
	for name, service := range services {
		if DriverFromString(service.Driver) == EXEC {
			if err := app.createProcess(name, service); err != nil {
				return err
			}
		} else {
			if err := app.createContainer(name, service); err != nil {
				return err
			}
		}
		logContainerStatus(name, "RUNNING", true)
	}
	return nil
}

func (app *App) createProcess(name string, service Service) error {
	cmds := strings.Split(service.Command, " ")
	cmd := exec.Command(cmds[0], cmds[1:]...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not start process %s: %v, %v", cmd.Path, cmd.Args, err)
	}
	app.Processes[name] = Process{
		ID:     fmt.Sprintf("%d", cmd.Process.Pid),
		Driver: EXEC,
		Status: RUNNING,
	}
	return nil
}

func (app *App) createContainer(name string, service Service) error {
	logContainerStatus(name, "PENDING", false)
	c, err := NewContainerBuilder(name).
		SetConfig(service).
		AddVolumes(service, app.Volumes).
		Build(app.cli)
	if err != nil {
		return err
	}
	app.Containers[name] = Process{
		ID: c.ID,
		Driver: DOCKER,
	}
	logContainerStatus(name, "CREATED", false)

	if err := app.cli.NetworkConnect(context.Background(), app.NetworkID, c.ID, nil); err != nil {
		return fmt.Errorf("[container: %s, network: %s] network connect returned with status: %v",
			c.ID, app.NetworkID, err)
	}
	return HandleErrors(
		app.cli.ContainerStart(context.Background(), c.ID, types.ContainerStartOptions{}),
	)
}

type ContainerBuilder struct {
	err error
	ctx context.Context
	config *container.Config
	hostconfig *container.HostConfig
	name string
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
		Hostname:        builder.name,
		Image:           service.Image,
		Env: service.Env,
		Entrypoint: service.GetEntrypoint(),
	}
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
			Target: vol.Target,
			Source: source,
			Type: "volume",
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

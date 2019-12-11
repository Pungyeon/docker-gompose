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
	"github.com/docker/go-connections/nat"
	"github.com/docker/docker/client"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

func returnError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func logErrors(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			log.Println(err)
		}
	}
	return nil
}

func HandleErrors(f func(...error) error, errs ...error) error {
	return f(errs...)
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
	OnStop string `yaml:"on_stop"`
	Ports []string
	DependsOn []string `yaml:"depends_on"`
}

func (s *Service) GetEntrypoint() strslice.StrSlice {
	if s.Entrypoint == "" {
		return nil
	}
	return strslice.StrSlice{ s.Entrypoint }
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
			HostIP: "0.0.0.0",
			HostPort: pb.Host,
		})
	}
	return portmap, nil
}

type Binding struct {
	Container nat.Port
	Host string
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

func main() {
	if len(os.Args) < 2 {
		help()
		return
	}

	app, err := NewApp()
	if err != nil {
		panic(err)
	}
	if err := HandleErrors(logErrors,
		app.Run(os.Args[1]),
		app.Wait(),
		app.Save(),
	); err != nil {
		log.Println(err)
	}
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
	OnStop string
	PID int
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

type Dependency struct {
	service string
	channel chan bool
}

func NewDependency(service string) Dependency {
	return Dependency{
		service: service,
		channel: make(chan bool),
	}
}

func (d Dependency) Wait() {
	<-d.channel
}

type App struct {
	cli         *client.Client
	dependants  chan Dependency
	serviceDone chan string
	wg sync.WaitGroup

	Volumes     map[string]string
	NetworkID   string
	Containers  map[string]Process
	Processes   map[string]Process
}

func NewApp() (*App, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		return &App{}, err
	}
	return loadLockFile(cli)
}

func (app *App) Wait() error {
	app.wg.Wait()
	return nil
}

func (app *App) AddDependency(d Dependency) {
	app.dependants <- d
}

func (app *App) Done(service string) {
	app.serviceDone <- service
}

func loadLockFile(cli *client.Client) (*App, error) {
	app, err := createAppFromLockFile(cli)
	if err != nil {
		return nil, err
	}
	dep, done := newDepedencyHandlers()
	app.cli = cli
	app.dependants = dep
	app.serviceDone = done
	return app, nil
}

func createAppFromLockFile(cli *client.Client) (*App, error) {
	lock, err := ioutil.ReadFile(".gompose.lock")
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("no lock file found, creating new environment")
			return &App{
				Volumes:    map[string]string{},
				Containers: map[string]Process{},
				Processes:  map[string]Process{},
				dependants: make(chan Dependency),
			}, nil
		}
		return nil, err
	}
	var app *App
	if err := json.Unmarshal(lock, &app); err != nil {
		return nil, err
	}
	return app, nil
}

func newDepedencyHandlers() (chan Dependency, chan string) {
	waiter := make(chan Dependency)
	doneService := make(chan string)
	go func() {
		var dependants []Dependency
		for {
			select {
			case d := <- waiter:
				fmt.Println("Received waiter:", d)
				dependants = append(dependants, d)
			case service := <- doneService:
				fmt.Println("service up:", service)
				for _, d := range dependants {
					if d.service == service {
						fmt.Println("sending service to dependant:", service)
						d.channel <- true
					}
				}
			}
		}
	}()
	return waiter, doneService
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
	app.stopProcesses()
	return nil
}

func (app *App) stopProcesses() {
	for name, proc := range app.Processes {
		fmt.Printf("\rStopping %s (PID: %s) [PENDING]", name, proc.ID)
		_, err := os.FindProcess(proc.PID)
		if err != nil {
			defer log.Println("NOTE: some errors were encountered:", err)
		} else {
			fmt.Printf("\rStopping %s (PID: %s) [STOPPED]\n", name, proc.ID)
			delete(app.Processes, name)
		}
		if proc.OnStop != "" {
			stop := strings.Split(proc.OnStop, " ")
			if err := exec.Command(stop[0], stop[1:]...).Start(); err != nil {
				log.Println(err)
			} else {
				fmt.Printf("\rStopping %s (PID: %s) [STOPPED]\n", name, proc.ID)
				delete(app.Processes, name)
			}
		}
	}
}

func (app *App) clean() {
	app.stopProcesses()

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
	fmt.Printf("\n")
	buffer := bytes.NewBufferString(fmt.Sprintf("\r%15s | %15s | %10s | %10s\n", "Id", "Name", "Driver", "Status"))
	buffer.WriteString("-------------------------------------------------------------------------\n")
	writeProcessPS(buffer, app.Containers)
	writeProcessPS(buffer, app.Processes)
	fmt.Println(buffer.String())
	return nil
}

func writeProcessPS(buffer *bytes.Buffer, processes map[string]Process) {
	for name, proc := range processes {
		buffer.WriteString(fmt.Sprintf("%15s | %15s | %10s | %10s\n", limit(proc.ID, 10), name, proc.Driver, proc.Status))
	}
}

func limit(str string, length int) string {
	if len(str) < length {
		return str
	}
	return str[:length]
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
	return HandleErrors(returnError,
		yaml.Unmarshal(data, &definition),
		app.createNetworks(),
		app.registerVolumes(definition.Services),
		app.createProcesses(definition.Services),
		app.ps(),
	)
}

func (app *App) createNetworks() error {
	if app.NetworkID != "" {
		if _, err := app.cli.NetworkInspect(context.Background(), app.NetworkID, types.NetworkInspectOptions{}); err == nil {
			fmt.Println("Network already created: gompose-network")
			return nil
		}
	}
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
		if err := app.createServiceVolumes(service); err != nil {
			return err
		}
	}
	return nil
}

func (app *App) createServiceVolumes(service Service) error {
	for _, v := range service.Volumes {
		vol, err := NewVolume(v)
		if err != nil {
			return err
		}
		if err := app.createDockerVolume(vol); err != nil {
			return err
		}
	}
	return nil
}

func (app *App) createDockerVolume(vol Volume) error {
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
	return nil
}

func logContainerStatus(name, status string, final bool) {
	fmt.Printf("\rCreating container: %30s [%s]", shortened(name), status)
	if final {
		fmt.Printf("\n")
	}
}

func (app *App) createProcesses(services map[string]Service) error {
	app.wg.Add(len(services))
	for name, service := range services {
		start, err := app.createProcess(name, service)
		if err != nil {
			return err
		}
		if len(service.DependsOn) == 0 {
			app.invokeStart(start, name)
			continue
		}
		go func() {
			app.waitForDependencies(service, name)
			app.invokeStart(start, name)
		}()
	}
	return nil
}

func (app *App) waitForDependencies(service Service, name string) {
	for _, srv := range service.DependsOn {
		if !app.IsServiceRunning(srv) {
			dep := NewDependency(srv)
			app.AddDependency(dep)
			dep.Wait()
		}
	}
}

func (app *App) invokeStart(start func() error, name string) {
	app.serviceDone <- name
	if err := start(); err != nil {
		log.Println(err)
	}
	logContainerStatus(name, "RUNNING", true)
	app.wg.Done()
}

func (app *App) IsServiceRunning(service string) bool {
	s, ok := app.Containers[service]
	if ok && s.Status == RUNNING {
		return true
	}
	p, ok := app.Processes[service]
	if ok && p.Status == RUNNING {
		return true
	}
	return false
}

func (app *App) createProcess(name string, service Service) (func() error, error) {
	if DriverFromString(service.Driver) == EXEC {
		return app.createExecProcess(name, service)
	}
	return app.createContainer(name, service)
}

func (app *App) createExecProcess(name string, service Service) (func() error, error) {
	if _, ok := app.Processes[name]; ok {
		return nilfn, nil
	}
	cmds := strings.Split(service.Command, " ")
	cmd := exec.Command(cmds[0], cmds[1:]...)

	return func() error {
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("could not start process %s: %v, %v", cmd.Path, cmd.Args, err)
		}
		app.Processes[name] = Process{
			ID:     fmt.Sprintf("%d", cmd.Process.Pid),
			PID: cmd.Process.Pid,
			Driver: EXEC,
			Status: RUNNING,
			OnStop: service.OnStop,
		}
		return nil
	}, nil
}

func nilfn() error { return nil }

func (app *App) createContainer(name string, service Service) (func() error, error) {
	if proc, ok := app.Containers[name]; ok {
		if proc.Status != STOPPED {
			return nilfn, nil
		}
		return func() error {
			if err := app.cli.ContainerStart(context.Background(), proc.ID, types.ContainerStartOptions{}); err != nil {
				return err
			}
			proc.Status = RUNNING
			app.Containers[name] = proc
			return nil
		}, nil
	}
	return app.createNewContainer(name, service)
}

func (app *App) createNewContainer(name string, service Service) (func() error, error) {
	logContainerStatus(name, "PENDING", false)
	c, err := NewContainerBuilder(name).
		SetConfig(service).
		AddVolumes(service, app.Volumes).
		AddPortBindings(service).
		Build(app.cli)
	if err != nil {
		return nilfn, err
	}
	app.Containers[name] = Process{
		ID: c.ID,
		Driver: DOCKER,
		Status: RUNNING,
	}
	logContainerStatus(name, "CREATED", false)

	if err := app.cli.NetworkConnect(context.Background(), app.NetworkID, c.ID, nil); err != nil {
		return nilfn, fmt.Errorf("[container: %s, network: %s] network connect returned with status: %v",
			c.ID, app.NetworkID, err)
	}
	return func() error {
		return app.cli.ContainerStart(context.Background(), c.ID, types.ContainerStartOptions{})
	}, nil
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

func (builder ContainerBuilder) AddPortBindings(service Service) ContainerBuilder {
	if builder.err != nil {
		return builder
	}
	binds, err := service.GetPortBindings()
	if err != nil {
		builder.err = err
		return builder
	}
	fmt.Println(binds)
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

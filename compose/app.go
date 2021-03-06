package compose

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/Pungyeon/docker-gompose/utils"
	"github.com/corticph/go-logging/pkg/logging"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"gopkg.in/yaml.v2"
)

type Result struct {
	err    error
	output *bytes.Buffer
}

func NewResult() *Result {
	return &Result{
		err:    nil,
		output: &bytes.Buffer{},
	}
}

type App struct {
	cli         *client.Client
	dependants  chan Dependency
	serviceDone chan string
	wg          sync.WaitGroup

	Volumes    map[string]string
	NetworkID  string
	Containers map[string]Process
	Processes  map[string]Process
}

func NewApp() (*App, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		return &App{}, err
	}
	return loadLockFile(cli)
}

func (app *App) Monitor() {
	go func() {
		for {
			select {
			case <- time.Tick(time.Second*3):

				//if err := app.ps(os.Stdout); err != nil {
				//	logging.Err(err.Error())
				//}
			}
		}
	}()
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
			logging.Info("no lock file found, creating new environment")
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
			case d := <-waiter:
				dependants = append(dependants, d)
			case service := <-doneService:
				for _, d := range dependants {
					if d.service == service {
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
		return app.start(&bytes.Buffer{})
	case "ps":
		return app.ps(&bytes.Buffer{})
	case "clean", "rm":
		app.clean(&bytes.Buffer{})
		return nil
	case "stop":
		return app.stop()
	default:
		Help()
		return nil
	}
}

func (app *App) RunWithDefinition(cmd string, definition Definition, writer io.Writer) error {
	switch cmd {
	case "start":
		return app.startWithDefinition(definition, writer)
	case "ps":
		return app.ps(writer)
	case "clean", "rm":
		buffer := &bytes.Buffer{}
		app.clean(buffer)
		writer.Write(buffer.Bytes())
		return nil
	case "stop":
		return app.stop()
	default:
		Help()
		return nil
	}
}

func Help() {
	fmt.Println(`Docker Gompose v0.1 
	To interact with the Gompose server, please use the 'server' command and one of the following:
		start - (start an instance of the Gompose server)
		stop  - (stop the currently running instance of the Gompose server)

	Full example:
		#> gompose server start

	To use the cli interface for invoking commands on the server, please specify one of the following:
		start - (start the Containers and executables specified in config)
		ps  - (list all running Containers and executables specified in config)
		rm  - (clean all networks, Volumes and Containers)
		stop - (stop all running containers)

	Full example:
		#> gompose cli ps
`)
}

func (app *App) stop() error {
	for name, proc := range app.Containers {
		if err := app.stopContainer(name, proc); err != nil {
			log.Println(err)
		}
	}
	app.stopProcesses()
	return nil
}

func (app *App) stopContainer(name string, proc Process) error {
	duration := time.Second * 15
	if proc.StopSignal == "" {
		if err := app.cli.ContainerStop(context.Background(), proc.ID, &duration); err != nil {
			return err
		}
		return app.stopContainerInStore(name, proc)
	}
	if err := app.cli.ContainerKill(context.Background(), proc.ID, proc.StopSignal); err != nil {
		log.Println(err)
		if err := app.cli.ContainerKill(context.Background(), proc.ID, "SIGKILL"); err != nil {
			return err
		}
	}
	return app.stopContainerInStore(name, proc)
}

func (app *App) stopContainerInStore(name string, proc Process) error {
	proc.Status = STOPPED
	app.Containers[name] = proc
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

func (app *App) clean(writer io.Writer) {
	app.stopProcesses()
	for name, proc := range app.Containers {
		if err := app.stopContainer(name, proc); err != nil {
			log.Println(err)
		}
		fmt.Printf("\rRemoving %s [STOPPED]", name)
		if err := app.cli.ContainerRemove(context.Background(), proc.ID, types.ContainerRemoveOptions{}); err != nil {
			log.Println(err)
		}
		fmt.Printf("\rRemoving %s [REMOVED]\n", name)
		writer.Write([]byte(fmt.Sprintf("Removed %s [%v][%v]\n", name, proc.Driver, proc.ID)))
		delete(app.Containers, name)
	}

	if app.NetworkID != "" {
		fmt.Printf("\rRemoving Network: %s [PENDING]", app.NetworkID)
		if err := app.cli.NetworkRemove(context.Background(), app.NetworkID); err != nil {
			log.Println(err)
		}
		fmt.Printf("\rRemoving Network: %s [REMOVED]\n", app.NetworkID)
		writer.Write([]byte(fmt.Sprintf("Removing Network: %s [REMOVED]\n", app.NetworkID)))
		app.NetworkID = ""
	}

	for name, id := range app.Volumes {
		fmt.Printf("\rRemoving Volume: %s [PENDING]", id)
		if err := app.cli.VolumeRemove(context.Background(), id, false); err != nil {
			log.Println(err)
		}
		fmt.Printf("\rRemoving Volume: %s [REMOVED]\n", id)
		writer.Write([]byte(fmt.Sprintf("Removing Volume: %s [REMOVED]\n", name)))
		delete(app.Volumes, name)
	}
}

func (app *App) ps(writer io.Writer) error {
	fmt.Printf("\n")
	buffer := &bytes.Buffer{}
	buffer.WriteString(fmt.Sprintf("\r%15s | %15s | %10s | %10s\n", "Id", "Name", "Driver", "Status"))
	buffer.WriteString("-------------------------------------------------------------------------\n")
	writeProcessPS(buffer, app.Containers)
	writeProcessPS(buffer, app.Processes)
	fmt.Println(buffer.String())

	writer.Write(buffer.Bytes())
	return nil
}

func (app *App) start(writer io.Writer) error {
	data, err := ioutil.ReadFile("config.yaml")
	if err != nil {
		return err
	}

	var definition Definition
	return utils.HandleErrors(utils.ReturnError,
		yaml.Unmarshal(data, &definition),
		app.createNetworks(),
		app.registerVolumes(definition.Services),
		app.createProcesses(definition.Services),
		app.ps(writer),
	)
}

func (app *App) startWithDefinition(definition Definition, writer io.Writer) error {
	return utils.HandleErrors(utils.ReturnError,
		app.createNetworks(),
		app.registerVolumes(definition.Services),
		app.createProcesses(definition.Services),
		app.ps(writer),
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
			Driver: "local",
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
			app.wg.Done()
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
			ID:         fmt.Sprintf("%d", cmd.Process.Pid),
			PID:        cmd.Process.Pid,
			Driver:     EXEC,
			Status:     RUNNING,
			OnStop:     service.OnStop,
			StopSignal: service.StopSignal,
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
	builder := NewContainerBuilder(name).
		SetConfig(service).
		AddRestartPolicy(service).
		AddVolumes(service, app.Volumes).
		AddPortBindings(service)

	c, err := builder.Build(app.cli)
	if err != nil {
		if !strings.Contains(err.Error(), "No such image") {
			return nilfn, err
		}
		reader, err := app.cli.ImagePull(context.Background(), service.GetImage(), types.ImagePullOptions{})
		if err != nil {
			return nilfn, err
		}
		readToStdOut(reader)
		c, err = builder.Build(app.cli)
		if err != nil {
			fmt.Println("oh shit it's down here?")
			return nilfn, err
		}
	}
	app.Containers[name] = Process{
		ID:         c.ID,
		Driver:     DOCKER,
		Status:     RUNNING,
		StopSignal: service.StopSignal,
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

package server

import (
	"io/ioutil"
	"log"
	"net/http"
	"os"

	"github.com/Pungyeon/docker-gompose/compose"
	"github.com/Pungyeon/docker-gompose/utils"
	"gopkg.in/yaml.v2"
)

type Server struct {
	app *compose.App
}

func New() (*Server, error) {
	if err := os.Setenv("DOCKER_API_VERSION", "1.40"); err != nil {
		return nil, err
	}
	app, err := compose.NewApp()
	if err != nil {
		return nil, err
	}
	app.Monitor()

	return &Server{
		app: app,
	}, nil
}

func (server *Server) Start() error {
	http.HandleFunc("/config", server.config)
	return http.ListenAndServe(":8080", nil)
}

func (server *Server) config(w http.ResponseWriter, r *http.Request) {
	cmd := r.URL.Query().Get("cmd")
	definition, err := getDefinitionFromBody(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
	if err := utils.HandleErrors(utils.LogErrors,
		server.app.RunWithDefinition(cmd, definition, w),
		server.app.Wait(),
		server.app.Save(),
	); err != nil {
		log.Println(err)
	}
}

func getDefinitionFromBody(r *http.Request) (compose.Definition, error) {
	data, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return compose.Definition{}, err
	}
	if err := r.Body.Close(); err != nil {
		return compose.Definition{}, err
	}

	var definition compose.Definition
	if err := yaml.Unmarshal(data, &definition); err != nil {
		return compose.Definition{}, err
	}
	return definition, nil
}


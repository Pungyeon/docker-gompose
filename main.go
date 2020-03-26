package main

import (
	"bytes"
	"io/ioutil"
	"log"
	"net/http"
	"os"

	"github.com/Pungyeon/docker-gompose/compose"
	"github.com/Pungyeon/docker-gompose/server"
)

func main() {
	if len(os.Args) < 3 {
		compose.Help()
		return
	}

	switch os.Args[1] {
	case "server":
		switch os.Args[2] {
		case "start":
			srv, err := server.New()
			if err != nil {
				panic(err)
			}
			log.Println(srv.Start())
		default:
			compose.Help()
		}
	case "cli":
		data, err := ioutil.ReadFile("config.yaml")
		if err != nil {
			panic(err)
		}

		req, err := http.NewRequest("POST", "http://localhost:8080/config?cmd=" + os.Args[2], bytes.NewReader(data))
		if err != nil {
			panic(err)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			panic(err)
		}
		response, err := ioutil.ReadAll(res.Body)
		if err != nil {
			panic(err)
		}
		defer res.Body.Close()
		log.Println(string(response))
	default:
		compose.Help()
	}
}

package main

import (
	"github.com/Pungyeon/docker-gompose/compose"
	"github.com/Pungyeon/docker-gompose/utils"
	"log"
	"os"
)


func main() {
	if len(os.Args) < 2 {
		compose.Help()
		return
	}

	app, err := compose.NewApp()
	if err != nil {
		panic(err)
	}
	if err := utils.HandleErrors(utils.LogErrors,
		app.Run(os.Args[1]),
		app.Wait(),
		app.Save(),
	); err != nil {
		log.Println(err)
	}
}

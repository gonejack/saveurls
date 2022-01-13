package main

import (
	"log"
	"os"

	"github.com/gonejack/saveurls/saveurls"
)

func init() {
	log.SetOutput(os.Stdout)
}

func main() {
	cmd := saveurls.SaveURL{
		Options: saveurls.MustParseOptions(),
	}
	err := cmd.Run()
	if err != nil {
		log.Fatal(err)
	}
}

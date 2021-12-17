package main

import (
	"context"
	"flag"
	"os"

	"within.website/ln"
	"within.website/ln/opname"
)

var (
	configPath = flag.String("config", "./var/config.yaml", "hardcoded path to load")
)

func main() {
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = opname.With(ctx, "main")

	ln.Log(ctx, ln.Fmt("starting up"), ln.F{"config": *configPath})

	fin, err := os.Open(*configPath)
	if err != nil {
		ln.FatalErr(ctx, err)
	}
	defer fin.Close()

	config, err := ParseConfig(fin)
	if err != nil {
		ln.FatalErr(ctx, err)
	}

	err = config.Apply(ctx)
	if err != nil {
		ln.FatalErr(ctx, err)
	}
}

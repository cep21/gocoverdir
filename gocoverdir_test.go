package main

import (
	"flag"
	"fmt"
	"testing"
)

func noError(t *testing.T, err error) {
	if err != nil {
		panic(fmt.Sprintf("Saw error %s", err))
	}
}

func TestGocoverdir(t *testing.T) {
	m := gocoverdir{}
	fs := flag.NewFlagSet("testsetup", flag.PanicOnError)
	m.setupFlags(fs)
	noError(t, fs.Parse([]string{""}))
	fmt.Printf("%+v", m)
	noError(t, m.setup())
}

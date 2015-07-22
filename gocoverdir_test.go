package main

import (
	"testing"
	"github.com/stretchr/testify/assert"
	"flag"
	"fmt"
)

func TestGocoverdir(t *testing.T) {
	m := gocoverdir{}
	fs := flag.NewFlagSet("testsetup", flag.PanicOnError)
	m.setupFlags(fs)
	assert.NoError(t, fs.Parse([]string{""}))
	fmt.Printf("%+v", m)
	assert.NoError(t, m.setup())
}

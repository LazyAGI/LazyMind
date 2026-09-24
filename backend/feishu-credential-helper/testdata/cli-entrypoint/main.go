package main

import (
	"os"

	"github.com/larksuite/cli/cmd"
)

func main() { os.Exit(cmd.ExecuteWithOptions(cmd.WithoutPlugins(), cmd.WithoutServiceCommands())) }

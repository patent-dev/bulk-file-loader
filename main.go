package main

import (
	"embed"
	"os"

	cli "github.com/patent-dev/bulk-file-loader/cmd/bulk-file-loader"
)

//go:embed web/ui/dist/*
var webAssets embed.FS

func main() {
	cli.WebAssets = webAssets
	err := cli.Execute()
	if code := cli.ExitCode(err); code != 0 {
		os.Exit(code)
	}
}

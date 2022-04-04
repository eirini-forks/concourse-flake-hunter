package main

import (
	"os"

	"github.com/cloudfoundry/concourse-flake-hunter/commands"
	"github.com/cloudfoundry/concourse-flake-hunter/fly"
	"github.com/urfave/cli"
)

func main() {
	hunter := cli.NewApp()
	hunter.Name = "concourse-flake-hunter"
	hunter.Usage = "concourse-flake-hunter <global-options> <command> [command-options]"

	hunter.Flags = []cli.Flag{
		cli.StringFlag{
			Name:  "concourse-url, c",
			Usage: "Concourse URL to authenticate with",
		},
		cli.StringFlag{
			Name:  "username, u",
			Usage: "Username for basic auth",
		},
		cli.StringFlag{
			Name:  "password, p",
			Usage: "Password for basic auth",
		},
		cli.StringFlag{
			Name:  "team-name, n",
			Usage: "Password for basic auth",
		},
	}

	hunter.Commands = []cli.Command{
		commands.SearchCommand,
		commands.AggregateCommand,
	}

	hunter.Before = func(ctx *cli.Context) error {
		client := fly.NewClient(ctx.String("concourse-url"),
			ctx.String("username"),
			ctx.String("password"),
			ctx.String("team-name"),
		)
		if err := client.InitConcourseClient(); err != nil {
			println("Clould not init concourse client: ", err.Error())
			os.Exit(1)
		}
		ctx.App.Metadata["client"] = client

		return nil
	}

	_ = hunter.Run(os.Args)
}

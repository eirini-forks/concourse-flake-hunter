package commands

import (
	"fmt"
	"regexp"

	"github.com/cloudfoundry/concourse-flake-hunter/fly"
	"github.com/cloudfoundry/concourse-flake-hunter/hunter"
	"github.com/urfave/cli"
)

var SearchCommand = cli.Command{
	Name:        "search",
	Usage:       "search <arguments>",
	Description: "Searches for flakes",

	Flags: []cli.Flag{
		cli.BoolFlag{
			Name:  "show-one-offs",
			Usage: "If set one off failures will be reported as well",
		},
		cli.IntFlag{
			Name:  "max-age, m",
			Usage: "Lists builds that failed in the last n hours",
			Value: 0,
		},
	},

	Action: func(ctx *cli.Context) error {
		if ctx.Args().First() == "" {
			return cli.NewExitError("need to provide a pattern", 1)
		}

		client := ctx.App.Metadata["client"].(fly.Client)

		searcher := hunter.NewSearcher(client)
		spec := hunter.SearchSpec{
			Pattern: regexp.MustCompile(ctx.Args().First()),
		}

		if ctx.Bool("show-one-offs") {
			spec.ShowOneOffs = true
		}

		if ctx.Int("max-age") > 0 {
			spec.MaxAge = ctx.Int("max-age")
		}

		builds := searcher.Search(spec)

		fmt.Printf("+-------+%-32s+%s\n", "----------------------------------", "-----------------------------------------------------")
		fmt.Printf("| %-5s | %-32s | %s\n", "Ended", "Job", "Url")
		fmt.Printf("+-------+%-32s+%s\n", "----------------------------------", "-----------------------------------------------------")

		for build := range builds {
			fmt.Printf("| %-5s | %-32s | %s\n", timeSince(build.EndTime), build.PipelineName+"/"+build.JobName, build.ConcourseURL)
		}

		fmt.Printf("+-------+%-32s+%s\n", "----------------------------------", "-----------------------------------------------------")

		return nil
	},
}

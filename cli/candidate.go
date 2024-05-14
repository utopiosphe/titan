package cli

import (
	"fmt"

	"github.com/Filecoin-Titan/titan/api/types"
	"github.com/urfave/cli/v2"
)

var CandidateCmds = []*cli.Command{
	nodeInfoCmd,
	cacheStatCmd,
	progressCmd,
	keyCmds,
	configCmds,
	registerCmds,
	bindCmd,
}

var registerCmds = &cli.Command{
	Name:  "register",
	Usage: "register candidate to scheduler",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "locator-url",
			Usage: "--locator=https://titan-locator-domain/rpc/v0",
			Value: "",
		},
		&cli.StringFlag{
			Name:  "scheduler-url",
			Usage: "--locator=https://titan-locator-domain/rpc/v0",
			Value: "",
		},
		&cli.IntFlag{
			Name:  "node-type",
			Usage: "--node-type=2, 2:candidate,3:validator",
		},
		&cli.StringFlag{
			Name:  "code",
			Usage: "candidate register code",
			Value: "",
		},
	},
	Action: func(cctx *cli.Context) error {
		locatorURL := cctx.String("locator-url")
		if len(locatorURL) == 0 {
			return fmt.Errorf("--locator-url can not empty")
		}

		schedulerURL := cctx.String("scheduler-url")
		if len(schedulerURL) == 0 {
			return fmt.Errorf("--scheduler-url can not empty")
		}

		code := cctx.String("code")
		if len(code) == 0 {
			return fmt.Errorf("--code can not empty")
		}

		nodeType := cctx.Int("node-type")
		if nodeType != int(types.NodeCandidate) && nodeType != int(types.NodeValidator) {
			return fmt.Errorf("Must set --node-type=2 or --node-type=3")
		}
		_, lr, err := openRepoAndLock(cctx)
		if err != nil {
			return err
		}
		defer lr.Close() //nolint:errcheck  // ignore error

		return RegisterNodeWithScheduler(lr, schedulerURL, locatorURL, types.NodeCandidate, code)
	},
}

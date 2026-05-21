package wirectl

import (
	"flag"

	"github.com/ironcore-dev/wire/wirectl/cli/wirectl/apply"
	"github.com/ironcore-dev/wire/wirectl/cli/wirectl/render"
	"github.com/spf13/cobra"
	ctrl "sigs.k8s.io/controller-runtime"
)

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use: "wirectl",
	}

	cmd.AddCommand(
		render.Command(),
		apply.Command(),
	)

	goFlags := flag.NewFlagSet("", flag.ExitOnError)
	ctrl.RegisterFlags(goFlags)
	cmd.PersistentFlags().AddGoFlagSet(goFlags)

	return cmd
}

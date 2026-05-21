package apply

import (
	"context"
	"fmt"

	"github.com/ironcore-dev/wire/api/v1alpha1"
	"github.com/ironcore-dev/wire/wirectl"
	"github.com/ironcore-dev/wire/wirectl/api"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func Command() *cobra.Command {
	var (
		filename     string
		varsFilename string
	)

	cmd := &cobra.Command{
		Use: "apply",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			return Run(ctx, filename, varsFilename)
		},
	}

	cmd.Flags().StringVarP(&filename, "filename", "f", filename, "Filename containing a config to render.")
	cmd.Flags().StringVar(&varsFilename, "vars", varsFilename, "Filename containing variables.")
	_ = cmd.MarkFlagRequired("filename")

	return cmd
}

func Run(ctx context.Context, filename, varsFilename string) error {
	restCfg, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("getting rest config: %w", err)
	}

	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("adding to scheme: %w", err)
	}

	c, err := client.New(restCfg, client.Options{
		Scheme: scheme,
	})
	if err != nil {
		return fmt.Errorf("creating client: %w", err)
	}

	cfg, err := api.ReadAndOptionallyRenderConfig(filename, varsFilename, false)
	if err != nil {
		return fmt.Errorf("reading config: %w", err)
	}

	if err := wirectl.ApplyTopology(ctx, c, cfg); err != nil {
		return fmt.Errorf("applying topology: %w", err)
	}
	return nil
}

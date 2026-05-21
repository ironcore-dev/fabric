package render

import (
	"fmt"

	"github.com/ironcore-dev/wire/wirectl"
	"github.com/ironcore-dev/wire/wirectl/api"
	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"
)

func Command() *cobra.Command {
	var (
		filename     string
		varsFilename string
	)

	cmd := &cobra.Command{
		Use: "render",
		RunE: func(cmd *cobra.Command, args []string) error {
			return Run(filename, varsFilename)
		},
	}

	cmd.Flags().StringVarP(&filename, "filename", "f", filename, "Filename containing a config to render.")
	cmd.Flags().StringVar(&varsFilename, "vars", varsFilename, "Filename containing variables.")

	_ = cmd.MarkFlagRequired("filename")

	return cmd
}

func Run(filename, varsFilename string) error {
	cfg, err := api.ReadAndOptionallyRenderConfig(filename, varsFilename, false)
	if err != nil {
		return fmt.Errorf("reading config: %w", err)
	}

	cfgs, err := wirectl.RenderTopology(cfg)
	if err != nil {
		return fmt.Errorf("rendering config: %w", err)
	}

	for i, cfg := range cfgs {
		if i != 0 {
			fmt.Println("---")
		}

		data, err := yaml.Marshal(cfg)
		if err != nil {
			return fmt.Errorf("marshalling config: %w", err)
		}

		fmt.Println(string(data))
	}
	return nil
}

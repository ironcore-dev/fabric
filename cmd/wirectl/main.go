package main

import (
	"log/slog"
	"os"

	"github.com/go-logr/logr"
	"github.com/ironcore-dev/wire/wirectl/cli/wirectl"
	ctrl "sigs.k8s.io/controller-runtime"
)

func main() {
	ctx := ctrl.SetupSignalHandler()
	ctrl.SetLogger(logr.FromSlogHandler(slog.Default().Handler()))

	if err := wirectl.Command().ExecuteContext(ctx); err != nil {
		ctrl.Log.Error(err, "Running command")
		os.Exit(1)
	}
}
